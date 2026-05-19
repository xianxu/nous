package brain

import (
	"context"
	"fmt"
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

// RevokePubkey removes fp's pubkey entry from the brain's keys
// store. Idempotent at the filestore layer (no-op when the entry
// doesn't exist). Does NOT remove the key from the local GPG
// keyring — that's a separate concern owned by the operator.
//
// Symmetric counterpart to PublishPubkey; called by
// `nous brain recipient remove` after the manifest + gcrypt-
// participants list have been updated.
func RevokePubkey(ctx context.Context, brainRoot, fp string) error {
	store, err := filestore.Open(brainRoot, keysBranch)
	if err != nil {
		return fmt.Errorf("peerkeys: open keys store: %w", err)
	}
	defer store.Close()

	name := strings.ToUpper(fp) + pubkeyFilenameSuffix
	if err := store.Delete(ctx, name); err != nil {
		return fmt.Errorf("peerkeys: revoke %s: %w", name, err)
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
