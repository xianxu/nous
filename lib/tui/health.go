package tui

import "github.com/xianxu/nous/lib/provider/vault"

// AccountHealth describes whether a stored OAuth credential's
// refresh-token still works against the issuer. Surfaced as inline
// badges in the provider picker (per-provider count) and the account
// picker (per-account badge), per nous#15.
//
// String-typed so lib/tui stays domain-neutral — callers in
// lib/charoncli adapt from oauth.HealthState to tui.AccountHealth.
// Future brain TUIs that import lib/tui won't transitively pull in
// lib/provider/oauth.
type AccountHealth string

const (
	// AccountHealthUnchecked is the initial state before any check
	// has run, or when checks are disabled (no checker configured).
	// Renders as no badge — distinct from Unknown which renders as
	// "(?)" so the operator can see the check was attempted.
	AccountHealthUnchecked AccountHealth = ""

	// AccountHealthHealthy: refresh-token successfully exchanged for
	// a fresh access token at last check. Renders without a badge —
	// healthy is the default state, no UI noise.
	AccountHealthHealthy AccountHealth = "healthy"

	// AccountHealthNeedsReauth: issuer rejected the refresh token in
	// a way that won't recover from retry. Renders as "(needs reauth)"
	// inline. Operator press 'r' to trigger fresh OAuth (M3).
	AccountHealthNeedsReauth AccountHealth = "needs-reauth"

	// AccountHealthUnknown: transient failure (network, 5xx). Renders
	// as "(?)" so the operator knows the check ran but is inconclusive,
	// without penalizing them for transient infrastructure.
	AccountHealthUnknown AccountHealth = "unknown"
)

// AccountHealthChecker probes one credential and returns its health.
// nil checker → skip checks (used by tests + when health-surfacing
// isn't wired). Production wires an adapter over oauth.GoogleProvider.
// CheckHealth in lib/charoncli's AuthCmd.
type AccountHealthChecker func(*vault.Credential) AccountHealth
