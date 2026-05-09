package security

import "testing"

func TestParseSIPStatus(t *testing.T) {
	cases := []struct {
		name        string
		out         string
		wantID      string
		wantSev     Severity
		wantNoFinds bool
	}{
		{
			name:        "enabled",
			out:         "System Integrity Protection status: enabled.\n",
			wantNoFinds: true,
		},
		{
			name:    "disabled",
			out:     "System Integrity Protection status: disabled.\n",
			wantID:  "sip-disabled",
			wantSev: SevCritical,
		},
		{
			name: "custom config",
			out: "System Integrity Protection status: unknown (Custom Configuration).\n" +
				"Configuration:\n\tApple Internal: disabled\n",
			wantID:  "sip-unknown",
			wantSev: SevImportant,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseSIPStatus(tc.out)
			if tc.wantNoFinds {
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
		})
	}
}
