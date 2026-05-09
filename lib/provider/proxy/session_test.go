package proxy

import (
	"testing"
	"time"
)

// freshSession returns a session with an injected clock so tests can
// drive time forward deterministically.
func freshSession() (*Session, *fakeClock) {
	clk := &fakeClock{t: time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)}
	s := NewSession()
	s.now = clk.Now
	return s, clk
}

type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time { return c.t }

func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func TestSession_BootsDisarmed(t *testing.T) {
	s, _ := freshSession()
	if s.IsArmed() {
		t.Error("fresh session must boot disarmed")
	}
	if got := s.Status(); got.Armed {
		t.Errorf("Status() = %+v, want Armed=false", got)
	}
}

func TestSession_ArmThenIsArmed(t *testing.T) {
	s, _ := freshSession()
	s.Arm(15 * time.Minute)
	if !s.IsArmed() {
		t.Error("after Arm, IsArmed should be true")
	}
	st := s.Status()
	if !st.Armed {
		t.Error("Status.Armed should be true after Arm")
	}
	if st.TTLRemaining <= 0 {
		t.Errorf("TTLRemaining = %v, want positive", st.TTLRemaining)
	}
}

func TestSession_ZeroTTLDefaultsToOneHour(t *testing.T) {
	s, _ := freshSession()
	s.Arm(0)
	st := s.Status()
	// Absolute cap should be exactly armedAt + 1h.
	want := st.ArmedAt.Add(SessionDefaultTTL)
	if !st.AbsoluteCapAt.Equal(want) {
		t.Errorf("AbsoluteCapAt = %v, want %v", st.AbsoluteCapAt, want)
	}
}

func TestSession_RequestedTTLCappedAtAbsolute(t *testing.T) {
	s, _ := freshSession()
	// Asking for 24h must be capped at SessionAbsoluteCap (8h).
	s.Arm(24 * time.Hour)
	st := s.Status()
	want := st.ArmedAt.Add(SessionAbsoluteCap)
	if !st.AbsoluteCapAt.Equal(want) {
		t.Errorf("AbsoluteCapAt = %v, want %v", st.AbsoluteCapAt, want)
	}
}

func TestSession_IdleAutoDisarm(t *testing.T) {
	s, clk := freshSession()
	s.Arm(8 * time.Hour) // long absolute cap so idle is the binding constraint
	clk.advance(SessionIdleTTL + time.Second)
	if s.IsArmed() {
		t.Error("session should auto-disarm after SessionIdleTTL of inactivity")
	}
	if s.Status().Armed {
		t.Error("Status should also reflect disarmed")
	}
}

func TestSession_AbsoluteCapAutoDisarm(t *testing.T) {
	s, clk := freshSession()
	s.Arm(8 * time.Hour)
	// Keep the idle timer alive by polling IsArmed periodically,
	// but blow past the absolute cap. This is the "chatty agent"
	// scenario the absolute cap exists to defend against.
	// 8h absolute cap / (15min advance per iter) = 32 iters, plus
	// slack to push past the cap. Each advance is half the idle
	// window so the idle timer keeps getting refreshed.
	for i := 0; i < 40; i++ {
		clk.advance(SessionIdleTTL / 2)
		if !s.IsArmed() {
			break
		}
	}
	if s.IsArmed() {
		t.Error("absolute cap should auto-disarm even with continuous activity")
	}
}

func TestSession_IsArmedRefreshesIdle(t *testing.T) {
	s, clk := freshSession()
	s.Arm(8 * time.Hour)
	// 25min later — still within idle window, should refresh it.
	clk.advance(25 * time.Minute)
	if !s.IsArmed() {
		t.Fatal("should still be armed at 25min")
	}
	// 20min more — would be 45min total without refresh, idle would
	// have fired. With refresh from the prior IsArmed call, only 20
	// min of "idle" have passed.
	clk.advance(20 * time.Minute)
	if !s.IsArmed() {
		t.Error("IsArmed should have refreshed the idle timer; expected still armed")
	}
}

func TestSession_StatusDoesNotRefreshIdle(t *testing.T) {
	s, clk := freshSession()
	s.Arm(8 * time.Hour)
	// Repeatedly check Status — must NOT bump the idle timer.
	for i := 0; i < 5; i++ {
		clk.advance(10 * time.Minute)
		_ = s.Status()
	}
	// At 50 min cumulative, idle timer (30 min) should have lapsed.
	if s.Status().Armed {
		t.Error("Status must not bump idle timer; expected disarmed after 50min of Status-only")
	}
}

func TestSession_DisarmIsImmediate(t *testing.T) {
	s, _ := freshSession()
	s.Arm(time.Hour)
	s.Disarm()
	if s.IsArmed() {
		t.Error("Disarm should immediately drop armed state")
	}
}

func TestSession_ReArmExtends(t *testing.T) {
	s, clk := freshSession()
	s.Arm(30 * time.Minute)
	clk.advance(20 * time.Minute)
	// Re-arm with a fresh hour. Replaces both timers.
	s.Arm(1 * time.Hour)
	st := s.Status()
	if st.AbsoluteCapAt.Sub(st.ArmedAt) != 1*time.Hour {
		t.Errorf("re-Arm should reset absolute cap; got %v from arm", st.AbsoluteCapAt.Sub(st.ArmedAt))
	}
}

func TestSession_StatusOnExpiredReturnsDisarmed(t *testing.T) {
	s, clk := freshSession()
	s.Arm(time.Minute)
	clk.advance(5 * time.Minute)
	st := s.Status()
	if st.Armed {
		t.Error("Status on expired session should return Armed=false")
	}
}
