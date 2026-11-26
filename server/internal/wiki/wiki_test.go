// SPDX-License-Identifier: GPL-3.0-or-later
// Forever Suite fork addition.

package wiki

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteReadListRoundTrip(t *testing.T) {
	w := Wiki{Dir: filepath.Join(t.TempDir(), "wiki")}
	content := "# Gold routes\n\nTin farming in Hillsbrad pays ~2g/h (wow_auctions 2026-11-25).\n"
	if err := w.Write("gold-routes", content); err != nil {
		t.Fatal(err)
	}
	if err := w.Write("untitled", "just notes\n"); err != nil {
		t.Fatal(err)
	}
	got, err := w.Read("gold-routes")
	if err != nil || got != content {
		t.Fatalf("read: %q %v", got, err)
	}
	pages, err := w.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 2 || pages[0].Slug != "gold-routes" || pages[0].Title != "Gold routes" {
		t.Fatalf("list: %+v", pages)
	}
	if pages[1].Title != "untitled" { // no "# Title" line: the slug is the title
		t.Errorf("untitled page title: %q", pages[1].Title)
	}
	if pages[0].Size != int64(len(content)) || pages[0].Modified.IsZero() {
		t.Errorf("page meta: %+v", pages[0])
	}
}

func TestWriteReplacesWholePageAtomically(t *testing.T) {
	w := Wiki{Dir: t.TempDir()}
	if err := w.Write("a", "old"); err != nil {
		t.Fatal(err)
	}
	if err := w.Write("a", "new"); err != nil {
		t.Fatal(err)
	}
	if got, _ := w.Read("a"); got != "new" {
		t.Fatalf("read after rewrite: %q", got)
	}
	entries, _ := os.ReadDir(w.Dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("leftover temp file: %s", e.Name())
		}
	}
}

// A slug can never name a path outside the wiki directory or an unexpected file.
func TestSlugSanitization(t *testing.T) {
	w := Wiki{Dir: t.TempDir()}
	bad := []string{
		"", ".", "..", "../x", "..%2Fx", "a/b", `a\b`, "A", "Upper", "with space",
		"dot.md", "under_score", "ütf", strings.Repeat("a", 65),
	}
	for _, slug := range bad {
		if err := w.Write(slug, "x"); err == nil {
			t.Errorf("Write accepted slug %q", slug)
		}
		if _, err := w.Read(slug); err == nil {
			t.Errorf("Read accepted slug %q", slug)
		}
	}
	if err := w.Write(strings.Repeat("a", 64), "x"); err != nil {
		t.Errorf("64 chars is the limit, not over it: %v", err)
	}
	entries, _ := os.ReadDir(w.Dir)
	if len(entries) != 1 {
		t.Errorf("bad slugs left files behind: %v", entries)
	}
}

func TestReadMissingPageSaysSo(t *testing.T) {
	w := Wiki{Dir: t.TempDir()}
	if _, err := w.Read("nope"); err == nil || !strings.Contains(err.Error(), "wow_wiki_list") {
		t.Fatalf("missing page: %v", err)
	}
}

func TestSearch(t *testing.T) {
	w := Wiki{Dir: t.TempDir()}
	long := strings.Repeat("x", 500)
	if err := w.Write("gold-routes", "# Gold routes\n\nTin ore farming in HILLSBRAD foothills.\n"); err != nil {
		t.Fatal(err)
	}
	if err := w.Write("long", "# Long\n"+long+"needle"+long); err != nil {
		t.Fatal(err)
	}
	if err := w.Write("title-only", "# Needlework notes\n\nnothing else\n"); err != nil {
		t.Fatal(err)
	}
	hits, err := w.Search("hillsbrad")
	if err != nil || len(hits) != 1 || hits[0].Slug != "gold-routes" {
		t.Fatalf("case-insensitive search: %+v %v", hits, err)
	}
	if !strings.Contains(hits[0].Snippet, "HILLSBRAD") {
		t.Errorf("snippet misses the match: %q", hits[0].Snippet)
	}
	hits, _ = w.Search("needle")
	if len(hits) != 2 {
		t.Fatalf("title+content matches: %+v", hits)
	}
	for _, h := range hits {
		if len(h.Snippet) > 200 {
			t.Errorf("%s: snippet has %d chars", h.Slug, len(h.Snippet))
		}
	}
	if _, err := w.Search("  "); err == nil {
		t.Error("an empty query is an error")
	}
}

func TestSearchCapsAtTenHits(t *testing.T) {
	w := Wiki{Dir: t.TempDir()}
	for i := 0; i < 12; i++ {
		if err := w.Write("page-"+string(rune('a'+i)), "common word\n"); err != nil {
			t.Fatal(err)
		}
	}
	hits, err := w.Search("common")
	if err != nil || len(hits) != 10 {
		t.Fatalf("hits: %d %v", len(hits), err)
	}
}

func TestSeed(t *testing.T) {
	w := Wiki{Dir: filepath.Join(t.TempDir(), "wiki")}
	if err := w.Seed(); err != nil {
		t.Fatal(err)
	}
	content, err := w.Read("readme")
	if err != nil || !strings.Contains(content, "wow_wiki_") {
		t.Fatalf("seed readme: %v\n%s", err, content)
	}
	// Seeding again, or seeding a wiki that already has pages, changes nothing.
	if err := w.Write("readme", "# mine\n"); err != nil {
		t.Fatal(err)
	}
	if err := w.Seed(); err != nil {
		t.Fatal(err)
	}
	if content, _ := w.Read("readme"); content != "# mine\n" {
		t.Fatalf("seed overwrote an existing page: %q", content)
	}
}
