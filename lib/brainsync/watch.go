package brainsync

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/xianxu/nous/lib/brain"
)

// PeerIDFor derives a stable peer label from `git config user.name` in the
// given repo (lowercased, hyphenated). Falls back to the machine's hostname
// (stripped of .local/.lan suffix), then "unknown-peer".
func PeerIDFor(repo string) string {
	out, err := RunGit(repo, "config", "user.name")
	name := ""
	if err == nil {
		name = strings.TrimSpace(string(out))
	}
	if name != "" {
		return strings.ReplaceAll(strings.ToLower(name), " ", "-")
	}
	if host, err := os.Hostname(); err == nil && host != "" {
		host = strings.TrimSuffix(host, ".local")
		host = strings.TrimSuffix(host, ".lan")
		return strings.ToLower(host)
	}
	return "unknown-peer"
}

// PushBrain pushes any unpushed commits, resolving + retrying on rejection.
// Caps retries at 5. No-op if nothing to push. Returns true if a push
// actually happened (caller can use this for verbose logging).
func PushBrain(repo, peer string, now func() time.Time) (pushed bool, err error) {
	hasNew, err := HasUnpushedCommits(repo)
	if err != nil {
		return false, err
	}
	if !hasNew {
		return false, nil
	}
	for retry := 0; retry < 5; retry++ {
		err := Push(repo)
		if err == nil {
			return true, nil
		}
		if !errors.Is(err, ErrPushRejected) {
			return false, err
		}
		log.Printf("brainsync: push rejected for %s, resolving (retry %d)", repo, retry)
		if err := Resolve(repo, peer, now()); err != nil {
			return false, err
		}
	}
	return false, errors.New("exceeded retries")
}

// PullBrain fetches and fast-forwards if possible. Skips if working tree
// is dirty (lets the user commit first; resolve happens on push). Returns
// true if a fast-forward pull actually happened.
//
// Doesn't sync gcrypt-participants — that's the push wrapper's job
// (AddCommitPush / Push) per nous#24. The manifest update from the
// pull lands on disk; the next push reads the manifest and derives
// gcrypt-participants from it. Pull-side sync would be belt-and-
// suspenders; the push-side single sync point is sufficient for the
// "manifest is canonical" property.
func PullBrain(repo string) (pulled bool, err error) {
	if err := Fetch(repo); err != nil {
		return false, err
	}
	clean, err := CleanWorkingTree(repo)
	if err != nil || !clean {
		return false, err
	}
	behind, err := IsStrictlyBehind(repo)
	if err != nil || !behind {
		return false, err
	}
	if err := PullFF(repo); err != nil {
		return false, err
	}
	return true, nil
}

// Watch ties RefWatcher events + a periodic fetch ticker to push/pull.
// Blocks until ctx is cancelled. When verbose is true, logs every successful
// push/pull (otherwise only errors are logged).
func Watch(ctx context.Context, brains []string, fetchEvery time.Duration, verbose bool) error {
	if len(brains) == 0 {
		return errors.New("no brains to watch")
	}
	peer := PeerIDFor(brains[0])
	log.Printf("brainsync: watching %d brain(s) as peer %q", len(brains), peer)

	rw, err := NewRefWatcher(brains)
	if err != nil {
		return err
	}
	defer rw.Close()

	ticker := time.NewTicker(fetchEvery)
	defer ticker.Stop()

	for {
		select {
		case b := <-rw.Events():
			pushed, err := PushBrain(b, peer, time.Now)
			if err != nil {
				log.Printf("brainsync: push %s: %v", b, err)
			} else if pushed && verbose {
				log.Printf("brainsync: pushed %s", b)
			}
		case <-ticker.C:
			for _, b := range brains {
				pulled, err := PullBrain(b)
				if err != nil {
					log.Printf("brainsync: pull %s: %v", b, err)
				} else if pulled && verbose {
					log.Printf("brainsync: pulled %s", b)
				}
				// Also refresh peer pubkeys from the keys branch
				// (nous#23). A peer added by anyone on this brain
				// shows up in the keyring within one tick — no
				// operator action required. New imports are logged
				// at Info; "nothing new" is silent.
				syncBrainPubkeys(ctx, b, verbose)
				// Auto-admit any <login>.asc on the keys branch that's
				// not yet in the manifest (nous#26). Trust anchor:
				// operator's earlier `nous brain invite` grants GitHub
				// collaborator access; the joiner's push of their own
				// pubkey IS the affirmative "I want in." No prompt.
				autoAdmitBrain(ctx, b, verbose)
			}
		case <-ctx.Done():
			return nil
		}
	}
}

// autoAdmitBrain runs lib/brain.AutoAdmitFromKeysBranch and pushes
// the resulting manifest mutation via AddCommitPush. The #24 push
// wrapper handles gcrypt-participants sync, so by the time the
// remote sees the manifest update, the ciphertext is also re-
// encrypted to include the newly-admitted recipient.
//
// Drift events (a login whose keys-branch fingerprint differs from
// what verified.yaml pins) are logged loudly at Error every tick
// until the operator either re-verifies or revokes — they represent
// a potential MITM and should not be quiet.
//
// Logs at Info on each admission (this is operator-visible state
// change worth surfacing). Errors logged at Info too — auto-admit
// is best-effort; a transient failure resolves on the next tick.
func autoAdmitBrain(ctx context.Context, brainRoot string, verbose bool) {
	added, drift, err := brain.AutoAdmitFromKeysBranch(ctx, brainRoot)
	if err != nil {
		// Soft-log. The most common cause is "keys branch doesn't
		// exist on a pre-#23 brain"; logging every tick would spam.
		if verbose {
			log.Printf("brainsync: auto-admit %s: %v", brainRoot, err)
		}
		return
	}
	// Drift goes out loudly regardless of verbose — substituted-key
	// MITM is the one thing we don't want to be silent about.
	for _, d := range drift {
		log.Printf("brainsync: DRIFT on %s: login %q changed from %s to %s (originally verified by %s). "+
			"Auto-admit paused for this login. Re-verify with `nous brain recipient verify` "+
			"to accept the new key, or remove the entry from .brain/verified.yaml to clear.",
			brainRoot, d.Login,
			d.OldFingerprint[len(d.OldFingerprint)-8:], d.NewFingerprint[len(d.NewFingerprint)-8:],
			d.VerifiedBy)
	}
	if len(added) == 0 {
		return
	}
	parts := make([]string, 0, len(added))
	for _, r := range added {
		parts = append(parts, fmt.Sprintf("%s (%s)", r.Login, r.Fingerprint[len(r.Fingerprint)-8:]))
	}
	msg := "auto-admit " + strings.Join(parts, ", ")
	if err := AddCommitPush(brainRoot, msg); err != nil {
		log.Printf("brainsync: auto-admit commit/push %s: %v", brainRoot, err)
		return
	}
	log.Printf("brainsync: %s on %s", msg, brainRoot)
}

// syncBrainPubkeys runs peerkeys.ImportAllPubkeys for one brain and
// logs new imports at Info. Wrapped (rather than inlined) so the
// import-loop's error handling is consistent across the verbose /
// non-verbose paths, and so the function-level doc captures why we
// don't surface per-file Import errors at Warning (gpg's import is
// noisy on duplicates; logging at Info would spam every tick).
func syncBrainPubkeys(ctx context.Context, brainRoot string, verbose bool) {
	imported, perFileErrs, err := brain.ImportAllPubkeys(ctx, brainRoot)
	if err != nil {
		// Top-level error (filestore open / list failed). Common cause:
		// the brain was provisioned before nous#23 and has no `keys`
		// branch yet. Don't log every tick — the operator already saw
		// it once. Future enhancement: rate-limit this with a sync.Map
		// keyed by brain path. For now, only log under verbose.
		if verbose {
			log.Printf("brainsync: pubkey sync %s: %v", brainRoot, err)
		}
		return
	}
	if imported > 0 {
		log.Printf("brainsync: imported %d peer pubkey(s) for %s", imported, brainRoot)
	}
	if verbose {
		for _, e := range perFileErrs {
			log.Printf("brainsync: pubkey sync %s: %v", brainRoot, e)
		}
	}
}
