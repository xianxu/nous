package proxy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// /session/{arm,disarm,status} HTTP handlers. Today these are POST-able
// from anywhere on the local machine — 127.0.0.1 binding means same-
// host, but NOT same-uid (any process on the box can reach these
// endpoints). #16 C will gate arm/disarm behind a unix-socket DR-
// pinned trust edge so only Charon Security.app can drive them.
// Until C lands, treat the gate as advisory only.

type armRequest struct {
	// TTLSeconds is the requested arm duration. 0 means default
	// (SessionDefaultTTL). Capped at SessionAbsoluteCap.
	TTLSeconds int64 `json:"ttl_seconds,omitempty"`
}

type armResponse struct {
	OK     bool          `json:"ok"`
	Status SessionStatus `json:"status"`
}

func (s *Server) handleSessionArm(w http.ResponseWriter, r *http.Request) {
	if s.Session == nil {
		http.Error(w, "session not configured", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var req armRequest
	// Empty body is fine — defaults to SessionDefaultTTL.
	if r.Body != nil && r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf("invalid body: %v", err), http.StatusBadRequest)
			return
		}
	}
	ttl := time.Duration(req.TTLSeconds) * time.Second
	s.Session.Arm(ttl)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(armResponse{OK: true, Status: s.Session.Status()})
}

func (s *Server) handleSessionDisarm(w http.ResponseWriter, r *http.Request) {
	if s.Session == nil {
		http.Error(w, "session not configured", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	s.Session.Disarm()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(armResponse{OK: true, Status: s.Session.Status()})
}

func (s *Server) handleSessionStatus(w http.ResponseWriter, r *http.Request) {
	if s.Session == nil {
		http.Error(w, "session not configured", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.Session.Status())
}
