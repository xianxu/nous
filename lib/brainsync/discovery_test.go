package brainsync

import (
	"os"
	"os/exec"
	"path/filepath"
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

func TestFindSharedBrains(t *testing.T) {
	root := t.TempDir()
	// Shared = 2+ recipients (derived signal, replaced the old `mode:` field).
	mustWriteBrain(t, filepath.Join(root, "shared-family"), "name: family\nrecipients: [FP1, FP2]\n")
	mustWriteBrain(t, filepath.Join(root, "private-brain"), "name: personal\nrecipients: [FP1]\n")
	// Plain dir without .brain/ — must be skipped.
	if err := os.MkdirAll(filepath.Join(root, "code-repo"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := FindSharedBrains([]string{root})
	if err != nil {
		t.Fatalf("FindSharedBrains: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 shared brain, got %d: %v", len(got), got)
	}
	if filepath.Base(got[0]) != "shared-family" {
		t.Errorf("want shared-family, got %s", got[0])
	}
}

func TestFindSharedBrains_LegacyModeFieldIgnored(t *testing.T) {
	// Existing manifests with the old `mode:` field still parse; the
	// derived signal (recipients length) is what matters.
	root := t.TempDir()
	// Single recipient with `mode: shared` left over — NOT shared by
	// the derived rule, so the daemon shouldn't watch it.
	mustWriteBrain(t, filepath.Join(root, "stale-shared"), "mode: shared\nname: stale\nrecipients: [FP1]\n")
	// Two recipients with no `mode:` field at all — IS shared.
	mustWriteBrain(t, filepath.Join(root, "real-shared"), "name: real\nrecipients: [FP1, FP2]\n")

	got, err := FindSharedBrains([]string{root})
	if err != nil {
		t.Fatalf("FindSharedBrains: %v", err)
	}
	if len(got) != 1 || filepath.Base(got[0]) != "real-shared" {
		t.Errorf("got %v, want [real-shared] only", got)
	}
}

func TestFindSharedBrains_EmptyRoot(t *testing.T) {
	got, err := FindSharedBrains([]string{t.TempDir()})
	if err != nil {
		t.Fatalf("FindSharedBrains: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("want 0 brains in empty root, got %v", got)
	}
}

func TestFindSharedBrains_BadRoot(t *testing.T) {
	_, err := FindSharedBrains([]string{"/no/such/path"})
	if err == nil {
		t.Error("expected error for nonexistent root")
	}
}

// TestFindSharedBrains_SingleRecipientWithGcryptRemote captures the
// nous#26 bug: a brand-new brain that's just been provisioned (one
// recipient = the operator, gcrypt remote configured, invitation
// sent to a peer who hasn't been admitted yet) should be watched.
// Otherwise auto-admit never fires for it — chicken-and-egg.
func TestFindSharedBrains_SingleRecipientWithGcryptRemote(t *testing.T) {
	root := t.TempDir()
	// Single-recipient brain with a gcrypt:: remote = "shared-intent,
	// not yet admitted." Must be watched.
	dir := filepath.Join(root, "brain-family")
	mustWriteBrain(t, dir, "name: brain-family\nrecipients: [FP1]\n")
	mustInitGitRemote(t, dir, "gcrypt::ssh://git@github.com/xianxu/brain-family.git")

	// Single-recipient brain with NO remote = truly private, must be
	// skipped (no point watching; nothing to sync).
	priv := filepath.Join(root, "personal-brain")
	mustWriteBrain(t, priv, "name: personal\nrecipients: [FP1]\n")

	// Single-recipient brain with non-gcrypt remote (e.g., plain
	// github mirror) = not subject to our auto-admit flow. Skipped.
	nongcrypt := filepath.Join(root, "code-repo")
	mustWriteBrain(t, nongcrypt, "name: code\nrecipients: [FP1]\n")
	mustInitGitRemote(t, nongcrypt, "https://github.com/xianxu/code-repo.git")

	got, err := FindSharedBrains([]string{root})
	if err != nil {
		t.Fatalf("FindSharedBrains: %v", err)
	}
	if len(got) != 1 || filepath.Base(got[0]) != "brain-family" {
		t.Errorf("got %v, want [brain-family] only", got)
	}
}

func TestFindAllSharedBrainsInWorkspace(t *testing.T) {
	root := t.TempDir()
	mustWriteBrain(t, filepath.Join(root, "shared-x"), "name: x\nrecipients: [FP1, FP2]\n")
	t.Setenv("WORKSPACE_ROOT", root)

	got, err := FindAllSharedBrainsInWorkspace()
	if err != nil {
		t.Fatalf("FindAllSharedBrainsInWorkspace: %v", err)
	}
	if len(got) != 1 || filepath.Base(got[0]) != "shared-x" {
		t.Errorf("got %v, want [shared-x]", got)
	}
}
