package brain

import (
	"strings"
	"testing"

	libbrain "github.com/xianxu/nous/lib/brain"
)

func TestClassifyRung(t *testing.T) {
	cases := []struct {
		name      string
		hasRemote bool
		count     int
		want      rung
	}{
		{"local single", false, 1, rungLocal},
		{"local multi (degenerate)", false, 3, rungLocal}, // no remote wins
		{"private", true, 1, rungPrivate},
		{"shared", true, 2, rungShared},
		{"shared many", true, 5, rungShared},
	}
	for _, c := range cases {
		if got := classifyRung(c.hasRemote, c.count); got != c.want {
			t.Errorf("%s: classifyRung(%v,%d) = %d, want %d", c.name, c.hasRemote, c.count, got, c.want)
		}
	}
}

func TestRungLabel(t *testing.T) {
	if got := rungLabel(rungLocal, 1); got != "local" {
		t.Errorf("local label = %q", got)
	}
	if got := rungLabel(rungPrivate, 1); got != "private" {
		t.Errorf("private label = %q", got)
	}
	if got := rungLabel(rungShared, 3); got != "shared · 3" {
		t.Errorf("shared label = %q", got)
	}
}

// TestLabelInner_RungLadder pins the three-rung list label: a local
// brain and a hosted-solo brain are both single-recipient but must
// render distinctly (the bug the ladder fixes).
func TestLabelInner_RungLadder(t *testing.T) {
	local := listItem{manifest: libbrain.Manifest{Path: "/x/scratch", Recipients: []string{"A"}}, hasRemote: false}
	private := listItem{manifest: libbrain.Manifest{Path: "/x/work", Recipients: []string{"A"}}, hasRemote: true}
	shared := listItem{manifest: libbrain.Manifest{Path: "/x/family", Recipients: []string{"A", "B"}}, hasRemote: true}

	if got := local.labelInner(); !strings.Contains(got, "scratch") || !strings.Contains(got, "local") {
		t.Errorf("local label = %q, want scratch + local", got)
	}
	if got := private.labelInner(); !strings.Contains(got, "work") || !strings.Contains(got, "private") {
		t.Errorf("private label = %q, want work + private", got)
	}
	if got := shared.labelInner(); !strings.Contains(got, "family") || !strings.Contains(got, "shared · 2") {
		t.Errorf("shared label = %q, want family + 'shared · 2'", got)
	}
}
