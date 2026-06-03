package brain

import (
	"context"
	"fmt"
	"sort"
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

	// SupersededFingerprint is set (40-hex uppercase) when this admission
	// is a key rotation: the login was previously admitted as this old
	// fingerprint (recorded in the manifest's recipient_logins map), which
	// auto-admit just evicted from Recipients in favor of Fingerprint
	// (nous#41 #7/#8). Empty for a first-time admission.
	SupersededFingerprint string
}

// DriftEvent describes a recipient whose keys-branch fingerprint has
// changed from what's recorded in verified.yaml. Auto-admit pauses
// for these — re-admitting a substituted key would silently widen
// the trust circle without operator consent. Surfaced so the caller
// can log loudly and surface to the operator (in TUI: "ying's key
// rotated/changed since you verified it last; re-verify or revoke").
type DriftEvent struct {
	Login            string // github login of the affected recipient
	OldFingerprint   string // fingerprint pinned in verified.yaml
	NewFingerprint   string // fingerprint on the keys branch now
	VerifiedBy       string // who recorded the original verification
}

// AutoAdmitFromKeysBranch scans the brain's keys branch for
// `<github-login>.asc` files whose fingerprints are not yet listed
// in `.brain/config.md`'s recipients, and appends them. Returns the
// newly-admitted recipients alongside any drift events (a login
// previously verified now showing a different fingerprint).
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
// Drift detection: if .brain/verified.yaml has an entry for a
// login and that entry's fingerprint differs from what's on the
// keys branch now, auto-admit refuses to bring the new fingerprint
// in. The operator must re-verify (which updates verified.yaml) to
// re-enable admission for that login. This is the only safety
// floor the otherwise-fully-automatic admit flow has against a
// substituted-key MITM.
//
// Returns (nil, nil, nil) on no-op. Errors propagate only on
// infrastructure failures (manifest unreadable, keys branch listable
// but unparseable, verified.yaml malformed).
func AutoAdmitFromKeysBranch(ctx context.Context, brainRoot string) ([]AdmittedRecipient, []DriftEvent, error) {
	m, err := Read(brainRoot)
	if err != nil {
		return nil, nil, fmt.Errorf("auto-admit: read manifest: %w", err)
	}
	verified, err := ReadVerified(brainRoot)
	if err != nil {
		return nil, nil, fmt.Errorf("auto-admit: read verified: %w", err)
	}

	store, err := filestore.Open(brainRoot, keysBranch)
	if err != nil {
		return nil, nil, fmt.Errorf("auto-admit: open keys store: %w", err)
	}
	defer store.Close()

	files, err := store.List(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("auto-admit: list keys store: %w", err)
	}

	existing := setOfUpper(m.Recipients)
	if m.RecipientLogins == nil {
		m.RecipientLogins = map[string]string{}
	}
	var added []AdmittedRecipient
	var drift []DriftEvent

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

		// Drift check: if this login has been verified before with a
		// different fingerprint, refuse to admit the new key. The
		// operator must explicitly re-verify (writing the new FP into
		// verified.yaml) before auto-admit will accept it.
		if v, ok := verified[stem]; ok && !strings.EqualFold(v.Fingerprint, fpUp) {
			drift = append(drift, DriftEvent{
				Login:          stem,
				OldFingerprint: strings.ToUpper(v.Fingerprint),
				NewFingerprint: fpUp,
				VerifiedBy:     v.VerifiedBy,
			})
			continue
		}

		// Rotation supersede: this login was admitted before as a different
		// fingerprint (recorded in recipient_logins). Evict the old fp from
		// Recipients so the rotated-away key can't decrypt future pushes —
		// the one-active-fp-per-login invariant (nous#41 #7/#8). The
		// verified-drift gate above already refused the rotation if the
		// operator had pinned the old fp in verified.yaml, so reaching here
		// means the supersede is consent-consistent.
		prevFp, hasPrev := m.RecipientLogins[stem]
		rotated := hasPrev && !strings.EqualFold(prevFp, fpUp)

		if existing[fpUp] && !rotated {
			// Already a recipient and not a rotation — nothing to do. (A
			// pre-map recipient with no recipient_logins entry is left as-is;
			// backfilling here would be a manifest write the caller won't
			// commit. It gains an entry the next time this login is admitted
			// or rotated.)
			continue
		}

		var superseded string
		if rotated {
			m.Recipients = WithoutRecipient(m.Recipients, prevFp)
			delete(existing, strings.ToUpper(prevFp))
			superseded = strings.ToUpper(prevFp)
		}
		if !existing[fpUp] {
			m.Recipients = append(m.Recipients, fpUp)
			existing[fpUp] = true
		}
		m.RecipientLogins[stem] = fpUp
		added = append(added, AdmittedRecipient{
			Login:                 stem,
			Fingerprint:           fpUp,
			UID:                   key.UID,
			SupersededFingerprint: superseded,
		})
	}

	if len(added) == 0 {
		return nil, drift, nil
	}
	if err := RewriteFrontmatter(brainRoot, m); err != nil {
		return nil, drift, fmt.Errorf("auto-admit: rewrite manifest: %w", err)
	}
	return added, drift, nil
}

// DetectLoginDrift returns the GitHub logins this brain has membership records
// for (recipient_logins keys, keys-branch `<login>.asc` stems — whatever the
// caller collects into recordedLogins) that are NOT in the repo's CURRENT
// collaborator list. Each such login is a possible GitHub login *rename* (the
// person still has access under a new login, leaving the old login orphaned in
// our records) or a stale record after a departure. Case-insensitive; the
// result is de-duplicated and sorted.
//
// Pure — the caller supplies the gh-fetched collaborator list. Detection only;
// auto-healing a rename (rewriting `<old>.asc` → `<new>.asc`, the verified.yaml
// key, the recipient_logins key) is deferred until the dogfood shows it biting
// (nous#41 #10).
func DetectLoginDrift(recordedLogins, currentCollaborators []string) []string {
	current := make(map[string]bool, len(currentCollaborators))
	for _, c := range currentCollaborators {
		if c = strings.TrimSpace(c); c != "" {
			current[strings.ToLower(c)] = true
		}
	}
	seen := make(map[string]bool)
	var orphaned []string
	for _, l := range recordedLogins {
		ll := strings.ToLower(strings.TrimSpace(l))
		if ll == "" || current[ll] || seen[ll] {
			continue
		}
		seen[ll] = true
		orphaned = append(orphaned, strings.TrimSpace(l))
	}
	sort.Strings(orphaned)
	return orphaned
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
