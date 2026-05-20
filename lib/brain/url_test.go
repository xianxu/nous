package brain

import "testing"

func TestGitHubOwnerRepo(t *testing.T) {
	cases := []struct {
		url   string
		owner string
		repo  string
		ok    bool
	}{
		{"gcrypt::ssh://git@github.com/xianxu/brain-shared-test.git", "xianxu", "brain-shared-test", true},
		{"gcrypt::ssh://git@github.com:xianxu/brain.git", "xianxu", "brain", true},
		{"gcrypt::https://github.com/xianxu/brain", "xianxu", "brain", true},
		{"ssh://git@github.com/xianxu/brain.git", "xianxu", "brain", true},
		{"https://github.com/xianxu/brain.git", "xianxu", "brain", true},
		{"https://github.com/xianxu/brain", "xianxu", "brain", true},
		{"git@github.com:xianxu/brain.git", "xianxu", "brain", true},
		{"git@github.com:xianxu/brain", "xianxu", "brain", true},
		{"https://github.com/xianxu/brain/", "xianxu", "brain", true},
		// Non-matches: not github.com.
		{"gcrypt::ssh://git@gitlab.com/xianxu/brain.git", "", "", false},
		{"https://example.com/foo/bar", "", "", false},
		{"", "", "", false},
	}
	for _, c := range cases {
		owner, repo, err := GitHubOwnerRepo(c.url)
		if c.ok {
			if err != nil {
				t.Errorf("%s: unexpected error %v", c.url, err)
				continue
			}
			if owner != c.owner || repo != c.repo {
				t.Errorf("%s: got (%q,%q), want (%q,%q)", c.url, owner, repo, c.owner, c.repo)
			}
		} else {
			if err == nil {
				t.Errorf("%s: expected error, got (%q,%q)", c.url, owner, repo)
			}
		}
	}
}
