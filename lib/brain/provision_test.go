package brain

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const testFP = "0ECF6AC06E9BB6C5B928F10B5D6885D83872C2F0"

func TestInitLocal_CreatesLocalBrain(t *testing.T) {
	root := filepath.Join(t.TempDir(), "scratch")

	if err := InitLocal(root, "scratch", testFP, nil); err != nil {
		t.Fatalf("InitLocal: %v", err)
	}

	// Manifest: single recipient, sync_substrate none, name set.
	m, err := Read(root)
	if err != nil {
		t.Fatalf("Read manifest: %v", err)
	}
	if m.Name != "scratch" {
		t.Errorf("Name = %q, want scratch", m.Name)
	}
	if len(m.Recipients) != 1 || m.Recipients[0] != testFP {
		t.Errorf("Recipients = %v, want [%s]", m.Recipients, testFP)
	}
	if m.SyncSubstrate != "none" {
		t.Errorf("SyncSubstrate = %q, want none", m.SyncSubstrate)
	}
	if m.Shared() {
		t.Errorf("Shared() = true; single-recipient brain must be private")
	}

	// go.mod wires nous as the substrate ancestor.
	gomod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	if !strings.Contains(string(gomod), "module local/scratch") {
		t.Errorf("go.mod missing module path:\n%s", gomod)
	}
	if !strings.Contains(string(gomod), "replace github.com/xianxu/nous => ../nous") {
		t.Errorf("go.mod missing nous replace directive:\n%s", gomod)
	}

	// Status: a local brain has no remote and no upstream.
	s, err := LoadStatus(root)
	if err != nil {
		t.Fatalf("LoadStatus: %v", err)
	}
	if s.OriginURL != "" {
		t.Errorf("OriginURL = %q, want empty (local brain has no remote)", s.OriginURL)
	}
	if s.HasUpstream {
		t.Errorf("HasUpstream = true, want false (local brain has no upstream)")
	}
	if s.LastCommit.Subject != "init: bootstrap local brain (scratch)" {
		t.Errorf("LastCommit.Subject = %q", s.LastCommit.Subject)
	}

	// Confirm git really has no remote configured.
	out, _ := exec.Command("git", "-C", root, "remote").Output()
	if strings.TrimSpace(string(out)) != "" {
		t.Errorf("git remote = %q, want none", out)
	}
}

func TestInitLocal_RefusesExistingPath(t *testing.T) {
	root := t.TempDir() // already exists
	if err := InitLocal(root, "x", testFP, nil); err == nil {
		t.Fatalf("InitLocal on existing path: want error, got nil")
	}
}

func TestInitLocal_RequiresRecipient(t *testing.T) {
	root := filepath.Join(t.TempDir(), "x")
	if err := InitLocal(root, "x", "", nil); err == nil {
		t.Fatalf("InitLocal with empty recipientFP: want error, got nil")
	}
}

func TestInitLocal_SubstrateCallbackOutputIsCommitted(t *testing.T) {
	root := filepath.Join(t.TempDir(), "scratch")
	setup := func() error {
		return os.WriteFile(filepath.Join(root, "SUBSTRATE.txt"), []byte("wired"), 0o644)
	}
	if err := InitLocal(root, "scratch", testFP, setup); err != nil {
		t.Fatalf("InitLocal: %v", err)
	}
	// The substrate file must be in the initial commit, not left dirty.
	out, err := exec.Command("git", "-C", root, "status", "--porcelain").Output()
	if err != nil {
		t.Fatalf("git status: %v", err)
	}
	if strings.TrimSpace(string(out)) != "" {
		t.Errorf("working tree dirty after InitLocal: %q (substrate output not committed)", out)
	}
	out, err = exec.Command("git", "-C", root, "ls-files", "SUBSTRATE.txt").Output()
	if err != nil || strings.TrimSpace(string(out)) == "" {
		t.Errorf("SUBSTRATE.txt not tracked in the initial commit")
	}
}

func TestInitLocal_SubstrateFailureAborts(t *testing.T) {
	root := filepath.Join(t.TempDir(), "scratch")
	boom := func() error { return os.ErrPermission }
	if err := InitLocal(root, "scratch", testFP, boom); err == nil {
		t.Fatalf("InitLocal: want error when substrate setup fails, got nil")
	}
}

func TestModuleSafe(t *testing.T) {
	cases := map[string]string{
		"scratch":       "scratch",
		"brain-family":  "brain-family",
		"my brain":      "my-brain",
		"weird/../name": "weird-..-name",
		"":              "brain",
		"---":           "brain",
	}
	for in, want := range cases {
		if got := moduleSafe(in); got != want {
			t.Errorf("moduleSafe(%q) = %q, want %q", in, got, want)
		}
	}
}
