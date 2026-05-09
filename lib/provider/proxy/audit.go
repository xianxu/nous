package proxy

import (
	"encoding/json"
	"io"
	"os"
	"sync"
	"time"
)

// AuditEntry represents a single proxied request in the audit log.
//
// Peer fields (Peer*) are best-effort caller identification —
// resolved once per CONNECT tunnel (not per request) and copied into
// each request entry. Display-quality only: the spec (#16 §3) is
// explicit that peer fields never gate auth. A connecting process
// can fork/exec between accept and lookup; the snapshot is observed-
// at-accept, not authoritative-at-attribution.
type AuditEntry struct {
	Timestamp  time.Time `json:"ts"`
	Method     string    `json:"method"`
	Host       string    `json:"host"`
	Path       string    `json:"path"`
	StatusCode int       `json:"status"`
	LatencyMs  int64     `json:"latency_ms"`
	Provider   string    `json:"provider,omitempty"`
	Account    string    `json:"account,omitempty"`
	Error      string    `json:"error,omitempty"`

	// Peer caller identification (#16 B). Populated when the lookup
	// at CONNECT time succeeded; absent when it failed (process
	// gone, table churn, lsof unavailable).
	PeerPID         int         `json:"peer_pid,omitempty"`
	PeerExe         string      `json:"peer_exe,omitempty"`
	PeerArgv0       string      `json:"peer_argv0,omitempty"`
	PeerParentChain []ParentRef `json:"peer_parent_chain,omitempty"`

	// Stats (#16 E). Tier 1 (req_bytes/resp_bytes/content_type) is
	// always populated for proxied requests. Tier 2 (items_returned)
	// only when the response was JSON, within statsBodyCap, and the
	// top-level structure had a countable shape.
	ReqBytes         int64  `json:"req_bytes,omitempty"`
	RespBytes        int64  `json:"resp_bytes,omitempty"`
	RespContentType  string `json:"resp_content_type,omitempty"`
	// ItemsReturned is a *int (not int) so consumers can distinguish
	// "we counted 0 items" from "we didn't count" — the latter
	// happens for non-JSON responses, oversize responses (skipped
	// past statsBodyCap), or parse failures. Bytes don't have this
	// ambiguity since 0-bytes is itself meaningful and unambiguous.
	ItemsReturned    *int   `json:"items_returned,omitempty"`
}

// auditRingSize bounds the in-memory ring buffer of recent audit
// entries. Sized for a few hours of moderate traffic; trim from the
// oldest end when full. Memory cost: ~100KB at typical entry size.
const auditRingSize = 5000

// AuditLog writes append-only JSON lines to a log file AND keeps a
// bounded in-memory ring of recent entries that `charon who` and
// `charon stats` query via the proxy's /audit/recent endpoint.
type AuditLog struct {
	mu sync.Mutex
	w  io.WriteCloser

	// ring is a FIFO of the most recent auditRingSize entries.
	// Older entries get dropped as new ones arrive. The ring is
	// in-memory only — process restart loses it; the file (if
	// configured) is the persistent record.
	ring []AuditEntry
}

// NewAuditLog creates an audit log writer. If path is empty, writes to stderr.
func NewAuditLog(path string) (*AuditLog, error) {
	if path == "" {
		return &AuditLog{w: nopCloseWriter{os.Stderr}}, nil
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	return &AuditLog{w: f}, nil
}

// Log writes an audit entry to the file/stderr AND records it in the
// in-memory ring for /audit/recent queries.
func (a *AuditLog) Log(entry AuditEntry) {
	a.mu.Lock()
	defer a.mu.Unlock()
	data, _ := json.Marshal(entry)
	data = append(data, '\n')
	_, _ = a.w.Write(data)

	// Append to ring; drop oldest if full.
	if len(a.ring) >= auditRingSize {
		copy(a.ring, a.ring[1:])
		a.ring = a.ring[:auditRingSize-1]
	}
	a.ring = append(a.ring, entry)
}

// Recent returns audit entries with Timestamp >= cutoff. Returned
// slice is a copy — callers may freely modify without holding the
// lock.
func (a *AuditLog) Recent(cutoff time.Time) []AuditEntry {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]AuditEntry, 0, len(a.ring))
	for _, e := range a.ring {
		if !e.Timestamp.Before(cutoff) {
			out = append(out, e)
		}
	}
	return out
}

// Close closes the underlying file.
func (a *AuditLog) Close() error {
	return a.w.Close()
}

// NopAuditLog returns an audit log that discards entries (for testing).
func NopAuditLog() *AuditLog {
	return &AuditLog{w: nopWriteCloser{}}
}

type nopWriteCloser struct{}

func (nopWriteCloser) Write(p []byte) (int, error) { return len(p), nil }
func (nopWriteCloser) Close() error                { return nil }

// nopCloseWriter wraps a writer and makes Close a no-op (for stderr).
type nopCloseWriter struct{ io.Writer }

func (nopCloseWriter) Close() error { return nil }
