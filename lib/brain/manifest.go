// Package brain reads and writes the .brain/config.md manifest that
// declares a directory as a nous brain (per AGENTS.md §1's brain-
// identification convention: "a repo is a brain iff it contains
// .brain/config.md").
//
// Used by:
//   - cmd/nous identity (joined view: which keys are recipients of which brains)
//   - cmd/nous brain    (provisioning, recipient management — M4b)
//   - lib/brainsync     (already discovers shared brains; this is the
//                        broader read API not tied to mode-shared)
//
// The manifest is YAML frontmatter wrapping a markdown body. We parse
// only the frontmatter fields nous cares about (name, mode, recipients,
// sync_substrate) — using a minimal hand-rolled parser to avoid pulling
// a YAML dependency for what's effectively a fixed-schema config.
package brain

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/xianxu/nous/lib/workspace"
)

// Manifest captures the .brain/config.md fields nous operates on.
// Path is the absolute path to the brain's root directory (the
// directory that contains .brain/, not .brain/ itself).
type Manifest struct {
	Path string

	Name          string   // human-friendly slug (`personal`, `family`, ...)
	Recipients    []string // GPG fingerprints (full 40-char uppercase hex)
	SyncSubstrate string   // "syncthing" | "git-daemon" | "none"

	// RecipientLogins maps a GitHub login to the fingerprint it was
	// admitted as — the durable login→admitted-fp record auto-admit writes
	// so a key rotation (keys-branch `<login>.asc` overwritten with a new
	// fp) can retire the superseded fp from Recipients instead of leaving it
	// as a stale recipient (nous#41 #7/#8). Sparse: sneakernet recipients
	// added by fingerprint with no GitHub login have no entry. Encoded
	// inline in the manifest as `recipient_logins: {login: FP, ...}`.
	RecipientLogins map[string]string

	// Autosave controls the *commit* axis: whether the brainsync daemon
	// auto-commits tracked-file edits in this brain (5s debounce) as a
	// local safety net. Independent of the push axis (see Publish) since
	// nous#47 decoupled the two cadences. Values: "" (unset → on), "on",
	// "off". Use AutosaveEnabled() instead of comparing the string.
	Autosave string

	// Publish controls the *push* axis: whether the daemon auto-pushes
	// committed changes to origin (60s debounce). Tri-state, consumed by
	// lib/brainsync's ComputePolicy/shouldPush:
	//   - "" (unset) → derived: gcrypt/shared brains push; a plain remote
	//     pushes only if shared; a private plain-remote brain does not.
	//   - "on"  → auto-push whenever a remote exists (the "private but
	//     published" opt-in for plain remotes).
	//   - "off" → never auto-push (pull still runs if the brain is a sync
	//     participant; only the push half is paused).
	// Orthogonal to Autosave: a brain can commit locally without pushing.
	Publish string

	// LegacyMode holds the value of the deprecated `mode:` field when an
	// existing manifest carries it. Readers don't act on it (shared-vs-
	// private is derived from len(Recipients)); kept here only so the
	// reader can preserve it on rewrite without dropping operator-
	// authored content. M4b's writer doesn't emit this field.
	LegacyMode string
}

// Shared reports whether the brain has more than one recipient — the
// derived signal that replaced the explicit `mode:` field. Private =
// single recipient (the operator); Shared = multiple. See
// AGENTS.md §1's brain-identification block for the rationale.
func (m Manifest) Shared() bool {
	return len(m.Recipients) > 1
}

// AutosaveEnabled reports whether the brainsync daemon should
// auto-commit this brain (the commit axis only — the push axis is the
// Publish field, decoupled in nous#47). Default on — only an explicit
// `autosave: off` in the manifest disables it. Any other value
// (including the empty string from a manifest that predates the
// field) means enabled.
func (m Manifest) AutosaveEnabled() bool {
	return strings.ToLower(strings.TrimSpace(m.Autosave)) != "off"
}

// Read parses the manifest at <brainRoot>/.brain/config.md. Returns an
// error if the brain root doesn't exist or the manifest is missing.
//
// Tolerant parser: missing fields default to empty. Validity
// (e.g. recipient fingerprint format) is the caller's responsibility.
func Read(brainRoot string) (Manifest, error) {
	cfgPath := filepath.Join(brainRoot, ".brain", "config.md")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return Manifest{}, fmt.Errorf("read %s: %w", cfgPath, err)
	}
	m, err := parseManifest(string(data))
	if err != nil {
		return m, err
	}
	abs, err := filepath.Abs(brainRoot)
	if err != nil {
		return m, err
	}
	m.Path = abs
	return m, nil
}

// DiscoverAll walks the workspace root (resolved by lib/workspace.Root —
// $WORKSPACE_ROOT, $NOUS_DIR's parent, the running binary's grandparent,
// or $HOME/workspace as the final fallback) one level deep and returns
// every directory that's a brain. Mirrors
// lib/brainsync.FindAllBrainsInWorkspace but doesn't filter by the
// watch policy — useful for `nous identity list` and `nous brain list`.
func DiscoverAll() ([]Manifest, error) {
	root, err := workspace.Root()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", root, err)
	}
	var found []Manifest
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		brainRoot := filepath.Join(root, e.Name())
		m, err := Read(brainRoot)
		if err != nil {
			continue // not a brain (no .brain/config.md), or unreadable
		}
		found = append(found, m)
	}
	return found, nil
}

// parseManifest extracts the YAML frontmatter fields nous cares about.
// Only handles a flat key:value structure — no nested maps, no anchors.
// The brain manifest schema is intentionally flat (per AGENTS.md), so
// this is sufficient.
func parseManifest(content string) (Manifest, error) {
	body, ok := frontmatterBody(content)
	if !ok {
		return Manifest{}, fmt.Errorf("missing YAML frontmatter (--- ... ---)")
	}
	var m Manifest
	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		switch key {
		case "name":
			m.Name = unquote(val)
		case "mode":
			// Deprecated; preserved verbatim for round-trips through the
			// writer in M4b. Not used as a discriminator anymore.
			m.LegacyMode = unquote(val)
		case "sync_substrate":
			m.SyncSubstrate = unquote(val)
		case "recipients":
			m.Recipients = parseList(val)
		case "recipient_logins":
			m.RecipientLogins = parseInlineMap(val)
		case "autosave":
			m.Autosave = unquote(val)
		case "publish":
			m.Publish = unquote(val)
		}
	}
	return m, nil
}

// frontmatterBody returns the text between leading "---\n" and the next
// "---\n", or (false) if the document isn't frontmatter-wrapped.
func frontmatterBody(content string) (string, bool) {
	if !strings.HasPrefix(content, "---\n") && !strings.HasPrefix(content, "---\r\n") {
		return "", false
	}
	rest := strings.TrimPrefix(content, "---\n")
	rest = strings.TrimPrefix(rest, "---\r\n")
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "", false
	}
	return rest[:end], true
}

// parseList parses an inline YAML list `[a, b, "c"]` to []string.
// Handles surrounding whitespace + optional quotes. Bare values too:
// the recipients field has occasionally been seen unquoted in
// hand-edited manifests.
func parseList(val string) []string {
	val = strings.TrimSpace(val)
	val = strings.TrimPrefix(val, "[")
	val = strings.TrimSuffix(val, "]")
	if val == "" {
		return nil
	}
	parts := strings.Split(val, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		v := unquote(strings.TrimSpace(p))
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

// parseInlineMap parses an inline YAML map `{a: x, b: "y"}` to a
// map[string]string. Mirrors parseList's flat, dependency-free posture
// (the manifest schema is intentionally flat — see parseManifest). Keys
// and values are trimmed + unquoted; fingerprint values are uppercased so
// membership tests against Recipients stay case-insensitive. Returns nil
// for an empty `{}` or malformed entry-with-no-colon.
func parseInlineMap(val string) map[string]string {
	val = strings.TrimSpace(val)
	val = strings.TrimPrefix(val, "{")
	val = strings.TrimSuffix(val, "}")
	if strings.TrimSpace(val) == "" {
		return nil
	}
	out := make(map[string]string)
	for _, pair := range strings.Split(val, ",") {
		k, v, found := strings.Cut(pair, ":")
		if !found {
			continue
		}
		k = unquote(strings.TrimSpace(k))
		v = strings.ToUpper(unquote(strings.TrimSpace(v)))
		if k != "" && v != "" {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && (s[0] == '"' && s[len(s)-1] == '"' || s[0] == '\'' && s[len(s)-1] == '\'') {
		return s[1 : len(s)-1]
	}
	return s
}
