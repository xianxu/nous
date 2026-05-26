package brainsync

import (
	"bytes"
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

// RemoteRawURL returns origin's underlying transport url with the
// "gcrypt::" prefix stripped if present. The result is suitable for
// `git ls-remote` calls that should bypass the gcrypt remote helper
// (and therefore not invoke gpg). See nous#34.
func RemoteRawURL(repo string) (string, error) {
	out, err := RunGit(repo, "config", "--get", "remote.origin.url")
	if err != nil {
		return "", fmt.Errorf("get remote.origin.url: %w", err)
	}
	u := strings.TrimSpace(string(out))
	if u == "" {
		return "", fmt.Errorf("remote.origin.url is empty for %s", repo)
	}
	return strings.TrimPrefix(u, "gcrypt::"), nil
}

// LsRemoteRaw runs `git ls-remote <url>` and returns a byte-stable,
// sorted serialization of the (sha, ref) pairs. Used as a cheap
// negative-cache key: identical output across calls = remote refs
// haven't moved = no work to do.
//
// Critically, this calls git ls-remote against the *raw* underlying
// transport (the caller is expected to pass the gcrypt-stripped url),
// so it does NOT invoke the gcrypt remote helper and does NOT spawn
// gpg. That's the whole point — see nous#34.
func LsRemoteRaw(url string) (string, error) {
	c := exec.Command("git", "ls-remote", url)
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr
	if err := c.Run(); err != nil {
		return "", fmt.Errorf("git ls-remote %s: %w: %s", url, err, stderr.String())
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	sort.Strings(lines)
	return strings.Join(lines, "\n"), nil
}
