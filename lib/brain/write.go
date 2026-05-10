package brain

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// WriteManifest writes a .brain/config.md at the given brain root with
// the fields nous cares about. The file is replaced atomically (write
// to .tmp + rename) so a crashed write doesn't leave a half-formed
// manifest readers might choke on.
//
// The mode: field is intentionally NOT written — shared-vs-private is
// derived from len(Recipients) per the M4c schema cleanup. Existing
// manifests with mode: still parse fine via the reader's tolerance,
// so older brains aren't broken; they just stop having the field
// rewritten when nous touches them.
//
// The file body (after frontmatter) is a fixed boilerplate paragraph
// pointing to ariadne AGENTS.md §1 and the threat model. Operators
// who want richer manifests can hand-edit; nous won't clobber that
// because it only ever calls WriteManifest at provisioning time, not
// on every recipient change.
func WriteManifest(brainRoot string, m Manifest) error {
	dir := filepath.Join(brainRoot, ".brain")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}

	body := renderManifest(m)
	target := filepath.Join(dir, "config.md")
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, []byte(body), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, target); err != nil {
		return fmt.Errorf("rename %s -> %s: %w", tmp, target, err)
	}
	return nil
}

// renderManifest returns the canonical .brain/config.md body for m.
// Recipients are sorted to keep diffs minimal across recipient changes
// (otherwise re-rendering after an Add could shuffle order even when
// nothing meaningful changed).
func renderManifest(m Manifest) string {
	recipients := append([]string(nil), m.Recipients...)
	sort.Strings(recipients)

	var b strings.Builder
	b.WriteString("---\n")
	if m.Name != "" {
		fmt.Fprintf(&b, "name: %s\n", m.Name)
	}
	fmt.Fprintf(&b, "recipients: [%s]\n", strings.Join(recipients, ", "))
	if m.SyncSubstrate != "" {
		fmt.Fprintf(&b, "sync_substrate: %s\n", m.SyncSubstrate)
	}
	b.WriteString("---\n\n")

	// Body. Plural ("recipients") for shared brains; singular for
	// private brains. Cheap human-readable nuance; the schema is the
	// source of truth either way.
	name := m.Name
	if name == "" {
		name = "brain"
	}
	if m.Shared() {
		fmt.Fprintf(&b, "# %s brain manifest\n\nEncrypted via gcrypt with a multi-recipient GPG list (%d recipients). Bootstrapped by `nous brain new`.\n\nSchema reference: ariadne `AGENTS.md` §1 (Peer Repo). Security posture: `atlas/threat-model-shared-brain.md`.\n", name, len(recipients))
	} else {
		fmt.Fprintf(&b, "# %s brain manifest\n\nEncrypted via gcrypt with a single-recipient GPG list (the operator). Bootstrapped by `nous brain new`.\n\nSchema reference: ariadne `AGENTS.md` §1 (Peer Repo). Security posture: `atlas/threat-model-shared-brain.md`.\n", name)
	}
	return b.String()
}

// SetGcryptParticipants writes the gcrypt-participants config on the
// brain's origin remote. Wraps `git config remote.origin.gcrypt-participants`.
// gcrypt accepts a space-separated list of fingerprints; sorted to
// match WriteManifest's posture.
//
// Re-running with a different fingerprint list updates the config; the
// next `git push` re-encrypts gcrypt's metadata to the new list. Old
// recipients can still decrypt blobs already in the gcrypt object
// store — see RecipientRemove caller-side comments for the revocation
// caveat.
func SetGcryptParticipants(brainRoot string, fingerprints []string) error {
	if len(fingerprints) == 0 {
		return fmt.Errorf("at least one recipient required (gcrypt rejects empty participant list)")
	}
	sorted := append([]string(nil), fingerprints...)
	sort.Strings(sorted)
	val := strings.Join(sorted, " ")

	cmd := exec.Command("git", "-C", brainRoot, "config", "remote.origin.gcrypt-participants", val)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git config gcrypt-participants: %w\n%s", err, string(out))
	}
	return nil
}

// ReadGcryptParticipants returns the fingerprint list configured on
// the brain's origin remote. Empty slice (no error) when the config
// key is unset — useful for "is this even a gcrypt-managed brain?"
// checks.
func ReadGcryptParticipants(brainRoot string) ([]string, error) {
	cmd := exec.Command("git", "-C", brainRoot, "config", "--get", "remote.origin.gcrypt-participants")
	out, err := cmd.Output()
	if err != nil {
		// `git config --get` exits 1 when the key is absent. That's
		// the empty-list case, not an error.
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return nil, nil
		}
		return nil, fmt.Errorf("git config --get gcrypt-participants: %w", err)
	}
	val := strings.TrimSpace(string(out))
	if val == "" {
		return nil, nil
	}
	return strings.Fields(val), nil
}
