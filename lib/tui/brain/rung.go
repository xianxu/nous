package brain

import "fmt"

// rung is a brain's position on the topology ladder (nous#33):
// local → private → shared. It's orthogonal to who can decrypt
// (recipient count) — it answers "where does the ciphertext live, and
// is there an upstream at all?" Both the list label and the detail
// header/footer derive from it, so the classification lives here once.
//
//   - rungLocal:   no remote. A git repo on this device only; gcrypt
//                  never engaged, so the working tree is plaintext
//                  (FileVault is the at-rest protection).
//   - rungPrivate: a remote + a single recipient. Encrypted backup on
//                  GitHub, solo.
//   - rungShared:  a remote + 2+ recipients. Encrypted on GitHub, shared.
type rung int

const (
	rungLocal rung = iota
	rungPrivate
	rungShared
)

// classifyRung derives the rung from the two orthogonal signals: does
// the brain have a remote, and how many recipients can decrypt it.
func classifyRung(hasRemote bool, recipientCount int) rung {
	if !hasRemote {
		return rungLocal
	}
	if recipientCount > 1 {
		return rungShared
	}
	return rungPrivate
}

// rungLabel is the short one-word (plus count) label shown in the list
// and detail header. Labels describe reach, not encryption: a local
// brain is also private in the recipient sense, but "local" tells the
// operator where it lives — the disambiguation that matters.
func rungLabel(r rung, recipientCount int) string {
	switch r {
	case rungLocal:
		return "local"
	case rungShared:
		return fmt.Sprintf("shared · %d", recipientCount)
	default:
		return "private"
	}
}
