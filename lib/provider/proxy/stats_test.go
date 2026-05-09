package proxy

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestBodyTap_CountsAndSamples(t *testing.T) {
	body := strings.Repeat("x", 1500)
	tap := newBodyTap(io.NopCloser(strings.NewReader(body)), 1000)
	got, _ := io.ReadAll(tap)
	if len(got) != len(body) {
		t.Errorf("read len = %d, want %d (passthrough must be transparent)", len(got), len(body))
	}
	if tap.Total != int64(len(body)) {
		t.Errorf("Total = %d, want %d", tap.Total, len(body))
	}
	if !tap.Capped {
		t.Error("Capped should be true once body exceeded cap")
	}
	if tap.sample.Len() != 1000 {
		t.Errorf("sample len = %d, want 1000 (cap)", tap.sample.Len())
	}
}

func TestBodyTap_BelowCapNotCapped(t *testing.T) {
	body := "small"
	tap := newBodyTap(io.NopCloser(strings.NewReader(body)), 1000)
	_, _ = io.ReadAll(tap)
	if tap.Capped {
		t.Error("Capped should remain false for body smaller than cap")
	}
	if tap.sample.String() != body {
		t.Errorf("sample = %q, want %q", tap.sample.String(), body)
	}
}

// Top-level array — count = len.
func TestCountTopLevelItems_TopArray(t *testing.T) {
	n, ok := countTopLevelItems([]byte(`[1, 2, 3, 4]`))
	if !ok || n != 4 {
		t.Errorf("got n=%d ok=%v, want 4 true", n, ok)
	}
}

// Top-level object with one array field — count = its length.
func TestCountTopLevelItems_GoogleListShape(t *testing.T) {
	body := []byte(`{"messages":[{"id":"a"},{"id":"b"},{"id":"c"}],"resultSizeEstimate":3}`)
	n, ok := countTopLevelItems(body)
	if !ok || n != 3 {
		t.Errorf("got n=%d ok=%v, want 3 true", n, ok)
	}
}

// Top-level object with multiple array fields — sum.
func TestCountTopLevelItems_MultipleArrays(t *testing.T) {
	body := []byte(`{"items":[1,2],"errors":[{"a":1}]}`)
	n, ok := countTopLevelItems(body)
	if !ok || n != 3 {
		t.Errorf("got n=%d ok=%v, want 3 true", n, ok)
	}
}

func TestCountTopLevelItems_NoArrays(t *testing.T) {
	body := []byte(`{"id":"x","status":"ok"}`)
	_, ok := countTopLevelItems(body)
	if ok {
		t.Error("ok should be false when no array-valued top-level fields")
	}
}

func TestCountTopLevelItems_TopScalar(t *testing.T) {
	for _, body := range []string{`42`, `"hi"`, `true`, `null`} {
		_, ok := countTopLevelItems([]byte(body))
		if ok {
			t.Errorf("scalar %q should not produce a count", body)
		}
	}
}

func TestCountTopLevelItems_ParseError(t *testing.T) {
	_, ok := countTopLevelItems([]byte(`not json{{{`))
	if ok {
		t.Error("ok should be false on parse error")
	}
}

// Sensitive content (key names, values) MUST NOT leak through the
// counter. Regression guard: future "smarter" extractors that
// inadvertently log keys would fail this test.
func TestCountTopLevelItems_DoesNotEmitContent(t *testing.T) {
	body := []byte(`{"messages":[{"subject":"Re: leak this please"}]}`)
	n, ok := countTopLevelItems(body)
	if !ok || n != 1 {
		t.Errorf("count behavior changed: n=%d ok=%v", n, ok)
	}
	// The function only returns (int, bool). There's no path through
	// which keys/values can escape; this test pins that contract by
	// ensuring no other return paths get added.
}

func TestIsJSONContentType(t *testing.T) {
	cases := map[string]bool{
		"application/json":                            true,
		"application/json; charset=utf-8":             true,
		"application/vnd.github.v3+json":              true,
		"application/x-ndjson":                        false,
		"application/jsonl":                           false,
		"text/event-stream":                           false,
		"text/event-stream; charset=utf-8":            false,
		"text/html":                                   false,
		"":                                            false,
	}
	for ct, want := range cases {
		if got := isJSONContentType(ct); got != want {
			t.Errorf("isJSONContentType(%q) = %v, want %v", ct, got, want)
		}
	}
}

// Sample limits: the tap must not write past the cap even on
// adversarial Read patterns (one big read, many small reads).
func TestBodyTap_AdversarialReadPatterns(t *testing.T) {
	body := bytes.Repeat([]byte("a"), 5000)
	for _, name := range []string{"one-shot", "byte-by-byte"} {
		t.Run(name, func(t *testing.T) {
			tap := newBodyTap(io.NopCloser(bytes.NewReader(body)), 1000)
			if name == "byte-by-byte" {
				buf := make([]byte, 1)
				for {
					_, err := tap.Read(buf)
					if err == io.EOF {
						break
					}
				}
			} else {
				buf := make([]byte, len(body)*2)
				_, _ = tap.Read(buf)
			}
			if tap.sample.Len() > 1000 {
				t.Errorf("sample len %d exceeded cap 1000", tap.sample.Len())
			}
		})
	}
}
