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
