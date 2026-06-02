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

// initInputsComplete gates whether `identity init` may skip the TTY: both
// name and email must be present (nous#36 M3).
func TestInitInputsComplete(t *testing.T) {
	cases := []struct {
		name, email string
		want        bool
	}{
		{"Ying Test", "ying@example.com", true},
		{"Ying Test", "", false},
		{"", "ying@example.com", false},
		{"", "", false},
	}
	for _, c := range cases {
		if got := initInputsComplete(c.name, c.email); got != c.want {
			t.Errorf("initInputsComplete(%q,%q) = %v, want %v", c.name, c.email, got, c.want)
		}
	}
}

// ── verifyLast8 (--verified-last8 non-interactive ceremony, nous#36) ──

// Assumed value matching the expected last-8 returns nil WITHOUT touching
// the prompt reader. The empty `in` would make the prompt path error, so a
// nil result proves the prompt was skipped.
func TestVerifyLast8_AssumedMatch_SkipsPrompt(t *testing.T) {
	var out bytes.Buffer
	if err := verifyLast8(strings.NewReader(""), &out, "abcdef01", "abcdef01"); err != nil {
		t.Fatalf("verifyLast8 assumed-match: %v", err)
	}
	if strings.Contains(out.String(), "Type the last-8") {
		t.Errorf("assumed-match should not prompt; got:\n%s", out.String())
	}
}

// Assumed match is case-insensitive and whitespace-trimmed, like the prompt.
func TestVerifyLast8_AssumedMatch_CaseInsensitiveAndTrimmed(t *testing.T) {
	var out bytes.Buffer
	if err := verifyLast8(strings.NewReader(""), &out, "abcdef01", "  ABCDEF01\n"); err != nil {
		t.Errorf("verifyLast8 case/space-insensitive assumed match: %v", err)
	}
}

// A non-empty assumed value that does NOT match the key errors (the scripted
// equivalent of failing the ceremony) and names the flag.
func TestVerifyLast8_AssumedMismatch_Errors(t *testing.T) {
	var out bytes.Buffer
	err := verifyLast8(strings.NewReader("abcdef01\n"), &out, "abcdef01", "deadbeef")
	if err == nil {
		t.Fatal("verifyLast8 should reject a non-matching --verified-last8")
	}
	if !strings.Contains(err.Error(), "verified-last8") {
		t.Errorf("error should mention the flag; got: %v", err)
	}
}

// Empty assumed value falls back to the interactive prompt: it reads from
// `in`, accepts a correct line, and errors when the reader is exhausted.
func TestVerifyLast8_Empty_FallsBackToPrompt(t *testing.T) {
	var out bytes.Buffer
	if err := verifyLast8(strings.NewReader("abcdef01\n"), &out, "abcdef01", ""); err != nil {
		t.Errorf("verifyLast8 empty-assumed should accept correct prompt input: %v", err)
	}
	if !strings.Contains(out.String(), "Type the last-8") {
		t.Errorf("empty-assumed should fall back to the prompt; got:\n%s", out.String())
	}
	var out2 bytes.Buffer
	if err := verifyLast8(strings.NewReader(""), &out2, "abcdef01", ""); err == nil {
		t.Error("verifyLast8 empty-assumed with no prompt input should error")
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
