package brain

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/xianxu/nous/lib/brain/filestore"
	"github.com/xianxu/nous/lib/identity"
)

// verifiedFilename is the sidecar file under .brain/ that records
// out-of-band fingerprint verifications. Travels with the brain
// (committed alongside .brain/config.md, encrypted via gcrypt push
// the same as everything else).
const verifiedFilename = "verified.yaml"

// VerifiedEntry captures one row in verified.yaml. Keyed by
// github-login in the outer map; each entry pins the fingerprint
// verified-against at a specific moment, by a specific verifier.
//
// Field semantics:
//
//   - Fingerprint: the 40-hex uppercase fingerprint the operator
//     verified out of band. If the keys-branch pubkey for the same
//     login later carries a *different* fingerprint, that's drift —
//     auto-admit pauses for that login until re-verified.
//
//   - VerifiedBy: github-login of the operator who did the verify.
//     Informational; not a gating signal (any recipient can claim
//     they verified anyone). Mainly useful for the audit trail
//     ("xianxu verified ying's key on 2026-05-19").
//
//   - VerifiedAt: UTC, second-precision RFC3339 timestamp.
type VerifiedEntry struct {
	Fingerprint string    `yaml:"fingerprint"`
	VerifiedBy  string    `yaml:"verified_by"`
	VerifiedAt  time.Time `yaml:"verified_at"`
}

// Verified is the in-memory representation of verified.yaml —
// github-login → VerifiedEntry.
type Verified map[string]VerifiedEntry

// ReadVerified parses .brain/verified.yaml at brainRoot. Returns an
// empty map (not nil) when the file doesn't exist — every brain
// starts with zero verifications and accumulates them over time.
func ReadVerified(brainRoot string) (Verified, error) {
	path := filepath.Join(brainRoot, ".brain", verifiedFilename)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Verified{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var v Verified
	if err := yaml.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if v == nil {
		v = Verified{}
	}
	// Normalize fingerprint case on read so callers can compare
	// straight without strings.EqualFold dance.
	for k, e := range v {
		e.Fingerprint = strings.ToUpper(e.Fingerprint)
		v[k] = e
	}
	return v, nil
}

// WriteVerified atomically writes .brain/verified.yaml. Output is
// sorted by login for stable git diffs; yaml.v3's default map-
// marshaling has unstable order.
//
// An empty Verified map writes an empty file (rather than deleting),
// which lets a `git status` reader see "this brain has been touched
// by verify tooling" even when no entries are present.
func WriteVerified(brainRoot string, v Verified) error {
	if err := os.MkdirAll(filepath.Join(brainRoot, ".brain"), 0o755); err != nil {
		return fmt.Errorf("mkdir .brain: %w", err)
	}

	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	root := &yaml.Node{Kind: yaml.MappingNode}
	for _, k := range keys {
		e := v[k]
		root.Content = append(root.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: k},
			entryNode(e),
		)
	}

	out, err := yaml.Marshal(root)
	if err != nil {
		return fmt.Errorf("marshal verified: %w", err)
	}

	path := filepath.Join(brainRoot, ".brain", verifiedFilename)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	return os.Rename(tmp, path)
}

// RemoveVerifiedFor drops every verified.yaml entry whose fingerprint
// matches fp (case-insensitive) and returns the login(s) removed — the
// caller uses them to revoke the matching GitHub collaborator. No-op
// (returns nil, nil) when nothing matches, so it's safe to call on a
// brain that never recorded a verification. Without this, `recipient
// remove` leaves the login→fp mapping "verified", letting auto-admit
// silently re-admit on the next keys-branch re-publish (nous#38 leak #2).
func RemoveVerifiedFor(brainRoot, fp string) ([]string, error) {
	v, err := ReadVerified(brainRoot)
	if err != nil {
		return nil, err
	}
	fpUp := strings.ToUpper(strings.TrimSpace(fp))
	var removed []string
	for login, e := range v {
		if strings.ToUpper(e.Fingerprint) == fpUp {
			removed = append(removed, login)
			delete(v, login)
		}
	}
	if len(removed) == 0 {
		return nil, nil
	}
	sort.Strings(removed)
	if err := WriteVerified(brainRoot, v); err != nil {
		return removed, err
	}
	return removed, nil
}

// LoginForFingerprint scans the brain's keys branch for a
// `<login>.asc` whose pubkey content has fingerprint `fp`, and
// returns the login (filename stem). Returns ("", nil) when no
// matching entry exists — typically because the recipient was
// admitted through the legacy `<FP>.asc` sneakernet path which
// doesn't carry a github login.
//
// Used by the verify CLI to determine which login to write to
// verified.yaml after a successful OOB ceremony. When this returns
// "", the verify ceremony still works (operator confirms FP
// match) but the result isn't persisted — persistent verify is
// only available for recipients admitted via the GitHub-mediated
// flow (`<login>.asc`).
func LoginForFingerprint(ctx context.Context, brainRoot, fp string) (string, error) {
	store, err := filestore.Open(brainRoot, keysBranch)
	if err != nil {
		return "", fmt.Errorf("login lookup: open keys store: %w", err)
	}
	defer store.Close()

	files, err := store.List(ctx)
	if err != nil {
		return "", fmt.Errorf("login lookup: list keys: %w", err)
	}

	fpUp := strings.ToUpper(fp)
	for name, content := range files {
		if !strings.HasSuffix(name, pubkeyFilenameSuffix) {
			continue
		}
		stem := strings.TrimSuffix(name, pubkeyFilenameSuffix)
		if looksLikeFingerprint(stem) {
			continue
		}
		key, err := identity.Inspect(string(content))
		if err != nil {
			continue
		}
		if strings.EqualFold(key.Fingerprint, fpUp) {
			return stem, nil
		}
	}
	return "", nil
}

// FingerprintForLogin resolves a GitHub login to a fingerprint from the
// brain's state — the reverse of LoginForFingerprint. Sources in order:
// verified.yaml (login→fp) → keys branch (`<login>.asc` content) → peer
// sidecar (github_user). Returns ("", nil) when no source knows the login.
func FingerprintForLogin(ctx context.Context, brainRoot, login string) (string, error) {
	if v, err := ReadVerified(brainRoot); err == nil {
		for k, e := range v {
			if strings.EqualFold(k, login) && e.Fingerprint != "" {
				return strings.ToUpper(e.Fingerprint), nil
			}
		}
	}
	if store, err := filestore.Open(brainRoot, keysBranch); err == nil {
		defer store.Close()
		if files, lerr := store.List(ctx); lerr == nil {
			for name, content := range files {
				if !strings.HasSuffix(name, pubkeyFilenameSuffix) {
					continue
				}
				stem := strings.TrimSuffix(name, pubkeyFilenameSuffix)
				if !strings.EqualFold(stem, login) {
					continue
				}
				if key, ierr := identity.Inspect(string(content)); ierr == nil {
					return strings.ToUpper(key.Fingerprint), nil
				}
			}
		}
	}
	if metas, err := identity.ListPeerMeta(); err == nil {
		for _, pm := range metas {
			if strings.EqualFold(pm.GithubUser, login) && pm.Fingerprint != "" {
				return strings.ToUpper(pm.Fingerprint), nil
			}
		}
	}
	return "", nil
}

// entryNode builds a yaml.Node for one VerifiedEntry in a stable
// key order (fingerprint → verified_by → verified_at). Stable
// internal ordering matters as much as stable outer ordering for
// git-diff readability.
func entryNode(e VerifiedEntry) *yaml.Node {
	n := &yaml.Node{Kind: yaml.MappingNode}
	n.Content = append(n.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: "fingerprint"},
		&yaml.Node{Kind: yaml.ScalarNode, Value: strings.ToUpper(e.Fingerprint)},
		&yaml.Node{Kind: yaml.ScalarNode, Value: "verified_by"},
		&yaml.Node{Kind: yaml.ScalarNode, Value: e.VerifiedBy},
		&yaml.Node{Kind: yaml.ScalarNode, Value: "verified_at"},
		&yaml.Node{Kind: yaml.ScalarNode, Value: e.VerifiedAt.UTC().Format(time.RFC3339)},
	)
	return n
}
