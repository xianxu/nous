package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"golang.org/x/term"

	"github.com/xianxu/nous/lib/identity"
)

// runAddCmd executes `brain recipient add` with the given args, returning
// the error. It captures output so failing runs don't spam test logs.
func runAddCmd(args ...string) error {
	cmd := newBrainRecipientAddCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)
	return cmd.Execute()
}

const ttyGateMsg = "interactive terminal"

// The TTY gate is the first statement in RunE, shared by both the import
// (BRAIN + PUBKEY-FILE) and fingerprint (--fingerprint) paths. Without
// --verified-last8 it fires immediately for both — before any gpg/file IO —
// so these assertions are fast and gpg-free.
func TestRecipientAdd_GateFires_BothPaths(t *testing.T) {
	if term.IsTerminal(int(os.Stdin.Fd())) {
		t.Skip("stdin is a TTY; the no-flag gate assertion only holds when stdin is non-interactive")
	}
	importErr := runAddCmd("/no/such/brain", "/no/such/pubkey.pub")
	if importErr == nil || !strings.Contains(importErr.Error(), ttyGateMsg) {
		t.Errorf("import path without --verified-last8 should hit the TTY gate; got: %v", importErr)
	}
	fpErr := runAddCmd("/no/such/brain", "--fingerprint", "deadbeef")
	if fpErr == nil || !strings.Contains(fpErr.Error(), ttyGateMsg) {
		t.Errorf("fingerprint path without --verified-last8 should hit the TTY gate; got: %v", fpErr)
	}
}

// With --verified-last8 the gate lifts: the import path proceeds past the gate
// and fails later reading the bogus pubkey (NOT with the TTY message). Stays
// fast because importPubkeyFromFile reads the file before touching gpg.
func TestRecipientAdd_GateLifts_ImportPath(t *testing.T) {
	err := runAddCmd("/no/such/brain", "/no/such/pubkey.pub", "--verified-last8", "deadbeef")
	if err != nil && strings.Contains(err.Error(), ttyGateMsg) {
		t.Errorf("import path with --verified-last8 should clear the TTY gate; got: %v", err)
	}
	if err == nil || !strings.Contains(err.Error(), "read pubkey") {
		t.Errorf("expected a downstream 'read pubkey' failure after the gate lifts; got: %v", err)
	}
}

// confirmKey is the fingerprint path's verify step. Testing it directly (no
// gpg lookup) proves that path threads --verified-last8 into verifyLast8: a
// matching value passes without prompting, a wrong one errors.
func TestConfirmKey_ThreadsVerifiedLast8(t *testing.T) {
	key := identity.Key{Fingerprint: "0123456789ABCDEF0123456789ABCDEF01ABCDEF"}
	want := key.Last8() // last-8 of the fingerprint

	var out bytes.Buffer
	if err := confirmKey(&out, key, want); err != nil {
		t.Errorf("confirmKey with matching --verified-last8 should pass: %v", err)
	}
	if strings.Contains(out.String(), "Type the last-8") {
		t.Errorf("confirmKey with a matching value should not prompt; got:\n%s", out.String())
	}

	var out2 bytes.Buffer
	if err := confirmKey(&out2, key, "deadbeef"); err == nil {
		t.Error("confirmKey with a non-matching --verified-last8 should error")
	}
}
