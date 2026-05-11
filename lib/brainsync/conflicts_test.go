package brainsync

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"
)

func TestParseConflictName_FilenameShapes(t *testing.T) {
	cases := []struct {
		name string
		root string
		path string
		want Conflict
		ok   bool
	}{
		{
			name: "nested with extension",
			root: "/r",
			path: "/r/data/life/travel/paris.conflict-xianxu-mbp-20260507T221604Z.md",
			want: Conflict{
				Canonical:    "data/life/travel/paris.md",
				ConflictFile: "data/life/travel/paris.conflict-xianxu-mbp-20260507T221604Z.md",
				Peer:         "xianxu-mbp",
				At:           time.Date(2026, 5, 7, 22, 16, 4, 0, time.UTC),
			},
			ok: true,
		},
		{
			name: "top-level extensionless",
			root: "/r",
			path: "/r/README.conflict-p-20260507T221604Z",
			want: Conflict{
				Canonical:    "README",
				ConflictFile: "README.conflict-p-20260507T221604Z",
				Peer:         "p",
				At:           time.Date(2026, 5, 7, 22, 16, 4, 0, time.UTC),
			},
			ok: true,
		},
		{
			name: "non-conflict filename",
			root: "/r",
			path: "/r/notes.md",
			ok:   false,
		},
		{
			name: "near-miss: timestamp wrong shape",
			root: "/r",
			path: "/r/x.conflict-p-bad.md",
			ok:   false,
		},
		{
			name: "near-miss: prose 'conflict-resolution' suffix",
			root: "/r",
			path: "/r/notes-about-conflict-resolution.md",
			ok:   false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := parseConflictName(c.path, c.root)
			if ok != c.ok {
				t.Fatalf("ok=%v want=%v (got=%+v)", ok, c.ok, got)
			}
			if !ok {
				return
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("got=%+v\nwant=%+v", got, c.want)
			}
		})
	}
}

func TestConflictFiles_WalksAndSkipsSubstrate(t *testing.T) {
	root := t.TempDir()
	mustWrite := func(rel string) {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// real hits
	mustWrite("data/travel/paris.conflict-mbp-20260507T221604Z.md")
	mustWrite("README.conflict-laptop-20260101T000000Z")
	// substrate dirs — should be skipped
	mustWrite(".git/objects/x.conflict-mbp-20260507T221604Z.md")
	mustWrite(".brain/cache/y.conflict-mbp-20260507T221604Z.md")
	// near-misses
	mustWrite("data/notes-about-conflict-resolution.md")
	mustWrite("data/conflict-log.md")

	got, err := ConflictFiles(root)
	if err != nil {
		t.Fatalf("ConflictFiles: %v", err)
	}
	sort.Slice(got, func(i, j int) bool { return got[i].ConflictFile < got[j].ConflictFile })
	if len(got) != 2 {
		t.Fatalf("want 2 conflicts, got %d: %+v", len(got), got)
	}
	if got[0].Canonical != "README" {
		t.Errorf("[0].Canonical=%q want README", got[0].Canonical)
	}
	if got[1].Canonical != "data/travel/paris.md" {
		t.Errorf("[1].Canonical=%q want data/travel/paris.md", got[1].Canonical)
	}
	if got[1].Peer != "mbp" {
		t.Errorf("[1].Peer=%q want mbp", got[1].Peer)
	}
}

func TestConflictFiles_CleanBrainReturnsEmpty(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "notes.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ConflictFiles(root)
	if err != nil {
		t.Fatalf("ConflictFiles: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("want empty, got %+v", got)
	}
}
