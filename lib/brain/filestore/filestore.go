// Package filestore is a small key-value file store backed by a plain
// (non-gcrypt) git branch on a brain's GitHub repo.
//
// The motivating use case: pubkey exchange between recipients of a
// gcrypt-encrypted shared brain. gcrypt manifests are signed with the
// producer's GPG key, so consumers need every other recipient's pubkey
// to verify before decrypting. The pubkeys themselves are public
// (anyone admitted to the brain inherently sees them); they don't need
// the gcrypt encryption layer; and the most natural place to put them
// is alongside the gcrypt blobs on the same GitHub repo, on a plain
// branch.
//
// This package hides every detail of doing that:
//
//   - A second remote pointing at the plain SSH URL of the same repo
//     (so the gcrypt:: helper doesn't intercept pushes to this branch).
//   - An orphan branch created on first publish if it doesn't exist
//     remotely.
//   - A shallow clone (depth=1, single-branch) maintained under
//     <brainRoot>/.git/nous-filestore/<branch>/, kept out of the
//     brain's gcrypt-encrypted working tree.
//   - Fetch-modify-push for every mutation, with retries on push
//     rejections from concurrent writers.
//
// Callers see only List / Put / Delete on byte-slice contents — no
// git vocabulary leaks out. That contract is load-bearing: when
// peerkeys (lib/brain/peerkeys.go) and the brain-sync watcher consume
// this package, they should never need to know "what branch" or
// "what remote" — only "which file at what path."
package filestore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// Store is the public API. Implementations may add concrete types
// (e.g., a memory-backed Store for tests not covered by the
// real-git fixture), but the interface is the contract.
type Store interface {
	// List returns every file under the store as name → content.
	// Refreshes from the remote before reading; new entries pushed
	// by other peers appear here once they've synced.
	List(ctx context.Context) (map[string][]byte, error)

	// Put writes or overwrites a file. Atomic from the caller's
	// perspective: either the new content is published to the
	// remote or an error is returned. Fetches latest before pushing;
	// retries on non-fast-forward rejections from concurrent writers.
	// No-op when the file content is unchanged (avoids empty commits).
	Put(ctx context.Context, name string, content []byte) error

	// Delete removes a file. Idempotent — returns nil when the file
	// is already absent.
	Delete(ctx context.Context, name string) error

	// Close releases any resources held by the Store. For the git-
	// backed implementation this is a no-op (every operation forks
	// a fresh git subprocess), but callers should still defer
	// Close to allow alternative backends (in-memory, lock-file
	// based) to clean up.
	Close() error
}

// Open returns a Store backed by `branch` on the brain's repo.
//
// brainRoot: the directory containing the brain's .git/ and
// .brain/config.md.
// branch:    the plain branch name to use (e.g., "keys"). Created
//            on the remote as an orphan branch the first time a Put
//            actually needs to push.
//
// Errors when the brain's origin URL can't be read (no remote
// configured), or when the workdir can't be created. Does not
// require the branch to exist on the remote — that's lazy on
// first push.
func Open(brainRoot, branch string) (Store, error) {
	if branch == "" {
		return nil, errors.New("filestore: branch name required")
	}
	plainURL, err := readPlainOriginURL(brainRoot)
	if err != nil {
		return nil, fmt.Errorf("filestore: %w", err)
	}
	s := &gitStore{
		brainRoot: brainRoot,
		branch:    branch,
		plainURL:  plainURL,
		workdir:   filepath.Join(brainRoot, ".git", "nous-filestore", branch),
	}
	if err := s.ensureWorkdir(); err != nil {
		return nil, fmt.Errorf("filestore: setup workdir: %w", err)
	}
	return s, nil
}

// readPlainOriginURL returns the brain's remote.origin.url with any
// `gcrypt::` prefix stripped — i.e., the URL gcrypt's remote helper
// is wrapping. For a non-encrypted brain (rare; legacy), origin
// itself is the plain URL and the prefix-strip is a no-op.
func readPlainOriginURL(brainRoot string) (string, error) {
	out, err := exec.Command("git", "-C", brainRoot, "config", "--get", "remote.origin.url").Output()
	if err != nil {
		return "", fmt.Errorf("read remote.origin.url: %w", err)
	}
	url := strings.TrimSpace(string(out))
	if url == "" {
		return "", errors.New("remote.origin.url is unset; configure a remote before opening a filestore")
	}
	return strings.TrimPrefix(url, "gcrypt::"), nil
}

// gitStore implements Store via a local shallow clone of a plain
// branch on the brain's repo. Single-instance: callers serialize via
// the embedded mutex, and the underlying git subprocesses each
// hold the workdir for the duration of their operation.
type gitStore struct {
	brainRoot string
	branch    string
	plainURL  string
	workdir   string

	mu sync.Mutex
}

// ensureWorkdir prepares the local clone of the branch. Three cases:
//
//  1. Workdir already exists with a .git inside → reuse it (refresh
//     before each operation).
//  2. Branch exists on remote, workdir doesn't → shallow-clone it.
//  3. Branch doesn't exist on remote → init a fresh empty repo with
//     the branch as HEAD; first Put will commit + push to create
//     the orphan branch remotely.
func (s *gitStore) ensureWorkdir() error {
	if _, err := os.Stat(filepath.Join(s.workdir, ".git")); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.workdir), 0o755); err != nil {
		return err
	}
	exists, err := s.remoteBranchExists()
	if err != nil {
		return fmt.Errorf("check remote branch: %w", err)
	}
	if exists {
		// Shallow clone of just the target branch — small bandwidth
		// + disk footprint, no irrelevant history.
		cmd := exec.Command("git", "clone",
			"--branch", s.branch,
			"--single-branch",
			"--depth=1",
			s.plainURL, s.workdir)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("clone branch %s: %w\n%s", s.branch, err, out)
		}
	} else {
		// Branch doesn't exist remotely yet — init an empty repo
		// locally with the branch as HEAD. First Put will populate
		// + push, creating the orphan branch on the remote.
		if err := os.MkdirAll(s.workdir, 0o755); err != nil {
			return err
		}
		if err := s.run("init", "-q", "-b", s.branch); err != nil {
			return err
		}
		if err := s.run("remote", "add", "origin", s.plainURL); err != nil {
			return err
		}
	}
	// Inherit committer identity from the brain's git config so
	// commits don't fail with "user.email is unset." Best-effort:
	// if the brain hasn't been configured either, fall back to a
	// nous-flavored default — committer identity doesn't carry
	// trust weight here (we don't sign filestore commits).
	if email, err := exec.Command("git", "-C", s.brainRoot, "config", "user.email").Output(); err == nil {
		if e := strings.TrimSpace(string(email)); e != "" {
			_ = s.run("config", "user.email", e)
		}
	}
	if name, err := exec.Command("git", "-C", s.brainRoot, "config", "user.name").Output(); err == nil {
		if n := strings.TrimSpace(string(name)); n != "" {
			_ = s.run("config", "user.name", n)
		}
	}
	return nil
}

// remoteBranchExists checks whether the branch is published on the
// plain remote. Cheap (one ls-remote round trip); doesn't fetch
// objects.
func (s *gitStore) remoteBranchExists() (bool, error) {
	out, err := exec.Command("git", "ls-remote", "--heads", s.plainURL, s.branch).Output()
	if err != nil {
		return false, err
	}
	return strings.Contains(string(out), "refs/heads/"+s.branch), nil
}

// refresh fetches the latest from origin/<branch> and hard-resets
// the local workdir to match. Pre-flight for every operation so
// callers see a recent view. No-op when the branch doesn't exist
// on remote yet (orphan-branch first run): fetch will fail, but
// reset to local HEAD is the current state.
func (s *gitStore) refresh() error {
	out, err := exec.Command("git", "-C", s.workdir, "fetch", "origin", s.branch).CombinedOutput()
	if err != nil {
		// Common case on first-Put / orphan branch: the branch
		// doesn't exist yet remotely. Don't error — just leave the
		// local state as-is (empty repo). The push will create the
		// branch on the remote.
		msg := string(out)
		if strings.Contains(msg, "couldn't find remote ref") ||
			strings.Contains(msg, "does not appear to be a git repository") {
			return nil
		}
		return fmt.Errorf("fetch: %w\n%s", err, msg)
	}
	// Reset only if the remote-tracking ref now exists (a successful
	// fetch creates it).
	if err := s.run("rev-parse", "--verify", "origin/"+s.branch); err == nil {
		return s.run("reset", "--hard", "origin/"+s.branch)
	}
	return nil
}

// List walks the workdir, filtering out dotfiles (which would
// include .git) and subdirectories. Filestore is a flat key-value
// store today; nested directories aren't part of the contract.
func (s *gitStore) List(ctx context.Context) (map[string][]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := s.refresh(); err != nil {
		return nil, fmt.Errorf("filestore List: %w", err)
	}
	entries, err := os.ReadDir(s.workdir)
	if err != nil {
		return nil, fmt.Errorf("filestore List: read workdir: %w", err)
	}
	out := map[string][]byte{}
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		content, err := os.ReadFile(filepath.Join(s.workdir, e.Name()))
		if err != nil {
			// Skip unreadable files rather than failing the whole
			// List — partial visibility is better than no visibility.
			continue
		}
		out[e.Name()] = content
	}
	return out, nil
}

// Put writes content under name and pushes. Retries up to 3 times on
// non-fast-forward (someone else pushed first); each retry re-fetches
// before re-applying.
func (s *gitStore) Put(ctx context.Context, name string, content []byte) error {
	if name == "" {
		return errors.New("filestore Put: name required")
	}
	if strings.ContainsAny(name, "/\\") {
		return fmt.Errorf("filestore Put: name %q must not contain path separators (flat namespace)", name)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.withRetry(ctx, 3, func() error {
		if err := s.refresh(); err != nil {
			return err
		}
		path := filepath.Join(s.workdir, name)
		if err := os.WriteFile(path, content, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
		if err := s.run("add", name); err != nil {
			return err
		}
		// `git diff --cached --quiet` exits 0 when there's nothing
		// staged — i.e., the new content is identical to what's
		// already committed. Skip the commit + push to avoid empty
		// commits and unnecessary remote churn.
		if err := s.run("diff", "--cached", "--quiet"); err == nil {
			return nil
		}
		if err := s.run("commit", "-q", "-m", "put "+name); err != nil {
			return err
		}
		return s.run("push", "origin", s.branch+":refs/heads/"+s.branch)
	})
}

// Delete removes name from the store and pushes. Idempotent.
func (s *gitStore) Delete(ctx context.Context, name string) error {
	if name == "" {
		return errors.New("filestore Delete: name required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.withRetry(ctx, 3, func() error {
		if err := s.refresh(); err != nil {
			return err
		}
		path := filepath.Join(s.workdir, name)
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err := s.run("rm", "-q", name); err != nil {
			return err
		}
		if err := s.run("commit", "-q", "-m", "delete "+name); err != nil {
			return err
		}
		return s.run("push", "origin", s.branch+":refs/heads/"+s.branch)
	})
}

func (s *gitStore) Close() error {
	// No persistent resources to release — every op forks a fresh
	// git subprocess. Reserved as a Close()-able API in case a
	// future backend (in-memory, lock-file based) needs cleanup.
	return nil
}

// withRetry runs fn up to n times. Discriminates between "transient
// push rejection" (retry) and "actual failure" (abort) by inspecting
// the error: we treat any error as retryable since the next attempt
// re-fetches + re-applies, which is also the recovery for a non-
// fast-forward. Cheap (an extra fetch + re-stage), so the simplicity
// of "just try again" is worth the slight overcorrection.
func (s *gitStore) withRetry(ctx context.Context, n int, fn func() error) error {
	var lastErr error
	for i := 0; i < n; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := fn(); err == nil {
			return nil
		} else {
			lastErr = err
		}
	}
	return lastErr
}

// run executes a git subcommand against the local workdir. Wraps
// the error with the command for easier debugging.
func (s *gitStore) run(args ...string) error {
	full := append([]string{"-C", s.workdir}, args...)
	cmd := exec.Command("git", full...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, out)
	}
	return nil
}
