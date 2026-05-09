package agent

import "testing"

// Sample colon-separated output from `gpg --with-keygrip --with-colons
// --list-keys <fp>` for a key with primary + one encryption subkey.
// Format is gnupg's stable DETAILS spec.
const sampleColonsOutput = `tru:t:1:1745001234:0:3:1:5
pub:u:4096:1:0123456789ABCDEF:1745000000:1902745600::u:::scESC::::::23::0:
fpr:::::::::ABCDEF0123456789ABCDEF0123456789ABCDEF01:
grp:::::::::1111111111111111111111111111111111111111:
uid:u::::1745000000::AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA::Test User <test@example.com>::::::::::0:
sub:u:4096:1:FEDCBA9876543210:1745000000:1902745600:::::e::::::23:
fpr:::::::::FEDCBA9876543210FEDCBA9876543210FEDCBA98:
grp:::::::::2222222222222222222222222222222222222222:
`

func TestParseColonsOutput_PrimaryFingerprint(t *testing.T) {
	id, err := parseColonsOutput(sampleColonsOutput)
	if err != nil {
		t.Fatalf("parseColonsOutput: %v", err)
	}
	want := "ABCDEF0123456789ABCDEF0123456789ABCDEF01"
	if id.Fingerprint != want {
		t.Errorf("Fingerprint = %q, want %q (subkey fpr should be ignored)", id.Fingerprint, want)
	}
}

func TestParseColonsOutput_AllKeygrips(t *testing.T) {
	id, err := parseColonsOutput(sampleColonsOutput)
	if err != nil {
		t.Fatalf("parseColonsOutput: %v", err)
	}
	if len(id.Keygrips) != 2 {
		t.Fatalf("Keygrips count = %d, want 2 (primary + 1 subkey)", len(id.Keygrips))
	}
	if id.Keygrips[0] != "1111111111111111111111111111111111111111" {
		t.Errorf("Keygrips[0] = %q, want primary keygrip", id.Keygrips[0])
	}
	if id.Keygrips[1] != "2222222222222222222222222222222222222222" {
		t.Errorf("Keygrips[1] = %q, want subkey keygrip", id.Keygrips[1])
	}
}

func TestParseColonsOutput_UID(t *testing.T) {
	id, err := parseColonsOutput(sampleColonsOutput)
	if err != nil {
		t.Fatalf("parseColonsOutput: %v", err)
	}
	want := "Test User <test@example.com>"
	if id.UID != want {
		t.Errorf("UID = %q, want %q", id.UID, want)
	}
}

func TestParseColonsOutput_Empty(t *testing.T) {
	id, err := parseColonsOutput("")
	if err != nil {
		t.Fatalf("parseColonsOutput on empty: %v", err)
	}
	if id.Fingerprint != "" {
		t.Errorf("Fingerprint on empty = %q, want empty", id.Fingerprint)
	}
	if len(id.Keygrips) != 0 {
		t.Errorf("Keygrips on empty len = %d, want 0", len(id.Keygrips))
	}
}
