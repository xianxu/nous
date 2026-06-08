package brainsync

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"
)

// mustWriteBrain creates dir/.brain/config.md with the given body
// wrapped in YAML frontmatter delimiters.
func mustWriteBrain(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".brain"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".brain", "config.md"), []byte("---\n"+body+"---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// mustInitGitRemote does a `git init` in dir and configures origin to
// the given URL. Used to simulate a brain that's been provisioned
// for github-mediated sync (has gcrypt:: remote) without actually
// pushing to GitHub.
func mustInitGitRemote(t *testing.T, dir, url string) {
	t.Helper()
	if err := exec.Command("git", "init", "-q", "-b", "main", dir).Run(); err != nil {
		t.Fatalf("git init %s: %v", dir, err)
	}
	if err := exec.Command("git", "-C", dir, "remote", "add", "origin", url).Run(); err != nil {
		t.Fatalf("git remote add origin in %s: %v", dir, err)
	}
}

// names returns the sorted basenames of a path list — order-independent
// comparison for the discovery results.
func names(paths []string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = filepath.Base(p)
	}
	sort.Strings(out)
	return out
}

// TestFindBrains exercises the nous#47 semantics: the daemon watches
// every brain whose policy is Active(). That now includes purely-local
// and plain-remote private brains (they get the autosave commit safety
// net); only a fully opted-out brain is excluded.
func TestFindBrains(t *testing.T) {
	root := t.TempDir()
	// Shared (2+ recipients) — watched (full sync).
	mustWriteBrain(t, filepath.Join(root, "shared-family"), "name: family\nrecipients: [FP1, FP2]\n")
	// Single recipient, no remote — now watched (commit-only safety net).
	mustWriteBrain(t, filepath.Join(root, "local-personal"), "name: personal\nrecipients: [FP1]\n")
	// Plain remote, single recipient, no opt-in — watched (commit-only).
	plainMirror := filepath.Join(root, "plain-mirror")
	mustWriteBrain(t, plainMirror, "name: mirror\nrecipients: [FP1]\n")
	mustInitGitRemote(t, plainMirror, "https://github.com/xianxu/plain-mirror.git")
	// gcrypt single recipient — watched (the nous#26 just-provisioned case).
	gcryptDir := filepath.Join(root, "gcrypt-brain")
	mustWriteBrain(t, gcryptDir, "name: gcryptb\nrecipients: [FP1]\n")
	mustInitGitRemote(t, gcryptDir, "gcrypt::ssh://git@github.com/xianxu/gcrypt-brain.git")
	// Plain dir without .brain/ — not a brain, skipped.
	if err := os.MkdirAll(filepath.Join(root, "code-repo"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Fully opted out: autosave off + no remote — NOT watched.
	mustWriteBrain(t, filepath.Join(root, "opted-out"), "name: out\nrecipients: [FP1]\nautosave: off\n")

	got, err := FindBrains([]string{root})
	if err != nil {
		t.Fatalf("FindBrains: %v", err)
	}
	want := []string{"gcrypt-brain", "local-personal", "plain-mirror", "shared-family"}
	if g := names(got); !equalStrings(g, want) {
		t.Errorf("FindBrains = %v, want %v", g, want)
	}
}

func TestFindBrains_PrivatePublishOptIn(t *testing.T) {
	// A plain-remote brain with `publish: on` is watched (it pushes).
	// Its opted-out sibling (autosave off, plain remote, no publish) is not.
	root := t.TempDir()
	pub := filepath.Join(root, "published")
	mustWriteBrain(t, pub, "name: published\nrecipients: [FP1]\npublish: on\n")
	mustInitGitRemote(t, pub, "https://github.com/xianxu/published.git")

	out := filepath.Join(root, "inert")
	mustWriteBrain(t, out, "name: inert\nrecipients: [FP1]\nautosave: off\n")
	mustInitGitRemote(t, out, "https://github.com/xianxu/inert.git")

	got, err := FindBrains([]string{root})
	if err != nil {
		t.Fatalf("FindBrains: %v", err)
	}
	if g := names(got); !equalStrings(g, []string{"published"}) {
		t.Errorf("FindBrains = %v, want [published]", g)
	}
}

func TestFindBrains_EmptyRoot(t *testing.T) {
	got, err := FindBrains([]string{t.TempDir()})
	if err != nil {
		t.Fatalf("FindBrains: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("want 0 brains in empty root, got %v", got)
	}
}

func TestFindBrains_BadRoot(t *testing.T) {
	_, err := FindBrains([]string{"/no/such/path"})
	if err == nil {
		t.Error("expected error for nonexistent root")
	}
}

func TestFindAllBrainsInWorkspace(t *testing.T) {
	root := t.TempDir()
	mustWriteBrain(t, filepath.Join(root, "shared-x"), "name: x\nrecipients: [FP1, FP2]\n")
	t.Setenv("WORKSPACE_ROOT", root)

	got, err := FindAllBrainsInWorkspace()
	if err != nil {
		t.Fatalf("FindAllBrainsInWorkspace: %v", err)
	}
	if len(got) != 1 || filepath.Base(got[0]) != "shared-x" {
		t.Errorf("got %v, want [shared-x]", got)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
