package brain

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/xianxu/nous/lib/brain/filestore"
	"github.com/xianxu/nous/lib/identity"
)

// peerkeys.go layers pubkey-exchange semantics on top of the generic
// lib/brain/filestore. Callers operate on fingerprints and use
// identity.Import/Export under the hood; they never see filenames
// or branches.
//
// Filename convention: <40-hex-uppercase-fingerprint>.asc. The
// fingerprint is redundant with the pubkey content but useful for
// human inspection (`ls .git/nous-filestore/keys/` shows the
// recipient set at a glance) and for selectively deleting entries
// by name.

// keysBranch is the conventional branch name for the pubkey
// filestore on every brain. Package-private — callers don't need
// to know the branch name; they get a fully-configured Store back.
const keysBranch = "keys"

// pubkeyFilenameSuffix is the convention all peerkeys entries
// follow. Used both when publishing (we write `<fp>.asc`) and
// when filtering List output (we skip non-matching files so a
// curious operator who manually committed `README.md` to the
// keys branch doesn't trip up ImportAllPubkeys).
const pubkeyFilenameSuffix = ".asc"

// PublishPubkey adds (or refreshes) the pubkey for fingerprint
// fp to the brain's keys store. Idempotent: republishing the
// same pubkey is a no-op at the filestore layer (identical
// content → no commit / push).
//
// The pubkey is read from the local GPG keyring via
// identity.Export(fp), so fp must already be present locally —
// typically that's just been imported via identity.Import after
// a sneakernet hand-off or fetched from another peer's keys
// branch.
func PublishPubkey(ctx context.Context, brainRoot, fp string) error {
	armor, err := identity.Export(fp)
	if err != nil {
		return fmt.Errorf("peerkeys: export %s: %w", fp, err)
	}
	store, err := filestore.Open(brainRoot, keysBranch)
	if err != nil {
		return fmt.Errorf("peerkeys: open keys store: %w", err)
	}
	defer store.Close()

	name := strings.ToUpper(fp) + pubkeyFilenameSuffix
	if err := store.Put(ctx, name, []byte(armor)); err != nil {
		return fmt.Errorf("peerkeys: publish %s: %w", name, err)
	}
	return nil
}

// RevokePubkey removes every keys-branch entry for fp — matched by the
// pubkey's *content* fingerprint, not by filename. A recipient may be
// published under more than one name: the legacy `<FP>.asc` (nous#23)
// AND the `<login>.asc` (nous#26) the GitHub-mediated join writes. A
// filename-only delete of `<FP>.asc` left `<login>.asc` behind, and
// since auto-admit derives the fp from contents, the survivor silently
// re-admitted the revoked peer (nous#38 leak #1). Deleting by content
// fingerprint covers all naming conventions at once.
//
// Idempotent (no-op when nothing matches). Does NOT touch the local GPG
// keyring — that's the operator's concern. Symmetric counterpart to
// PublishPubkey; called by the shared remove path after the manifest +
// gcrypt-participants update.
func RevokePubkey(ctx context.Context, brainRoot, fp string) error {
	store, err := filestore.Open(brainRoot, keysBranch)
	if err != nil {
		return fmt.Errorf("peerkeys: open keys store: %w", err)
	}
	defer store.Close()

	files, err := store.List(ctx)
	if err != nil {
		return fmt.Errorf("peerkeys: list keys store: %w", err)
	}
	fpUp := strings.ToUpper(strings.TrimSpace(fp))
	for name, content := range files {
		if !strings.HasSuffix(name, pubkeyFilenameSuffix) {
			continue
		}
		key, ierr := identity.Inspect(string(content))
		if ierr != nil {
			continue // unparseable entry — not ours to delete; leave it
		}
		if strings.ToUpper(key.Fingerprint) != fpUp {
			continue
		}
		if derr := store.Delete(ctx, name); derr != nil {
			return fmt.Errorf("peerkeys: revoke %s: %w", name, derr)
		}
	}
	return nil
}

// BootstrapPubkeys fetches the brain's `keys` branch directly from
// its gcrypt remote URL — WITHOUT requiring a local brain clone to
// exist yet — and imports every pubkey into the local GPG keyring.
// The pre-flight for `nous brain clone`: peers run this first so
// gcrypt's signature-verification has the operator's (and every
// other peer's) pubkey by the time the actual brain clone runs.
//
// gcryptURL: the same URL the operator would pass to `git clone
// gcrypt::...`. The `gcrypt::` prefix is stripped to get the plain
// URL used to fetch the keys branch.
//
// Returns the number of pubkeys successfully imported and any
// per-file errors (none aborts the loop). The top-level err is
// non-nil only on infrastructure failures (no remote, git not
// installed). A brain provisioned before #23 landed has no `keys`
// branch — that case returns (0, nil, nil) so callers proceed
// with the gcrypt clone gracefully and rely on the legacy
// sneakernet pubkey flow.
func BootstrapPubkeys(ctx context.Context, gcryptURL string) (imported int, errs []error, err error) {
	plainURL := strings.TrimPrefix(gcryptURL, "gcrypt::")

	tmpDir, err := os.MkdirTemp("", "nous-bootstrap-keys-")
	if err != nil {
		return 0, nil, fmt.Errorf("peerkeys bootstrap: tempdir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	cmd := exec.CommandContext(ctx, "git", "clone",
		"--branch", keysBranch,
		"--single-branch",
		"--depth=1",
		plainURL, tmpDir)
	if out, cerr := cmd.CombinedOutput(); cerr != nil {
		// Remote-branch-missing is the common case for brains
		// provisioned before #23 landed. Detect via git's stderr
		// rather than parsing exit codes; "Remote branch ... not
		// found" is git's standard phrasing.
		msg := string(out)
		if strings.Contains(msg, "Remote branch") && strings.Contains(msg, "not found") {
			return 0, nil, nil
		}
		return 0, nil, fmt.Errorf("peerkeys bootstrap: clone keys branch: %w\n%s", cerr, msg)
	}

	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		return 0, nil, fmt.Errorf("peerkeys bootstrap: read tempdir: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if !strings.HasSuffix(e.Name(), pubkeyFilenameSuffix) {
			continue
		}
		content, rerr := os.ReadFile(filepath.Join(tmpDir, e.Name()))
		if rerr != nil {
			errs = append(errs, fmt.Errorf("read %s: %w", e.Name(), rerr))
			continue
		}
		if _, ierr := identity.Import(string(content)); ierr != nil {
			errs = append(errs, fmt.Errorf("import %s: %w", e.Name(), ierr))
			continue
		}
		imported++
	}
	return imported, errs, nil
}

// PublishOwnPubkey writes the operator's pubkey to the brain's keys
// store under the new nous#26 `<login>.asc` convention.
// Counterpart to PublishPubkey (which writes `<FP>.asc`, the
// nous#23 legacy convention). Both can coexist on the same keys
// branch — ImportAllPubkeys imports any `.asc` file regardless of
// stem, and auto-admit's looksLikeFingerprint discriminator
// correctly skips legacy `<FP>.asc` entries.
//
// Use this at brain creation time (nous brain new) so the operator's
// pubkey is published under both conventions: legacy `<FP>.asc` keeps
// pre-#26 clones working; new `<login>.asc` makes drift detection
// (via verified.yaml) keyable by github-login as designed.
//
// The fp must already be in the operator's local GPG keyring;
// identity.Export reads it from there.
func PublishOwnPubkey(ctx context.Context, brainRoot, login, fp string) error {
	armor, err := identity.Export(fp)
	if err != nil {
		return fmt.Errorf("peerkeys: export %s: %w", fp, err)
	}
	store, err := filestore.Open(brainRoot, keysBranch)
	if err != nil {
		return fmt.Errorf("peerkeys: open keys store: %w", err)
	}
	defer store.Close()

	name := login + pubkeyFilenameSuffix
	if err := store.Put(ctx, name, []byte(armor)); err != nil {
		return fmt.Errorf("peerkeys: publish %s: %w", name, err)
	}
	return nil
}

// PublishOwnPubkeyToRemote writes `<login>.asc` (with the given
// armored pubkey) to the `keys` branch of the remote at `cloneURL`,
// without requiring a local brain clone. The new-joiner flow
// (nous#26): a freshly-invited collaborator has plain-git push
// access to the repo (including the keys branch) but cannot yet
// decrypt the gcrypt'd main branch, so the filestore abstraction
// (which assumes a local brain) doesn't fit.
//
// `cloneURL` is the plain (non-gcrypt) URL. Typically the
// invitation's ssh_url field. `login` is the joiner's GitHub
// login (used as filename stem — the auto-admit on the operator's
// side keys the trust mapping off this stem).
//
// Handles the case where the keys branch doesn't exist yet by
// creating it as an orphan branch and pushing. That's the
// expected state for a brand-new brain whose operator hasn't
// finished pubkey publishing.
func PublishOwnPubkeyToRemote(ctx context.Context, cloneURL, login, armoredPubkey string) error {
	tmpDir, err := os.MkdirTemp("", "nous-join-")
	if err != nil {
		return fmt.Errorf("peerkeys join: tempdir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// Try to clone the keys branch directly.
	cmd := exec.CommandContext(ctx, "git", "clone",
		"--branch", keysBranch,
		"--single-branch",
		"--depth=1",
		cloneURL, tmpDir)
	if out, cerr := cmd.CombinedOutput(); cerr != nil {
		// "Remote branch ... not found" is the new-brain case —
		// fall through to orphan-branch creation. Anything else is
		// a real failure (auth, network, repo missing).
		msg := string(out)
		if !(strings.Contains(msg, "Remote branch") && strings.Contains(msg, "not found")) {
			return fmt.Errorf("peerkeys join: clone keys branch: %w\n%s", cerr, msg)
		}
		// Fresh clone of any branch, then orphan-checkout keys.
		if err := os.RemoveAll(tmpDir); err != nil {
			return fmt.Errorf("peerkeys join: clean tempdir: %w", err)
		}
		if err := os.MkdirAll(tmpDir, 0o755); err != nil {
			return fmt.Errorf("peerkeys join: re-mkdir: %w", err)
		}
		init := exec.CommandContext(ctx, "git", "-C", tmpDir, "init", "-q", "-b", keysBranch)
		if iout, ierr := init.CombinedOutput(); ierr != nil {
			return fmt.Errorf("peerkeys join: git init: %w\n%s", ierr, iout)
		}
		add := exec.CommandContext(ctx, "git", "-C", tmpDir, "remote", "add", "origin", cloneURL)
		if aout, aerr := add.CombinedOutput(); aerr != nil {
			return fmt.Errorf("peerkeys join: git remote add: %w\n%s", aerr, aout)
		}
	}

	// Write <login>.asc.
	target := filepath.Join(tmpDir, login+pubkeyFilenameSuffix)
	if err := os.WriteFile(target, []byte(armoredPubkey), 0o644); err != nil {
		return fmt.Errorf("peerkeys join: write %s: %w", target, err)
	}

	// git add + commit. Configure user.email/name from the operator's
	// global git config so the commit author is consistent with their
	// other repos. If not set, leave the default ("git config user.email"
	// returning empty means git will use the global / default).
	if out, err := exec.CommandContext(ctx, "git", "-C", tmpDir, "add", login+pubkeyFilenameSuffix).CombinedOutput(); err != nil {
		return fmt.Errorf("peerkeys join: git add: %w\n%s", err, out)
	}
	commit := exec.CommandContext(ctx, "git", "-C", tmpDir, "commit", "-q", "-m", "publish "+login+pubkeyFilenameSuffix)
	if out, err := commit.CombinedOutput(); err != nil {
		// "nothing to commit" means the same key is already published —
		// idempotent.
		if strings.Contains(string(out), "nothing to commit") {
			return nil
		}
		return fmt.Errorf("peerkeys join: git commit: %w\n%s", err, out)
	}
	push := exec.CommandContext(ctx, "git", "-C", tmpDir, "push", "origin", keysBranch)
	if out, err := push.CombinedOutput(); err != nil {
		return fmt.Errorf("peerkeys join: git push: %w\n%s", err, out)
	}
	return nil
}

// ImportAllPubkeys fetches every pubkey from the brain's keys store
// and runs identity.Import on each. Returns the count successfully
// imported. Idempotent: gpg's import is a no-op for keys already in
// the keyring.
//
// Called by `nous brain clone` on first peer setup (to seed the
// keyring before gcrypt verification) and by the brain-sync
// watcher on every tick (to pick up newly added recipients
// without operator intervention).
//
// Errors on Store.List failure (network outage, branch missing
// for a fresh brain that hasn't pushed yet). Per-file Import
// errors are surfaced via errs without aborting the whole
// operation — a corrupt entry shouldn't block legitimate keys
// from landing.
func ImportAllPubkeys(ctx context.Context, brainRoot string) (imported int, errs []error, err error) {
	store, err := filestore.Open(brainRoot, keysBranch)
	if err != nil {
		return 0, nil, fmt.Errorf("peerkeys: open keys store: %w", err)
	}
	defer store.Close()

	files, err := store.List(ctx)
	if err != nil {
		return 0, nil, fmt.Errorf("peerkeys: list keys store: %w", err)
	}
	for name, content := range files {
		if !strings.HasSuffix(name, pubkeyFilenameSuffix) {
			// Skip non-pubkey entries (README.md, etc.). The
			// filestore stores arbitrary bytes; only files matching
			// the convention are pubkeys we should import.
			continue
		}
		if _, ierr := identity.Import(string(content)); ierr != nil {
			errs = append(errs, fmt.Errorf("import %s: %w", name, ierr))
			continue
		}
		imported++
	}
	return imported, errs, nil
}
