package proxy

import (
	"sync"
	"time"
)

// SessionDefaultTTL is the time-to-live applied when Arm is called
// without an explicit duration. Absolute caps still apply on top.
const SessionDefaultTTL = 1 * time.Hour

// SessionAbsoluteCap is the longest a single arm can last regardless
// of activity. A chatty agent could otherwise keep the idle timer
// alive indefinitely on its own traffic; the absolute cap is the
// load-bearing safety net.
const SessionAbsoluteCap = 8 * time.Hour

// SessionIdleTTL is how long without proxy traffic before the
// session auto-disarms. Reset by every successful CONNECT gate.
const SessionIdleTTL = 30 * time.Minute

// Session is the proxy's runtime-consent state: a single armed/
// disarmed bit plus the timers that bound how long an arm can last.
//
// Boots disarmed. Re-arming is initiated by the user (CLI `charon arm`
// today; security.app menubar in #16 D). State lives in memory only —
// persisting armed-state across restarts would defeat the point (a
// forgotten armed jar surviving overnight is exactly what we're
// preventing).
//
// Expiry is computed lazily on every IsArmed/Status call rather than
// driven by a background goroutine. Two reasons: (1) no goroutine
// lifecycle to manage on Server shutdown, and (2) "armed but expired"
// is a degenerate state — by the time anyone observes IsArmed, the
// answer is already accurate.
type Session struct {
	mu             sync.Mutex
	armed          bool
	armedAt        time.Time // when the current arm started
	idleExpiresAt  time.Time // sliding window; bumped on activity
	absoluteCapAt  time.Time // hard ceiling on the current arm

	// now is injectable for tests. Defaults to time.Now in NewSession.
	now func() time.Time
}

// SessionStatus is an immutable snapshot of session state for callers
// that need to display or reason about it. JSON-shaped so it can ship
// directly through HTTP.
type SessionStatus struct {
	Armed         bool          `json:"armed"`
	ArmedAt       time.Time     `json:"armed_at,omitempty"`
	ExpiresAt     time.Time     `json:"expires_at,omitempty"`
	ExpiresReason string        `json:"expires_reason,omitempty"` // "idle" or "absolute"
	IdleExpiresAt time.Time     `json:"idle_expires_at,omitempty"`
	AbsoluteCapAt time.Time     `json:"absolute_cap_at,omitempty"`
	TTLRemaining  time.Duration `json:"ttl_remaining_ns,omitempty"`
}

// NewSession returns a Session in the disarmed state, with default
// timers. Call Arm to enable proxy traffic.
func NewSession() *Session {
	return &Session{now: time.Now}
}

// Arm enables proxy traffic for the requested duration (or
// SessionDefaultTTL when ttl is zero). The effective expiry is
// the lesser of the requested ttl and SessionAbsoluteCap. Calling
// Arm while already armed extends/replaces the timers — useful for
// the menubar's "extend by 1h" gesture.
func (s *Session) Arm(ttl time.Duration) {
	if ttl <= 0 {
		ttl = SessionDefaultTTL
	}
	if ttl > SessionAbsoluteCap {
		ttl = SessionAbsoluteCap
	}
	now := s.clock()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.armed = true
	s.armedAt = now
	s.absoluteCapAt = now.Add(ttl)
	s.idleExpiresAt = now.Add(SessionIdleTTL)
	if s.idleExpiresAt.After(s.absoluteCapAt) {
		s.idleExpiresAt = s.absoluteCapAt
	}
}

// Disarm immediately ends the current session. New CONNECTs will be
// rejected; per spec, in-flight tunnels drain rather than RST.
func (s *Session) Disarm() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.armed = false
	s.armedAt = time.Time{}
	s.idleExpiresAt = time.Time{}
	s.absoluteCapAt = time.Time{}
}

// IsArmed reports whether the session currently authorizes new
// CONNECT requests. Side effect: if armed and not yet expired, the
// idle timer is reset (this is the "activity" signal). Expiration is
// applied lazily — calling IsArmed after the timers have elapsed
// quietly transitions the session to disarmed.
//
// Callers that don't want to register activity (e.g. status display)
// should use Status instead.
func (s *Session) IsArmed() bool {
	now := s.clock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.armed {
		return false
	}
	if !now.Before(s.absoluteCapAt) || !now.Before(s.idleExpiresAt) {
		s.armed = false
		s.armedAt = time.Time{}
		s.idleExpiresAt = time.Time{}
		s.absoluteCapAt = time.Time{}
		return false
	}
	// Bump the idle window. Cap at the absolute ceiling.
	s.idleExpiresAt = now.Add(SessionIdleTTL)
	if s.idleExpiresAt.After(s.absoluteCapAt) {
		s.idleExpiresAt = s.absoluteCapAt
	}
	return true
}

// Status returns a snapshot of the session state without bumping the
// idle timer. The expires_at field reflects whichever of (idle,
// absolute) lapses sooner. Safe to call when armed=false (returns a
// zero-valued snapshot with Armed=false).
func (s *Session) Status() SessionStatus {
	now := s.clock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.armed {
		return SessionStatus{Armed: false}
	}
	// Apply lazy expiry: if either timer has lapsed, treat as
	// disarmed in the snapshot but DON'T mutate s — Status is read-
	// only. (IsArmed is the path that mutates on lapse.)
	if !now.Before(s.absoluteCapAt) || !now.Before(s.idleExpiresAt) {
		return SessionStatus{Armed: false}
	}
	expiresAt, reason := s.idleExpiresAt, "idle"
	if s.absoluteCapAt.Before(expiresAt) {
		expiresAt, reason = s.absoluteCapAt, "absolute"
	}
	return SessionStatus{
		Armed:         true,
		ArmedAt:       s.armedAt,
		ExpiresAt:     expiresAt,
		ExpiresReason: reason,
		IdleExpiresAt: s.idleExpiresAt,
		AbsoluteCapAt: s.absoluteCapAt,
		TTLRemaining:  expiresAt.Sub(now),
	}
}

func (s *Session) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}
