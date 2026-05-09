package agent

import (
	"bufio"
	"fmt"
	"os/exec"
	"strings"
)

// DiscoverIdentity returns the Identity for a given fingerprint —
// fingerprint, primary UID, and all keygrips (primary + subkeys).
// Wraps `gpg --with-keygrip --list-keys --with-colons <fp>` and
// parses the colon-separated output (stable format documented in
// gnupg's DETAILS file).
//
// Returns an error if gpg isn't on PATH or the fingerprint isn't in
// the keyring.
func DiscoverIdentity(fp string) (Identity, error) {
	out, err := exec.Command("gpg", "--with-keygrip", "--with-colons", "--list-keys", fp).Output()
	if err != nil {
		return Identity{}, fmt.Errorf("gpg --list-keys %s: %w", fp, err)
	}
	id, err := parseColonsOutput(string(out))
	if err != nil {
		return Identity{}, err
	}
	if id.Fingerprint == "" {
		return Identity{}, fmt.Errorf("no fingerprint found in gpg output for %s", fp)
	}
	return id, nil
}

// parseColonsOutput parses gpg's --with-colons --with-keygrip output.
//
// Relevant record types (per gnupg DETAILS):
//   - pub: primary public key. Field 5 is the long key ID; we use fpr
//     line that follows for the full fingerprint.
//   - sub: public subkey. Same pattern with following fpr/grp lines.
//   - fpr: full fingerprint (the line right after pub or sub). Field 10.
//   - grp: keygrip (the line right after fpr when --with-keygrip set).
//     Field 10.
//   - uid: user ID. Field 10.
//
// Order in stream: pub → fpr → grp → uid → sub → fpr → grp → sub → ...
// Primary fingerprint = first fpr after pub. Primary UID = first uid.
// Keygrips = every grp line in stream order (primary first, subkeys
// after).
func parseColonsOutput(out string) (Identity, error) {
	var id Identity
	var firstFprSeen bool

	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), ":")
		if len(fields) < 10 {
			continue
		}
		switch fields[0] {
		case "fpr":
			if !firstFprSeen {
				id.Fingerprint = fields[9]
				firstFprSeen = true
			}
			// Subkey fpr lines are ignored — we only track the
			// primary fingerprint as the identity's anchor;
			// subkey fingerprints aren't needed for prewarm.
		case "grp":
			id.Keygrips = append(id.Keygrips, Keygrip(fields[9]))
		case "uid":
			if id.UID == "" {
				id.UID = fields[9]
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return id, fmt.Errorf("parse gpg output: %w", err)
	}
	return id, nil
}
