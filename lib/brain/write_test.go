package brain

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteManifest_RoundTripsThroughRead(t *testing.T) {
	root := t.TempDir()
	in := Manifest{
		Name:          "family",
		Recipients:    []string{"FP_BOB", "FP_ALICE"},
		SyncSubstrate: "syncthing",
	}
	if err := WriteManifest(root, in); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}
	out, err := Read(root)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if out.Name != in.Name {
		t.Errorf("Name = %q, want %q", out.Name, in.Name)
	}
	if out.SyncSubstrate != in.SyncSubstrate {
		t.Errorf("SyncSubstrate = %q, want %q", out.SyncSubstrate, in.SyncSubstrate)
	}
	// Recipients are sorted on write.
	want := []string{"FP_ALICE", "FP_BOB"}
	if len(out.Recipients) != len(want) {
		t.Fatalf("Recipients len = %d, want %d", len(out.Recipients), len(want))
	}
	for i := range want {
		if out.Recipients[i] != want[i] {
			t.Errorf("Recipients[%d] = %q, want %q", i, out.Recipients[i], want[i])
		}
	}
	if !out.Shared() {
		t.Errorf("Shared() = false; expected true (multi-recipient)")
	}
	if out.LegacyMode != "" {
		t.Errorf("LegacyMode = %q after WriteManifest; want empty (we don't write the field)", out.LegacyMode)
	}
}

func TestWriteManifest_NoModeFieldEmitted(t *testing.T) {
	root := t.TempDir()
	if err := WriteManifest(root, Manifest{Name: "personal", Recipients: []string{"FP1"}}); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}
	body, err := readFile(filepath.Join(root, ".brain", "config.md"))
	if err != nil {
		t.Fatalf("readFile: %v", err)
	}
	if strings.Contains(body, "mode:") {
		t.Errorf("WriteManifest emitted mode: field — must be dropped per M4c.\nManifest:\n%s", body)
	}
}

func TestWriteManifest_AtomicReplace(t *testing.T) {
	// Write, then write again with different content; final state
	// reflects the second write, no .tmp left over.
	root := t.TempDir()
	if err := WriteManifest(root, Manifest{Name: "v1", Recipients: []string{"FP1"}}); err != nil {
		t.Fatal(err)
	}
	if err := WriteManifest(root, Manifest{Name: "v2", Recipients: []string{"FP1"}}); err != nil {
		t.Fatal(err)
	}
	m, err := Read(root)
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "v2" {
		t.Errorf("Name = %q after rewrite, want v2", m.Name)
	}
	// .tmp must not linger after a successful rename.
	matches, _ := filepath.Glob(filepath.Join(root, ".brain", "*.tmp"))
	if len(matches) != 0 {
		t.Errorf("found %d stray .tmp files: %v", len(matches), matches)
	}
}

func TestSetGcryptParticipants_RoundTrip(t *testing.T) {
	repo := t.TempDir()
	if err := exec.Command("git", "-C", repo, "init", "-q").Run(); err != nil {
		t.Fatalf("git init: %v", err)
	}
	// gcrypt-participants is keyed off remote.origin, but git lets us
	// set the config without the remote actually existing. Good — we
	// don't want this lib coupled to a real gcrypt endpoint for
	// testing.
	want := []string{"FP_C", "FP_A", "FP_B"}
	if err := SetGcryptParticipants(repo, want); err != nil {
		t.Fatalf("SetGcryptParticipants: %v", err)
	}
	got, err := ReadGcryptParticipants(repo)
	if err != nil {
		t.Fatalf("ReadGcryptParticipants: %v", err)
	}
	// Sorted on write.
	wantSorted := []string{"FP_A", "FP_B", "FP_C"}
	if len(got) != len(wantSorted) {
		t.Fatalf("got %v, want %v", got, wantSorted)
	}
	for i := range wantSorted {
		if got[i] != wantSorted[i] {
			t.Errorf("[%d] = %q, want %q", i, got[i], wantSorted[i])
		}
	}
}

func TestSetGcryptParticipants_RejectsEmpty(t *testing.T) {
	repo := t.TempDir()
	if err := exec.Command("git", "-C", repo, "init", "-q").Run(); err != nil {
		t.Fatal(err)
	}
	if err := SetGcryptParticipants(repo, nil); err == nil {
		t.Errorf("SetGcryptParticipants(nil) should error — gcrypt rejects empty list")
	}
}

func TestReadGcryptParticipants_UnsetReturnsEmpty(t *testing.T) {
	repo := t.TempDir()
	if err := exec.Command("git", "-C", repo, "init", "-q").Run(); err != nil {
		t.Fatal(err)
	}
	got, err := ReadGcryptParticipants(repo)
	if err != nil {
		t.Errorf("ReadGcryptParticipants on unset key should not error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ReadGcryptParticipants on unset key = %v, want empty", got)
	}
}

func readFile(path string) (string, error) {
	out, err := exec.Command("cat", path).Output()
	return string(out), err
}
