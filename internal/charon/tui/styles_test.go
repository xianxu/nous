package tui

import (
	"strings"
	"testing"
)

func TestHyperlink_WrapsTextWithOSC8Sequence(t *testing.T) {
	got := hyperlink("https://example.com/keys", "click here")
	// OSC 8 spec: ESC ] 8 ; ; URL ST text ESC ] 8 ; ; ST
	// where ST is ESC \ (or ESC \\ in Go string literals).
	wantPrefix := "\x1b]8;;https://example.com/keys\x1b\\"
	wantSuffix := "\x1b]8;;\x1b\\"
	if !strings.HasPrefix(got, wantPrefix) {
		t.Errorf("missing OSC 8 anchor prefix:\n  got:  %q\n  want prefix: %q", got, wantPrefix)
	}
	if !strings.HasSuffix(got, wantSuffix) {
		t.Errorf("missing OSC 8 close:\n  got:  %q\n  want suffix: %q", got, wantSuffix)
	}
	// Visible text must be present between the anchors.
	if !strings.Contains(got, "click here") {
		t.Errorf("visible text not preserved:\n  got: %q", got)
	}
}

func TestHyperlink_EmptyURLPassesTextThrough(t *testing.T) {
	got := hyperlink("", "plain text")
	if got != "plain text" {
		t.Errorf("hyperlink(\"\", text) = %q, want unwrapped passthrough", got)
	}
}

func TestHyperlink_EmptyTextFallsBackToURL(t *testing.T) {
	got := hyperlink("https://example.com", "")
	if !strings.Contains(got, "https://example.com") {
		t.Errorf("URL not used as fallback label:\n  got: %q", got)
	}
}

func TestHyperlink_ComposesWithLipglossStyledLabel(t *testing.T) {
	// Pre-styled label (SGR codes from mutedStyle) should land
	// inside the OSC 8 anchor so terminals apply both color and
	// clickability.
	styled := mutedStyle.Render("https://example.com")
	got := hyperlink("https://example.com", styled)
	if !strings.Contains(got, styled) {
		t.Errorf("styled label lost in wrapping:\n  styled: %q\n  got:    %q", styled, got)
	}
	// And the OSC 8 anchor still wraps the styled bytes.
	if !strings.HasPrefix(got, "\x1b]8;;") || !strings.HasSuffix(got, "\x1b]8;;\x1b\\") {
		t.Errorf("OSC 8 anchors missing around styled label:\n  got: %q", got)
	}
}
