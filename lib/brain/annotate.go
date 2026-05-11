package brain

import (
	"errors"
	"fmt"
	"strings"

	"github.com/xianxu/nous/lib/identity"
)

// Annotator returns a function that maps a recipient fingerprint to a
// human-readable annotation. Three positive tiers + one fallback:
//
//	"(self) <UID>"          — the operator's primary identity (see
//	                          lib/identity.Primary). Strict: at most
//	                          one key on a machine carries this label
//	                          per resolved primary.
//	"(local secret) <UID>"  — secret half present in keyring, but not
//	                          primary. Common for throwaway test keys,
//	                          work keys, prior-rotation keys.
//	"(peer) <UID>"          — public half present; secret elsewhere.
//	"(unknown — not in
//	  keyring)"             — fingerprint isn't in this machine's
//	                          gpg keyring at all.
//
// Resolution of "primary": Annotator looks first at the stored primary
// (lib/identity.Primary). If that's unset and the keyring has multiple
// secret keys, it falls back to a brain-aware heuristic: scan
// DiscoverAll() for a private brain (single recipient) whose
// recipient is also a local secret key. A private brain is by
// definition operator-private, so its sole recipient is almost
// certainly the operator. The result is used for labeling only —
// nothing is written to disk; `nous identity primary` is the place
// that persists.
//
// Failure modes are soft: a gpg outage or workspace-scan failure
// collapses the annotation to (unknown ...) rather than blocking
// callers. CLI and TUI both reach this through the same shim, so
// behavior stays uniform.
func Annotator() (func(string) string, error) {
	secret, err := identity.List()
	if err != nil {
		return nil, err
	}
	pub, err := identity.ListPublic()
	if err != nil {
		return nil, err
	}
	primaryFP := resolvePrimaryFingerprint(secret)

	return func(fp string) string {
		fpU := strings.ToUpper(fp)
		if primaryFP != "" && strings.EqualFold(primaryFP, fpU) {
			return fmt.Sprintf("(self) %s", uidFor(secret, fpU))
		}
		for _, k := range secret {
			if strings.EqualFold(k.Fingerprint, fpU) {
				return fmt.Sprintf("(local secret) %s", k.UID)
			}
		}
		for _, k := range pub {
			if strings.EqualFold(k.Fingerprint, fpU) {
				return fmt.Sprintf("(peer) %s", k.UID)
			}
		}
		return "(unknown — not in keyring)"
	}, nil
}

// resolvePrimaryFingerprint returns the fp to treat as (self):
//   - identity.Primary state (file or implicit single-secret).
//   - On ErrPrimaryUnset, fall back to HeuristicPrimary (brain-aware).
//   - Otherwise empty string (caller renders no (self), only
//     (local secret) labels).
//
// `secret` is passed in by Annotator to avoid a redundant
// identity.List call inside HeuristicPrimary; the public function
// re-derives it from gpg directly.
func resolvePrimaryFingerprint(secret []identity.Key) string {
	if key, err := identity.Primary(); err == nil {
		return strings.ToUpper(key.Fingerprint)
	} else if !errors.Is(err, identity.ErrPrimaryUnset) {
		// Stale state, transient outage, etc. — fall through to the
		// heuristic rather than block on it.
	}
	fp, _, err := heuristicPrimaryFromKeys(secret)
	if err != nil {
		return ""
	}
	return fp
}

// HeuristicPrimary infers the operator's primary identity from brain
// state without consulting `identity.Primary`'s persistent record. A
// private brain (single recipient) whose recipient also has a secret
// half on this machine is almost certainly the operator's primary —
// operator-private brains are by definition encrypted to one's own
// key.
//
// Returns (fp, hint, nil) on match where `hint` explains which brain
// supplied the signal. Returns ("", "", nil) on no-match (not an
// error). Errors propagate from gpg / brain discovery.
//
// Used in two places: lib/brain.Annotator (read-only fallback when
// identity.Primary is unset) and cmd/nous/identity_primary
// (interactive `nous identity primary` runs this and offers to
// persist).
func HeuristicPrimary() (fp, hint string, err error) {
	secret, err := identity.List()
	if err != nil {
		return "", "", err
	}
	return heuristicPrimaryFromKeys(secret)
}

func heuristicPrimaryFromKeys(secret []identity.Key) (fp, hint string, err error) {
	if len(secret) == 0 {
		return "", "", nil
	}
	secretSet := map[string]bool{}
	for _, k := range secret {
		secretSet[strings.ToUpper(k.Fingerprint)] = true
	}
	manifests, err := DiscoverAll()
	if err != nil {
		return "", "", err
	}
	for _, m := range manifests {
		if len(m.Recipients) != 1 {
			continue
		}
		fpU := strings.ToUpper(m.Recipients[0])
		if secretSet[fpU] {
			return fpU, fmt.Sprintf("private brain %s has this key as its sole recipient", m.Path), nil
		}
	}
	return "", "", nil
}

func uidFor(keys []identity.Key, fp string) string {
	for _, k := range keys {
		if strings.EqualFold(k.Fingerprint, fp) {
			return k.UID
		}
	}
	return ""
}
