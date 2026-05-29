package brain

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// WriteManifest writes a .brain/config.md at the given brain root with
// the fields nous cares about, generating a default boilerplate body.
// The file is replaced atomically (write to .tmp + rename) so a
// crashed write doesn't leave a half-formed manifest readers might
// choke on.
//
// The mode: field is intentionally NOT written — shared-vs-private is
// derived from len(Recipients) per the M4c schema cleanup. Existing
// manifests with mode: still parse fine via the reader's tolerance,
// so older brains aren't broken; they just stop having the field
// rewritten when nous touches them.
//
// **Use only at provisioning time.** WriteManifest emits a fresh
// boilerplate body that overwrites whatever's there. For recipient
// changes on an already-provisioned brain, use RewriteFrontmatter —
// which preserves the operator-authored body verbatim.
func WriteManifest(brainRoot string, m Manifest) error {
	dir := filepath.Join(brainRoot, ".brain")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	return atomicWrite(filepath.Join(dir, "config.md"), renderManifest(m))
}

// RewriteFrontmatter swaps the YAML frontmatter of an existing
// .brain/config.md without touching the body below it. Used by
// recipient-change ops (add/remove) so operator-authored notes,
// procedures, and rationale survive every change.
//
// Read existing → split at the closing `---` → render new frontmatter
// from m → splice → atomic write (same .tmp+rename as WriteManifest).
// If the existing file has no frontmatter, errors out — bail rather
// than overwrite something we don't recognize.
func RewriteFrontmatter(brainRoot string, m Manifest) error {
	cfgPath := filepath.Join(brainRoot, ".brain", "config.md")
	existing, err := os.ReadFile(cfgPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", cfgPath, err)
	}
	body, ok := splitBody(string(existing))
	if !ok {
		return fmt.Errorf("%s has no YAML frontmatter — refusing to rewrite (would overwrite an unrecognized format)", cfgPath)
	}

	var b strings.Builder
	b.WriteString(renderFrontmatter(m))
	b.WriteString(body)
	return atomicWrite(cfgPath, b.String())
}

// splitBody returns the part of the manifest after the closing `---\n`
// of the YAML frontmatter, including a leading newline so callers can
// concatenate the new frontmatter directly. Returns (false) when the
// document doesn't start with frontmatter delimiters.
func splitBody(content string) (string, bool) {
	if !strings.HasPrefix(content, "---\n") && !strings.HasPrefix(content, "---\r\n") {
		return "", false
	}
	rest := strings.TrimPrefix(content, "---\n")
	rest = strings.TrimPrefix(rest, "---\r\n")
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "", false
	}
	// Skip past the closing "---" line and its trailing newline so the
	// body we return doesn't double up the delimiter.
	after := rest[end:]
	after = strings.TrimPrefix(after, "\n---")
	after = strings.TrimPrefix(after, "\r\n---")
	// Eat the newline that follows the closing delimiter.
	after = strings.TrimPrefix(after, "\n")
	after = strings.TrimPrefix(after, "\r\n")
	return after, true
}

// renderFrontmatter returns the YAML frontmatter block for m,
// including both `---` delimiters and a trailing blank line. Shared
// between WriteManifest (full write) and RewriteFrontmatter
// (frontmatter-only swap).
func renderFrontmatter(m Manifest) string {
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
	return b.String()
}

// atomicWrite writes content to path via .tmp + rename. Same posture
// as WriteManifest's inline write; extracted so RewriteFrontmatter
// can share it.
func atomicWrite(path, content string) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename %s -> %s: %w", tmp, path, err)
	}
	return nil
}

// renderManifest returns the canonical .brain/config.md content for
// m — frontmatter (via renderFrontmatter) + a default boilerplate
// body. Used by WriteManifest at provisioning time;
// RewriteFrontmatter (recipient-change path) skips the body entirely.
func renderManifest(m Manifest) string {
	recipients := append([]string(nil), m.Recipients...)
	sort.Strings(recipients)

	var b strings.Builder
	b.WriteString(renderFrontmatter(m))

	// Body. Plural ("recipients") for shared brains; singular for
	// private brains. Cheap human-readable nuance; the schema is the
	// source of truth either way.
	name := m.Name
	if name == "" {
		name = "brain"
	}
	// Body wording is topology-neutral: the manifest can't tell whether
	// this brain has a remote yet (a local brain and a hosted-private
	// brain both have one recipient + sync_substrate none), so it must
	// not assert "encrypted" — a local brain's working tree is plaintext
	// until it's published. "Encrypted via gcrypt when published" is true
	// at every rung of the topology ladder (nous#33).
	if m.Shared() {
		fmt.Fprintf(&b, "# %s brain manifest\n\nMulti-recipient GPG list (%d recipients); encrypted via gcrypt when pushed to a remote. Bootstrapped by `nous brain new`.\n\nSchema reference: ariadne `AGENTS.md` §1 (Peer Repo). Security posture: `atlas/threat-model-shared-brain.md`.\n", name, len(recipients))
	} else {
		fmt.Fprintf(&b, "# %s brain manifest\n\nSingle-recipient GPG list (the operator); encrypted via gcrypt when pushed to a remote. A local-only brain stays plaintext on disk (FileVault is the at-rest protection) until `nous brain publish`. Bootstrapped by `nous brain new`.\n\nSchema reference: ariadne `AGENTS.md` §1 (Peer Repo). Security posture: `atlas/threat-model-shared-brain.md`.\n", name)
	}
	return b.String()
}

// SetGcryptParticipants writes the gcrypt-participants config on the
// brain's origin remote. Wraps `git config remote.origin.gcrypt-participants`.
// gcrypt accepts a space-separated list of fingerprints; sorted to
// match WriteManifest's posture.
//
// **New code should not call this directly.** Per nous#24, the manifest
// is the canonical source for the recipient list, and gcrypt-participants
// is a derived cache the brainsync push wrapper refreshes before every
// push (via SyncGcryptParticipantsFromManifest below). Calling
// SetGcryptParticipants from outside the push wrapper risks drift
// between the two stores — exactly the bug class nous#24 closed.
//
// Kept exported because (a) the push wrapper needs it as the underlying
// primitive, and (b) tests / migration tools occasionally need an
// explicit write that bypasses the manifest read.
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

// SyncGcryptParticipantsFromManifest reads the brain's manifest and
// writes its recipient list to `remote.origin.gcrypt-participants`.
// Used by:
//
//   - `nous brain clone` (after a fresh gcrypt clone): gcrypt's
//     clone protocol doesn't auto-populate participants on the
//     local side, so the cloned brain's first push would otherwise
//     encrypt to a stale (or empty) participants list.
//   - `brainsync.PullBrain` (after a successful pull): peers added
//     by other recipients should propagate into the local
//     participants config on the next pull, so subsequent pushes
//     encrypt to the full current recipient set without operator
//     intervention.
//
// Idempotent: if the manifest's recipients match the current
// gcrypt-participants config, the write is a no-op at the file
// level (git config emits the same single line).
func SyncGcryptParticipantsFromManifest(brainRoot string) error {
	// Tolerate non-brain repos. The push wrapper in lib/brainsync calls
	// us on every AddCommitPush, including against test fixtures and
	// generic git repos that don't have a .brain/config.md. For those,
	// there's nothing to sync — skip silently. A real brain missing its
	// manifest is a corrupt-repo state and would surface elsewhere
	// (nous brain commands fail to read it, daemon won't watch it).
	if _, err := os.Stat(filepath.Join(brainRoot, ".brain", "config.md")); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	m, err := Read(brainRoot)
	if err != nil {
		return fmt.Errorf("sync gcrypt-participants: read manifest: %w", err)
	}
	if len(m.Recipients) == 0 {
		// Defensive: a manifest with no recipients shouldn't reach
		// us in practice (brain provisioning always seeds at least
		// the operator), but if it did, SetGcryptParticipants would
		// reject the empty list. Skip silently rather than fail —
		// downstream callers shouldn't bail on a degenerate manifest.
		return nil
	}
	return SetGcryptParticipants(brainRoot, m.Recipients)
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
