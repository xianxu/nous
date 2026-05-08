package brainsync

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"
)

// PeerIDFor derives a stable peer label from `git config user.name` in the
// given repo (lowercased, hyphenated). Falls back to "unknown-peer".
func PeerIDFor(repo string) string {
	out, err := RunGit(repo, "config", "user.name")
	if err != nil {
		return "unknown-peer"
	}
	name := strings.TrimSpace(string(out))
	name = strings.ReplaceAll(strings.ToLower(name), " ", "-")
	if name == "" {
		return "unknown-peer"
	}
	return name
}

// PushBrain pushes any unpushed commits, resolving + retrying on rejection.
// Caps retries at 5. No-op if nothing to push.
func PushBrain(repo, peer string, now func() time.Time) error {
	hasNew, err := HasUnpushedCommits(repo)
	if err != nil {
		return err
	}
	if !hasNew {
		return nil
	}
	for retry := 0; retry < 5; retry++ {
		err := Push(repo)
		if err == nil {
			return nil
		}
		if !errors.Is(err, ErrPushRejected) {
			return err
		}
		log.Printf("brainsync: push rejected for %s, resolving (retry %d)", repo, retry)
		if err := Resolve(repo, peer, now()); err != nil {
			return err
		}
	}
	return errors.New("exceeded retries")
}

// PullBrain fetches and fast-forwards if possible. Skips if working tree
// is dirty (lets the user commit first; resolve happens on push).
func PullBrain(repo string) error {
	if err := Fetch(repo); err != nil {
		return err
	}
	clean, err := CleanWorkingTree(repo)
	if err != nil || !clean {
		return err
	}
	behind, err := IsStrictlyBehind(repo)
	if err != nil || !behind {
		return err
	}
	return PullFF(repo)
}

// Watch ties RefWatcher events + a periodic fetch ticker to push/pull.
// Blocks until ctx is cancelled.
func Watch(ctx context.Context, brains []string, fetchEvery time.Duration) error {
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
			if err := PushBrain(b, peer, time.Now); err != nil {
				log.Printf("brainsync: push %s: %v", b, err)
			}
		case <-ticker.C:
			for _, b := range brains {
				if err := PullBrain(b); err != nil {
					log.Printf("brainsync: pull %s: %v", b, err)
				}
			}
		case <-ctx.Done():
			return nil
		}
	}
}
