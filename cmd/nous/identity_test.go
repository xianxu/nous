package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/xianxu/nous/lib/brain"
	"github.com/xianxu/nous/lib/identity"
)

func TestPromptVerify_AcceptsCorrectFingerprint(t *testing.T) {
	in := strings.NewReader("abcdef01\n")
	var out bytes.Buffer
	if err := promptVerify(in, &out, "abcdef01"); err != nil {
		t.Errorf("promptVerify: %v", err)
	}
}

func TestPromptVerify_AcceptsCaseInsensitive(t *testing.T) {
	in := strings.NewReader("ABCDEF01\n")
	var out bytes.Buffer
	if err := promptVerify(in, &out, "abcdef01"); err != nil {
		t.Errorf("promptVerify case-insensitive: %v", err)
	}
}

func TestPromptVerify_TrimsWhitespace(t *testing.T) {
	in := strings.NewReader("  abcdef01  \n")
	var out bytes.Buffer
	if err := promptVerify(in, &out, "abcdef01"); err != nil {
		t.Errorf("promptVerify with whitespace: %v", err)
	}
}

func TestPromptVerify_RejectsAfter3Mismatches(t *testing.T) {
	in := strings.NewReader("wrong1\nwrong2\nwrong3\n")
	var out bytes.Buffer
	if err := promptVerify(in, &out, "abcdef01"); err == nil {
		t.Errorf("promptVerify should fail after 3 mismatches")
	}
	// Each attempt should print a "mismatch" hint.
	if got := strings.Count(out.String(), "mismatch"); got != 3 {
		t.Errorf("expected 3 mismatch hints in output, got %d:\n%s", got, out.String())
	}
}

func TestPromptVerify_AcceptsOnSecondAttempt(t *testing.T) {
	in := strings.NewReader("wrong\nabcdef01\n")
	var out bytes.Buffer
	if err := promptVerify(in, &out, "abcdef01"); err != nil {
		t.Errorf("promptVerify second-attempt: %v", err)
	}
}

func TestKeyBrains_FormatsAssignments(t *testing.T) {
	brains := []brain.Manifest{
		{Path: "/x/personal", Name: "personal", Recipients: []string{"FP1"}},
		{Path: "/x/family", Name: "family", Recipients: []string{"FP1", "FP2"}},
		{Path: "/x/empty", Name: "empty", Recipients: []string{"FP3"}},
	}

	tests := []struct {
		fp   string
		want string
	}{
		{"FP1", "[personal, family]"},
		{"FP2", "[family]"},
		{"FP3", "[empty]"},
		{"FPX", "(no brain)"},
		// Case-insensitive match against manifest values:
		{"fp1", "[personal, family]"},
	}
	for _, tt := range tests {
		got := keyBrains(identity.Key{Fingerprint: tt.fp}, brains)
		if got != tt.want {
			t.Errorf("keyBrains(%q) = %q, want %q", tt.fp, got, tt.want)
		}
	}
}

func TestKeyBrains_FallsBackToPathBaseWhenNameMissing(t *testing.T) {
	brains := []brain.Manifest{
		{Path: "/x/unnamed-brain", Name: "", Recipients: []string{"FP1"}},
	}
	got := keyBrains(identity.Key{Fingerprint: "FP1"}, brains)
	if got != "[unnamed-brain]" {
		t.Errorf("keyBrains fallback: got %q, want [unnamed-brain]", got)
	}
}
