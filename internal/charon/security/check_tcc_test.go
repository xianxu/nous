package security

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fixtureTCC builds a minimal TCC-shaped sqlite DB at the given path,
// writing rows via the sqlite3 CLI. Schema includes all columns we
// SELECT from in ReadTCC plus a few extras to mimic a real DB.
func fixtureTCC(t *testing.T, path string, rows []TCCRow) {
	t.Helper()
	createSQL := `CREATE TABLE access (
		service TEXT NOT NULL,
		client TEXT NOT NULL,
		client_type INTEGER NOT NULL,
		auth_value INTEGER NOT NULL,
		auth_reason INTEGER,
		auth_version INTEGER,
		csreq BLOB,
		policy_id INTEGER,
		indirect_object_identifier_type INTEGER,
		indirect_object_identifier TEXT,
		indirect_object_code_identity BLOB,
		flags INTEGER,
		last_modified INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (service, client, client_type, indirect_object_identifier)
	);`
	if err := exec.Command("/usr/bin/sqlite3", path, createSQL).Run(); err != nil {
		t.Fatalf("create table: %v", err)
	}
	for _, r := range rows {
		insert := strings.NewReplacer(
			"{{service}}", r.Service,
			"{{client}}", r.Client,
			"{{client_type}}", itoa(r.ClientType),
			"{{auth_value}}", itoa(r.AuthValue),
			"{{indirect}}", r.IndirectObjectIdentifier,
		).Replace(`INSERT INTO access (service, client, client_type, auth_value, indirect_object_identifier)
		           VALUES ('{{service}}', '{{client}}', {{client_type}}, {{auth_value}}, '{{indirect}}');`)
		if err := exec.Command("/usr/bin/sqlite3", path, insert).Run(); err != nil {
			t.Fatalf("insert row: %v", err)
		}
	}
}

func itoa(i int) string {
	switch i {
	case 0:
		return "0"
	case 1:
		return "1"
	case 2:
		return "2"
	case 3:
		return "3"
	}
	return "0"
}

func TestReadTCC_ParsesRows(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "TCC.db")
	want := []TCCRow{
		{Service: tccFDA, Client: "com.apple.Terminal", ClientType: 0, AuthValue: 2},
		{Service: tccA11y, Client: "com.googlecode.iterm2", ClientType: 0, AuthValue: 2},
		{Service: tccFDA, Client: "/usr/local/bin/foo", ClientType: 1, AuthValue: 0},
		{Service: tccEvents, Client: "com.microsoft.VSCode", ClientType: 0, AuthValue: 2,
			IndirectObjectIdentifier: "com.apple.keychainaccess"},
	}
	fixtureTCC(t, path, want)
	got, err := ReadTCC(path)
	if err != nil {
		t.Fatalf("ReadTCC: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("got %d rows, want %d: %+v", len(got), len(want), got)
	}
}

func TestReadTCC_MissingDB(t *testing.T) {
	rows, err := ReadTCC("/nonexistent/path/TCC.db")
	if err != nil {
		t.Fatalf("missing DB should return nil rows + nil err, got err=%v", err)
	}
	if rows != nil {
		t.Errorf("missing DB should return nil rows, got %+v", rows)
	}
}

func TestEvaluateTCCRows_SeverityMatrix(t *testing.T) {
	apps := map[string]DetectedApp{
		"com.apple.Terminal":     {KnownApp: KnownApp{BundleID: "com.apple.Terminal", Name: "Terminal", Category: CatTerminal}, Path: "/Applications/Terminal.app"},
		"com.microsoft.VSCode":   {KnownApp: KnownApp{BundleID: "com.microsoft.VSCode", Name: "VS Code", Category: CatEditor}, Path: "/Applications/VSCode.app"},
		"com.googlecode.iterm2":  {KnownApp: KnownApp{BundleID: "com.googlecode.iterm2", Name: "iTerm2", Category: CatTerminal}, Path: "/Applications/iTerm.app"},
		"com.github.wez.wezterm": {KnownApp: KnownApp{BundleID: "com.github.wez.wezterm", Name: "WezTerm", Category: CatTerminal}, Path: "/Applications/WezTerm.app"},
	}
	rows := []TCCRow{
		// Critical: FDA on terminal
		{Service: tccFDA, Client: "com.apple.Terminal", ClientType: 0, AuthValue: 2},
		// Critical: Accessibility on editor
		{Service: tccA11y, Client: "com.microsoft.VSCode", ClientType: 0, AuthValue: 2},
		// Important: ScreenCapture on terminal
		{Service: tccScreen, Client: "com.googlecode.iterm2", ClientType: 0, AuthValue: 2},
		// Critical: AppleEvents → credential app
		{Service: tccEvents, Client: "com.github.wez.wezterm", ClientType: 0, AuthValue: 2, IndirectObjectIdentifier: "com.apple.keychainaccess"},
		// Important: AppleEvents → non-credential target
		{Service: tccEvents, Client: "com.github.wez.wezterm", ClientType: 0, AuthValue: 2, IndirectObjectIdentifier: "com.apple.Music"},
		// Filtered: denied auth_value
		{Service: tccFDA, Client: "com.apple.Terminal", ClientType: 0, AuthValue: 0},
		// Filtered: client_type=1 (path-based)
		{Service: tccFDA, Client: "/usr/local/bin/foo", ClientType: 1, AuthValue: 2},
		// Filtered: unknown app
		{Service: tccFDA, Client: "com.unknown.app", ClientType: 0, AuthValue: 2},
		// Filtered: AppleEvents with empty target
		{Service: tccEvents, Client: "com.apple.Terminal", ClientType: 0, AuthValue: 2},
	}
	findings := evaluateTCCRows(rows, apps, "user")
	wantSeverity := []Severity{
		SevCritical,  // FDA on Terminal
		SevCritical,  // A11y on VS Code
		SevImportant, // Screen Recording on iTerm2
		SevCritical,  // Events to keychainaccess
		SevImportant, // Events to Music
	}
	if len(findings) != len(wantSeverity) {
		t.Fatalf("got %d findings, want %d:\n%+v", len(findings), len(wantSeverity), findings)
	}
	for i, want := range wantSeverity {
		if findings[i].Severity != want {
			t.Errorf("[%d] severity = %v, want %v (%s)", i, findings[i].Severity, want, findings[i].Title)
		}
	}
}

func TestEvaluateTCCRows_PathBased(t *testing.T) {
	apps := map[string]DetectedApp{} // not relevant for path-based rows
	rows := []TCCRow{
		// Critical: /usr/bin/security with FDA — universally bad
		{Service: tccFDA, Client: "/usr/bin/security", ClientType: 1, AuthValue: 2},
		// Critical: /usr/bin/codesign with anything (here: A11y)
		{Service: tccA11y, Client: "/usr/bin/codesign", ClientType: 1, AuthValue: 2},
		// Critical: dangerous shell with FDA
		{Service: tccFDA, Client: "/bin/bash", ClientType: 1, AuthValue: 2},
		// Important: dangerous shell with Screen Recording
		{Service: tccScreen, Client: "/bin/zsh", ClientType: 1, AuthValue: 2},
		// Important: suspicious-prefix path with FDA
		{Service: tccFDA, Client: "/private/tmp/foo", ClientType: 1, AuthValue: 2},
		// Silent: legitimate path not on either list
		{Service: tccFDA, Client: "/usr/local/bin/git", ClientType: 1, AuthValue: 2},
		// Silent: denied auth_value
		{Service: tccFDA, Client: "/usr/bin/security", ClientType: 1, AuthValue: 0},
	}
	findings := evaluateTCCRows(rows, apps, "user")
	wantSeverity := []Severity{
		SevCritical,  // security FDA
		SevCritical,  // codesign A11y (bumped from Important)
		SevCritical,  // bash FDA
		SevImportant, // zsh ScreenCapture
		SevImportant, // /private/tmp/foo FDA (suspicious prefix)
	}
	if len(findings) != len(wantSeverity) {
		t.Fatalf("got %d findings, want %d:\n%+v", len(findings), len(wantSeverity), findings)
	}
	for i, want := range wantSeverity {
		if findings[i].Severity != want {
			t.Errorf("[%d] severity = %v, want %v (%s)", i, findings[i].Severity, want, findings[i].Title)
		}
	}
}

func TestEvaluateTCCRows_LimitedAuthValueAllowed(t *testing.T) {
	// auth_value=3 is "limited" (e.g. Documents only). For our coarse
	// audit, any non-denied grant on a terminal still surfaces.
	apps := map[string]DetectedApp{
		"com.apple.Terminal": {KnownApp: KnownApp{BundleID: "com.apple.Terminal", Name: "Terminal"}, Path: "/Applications/Terminal.app"},
	}
	rows := []TCCRow{{Service: tccFDA, Client: "com.apple.Terminal", ClientType: 0, AuthValue: 3}}
	findings := evaluateTCCRows(rows, apps, "user")
	if len(findings) != 1 || findings[0].Severity != SevCritical {
		t.Fatalf("expected 1 critical finding for limited grant, got %+v", findings)
	}
}
