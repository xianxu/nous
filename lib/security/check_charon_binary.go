package security

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// nousInstallPath is where `make nous-install` places the unified nous
// binary. Hardcoded to match the Makefile target; deliberately not
// parameterized — alternate install paths (homebrew, /usr/local/bin)
// would need per-path attestation logic which we punt on for now.
var nousInstallPath = filepath.Join(os.Getenv("HOME"), ".local", "bin", "nous")

// CheckCharonBinary attests that the installed nous binary at
// ~/.local/bin/nous has the codesign properties charon's threat
// model relies on (nous embeds the credential proxy + vault since
// nous#20 retired the separate cmd/charon/ binary):
//
//   - exists (otherwise the check is moot)
//   - is signed (not unsigned/ad-hoc — the M4 ACL would refuse to
//     attach to entries written by such a binary anyway)
//   - has identifier `com.charon.cli` (matches what the M4 ACL
//     predicates against; an unexpected identifier is impostor-shaped)
//   - has hardened runtime enabled (#12G; closes A5 — regression
//     check in case the user has an older binary lying around)
//
// Catches the "stale binary" / "wrong binary" class of issues. A
// stale binary that's still signed by an old identity will still
// be able to read its keychain entries (cdhash matches) but won't
// have hardened runtime; an impostor binary with the right
// identifier but a different signature will fail the ACL outright.
func CheckCharonBinary() []Finding {
	if _, err := os.Stat(nousInstallPath); err != nil {
		if os.IsNotExist(err) {
			return []Finding{{
				ID:       "charon-binary-not-installed",
				Severity: SevInfo,
				Title:    "nous CLI not installed at " + nousInstallPath,
				Detail: fmt.Sprintf(
					"Couldn't find a nous binary at %s. If you've installed "+
						"nous to a non-standard location, this check is skipped "+
						"and you should manually verify codesign properties. Otherwise: "+
						"`make nous-install` from the nous repo.",
					nousInstallPath),
				BarItem: BarCharonBinary,
			}}
		}
		return []Finding{{
			ID:       "charon-binary-stat-error",
			Severity: SevImportant,
			Title:    "Could not stat installed charon CLI",
			Detail:   fmt.Sprintf("os.Stat(%q): %v", nousInstallPath, err),
			BarItem:  BarCharonBinary,
		}}
	}

	out, err := exec.Command("codesign", "-dvv", nousInstallPath).CombinedOutput()
	if err != nil {
		// codesign returns non-zero for unsigned binaries. The
		// stderr ("code object is not signed at all") gets mixed
		// with stdout in CombinedOutput, so treat any failure as
		// "unsigned or unverifiable" — Critical either way.
		return []Finding{{
			ID:       "charon-binary-unsigned",
			Severity: SevCritical,
			Title:    "Installed charon CLI is unsigned (or signature is invalid)",
			Detail: fmt.Sprintf(
				"codesign -dvv exited with %v on %s. Output:\n\n%s\n\n"+
					"An unsigned charon binary cannot satisfy the M4 keychain "+
					"ACL — it can't write or update charon entries. Worse, an "+
					"agent could replace the binary in place since there's no "+
					"signature to verify against. Run `make install` to sign.",
				err, nousInstallPath, strings.TrimSpace(string(out))),
			Affects:   []string{nousInstallPath},
			RemedyRef: "charon-binary",
			BarItem:   BarCharonBinary,
		}}
	}
	s := string(out)

	var findings []Finding

	// Identifier check. The M4 ACL pins on `identifier "com.charon.cli"`;
	// a binary with a different identifier won't satisfy the predicate
	// and shouldn't be at ~/.local/bin/charon at all.
	if !strings.Contains(s, "Identifier=com.charon.cli") {
		actualIdentifier := extractCodesignField(s, "Identifier")
		findings = append(findings, Finding{
			ID:       "charon-binary-wrong-identifier",
			Severity: SevCritical,
			Title:    "Installed charon CLI has unexpected codesign identifier",
			Detail: fmt.Sprintf(
				"Expected `Identifier=com.charon.cli`. Found %q. Either an "+
					"impostor binary was placed at %s, or `make install` was "+
					"misconfigured. Inspect the binary; if you didn't install "+
					"it, treat as a compromise.",
				actualIdentifier, nousInstallPath),
			Affects:   []string{nousInstallPath},
			RemedyRef: "charon-binary",
			BarItem:   BarCharonBinary,
		})
	}

	// Hardened runtime check. `codesign -dvv` includes a CodeDirectory
	// line whose `flags=0x...(runtime)` indicates hardened runtime.
	// Various format variations exist; presence of the "(runtime)"
	// substring near the flags is the simplest stable signal.
	if !codesignHasRuntimeFlag(s) {
		findings = append(findings, Finding{
			ID:       "charon-binary-not-hardened",
			Severity: SevImportant,
			Title:    "Installed charon CLI lacks hardened runtime",
			Detail: fmt.Sprintf(
				"codesign output for %s doesn't include the runtime flag in "+
					"its CodeDirectory. Hardened runtime is the macOS-level "+
					"mitigation for A5 (DYLD injection, debugger attach, "+
					"unsigned dylib loading) and is what `make install` should "+
					"set since #12G landed. Most likely cause: the installed "+
					"binary predates that change. Re-run `make install` to "+
					"re-sign with `--options runtime`.",
				nousInstallPath),
			Affects:   []string{nousInstallPath},
			RemedyRef: "charon-binary",
			BarItem:   BarCharonBinary,
		})
	}

	return findings
}

// extractCodesignField pulls the value following `<Field>=` from
// codesign -dvv output. Returns "" if the field isn't present.
func extractCodesignField(out, field string) string {
	prefix := field + "="
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}

// codesignHasRuntimeFlag reports whether the codesign -dvv output
// includes the hardened-runtime marker. CodeDirectory lines look like:
//
//	CodeDirectory v=20500 size=... flags=0x10000(runtime) hashes=...
//	CodeDirectory v=20500 size=... flags=0x12000(runtime,library-validation) hashes=...
//
// Parses the parenthesized flag list rather than matching the literal
// "(runtime)" substring so combined-flag forms still match.
func codesignHasRuntimeFlag(out string) bool {
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "CodeDirectory") {
			continue
		}
		i := strings.Index(line, "flags=")
		if i < 0 {
			continue
		}
		rest := line[i:]
		open := strings.Index(rest, "(")
		close := strings.Index(rest, ")")
		if open < 0 || close < 0 || close <= open {
			continue
		}
		for _, name := range strings.Split(rest[open+1:close], ",") {
			if strings.TrimSpace(name) == "runtime" {
				return true
			}
		}
	}
	return false
}
