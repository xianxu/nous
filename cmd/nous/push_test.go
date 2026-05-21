package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// pushBrainRig stands up a bare remote, a peer clone with a brain
// manifest, an initial committed file, and returns peer + bare paths.
// The manifest is minimal — just enough that EnclosingBrain resolves.
func pushBrainRig(t *testing.T) (peer, bare string) {
	t.Helper()
	bare = t.TempDir()
	mustRunGit(t, bare, "init", "--bare", "-q", "-b", "main")

	seed := t.TempDir()
	mustRunGit(t, seed, "clone", "-q", bare, ".")
	mustRunGit(t, seed, "config", "user.email", "test@nous.local")
	mustRunGit(t, seed, "config", "user.name", "Test")
	if err := os.MkdirAll(filepath.Join(seed, ".brain"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(seed, ".brain", "config.md"),
		[]byte("---\nname: testbrain\nrecipients: [0ECF6AC06E9BB6C5B928F10B5D6885D83872C2F0]\n---\n"),
		0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(seed, "paris.md"), []byte("Day 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRunGit(t, seed, "add", ".")
	mustRunGit(t, seed, "commit", "-q", "-m", "seed")
	mustRunGit(t, seed, "push", "-q", "origin", "main")

	peer = t.TempDir()
	mustRunGit(t, peer, "clone", "-q", bare, ".")
	mustRunGit(t, peer, "config", "user.email", "test@nous.local")
	mustRunGit(t, peer, "config", "user.name", "Test")
	return peer, bare
}

func mustRunGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	c := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func newPushCmdForTest(t *testing.T) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	c := newPushCmd()
	var buf bytes.Buffer
	c.SetOut(&buf)
	c.SetErr(&buf)
	return c, &buf
}

func runPushTest(t *testing.T, brainPath, msg string) string {
	t.Helper()
	cmd, buf := newPushCmdForTest(t)
	if err := runPushOnBrain(cmd, brainPath, msg); err != nil {
		t.Fatalf("runPushOnBrain: %v", err)
	}
	return buf.String()
}

func TestPush_CommitsAndPushesModifiedTracked(t *testing.T) {
	peer, bare := pushBrainRig(t)

	if err := os.WriteFile(filepath.Join(peer, "paris.md"), []byte("Day 1\nDay 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := runPushTest(t, peer, "")
	if !strings.Contains(out, "committed: autosave:") {
		t.Errorf("expected autosave commit in output, got:\n%s", out)
	}
	if !strings.Contains(out, "pushed ") {
		t.Errorf("expected push confirmation, got:\n%s", out)
	}

	// Bare remote should now carry the new commit.
	c := exec.Command("git", "-C", bare, "log", "main", "--format=%s")
	o, err := c.CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(o), "autosave:") {
		t.Errorf("bare didn't receive autosave commit:\n%s", o)
	}
}

func TestPush_UsesProvidedMessage(t *testing.T) {
	peer, bare := pushBrainRig(t)

	if err := os.WriteFile(filepath.Join(peer, "paris.md"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := runPushTest(t, peer, "finished tokyo draft")
	if !strings.Contains(out, "committed: finished tokyo draft") {
		t.Errorf("expected user message in commit confirmation, got:\n%s", out)
	}

	c := exec.Command("git", "-C", bare, "log", "main", "--format=%s")
	o, _ := c.CombinedOutput()
	if !strings.Contains(string(o), "finished tokyo draft") {
		t.Errorf("user message not on bare remote:\n%s", o)
	}
}

func TestPush_NothingToCommit_NothingToPush(t *testing.T) {
	peer, _ := pushBrainRig(t)

	out := runPushTest(t, peer, "")
	if !strings.Contains(out, "nothing to push") {
		t.Errorf("expected nothing-to-push notice, got:\n%s", out)
	}
}

func TestPush_NothingToCommit_MessageIgnored(t *testing.T) {
	peer, _ := pushBrainRig(t)

	out := runPushTest(t, peer, "labeled checkpoint")
	if !strings.Contains(out, "not used") {
		t.Errorf("expected hint that message wasn't used (no empty commits in v1), got:\n%s", out)
	}
}

func TestPush_HintsUntrackedFile(t *testing.T) {
	peer, _ := pushBrainRig(t)

	if err := os.WriteFile(filepath.Join(peer, "tokyo.md"), []byte("untracked\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := runPushTest(t, peer, "")
	if !strings.Contains(out, "hint:") || !strings.Contains(out, "tokyo.md") {
		t.Errorf("expected hint mentioning tokyo.md, got:\n%s", out)
	}
	if !strings.Contains(out, "git add") {
		t.Errorf("expected hint to mention `git add`, got:\n%s", out)
	}
}

func TestPush_HintsDeletedTracked(t *testing.T) {
	peer, _ := pushBrainRig(t)

	if err := os.Remove(filepath.Join(peer, "paris.md")); err != nil {
		t.Fatal(err)
	}

	out := runPushTest(t, peer, "")
	if !strings.Contains(out, "hint:") || !strings.Contains(out, "paris.md") {
		t.Errorf("expected hint mentioning paris.md deletion, got:\n%s", out)
	}
	if !strings.Contains(out, "git rm") {
		t.Errorf("expected hint to mention `git rm`, got:\n%s", out)
	}
}

func TestPush_RefusesDuringMerge(t *testing.T) {
	peer, _ := pushBrainRig(t)

	if err := os.WriteFile(filepath.Join(peer, ".git", "MERGE_HEAD"),
		[]byte("deadbeef\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd, _ := newPushCmdForTest(t)
	err := runPushOnBrain(cmd, peer, "")
	if err == nil || !strings.Contains(err.Error(), "MERGE_HEAD") {
		t.Errorf("expected refusal mentioning MERGE_HEAD, got err=%v", err)
	}
}
