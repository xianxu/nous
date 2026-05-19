package brainsync

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/xianxu/nous/lib/brain"
)

// RunGit runs `git -C repo args...` and returns stdout. Stderr is folded
// into the returned error on failure.
func RunGit(repo string, args ...string) ([]byte, error) {
	c := exec.Command("git", append([]string{"-C", repo}, args...)...)
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr
	err := c.Run()
	if err != nil {
		return stdout.Bytes(), fmt.Errorf("git %v: %w: %s", args, err, stderr.String())
	}
	return stdout.Bytes(), nil
}

// Status returns the list of modified-or-untracked paths (relative to
// repo). Empty if working tree is clean.
func Status(repo string) ([]string, error) {
	out, err := RunGit(repo, "status", "--porcelain")
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, line := range strings.Split(string(out), "\n") {
		if len(line) < 4 {
			continue
		}
		// Format: "XY filename" — XY is two-char status, then space, then path.
		paths = append(paths, line[3:])
	}
	return paths, nil
}

// Fetch runs `git fetch origin`.
func Fetch(repo string) error {
	_, err := RunGit(repo, "fetch", "origin")
	return err
}

// ErrPushRejected is returned by AddCommitPush / Push when git push fails
// because the remote rejected our push (typically: someone else pushed
// first). Caller should resolve and retry.
var ErrPushRejected = errors.New("push rejected by remote")

// AddCommitPush stages everything, commits with msg, pushes to origin.
// Returns ErrPushRejected if the push was rejected; nil on success;
// other error otherwise.
//
// If the working tree is clean (nothing to commit), returns nil without
// error — this is the "edited and reverted within window" case.
//
// Before the push, syncs `remote.origin.gcrypt-participants` from the
// brain's manifest (the canonical source of recipients). gcrypt's
// remote helper reads that config at push time to decide who to
// encrypt to; treating it as a derived-cache-of-the-manifest rather
// than a separately-mutated store eliminates the drift class that
// produces silent ciphertext-to-wrong-recipients bugs (see nous#24).
func AddCommitPush(repo, msg string) error {
	if _, err := RunGit(repo, "add", "-A"); err != nil {
		return err
	}
	// Skip empty commits.
	if _, err := RunGit(repo, "diff", "--cached", "--quiet"); err == nil {
		return nil // nothing staged
	}
	if _, err := RunGit(repo, "commit", "-m", msg); err != nil {
		return err
	}
	// Derive gcrypt-participants from the (now-committed) manifest
	// before push. Manifest is canonical; this is the single sync
	// point that keeps the two storage locations consistent.
	if err := brain.SyncGcryptParticipantsFromManifest(repo); err != nil {
		return fmt.Errorf("sync gcrypt-participants from manifest: %w", err)
	}
	if _, err := RunGit(repo, "push", "origin", "main"); err != nil {
		if strings.Contains(err.Error(), "rejected") || strings.Contains(err.Error(), "non-fast-forward") {
			return ErrPushRejected
		}
		return err
	}
	return nil
}

// Push runs `git push origin`. Returns ErrPushRejected on non-fast-forward.
//
// Same gcrypt-participants sync as AddCommitPush — manifest is the
// canonical source; push is the only place we need the derived config
// to be current.
func Push(repo string) error {
	if err := brain.SyncGcryptParticipantsFromManifest(repo); err != nil {
		return fmt.Errorf("sync gcrypt-participants from manifest: %w", err)
	}
	if _, err := RunGit(repo, "push", "origin", "main"); err != nil {
		if strings.Contains(err.Error(), "rejected") || strings.Contains(err.Error(), "non-fast-forward") {
			return ErrPushRejected
		}
		return err
	}
	return nil
}

// PullFF runs `git pull --ff-only origin main`.
func PullFF(repo string) error {
	_, err := RunGit(repo, "pull", "--ff-only", "origin", "main")
	return err
}

// HasUnpushedCommits returns true if HEAD is ahead of origin/main.
func HasUnpushedCommits(repo string) (bool, error) {
	out, err := RunGit(repo, "rev-list", "--count", "origin/main..HEAD")
	if err != nil {
		// First push (no upstream yet) — treat as having commits.
		if strings.Contains(err.Error(), "unknown revision") {
			return true, nil
		}
		return false, err
	}
	return strings.TrimSpace(string(out)) != "0", nil
}

// CleanWorkingTree returns true iff there are no uncommitted changes.
func CleanWorkingTree(repo string) (bool, error) {
	out, err := RunGit(repo, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(out)) == "", nil
}

// IsStrictlyBehind returns true iff origin/main is ahead of HEAD AND HEAD
// has nothing origin/main lacks (i.e., a fast-forward is possible).
func IsStrictlyBehind(repo string) (bool, error) {
	ahead, err := RunGit(repo, "rev-list", "--count", "origin/main..HEAD")
	if err != nil {
		return false, err
	}
	behind, err := RunGit(repo, "rev-list", "--count", "HEAD..origin/main")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(ahead)) == "0" && strings.TrimSpace(string(behind)) != "0", nil
}
