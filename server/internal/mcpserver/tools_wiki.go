// SPDX-License-Identifier: GPL-3.0-or-later
// Forever Suite fork addition.

// Wiki tools: the server-owned markdown wiki (internal/wiki) where the assistant keeps
// min-max notes for WoW: Forever between sessions. Everything lives in the server's own
// wiki directory, never inside the game directory. The group is wired by adding
// s.wikiTools() to tools() and a WikiStore to the service.

package mcpserver

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"wowmcp/internal/wiki"
)

// WikiStore is the port to the wiki (wiki.Wiki implements it). nil = the wiki is off.
type WikiStore interface {
	List() ([]wiki.Page, error)
	Read(slug string) (string, error)
	Write(slug, content string) error
	Search(query string) ([]wiki.Hit, error)
	Where() string
}

func (s *service) wikiStore() WikiStore { return s.Wiki }

func (s *service) wikiOn() (WikiStore, error) {
	w := s.wikiStore()
	if w == nil {
		return nil, fmt.Errorf("the wiki is off: the server has no wiki directory wired")
	}
	return w, nil
}

type wikiReadArgs struct {
	Slug string `json:"slug" jsonschema:"page slug from wow_wiki_list, e.g. gold-routes"`
}

type wikiSearchArgs struct {
	Query string `json:"query" jsonschema:"text to look for (case-insensitive) in page titles and content"`
}

type wikiWriteArgs struct {
	Slug    string `json:"slug" jsonschema:"page slug: 1-64 of a-z, 0-9 and -, e.g. gold-routes"`
	Content string `json:"content" jsonschema:"the whole new markdown content of the page; first line '# Title' becomes its title"`
}

// wikiTool is a tool whose handler needs only the wiki port.
func wikiTool[In any](s *service, name, title, description string, writes bool,
	view func(w WikiStore, a In) (map[string]any, error)) tool {

	return tool{name: name, title: title, description: description, writes: writes, args: argNames[In](),
		add: func(server *mcp.Server, def *mcp.Tool) {
			mcp.AddTool(server, def, func(ctx context.Context, req *mcp.CallToolRequest, a In) (*mcp.CallToolResult, any, error) {
				w, err := s.wikiOn()
				if err != nil {
					return nil, nil, err
				}
				out, err := view(w, a)
				if err != nil {
					return nil, nil, err
				}
				return textResult(out)
			})
		}}
}

// wikiTools: the assistant's own markdown wiki.
func (s *service) wikiTools() []tool {
	return []tool{
		wikiTool(s, "wow_wiki_list", "Wiki pages",
			"Lists the pages of the assistant's own wiki (markdown files in the server's wiki directory): slug, title, size and when each was last modified. The wiki holds durable min-max notes for WoW: Forever, written by and for the assistant.",
			false, func(w WikiStore, _ noArgs) (map[string]any, error) {
				pages, err := w.List()
				if err != nil {
					return nil, err
				}
				return map[string]any{"pages": pages, "dir": w.Where()}, nil
			}),

		wikiTool(s, "wow_wiki_read", "Read a wiki page",
			"Returns the whole markdown content of one wiki page by slug (see wow_wiki_list).",
			false, func(w WikiStore, a wikiReadArgs) (map[string]any, error) {
				content, err := w.Read(a.Slug)
				if err != nil {
					return nil, err
				}
				return map[string]any{"slug": a.Slug, "content": content}, nil
			}),

		wikiTool(s, "wow_wiki_search", "Search the wiki",
			"Searches the wiki (case-insensitive) over page titles and content; up to 10 hits, each with the slug, title and a snippet of at most 200 characters around the first match.",
			false, func(w WikiStore, a wikiSearchArgs) (map[string]any, error) {
				hits, err := w.Search(a.Query)
				if err != nil {
					return nil, err
				}
				return map[string]any{"query": a.Query, "hits": hits}, nil
			}),

		wikiTool(s, "wow_wiki_write", "Write a wiki page",
			"Creates or replaces one page of the assistant's wiki (slug: 1-64 of a-z, 0-9 and -; content: the whole new markdown). It writes ONLY a <slug>.md file in the server's own wiki directory - nothing in the game, nothing in the game directory, nothing anywhere else. Keep notes durable: one topic per page, dated claims.",
			true, func(w WikiStore, a wikiWriteArgs) (map[string]any, error) {
				if err := w.Write(a.Slug, a.Content); err != nil {
					return nil, err
				}
				return map[string]any{"written": a.Slug, "bytes": len(a.Content), "dir": w.Where()}, nil
			}),
	}
}
