// nous service doctor: prescriptive health check across the substrate.
// Each check is OK / fail; failures name a specific fix. Audience is
// the operator after something broke ("why isn't sync working?") or
// after a fresh install ("did I miss a step?"). Read-only — never
// auto-fixes.
package main

import (
	"bytes"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
	"github.com/xianxu/nous/lib/brain"
	"github.com/xianxu/nous/lib/brainsync"
	"github.com/xianxu/nous/lib/identity"
	"github.com/xianxu/nous/lib/service"
)

func newServiceDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Prescriptive health check across gpg, identity, brains, and services",
		Long: `Walks a battery of checks and prints OK or FAIL for each. Each
failure names a specific fix.

Read-only: doctor never modifies state. Run after install, after errors,
or anytime the substrate feels off.

Exit status: 0 if all checks pass, 1 if any check fails.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDoctor(cmd.OutOrStdout())
		},
	}
}

// checkResult is the per-check verdict. Msg always describes the
// observation; Fix is non-empty only when OK is false. Format is the
// only contract — callers don't compose results.
type checkResult struct {
	OK  bool
	Msg string
	Fix string
}

func ok(msg string) checkResult       { return checkResult{OK: true, Msg: msg} }
func fail(msg, fix string) checkResult { return checkResult{Msg: msg, Fix: fix} }

func runDoctor(w io.Writer) error {
	checks := []struct {
		name string
		run  func() checkResult
	}{
		{"gpg installed", checkGPGInstalled},
		{"gpg-agent reachable", checkGPGAgent},
		{"identity exists in keyring", checkIdentityExists},
		{"brain manifests parse", checkBrainsParse},
		{"recipient fingerprints present in keyring", checkRecipientsInKeyring},
		{"charon service installed", checkCharonInstalled},
		{"brain-sync service installed", checkBrainSyncInstalled},
		{"charon service running", checkCharonRunning},
		{"brain-sync service running", checkBrainSyncRunning},
	}

	failed := 0
	for _, c := range checks {
		r := c.run()
		if r.OK {
			fmt.Fprintf(w, "  [ok]   %s — %s\n", c.name, r.Msg)
			continue
		}
		failed++
		fmt.Fprintf(w, "  [FAIL] %s — %s\n", c.name, r.Msg)
		if r.Fix != "" {
			for _, line := range strings.Split(r.Fix, "\n") {
				fmt.Fprintf(w, "         fix: %s\n", line)
			}
		}
	}

	fmt.Fprintln(w)
	if failed == 0 {
		fmt.Fprintln(w, "All checks passed.")
		return nil
	}
	return fmt.Errorf("%d check(s) failed", failed)
}

// ─── checks ──────────────────────────────────────────────────────────

func checkGPGInstalled() checkResult {
	if _, err := exec.LookPath("gpg"); err != nil {
		return fail("gpg not on PATH", "brew install gnupg")
	}
	out, err := exec.Command("gpg", "--version").Output()
	if err != nil {
		return fail(fmt.Sprintf("gpg --version failed: %v", err), "brew reinstall gnupg")
	}
	first, _, _ := bytes.Cut(out, []byte("\n"))
	return ok(string(first))
}

func checkGPGAgent() checkResult {
	c := exec.Command("gpg-connect-agent", "/bye")
	if err := c.Run(); err != nil {
		return fail("gpg-agent not reachable", "gpgconf --launch gpg-agent  (or: nous identity init to bootstrap pinentry-mac)")
	}
	return ok("agent responding")
}

func checkIdentityExists() checkResult {
	keys, err := identity.List()
	if err != nil {
		return fail(fmt.Sprintf("listing keys: %v", err), "nous identity init")
	}
	if len(keys) == 0 {
		return fail("no secret key in keyring", "nous identity init")
	}
	if len(keys) == 1 {
		return ok(fmt.Sprintf("%s (%s)", keys[0].Last8(), keys[0].UID))
	}
	return ok(fmt.Sprintf("%d secret keys in keyring", len(keys)))
}

func checkBrainsParse() checkResult {
	brains, err := brain.DiscoverAll()
	if err != nil {
		return fail(fmt.Sprintf("discovery: %v", err), "check $WORKSPACE_ROOT or $HOME/workspace permissions")
	}
	if len(brains) == 0 {
		return ok("no brains under workspace root (nothing to validate)")
	}
	var names []string
	for _, b := range brains {
		names = append(names, b.Name)
	}
	return ok(fmt.Sprintf("%d brain(s): %s", len(brains), strings.Join(names, ", ")))
}

func checkRecipientsInKeyring() checkResult {
	brains, err := brain.DiscoverAll()
	if err != nil {
		return fail(fmt.Sprintf("discovery: %v", err), "check workspace root permissions")
	}
	if len(brains) == 0 {
		return ok("no brains; nothing to check")
	}
	secret, err := identity.List()
	if err != nil {
		return fail(fmt.Sprintf("listing secret keys: %v", err), "")
	}
	pub, err := identity.ListPublic()
	if err != nil {
		return fail(fmt.Sprintf("listing public keys: %v", err), "")
	}
	known := make(map[string]bool)
	for _, k := range secret {
		known[strings.ToUpper(k.Fingerprint)] = true
	}
	for _, k := range pub {
		known[strings.ToUpper(k.Fingerprint)] = true
	}

	var missing []string
	for _, b := range brains {
		for _, fp := range b.Recipients {
			if !known[strings.ToUpper(fp)] {
				missing = append(missing, fmt.Sprintf("%s (recipient of %s)", fp, b.Name))
			}
		}
	}
	if len(missing) > 0 {
		return fail(
			fmt.Sprintf("%d recipient fingerprint(s) not in keyring", len(missing)),
			"import each missing pubkey:\n              "+strings.Join(missing, "\n              ")+"\n         via: nous identity import <pubkey-file>",
		)
	}
	total := 0
	for _, b := range brains {
		total += len(b.Recipients)
	}
	return ok(fmt.Sprintf("%d recipient(s) across %d brain(s) all known", total, len(brains)))
}

func checkCharonInstalled() checkResult {
	mgr, err := service.New()
	if err != nil {
		return fail(fmt.Sprintf("manager init: %v", err), "")
	}
	status, _ := mgr.Status()
	if strings.Contains(strings.ToLower(status), "not installed") {
		return fail("charon launchd service not installed", "nous service install")
	}
	return ok("plist present")
}

func checkBrainSyncInstalled() checkResult {
	mgr, err := brainsync.NewServiceManager()
	if err != nil {
		return fail(fmt.Sprintf("manager init: %v", err), "")
	}
	status, _ := mgr.Status()
	if strings.Contains(strings.ToLower(status), "not installed") {
		return fail("brain-sync launchd service not installed", "nous service install")
	}
	return ok("plist present")
}

func checkCharonRunning() checkResult {
	mgr, err := service.New()
	if err != nil {
		return fail(fmt.Sprintf("manager init: %v", err), "")
	}
	status, err := mgr.Status()
	if err != nil {
		return fail(fmt.Sprintf("status query: %v", err), "nous service start")
	}
	if !strings.Contains(strings.ToLower(status), "running") && !strings.Contains(status, "PID") {
		return fail(strings.TrimSpace(status), "nous service start")
	}
	return ok(strings.TrimSpace(status))
}

func checkBrainSyncRunning() checkResult {
	mgr, err := brainsync.NewServiceManager()
	if err != nil {
		return fail(fmt.Sprintf("manager init: %v", err), "")
	}
	status, err := mgr.Status()
	if err != nil {
		return fail(fmt.Sprintf("status query: %v", err), "nous service start")
	}
	if !strings.Contains(strings.ToLower(status), "running") && !strings.Contains(status, "PID") {
		return fail(strings.TrimSpace(status), "nous service start")
	}
	return ok(strings.TrimSpace(status))
}

