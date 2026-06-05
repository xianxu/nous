package brainsync

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xianxu/nous/lib/gh"
)

// makeBrainNoOrigin creates a minimal brain dir with manifest +
// initial commit but no `origin` remote configured. Used to
// exercise LeaveBrain's "no origin" refusal branch.
func makeBrainNoOrigin(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(root, ".brain"), 0o755))
	must(t, os.WriteFile(filepath.Join(root, ".brain", "config.md"),
		[]byte("---\nname: testbrain\nrecipients: [0ECF6AC06E9BB6C5B928F10B5D6885D83872C2F0]\n---\n"),
		0o644))
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "test@nous.local"},
		{"config", "user.name", "Test"},
		{"add", "."},
		{"commit", "-q", "-m", "seed"},
	} {
		c := exec.Command("git", append([]string{"-C", root}, args...)...)
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	return root
}

// makeBrainNonGithubOrigin sets origin to a file:// URL to exercise
// the GitHubOwnerRepo parse-fail branch.
func makeBrainNonGithubOrigin(t *testing.T) string {
	t.Helper()
	root := makeBrainNoOrigin(t)
	upstream := t.TempDir()
	c := exec.Command("git", "-C", upstream, "init", "--bare", "-q", "-b", "main")
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("init bare: %v: %s", err, out)
	}
	c = exec.Command("git", "-C", root, "remote", "add", "origin", upstream)
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("remote add: %v: %s", err, out)
	}
	return root
}

func TestLeaveBrain_RefusesNoOrigin(t *testing.T) {
	root := makeBrainNoOrigin(t)
	_, err := LeaveBrain(context.Background(), gh.New(gh.Conf{}), root, false)
	if err == nil {
		t.Fatal("expected error on brain with no origin, got nil")
	}
	if !strings.Contains(err.Error(), "no origin") && !strings.Contains(err.Error(), "origin remote") {
		t.Errorf("expected error about missing origin, got %v", err)
	}
}

func TestLeaveBrain_RefusesNonGithubOrigin(t *testing.T) {
	root := makeBrainNonGithubOrigin(t)
	_, err := LeaveBrain(context.Background(), gh.New(gh.Conf{}), root, false)
	if err == nil {
		t.Fatal("expected error on non-github origin, got nil")
	}
	// Either the parse-origin error (preferred) or a github-specific
	// downstream error. Both indicate the right branch fired.
	if !strings.Contains(err.Error(), "github") && !strings.Contains(err.Error(), "parse") {
		t.Errorf("expected github / parse error, got %v", err)
	}
}

func TestLeaveBrain_RefusesMissingBrain(t *testing.T) {
	_, err := LeaveBrain(context.Background(), gh.New(gh.Conf{}), "/nonexistent/brain/path", false)
	if err == nil {
		t.Fatal("expected error on nonexistent brain path, got nil")
	}
	if !strings.Contains(err.Error(), "manifest") {
		t.Errorf("expected manifest read error, got %v", err)
	}
}

func TestShortFpLast8(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"0ECF6AC06E9BB6C5B928F10B5D6885D83872C2F0", "3872c2f0"},
		{"abcdef", "abcdef"}, // shorter than 8, fall through to lower
		{"ABCDEF12", "abcdef12"},
	}
	for _, c := range cases {
		if got := shortFpLast8(c.in); got != c.want {
			t.Errorf("shortFpLast8(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
