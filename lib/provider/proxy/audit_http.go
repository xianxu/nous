package proxy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// handleAuditRecent serves the in-memory ring buffer as a JSON
// array. Filters by `since` query parameter (Go duration format,
// e.g. "1h", "30m"; default "1h"). Used by `charon who` /
// `charon stats` (#16 F).
func (s *Server) handleAuditRecent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	if s.Audit == nil {
		http.Error(w, "audit log not configured", http.StatusServiceUnavailable)
		return
	}
	sinceStr := r.URL.Query().Get("since")
	if sinceStr == "" {
		sinceStr = "1h"
	}
	since, err := time.ParseDuration(sinceStr)
	if err != nil {
		http.Error(w, fmt.Sprintf("invalid since: %v", err), http.StatusBadRequest)
		return
	}
	cutoff := time.Now().Add(-since)
	entries := s.Audit.Recent(cutoff)
	w.Header().Set("Content-Type", "application/json")
	if entries == nil {
		entries = []AuditEntry{}
	}
	_ = json.NewEncoder(w).Encode(entries)
}
