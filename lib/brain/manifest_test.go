package brain

import (
	"os"
	"path/filepath"
	"testing"
)

func writeBrain(t *testing.T, root, manifest string) string {
	t.Helper()
	dir := filepath.Join(root, ".brain")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.md"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return root
}

func TestRead_PrivateBrain(t *testing.T) {
	root := writeBrain(t, t.TempDir(), `---
mode: private
name: personal
recipients: [0ECF6AC06E9BB6C5B928F10B5D6885D83872C2F0]
sync_substrate: none
---

# personal brain manifest
`)
	m, err := Read(root)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if m.LegacyMode != "private" {
		t.Errorf("LegacyMode = %q, want private (preserved for round-trip)", m.LegacyMode)
	}
	if m.Shared() {
		t.Errorf("Shared() = true for single-recipient manifest")
	}
	if m.Name != "personal" {
		t.Errorf("Name = %q, want personal", m.Name)
	}
	if len(m.Recipients) != 1 || m.Recipients[0] != "0ECF6AC06E9BB6C5B928F10B5D6885D83872C2F0" {
		t.Errorf("Recipients = %v, want one fingerprint", m.Recipients)
	}
	if m.SyncSubstrate != "none" {
		t.Errorf("SyncSubstrate = %q, want none", m.SyncSubstrate)
	}
}

func TestRead_SharedBrainMultipleRecipients(t *testing.T) {
	root := writeBrain(t, t.TempDir(), `---
mode: shared
name: family
recipients: [FP1, "FP2", 'FP3']
sync_substrate: syncthing
---
`)
	m, err := Read(root)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	want := []string{"FP1", "FP2", "FP3"}
	if len(m.Recipients) != len(want) {
		t.Fatalf("Recipients = %v, want %v", m.Recipients, want)
	}
	for i, w := range want {
		if m.Recipients[i] != w {
			t.Errorf("Recipients[%d] = %q, want %q", i, m.Recipients[i], w)
		}
	}
	if !m.Shared() {
		t.Errorf("Shared() = false for 3-recipient manifest")
	}
}

func TestRead_AutosaveAndPublishFields(t *testing.T) {
	cases := []struct {
		name         string
		manifest     string
		wantAutosave string
		wantPublish  string
	}{
		{
			name:         "both set",
			manifest:     "---\nname: n\nrecipients: [FP1]\nautosave: off\npublish: on\n---\n",
			wantAutosave: "off",
			wantPublish:  "on",
		},
		{
			name:         "publish off",
			manifest:     "---\nname: n\nrecipients: [FP1]\npublish: off\n---\n",
			wantAutosave: "",
			wantPublish:  "off",
		},
		{
			name:         "neither set (predates fields)",
			manifest:     "---\nname: n\nrecipients: [FP1]\n---\n",
			wantAutosave: "",
			wantPublish:  "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, err := Read(writeBrain(t, t.TempDir(), tc.manifest))
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			if m.Autosave != tc.wantAutosave {
				t.Errorf("Autosave = %q, want %q", m.Autosave, tc.wantAutosave)
			}
			if m.Publish != tc.wantPublish {
				t.Errorf("Publish = %q, want %q", m.Publish, tc.wantPublish)
			}
		})
	}
}

func TestShared_Boundary(t *testing.T) {
	// Boundary: exactly 1 recipient = not shared; 2+ = shared.
	cases := []struct {
		recipients []string
		want       bool
	}{
		{nil, false},
		{[]string{"FP1"}, false},
		{[]string{"FP1", "FP2"}, true},
		{[]string{"FP1", "FP2", "FP3"}, true},
	}
	for _, c := range cases {
		m := Manifest{Recipients: c.recipients}
		if got := m.Shared(); got != c.want {
			t.Errorf("Shared() with %d recipients = %v, want %v", len(c.recipients), got, c.want)
		}
	}
}

func TestRead_MissingFrontmatter(t *testing.T) {
	root := writeBrain(t, t.TempDir(), `# bare brain
no frontmatter here
`)
	if _, err := Read(root); err == nil {
		t.Errorf("Read should fail without frontmatter")
	}
}

func TestRead_NotABrain(t *testing.T) {
	if _, err := Read(t.TempDir()); err == nil {
		t.Errorf("Read should fail for dir without .brain/config.md")
	}
}

func TestDiscoverAll(t *testing.T) {
	workspace := t.TempDir()
	t.Setenv("WORKSPACE_ROOT", workspace)

	writeBrain(t, filepath.Join(workspace, "personal"), "---\nmode: private\nname: personal\nrecipients: [FP1]\n---\n")
	writeBrain(t, filepath.Join(workspace, "family"), "---\nmode: shared\nname: family\nrecipients: [FP1, FP2]\n---\n")
	// Non-brain dir should be skipped.
	if err := os.MkdirAll(filepath.Join(workspace, "not-a-brain"), 0o755); err != nil {
		t.Fatal(err)
	}

	brains, err := DiscoverAll()
	if err != nil {
		t.Fatalf("DiscoverAll: %v", err)
	}
	if len(brains) != 2 {
		t.Errorf("expected 2 brains, got %d: %+v", len(brains), brains)
	}
}

func TestParseList(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"[a, b, c]", []string{"a", "b", "c"}},
		{`["a", "b"]`, []string{"a", "b"}},
		{"[]", nil},
		{"[ a , b ]", []string{"a", "b"}},
		{"[a]", []string{"a"}},
	}
	for _, tt := range tests {
		got := parseList(tt.in)
		if len(got) != len(tt.want) {
			t.Errorf("parseList(%q) = %v, want %v", tt.in, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("parseList(%q)[%d] = %q, want %q", tt.in, i, got[i], tt.want[i])
			}
		}
	}
}
