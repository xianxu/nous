package security

import (
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// CheckTimeMachine inspects configured Time Machine destinations
// and emits findings for any local destination whose volume is
// unencrypted. Closes the C1 (stolen / unencrypted backup) attack
// path: even with FileVault on, an unencrypted external Time Machine
// drive contains plaintext copies of your keychain database.
//
// No configured destinations → silent (you don't have a TM backup
// to worry about).
//
// Network destinations (AFP/SMB) → Info-tier finding noting we
// can't check encryption status programmatically (it's a property
// of the remote server and the SMB session).
func CheckTimeMachine() []Finding {
	out, err := exec.Command("tmutil", "destinationinfo").CombinedOutput()
	if err != nil {
		return []Finding{{
			ID:       "tm-status-error",
			Severity: SevImportant,
			Title:    "Could not query Time Machine destinations",
			Detail:   fmt.Sprintf("`tmutil destinationinfo` failed: %v\n\nOutput:\n%s", err, string(out)),
			BarItem:  BarTimeMachine,
		}}
	}
	return evaluateTmDestinations(string(out), encryptedFromDiskutil)
}

// tmDestination is one parsed entry from `tmutil destinationinfo`.
type tmDestination struct {
	Name       string
	Kind       string // "Local" | "Network" | other
	MountPoint string // for local destinations
	URL        string // for network destinations
}

// evaluateTmDestinations is the pure-ish core: parses tmutil output,
// classifies each destination, queries encryption via the supplied
// callback (so tests can inject a fake). Production calls
// encryptedFromDiskutil.
func evaluateTmDestinations(out string, isEncrypted func(mountPoint string) (encrypted bool, ok bool)) []Finding {
	dests := parseTmDestinations(out)
	if len(dests) == 0 {
		return nil
	}

	var findings []Finding
	for _, d := range dests {
		switch d.Kind {
		case "Local":
			if d.MountPoint == "" {
				findings = append(findings, Finding{
					ID:       "tm-no-mount-point-" + safeLabel(d.Name),
					Severity: SevInfo,
					Title:    fmt.Sprintf("Time Machine destination %q has no mount point", d.Name),
					Detail:   "tmutil reported a Local destination without a Mount Point — drive may be disconnected. Encryption status not checked.",
					BarItem:  BarTimeMachine,
				})
				continue
			}
			encrypted, ok := isEncrypted(d.MountPoint)
			if !ok {
				findings = append(findings, Finding{
					ID:       "tm-encryption-unknown-" + safeLabel(d.Name),
					Severity: SevInfo,
					Title:    fmt.Sprintf("Time Machine destination %q encryption status unknown", d.Name),
					Detail:   fmt.Sprintf("Could not determine encryption status of %s. Inspect manually via `diskutil info '%s' | grep -i encrypt`.", d.MountPoint, d.MountPoint),
					BarItem:  BarTimeMachine,
				})
				continue
			}
			if !encrypted {
				findings = append(findings, Finding{
					ID:        "tm-unencrypted-" + safeLabel(d.Name),
					Severity:  SevImportant,
					Title:     fmt.Sprintf("Time Machine destination %q is UNENCRYPTED", d.Name),
					Detail:    fmt.Sprintf("Backup volume %s (mounted at %s) is not encrypted. An attacker who acquires the drive (theft, repair handover, etc.) can read raw keychain database files from the backup — bypassing FileVault and the M4 ACL boundary.\n\nFix: in System Settings → Time Machine → Options, change the backup destination to an encrypted volume. Or wipe the existing volume as APFS Encrypted via Disk Utility, then add it back to Time Machine.\n\nNote: if you have older backups on the unencrypted volume that you no longer need, securely erase the volume after the migration.", d.MountPoint, d.MountPoint),
					RemedyRef: "filevault", // shares the C1 / at-rest remedy
					Affects:   []string{d.MountPoint},
					BarItem:   BarTimeMachine,
				})
			}
		case "Network":
			findings = append(findings, Finding{
				ID:       "tm-network-destination-" + safeLabel(d.Name),
				Severity: SevInfo,
				Title:    fmt.Sprintf("Time Machine destination %q is on a network share", d.Name),
				Detail:   fmt.Sprintf("URL: %s\n\nNetwork TM destinations (AFP/SMB) inherit encryption from the server / share configuration. The audit can't verify this programmatically. Confirm manually that the share is configured with at-rest encryption (or that the remote server's filesystem is encrypted).", d.URL),
				BarItem:  BarTimeMachine,
			})
		}
	}
	return findings
}

// destBlockSep is the row of '=' chars tmutil prints between
// destination blocks. Splitting on this isolates each destination.
var destBlockSep = regexp.MustCompile(`(?m)^=+\s*$`)

// parseTmDestinations splits tmutil destinationinfo output into
// destination structs. Output looks like:
//
//	====================================================
//	Name          : Foo
//	Kind          : Local
//	Mount Point   : /Volumes/Foo
//	ID            : ...
//	====================================================
//	Name          : Bar
//	Kind          : Network
//	URL           : afp://server/share
//	ID            : ...
//
// Empty input or "No destinations configured." returns nil.
func parseTmDestinations(out string) []tmDestination {
	out = strings.TrimSpace(out)
	if out == "" || strings.Contains(out, "No destinations configured") {
		return nil
	}
	var dests []tmDestination
	for _, block := range destBlockSep.Split(out, -1) {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		var d tmDestination
		for _, line := range strings.Split(block, "\n") {
			line = strings.TrimSpace(line)
			key, val, ok := splitFieldColon(line)
			if !ok {
				continue
			}
			switch key {
			case "Name":
				d.Name = val
			case "Kind":
				d.Kind = val
			case "Mount Point":
				d.MountPoint = val
			case "URL":
				d.URL = val
			}
		}
		if d.Name == "" && d.Kind == "" {
			continue
		}
		dests = append(dests, d)
	}
	return dests
}

// splitFieldColon parses a "Key   :  Value" line. Returns (key, val,
// true) on success.
func splitFieldColon(line string) (key, val string, ok bool) {
	idx := strings.Index(line, ":")
	if idx < 0 {
		return "", "", false
	}
	return strings.TrimSpace(line[:idx]), strings.TrimSpace(line[idx+1:]), true
}

// encryptedFromDiskutil queries `diskutil info <mountPoint>` and
// looks for `Encrypted: Yes` / `FileVault: Yes`. Returns
// (encrypted=true/false, ok=true) when the line is present, or
// (false, ok=false) when neither line appears.
//
// Both lines exist on APFS encrypted volumes; either is sufficient
// signal. We accept whichever we see first.
func encryptedFromDiskutil(mountPoint string) (encrypted bool, ok bool) {
	out, err := exec.Command("diskutil", "info", mountPoint).CombinedOutput()
	if err != nil {
		return false, false
	}
	for _, line := range strings.Split(string(out), "\n") {
		trimmed := strings.TrimSpace(line)
		key, val, hasField := splitFieldColon(trimmed)
		if !hasField {
			continue
		}
		switch key {
		case "Encrypted", "FileVault":
			if strings.HasPrefix(val, "Yes") {
				return true, true
			}
			if strings.HasPrefix(val, "No") {
				return false, true
			}
		}
	}
	return false, false
}
