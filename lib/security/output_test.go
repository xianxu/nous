package security

import (
	"strings"
	"testing"
)

func TestReportExitCode(t *testing.T) {
	cases := []struct {
		name     string
		findings []Finding
		want     int
	}{
		{"empty", nil, 0},
		{"hygiene-only", []Finding{{Severity: SevHygiene}}, 0},
		{"info-only", []Finding{{Severity: SevInfo}}, 0},
		{"important", []Finding{{Severity: SevInfo}, {Severity: SevImportant}}, 1},
		{"critical-wins", []Finding{{Severity: SevImportant}, {Severity: SevCritical}}, 2},
		{"critical-only", []Finding{{Severity: SevCritical}}, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := Report{Findings: tc.findings}
			if got := r.ExitCode(); got != tc.want {
				t.Fatalf("ExitCode() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestReportPrintJSON(t *testing.T) {
	r := Report{Findings: []Finding{
		{ID: "f1", Severity: SevCritical, Title: "T1", RemedyRef: "ref"},
		{ID: "f2", Severity: SevHygiene, Title: "T2"},
	}}
	var buf strings.Builder
	if err := r.Print(&buf, PrintOptions{JSON: true}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		`"total": 2`,
		`"exit_code": 2`,
		`"critical": 1`,
		`"hygiene": 1`,
		`"severity": "CRITICAL"`,
		`"severity": "HYGIENE"`,
		`"remedy_ref": "ref"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("JSON output missing %q\n%s", want, out)
		}
	}
}

func TestBarStatusRollup(t *testing.T) {
	cases := []struct {
		name      string
		evaluated []BarItem
		findings  []Finding
		expect    map[BarItem]BarStatus
	}{
		{
			name:      "evaluated, no findings → pass",
			evaluated: []BarItem{BarSIP},
			expect:    map[BarItem]BarStatus{BarSIP: BarPass},
		},
		{
			name:      "not evaluated → skipped",
			evaluated: nil,
			expect:    map[BarItem]BarStatus{BarSIP: BarSkipped},
		},
		{
			name:      "info finding → review",
			evaluated: []BarItem{BarLaunchdPersistence},
			findings:  []Finding{{Severity: SevInfo, BarItem: BarLaunchdPersistence}},
			expect:    map[BarItem]BarStatus{BarLaunchdPersistence: BarReview},
		},
		{
			name:      "important finding → fail",
			evaluated: []BarItem{BarSigningKeyACL},
			findings:  []Finding{{Severity: SevImportant, BarItem: BarSigningKeyACL}},
			expect:    map[BarItem]BarStatus{BarSigningKeyACL: BarFail},
		},
		{
			name:      "critical wins over info on same bar",
			evaluated: []BarItem{BarSigningKeyACL},
			findings: []Finding{
				{Severity: SevInfo, BarItem: BarSigningKeyACL},
				{Severity: SevCritical, BarItem: BarSigningKeyACL},
			},
			expect: map[BarItem]BarStatus{BarSigningKeyACL: BarFail},
		},
		{
			name:      "hygiene-only → pass",
			evaluated: []BarItem{BarSIP},
			findings:  []Finding{{Severity: SevHygiene, BarItem: BarSIP}},
			expect:    map[BarItem]BarStatus{BarSIP: BarPass},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := Report{Findings: tc.findings, Evaluated: tc.evaluated}
			got := r.barStatuses()
			for b, want := range tc.expect {
				if got[b] != want {
					t.Errorf("bar %d: got %v, want %v", int(b), got[b], want)
				}
			}
		})
	}
}

func TestMarkEvaluatedIdempotent(t *testing.T) {
	r := Report{}
	r.MarkEvaluated(BarSIP)
	r.MarkEvaluated(BarSIP, BarSudoCache)
	r.MarkEvaluated(BarSIP)
	if len(r.Evaluated) != 2 {
		t.Errorf("expected 2 unique items, got %d: %+v", len(r.Evaluated), r.Evaluated)
	}
}

func TestReportPrintTextHidesHygiene(t *testing.T) {
	r := Report{Findings: []Finding{
		{ID: "h", Severity: SevHygiene, Title: "Hygiene-thing"},
		{ID: "c", Severity: SevCritical, Title: "Critical-thing"},
		{ID: "i", Severity: SevInfo, Title: "Info-thing"},
	}}
	var buf strings.Builder
	r.Print(&buf, PrintOptions{NoColor: true})
	out := buf.String()
	// Critical and Info show up in the per-finding list. Critical
	// before Info (severity-desc sort).
	cIdx := strings.Index(out, "[CRITICAL]")
	iIdx := strings.Index(out, "[INFO]")
	if !(cIdx >= 0 && cIdx < iIdx) {
		t.Fatalf("severity sort wrong: critical@%d info@%d\n%s", cIdx, iIdx, out)
	}
	// Hygiene per-finding tag must NOT appear in the printed list.
	if strings.Contains(out, "[HYGIENE]") {
		t.Errorf("hygiene findings should be hidden from text output:\n%s", out)
	}
	// But the count line should still show hygiene=1.
	if !strings.Contains(out, "hygiene=1") {
		t.Errorf("count line should still show hygiene=1:\n%s", out)
	}
	// Hidden-tail line should mention how many were suppressed.
	if !strings.Contains(out, "1 hygiene finding hidden") {
		t.Errorf("expected hidden-count tail line:\n%s", out)
	}
}

func TestReportCounts(t *testing.T) {
	r := Report{Findings: []Finding{
		{Severity: SevCritical}, {Severity: SevCritical},
		{Severity: SevImportant},
		{Severity: SevHygiene},
	}}
	c := r.Counts()
	if c[SevCritical] != 2 || c[SevImportant] != 1 || c[SevInfo] != 0 || c[SevHygiene] != 1 {
		t.Fatalf("counts = %+v", c)
	}
}
