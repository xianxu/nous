package security

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// TCC service names we care about. Stable across macOS 11+.
const (
	tccFDA    = "kTCCServiceSystemPolicyAllFiles"
	tccA11y   = "kTCCServiceAccessibility"
	tccScreen = "kTCCServiceScreenCapture"
	tccEvents = "kTCCServiceAppleEvents"
)

// CredentialApps are high-value targets for AppleEvents grants — apps
// that hold or display credentials. A terminal/editor/IDE with
// Automation rights to one of these is a Critical finding (it can be
// scripted via AppleEvents to extract secrets, bypassing direct
// keychain ACLs).
var CredentialApps = map[string]string{
	"com.apple.keychainaccess":   "Keychain Access",
	"com.agilebits.onepassword":  "1Password",
	"com.agilebits.onepassword7": "1Password 7",
	"com.bitwarden.desktop":      "Bitwarden",
	"com.dashlane.dashlane":      "Dashlane",
	"com.lastpass.LastPass":      "LastPass",
}

// DangerousTCCPaths are absolute paths that should not appear as TCC
// clients (client_type=1). If any of these holds FDA, Accessibility,
// Screen Recording, or AppleEvents grants, the TCC boundary is
// effectively bypassed for that service: any process running as the
// user can shell out to the path and inherit the granted permission.
//
// Curated list — narrow to obvious silent-bypass paths. Common-but-
// noisy entries (git, code, etc.) deliberately excluded.
var DangerousTCCPaths = map[string]string{
	"/usr/bin/security":   "any process can read keychain entries via the `security` CLI",
	"/usr/bin/codesign":   "any process can produce charon-signed Mach-O binaries silently",
	"/bin/sh":             "any process can shell out and inherit broad permissions",
	"/bin/bash":           "any process can shell out and inherit broad permissions",
	"/bin/zsh":            "any process can shell out and inherit broad permissions",
	"/usr/bin/osascript":  "AppleScript with TCC grants is essentially universal automation",
	"/usr/bin/python3":    "any Python script can use the granted permission",
	"/usr/bin/perl":       "any Perl script can use the granted permission",
	"/usr/bin/ruby":       "any Ruby script can use the granted permission",
	"/opt/homebrew/bin/sh":   "any process can shell out and inherit broad permissions",
	"/opt/homebrew/bin/bash": "any process can shell out and inherit broad permissions",
	"/opt/homebrew/bin/zsh":  "any process can shell out and inherit broad permissions",
}

// suspiciousPathPrefixes are paths under which a TCC client is
// almost certainly user-downloaded or build-output content rather
// than a legitimate installed app. Important-tier flag (not Critical
// — could be a development binary the user trusts).
var suspiciousPathPrefixes = []string{
	"/private/tmp/",
	"/tmp/",
	"/private/var/folders/", // macOS user-temp directories
}

// TCCRow is one row from the access table. Only the columns we
// consume are pulled — the schema has churned across macOS versions
// and a wide SELECT * would break on older or newer hosts.
type TCCRow struct {
	Service                  string `json:"service"`
	Client                   string `json:"client"`
	ClientType               int    `json:"client_type"`
	AuthValue                int    `json:"auth_value"`
	IndirectObjectIdentifier string `json:"indirect_object_identifier"`
}

// IsAllowed reports whether this auth_value means the grant is
// currently active. 2 = allowed, 3 = limited (e.g. partial folder
// access for SystemPolicy* services). 0 = denied, 1 = unknown.
func (r TCCRow) IsAllowed() bool {
	return r.AuthValue == 2 || r.AuthValue == 3
}

// ErrNoFDA is returned by ReadTCC when the database can't be opened
// because the running process lacks Full Disk Access. Callers can
// surface this as a "grant FDA and re-run" hint instead of a hard
// error.
var ErrNoFDA = errors.New("TCC.db not readable (Full Disk Access required)")

// TCCDatabasePath returns the canonical path for the user-scope or
// system-scope TCC database. Returns "" for unknown scope.
func TCCDatabasePath(scope string) string {
	switch scope {
	case "user":
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		return filepath.Join(home, "Library/Application Support/com.apple.TCC/TCC.db")
	case "system":
		return "/Library/Application Support/com.apple.TCC/TCC.db"
	}
	return ""
}

// ReadTCC opens the given TCC.db read-only via /usr/bin/sqlite3 and
// returns the access rows we evaluate.
//
// Why shell-out instead of a Go SQLite library: the audit tool
// already shells out to csrutil, sudo, mdfind, codesign, PlistBuddy.
// Adding a 1 MB pure-Go SQLite dep for one read-only query against a
// stable schema is overkill; macOS ships sqlite3 in /usr/bin and
// supports -json output since 3.33 (Catalina+).
func ReadTCC(dbPath string) ([]TCCRow, error) {
	fi, err := os.Stat(dbPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // system-scope DB is sometimes absent
		}
		if os.IsPermission(err) {
			return nil, ErrNoFDA
		}
		return nil, err
	}
	if fi.IsDir() {
		return nil, fmt.Errorf("TCC.db path is a directory: %s", dbPath)
	}

	// Probe FDA attribution by opening the file directly first. If
	// this succeeds the running process has FDA via TCC; if a
	// subsequent sqlite3 shell-out then fails, we've isolated the
	// bug to child-process attribution rather than the grant itself.
	probe, openErr := os.Open(dbPath)
	if openErr != nil {
		if os.IsPermission(openErr) {
			return nil, fmt.Errorf("%w (os.Open: %v)", ErrNoFDA, openErr)
		}
		return nil, openErr
	}
	probe.Close()

	cmd := exec.Command("/usr/bin/sqlite3",
		"-readonly", "-json", dbPath,
		"SELECT service, client, client_type, auth_value, "+
			"IFNULL(indirect_object_identifier, '') AS indirect_object_identifier "+
			"FROM access")
	out, err := cmd.CombinedOutput()
	if err != nil {
		s := string(out)
		switch {
		case strings.Contains(s, "unable to open database"),
			strings.Contains(s, "authorization denied"),
			strings.Contains(s, "Operation not permitted"):
			// Direct os.Open succeeded above — so the parent bundle
			// HAS FDA. sqlite3 failing here means the child process
			// didn't inherit attribution. Surface that diagnosis
			// rather than the vague "FDA required" hint.
			return nil, fmt.Errorf("FDA attached to bundle but /usr/bin/sqlite3 child failed: %s", strings.TrimSpace(s))
		default:
			return nil, fmt.Errorf("sqlite3 query failed: %w: %s", err, strings.TrimSpace(s))
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	var rows []TCCRow
	if err := json.Unmarshal(out, &rows); err != nil {
		return nil, fmt.Errorf("parse TCC json: %w", err)
	}
	return rows, nil
}

// CheckTCC reads both TCC databases and emits findings for grants
// held by detected terminals/editors/IDEs. Path-based grants
// (client_type=1) are out of scope — terminals and IDEs are bundles.
//
// Severity matrix:
//
//	FDA           on terminal/editor/IDE → Critical
//	Accessibility on terminal/editor/IDE → Critical
//	ScreenCapture on terminal/editor/IDE → Important
//	AppleEvents   on terminal/editor/IDE → Important (Critical when target is a credential app)
//
// When a TCC database can't be read for FDA reasons, a single Info
// finding suggests granting FDA to the audit tool itself.
func CheckTCC(apps []DetectedApp) []Finding {
	byBundle := make(map[string]DetectedApp, len(apps))
	for _, a := range apps {
		byBundle[a.BundleID] = a
	}

	var findings []Finding
	for _, scope := range []string{"user", "system"} {
		path := TCCDatabasePath(scope)
		if path == "" {
			continue
		}
		rows, err := ReadTCC(path)
		switch {
		case errors.Is(err, ErrNoFDA):
			findings = append(findings, Finding{
				ID:        "tcc-no-fda-" + scope,
				Severity:  SevInfo,
				Title:     fmt.Sprintf("Cannot read %s-scope TCC.db: %v", scope, err),
				Detail:    fmt.Sprintf("Underlying error: %v\n\nGrant Full Disk Access to com.charon.security via System Settings → Privacy & Security → Full Disk Access, then re-run. Alternatively run with --no-tcc to skip this check and use the visual System Settings walk.", err),
				RemedyRef: "tcc-fda",
				Affects:   []string{path},
			})
			continue
		case err != nil:
			findings = append(findings, Finding{
				ID:       "tcc-readerr-" + scope,
				Severity: SevInfo,
				Title:    fmt.Sprintf("Could not read %s-scope TCC.db", scope),
				Detail:   err.Error(),
				Affects:  []string{path},
			})
			continue
		}
		findings = append(findings, evaluateTCCRows(rows, byBundle, scope)...)
	}
	return findings
}

// evaluateTCCRows is the pure function — ZERO syscalls — that turns
// rows + known-app set into findings. Tests target this directly.
//
// Two client types are evaluated:
//   client_type=0 (bundle ID) — joined against KnownApps; flagged
//                                only if the bundle ID is in the
//                                curated terminal/editor/IDE list.
//   client_type=1 (absolute path) — joined against DangerousTCCPaths
//                                    (Critical) or suspiciousPathPrefixes
//                                    (Important). Other paths silent.
func evaluateTCCRows(rows []TCCRow, byBundle map[string]DetectedApp, scope string) []Finding {
	var findings []Finding
	for _, r := range rows {
		if !r.IsAllowed() {
			continue
		}
		if r.ClientType == 1 {
			findings = append(findings, evaluatePathBasedRow(r, scope)...)
			continue
		}
		if r.ClientType != 0 {
			continue // unknown client type
		}
		app, known := byBundle[r.Client]
		if !known {
			continue
		}
		f := Finding{
			Affects: []string{app.Path},
		}
		switch r.Service {
		case tccFDA:
			f.ID = "tcc-fda-" + r.Client
			f.Severity = SevCritical
			f.Title = fmt.Sprintf("%s has Full Disk Access", app.Name)
			f.RemedyRef = "tcc-fda"
			f.BarItem = BarTerminalFDA
		case tccA11y:
			f.ID = "tcc-a11y-" + r.Client
			f.Severity = SevCritical
			f.Title = fmt.Sprintf("%s has Accessibility", app.Name)
			f.RemedyRef = "tcc-a11y"
			f.BarItem = BarTerminalA11y
		case tccScreen:
			f.ID = "tcc-screen-" + r.Client
			f.Severity = SevImportant
			f.Title = fmt.Sprintf("%s has Screen Recording", app.Name)
			f.RemedyRef = "tcc-screen"
			f.BarItem = BarTerminalScreen
		case tccEvents:
			target := r.IndirectObjectIdentifier
			if target == "" {
				continue // bare AppleEvents permission with no target — no signal
			}
			f.ID = "tcc-events-" + r.Client + "-" + target
			f.Severity = SevImportant
			targetLabel := target
			if name, ok := CredentialApps[target]; ok {
				f.Severity = SevCritical
				targetLabel = name + " (" + target + ")"
			}
			f.Title = fmt.Sprintf("%s can drive %s via AppleEvents", app.Name, targetLabel)
			f.RemedyRef = "tcc-events"
			f.BarItem = BarTerminalEvents
		default:
			continue // service we don't audit
		}
		f.Detail = fmt.Sprintf("Service: %s\nScope: %s-scope TCC.db\nClient: %s (bundle)", r.Service, scope, r.Client)
		findings = append(findings, f)
	}
	return findings
}

// evaluatePathBasedRow handles client_type=1 (absolute-path TCC
// clients) — anything in DangerousTCCPaths gets a Critical finding
// (with the bar item matching the service); paths under
// suspiciousPathPrefixes get Important. All other paths are silent
// to avoid noise from legitimate user installs.
//
// A TCC grant on /usr/bin/security with FDA, for example, means any
// process running as the user can `security find-generic-password
// -s charon -a ... -g` and silently read tokens — entirely bypassing
// charon's M4 keychain ACL. Same severity tier as the bundle-ID
// case; tagged to the same bar item.
func evaluatePathBasedRow(r TCCRow, scope string) []Finding {
	bar, sev, idPrefix := tccServiceMeta(r.Service)
	if bar == BarNone {
		return nil // service we don't audit
	}

	// Dangerous paths: surface as the same severity as the bundle-ID
	// case (Critical for FDA/A11y, Important for Screen/Events) but
	// always at minimum Critical for `/usr/bin/security` and
	// `/usr/bin/codesign` regardless of service — they're A1/A10
	// silent-bypass shaped.
	if reason, ok := DangerousTCCPaths[r.Client]; ok {
		// Bump to Critical for the universally-bad paths.
		switch r.Client {
		case "/usr/bin/security", "/usr/bin/codesign":
			sev = SevCritical
		}
		return []Finding{{
			ID:       idPrefix + "-path-" + sanitizePathID(r.Client),
			Severity: sev,
			Title:    fmt.Sprintf("%s has %s — %s", r.Client, tccServiceName(r.Service), reason),
			Detail: fmt.Sprintf(
				"Service: %s\nScope: %s-scope TCC.db\nClient: %s (path)\n\n%s",
				r.Service, scope, r.Client, reason),
			Affects:   []string{r.Client},
			RemedyRef: tccServiceRemedy(r.Service),
			BarItem:   bar,
		}}
	}

	// Suspicious-prefix paths: Important regardless of service
	// (downgraded from Critical because legitimate dev binaries
	// can land under /private/var/folders briefly).
	for _, prefix := range suspiciousPathPrefixes {
		if strings.HasPrefix(r.Client, prefix) {
			return []Finding{{
				ID:       idPrefix + "-suspath-" + sanitizePathID(r.Client),
				Severity: SevImportant,
				Title:    fmt.Sprintf("%s has %s — path is in a suspicious location", r.Client, tccServiceName(r.Service)),
				Detail: fmt.Sprintf(
					"Service: %s\nScope: %s-scope TCC.db\nClient: %s (path)\n\n"+
						"This path is under %s, which is normally only used for transient files. "+
						"A TCC grant here suggests a downloaded binary asked for permissions and the user accepted. "+
						"Verify that this binary should hold the grant; remove via System Settings if not.",
					r.Service, scope, r.Client, prefix),
				Affects:   []string{r.Client},
				RemedyRef: tccServiceRemedy(r.Service),
				BarItem:   bar,
			}}
		}
	}
	return nil
}

// tccServiceMeta returns the bar item, default severity, and ID prefix
// for a TCC service — shared between bundle and path code paths.
func tccServiceMeta(service string) (BarItem, Severity, string) {
	switch service {
	case tccFDA:
		return BarTerminalFDA, SevCritical, "tcc-fda"
	case tccA11y:
		return BarTerminalA11y, SevCritical, "tcc-a11y"
	case tccScreen:
		return BarTerminalScreen, SevImportant, "tcc-screen"
	case tccEvents:
		return BarTerminalEvents, SevImportant, "tcc-events"
	}
	return BarNone, SevHygiene, ""
}

func tccServiceName(service string) string {
	switch service {
	case tccFDA:
		return "Full Disk Access"
	case tccA11y:
		return "Accessibility"
	case tccScreen:
		return "Screen Recording"
	case tccEvents:
		return "AppleEvents"
	}
	return service
}

func tccServiceRemedy(service string) string {
	switch service {
	case tccFDA:
		return "tcc-fda"
	case tccA11y:
		return "tcc-a11y"
	case tccScreen:
		return "tcc-screen"
	case tccEvents:
		return "tcc-events"
	}
	return ""
}

// sanitizePathID turns an absolute path into a Finding-ID-safe suffix
// (no slashes / spaces / special chars). Reversible: the path lives
// in Affects.
func sanitizePathID(path string) string {
	r := strings.NewReplacer("/", "-", " ", "_", ".", "_")
	return strings.TrimPrefix(r.Replace(path), "-")
}
