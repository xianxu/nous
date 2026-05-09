package security

import (
	"fmt"
	"os/exec"
	"strings"
)

// WeakeningEntitlements are entitlements that materially expand the
// attack surface inside a hardened-runtime app. A terminal or IDE that
// loads user code (shells, plugins) with these set offers an injection
// vector — A5-class.
var WeakeningEntitlements = []string{
	"com.apple.security.cs.allow-dyld-environment-variables",
	"com.apple.security.cs.disable-library-validation",
	"com.apple.security.cs.disable-executable-page-protection",
	"com.apple.security.cs.allow-jit",
	"com.apple.security.cs.allow-unsigned-executable-memory",
	"com.apple.security.get-task-allow",
}

// CheckCodesignEntitlements runs `codesign -d --entitlements -` on each
// detected terminal/editor/IDE and flags weakening entitlements.
//
// Surfaces as Hygiene-tier — the user generally can't fix this (they'd
// need to repackage someone else's app). The signal is "this app is a
// poor host for agentic work," informing relocation more than action.
func CheckCodesignEntitlements(apps []DetectedApp) []Finding {
	var findings []Finding
	for _, app := range apps {
		ents, err := readEntitlements(app.Path)
		if err != nil {
			// codesign may emit "code object is not signed at all" for
			// adhoc binaries. Treat as Hygiene info — most users won't
			// have these.
			findings = append(findings, Finding{
				ID:       "codesign-readfail-" + app.BundleID,
				Severity: SevHygiene,
				Title:    fmt.Sprintf("Could not read entitlements for %s", app.Name),
				Detail:   fmt.Sprintf("codesign error: %v", err),
				Affects:  []string{app.Path},
			})
			continue
		}
		var weak []string
		for _, e := range WeakeningEntitlements {
			if strings.Contains(ents, e) {
				weak = append(weak, e)
			}
		}
		if len(weak) == 0 {
			continue
		}
		findings = append(findings, Finding{
			ID:       "codesign-weak-" + app.BundleID,
			Severity: SevHygiene,
			Title:    fmt.Sprintf("%s ships weakening entitlements", app.Name),
			Detail: fmt.Sprintf(
				"%s declares entitlements that loosen the hardened runtime:\n"+
					"  - %s\n\n"+
					"Implication: code injected via DYLD_*, dlopen of unsigned\n"+
					"libraries, or JIT pages may run inside this process.\n"+
					"Action: avoid running agents from this app if a stricter\n"+
					"alternative exists.",
				app.Name, strings.Join(weak, "\n  - ")),
			RemedyRef: "codesign",
			Affects:   []string{app.Path},
		})
	}
	return findings
}

// readEntitlements is the shell-out. Output is plist XML; we just grep
// it for entitlement keys rather than parsing — false-positive rate
// is acceptable since the keys are structured strings.
func readEntitlements(appPath string) (string, error) {
	cmd := exec.Command("codesign", "-d", "--entitlements", "-", "--xml", appPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
