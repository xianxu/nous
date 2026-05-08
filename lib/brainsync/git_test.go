package brainsync

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunGit_StatusOnEmpty(t *testing.T) {
	repo := initRepoForTest(t)
	out, err := RunGit(repo, "status", "--porcelain")
	if err != nil {
		t.Fatalf("RunGit: %v", err)
	}
	if strings.TrimSpace(string(out)) != "" {
		t.Errorf("want empty status, got %q", out)
	}
}

func TestStatus_ListsModifiedFiles(t *testing.T) {
	repo := initRepoForTest(t)
	if err := os.WriteFile(filepath.Join(repo, "a.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Status(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "a.md" {
		t.Errorf("want [a.md], got %v", got)
	}
}

// initRepoWithBareRemote creates a bare remote and a clone with origin set.
func initRepoWithBareRemote(t *testing.T) (workTree, bare string) {
	t.Helper()
	bare = t.TempDir()
	if out, err := exec.Command("git", "-C", bare, "init", "--bare", "-q", "-b", "main").CombinedOutput(); err != nil {
		t.Fatalf("init bare: %v: %s", err, out)
	}
	workTree = initRepoForTest(t)
	if out, err := exec.Command("git", "-C", workTree, "remote", "add", "origin", bare).CombinedOutput(); err != nil {
		t.Fatalf("add remote: %v: %s", err, out)
	}
	return
}

func TestAddCommitPush_HappyPath(t *testing.T) {
	work, bare := initRepoWithBareRemote(t)
	if err := os.WriteFile(filepath.Join(work, "a.md"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := AddCommitPush(work, "test commit"); err != nil {
		t.Fatalf("AddCommitPush: %v", err)
	}
	out, err := exec.Command("git", "-C", bare, "log", "--oneline").CombinedOutput()
	if err != nil {
		t.Fatalf("verify log: %v", err)
	}
	if !strings.Contains(string(out), "test commit") {
		t.Errorf("commit didn't reach bare; log: %s", out)
	}
}

func TestAddCommitPush_NoChanges(t *testing.T) {
	work, _ := initRepoWithBareRemote(t)
	if err := AddCommitPush(work, "should be skipped"); err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}

func TestCleanWorkingTree(t *testing.T) {
	repo := initRepoForTest(t)
	clean, err := CleanWorkingTree(repo)
	if err != nil || !clean {
		t.Errorf("fresh repo should be clean: clean=%v err=%v", clean, err)
	}
	if err := os.WriteFile(filepath.Join(repo, "a.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	clean, err = CleanWorkingTree(repo)
	if err != nil || clean {
		t.Errorf("dirty repo should be unclean: clean=%v err=%v", clean, err)
	}
}
