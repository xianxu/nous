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

// CurrentBranch returns the name of the branch HEAD points at, or "" when
// HEAD is detached — a detached HEAD simply isn't on a branch, which is not
// an error for our callers. A genuine git failure (not a repo, etc.) is
// returned as an error.
//
// Implemented with `git symbolic-ref --quiet --short HEAD`, run directly
// rather than via RunGit: on a branch it prints the short name and exits 0;
// on a detached HEAD it prints nothing and exits 1 (`--quiet` suppresses the
// stderr message but NOT the exit status). RunGit folds any non-zero exit
// into an error, which would mis-report a detached HEAD — so we handle the
// exit code here and map the silent exit-1 to ("", nil).
func CurrentBranch(repo string) (string, error) {
	cmd := exec.Command("git", "-C", repo, "symbolic-ref", "--quiet", "--short", "HEAD")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return strings.TrimSpace(stdout.String()), nil
	}
	// Detached HEAD: exit 1 with empty stderr (the --quiet contract). Not an
	// error — HEAD just isn't a branch.
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 && strings.TrimSpace(stderr.String()) == "" {
		return "", nil
	}
	return "", fmt.Errorf("symbolic-ref HEAD: %w: %s", err, stderr.String())
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

// ResetToRemoteMain fetches origin and hard-resets the working tree to
// origin/main — discarding any local commits + tracked-file changes. Used by
// the membership push-retry (nous#41 #6) to drop a rejected commit and
// re-apply the membership change on top of the concurrent operator's state.
//
// Untracked files survive (reset --hard leaves them in place). The membership
// ops that use this assume the brain has no unrelated uncommitted *tracked*
// work — AddCommitPush's `git add -A` would otherwise have bundled it into the
// rejected commit, and the reset would roll it back to origin/main. That holds
// for the deliberate recipient-add/remove/leave gestures and the daemon's
// auto-admit (which runs against an otherwise-synced brain).
// Assumes an established brain with an existing origin/main — membership ops
// (remove/leave/auto-admit) only run after provisioning's initial push. On a
// never-pushed brain `reset --hard origin/main` fails with "unknown revision";
// that surfaces as an error and aborts the retry cleanly rather than looping.
func ResetToRemoteMain(repo string) error {
	if err := Fetch(repo); err != nil {
		return fmt.Errorf("fetch: %w", err)
	}
	if _, err := RunGit(repo, "reset", "--hard", "origin/main"); err != nil {
		return fmt.Errorf("reset --hard origin/main: %w", err)
	}
	return nil
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

// CleanWorkingTree returns true iff there are no uncommitted changes,
// including untracked files. Strict definition — used by callers
// who need a "nothing on disk that isn't already committed" check.
func CleanWorkingTree(repo string) (bool, error) {
	out, err := RunGit(repo, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(out)) == "", nil
}

// SafeToFastForward returns true iff a `git pull --ff-only` would
// succeed on the next ref motion — i.e., no modified-tracked or
// staged changes that a fast-forward might collide with. Untracked
// files are deliberately tolerated: they're not in the index, and
// `git pull --ff-only` won't touch them.
//
// Distinct from CleanWorkingTree because brain workflows often
// carry long-lived untracked files (operator drafts they haven't
// `git add`'d yet); blocking pulls on those produces a silent
// "remote never seen as ahead" failure mode — see #30 follow-up.
//
// Uses `git diff-index --quiet HEAD --` which exits non-zero iff
// the index or working tree differs from HEAD on tracked paths.
// `--quiet` suppresses output; we only care about the exit code.
func SafeToFastForward(repo string) (bool, error) {
	cmd := exec.Command("git", "-C", repo, "diff-index", "--quiet", "HEAD", "--")
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	// Exit code 1 = differences exist (the only non-error result we
	// expect to see from diff-index --quiet). Any other error
	// (couldn't run git, HEAD doesn't exist, etc.) is a real failure.
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, err
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
