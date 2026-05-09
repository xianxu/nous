package security

import (
	"bytes"
	"strings"
	"testing"
)

func TestLookupRemedy(t *testing.T) {
	if e := LookupRemedy("sip"); e == nil || e.Title == "" {
		t.Fatalf("LookupRemedy(\"sip\") missing or empty: %+v", e)
	}
	if e := LookupRemedy("not-a-ref"); e != nil {
		t.Errorf("LookupRemedy(unknown) = %+v, want nil", e)
	}
}

func TestEveryRemedyHasContent(t *testing.T) {
	for _, e := range Remedies {
		if e.Ref == "" || e.Title == "" || e.Why == "" || e.Fix == "" {
			t.Errorf("incomplete remedy: %+v", e)
		}
	}
}

func TestPrintRemedyShape(t *testing.T) {
	var buf bytes.Buffer
	// NoColor + fixed width keeps the test stable against ANSI escapes
	// and TTY size variation.
	PrintRemedy(&buf, LookupRemedy("sip"), RenderOptions{NoColor: true, Width: 80})
	out := buf.String()
	// Glamour transforms section headings and trims punctuation, so we
	// look for content tokens that survive any reasonable styling.
	for _, want := range []string{"sip", "Why", "Fix", "csrutil"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n%s", want, out)
		}
	}
}

func TestFindingRefsHaveRemedies(t *testing.T) {
	// Every RemedyRef emitted by checks should have a matching entry —
	// otherwise the inline hint points the user at a missing remedy.
	want := []string{
		"sip",      // check_sip.go
		"sudo",     // check_sudo.go
		"launchd",  // check_launchd.go
		"codesign", // check_codesign.go
	}
	for _, ref := range want {
		if LookupRemedy(ref) == nil {
			t.Errorf("check emits RemedyRef=%q with no Remedies entry", ref)
		}
	}
}
