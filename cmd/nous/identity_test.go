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

func TestValidateGithubUser(t *testing.T) {
	// Cases lifted from GitHub's documented username rules:
	// 1-39 chars, alphanumeric + single hyphens, no leading/trailing
	// hyphen, no consecutive hyphens.
	cases := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"simple", "emmatest42", false},
		{"hyphenated", "emma-test", false},
		{"single-char", "x", false},
		{"max-length", strings.Repeat("a", 39), false},
		{"empty", "", true},
		{"too-long", strings.Repeat("a", 40), true},
		{"leading-hyphen", "-emma", true},
		{"trailing-hyphen", "emma-", true},
		{"consecutive-hyphens", "em--ma", true},
		{"with-space", "emma test", true},
		{"with-email", "emma@test.local", true},
		{"with-dot", "emma.test", true},
		{"with-slash", "emma/test", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateGithubUser(c.input)
			gotErr := err != nil
			if gotErr != c.wantErr {
				t.Errorf("validateGithubUser(%q): wantErr=%v, got %v", c.input, c.wantErr, err)
			}
		})
	}
}

func TestPromptGithubUser_AcceptsValid(t *testing.T) {
	in := strings.NewReader("emmatest42\n")
	var out bytes.Buffer
	got, err := promptGithubUser(in, &out)
	if err != nil {
		t.Fatalf("promptGithubUser: %v", err)
	}
	if got != "emmatest42" {
		t.Errorf("got %q, want %q", got, "emmatest42")
	}
}

func TestPromptGithubUser_RejectsAfter3Invalid(t *testing.T) {
	in := strings.NewReader("\n--bad--\nemma test\n")
	var out bytes.Buffer
	_, err := promptGithubUser(in, &out)
	if err == nil {
		t.Errorf("expected failure after 3 invalid inputs")
	}
}

func TestPromptGithubUser_AcceptsAfterInvalid(t *testing.T) {
	in := strings.NewReader("emma test\nemmatest42\n")
	var out bytes.Buffer
	got, err := promptGithubUser(in, &out)
	if err != nil {
		t.Fatalf("promptGithubUser: %v", err)
	}
	if got != "emmatest42" {
		t.Errorf("got %q, want %q", got, "emmatest42")
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
