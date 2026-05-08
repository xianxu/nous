package brainsync

import (
	"context"
	"errors"
	"log"
	"os"
	"strings"
	"time"
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
			}
		case <-ctx.Done():
			return nil
		}
	}
}
