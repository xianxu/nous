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
//   - On ErrPrimaryUnset, fall back to brain heuristic: scan brains
//     for a private brain whose only recipient is a local secret
//     key — that recipient is almost certainly the operator.
//   - Otherwise empty string (caller renders no (self), only
//     (local secret) labels).
func resolvePrimaryFingerprint(secret []identity.Key) string {
	if key, err := identity.Primary(); err == nil {
		return strings.ToUpper(key.Fingerprint)
	} else if !errors.Is(err, identity.ErrPrimaryUnset) {
		// Stale state, transient outage, etc. — fall through to the
		// heuristic rather than block on it.
	}

	secretSet := map[string]bool{}
	for _, k := range secret {
		secretSet[strings.ToUpper(k.Fingerprint)] = true
	}
	if len(secretSet) == 0 {
		return ""
	}
	manifests, err := DiscoverAll()
	if err != nil {
		return ""
	}
	for _, m := range manifests {
		if len(m.Recipients) != 1 {
			continue
		}
		fp := strings.ToUpper(m.Recipients[0])
		if secretSet[fp] {
			return fp
		}
	}
	return ""
}

func uidFor(keys []identity.Key, fp string) string {
	for _, k := range keys {
		if strings.EqualFold(k.Fingerprint, fp) {
			return k.UID
		}
	}
	return ""
}
