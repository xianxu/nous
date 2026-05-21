package brain

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestMergeRecipients_ManifestOrderThenGcryptExtras(t *testing.T) {
	annotate := func(fp string) string { return "(noop)" }
	out, mismatch := mergeRecipients(
		[]string{"AAA", "BBB"},
		[]string{"BBB", "CCC"},
		annotate,
	)
	if len(out) != 3 {
		t.Fatalf("want 3 recipients, got %d: %+v", len(out), out)
	}
	wantFps := []string{"AAA", "BBB", "CCC"}
	for i, ri := range out {
		if ri.Fingerprint != wantFps[i] {
			t.Errorf("idx %d: fp=%s want=%s", i, ri.Fingerprint, wantFps[i])
		}
	}
	if !out[0].InManifest || out[0].InGcrypt {
		t.Errorf("AAA: want manifest-only, got %+v", out[0])
	}
	if !out[1].InManifest || !out[1].InGcrypt {
		t.Errorf("BBB: want both, got %+v", out[1])
	}
	if out[2].InManifest || !out[2].InGcrypt {
		t.Errorf("CCC: want gcrypt-only, got %+v", out[2])
	}
	if !mismatch {
		t.Errorf("mismatch=false; AAA and CCC asymmetric in 2+-recipient set should flag")
	}
}

func TestMergeRecipients_SingleRecipientNoGcryptIsNotMismatch(t *testing.T) {
	// Common case: private brain with one recipient and no gcrypt
	// participants configured (substrate doesn't need it). Should NOT
	// flag mismatch — that'd be noise.
	out, mismatch := mergeRecipients(
		[]string{"DEAD"},
		nil,
		nil,
	)
	if len(out) != 1 || mismatch {
		t.Fatalf("want 1 recipient + no mismatch, got len=%d mismatch=%v", len(out), mismatch)
	}
	if !out[0].InManifest || out[0].InGcrypt {
		t.Errorf("DEAD: want manifest-only, got %+v", out[0])
	}
}

func TestMergeRecipients_FreshClonedMultiRecipientIsNotMismatch(t *testing.T) {
	// The ying-on-brain1 case from operator manual test
	// 2026-05-20: after gcrypt clone of a shared brain, the
	// local remote.origin.gcrypt-participants is empty even
	// though the manifest has multiple recipients. The push
	// wrapper (or nous brain clone's post-clone sync) populates
	// it; until then both sides legitimately disagree but the
	// state is harmless and self-healing. Don't warn.
	out, mismatch := mergeRecipients(
		[]string{"AAA", "BBB"},
		nil, // gcrypt-participants empty (fresh clone)
		nil,
	)
	if len(out) != 2 {
		t.Fatalf("want 2 recipients, got %d: %+v", len(out), out)
	}
	if mismatch {
		t.Errorf("empty gcrypt-participants is not real drift; got mismatch=true")
	}
}

func TestMergeRecipients_CaseInsensitiveDedup(t *testing.T) {
	out, _ := mergeRecipients(
		[]string{"deadbeef"},
		[]string{"DEADBEEF"},
		nil,
	)
	if len(out) != 1 {
		t.Fatalf("want dedup across case, got %d: %+v", len(out), out)
	}
	if !out[0].InManifest || !out[0].InGcrypt {
		t.Errorf("want both flags set, got %+v", out[0])
	}
}

func TestHumanizeDuration_BoundaryUnits(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "30s ago"},
		{5 * time.Minute, "5m ago"},
		{3 * time.Hour, "3h ago"},
		{2 * 24 * time.Hour, "2d ago"},
		{3 * 7 * 24 * time.Hour, "3w ago"},
		{-5 * time.Minute, "5m ago"}, // negatives absolute
	}
	for _, c := range cases {
		if got := HumanizeDuration(c.d); got != c.want {
			t.Errorf("HumanizeDuration(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}

func TestConflictFileRE_MatchesBrainsyncConvention(t *testing.T) {
	matches := []string{
		"data/travel/paris.conflict-xianxu-mbp-20260507T221604Z.md",
		"notes.conflict-peerB-20260507T221604Z.md",
		"README.conflict-p-20260507T221604Z", // extensionless
	}
	for _, p := range matches {
		if !conflictFileRE.MatchString(p) {
			t.Errorf("want match: %s", p)
		}
	}
	nonMatches := []string{
		"notes-about-the-conflict-resolution.md",
		"data/conflict-log.md",
		"paris.conflict-peer-bad-timestamp.md",
		"paris.conflict-peer.md", // missing timestamp
	}
	for _, p := range nonMatches {
		if conflictFileRE.MatchString(p) {
			t.Errorf("want NO match: %s", p)
		}
	}
}

func TestFindConflictFiles_WalksAndSkipsSubstrate(t *testing.T) {
	root := t.TempDir()
	must := func(p string) {
		full := filepath.Join(root, p)
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	must("data/notes.conflict-peer-20260507T221604Z.md") // hit
	must("notes.md")                                     // miss
	// substrate dirs that must be skipped — put a fake conflict file in
	// each and confirm it doesn't surface.
	must(".git/objects/conflict-peer-20260507T221604Z.md")
	must(".brain/cache/x.conflict-peer-20260507T221604Z.md")

	got := findConflictFiles(root)
	want := []string{"data/notes.conflict-peer-20260507T221604Z.md"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("findConflictFiles:\n got=%v\nwant=%v", got, want)
	}
}

// buildSyntheticBrain initializes a brain with a manifest, one commit,
// and (optionally) an upstream. Returns the brain root.
func buildSyntheticBrain(t *testing.T, withUpstream bool) string {
	t.Helper()
	root := t.TempDir()

	// .brain/config.md — minimal valid manifest.
	if err := os.MkdirAll(filepath.Join(root, ".brain"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := "---\nname: test\nrecipients: [\"AAA\"]\nsync_substrate: none\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(root, ".brain", "config.md"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	// git init + commit. Use -c to avoid depending on a global user.email.
	runGit := func(args ...string) {
		t.Helper()
		full := append([]string{"-C", root,
			"-c", "user.email=test@example.com",
			"-c", "user.name=Test",
			"-c", "init.defaultBranch=main",
			"-c", "commit.gpgsign=false",
		}, args...)
		out, err := exec.Command("git", full...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("init")
	if err := os.WriteFile(filepath.Join(root, "hello.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "-A")
	runGit("commit", "-m", "initial commit")

	if withUpstream {
		// Create a sibling bare repo and push to it so HEAD has @{u}.
		upstream := t.TempDir()
		runGit2 := func(dir string, args ...string) {
			t.Helper()
			out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
			if err != nil {
				t.Fatalf("git -C %s %v: %v\n%s", dir, args, err, out)
			}
		}
		runGit2(upstream, "init", "--bare", "-b", "main")
		runGit("remote", "add", "origin", upstream)
		runGit("push", "-u", "origin", "main")
	}
	return root
}

func TestReadLastCommit_PopulatesAllFields(t *testing.T) {
	root := buildSyntheticBrain(t, false)
	ci := readLastCommit(root)
	if ci.Hash == "" || len(ci.Hash) != 40 {
		t.Errorf("Hash: %q", ci.Hash)
	}
	if ci.ShortHash == "" || len(ci.ShortHash) < 7 {
		t.Errorf("ShortHash: %q", ci.ShortHash)
	}
	if ci.Subject != "initial commit" {
		t.Errorf("Subject: %q", ci.Subject)
	}
	if ci.When.IsZero() {
		t.Errorf("When zero")
	}
	if !strings.HasSuffix(ci.RelTime, "ago") {
		t.Errorf("RelTime: %q", ci.RelTime)
	}
}

func TestReadUpstreamPosition_NoUpstream(t *testing.T) {
	root := buildSyntheticBrain(t, false)
	has, a, b := readUpstreamPosition(root)
	if has || a != 0 || b != 0 {
		t.Errorf("no-upstream: has=%v a=%d b=%d", has, a, b)
	}
}

func TestReadUpstreamPosition_WithUpstreamFlush(t *testing.T) {
	root := buildSyntheticBrain(t, true)
	has, a, b := readUpstreamPosition(root)
	if !has {
		t.Errorf("want has-upstream")
	}
	if a != 0 || b != 0 {
		t.Errorf("just-pushed: want 0/0, got ahead=%d behind=%d", a, b)
	}
}

func TestLoadStatus_EndToEnd(t *testing.T) {
	root := buildSyntheticBrain(t, false)

	// Plant a conflict file under data/ to verify wired up.
	_ = os.MkdirAll(filepath.Join(root, "data"), 0o755)
	if err := os.WriteFile(filepath.Join(root, "data", "x.conflict-peer-20260507T221604Z.md"), []byte("c"), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := LoadStatus(root)
	if err != nil {
		t.Fatalf("LoadStatus: %v", err)
	}
	if s.Manifest.Name != "test" {
		t.Errorf("manifest name: %q", s.Manifest.Name)
	}
	if len(s.Recipients) != 1 || s.Recipients[0].Fingerprint != "AAA" {
		t.Errorf("recipients: %+v", s.Recipients)
	}
	if !s.Recipients[0].InManifest {
		t.Errorf("recipient should be marked InManifest")
	}
	if s.HasUpstream {
		t.Errorf("synthetic brain has no upstream")
	}
	if s.LastCommit.Hash == "" {
		t.Errorf("LastCommit not populated")
	}
	if len(s.ConflictFiles) != 1 {
		t.Errorf("conflict files: %v", s.ConflictFiles)
	}
}
