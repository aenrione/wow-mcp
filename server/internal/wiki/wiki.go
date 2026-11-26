// SPDX-License-Identifier: GPL-3.0-or-later
// Forever Suite fork addition.

// Package wiki is a small markdown wiki in the server's own directory: pages the assistant
// writes for itself (min-max notes, farming routes, price lore for WoW: Forever). A page is
// one <slug>.md file whose optional first line "# Title" is its title. The wiki lives next
// to the history and goals file, never inside the game directory, and slugs are confined to
// ^[a-z0-9-]{1,64}$ so a page name can never escape the wiki directory.
package wiki

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

var slugRe = regexp.MustCompile(`^[a-z0-9-]{1,64}$`)

const (
	maxHits    = 10
	maxSnippet = 200
)

// Wiki reads and writes pages under Dir.
type Wiki struct {
	Dir string
}

// Page describes one page for listings.
type Page struct {
	Slug     string    `json:"slug"`
	Title    string    `json:"title"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
}

// Hit is one search result: the page and a short snippet around the first match.
type Hit struct {
	Slug    string `json:"slug"`
	Title   string `json:"title"`
	Snippet string `json:"snippet"`
}

// Where returns the wiki directory.
func (w Wiki) Where() string { return w.Dir }

// checkSlug rejects anything but ^[a-z0-9-]{1,64}$: no dots, slashes, spaces or upper case.
func checkSlug(slug string) error {
	if !slugRe.MatchString(slug) {
		return fmt.Errorf("bad page slug %q: use 1-64 of a-z, 0-9 and - (e.g. gold-routes)", slug)
	}
	return nil
}

func (w Wiki) path(slug string) string { return filepath.Join(w.Dir, slug+".md") }

// List returns all pages sorted by slug.
func (w Wiki) List() ([]Page, error) {
	entries, err := os.ReadDir(w.Dir)
	if errors.Is(err, fs.ErrNotExist) {
		return []Page{}, nil
	}
	if err != nil {
		return nil, err
	}
	pages := []Page{}
	for _, e := range entries {
		slug := strings.TrimSuffix(e.Name(), ".md")
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") || !slugRe.MatchString(slug) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		pages = append(pages, Page{Slug: slug, Title: w.title(slug), Size: info.Size(), Modified: info.ModTime()})
	}
	sort.Slice(pages, func(i, j int) bool { return pages[i].Slug < pages[j].Slug })
	return pages, nil
}

// title reads the "# Title" first line of a page; the slug when there is none.
func (w Wiki) title(slug string) string {
	content, err := w.Read(slug)
	if err != nil {
		return slug
	}
	return titleOf(slug, content)
}

func titleOf(slug, content string) string {
	first, _, _ := strings.Cut(content, "\n")
	if title, ok := strings.CutPrefix(first, "# "); ok && strings.TrimSpace(title) != "" {
		return strings.TrimSpace(title)
	}
	return slug
}

// Read returns the content of the page.
func (w Wiki) Read(slug string) (string, error) {
	if err := checkSlug(slug); err != nil {
		return "", err
	}
	data, err := os.ReadFile(w.path(slug))
	if errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("no wiki page %q; wow_wiki_list shows the pages", slug)
	}
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// Write replaces the whole page (creating the wiki directory when needed) atomically:
// the content goes to a temporary file in the same directory, then rename.
func (w Wiki) Write(slug, content string) (err error) {
	if err := checkSlug(slug); err != nil {
		return err
	}
	if err := os.MkdirAll(w.Dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(w.Dir, slug+".*.tmp")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()
	if _, err = tmp.WriteString(content); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), w.path(slug))
}

// Search looks for the query (case-insensitive) in titles and content and returns up to
// 10 hits, each with a snippet of at most 200 characters around the first match.
func (w Wiki) Search(query string) ([]Hit, error) {
	needle := strings.ToLower(strings.TrimSpace(query))
	if needle == "" {
		return nil, fmt.Errorf("give a query to search the wiki for")
	}
	pages, err := w.List()
	if err != nil {
		return nil, err
	}
	hits := []Hit{}
	for _, page := range pages {
		content, err := w.Read(page.Slug)
		if err != nil {
			continue
		}
		at := strings.Index(strings.ToLower(content), needle)
		if at < 0 && !strings.Contains(strings.ToLower(page.Title), needle) {
			continue
		}
		hits = append(hits, Hit{Slug: page.Slug, Title: page.Title, Snippet: snippet(content, at)})
		if len(hits) == maxHits {
			break
		}
	}
	return hits, nil
}

// snippet cuts at most maxSnippet characters around the match at byte offset at (-1 = title
// match: the start of the page), keeping whole lines when it can.
func snippet(content string, at int) string {
	if at < 0 {
		at = 0
	}
	start := at
	if cut := strings.LastIndexByte(content[:at], '\n'); cut >= 0 {
		start = cut + 1
	}
	if at-start > maxSnippet/2 {
		start = at - maxSnippet/2
	}
	rest := content[start:]
	if end := strings.IndexByte(rest, '\n'); end >= 0 && end <= maxSnippet {
		rest = rest[:end]
	} else if len(rest) > maxSnippet {
		rest = rest[:maxSnippet]
	}
	return strings.TrimSpace(strings.ToValidUTF8(rest, ""))
}

// seedContent is the README created by Seed.
const seedContent = `# WowMcp wiki

Notes the assistant keeps for itself about this WoW: Forever account: min-max advice that
holds between sessions (profession and gold routes, dungeon and quest lore, what sells,
realm price patterns). Written and read through the wow_wiki_* tools; stored as plain
markdown files in the server's own directory, never inside the game directory.

Conventions: one topic per page, slug in a-z0-9- (e.g. gold-routes), first line "# Title".
Keep claims dated and cite the tool that produced them (e.g. wow_auctions 2026-11-25).
`

// Seed creates README.md explaining the wiki when the directory has no pages yet.
func (w Wiki) Seed() error {
	pages, err := w.List()
	if err != nil {
		return err
	}
	if len(pages) > 0 {
		return nil
	}
	return w.Write("readme", seedContent)
}
