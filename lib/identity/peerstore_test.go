package identity

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Redirect both $XDG_CONFIG_HOME (honored by os.UserConfigDir on Linux)
// and $HOME (so the macOS branch — $HOME/Library/Application Support —
// also resolves into the tempdir). Mirrors primary_test.go's helper.
// Returns the resolved peers/ dir.
func withTempStore(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("HOME", tmp)
	dir, err := PeerStorePath()
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestPeerStorePath_LandsUnderConfigDir(t *testing.T) {
	dir := withTempStore(t)
	if !filepath.IsAbs(dir) {
		t.Errorf("PeerStorePath() = %q, want absolute", dir)
	}
	if filepath.Base(dir) != "peers" {
		t.Errorf("PeerStorePath() base = %q, want 'peers'", filepath.Base(dir))
	}
}

func TestSaveLoadPeerMeta_Roundtrip(t *testing.T) {
	withTempStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	in := PeerMeta{
		Fingerprint: "ABCDEF0123456789ABCDEF0123456789ABCDEF01",
		GithubUser:  "emmatest42",
		ImportedAt:  now,
	}
	if err := SavePeerMeta(in); err != nil {
		t.Fatal(err)
	}
	out, err := LoadPeerMeta(in.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if out.Fingerprint != in.Fingerprint || out.GithubUser != in.GithubUser {
		t.Errorf("Load returned %+v, want fp+github of %+v", out, in)
	}
	if !out.ImportedAt.Equal(in.ImportedAt) {
		t.Errorf("ImportedAt: got %v, want %v", out.ImportedAt, in.ImportedAt)
	}
}

func TestSavePeerMeta_NormalizesFingerprintCase(t *testing.T) {
	withTempStore(t)
	// Write with lowercase, read with uppercase — should resolve to the
	// same record because the filename is canonicalized to uppercase.
	in := PeerMeta{Fingerprint: "abcdef0123", GithubUser: "x", ImportedAt: time.Now()}
	if err := SavePeerMeta(in); err != nil {
		t.Fatal(err)
	}
	out, err := LoadPeerMeta("ABCDEF0123")
	if err != nil {
		t.Fatalf("Load with uppercase fp: %v", err)
	}
	if out.GithubUser != "x" {
		t.Errorf("GithubUser = %q, want %q", out.GithubUser, "x")
	}
}

func TestLoadPeerMeta_MissingReturnsSentinel(t *testing.T) {
	withTempStore(t)
	_, err := LoadPeerMeta("0000000000000000000000000000000000000000")
	if !errors.Is(err, ErrPeerMetaNotFound) {
		t.Errorf("got %v, want ErrPeerMetaNotFound", err)
	}
}

func TestSavePeerMeta_RejectsEmptyFingerprint(t *testing.T) {
	withTempStore(t)
	err := SavePeerMeta(PeerMeta{GithubUser: "x"})
	if err == nil {
		t.Fatal("expected error on empty fingerprint")
	}
}

func TestListPeerMeta_EmptyDir(t *testing.T) {
	withTempStore(t)
	got, err := ListPeerMeta()
	if err != nil {
		t.Fatalf("ListPeerMeta on missing dir: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d entries, want 0", len(got))
	}
}

func TestListPeerMeta_ReturnsAllSidecars(t *testing.T) {
	withTempStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	for _, fp := range []string{"AAAA", "BBBB", "CCCC"} {
		if err := SavePeerMeta(PeerMeta{Fingerprint: fp, GithubUser: fp + "-user", ImportedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := ListPeerMeta()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Errorf("got %d entries, want 3", len(got))
	}
}

func TestListPeerMeta_SkipsNonJSONFiles(t *testing.T) {
	dir := withTempStore(t)
	// Save a valid sidecar to create the directory.
	if err := SavePeerMeta(PeerMeta{Fingerprint: "AAAA", GithubUser: "u", ImportedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	// Drop a stray file that isn't a sidecar.
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("notes"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ListPeerMeta()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Errorf("got %d entries, want 1 (non-json stray must be skipped)", len(got))
	}
}
