package brain

import (
	"fmt"
	"strings"

	"github.com/xianxu/nous/lib/identity"
)

// Recipient-modification helpers shared between the CLI surface
// (cmd/nous/brain_recipient.go) and the TUI surface (lib/tui/brain).
// Pure-Go where possible — git commit/push stays in lib/brainsync so
// this package doesn't take a circular dep on it (brainsync already
// imports lib/brain for Manifest.Shared()).
//
// The pattern callers use:
//
//	m, _ := brain.Read(path)
//	match, _ := brain.MatchRecipient(m.Recipients, input)
//	if err := brain.CanRemoveRecipient(m); err != nil { ... }
//	// caller-specific safeguards (self-removal warning, ceremony, etc.)
//	m.Recipients = brain.WithoutRecipient(m.Recipients, match)
//	brain.RewriteFrontmatter(path, m)
//	brain.SetGcryptParticipants(path, m.Recipients)
//	brainsync.AddCommitPush(path, msg)

// MatchRecipient finds a recipient on `manifestFps` matching the
// operator's `input` — either the full 40-char fingerprint or its last
// 8+ hex chars. Returns the canonical (40-char, uppercase) form on
// success. Errors when input is shorter than 8 (typo guard against
// matching the wrong key by accident) or no match.
func MatchRecipient(manifestFps []string, input string) (string, error) {
	want := strings.ToUpper(strings.TrimSpace(input))
	if len(want) != 40 && len(want) < 8 {
		return "", fmt.Errorf("fingerprint %q is too short — pass the last 8 hex chars (or full 40-char form) to avoid accidental matches", input)
	}
	for _, fp := range manifestFps {
		up := strings.ToUpper(fp)
		if up == want || (len(want) >= 8 && strings.HasSuffix(up, want)) {
			return fp, nil
		}
	}
	return "", nil // not found — caller distinguishes via "" return
}

// CanRemoveRecipient enforces the last-recipient guard. Returns an
// error explaining the refusal when the brain would be orphaned by
// removing one of its only recipient; nil otherwise.
func CanRemoveRecipient(m Manifest) error {
	if len(m.Recipients) <= 1 {
		return fmt.Errorf("refusing to remove the only recipient — would orphan the brain. Add another recipient first, or delete the brain")
	}
	return nil
}

// LocalSecretFingerprints returns the subset of `fps` whose secret
// half is on this machine — i.e., recipients the operator can decrypt
// as. Used by both the annotation pipeline ("(local secret)" tier)
// and the self-removal safeguard ("would I lose my decrypt path?").
//
// Errors propagate from identity.List; on outage the caller should
// treat the result as untrusted rather than empty.
func LocalSecretFingerprints(fps []string) ([]string, error) {
	keys, err := identity.List()
	if err != nil {
		return nil, err
	}
	secretSet := map[string]bool{}
	for _, k := range keys {
		secretSet[strings.ToUpper(k.Fingerprint)] = true
	}
	var out []string
	for _, fp := range fps {
		if secretSet[strings.ToUpper(fp)] {
			out = append(out, fp)
		}
	}
	return out, nil
}

// WouldLockOut reports whether removing `fp` from `recipients` would
// leave the operator with no local-secret recipient on this brain —
// the real safety floor for the self-removal safeguard. Distinct
// from the cosmetic "(self)" annotation, which only marks the primary
// identity. Both checks are needed: "(self)" makes the UI honest
// about which key is "you"; WouldLockOut is the functional refusal.
//
// Returns (false, err) on gpg outage; callers should default to
// blocking the operation in that case (defensive fallback for
// safety-critical paths).
func WouldLockOut(recipients []string, fp string) (bool, error) {
	remaining := WithoutRecipient(recipients, fp)
	localSecrets, err := LocalSecretFingerprints(remaining)
	if err != nil {
		return false, err
	}
	return len(localSecrets) == 0, nil
}

// WithoutRecipient returns a copy of `fps` with `target` (case-
// insensitive) removed. Pure helper used after MatchRecipient.
func WithoutRecipient(fps []string, target string) []string {
	out := make([]string, 0, len(fps))
	for _, fp := range fps {
		if !strings.EqualFold(fp, target) {
			out = append(out, fp)
		}
	}
	return out
}

// ContainsRecipient reports whether `target` is already in `fps`
// (case-insensitive). Used by add to detect the idempotent path.
func ContainsRecipient(fps []string, target string) bool {
	for _, fp := range fps {
		if strings.EqualFold(fp, target) {
			return true
		}
	}
	return false
}
