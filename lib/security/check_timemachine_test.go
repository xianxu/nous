package security

import (
	"strings"
	"testing"
)

func TestParseTmDestinations(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
	}{
		{
			name: "no destinations",
			in:   "tmutil: No destinations configured.\n",
			want: 0,
		},
		{
			name: "empty",
			in:   "",
			want: 0,
		},
		{
			name: "one local",
			in: `====================================================
Name          : Foo
Kind          : Local
Mount Point   : /Volumes/Foo
ID            : abc-123
`,
			want: 1,
		},
		{
			name: "two destinations mixed kinds",
			in: `====================================================
Name          : Local Drive
Kind          : Local
Mount Point   : /Volumes/Backup
ID            : abc-123
====================================================
Name          : Network NAS
Kind          : Network
URL           : afp://nas.local/Backup
ID            : def-456
`,
			want: 2,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseTmDestinations(tc.in)
			if len(got) != tc.want {
				t.Fatalf("got %d destinations, want %d: %+v", len(got), tc.want, got)
			}
		})
	}
}

func TestEvaluateTmDestinations(t *testing.T) {
	const twoMixed = `====================================================
Name          : EncryptedBackup
Kind          : Local
Mount Point   : /Volumes/Encrypted
ID            : abc
====================================================
Name          : PlaintextBackup
Kind          : Local
Mount Point   : /Volumes/Plain
ID            : def
====================================================
Name          : NetworkNAS
Kind          : Network
URL           : afp://nas.local/Backup
ID            : ghi
`
	// Synthetic encryption oracle.
	oracle := func(mp string) (bool, bool) {
		switch mp {
		case "/Volumes/Encrypted":
			return true, true
		case "/Volumes/Plain":
			return false, true
		}
		return false, false // unknown
	}
	findings := evaluateTmDestinations(twoMixed, oracle)
	// Expected: silent on encrypted, Important on unencrypted, Info on network.
	if len(findings) != 2 {
		t.Fatalf("got %d findings, want 2: %+v", len(findings), findings)
	}
	wantSeverities := []Severity{SevImportant, SevInfo}
	for i, want := range wantSeverities {
		if findings[i].Severity != want {
			t.Errorf("[%d] severity = %v, want %v (%s)", i, findings[i].Severity, want, findings[i].Title)
		}
	}
	if !strings.Contains(findings[0].Title, "UNENCRYPTED") {
		t.Errorf("expected unencrypted finding, got: %s", findings[0].Title)
	}
}

func TestEvaluateTmDestinations_NoDestinations(t *testing.T) {
	got := evaluateTmDestinations("tmutil: No destinations configured.", nil)
	if len(got) != 0 {
		t.Fatalf("got %d findings on empty TM, want 0: %+v", len(got), got)
	}
}
