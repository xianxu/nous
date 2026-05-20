package brain

import (
	"context"
	"fmt"
	"strings"

	"github.com/xianxu/nous/lib/brain/filestore"
	"github.com/xianxu/nous/lib/identity"
)

// AdmittedRecipient describes one recipient that auto-admit
// just appended to the manifest. Surfaced for the caller (brainsync)
// to build a human-readable commit message and log line.
type AdmittedRecipient struct {
	Login       string // github login (filename stem, e.g., "yingtest42")
	Fingerprint string // 40-hex uppercase, derived from the pubkey
	UID         string // first GPG UID on the imported pubkey
}

// AutoAdmitFromKeysBranch scans the brain's keys branch for
// `<github-login>.asc` files whose fingerprints are not yet listed
// in `.brain/config.md`'s recipients, and appends them. Returns
// the slice of newly-admitted recipients (empty when nothing
// changed).
//
// Does NOT commit or push — the caller (typically brainsync's
// Watch loop after `syncBrainPubkeys`) calls AddCommitPush so the
// manifest mutation, the #24 gcrypt-participants sync, and the
// push happen as one atomic operation.
//
// Filename convention: stems that look like a 40-hex fingerprint
// are skipped — those are legacy nous#23 entries (operator-
// published peer pubkeys) that are in the manifest by construction.
// The new nous#26 convention is `<github-login>.asc` where login
// is not a fingerprint; those are the candidates.
//
// Returns (nil, nil) on no-op (no keys branch yet, no candidates,
// no new keys). Errors propagate only on infrastructure failures
// (manifest unreadable, keys branch listable but unparseable).
func AutoAdmitFromKeysBranch(ctx context.Context, brainRoot string) ([]AdmittedRecipient, error) {
	m, err := Read(brainRoot)
	if err != nil {
		return nil, fmt.Errorf("auto-admit: read manifest: %w", err)
	}

	store, err := filestore.Open(brainRoot, keysBranch)
	if err != nil {
		return nil, fmt.Errorf("auto-admit: open keys store: %w", err)
	}
	defer store.Close()

	files, err := store.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("auto-admit: list keys store: %w", err)
	}

	existing := setOfUpper(m.Recipients)
	var added []AdmittedRecipient

	for name, content := range files {
		if !strings.HasSuffix(name, pubkeyFilenameSuffix) {
			continue
		}
		stem := strings.TrimSuffix(name, pubkeyFilenameSuffix)
		if looksLikeFingerprint(stem) {
			// Legacy nous#23 convention — the FP is already in the
			// manifest if this peer was added through the old path.
			// Auto-admit is opt-in via the new <login>.asc shape.
			continue
		}
		// Inspect without importing first, so we get the fingerprint
		// before committing to keyring state. (Import is also called
		// upstream by brainsync.syncBrainPubkeys via ImportAllPubkeys
		// — by the time auto-admit runs, the key is already imported.)
		key, ierr := identity.Inspect(string(content))
		if ierr != nil {
			// Malformed pubkey — skip with a soft signal via the
			// caller's logging. We don't return early; one bad file
			// shouldn't block legitimate admissions.
			continue
		}
		fpUp := strings.ToUpper(key.Fingerprint)
		if existing[fpUp] {
			continue
		}
		m.Recipients = append(m.Recipients, fpUp)
		existing[fpUp] = true
		added = append(added, AdmittedRecipient{
			Login:       stem,
			Fingerprint: fpUp,
			UID:         key.UID,
		})
	}

	if len(added) == 0 {
		return nil, nil
	}
	if err := RewriteFrontmatter(brainRoot, m); err != nil {
		return nil, fmt.Errorf("auto-admit: rewrite manifest: %w", err)
	}
	return added, nil
}

// looksLikeFingerprint reports whether s is a 40-character hex
// string — the legacy <FP>.asc filename convention. Case-
// insensitive on hex digits; spaces aren't tolerated (gpg
// fingerprints contain only [0-9A-F]).
func looksLikeFingerprint(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, c := range s {
		switch {
		case '0' <= c && c <= '9':
		case 'a' <= c && c <= 'f':
		case 'A' <= c && c <= 'F':
		default:
			return false
		}
	}
	return true
}

// setOfUpper builds an uppercase-key set for case-insensitive
// fingerprint membership tests. Re-implemented here (vs. the
// `setOf` in cmd/nous/brain_recipient.go) to keep this package
// dependency-free of the binary's main package.
func setOfUpper(xs []string) map[string]bool {
	out := make(map[string]bool, len(xs))
	for _, x := range xs {
		out[strings.ToUpper(x)] = true
	}
	return out
}
