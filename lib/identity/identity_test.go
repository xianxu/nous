package identity

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// shortTempHome makes a short-path tempdir and returns it. macOS unix
// sockets cap at 104 chars, and gpg-agent's socket path (GNUPGHOME +
// "/S.gpg-agent" + suffix) overflows that when GNUPGHOME comes from
// t.TempDir() (typically /var/folders/.../T/...). We use /tmp/ngpg-NNN
// which keeps headroom.
func shortTempHome(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "ngpg-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() {
		// Stop gpg-agent before removing — agent holds the socket open.
		exec.Command("gpgconf", "--homedir", dir, "--kill", "all").Run()
		os.RemoveAll(dir)
	})
	return dir
}

// setupGPGHome creates an isolated GNUPGHOME with a single test key
// and returns the key's fingerprint. GNUPGHOME is reverted by t.Setenv;
// the dir is cleaned in shortTempHome's t.Cleanup.
//
// gpg key generation is the slow part of these tests (~1-2s wall clock
// because of the entropy pool). Tests that don't need a real key
// should use parseList directly with synthetic colon output.
func setupGPGHome(t *testing.T) (homedir, fp string) {
	t.Helper()
	homedir = shortTempHome(t)
	t.Setenv("GNUPGHOME", homedir)
	// Tighten perms; gpg complains otherwise on first invocation.
	if err := os.Chmod(homedir, 0o700); err != nil {
		t.Fatalf("chmod GNUPGHOME: %v", err)
	}

	// Batch keygen with no passphrase for test speed. ed25519 + cv25519
	// matches scripts/identity.sh's algorithm; avoids the slow RSA
	// entropy gathering.
	batch := `%no-protection
Key-Type: eddsa
Key-Curve: ed25519
Subkey-Type: ecdh
Subkey-Curve: cv25519
Name-Real: Identity Test
Name-Email: test@example.com
Expire-Date: 0
%commit
`
	cmd := exec.Command("gpg", "--batch", "--generate-key")
	cmd.Stdin = strings.NewReader(batch)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("gpg --batch --generate-key: %v\n%s", err, out)
	}

	keys, err := List()
	if err != nil {
		t.Fatalf("List after keygen: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("expected 1 secret key after keygen, got %d", len(keys))
	}
	return homedir, keys[0].Fingerprint
}

func TestList_FreshHomeIsEmpty(t *testing.T) {
	homedir := shortTempHome(t)
	t.Setenv("GNUPGHOME", homedir)
	if err := os.Chmod(homedir, 0o700); err != nil {
		t.Fatalf("chmod GNUPGHOME: %v", err)
	}
	keys, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(keys) != 0 {
		t.Errorf("expected 0 keys in fresh GNUPGHOME, got %d", len(keys))
	}
}

func TestList_ReturnsGeneratedKey(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping gpg keygen in short mode")
	}
	_, fp := setupGPGHome(t)

	keys, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("expected 1 key, got %d", len(keys))
	}
	k := keys[0]
	if k.Fingerprint != fp {
		t.Errorf("Fingerprint = %q, want %q", k.Fingerprint, fp)
	}
	if k.Email != "test@example.com" {
		t.Errorf("Email = %q, want test@example.com", k.Email)
	}
	if !strings.Contains(k.UID, "Identity Test") {
		t.Errorf("UID = %q, expected to contain 'Identity Test'", k.UID)
	}
	if !k.Secret {
		t.Error("Secret = false, want true (List() returns own keys)")
	}
}

func TestExport_RoundtripsThroughInspect(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping gpg keygen in short mode")
	}
	_, fp := setupGPGHome(t)

	armor, err := Export(fp)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if !strings.Contains(armor, "-----BEGIN PGP PUBLIC KEY BLOCK-----") {
		t.Errorf("Export output missing armor header:\n%s", armor)
	}

	// Inspect against a *different* GNUPGHOME — so we know Inspect
	// doesn't lean on the local keyring.
	otherHome := shortTempHome(t)
	t.Setenv("GNUPGHOME", otherHome)
	if err := os.Chmod(otherHome, 0o700); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	k, err := Inspect(armor)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if k.Fingerprint != fp {
		t.Errorf("Inspect Fingerprint = %q, want %q", k.Fingerprint, fp)
	}
	if k.Email != "test@example.com" {
		t.Errorf("Inspect Email = %q, want test@example.com", k.Email)
	}
}

func TestImport_AddsKeyToKeyring(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping gpg keygen in short mode")
	}
	// Generate key in homedir A.
	_, fp := setupGPGHome(t)
	armor, err := Export(fp)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}

	// Switch to fresh homedir B; import the armored key there.
	otherHome := shortTempHome(t)
	t.Setenv("GNUPGHOME", otherHome)
	if err := os.Chmod(otherHome, 0o700); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	k, err := Import(armor)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if k.Fingerprint != fp {
		t.Errorf("Import Fingerprint = %q, want %q", k.Fingerprint, fp)
	}

	pub, err := ListPublic()
	if err != nil {
		t.Fatalf("ListPublic: %v", err)
	}
	found := false
	for _, p := range pub {
		if p.Fingerprint == fp {
			found = true
		}
	}
	if !found {
		t.Errorf("imported key %s not visible via ListPublic; got %d public-only keys", fp, len(pub))
	}
}

func TestLast8(t *testing.T) {
	tests := []struct {
		fp   string
		want string
	}{
		{"ABCDEF0123456789ABCDEF0123456789ABCDEF01", "abcdef01"},
		{"abcdef0123456789ABCDEF0123456789ABCDEF01", "abcdef01"},
		{"01", "01"},        // shorter than 8: return as-is, lowercased.
		{"", ""},
	}
	for _, tt := range tests {
		k := Key{Fingerprint: tt.fp}
		if got := k.Last8(); got != tt.want {
			t.Errorf("Last8(%q) = %q, want %q", tt.fp, got, tt.want)
		}
	}
}

func TestExtractEmail(t *testing.T) {
	tests := []struct {
		uid  string
		want string
	}{
		{"Alice <alice@example.com>", "alice@example.com"},
		{"Alice (work) <alice@example.com>", "alice@example.com"},
		{"Alice", ""},
		{"", ""},
		{"<a@b>", "a@b"},
	}
	for _, tt := range tests {
		if got := extractEmail(tt.uid); got != tt.want {
			t.Errorf("extractEmail(%q) = %q, want %q", tt.uid, got, tt.want)
		}
	}
}

func TestParseList_SkipsRecordsBefore10thField(t *testing.T) {
	// Defensive: gpg colons format spec says fields can be missing on
	// some record types. Make sure parseList tolerates short rows.
	short := "tru::1:1234567890:0:3:1:5\n"
	if got := parseList(short); len(got) != 0 {
		t.Errorf("expected 0 keys from short-row-only input, got %d", len(got))
	}
}

// TestList_SubkeyFprIsIgnored asserts that when a key has subkeys
// (which scripts/identity.sh always generates: signing primary + ECDH
// encryption subkey), parseList anchors to the primary fingerprint and
// doesn't double-count.
func TestList_SubkeyFprIgnored(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping gpg keygen in short mode")
	}
	_, fp := setupGPGHome(t)
	keys, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(keys) != 1 {
		t.Errorf("expected exactly 1 Key for a primary+subkey pair, got %d (subkey fprs leaked)", len(keys))
	}
	if keys[0].Fingerprint != fp {
		t.Errorf("primary fingerprint mis-anchored: got %q want %q", keys[0].Fingerprint, fp)
	}
	// Sanity: gpg should report at least 2 fpr lines (primary + subkey),
	// proving we actually had subkeys to ignore.
	out, _ := exec.Command("gpg", "--with-colons", "--list-secret-keys").Output()
	fprs := 0
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "fpr:") {
			fprs++
		}
	}
	if fprs < 2 {
		t.Errorf("test setup wrong: expected >= 2 fpr lines, got %d (no subkey?)", fprs)
	}
}
