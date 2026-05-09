package security

import (
	"fmt"
	"os/exec"
	"strings"
)

// CheckSIP runs `csrutil status` and emits a Critical finding if SIP is
// disabled. SIP-disabled is rare on consumer Macs but devastating: it
// lets `sudo` users attach debuggers to signed binaries, modify system
// files, and generally invalidates everything below it in charon's
// defense layers.
func CheckSIP() []Finding {
	out, err := exec.Command("csrutil", "status").CombinedOutput()
	if err != nil {
		return []Finding{{
			ID:       "sip-error",
			Severity: SevImportant,
			Title:    "Could not determine SIP status",
			Detail: fmt.Sprintf("`csrutil status` failed: %v\n\n"+
				"Output: %s", err, string(out)),
			RemedyRef: "sip",
			BarItem:   BarSIP,
		}}
	}
	return parseSIPStatus(string(out))
}

// parseSIPStatus is split out for testability. csrutil's output is
// stable: "System Integrity Protection status: enabled." (or disabled,
// or "unknown" on a custom configuration).
func parseSIPStatus(output string) []Finding {
	lower := strings.ToLower(output)
	switch {
	case strings.Contains(lower, "status: enabled"):
		return nil
	case strings.Contains(lower, "status: disabled"):
		return []Finding{{
			ID:        "sip-disabled",
			Severity:  SevCritical,
			Title:     "System Integrity Protection is DISABLED",
			Detail:    "SIP being off invalidates charon's threat model entirely. Re-enable from Recovery: `csrutil enable`, then reboot.",
			RemedyRef: "sip",
			BarItem:   BarSIP,
		}}
	default:
		return []Finding{{
			ID:        "sip-unknown",
			Severity:  SevImportant,
			Title:     "SIP status is non-standard (custom configuration)",
			Detail:    "csrutil reported a non-enabled/non-disabled status — likely a partial SIP override (e.g. `csrutil enable --without debug`). Investigate.\n\nOutput:\n" + strings.TrimSpace(output),
			RemedyRef: "sip",
			BarItem:   BarSIP,
		}}
	}
}
