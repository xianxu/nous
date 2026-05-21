package brain

import (
	"fmt"
	"io/fs"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/xianxu/nous/lib/gh"
)

// Status aggregates the state a human wants to see when they drill into
// a brain from `nous brain` TUI: manifest, recipients (joined manifest
// × gcrypt-participants), upstream sync state, conflict files.
//
// Sub-queries are best-effort. A failure in one section (e.g. no
// upstream → no ahead/behind) doesn't fail the whole Status call;
// the corresponding fields stay zero-valued and the caller renders
// "—" or "(none)". Hard failures (manifest unreadable) surface as
// the function's error return.
type Status struct {
	Manifest Manifest

	// Recipients is the union of manifest.Recipients and gcrypt
	// participants, in stable order (manifest order first, then any
	// gcrypt-only fingerprints appended). Order matches what `nous
	// brain recipient list` prints.
	Recipients []RecipientInfo

	// Mismatch is true when manifest and gcrypt-participants don't
	// agree (a fingerprint in one but not the other). The drill-in
	// renders a warning banner when set. False for single-recipient
	// brains where gcrypt isn't always populated.
	Mismatch bool

	// Sync state. HasUpstream=false means no `@{u}` is configured for
	// HEAD; Ahead/Behind are then meaningless and the caller should
	// render "no upstream tracking".
	LastCommit  CommitInfo
	HasUpstream bool
	Ahead       int
	Behind      int

	// OriginURL is the gcrypt remote URL configured at remote.origin.url,
	// e.g. `gcrypt::ssh://git@github.com/owner/brain.git`. Empty when
	// no origin remote is configured (fresh `git init` before any push).
	// Surfaced so the brain TUI can show peers the exact clone command.
	OriginURL string

	// ConflictFiles is relative paths under brain root that match the
	// brainsync conflict-file convention (`*.conflict-<peer>-<utc>.<ext>`).
	// Empty slice = no conflicts.
	ConflictFiles []string

	// PendingInvitations lists GitHub repo invitations the operator
	// (or another admin) has sent that the invitee hasn't accepted
	// yet. Visible only when the operator has admin/push access on
	// the repo (GitHub gates the endpoint); empty otherwise.
	//
	// Surfaces the limbo state between `nous brain` "invite
	// collaborator" succeeding and the invitee actually accepting +
	// auto-admit running on this peer. Without this, the operator
	// has no in-TUI signal that "I invited X" actually landed.
	PendingInvitations []gh.RepoInvitation
}

// RecipientInfo describes one recipient slot for the drill-in.
type RecipientInfo struct {
	Fingerprint string
	Annotation  string // from Annotator() — "(self) ...", "(peer) ...", "(unknown ...)"
	InManifest  bool
	InGcrypt    bool
}

// CommitInfo describes HEAD's last commit. Empty (Hash=="") when the
// brain has no commits yet — fresh `nous brain new` before any work.
type CommitInfo struct {
	Hash      string // full 40-char hash
	ShortHash string // 7-char hash
	When      time.Time
	RelTime   string // human-readable ("2h ago")
	Subject   string // first line of commit message
}

// LoadStatus reads the brain at brainRoot and returns a Status snapshot.
// brainRoot must be the directory that contains .brain/ (not .brain
// itself). Reading the manifest is the only step that can hard-fail —
// everything else degrades gracefully.
func LoadStatus(brainRoot string) (Status, error) {
	abs, err := filepath.Abs(brainRoot)
	if err != nil {
		return Status{}, err
	}
	m, err := Read(abs)
	if err != nil {
		return Status{}, fmt.Errorf("read manifest: %w", err)
	}
	s := Status{Manifest: m}

	// Recipients: union manifest × gcrypt-participants. Annotator
	// failures (e.g. gpg outage) → annotations stay empty rather than
	// blocking the whole status read.
	gcryptList, _ := ReadGcryptParticipants(abs)
	annotate, _ := Annotator()
	s.Recipients, s.Mismatch = mergeRecipients(m.Recipients, gcryptList, annotate)

	// Sync state.
	s.LastCommit = readLastCommit(abs)
	s.HasUpstream, s.Ahead, s.Behind = readUpstreamPosition(abs)
	s.OriginURL = readOriginURL(abs)

	// Conflict files.
	s.ConflictFiles = findConflictFiles(abs)

	// Pending GitHub invitations the operator sent on this repo.
	// Best-effort: only attempt when origin is a github URL we can
	// parse; swallow gh errors (no auth / not an admin / outage) so
	// status load still succeeds with PendingInvitations==nil.
	if s.OriginURL != "" {
		if owner, repo, err := GitHubOwnerRepo(s.OriginURL); err == nil {
			if invs, err := gh.RepoPendingInvitations(owner, repo); err == nil {
				s.PendingInvitations = invs
			}
		}
	}

	return s, nil
}

// readOriginURL returns the configured `remote.origin.url` (e.g.
// `gcrypt::ssh://git@github.com/owner/brain.git`), or empty string
// when no origin remote is configured. Best-effort; errors swallowed
// — Status callers degrade to "no clone URL available" rather than
// failing the whole status read.
func readOriginURL(brainRoot string) string {
	out, err := exec.Command("git", "-C", brainRoot, "config", "--get", "remote.origin.url").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// conflictFileRE matches the brainsync convention:
//
//	<stem>.conflict-<peer>-<utc-iso8601-compact>.<ext>   (with extension)
//	<stem>.conflict-<peer>-<utc-iso8601-compact>         (extensionless)
//
// Anchored to ensure we don't catch arbitrary user content like
// `notes-about-the-conflict-resolution.md`.
var conflictFileRE = regexp.MustCompile(`\.conflict-[^/]+-[0-9]{8}T[0-9]{6}Z(\.[^/.]+)?$`)

func findConflictFiles(root string) []string {
	var out []string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		// Skip .git, .brain — substrate, not content.
		name := d.Name()
		if d.IsDir() {
			if name == ".git" || name == ".brain" {
				return fs.SkipDir
			}
			return nil
		}
		if conflictFileRE.MatchString(path) {
			rel, err := filepath.Rel(root, path)
			if err == nil {
				out = append(out, rel)
			}
		}
		return nil
	})
	return out
}

func readLastCommit(brainRoot string) CommitInfo {
	cmd := exec.Command("git", "-C", brainRoot, "log", "-1",
		"--format=%H%n%h%n%at%n%s")
	raw, err := cmd.Output()
	if err != nil {
		return CommitInfo{}
	}
	lines := strings.SplitN(strings.TrimRight(string(raw), "\n"), "\n", 4)
	if len(lines) < 4 {
		return CommitInfo{}
	}
	ci := CommitInfo{
		Hash:      lines[0],
		ShortHash: lines[1],
		Subject:   lines[3],
	}
	if ts, err := time.Parse("2006-01-02 15:04:05", lines[2]); err == nil {
		ci.When = ts
	} else {
		// %at = unix seconds; fall through to parsing as integer.
		var secs int64
		if _, err := fmt.Sscanf(lines[2], "%d", &secs); err == nil {
			ci.When = time.Unix(secs, 0)
		}
	}
	ci.RelTime = HumanizeDuration(time.Since(ci.When))
	return ci
}

func readUpstreamPosition(brainRoot string) (hasUpstream bool, ahead, behind int) {
	// Probe for upstream first; absence is the common case for fresh
	// brains and shouldn't surface as an error.
	probe := exec.Command("git", "-C", brainRoot, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}")
	if err := probe.Run(); err != nil {
		return false, 0, 0
	}
	cmd := exec.Command("git", "-C", brainRoot, "rev-list", "--left-right", "--count", "HEAD...@{u}")
	out, err := cmd.Output()
	if err != nil {
		return true, 0, 0
	}
	var a, b int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(out)), "%d\t%d", &a, &b); err != nil {
		return true, 0, 0
	}
	return true, a, b
}

func mergeRecipients(manifestFps, gcryptFps []string, annotate func(string) string) ([]RecipientInfo, bool) {
	in := func(set []string, fp string) bool {
		for _, x := range set {
			if strings.EqualFold(x, fp) {
				return true
			}
		}
		return false
	}
	annot := func(fp string) string {
		if annotate == nil {
			return ""
		}
		return annotate(fp)
	}
	var out []RecipientInfo
	seen := map[string]bool{}
	for _, fp := range manifestFps {
		out = append(out, RecipientInfo{
			Fingerprint: fp,
			Annotation:  annot(fp),
			InManifest:  true,
			InGcrypt:    in(gcryptFps, fp),
		})
		seen[strings.ToUpper(fp)] = true
	}
	for _, fp := range gcryptFps {
		if seen[strings.ToUpper(fp)] {
			continue
		}
		out = append(out, RecipientInfo{
			Fingerprint: fp,
			Annotation:  annot(fp),
			InManifest:  false,
			InGcrypt:    true,
		})
	}
	// Don't warn when gcrypt-participants is empty: that's either a
	// single-recipient brain (substrate doesn't need the config) OR
	// a freshly-cloned shared brain whose local config hasn't been
	// populated from the manifest yet. In both cases the discrepancy
	// is harmless and self-healing — the next push (via the #24
	// push wrapper) will sync from manifest, and a freshly-cloned
	// brain post-#nous-brain-clone-#853c416 syncs at clone time.
	// Warning the operator about it just because they haven't
	// pushed yet is misleading.
	//
	// Real drift = both sides non-empty AND they disagree. That's
	// the case worth surfacing (manifest hand-edited; remote.origin
	// .gcrypt-participants hand-edited; corruption).
	if len(gcryptFps) == 0 {
		return out, false
	}
	mismatch := false
	for _, r := range out {
		if r.InManifest != r.InGcrypt {
			// Single-recipient brains commonly have empty gcrypt config
			// (handled above); for non-empty gcrypt, any one-sided
			// entry is a real divergence.
			mismatch = true
			break
		}
	}
	return out, mismatch
}

// humanizeDuration renders a duration as a coarse human string:
// HumanizeDuration formats a time.Duration as a coarse "X ago" string:
// "12s ago", "5m ago", "3h ago", "2d ago", "3w ago". Coarseness
// matches what operators want at a glance — exact timestamps are
// in CommitInfo.When for callers that need them. Exported so the
// brain TUI (and any future consumer that wants the same look-and-
// feel) can format other timestamps consistently.
func HumanizeDuration(d time.Duration) string {
	if d < 0 {
		d = -d
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return fmt.Sprintf("%dw ago", int(d.Hours()/(24*7)))
	}
}
