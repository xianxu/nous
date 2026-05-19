package brainsync

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Run is the entrypoint for the brain-sync goroutine. Two modes:
//
//   Static mode (opts.Brains is non-empty): watches the given list
//     verbatim until ctx cancellation. The legacy and explicit-flag
//     path; what `nous serve --brain <path>` produces.
//
//   Auto mode (opts.Brains is empty): runs a discovery loop that
//     periodically re-scans the workspace root for shared brains
//     (manifest recipient count > 1) and reconciles the watched set.
//     Starts with whatever the first scan finds (often zero — that's
//     no longer fatal); picks up new shared brains as they appear,
//     drops watchers for brains that are removed or downgraded to
//     private. Each watched brain runs in its own goroutine, so a
//     failure on one doesn't tear down the others.
//
// Returns nil on clean ctx-cancellation; errors only on unrecoverable
// setup problems. A failing Watch on a single brain is logged but
// does not abort.
func Run(ctx context.Context, opts RunOptions) error {
	fetchEvery := opts.FetchEvery
	if fetchEvery == 0 {
		fetchEvery = 30 * time.Second
	}
	if len(opts.Brains) > 0 {
		log.Printf("brainsync: static mode — watching %d brain(s)", len(opts.Brains))
		return Watch(ctx, opts.Brains, fetchEvery, opts.Verbose)
	}
	discoverEvery := opts.DiscoverEvery
	if discoverEvery == 0 {
		discoverEvery = 60 * time.Second
	}
	return runWithAutoDiscovery(ctx, fetchEvery, discoverEvery, opts.Verbose)
}

// RunOptions bundles config for Run. All fields optional.
type RunOptions struct {
	// Brains is the list of absolute brain-root paths to watch. Empty
	// → auto-discover shared brains under the workspace root and
	// periodically rescan for new ones.
	Brains []string

	// FetchEvery is the periodic fetch interval per brain. Zero → 30s.
	FetchEvery time.Duration

	// DiscoverEvery is the auto-mode rescan interval (the cadence at
	// which the workspace root is re-walked looking for new/removed
	// shared brains). Zero → 60s. Ignored in static mode.
	DiscoverEvery time.Duration

	// Verbose enables logging on every successful push/pull (not just
	// errors).
	Verbose bool
}

// runWithAutoDiscovery implements the auto-mode loop: periodic
// FindAllSharedBrainsInWorkspace → reconcile against the currently-
// watched set → spawn/cancel per-brain Watch goroutines.
//
// Reconciliation is set-diff: brains in the new scan but not currently
// watched get a new goroutine; brains currently watched but not in the
// new scan get their context cancelled and are removed from the map.
// Same-set ticks are no-ops (silent — see logging policy in package
// doc).
//
// Per-brain isolation: each Watch runs under a sub-context derived
// from ctx. Cancelling the sub-context stops that brain's loop without
// affecting siblings. A panic or unexpected error in one Watch is
// logged but doesn't tear down the discovery loop or other brains.
func runWithAutoDiscovery(ctx context.Context, fetchEvery, discoverEvery time.Duration, verbose bool) error {
	type watcher struct {
		cancel context.CancelFunc
		done   chan struct{}
	}
	var (
		mu      sync.Mutex
		watched = map[string]*watcher{} // brain path → live watcher
	)

	startWatch := func(path string) {
		subctx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		mu.Lock()
		watched[path] = &watcher{cancel: cancel, done: done}
		mu.Unlock()
		go func() {
			defer close(done)
			if err := Watch(subctx, []string{path}, fetchEvery, verbose); err != nil && ctx.Err() == nil {
				log.Printf("brainsync: watch %s exited with error: %v", path, err)
			}
		}()
	}

	reconcile := func() {
		desired, err := FindAllSharedBrainsInWorkspace()
		if err != nil {
			log.Printf("brainsync: discovery failed: %v", err)
			return
		}
		desiredSet := make(map[string]bool, len(desired))
		for _, p := range desired {
			desiredSet[p] = true
		}
		mu.Lock()
		toStop := make([]*watcher, 0)
		for path, w := range watched {
			if !desiredSet[path] {
				toStop = append(toStop, w)
				delete(watched, path)
				log.Printf("brainsync: removed %s (no longer shared, or directory gone)", path)
			}
		}
		toStart := make([]string, 0)
		for _, path := range desired {
			if _, ok := watched[path]; !ok {
				toStart = append(toStart, path)
			}
		}
		mu.Unlock()
		// Stop outside the lock so a slow Watch shutdown doesn't block
		// the next reconcile attempt.
		for _, w := range toStop {
			w.cancel()
			<-w.done
		}
		for _, path := range toStart {
			log.Printf("brainsync: added %s", path)
			startWatch(path)
		}
	}

	// Initial pass — log the empty-set case explicitly so operators
	// running `nous serve` against an unprovisioned workspace see the
	// "watching for brains" message rather than wondering whether the
	// daemon is alive.
	reconcile()
	mu.Lock()
	initialCount := len(watched)
	mu.Unlock()
	if initialCount == 0 {
		log.Printf("brainsync: no shared brains yet; rescanning workspace every %s", discoverEvery)
	} else {
		log.Printf("brainsync: auto-discovered %d shared brain(s); rescanning every %s", initialCount, discoverEvery)
	}

	// Set up an fsnotify watch on the rescan-signal file so any
	// `nous brain` cobra invocation wakes us sub-second (vs. waiting
	// up to discoverEvery for the periodic tick). Best-effort: if the
	// signal file path can't be resolved or fsnotify won't attach, we
	// fall back to ticker-only — a degraded but functional mode.
	signalCh := make(chan struct{}, 1)
	if signalPath, err := EnsureRescanSignal(); err != nil {
		log.Printf("brainsync: rescan-signal setup failed (ticker-only): %v", err)
	} else if watcher, err := fsnotify.NewWatcher(); err != nil {
		log.Printf("brainsync: fsnotify init failed (ticker-only): %v", err)
	} else {
		if err := watcher.Add(signalPath); err != nil {
			log.Printf("brainsync: fsnotify add %s failed (ticker-only): %v", signalPath, err)
			_ = watcher.Close()
		} else {
			defer watcher.Close()
			go func() {
				for {
					select {
					case ev, ok := <-watcher.Events:
						if !ok {
							return
						}
						// CHMOD covers os.Chtimes; WRITE covers explicit
						// writes; CREATE covers a manual `rm` + recreate.
						if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Chmod) != 0 {
							select {
							case signalCh <- struct{}{}:
							default:
								// Already have a pending wakeup queued; drop.
							}
						}
					case err, ok := <-watcher.Errors:
						if !ok {
							return
						}
						log.Printf("brainsync: fsnotify error: %v", err)
					case <-ctx.Done():
						return
					}
				}
			}()
		}
	}

	ticker := time.NewTicker(discoverEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			reconcile()
		case <-signalCh:
			reconcile()
		case <-ctx.Done():
			mu.Lock()
			toShutdown := make([]*watcher, 0, len(watched))
			for _, w := range watched {
				toShutdown = append(toShutdown, w)
			}
			watched = map[string]*watcher{}
			mu.Unlock()
			for _, w := range toShutdown {
				w.cancel()
			}
			for _, w := range toShutdown {
				<-w.done
			}
			return nil
		}
	}
}

