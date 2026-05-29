package main

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func gitInitRepo(t *testing.T, dir string) {
	t.Helper()
	if err := exec.Command("git", "-C", dir, "init", "-q").Run(); err != nil {
		t.Fatalf("git init: %v", err)
	}
}

func TestEnsureLocalForPublish_NoRemote(t *testing.T) {
	dir := t.TempDir()
	gitInitRepo(t, dir)
	if err := ensureLocalForPublish(dir); err != nil {
		t.Errorf("ensureLocalForPublish on a remoteless repo: want nil, got %v", err)
	}
}

func TestEnsureLocalForPublish_WithRemote(t *testing.T) {
	dir := t.TempDir()
	gitInitRepo(t, dir)
	if err := exec.Command("git", "-C", dir, "remote", "add", "origin",
		"gcrypt::ssh://git@github.com/me/already.git").Run(); err != nil {
		t.Fatalf("git remote add: %v", err)
	}
	err := ensureLocalForPublish(dir)
	if err == nil {
		t.Fatalf("ensureLocalForPublish on a published repo: want error, got nil")
	}
	if !strings.Contains(err.Error(), "already published") {
		t.Errorf("error should explain it's already published, got: %v", err)
	}
}

func TestResolvePublishTargetBrain_BrainFlag(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".brain"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := "---\nname: scratch\nrecipients: [\"AAA\"]\nsync_substrate: none\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(root, ".brain", "config.md"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := resolvePublishTargetBrain(io.Discard, nil, root)
	if err != nil {
		t.Fatalf("resolvePublishTargetBrain(--brain): %v", err)
	}
	if m.Name != "scratch" {
		t.Errorf("Name = %q, want scratch", m.Name)
	}
	if m.Path != root {
		t.Errorf("Path = %q, want %q", m.Path, root)
	}
}

func TestResolvePublishTargetBrain_BadPath(t *testing.T) {
	if _, err := resolvePublishTargetBrain(io.Discard, nil, t.TempDir()); err == nil {
		t.Fatalf("resolvePublishTargetBrain on a non-brain dir: want error, got nil")
	}
}

func TestShortFps(t *testing.T) {
	in := []string{"0ECF6AC06E9BB6C5B928F10B5D6885D83872C2F0", "ABCD"}
	got := shortFps(in)
	want := []string{"3872c2f0", "abcd"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("shortFps = %v, want %v", got, want)
	}
}

func TestOrPlaceholder(t *testing.T) {
	if orPlaceholder("", "x") != "x" {
		t.Error("empty should yield placeholder")
	}
	if orPlaceholder("me", "x") != "me" {
		t.Error("non-empty should yield itself")
	}
}
