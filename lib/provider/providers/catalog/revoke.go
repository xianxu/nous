package catalog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// httpClient backs revoke dispatcher calls with a sane timeout
// even when the caller forgot to thread a deadline through their
// context. The TUI passes a 30s ctx to RevokeKey already; this
// is a backstop for any future non-TUI caller (CLI subcommand,
// test harness, ad-hoc tool) that uses background context.
var httpClient = &http.Client{Timeout: 30 * time.Second}

// ErrNoRevokeEndpoint is returned by Entry.Revoke when the catalog
// entry has no revoke schema declared. Callers fall back to local
// delete + console_url message.
var ErrNoRevokeEndpoint = errors.New("catalog: entry has no revoke endpoint")

// ErrKeyNotFound is returned when a list_endpoint lookup completes
// but no entry's partial_key_hint matches the pasted key. The caller
// can still proceed with local-delete; the underlying key remains
// active at the provider until the user revokes it manually.
var ErrKeyNotFound = errors.New("catalog: pasted key not found in list endpoint")

// RevokeKey deactivates a pasted key upstream per the entry's revoke
// schema. Two shapes:
//
//  1. Direct: Method + URL is called with the pasted key substituted
//     into {key_id} (and used as the bearer per entry.Auth).
//  2. List-then-revoke: ListEndpoint is GET'd with the pasted key as
//     auth, the response is searched for an entry whose
//     partial_key_hint matches the pasted key's suffix, that entry's
//     id is substituted into Method + URL.
//
// Best-effort: any error is wrapped and returned to the caller; the
// caller decides whether to still proceed with local-delete (current
// policy in the TUI: yes — the user wants the credential gone, with
// the upstream-orphan caveat surfaced via console_url).
//
// Method name avoids colliding with the Revoke schema field.
func (e Entry) RevokeKey(ctx context.Context, pastedKey string) error {
	if e.Revoke == nil {
		return ErrNoRevokeEndpoint
	}
	keyID := pastedKey
	if e.Revoke.ListEndpoint != nil {
		var err error
		keyID, err = lookupKeyID(ctx, *e.Revoke.ListEndpoint, pastedKey, e.Auth)
		if err != nil {
			return err
		}
	}
	return callRevoke(ctx, *e.Revoke, keyID, pastedKey, e.Auth)
}

// lookupKeyID GETs the list endpoint with the pasted key as auth,
// then walks the JSON response looking for an entry whose
// partial_key_hint matches the pasted key's suffix.
//
// Today only "partial_key_hint" KeyMatch is supported. result_path
// is interpreted as "<array-field>[].<id-field>" (Anthropic's shape);
// future providers with different result paths would need a real
// JSONPath dependency.
func lookupKeyID(ctx context.Context, le ListEndpoint, pastedKey string, auth Auth) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", le.URL, nil)
	if err != nil {
		return "", fmt.Errorf("build list request: %w", err)
	}
	applyAuth(req, auth, pastedKey)

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("list request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("list endpoint %d: %s", resp.StatusCode, snippet(body))
	}

	arrayField, idField, err := parseResultPath(le.ResultPath)
	if err != nil {
		return "", err
	}

	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		return "", fmt.Errorf("parse list response: %w", err)
	}
	rawArr, ok := envelope[arrayField]
	if !ok {
		return "", fmt.Errorf("list response missing %q field", arrayField)
	}
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal(rawArr, &rows); err != nil {
		return "", fmt.Errorf("parse list rows: %w", err)
	}
	for _, row := range rows {
		var hint string
		if raw, ok := row["partial_key_hint"]; ok {
			_ = json.Unmarshal(raw, &hint)
		}
		if !matchesPartialKeyHint(hint, pastedKey) {
			continue
		}
		raw, ok := row[idField]
		if !ok {
			return "", fmt.Errorf("matched row missing %q field", idField)
		}
		var id string
		if err := json.Unmarshal(raw, &id); err != nil {
			return "", fmt.Errorf("parse id field: %w", err)
		}
		return id, nil
	}
	return "", fmt.Errorf("%w (checked %d entries)", ErrKeyNotFound, len(rows))
}

// callRevoke issues the configured revoke request for the resolved
// keyID. Treats any non-2xx as an error; the body snippet is
// included in the error message so 4xx/5xx debugging doesn't require
// re-running with verbose logging.
func callRevoke(ctx context.Context, r Revoke, keyID, pastedKey string, auth Auth) error {
	url := strings.ReplaceAll(r.URL, "{key_id}", keyID)
	var body io.Reader
	if r.Body != "" {
		body = bytes.NewBufferString(r.Body)
	}
	req, err := http.NewRequestWithContext(ctx, r.Method, url, body)
	if err != nil {
		return fmt.Errorf("build revoke request: %w", err)
	}
	if r.Body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	applyAuth(req, auth, pastedKey)
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("revoke request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("revoke endpoint %d: %s", resp.StatusCode, snippet(respBody))
	}
	return nil
}

// applyAuth attaches the pasted key to the request per entry.Auth.
// Mirrors what the proxy does for in-flight requests, except the
// dispatcher operates on a single fixed request rather than a stream.
func applyAuth(req *http.Request, auth Auth, key string) {
	switch auth.Style {
	case "bearer":
		prefix := auth.Prefix
		if prefix == "" {
			prefix = "Bearer "
		}
		req.Header.Set("Authorization", prefix+key)
	case "header":
		req.Header.Set(auth.Header, auth.Prefix+key)
	case "query":
		param := auth.Param
		if param == "" {
			param = "key"
		}
		q := req.URL.Query()
		q.Set(param, key)
		req.URL.RawQuery = q.Encode()
	}
	for k, v := range auth.ExtraHeaders {
		req.Header.Set(k, v)
	}
}

// matchesPartialKeyHint returns true when pastedKey ends with the
// suffix exposed by hint. Hint formats handled:
//
//   "prefix…suffix"  → match if pastedKey ends with "suffix"
//   "prefix...suffix" → same, ASCII-dot variant
//   "prefix"          → match if pastedKey ends with "prefix"
//                       (no ellipsis means hint is the suffix itself)
//
// The unicode ellipsis (U+2026 "…") is what Anthropic returns;
// the ASCII variant is accepted for robustness.
func matchesPartialKeyHint(hint, pastedKey string) bool {
	if pastedKey == "" {
		return false
	}
	suffix := hint
	if i := strings.LastIndex(hint, "…"); i >= 0 {
		suffix = hint[i+len("…"):]
	} else if i := strings.LastIndex(hint, "..."); i >= 0 {
		suffix = hint[i+len("..."):]
	}
	if suffix == "" {
		return false
	}
	return strings.HasSuffix(pastedKey, suffix)
}

// parseResultPath decodes "field[].subfield" into ("field",
// "subfield"). Anthropic's shape is "data[].id". More complex paths
// would need a real JSONPath library; rejecting them here keeps the
// catalog YAML's expressiveness aligned with what's actually
// supported.
func parseResultPath(p string) (arrayField, idField string, err error) {
	const sep = "[]."
	i := strings.Index(p, sep)
	if i < 0 {
		return "", "", fmt.Errorf("result_path %q: expected shape <array>[].<field> (e.g. \"data[].id\")", p)
	}
	arrayField = p[:i]
	idField = p[i+len(sep):]
	if arrayField == "" || idField == "" {
		return "", "", fmt.Errorf("result_path %q: empty array or id segment (use shape <array>[].<field>)", p)
	}
	if strings.ContainsAny(idField, ".[]") {
		return "", "", fmt.Errorf("result_path %q: nested id paths not supported (use shape <array>[].<field>)", p)
	}
	return arrayField, idField, nil
}

// snippet bounds a body excerpt so error messages don't dump
// kilobytes of provider HTML on a transient outage.
func snippet(b []byte) string {
	const max = 200
	s := strings.TrimSpace(string(b))
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}
