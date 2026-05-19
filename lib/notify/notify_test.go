package notify

import (
	"errors"
	"sync/atomic"
	"testing"
)

func TestSend_UsesSetBackend(t *testing.T) {
	t.Cleanup(func() { SetBackend(nil) })

	var calls atomic.Int32
	var seen Notification
	SetBackend(func(n Notification) error {
		calls.Add(1)
		seen = n
		return nil
	})

	in := Notification{Title: "t", Subtitle: "s", Body: "b"}
	if err := Send(in); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("backend calls = %d, want 1", got)
	}
	if seen != in {
		t.Errorf("backend received %+v, want %+v", seen, in)
	}
}

func TestSend_PropagatesBackendError(t *testing.T) {
	t.Cleanup(func() { SetBackend(nil) })

	want := errors.New("boom")
	SetBackend(func(Notification) error { return want })

	got := Send(Notification{Title: "x", Body: "y"})
	if !errors.Is(got, want) {
		t.Errorf("Send returned %v, want %v", got, want)
	}
}

func TestSetBackend_NilResetsCache(t *testing.T) {
	t.Cleanup(func() { SetBackend(nil) })

	var firstCalls atomic.Int32
	SetBackend(func(Notification) error {
		firstCalls.Add(1)
		return nil
	})
	_ = Send(Notification{Title: "1"})

	// Clear; next Send must re-pick (which on test machines means
	// invoking pickBackend; we don't assert what it picks here, just
	// that the first backend isn't called again).
	SetBackend(nil)

	// Replace with a distinct backend so we can detect re-dispatch
	// without depending on the host's actual backend.
	var secondCalls atomic.Int32
	SetBackend(func(Notification) error {
		secondCalls.Add(1)
		return nil
	})
	_ = Send(Notification{Title: "2"})

	if firstCalls.Load() != 1 {
		t.Errorf("first backend called %d times, want 1", firstCalls.Load())
	}
	if secondCalls.Load() != 1 {
		t.Errorf("second backend called %d times, want 1", secondCalls.Load())
	}
}
