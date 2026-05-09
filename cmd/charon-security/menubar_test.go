package main

import (
	"testing"
	"time"
)

func TestMenubarTitle(t *testing.T) {
	if got := menubarTitle(true, "27m"); got != "● 27m" {
		t.Errorf("armed title = %q, want '● 27m'", got)
	}
	if got := menubarTitle(false, "off"); got != "○ off" {
		t.Errorf("disarmed title = %q, want '○ off'", got)
	}
}

func TestSummarize(t *testing.T) {
	if got := summarize(false, time.Hour); got != "off" {
		t.Errorf("disarmed summary = %q, want 'off' regardless of ttl", got)
	}
	if got := summarize(true, 27*time.Minute); got != "27m" {
		t.Errorf("armed summary = %q, want '27m'", got)
	}
}

func TestHumanDuration(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{0, "0s"},
		{59 * time.Second, "59s"},
		{60 * time.Second, "1m"},
		{27 * time.Minute, "27m"},
		{59 * time.Minute, "59m"},
		{1 * time.Hour, "1h"},
		{1*time.Hour + 12*time.Minute, "1h12m"},
		{8 * time.Hour, "8h"},
		{-5 * time.Second, "0s"}, // negative clamps to 0
	}
	for _, c := range cases {
		if got := humanDuration(c.d); got != c.want {
			t.Errorf("humanDuration(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}
