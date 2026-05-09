package security

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// DetectedApp is a known app found installed on the system.
type DetectedApp struct {
	KnownApp
	Path string // path to the .app bundle
}

// DetectInstalledApps locates each KnownApp on disk.
//
// Strategy: scan standard Applications directories once with PlistBuddy
// to build a bundleID → path index. PlistBuddy handles both XML and
// binary Info.plists, which the regex-based reader does not. Then
// consult the index per KnownApp; for entries not in the index, fall
// back to mdfind in case the user installed somewhere non-standard.
//
// First hit wins for apps appearing in multiple directories.
func DetectInstalledApps() []DetectedApp {
	idx := buildAppIndex()
	out := make([]DetectedApp, 0, len(KnownApps))
	for _, app := range KnownApps {
		path, ok := idx[app.BundleID]
		if !ok {
			path = mdfindBundle(app.BundleID)
		}
		if path == "" {
			continue
		}
		out = append(out, DetectedApp{KnownApp: app, Path: path})
	}
	return out
}

// applicationsDirs returns the directories scanned for installed apps.
// /System/Applications/Utilities is listed explicitly because Apple
// keeps Terminal, Console, etc. there and Spotlight doesn't reliably
// index that subtree on every machine.
func applicationsDirs() []string {
	dirs := []string{
		"/Applications",
		"/Applications/Utilities",
		"/System/Applications",
		"/System/Applications/Utilities",
	}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, "Applications"))
	}
	return dirs
}

// buildAppIndex enumerates every .app under the standard Applications
// dirs and reads its CFBundleIdentifier via PlistBuddy. ~30–100 forks
// at startup, but each is sub-millisecond and we cache the result.
func buildAppIndex() map[string]string {
	idx := map[string]string{}
	for _, dir := range applicationsDirs() {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".app") {
				continue
			}
			path := filepath.Join(dir, e.Name())
			id := plistBuddyBundleID(filepath.Join(path, "Contents", "Info.plist"))
			if id == "" {
				continue
			}
			if _, exists := idx[id]; !exists {
				idx[id] = path
			}
		}
	}
	return idx
}

// plistBuddyBundleID extracts CFBundleIdentifier from an Info.plist of
// either format (XML or binary). Returns "" if the plist is missing,
// malformed, or doesn't contain the key.
func plistBuddyBundleID(plistPath string) string {
	out, err := exec.Command("/usr/libexec/PlistBuddy",
		"-c", "Print :CFBundleIdentifier", plistPath).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// mdfindBundle is a Spotlight fallback for apps installed outside the
// standard directories. Returns "" if Spotlight has no result or the
// returned path no longer exists on disk.
func mdfindBundle(bundleID string) string {
	out, err := exec.Command("mdfind",
		"kMDItemCFBundleIdentifier == '"+bundleID+"'").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasSuffix(line, ".app") {
			continue
		}
		if _, err := os.Stat(line); err == nil {
			return line
		}
	}
	return ""
}
