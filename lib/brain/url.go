package brain

import (
	"fmt"
	"regexp"
	"strings"
)

// githubRemoteRE matches the GitHub owner/repo segment of all the URL
// shapes nous encounters: gcrypt-wrapped ssh/https, bare ssh/https,
// and SCP-style. Capture groups are owner, repo (no .git suffix).
//
// Accepts:
//   gcrypt::ssh://git@github.com/owner/repo.git
//   gcrypt::ssh://git@github.com:owner/repo.git
//   gcrypt::https://github.com/owner/repo
//   ssh://git@github.com/owner/repo.git
//   https://github.com/owner/repo
//   git@github.com:owner/repo.git
var githubRemoteRE = regexp.MustCompile(
	`github\.com[:/]([A-Za-z0-9][A-Za-z0-9-]*)/([A-Za-z0-9._-]+?)(?:\.git)?/?$`,
)

// GitHubOwnerRepo extracts the (owner, repo) pair from a git remote
// URL pointing at github.com. Tolerates gcrypt:: prefixes, ssh/https
// schemes, SCP-style colons, and optional .git suffix.
//
// Returns an error when the URL doesn't reference github.com or the
// path component doesn't have the expected owner/repo shape — callers
// should treat that as "not a github-hosted brain" rather than panic.
func GitHubOwnerRepo(remoteURL string) (owner, repo string, err error) {
	url := strings.TrimPrefix(remoteURL, "gcrypt::")
	m := githubRemoteRE.FindStringSubmatch(url)
	if m == nil {
		return "", "", fmt.Errorf("not a github.com URL: %q", remoteURL)
	}
	return m[1], m[2], nil
}
