package brain

import (
	"fmt"
	"strings"

	"github.com/xianxu/nous/lib/identity"
)

// Annotator returns a function that maps a recipient fingerprint to a
// human-readable annotation: "(self) Xian Xu <...>" when the key has a
// secret half on this machine, "(peer) <UID>" when only the public
// half is present, "(unknown — not in keyring)" otherwise.
//
// Used by `nous brain recipient list` (cmd/nous) and the brain TUI
// drill-in (lib/tui/brain). Identity lookups are best-effort: an
// outage of `gpg --list-keys` surfaces as the unknown branch for
// every fingerprint, not as an error blocking the caller.
func Annotator() (func(string) string, error) {
	secret, err := identity.List()
	if err != nil {
		return nil, err
	}
	pub, err := identity.ListPublic()
	if err != nil {
		return nil, err
	}
	return func(fp string) string {
		fpU := strings.ToUpper(fp)
		for _, k := range secret {
			if strings.EqualFold(k.Fingerprint, fpU) {
				return fmt.Sprintf("(self) %s", k.UID)
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
