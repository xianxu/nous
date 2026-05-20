package brain

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestVerified_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 5, 20, 14, 32, 0, 0, time.UTC)
	want := Verified{
		"yingtest42": {
			Fingerprint: "DC73FD263FBD8C5DA86A3D72F61E60BD8E7AB6E9",
			VerifiedBy:  "xianxu",
			VerifiedAt:  at,
		},
		"alice": {
			Fingerprint: "0ECF6AC06E9BB6C5B928F10B5D6885D83872C2F0",
			VerifiedBy:  "xianxu",
			VerifiedAt:  at.Add(time.Hour),
		},
	}
	if err := WriteVerified(dir, want); err != nil {
		t.Fatalf("WriteVerified: %v", err)
	}
	got, err := ReadVerified(dir)
	if err != nil {
		t.Fatalf("ReadVerified: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("entries: got %d, want %d", len(got), len(want))
	}
	for k, w := range want {
		g, ok := got[k]
		if !ok {
			t.Errorf("missing entry for %s", k)
			continue
		}
		if g.Fingerprint != w.Fingerprint {
			t.Errorf("%s fingerprint: got %s, want %s", k, g.Fingerprint, w.Fingerprint)
		}
		if g.VerifiedBy != w.VerifiedBy {
			t.Errorf("%s verified_by: got %s, want %s", k, g.VerifiedBy, w.VerifiedBy)
		}
		if !g.VerifiedAt.Equal(w.VerifiedAt) {
			t.Errorf("%s verified_at: got %v, want %v", k, g.VerifiedAt, w.VerifiedAt)
		}
	}
}

func TestVerified_ReadMissingReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	got, err := ReadVerified(dir)
	if err != nil {
		t.Fatalf("ReadVerified on missing file: %v", err)
	}
	if got == nil {
		t.Errorf("ReadVerified returned nil; want empty map")
	}
	if len(got) != 0 {
		t.Errorf("ReadVerified returned %d entries; want 0", len(got))
	}
}

func TestVerified_FingerprintCaseNormalized(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 5, 20, 14, 32, 0, 0, time.UTC)
	// Write with lowercase — should normalize to uppercase on read.
	if err := WriteVerified(dir, Verified{
		"alice": {
			Fingerprint: "0ecf6ac06e9bb6c5b928f10b5d6885d83872c2f0",
			VerifiedBy:  "xianxu",
			VerifiedAt:  at,
		},
	}); err != nil {
		t.Fatalf("WriteVerified: %v", err)
	}
	got, err := ReadVerified(dir)
	if err != nil {
		t.Fatalf("ReadVerified: %v", err)
	}
	if got["alice"].Fingerprint != "0ECF6AC06E9BB6C5B928F10B5D6885D83872C2F0" {
		t.Errorf("expected uppercase fingerprint, got %s", got["alice"].Fingerprint)
	}
}

func TestVerified_StableSortedOutput(t *testing.T) {
	// Two consecutive writes with the same data should produce
	// byte-identical files (no random map iteration showing through).
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	at := time.Date(2026, 5, 20, 14, 32, 0, 0, time.UTC)
	v := Verified{
		"zoe":   {Fingerprint: strings.Repeat("Z", 40), VerifiedBy: "x", VerifiedAt: at},
		"alice": {Fingerprint: strings.Repeat("A", 40), VerifiedBy: "x", VerifiedAt: at},
		"mike":  {Fingerprint: strings.Repeat("M", 40), VerifiedBy: "x", VerifiedAt: at},
	}
	if err := WriteVerified(dir1, v); err != nil {
		t.Fatalf("WriteVerified dir1: %v", err)
	}
	if err := WriteVerified(dir2, v); err != nil {
		t.Fatalf("WriteVerified dir2: %v", err)
	}
	read := func(d string) []byte {
		path := filepath.Join(d, ".brain", verifiedFilename)
		data := mustReadFile(t, path)
		return data
	}
	a := read(dir1)
	b := read(dir2)
	if string(a) != string(b) {
		t.Errorf("output not deterministic across writes:\n--- a ---\n%s\n--- b ---\n%s", a, b)
	}
	// And alice should appear before mike before zoe (alphabetic sort).
	s := string(a)
	ai := strings.Index(s, "alice")
	mi := strings.Index(s, "mike")
	zi := strings.Index(s, "zoe")
	if !(ai < mi && mi < zi) {
		t.Errorf("expected alphabetic sort; positions alice=%d mike=%d zoe=%d:\n%s", ai, mi, zi, s)
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}
