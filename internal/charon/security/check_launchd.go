package security

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// LaunchdLocations are the standard plist directories. User-scope
// always exists (or is creatable); system-scope may not be readable
// without elevated permissions but reading directory listings doesn't
// need privilege.
var LaunchdLocations = []string{
	"~/Library/LaunchAgents",
	"/Library/LaunchAgents",
	"/Library/LaunchDaemons",
}

// CheckLaunchdAgents enumerates plists in the standard launchd
// directories and emits an Info-level finding listing the non-Apple
// entries. This is observational — the user judges what's expected.
//
// Apple-shipped daemons (`com.apple.*`) and Homebrew (`homebrew.*`,
// `org.openssl.*` etc.) are filtered out as low-signal.
func CheckLaunchdAgents() []Finding {
	var thirdParty []string
	for _, loc := range LaunchdLocations {
		dir := expandHome(loc)
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if !strings.HasSuffix(name, ".plist") {
				continue
			}
			label := strings.TrimSuffix(name, ".plist")
			if isWellKnownDaemon(label) {
				continue
			}
			thirdParty = append(thirdParty, filepath.Join(dir, name))
		}
	}
	if len(thirdParty) == 0 {
		return nil
	}
	return []Finding{{
		ID:       "launchd-third-party",
		Severity: SevInfo,
		Title:    fmt.Sprintf("%d third-party launchd plists found", len(thirdParty)),
		Detail: "These plists make their owners auto-start. Verify each is " +
			"yours; remove unrecognized entries with `launchctl bootout` + " +
			"`rm`.\n\n" + strings.Join(thirdParty, "\n"),
		RemedyRef: "launchd",
		Affects:   thirdParty,
		BarItem:   BarLaunchdPersistence,
	}}
}

// isWellKnownDaemon filters out plists that don't warrant user review.
// Conservative: when in doubt, surface it.
func isWellKnownDaemon(label string) bool {
	prefixes := []string{
		"com.apple.",
		"homebrew.mxcl.",
		"org.openssl.",
		"com.docker.",  // Docker Desktop
		"com.google.keystone.", // Google updater (debatable; surface? for now skip)
	}
	for _, p := range prefixes {
		if strings.HasPrefix(label, p) {
			return true
		}
	}
	return false
}

func expandHome(p string) string {
	if !strings.HasPrefix(p, "~") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return filepath.Join(home, strings.TrimPrefix(p, "~"))
}
