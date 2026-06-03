package brain

import "testing"

func TestLooksLikeFingerprint(t *testing.T) {
	cases := []struct {
		s    string
		want bool
	}{
		{"DC73FD263FBD8C5DA86A3D72F61E60BD8E7AB6E9", true},
		{"dc73fd263fbd8c5da86a3d72f61e60bd8e7ab6e9", true},
		{"Dc73Fd263fbd8c5dA86A3d72F61E60BD8E7AB6E9", true},
		// Wrong length.
		{"DC73FD263FBD8C5DA86A3D72F61E60BD8E7AB6E", false},
		{"DC73FD263FBD8C5DA86A3D72F61E60BD8E7AB6E90", false},
		// Github logins shouldn't match.
		{"yingtest42", false},
		{"xianxu", false},
		{"some-user-with-dashes", false},
		// Non-hex characters.
		{"DC73FD263FBD8C5DA86A3D72F61E60BD8E7AB6EZ", false},
		// Empty / short.
		{"", false},
		{"DEADBEEF", false},
	}
	for _, c := range cases {
		if got := looksLikeFingerprint(c.s); got != c.want {
			t.Errorf("looksLikeFingerprint(%q) = %v, want %v", c.s, got, c.want)
		}
	}
}

func TestDetectLoginDrift(t *testing.T) {
	cases := []struct {
		name      string
		recorded  []string
		current   []string
		wantOrphan []string
	}{
		{"all current", []string{"ying", "xian"}, []string{"xian", "ying"}, nil},
		{"one renamed away", []string{"ying", "xian"}, []string{"xian", "ying-new"}, []string{"ying"}},
		{"case-insensitive match", []string{"Ying"}, []string{"ying"}, nil},
		{"empty + dupes ignored", []string{"ying", "", "ying"}, []string{"xian"}, []string{"ying"}},
		{"no collaborators -> all orphaned, sorted", []string{"zeb", "amy"}, nil, []string{"amy", "zeb"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := DetectLoginDrift(c.recorded, c.current)
			if len(got) != len(c.wantOrphan) {
				t.Fatalf("orphaned = %v, want %v", got, c.wantOrphan)
			}
			for i := range c.wantOrphan {
				if got[i] != c.wantOrphan[i] {
					t.Errorf("orphaned[%d] = %q, want %q (full: %v)", i, got[i], c.wantOrphan[i], got)
				}
			}
		})
	}
}
