package brainsync

import (
	"context"
	"fmt"
	"log"
	"time"
)

// Run is the ergonomics wrapper around Watch: if opts.Brains is empty
// it auto-discovers shared brains under the workspace root, then
// delegates to Watch. Designed so cmd/brain-sync (legacy daemon) and
// `nous serve` (nous#16 M2, where Run is one goroutine in a unified
// daemon) can share one entry point. Callers own ctx + signal
// handling.
//
// Returns nil on clean ctx-cancellation (Watch behavior); errors on
// no-brains / discovery failure / underlying Watch error.
func Run(ctx context.Context, opts RunOptions) error {
	brains := opts.Brains
	if len(brains) == 0 {
		auto, err := FindAllSharedBrainsInWorkspace()
		if err != nil {
			return fmt.Errorf("auto-discover shared brains: %w", err)
		}
		if len(auto) == 0 {
			return fmt.Errorf("no shared brains found under the workspace root; pass Brains explicitly")
		}
		brains = auto
		log.Printf("brainsync: auto-discovered %d shared brain(s)", len(auto))
	}
	fetchEvery := opts.FetchEvery
	if fetchEvery == 0 {
		fetchEvery = 30 * time.Second
	}
	return Watch(ctx, brains, fetchEvery, opts.Verbose)
}

// RunOptions bundles config for Run. All fields optional.
type RunOptions struct {
	// Brains is the list of absolute brain-root paths to watch. Empty
	// → auto-discover shared brains under the workspace root.
	Brains []string

	// FetchEvery is the periodic fetch interval. Zero → 30 seconds.
	FetchEvery time.Duration

	// Verbose enables logging on every successful push/pull (not just
	// errors).
	Verbose bool
}
