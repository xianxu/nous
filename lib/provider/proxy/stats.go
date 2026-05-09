package proxy

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
)

// statsBodyCap is the maximum number of bytes charon samples for
// Tier 2 (JSON top-level array counting). Above the cap, the body
// passes through untouched and items_returned is omitted. 1 MiB is
// generous for typical API list responses (Google's mailbox list,
// OpenAI's models list, GitHub repos, …) without holding multi-megabyte
// payloads in memory.
const statsBodyCap = 1 << 20 // 1 MiB

// bodyTap wraps an io.ReadCloser and: (1) tees the first
// statsBodyCap bytes into an in-memory buffer for content sampling,
// and (2) counts total bytes read. The Read method passes everything
// through unchanged — only the sampling is non-destructive
// observation.
//
// Reads above the cap are passthrough — sample stays bounded; total
// keeps counting. After the response is fully written, callers
// inspect Total and (if not Capped) Sample.
type bodyTap struct {
	src    io.ReadCloser
	sample bytes.Buffer
	cap    int

	Total  int64 // total bytes read from src
	Capped bool  // true once Total > cap; sample is incomplete
}

func newBodyTap(src io.ReadCloser, cap int) *bodyTap {
	return &bodyTap{src: src, cap: cap}
}

func (b *bodyTap) Read(p []byte) (int, error) {
	n, err := b.src.Read(p)
	if n > 0 {
		b.Total += int64(n)
		if !b.Capped {
			room := b.cap - b.sample.Len()
			if room <= 0 {
				b.Capped = true
			} else {
				take := n
				if take > room {
					take = room
				}
				b.sample.Write(p[:take])
				if take < n {
					b.Capped = true
				}
			}
		}
	}
	return n, err
}

func (b *bodyTap) Close() error { return b.src.Close() }

// TODO(#16 G): document the content-sampling posture shift in
// docs/threat-model.md — the proxy now reads response *content*, not
// just headers. countTopLevelItems is constructed so it can ONLY
// emit (int, bool); no path through which keys/values can leak. The
// implementation contract is sound; only the doc is pending.

// countTopLevelItems returns (count, ok) for a JSON byte slice.
//
// `ok=true` cases:
//   - top-level is an array: count = len(array).
//   - top-level is an object: count = sum of len() across array-
//     valued fields. Captures conventional list shapes — Google's
//     `{"messages":[...]}`, OpenAI's `{"data":[...]}`, GitHub's
//     `{"items":[...]}`, AWS-style `{"Items":[...]}`.
//
// `ok=false` cases (no count emitted in audit):
//   - parse error.
//   - top-level is a scalar (string, number, bool, null) or no
//     array fields under an object — nothing to count.
//
// Per the spec's threat-model implication: this function looks at
// JSON structure (top-level keys, array lengths) but never logs
// keys or values themselves.
func countTopLevelItems(data []byte) (int, bool) {
	// `null` decodes successfully into both `[]json.RawMessage` and
	// `map[string]json.RawMessage` (nil), so check explicitly so we
	// don't return a meaningless 0-count.
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return 0, false
	}

	// Try top-level array first.
	var arr []json.RawMessage
	if err := json.Unmarshal(data, &arr); err == nil {
		return len(arr), true
	}
	// Fall through to top-level object.
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return 0, false
	}
	total := 0
	found := false
	for _, raw := range obj {
		// Cheap discriminator: array values start with '[' after
		// any leading whitespace. Avoids decoding scalars/objects
		// we don't care about.
		trimmed := bytes.TrimLeft(raw, " \t\n\r")
		if len(trimmed) == 0 || trimmed[0] != '[' {
			continue
		}
		var sub []json.RawMessage
		if err := json.Unmarshal(raw, &sub); err != nil {
			continue
		}
		total += len(sub)
		found = true
	}
	if !found {
		return 0, false
	}
	return total, true
}

// isJSONContentType reports whether the response is plain JSON we
// can sample. Streaming JSON-shaped content-types (SSE, NDJSON) are
// excluded — they're meant to be consumed incrementally and don't
// have a single top-level structure to walk.
func isJSONContentType(ct string) bool {
	if ct == "" {
		return false
	}
	// Strip parameters and whitespace BEFORE lowercasing so trailing
	// space (e.g. "application/json ;charset=utf-8") doesn't break
	// the equality check below.
	if i := strings.Index(ct, ";"); i >= 0 {
		ct = ct[:i]
	}
	ct = strings.ToLower(strings.TrimSpace(ct))
	switch ct {
	case "application/json":
		return true
	case "application/x-ndjson", "application/jsonl",
		"text/event-stream":
		return false
	}
	// Many APIs use "application/<vendor>+json" — treat those as JSON.
	return strings.HasSuffix(ct, "+json")
}
