package proxy

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAuditLogWritesJSONLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	log, err := NewAuditLog(path)
	if err != nil {
		t.Fatal(err)
	}

	log.Log(AuditEntry{
		Timestamp: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Method:    "GET",
		Host:      "gmail.googleapis.com",
		Path:      "/gmail/v1/users/me/profile",
		StatusCode: 200,
		LatencyMs: 42,
		Provider:  "google",
		Account:   "user@gmail.com",
	})
	log.Log(AuditEntry{
		Timestamp: time.Date(2026, 1, 1, 0, 0, 1, 0, time.UTC),
		Method:    "POST",
		Host:      "gmail.googleapis.com",
		Path:      "/gmail/v1/users/me/messages/send",
		StatusCode: 401,
		LatencyMs: 5,
		Provider:  "google",
		Error:     "token expired",
	})
	log.Close()

	// Read and parse.
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	var entries []AuditEntry
	for scanner.Scan() {
		var e AuditEntry
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			t.Fatalf("invalid JSON line: %v", err)
		}
		entries = append(entries, e)
	}

	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	if entries[0].Method != "GET" || entries[0].StatusCode != 200 {
		t.Errorf("entry 0: got method=%s status=%d", entries[0].Method, entries[0].StatusCode)
	}
	if entries[0].Provider != "google" || entries[0].Account != "user@gmail.com" {
		t.Errorf("entry 0: got provider=%s account=%s", entries[0].Provider, entries[0].Account)
	}
	if entries[1].Error != "token expired" {
		t.Errorf("entry 1: expected error 'token expired', got %q", entries[1].Error)
	}
}

func TestAuditLogDefaultPath(t *testing.T) {
	// Test with a temp dir to avoid writing to real home directory.
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	log, err := NewAuditLog(path)
	if err != nil {
		t.Fatal(err)
	}
	log.Close()
	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected audit log file to exist at %s", path)
	}
}

func TestAuditLogAppends(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	// Write first entry.
	log1, _ := NewAuditLog(path)
	log1.Log(AuditEntry{Method: "GET"})
	log1.Close()

	// Write second entry (append).
	log2, _ := NewAuditLog(path)
	log2.Log(AuditEntry{Method: "POST"})
	log2.Close()

	// Should have 2 lines.
	data, _ := os.ReadFile(path)
	lines := 0
	for _, b := range data {
		if b == '\n' {
			lines++
		}
	}
	if lines != 2 {
		t.Errorf("expected 2 lines, got %d", lines)
	}
}

func TestNopAuditLog(t *testing.T) {
	log := NopAuditLog()
	// Should not panic.
	log.Log(AuditEntry{Method: "GET"})
	log.Close()
}
