package security

import "testing"

func TestParseDiskutilFileVault(t *testing.T) {
	const happyPrefix = `   Volume Name:               Macintosh HD
   Mounted:                   Yes
   Mount Point:               /
`
	cases := []struct {
		name     string
		out      string
		wantID   string
		wantSev  Severity
		wantNone bool
	}{
		{
			name:     "yes",
			out:      happyPrefix + "   FileVault:                 Yes\n",
			wantNone: true,
		},
		{
			name:    "no",
			out:     happyPrefix + "   FileVault:                 No\n",
			wantID:  "filevault-off",
			wantSev: SevImportant,
		},
		{
			name:     "yes-unlocked-extra",
			out:      happyPrefix + "   FileVault:                 Yes (Unlocked)\n",
			wantNone: true,
		},
		{
			name:    "unknown-value",
			out:     happyPrefix + "   FileVault:                 Pending\n",
			wantID:  "filevault-status-unknown",
			wantSev: SevInfo,
		},
		{
			name:    "missing-line",
			out:     happyPrefix + "   Encrypted:                 No\n",
			wantID:  "filevault-status-unknown",
			wantSev: SevInfo,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseDiskutilFileVault(tc.out)
			if tc.wantNone {
				if len(got) != 0 {
					t.Fatalf("got %d findings, want 0: %+v", len(got), got)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("got %d findings, want 1: %+v", len(got), got)
			}
			if got[0].ID != tc.wantID {
				t.Errorf("ID = %q, want %q", got[0].ID, tc.wantID)
			}
			if got[0].Severity != tc.wantSev {
				t.Errorf("Severity = %v, want %v", got[0].Severity, tc.wantSev)
			}
			if got[0].BarItem != BarFileVault {
				t.Errorf("BarItem = %v, want %v", got[0].BarItem, BarFileVault)
			}
		})
	}
}
