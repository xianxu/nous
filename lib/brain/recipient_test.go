package brain

import (
	"reflect"
	"strings"
	"testing"
)

func TestMatchRecipient_FullAndLast8AndCaseInsensitive(t *testing.T) {
	manifest := []string{
		"0ECF6AC06E9BB6C5B928F10B5D6885D83872C2F0",
		"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
	}
	cases := []struct {
		in   string
		want string
	}{
		{"3872C2F0", "0ECF6AC06E9BB6C5B928F10B5D6885D83872C2F0"},
		{"3872c2f0", "0ECF6AC06E9BB6C5B928F10B5D6885D83872C2F0"}, // case-insensitive
		{"0ECF6AC06E9BB6C5B928F10B5D6885D83872C2F0", "0ECF6AC06E9BB6C5B928F10B5D6885D83872C2F0"},
		{"AAAAAAAA", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"},
	}
	for _, c := range cases {
		got, err := MatchRecipient(manifest, c.in)
		if err != nil {
			t.Errorf("MatchRecipient(%q) err: %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("MatchRecipient(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestMatchRecipient_TooShortIsError(t *testing.T) {
	_, err := MatchRecipient([]string{"AAAA"}, "AAAA")
	if err == nil || !strings.Contains(err.Error(), "too short") {
		t.Errorf("want too-short error, got %v", err)
	}
}

func TestMatchRecipient_NoMatchReturnsEmpty(t *testing.T) {
	got, err := MatchRecipient([]string{"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}, "BBBBBBBB")
	if err != nil {
		t.Errorf("unexpected err: %v", err)
	}
	if got != "" {
		t.Errorf("want empty (no match), got %q", got)
	}
}

func TestCanRemoveRecipient_LastRecipientGuard(t *testing.T) {
	if err := CanRemoveRecipient(Manifest{Recipients: []string{"A"}}); err == nil {
		t.Errorf("want refusal for single recipient")
	}
	if err := CanRemoveRecipient(Manifest{Recipients: []string{"A", "B"}}); err != nil {
		t.Errorf("want allow for 2+, got %v", err)
	}
	if err := CanRemoveRecipient(Manifest{Recipients: nil}); err == nil {
		t.Errorf("want refusal for empty")
	}
}

func TestWithoutRecipient_RemovesCaseInsensitive(t *testing.T) {
	got := WithoutRecipient([]string{"AAA", "BBB", "CCC"}, "bbb")
	want := []string{"AAA", "CCC"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestContainsRecipient_CaseInsensitive(t *testing.T) {
	if !ContainsRecipient([]string{"AAA"}, "aaa") {
		t.Errorf("want match")
	}
	if ContainsRecipient([]string{"AAA"}, "BBB") {
		t.Errorf("want no match")
	}
}
