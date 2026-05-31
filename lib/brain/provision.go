package brain

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// localGoModTmpl is the go.mod for a fresh local-only brain. The module
// path is local/<name> — a brain is never `go get`'d, so its own module
// path is cosmetic; what matters is the replace directive wiring nous
// (and transitively ariadne) in as the substrate ancestor, which
// construct/setup.sh's go.mod-based discovery walks. Mirrors
// scripts/new-brain.sh step 6, minus the GitHub-derived module path
// (we have no remote, and resolving an owner would need `gh` auth —
// exactly the network dependency a local brain avoids).
//
// The `replace … => ../nous` target is NOT cosmetic — it's the
// substrate-ancestor wiring construct/setup.sh's go.mod-based discovery
// walks. It hardcodes the workspace invariant that brains live as direct
// peers of the `nous` repo (`~/workspace/{nous,brain,...}`), matching
// scripts/new-brain.sh, which emits the same `../nous`. A brain created
// outside that layout won't resolve the substrate until the path is
// fixed up.
const localGoModTmpl = `module local/%s

go 1.22

// Substrate ancestor: nous (which transitively provides ariadne).
// Require + replace together — Go needs the require to consider the
// module part of the brain's module graph; the replace directs
// resolution to the sibling source.
require github.com/xianxu/nous v0.0.0-00010101000000-000000000000

replace github.com/xianxu/nous => ../nous
`

// InitLocal scaffolds a local-only brain at brainRoot: a git repo with
// NO remote, a go.mod wiring the nous substrate, a manifest with an
// EMPTY recipient list (sync_substrate: none), and one initial commit.
//
// No GitHub, no gcrypt, no network — and crucially, no GPG identity. A
// local brain has nothing to encrypt (gcrypt only engages on push to a
// gcrypt remote, and there is none), so it has no recipients: its
// working tree and git objects are plaintext, with FileVault (device
// FDE) as the at-rest protection. Requiring an identity here would tax
// the lightweight case for no benefit — you can make a local brain on a
// machine with no GPG set up at all.
//
// The recipient (the privacy axis — who can decrypt) is established at
// `nous brain publish`, the moment encryption first matters: publish
// resolves the operator's GPG identity, writes it into the manifest, and
// gcrypt-encrypts to it. This is the bottom rung of the topology ladder
// (local → private → shared).
//
// setupSubstrate, when non-nil, runs after go.mod is written and before
// the commit, so substrate symlinks (construct/setup.sh's output) land
// in the initial commit. Tests pass nil to skip the substrate step.
func InitLocal(brainRoot, name string, setupSubstrate func() error) error {
	if _, err := os.Stat(brainRoot); err == nil {
		return fmt.Errorf("InitLocal: %s already exists — move it aside or pick a fresh path", brainRoot)
	}
	if err := os.MkdirAll(brainRoot, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", brainRoot, err)
	}

	git := func(args ...string) error {
		full := append([]string{"-C", brainRoot}, args...)
		if out, err := exec.Command("git", full...).CombinedOutput(); err != nil {
			return fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, out)
		}
		return nil
	}

	if err := git("init", "-q", "-b", "main"); err != nil {
		return err
	}

	gomod := fmt.Sprintf(localGoModTmpl, moduleSafe(name))
	if err := os.WriteFile(filepath.Join(brainRoot, "go.mod"), []byte(gomod), 0o644); err != nil {
		return fmt.Errorf("write go.mod: %w", err)
	}

	if err := WriteManifest(brainRoot, Manifest{
		Name:          name,
		Recipients:    nil, // empty: a local brain has no one to encrypt to yet
		SyncSubstrate: "none",
	}); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}

	if setupSubstrate != nil {
		if err := setupSubstrate(); err != nil {
			return fmt.Errorf("substrate setup: %w", err)
		}
	}

	if err := git("add", "-A"); err != nil {
		return err
	}
	// Author from the operator's git identity, falling back to a neutral
	// default so provisioning works on a machine with no global git
	// config (and in tests). gpgsign forced off: a local brain has no
	// gcrypt, and an inherited commit.gpgsign=true would prompt for a
	// passphrase mid-bootstrap for no security benefit.
	name1 := gitConfigOr(brainRoot, "user.name", "nous")
	email := gitConfigOr(brainRoot, "user.email", "nous@localhost")
	if err := git(
		"-c", "user.name="+name1,
		"-c", "user.email="+email,
		"-c", "commit.gpgsign=false",
		"commit", "-q", "-m", fmt.Sprintf("init: bootstrap local brain (%s)", name),
	); err != nil {
		return err
	}
	return nil
}

// gitConfigOr returns the configured value for key (e.g. "user.name")
// or fallback when the key is unset or git errors.
func gitConfigOr(brainRoot, key, fallback string) string {
	out, err := exec.Command("git", "-C", brainRoot, "config", "--get", key).Output()
	if err != nil {
		return fallback
	}
	v := strings.TrimSpace(string(out))
	if v == "" {
		return fallback
	}
	return v
}

// moduleSafe sanitizes a brain name into a single go.mod path element:
// runs of disallowed characters collapse to a single '-'. Module path
// elements allow letters, digits, and the set -._~; anything else (path
// separators, spaces) is replaced.
func moduleSafe(name string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range name {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '.' || r == '_' || r == '~'
		if ok {
			b.WriteRune(r)
			prevDash = false
			continue
		}
		if !prevDash {
			b.WriteByte('-')
			prevDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "brain"
	}
	return out
}
