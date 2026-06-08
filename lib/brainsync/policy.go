package brainsync

import (
	"strings"

	"github.com/xianxu/nous/lib/brain"
)

// RemoteKind classifies a brain's origin remote — the IO input the pure
// policy derivation branches on. Computed once (via remoteKind) and fed
// to ComputePolicy.
type RemoteKind int

const (
	// RemoteNone: no origin configured (a purely local brain).
	RemoteNone RemoteKind = iota
	// RemoteGcrypt: a `gcrypt::` origin — the github-mediated encrypted
	// shared-brain substrate.
	RemoteGcrypt
	// RemotePlain: any other origin (e.g. a plain private GitHub repo).
	RemotePlain
)

// remoteKind classifies the brain's origin remote. Thin IO shell over
// brain.ReadOriginURL (the codebase's standard origin-URL reader) —
// keep the pure decision in ComputePolicy/shouldPush below.
func remoteKind(brainRoot string) RemoteKind {
	url := strings.TrimSpace(brain.ReadOriginURL(brainRoot))
	switch {
	case url == "":
		return RemoteNone
	case strings.HasPrefix(url, "gcrypt::"):
		return RemoteGcrypt
	default:
		return RemotePlain
	}
}

// BrainPolicy is the daemon's per-brain behavior, derived once from the
// manifest + remote kind and consumed by the commit loop, the push/pull
// loop, and the keys/auto-admit tick. nous#47 decoupled the commit and
// push cadences, so a brain can do any subset of these.
type BrainPolicy struct {
	Commit    bool // run the autosave commit loop (local safety net)
	Push      bool // auto-push committed changes to origin
	Pull      bool // periodic fetch + ff-only from origin
	KeysAdmit bool // keys-sync + auto-admit (gcrypt/shared only)
}

// Active reports whether the policy does anything worth watching the
// brain for. A fully opted-out brain (autosave off, not a sync
// participant) is excluded from the daemon's watch set.
func (p BrainPolicy) Active() bool {
	return p.Commit || p.Push || p.Pull
}

// publishMode normalizes the manifest's publish field to "", "on" or
// "off" (lowercased/trimmed). "" means "derive from remote kind".
func publishMode(m brain.Manifest) string {
	return strings.ToLower(strings.TrimSpace(m.Publish))
}

// syncParticipant reports whether the brain is a bidirectional sync
// target at all — i.e. whether the daemon should fetch from / push to
// its origin. This is the "do we talk to origin?" question; the push
// half is then additionally gated by `publish: off` (see ComputePolicy).
//
//   - no remote        → never (nothing to sync to)
//   - gcrypt remote    → always (the encrypted shared-brain substrate)
//   - plain remote     → only if shared (≥2 recipients, the legacy
//     shared-over-plain case) OR the operator opted in with `publish: on`
//     (the "private but published" case nous#47 adds).
func syncParticipant(m brain.Manifest, kind RemoteKind) bool {
	switch kind {
	case RemoteNone:
		return false
	case RemoteGcrypt:
		return true
	default: // RemotePlain
		return m.Shared() || publishMode(m) == "on"
	}
}

// ComputePolicy derives the per-brain BrainPolicy. Pure: a deterministic
// function of (manifest, remote kind) with no IO — remoteKind does the IO
// and hands kind in, so the whole policy table is unit-testable without a
// daemon or git (ARCH-PURE).
func ComputePolicy(m brain.Manifest, kind RemoteKind) BrainPolicy {
	sync := syncParticipant(m, kind)
	return BrainPolicy{
		Commit: m.AutosaveEnabled(),
		// Push only when we're a sync participant AND push isn't paused.
		// `publish: off` pauses the push half only — pull keeps running.
		Push: sync && publishMode(m) != "off",
		Pull: sync,
		// Keys-sync + auto-admit are gcrypt/shared-specific (no keys
		// branch on a plain private brain); only run them when syncing.
		KeysAdmit: sync && (kind == RemoteGcrypt || m.Shared()),
	}
}
