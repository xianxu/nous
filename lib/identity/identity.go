// Package identity manages GPG keypairs that anchor a brain's recipient
// list: list local keyring entries, export armored public keys for
// sneakernet, inspect-and-import peer pubkeys with the verify-fingerprint
// ceremony.
//
// Sits one level above lib/agent/ in the lib-first hierarchy — agent is
// keygrip-focused (gpg-agent passphrase cache); identity is human-
// readable (fingerprints, UIDs, who-can-decrypt-what). cmd/nous/identity.go
// wraps these into the `nous identity` cluster.
//
// All operations shell out to the local gpg binary. No GnuPG library
// dependency; matches the substrate posture (gpg is the source of
// truth, we don't reimplement OpenPGP).
package identity

import (
	"bufio"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// Key is a GPG identity reduced to the fields nous cares about for
// recipient bookkeeping. The fingerprint is the canonical anchor; UID
// and Email are conveniences for human-readable display and the
// verify-fingerprint ceremony.
type Key struct {
	// Fingerprint is the 40-char uppercase hex of the primary key.
	// Same form gpg emits for `--with-colons` fpr lines.
	Fingerprint string

	// UID is the primary user ID string ("Name <email>" or "Name (comment) <email>").
	// Empty when the key has no UID, which shouldn't happen for normal
	// keys but is possible for raw subkeys.
	UID string

	// Email is the address extracted from UID's "<...>" delimiters.
	// Empty when UID has no angle-bracketed email.
	Email string

	// Secret is true when the local keyring holds the private key
	// material (i.e. this Key represents the operator's own identity,
	// not a peer's pubkey). Identity.List sets this; Inspect leaves it
	// false.
	Secret bool
}

// Last8 returns the last eight hex characters of the fingerprint, the
// form humans verify in the import ceremony. Lowercase to match the
// usual presentation in pgp.com / keys.openpgp.org.
func (k Key) Last8() string {
	if len(k.Fingerprint) < 8 {
		return strings.ToLower(k.Fingerprint)
	}
	return strings.ToLower(k.Fingerprint[len(k.Fingerprint)-8:])
}

// List enumerates secret keys in the local keyring (operator's own
// identities). Public-only keys (peer pubkeys imported for brain
// recipient lists) are NOT returned — see ListPublic for those.
//
// Secret-vs-public split matches the human mental model: `nous identity
// list` shows the operator's identities, separately from the peer keys
// admitted to brains.
func List() ([]Key, error) {
	out, err := exec.Command("gpg", "--with-colons", "--list-secret-keys").Output()
	if err != nil {
		return nil, fmt.Errorf("gpg --list-secret-keys: %w", err)
	}
	keys := parseList(string(out))
	for i := range keys {
		keys[i].Secret = true
	}
	return keys, nil
}

// ListPublic enumerates public-only keys — keys where we have the
// pubkey but not the secret material. These are peers admitted to one
// or more brains.
func ListPublic() ([]Key, error) {
	pub, err := exec.Command("gpg", "--with-colons", "--list-public-keys").Output()
	if err != nil {
		return nil, fmt.Errorf("gpg --list-public-keys: %w", err)
	}
	sec, err := exec.Command("gpg", "--with-colons", "--list-secret-keys").Output()
	if err != nil {
		return nil, fmt.Errorf("gpg --list-secret-keys: %w", err)
	}
	secretFps := make(map[string]bool)
	for _, k := range parseList(string(sec)) {
		secretFps[k.Fingerprint] = true
	}
	var out []Key
	for _, k := range parseList(string(pub)) {
		if !secretFps[k.Fingerprint] {
			out = append(out, k)
		}
	}
	return out, nil
}

// Export returns the armored public key for the given fingerprint.
// Suitable for piping into a peer's `nous identity import` over
// sneakernet.
func Export(fp string) (string, error) {
	out, err := exec.Command("gpg", "--armor", "--export", fp).Output()
	if err != nil {
		return "", fmt.Errorf("gpg --armor --export %s: %w", fp, err)
	}
	if len(out) == 0 {
		return "", fmt.Errorf("no public key found for fingerprint %s", fp)
	}
	return string(out), nil
}

// Inspect parses an armored public-key blob WITHOUT importing it.
// Returns the Key describing what's inside — fingerprint, UID, email —
// so the operator can verify before the import commits.
//
// Implements the verify-before-add half of the brain recipient
// ceremony: see what you're about to admit, type the last-8 fingerprint
// chars to confirm, then call Import.
func Inspect(armor string) (Key, error) {
	cmd := exec.Command("gpg", "--with-colons", "--import-options", "show-only", "--import")
	cmd.Stdin = strings.NewReader(armor)
	out, err := cmd.Output()
	if err != nil {
		return Key{}, fmt.Errorf("gpg --import show-only: %w", err)
	}
	keys := parseList(string(out))
	if len(keys) == 0 {
		return Key{}, fmt.Errorf("no key found in armored input")
	}
	return keys[0], nil
}

// Import commits an armored public key to the local keyring. Caller is
// expected to have run Inspect first and confirmed the fingerprint with
// the operator (the verify-fingerprint ceremony lives in cmd/nous, not
// here — this function does the mechanical import only).
//
// Idempotent: importing an already-known key is a no-op at gpg's level
// and Import surfaces the same Key either way.
func Import(armor string) (Key, error) {
	cmd := exec.Command("gpg", "--import")
	cmd.Stdin = strings.NewReader(armor)
	if err := cmd.Run(); err != nil {
		return Key{}, fmt.Errorf("gpg --import: %w", err)
	}
	return Inspect(armor)
}

// parseList parses gpg's `--with-colons` listing output (DETAILS format).
// Multiple keys are separated by `pub` (or `sec`) records; we collect
// each key's primary fpr and first uid line.
//
// Sibling of lib/agent.parseColonsOutput, but oriented around iterating
// many keys rather than one — so kept package-local rather than calling
// across packages.
func parseList(out string) []Key {
	var keys []Key
	var cur *Key

	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), ":")
		if len(fields) < 10 {
			continue
		}
		switch fields[0] {
		case "pub", "sec":
			// Boundary: flush previous key, start a new one.
			if cur != nil {
				keys = append(keys, *cur)
			}
			cur = &Key{}
		case "fpr":
			// First fpr after pub/sec is the primary key fingerprint.
			// Subkey fprs are ignored — we anchor to the primary.
			if cur != nil && cur.Fingerprint == "" {
				cur.Fingerprint = fields[9]
			}
		case "uid":
			if cur != nil && cur.UID == "" {
				cur.UID = fields[9]
				cur.Email = extractEmail(fields[9])
			}
		}
	}
	if cur != nil {
		keys = append(keys, *cur)
	}
	return keys
}

var emailRe = regexp.MustCompile(`<([^>]+)>`)

func extractEmail(uid string) string {
	m := emailRe.FindStringSubmatch(uid)
	if len(m) >= 2 {
		return m[1]
	}
	return ""
}
