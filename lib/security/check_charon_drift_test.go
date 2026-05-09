package security

import (
	"testing"
)

func TestExtractTrustedPath(t *testing.T) {
	cases := []struct {
		dr   string
		want string
	}{
		{`identifier "/Users/foo/.local/bin/charon"`, "/Users/foo/.local/bin/charon"},
		{`identifier "/usr/bin/security"`, "/usr/bin/security"},
		{`identifier "com.charon.cli" and anchor apple generic`, ""},
		{`identifier H"abc..."`, ""},
		{``, ""},
	}
	for _, tc := range cases {
		t.Run(tc.dr, func(t *testing.T) {
			if got := extractTrustedPath(tc.dr); got != tc.want {
				t.Errorf("extractTrustedPath(%q) = %q, want %q", tc.dr, got, tc.want)
			}
		})
	}
}

func TestDriftFindings(t *testing.T) {
	// Setup: pretend the install path is at a known location for the
	// duration of this test. driftFindings reads charonInstallPath
	// directly so we can override.
	orig := charonInstallPath
	t.Cleanup(func() { charonInstallPath = orig })
	charonInstallPath = "/Users/foo/.local/bin/charon"

	cases := []struct {
		name     string
		apps     []classifiedTrustedApp
		wantFire bool
	}{
		{
			name: "all expected, install path among trusted → silent",
			apps: []classifiedTrustedApp{
				{DR: `identifier "/Users/foo/.local/bin/charon"`, Verdict: verdictExpected},
			},
			wantFire: false,
		},
		{
			name: "expected DR points elsewhere → drift",
			apps: []classifiedTrustedApp{
				{DR: `identifier "/Applications/old-charon/Contents/MacOS/charon"`, Verdict: verdictExpected},
			},
			wantFire: true,
		},
		{
			name: "expected DR is bundle-ID (universal) → silent",
			apps: []classifiedTrustedApp{
				{DR: `identifier "com.charon.cli" and anchor apple generic`, Verdict: verdictExpected},
			},
			wantFire: false,
		},
		{
			name: "no expected entries → silent (drift check is for expected only)",
			apps: []classifiedTrustedApp{
				{DR: `identifier "/usr/bin/security"`, Verdict: verdictCatastrophic},
			},
			wantFire: false,
		},
		{
			name: "expected at install path AND elsewhere → silent (install path present)",
			apps: []classifiedTrustedApp{
				{DR: `identifier "/Users/foo/.local/bin/charon"`, Verdict: verdictExpected},
				{DR: `identifier "/opt/charon/bin/charon"`, Verdict: verdictExpected},
			},
			wantFire: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := driftFindings("charon/test:account", tc.apps)
			fired := len(got) > 0
			if fired != tc.wantFire {
				t.Errorf("fired = %v, want %v\nfindings: %+v", fired, tc.wantFire, got)
			}
			if fired && got[0].Severity != SevImportant {
				t.Errorf("severity = %v, want Important", got[0].Severity)
			}
		})
	}
}
