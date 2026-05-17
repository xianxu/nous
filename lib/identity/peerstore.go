package identity

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// PeerMeta is per-peer metadata that lives OUTSIDE the GPG keyring
// because gpg has nowhere natural to attach it. Specifically: the
// GitHub username paired with the peer's GPG key, used by `nous brain
// share` to add the peer as a collaborator on the brain's gcrypt
// remote when admitting them.
//
// One sidecar JSON file per fingerprint at PeerStorePath()/<FP>.json.
// Atomic writes via tmp-file + rename. Missing-file is not an error —
// peers admitted before this metadata existed simply have no record;
// callers receive ErrPeerMetaNotFound and fall back gracefully.
//
// Why a sidecar rather than a GPG UID notation: nous-specific metadata
// shouldn't be encoded into the GPG identity itself (a UID change
// requires re-signing and propagates oddly when the same key is held by
// peers on other tools); sidecar is local to this machine, easy to
// inspect, easy to delete.
type PeerMeta struct {
	// Fingerprint is the 40-hex uppercase primary key fingerprint, the
	// same anchor identity.Key.Fingerprint uses.
	Fingerprint string `json:"fingerprint"`

	// GithubUser is the peer's GitHub username, used by `nous brain
	// share` to add them as a collaborator. Empty when the operator
	// hasn't supplied one (legacy records / pre-prompt imports).
	GithubUser string `json:"github_user,omitempty"`

	// ImportedAt is when the sidecar was written. Audit only; not used
	// for any logic.
	ImportedAt time.Time `json:"imported_at"`
}

// ErrPeerMetaNotFound signals no sidecar exists for the given
// fingerprint. Callers should treat as "no metadata yet" rather than as
// a failure condition.
var ErrPeerMetaNotFound = errors.New("peer metadata not found")

// PeerStorePath returns the directory where per-peer sidecars live.
// Created on demand by SavePeerMeta. Override via $XDG_CONFIG_HOME for
// tests (os.UserConfigDir honors it).
func PeerStorePath() (string, error) {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locate user config dir: %w", err)
	}
	return filepath.Join(cfg, "nous", "peers"), nil
}

func peerMetaFile(fp string) (string, error) {
	dir, err := PeerStorePath()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, strings.ToUpper(fp)+".json"), nil
}

// SavePeerMeta writes a sidecar JSON file for the peer. Atomic via
// tmp-file + rename so a crash mid-write can't produce a corrupt
// half-record. Creates the peers/ directory on demand.
func SavePeerMeta(meta PeerMeta) error {
	if meta.Fingerprint == "" {
		return fmt.Errorf("PeerMeta.Fingerprint required")
	}
	dir, err := PeerStorePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	path, err := peerMetaFile(meta.Fingerprint)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename %s -> %s: %w", tmp, path, err)
	}
	return nil
}

// LoadPeerMeta reads the sidecar for the given fingerprint. Returns
// ErrPeerMetaNotFound (sentinel) when no sidecar exists.
func LoadPeerMeta(fp string) (PeerMeta, error) {
	path, err := peerMetaFile(fp)
	if err != nil {
		return PeerMeta{}, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return PeerMeta{}, ErrPeerMetaNotFound
	}
	if err != nil {
		return PeerMeta{}, fmt.Errorf("read %s: %w", path, err)
	}
	var m PeerMeta
	if err := json.Unmarshal(data, &m); err != nil {
		return PeerMeta{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return m, nil
}

// ListPeerMeta enumerates all sidecars in the peer store. Missing
// directory returns an empty slice (no peers admitted yet on this
// machine). Unreadable individual files are skipped — listing
// should not fail because one record is corrupt.
func ListPeerMeta() ([]PeerMeta, error) {
	dir, err := PeerStorePath()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}
	var out []PeerMeta
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		fp := strings.TrimSuffix(e.Name(), ".json")
		m, err := LoadPeerMeta(fp)
		if err != nil {
			continue
		}
		out = append(out, m)
	}
	return out, nil
}
