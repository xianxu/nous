package security

import (
	"fmt"
	"os/exec"
	"strings"
)

// CheckFileVault runs `diskutil info /` and parses the FileVault
// line. Returns an Important finding when FileVault is off.
//
// Why diskutil and not fdesetup: on macOS 26 (Tahoe), `fdesetup
// status` fails with "Unknown volume or device specifier: '/'" —
// Apple appears to have changed the tool's expected invocation (or
// removed it entirely). diskutil's per-volume info has been stable
// across macOS versions and exposes the same "FileVault: Yes/No"
// signal at the boot volume level.
//
// FileVault encrypts the boot volume at rest. Without it, an
// attacker who steals the laptop (or a Time Machine backup of it)
// can mount the drive on another Mac and read everything as
// plaintext — including the login keychain file. Charon's M4 ACL
// only protects against the live API path; the file-system path
// is a separate concern that bottlenecks on FileVault. Threat-model
// adversary C1.
func CheckFileVault() []Finding {
	out, err := exec.Command("diskutil", "info", "/").CombinedOutput()
	if err != nil {
		return []Finding{{
			ID:       "filevault-status-error",
			Severity: SevImportant,
			Title:    "Could not determine FileVault status",
			Detail:   fmt.Sprintf("`diskutil info /` failed: %v\n\nOutput: %s", err, string(out)),
			BarItem:  BarFileVault,
		}}
	}
	return parseDiskutilFileVault(string(out))
}

// parseDiskutilFileVault is split out for testability. The boot
// volume's diskutil info contains a `FileVault: Yes` or
// `FileVault: No` line on every supported macOS. Other variants
// like "Yes (Unlocked)" exist on data volumes; for the boot volume
// we expect a clean Yes/No.
func parseDiskutilFileVault(out string) []Finding {
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "FileVault:") {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(trimmed, "FileVault:"))
		switch {
		case strings.HasPrefix(value, "Yes"):
			return nil // healthy
		case strings.HasPrefix(value, "No"):
			return []Finding{{
				ID:        "filevault-off",
				Severity:  SevImportant,
				Title:     "FileVault is OFF — disk is unencrypted at rest",
				Detail:    "Without FileVault, anyone who gets physical access to your Mac (theft, customs, repair) can mount the drive and read the keychain database directly — bypassing charon's M4 ACL entirely (the ACL gates the live API path; raw file access is a separate concern). Enable FileVault in System Settings → Privacy & Security → FileVault. Initial encryption takes minutes to hours depending on disk size and runs in the background. Recovery key — STORE IT SECURELY (1Password, paper in a safe, etc.). Without it, password loss = data loss.",
				RemedyRef: "filevault",
				BarItem:   BarFileVault,
			}}
		default:
			return []Finding{{
				ID:        "filevault-status-unknown",
				Severity:  SevInfo,
				Title:     "diskutil returned unexpected FileVault state",
				Detail:    fmt.Sprintf("Expected `FileVault: Yes` or `FileVault: No` on the boot volume. Got: `FileVault: %s`", value),
				BarItem:   BarFileVault,
			}}
		}
	}
	return []Finding{{
		ID:       "filevault-status-unknown",
		Severity: SevInfo,
		Title:    "diskutil output didn't include a FileVault line",
		Detail:   "Expected a `FileVault: Yes/No` line in the boot volume's diskutil info; none found.",
		BarItem:  BarFileVault,
	}}
}
