package security

import (
	"errors"
	"os/exec"
)

// CheckSudoCache reports whether the current shell has a live `sudo -n`
// credential cache. A cached sudo means any process spawned in this
// session can `sudo -n <anything>` without prompting — a footgun for
// agentic workflows where you may have just `sudo make install`'d.
//
// This is informational, not an architectural failure. The remedy is
// `sudo -k` to invalidate.
func CheckSudoCache() []Finding {
	// `sudo -nv` exits 0 if a cached credential is usable; nonzero
	// otherwise. We don't care about stderr beyond the exit code.
	err := exec.Command("sudo", "-nv").Run()
	if err == nil {
		return []Finding{{
			ID:        "sudo-cache-active",
			Severity:  SevInfo,
			Title:     "sudo credential cache is active in this shell",
			Detail:    "A subprocess in this session can call `sudo -n <anything>` without prompting. If you're about to launch an agent, run `sudo -k` first to invalidate.",
			RemedyRef: "sudo",
			BarItem:   BarSudoCache,
		}}
	}
	// Any error (including ExitError with nonzero code, or "sudo not
	// found") counts as "no usable cache" — the safe state.
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return nil
	}
	return nil
}
