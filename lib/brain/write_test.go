package brain

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteManifest_RoundTripsThroughRead(t *testing.T) {
	root := t.TempDir()
	in := Manifest{
		Name:          "family",
		Recipients:    []string{"FP_BOB", "FP_ALICE"},
		SyncSubstrate: "syncthing",
	}
	if err := WriteManifest(root, in); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}
	out, err := Read(root)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if out.Name != in.Name {
		t.Errorf("Name = %q, want %q", out.Name, in.Name)
	}
	if out.SyncSubstrate != in.SyncSubstrate {
		t.Errorf("SyncSubstrate = %q, want %q", out.SyncSubstrate, in.SyncSubstrate)
	}
	// Recipients are sorted on write.
	want := []string{"FP_ALICE", "FP_BOB"}
	if len(out.Recipients) != len(want) {
		t.Fatalf("Recipients len = %d, want %d", len(out.Recipients), len(want))
	}
	for i := range want {
		if out.Recipients[i] != want[i] {
			t.Errorf("Recipients[%d] = %q, want %q", i, out.Recipients[i], want[i])
		}
	}
	if !out.Shared() {
		t.Errorf("Shared() = false; expected true (multi-recipient)")
	}
	if out.LegacyMode != "" {
		t.Errorf("LegacyMode = %q after WriteManifest; want empty (we don't write the field)", out.LegacyMode)
	}
}

func TestManifest_RecipientLoginsRoundTrip(t *testing.T) {
	root := t.TempDir()
	in := Manifest{
		Name:       "family",
		Recipients: []string{"FP_ALICE", "FP_BOB"},
		RecipientLogins: map[string]string{
			"ying": "1a2bf0", // lowercase input must round-trip uppercased
			"xian": "9C8DA1",
		},
	}
	if err := WriteManifest(root, in); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}
	out, err := Read(root)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(out.RecipientLogins) != 2 {
		t.Fatalf("RecipientLogins len = %d, want 2 (%v)", len(out.RecipientLogins), out.RecipientLogins)
	}
	if out.RecipientLogins["ying"] != "1A2BF0" {
		t.Errorf("RecipientLogins[ying] = %q, want 1A2BF0 (uppercased)", out.RecipientLogins["ying"])
	}
	if out.RecipientLogins["xian"] != "9C8DA1" {
		t.Errorf("RecipientLogins[xian] = %q, want 9C8DA1", out.RecipientLogins["xian"])
	}
	// Inline, sorted by login (xian < ying).
	body, err := readFile(filepath.Join(root, ".brain", "config.md"))
	if err != nil {
		t.Fatalf("readFile: %v", err)
	}
	if !strings.Contains(body, "recipient_logins: {xian: 9C8DA1, ying: 1A2BF0}") {
		t.Errorf("manifest missing expected inline recipient_logins map:\n%s", body)
	}
}

func TestManifest_EmptyRecipientLoginsOmitted(t *testing.T) {
	root := t.TempDir()
	if err := WriteManifest(root, Manifest{Name: "personal", Recipients: []string{"FP1"}}); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}
	body, err := readFile(filepath.Join(root, ".brain", "config.md"))
	if err != nil {
		t.Fatalf("readFile: %v", err)
	}
	if strings.Contains(body, "recipient_logins") {
		t.Errorf("empty RecipientLogins must be omitted from the manifest:\n%s", body)
	}
}

func TestWriteManifest_NoModeFieldEmitted(t *testing.T) {
	root := t.TempDir()
	if err := WriteManifest(root, Manifest{Name: "personal", Recipients: []string{"FP1"}}); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}
	body, err := readFile(filepath.Join(root, ".brain", "config.md"))
	if err != nil {
		t.Fatalf("readFile: %v", err)
	}
	if strings.Contains(body, "mode:") {
		t.Errorf("WriteManifest emitted mode: field — must be dropped per M4c.\nManifest:\n%s", body)
	}
}

func TestWriteManifest_AtomicReplace(t *testing.T) {
	// Write, then write again with different content; final state
	// reflects the second write, no .tmp left over.
	root := t.TempDir()
	if err := WriteManifest(root, Manifest{Name: "v1", Recipients: []string{"FP1"}}); err != nil {
		t.Fatal(err)
	}
	if err := WriteManifest(root, Manifest{Name: "v2", Recipients: []string{"FP1"}}); err != nil {
		t.Fatal(err)
	}
	m, err := Read(root)
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "v2" {
		t.Errorf("Name = %q after rewrite, want v2", m.Name)
	}
	// .tmp must not linger after a successful rename.
	matches, _ := filepath.Glob(filepath.Join(root, ".brain", "*.tmp"))
	if len(matches) != 0 {
		t.Errorf("found %d stray .tmp files: %v", len(matches), matches)
	}
}

func TestRewriteFrontmatter_PreservesBody(t *testing.T) {
	// Operator-authored body must survive a recipient change.
	root := t.TempDir()
	customBody := `# family brain manifest

This brain is co-authored by Alice and Bob. Sync substrate decision
documented in workshop/issues/000099-sync-decision.md.

## Operating procedure

- Conflicts get resolved via /nous-resolve from inside Claude Code.
- Recipient changes go through the four-eyes principle (both authors
  approve before any add/remove).
`
	// Seed with WriteManifest then overwrite the body with the
	// operator's authored content (simulating a hand-edit after
	// provisioning).
	if err := WriteManifest(root, Manifest{Name: "family", Recipients: []string{"FP_A", "FP_B"}}); err != nil {
		t.Fatalf("WriteManifest seed: %v", err)
	}
	cfg := filepath.Join(root, ".brain", "config.md")
	full := "---\nname: family\nrecipients: [FP_A, FP_B]\n---\n\n" + customBody
	if err := os.WriteFile(cfg, []byte(full), 0o644); err != nil {
		t.Fatalf("seed manifest: %v", err)
	}

	// Add a recipient via RewriteFrontmatter — body should survive.
	m, err := Read(root)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	m.Recipients = append(m.Recipients, "FP_C")
	if err := RewriteFrontmatter(root, m); err != nil {
		t.Fatalf("RewriteFrontmatter: %v", err)
	}

	body, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatalf("read after rewrite: %v", err)
	}
	bodyStr := string(body)
	if !strings.Contains(bodyStr, "# family brain manifest") {
		t.Errorf("body header missing after rewrite:\n%s", bodyStr)
	}
	if !strings.Contains(bodyStr, "This brain is co-authored by Alice and Bob.") {
		t.Errorf("operator-authored prose missing after rewrite — body got clobbered:\n%s", bodyStr)
	}
	if !strings.Contains(bodyStr, "## Operating procedure") {
		t.Errorf("operator-authored sub-heading missing after rewrite:\n%s", bodyStr)
	}
	if !strings.Contains(bodyStr, "FP_C") {
		t.Errorf("new recipient FP_C not in frontmatter after rewrite:\n%s", bodyStr)
	}
	// Frontmatter content gets sorted; the OLD frontmatter shouldn't
	// be lurking somewhere in the body.
	if strings.Count(bodyStr, "---\n") < 2 {
		t.Errorf("expected at least two `---\\n` (open/close) after rewrite:\n%s", bodyStr)
	}
}

func TestRewriteFrontmatter_PreservesAutosaveAndPublish(t *testing.T) {
	// nous#47: the sync-control fields are hand-edited, so a recipient op
	// (Read → mutate → RewriteFrontmatter) must not silently drop them.
	root := t.TempDir()
	cfg := filepath.Join(root, ".brain", "config.md")
	if err := os.MkdirAll(filepath.Join(root, ".brain"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	seed := "---\nname: personal\nrecipients: [FP_A]\nautosave: off\npublish: on\n---\n\n# body\n"
	if err := os.WriteFile(cfg, []byte(seed), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Simulate `nous brain invite`: read, add a recipient, rewrite.
	m, err := Read(root)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	m.Recipients = append(m.Recipients, "FP_B")
	if err := RewriteFrontmatter(root, m); err != nil {
		t.Fatalf("RewriteFrontmatter: %v", err)
	}

	out, err := Read(root)
	if err != nil {
		t.Fatalf("Read after rewrite: %v", err)
	}
	if out.Autosave != "off" {
		t.Errorf("Autosave = %q after rewrite, want off (dropped — the nous#47 bug)", out.Autosave)
	}
	if out.Publish != "on" {
		t.Errorf("Publish = %q after rewrite, want on (dropped — the nous#47 bug)", out.Publish)
	}
}

func TestRewriteFrontmatter_RefusesMissingFrontmatter(t *testing.T) {
	// If the existing manifest doesn't start with `---\n`, refuse to
	// rewrite — better than silently overwriting an unrecognized
	// format.
	root := t.TempDir()
	dir := filepath.Join(root, ".brain")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.md"), []byte("# hand-edited brain config\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RewriteFrontmatter(root, Manifest{Name: "x", Recipients: []string{"FP1"}}); err == nil {
		t.Errorf("RewriteFrontmatter should refuse a frontmatter-less manifest")
	}
}

func TestSetGcryptParticipants_RoundTrip(t *testing.T) {
	repo := t.TempDir()
	if err := exec.Command("git", "-C", repo, "init", "-q").Run(); err != nil {
		t.Fatalf("git init: %v", err)
	}
	// gcrypt-participants is keyed off remote.origin, but git lets us
	// set the config without the remote actually existing. Good — we
	// don't want this lib coupled to a real gcrypt endpoint for
	// testing.
	want := []string{"FP_C", "FP_A", "FP_B"}
	if err := SetGcryptParticipants(repo, want); err != nil {
		t.Fatalf("SetGcryptParticipants: %v", err)
	}
	got, err := ReadGcryptParticipants(repo)
	if err != nil {
		t.Fatalf("ReadGcryptParticipants: %v", err)
	}
	// Sorted on write.
	wantSorted := []string{"FP_A", "FP_B", "FP_C"}
	if len(got) != len(wantSorted) {
		t.Fatalf("got %v, want %v", got, wantSorted)
	}
	for i := range wantSorted {
		if got[i] != wantSorted[i] {
			t.Errorf("[%d] = %q, want %q", i, got[i], wantSorted[i])
		}
	}
}

func TestSetGcryptParticipants_RejectsEmpty(t *testing.T) {
	repo := t.TempDir()
	if err := exec.Command("git", "-C", repo, "init", "-q").Run(); err != nil {
		t.Fatal(err)
	}
	if err := SetGcryptParticipants(repo, nil); err == nil {
		t.Errorf("SetGcryptParticipants(nil) should error — gcrypt rejects empty list")
	}
}

func TestReadGcryptParticipants_UnsetReturnsEmpty(t *testing.T) {
	repo := t.TempDir()
	if err := exec.Command("git", "-C", repo, "init", "-q").Run(); err != nil {
		t.Fatal(err)
	}
	got, err := ReadGcryptParticipants(repo)
	if err != nil {
		t.Errorf("ReadGcryptParticipants on unset key should not error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ReadGcryptParticipants on unset key = %v, want empty", got)
	}
}

func readFile(path string) (string, error) {
	out, err := exec.Command("cat", path).Output()
	return string(out), err
}
