package brain

import (
	"strings"

	"github.com/xianxu/nous/lib/gh"
)

// IsOperator reports whether `myLogin` can act as operator on the
// brain at brainRoot — that is, whether they own the github repo or
// hold admin/maintain permission on it. "Operator" gates the
// invite/remove ergonomics in the CLI and TUI: non-operators can
// still attempt those actions, but GitHub's authz layer will reject
// at API time, so we mark the capability upfront.
//
// Returns false (the safe default) for:
//   - empty myLogin (operator can't be resolved)
//   - brain with no remote.origin.url configured
//   - remote that doesn't parse as a github.com URL
//   - any gh outage on the permission probe
//
// Personal-repo owners short-circuit the gh probe — when myLogin
// matches the URL's owner segment, ownership is implicit (no API
// call needed).
//
// One gh API call per brain when the short-circuit doesn't fire.
// Cache via the caller if you have many brains to probe in a tight
// loop; this function makes no caching commitment of its own.
func IsOperator(brainRoot, myLogin string) bool {
	if myLogin == "" {
		return false
	}
	origin := readOriginURL(brainRoot)
	if origin == "" {
		return false
	}
	owner, repo, err := GitHubOwnerRepo(origin)
	if err != nil {
		return false
	}
	if strings.EqualFold(owner, myLogin) {
		return true
	}
	perm, err := gh.CollaboratorPermission(owner, repo, myLogin)
	if err != nil {
		return false
	}
	return perm == "admin" || perm == "maintain"
}

// (readOriginURL lives in status.go — package-private; we just reuse it.)
