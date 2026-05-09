package runtime

import (
	"path/filepath"
	"testing"
)

func TestRuntime_RoundTrip(t *testing.T) {
	t.Setenv("CHARON_RUNTIME_PATH", filepath.Join(t.TempDir(), "rt.json"))

	if got, err := Read(); err != nil || got != nil {
		t.Fatalf("expected nil/nil for missing file, got %v / %v", got, err)
	}

	if err := Write("127.0.0.1:9000"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil after write")
	}
	if got.Addr != "127.0.0.1:9000" {
		t.Errorf("addr = %q", got.Addr)
	}
	if got.PID == 0 {
		t.Error("PID should be non-zero")
	}
	if got.StartedAt.IsZero() {
		t.Error("StartedAt should be set")
	}
}

func TestRuntime_RemoveIdempotent(t *testing.T) {
	t.Setenv("CHARON_RUNTIME_PATH", filepath.Join(t.TempDir(), "rt.json"))
	// Removing a non-existent file is a no-op.
	if err := Remove(); err != nil {
		t.Errorf("Remove on missing file: %v", err)
	}
	// Write then remove twice — second remove is also a no-op.
	Write("127.0.0.1:8230")
	if err := Remove(); err != nil {
		t.Errorf("Remove (1st): %v", err)
	}
	if err := Remove(); err != nil {
		t.Errorf("Remove (2nd, missing): %v", err)
	}
	got, _ := Read()
	if got != nil {
		t.Errorf("expected file gone after Remove, got %+v", got)
	}
}

func TestRuntime_OverwriteOnSecondWrite(t *testing.T) {
	t.Setenv("CHARON_RUNTIME_PATH", filepath.Join(t.TempDir(), "rt.json"))
	Write("127.0.0.1:8230")
	Write("127.0.0.1:9000")
	got, err := Read()
	if err != nil || got == nil {
		t.Fatalf("Read: %v / %v", got, err)
	}
	if got.Addr != "127.0.0.1:9000" {
		t.Errorf("expected last writer wins, got addr=%q", got.Addr)
	}
}
