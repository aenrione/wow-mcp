// SPDX-License-Identifier: GPL-3.0-or-later
// Forever Suite fork addition.

// Board tools: an MCP agent posts notes, tasks, routes and sell lists into the game and reads back what the
// player ticked done. The inbound posts ride the EXISTING request-slot channel (internal/slots), exactly like
// the remote tools in tools_remote.go: a request is queued and the WowMcp_S* slot bodies are rewritten, the
// addon polls a slot every ~15 s, reads the inbox and drives its own Board panel. The readback (wow_board) is
// a plain snapshot read of the "board" section the addon records on each dump. The group is wired by adding
// s.boardTools() to tools(); it reuses the SlotChannel already on the service (s.Slots), no new Config field.

package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"wowmcp/internal/snapshot"
)

// maxBoardEntries bounds how many entries one wow_board_post call may carry.
const maxBoardEntries = 25

var boardCategories = map[string]bool{"sell": true, "task": true, "route": true, "note": true}

type boardGoalArg struct {
	Item int    `json:"item" jsonschema:"itemID the goal tracks against the player's inventory"`
	Qty  int    `json:"qty,omitempty" jsonschema:"target quantity: for collect/bank the amount to reach, for sell the amount to sell down to (default 0)"`
	Mode string `json:"mode,omitempty" jsonschema:"sell (done when the owned count drops to qty, usually 0), collect (done when bag count reaches qty) or bank (done when the banked count reaches qty); default collect"`
}

type boardEntryArg struct {
	Category      string        `json:"category" jsonschema:"required category of the entry: one of sell, task, route, note"`
	Title         string        `json:"title" jsonschema:"required short title line shown in the board"`
	Body          string        `json:"body,omitempty" jsonschema:"optional longer description shown under the title"`
	Items         []int         `json:"items,omitempty" jsonschema:"optional item IDs rendered as clickable item links in the entry"`
	Goal          *boardGoalArg `json:"goal,omitempty" jsonschema:"optional inventory goal that auto-completes the entry: the addon marks it done when the item count reaches the target, and un-marks it if the count changes back (like RXP's to-do list)"`
	MapID         int           `json:"mapID,omitempty" jsonschema:"optional uiMapID for a clickable coordinate link that drops a TomTom waypoint (a zone name is NOT resolved here)"`
	X             float64       `json:"x,omitempty" jsonschema:"optional map X coordinate in 0..100 (required when mapID is given)"`
	Y             float64       `json:"y,omitempty" jsonschema:"optional map Y coordinate in 0..100 (required when mapID is given)"`
	WaypointTitle string        `json:"waypointTitle,omitempty" jsonschema:"optional label for the TomTom waypoint of the coordinate link"`
}

var boardGoalModes = map[string]bool{"sell": true, "collect": true, "bank": true}

type boardPostArgs struct {
	Entries []boardEntryArg `json:"entries" jsonschema:"required list of board entries to post (max 25 per call)"`
}

type boardClearArgs struct {
	IDs []string `json:"ids,omitempty" jsonschema:"optional entry ids to remove; omit to clear the whole board"`
}

// boardEntryFields validates one entry and returns the map the addon's Board.post expects.
func boardEntryFields(e boardEntryArg) (map[string]any, error) {
	category := strings.ToLower(strings.TrimSpace(e.Category))
	if !boardCategories[category] {
		return nil, fmt.Errorf("category %q is not one of sell, task, route, note", e.Category)
	}
	if strings.TrimSpace(e.Title) == "" {
		return nil, fmt.Errorf("every board entry needs a title")
	}
	entry := map[string]any{"category": category, "title": e.Title}
	if e.Body != "" {
		entry["body"] = e.Body
	}
	if len(e.Items) > 0 {
		items := make([]any, 0, len(e.Items))
		for _, id := range e.Items {
			if id > 0 {
				items = append(items, id)
			}
		}
		if len(items) > 0 {
			entry["items"] = items
		}
	}
	if e.MapID > 0 {
		if err := checkCoords(e.MapID, e.X, e.Y); err != nil {
			return nil, err
		}
		coords := map[string]any{"mapID": e.MapID, "x": e.X, "y": e.Y}
		if e.WaypointTitle != "" {
			coords["title"] = e.WaypointTitle
		}
		entry["coords"] = coords
	}
	if goal, err := boardGoalFields(e.Goal); err != nil {
		return nil, err
	} else if goal != nil {
		entry["goal"] = goal
	}
	return entry, nil
}

// boardGoalFields validates an optional inventory goal and returns the map the addon's Board.post expects.
func boardGoalFields(g *boardGoalArg) (map[string]any, error) {
	if g == nil {
		return nil, nil
	}
	if g.Item <= 0 {
		return nil, fmt.Errorf("a board goal needs a positive item id")
	}
	mode := strings.ToLower(strings.TrimSpace(g.Mode))
	if mode == "" {
		mode = "collect"
	}
	if !boardGoalModes[mode] {
		return nil, fmt.Errorf("goal mode %q is not one of sell, collect, bank", g.Mode)
	}
	qty := g.Qty
	if qty < 0 {
		qty = 0
	}
	return map[string]any{"item": g.Item, "qty": qty, "mode": mode}, nil
}

// boardTools: agent-to-player board posts (through the slot channel) and the readback of the board section.
func (s *service) boardTools() []tool {
	return []tool{
		s.boardPostTool(),
		requestTool(s, "wow_board_clear", "Clear the agent board",
			"Asks the addon in game to remove board entries: ids removes those entries, no ids clears the whole board. "+
				"It writes ONLY the WowMcp_S* request-slot addon bodies under Interface/AddOns; "+pollNote+".",
			func(a boardClearArgs) (string, map[string]any, error) {
				fields := map[string]any{}
				if len(a.IDs) > 0 {
					ids := make([]any, len(a.IDs))
					for i, id := range a.IDs {
						ids[i] = id
					}
					fields["ids"] = ids
				}
				return "board_clear", fields, nil
			}),
		s.boardReadTool(),
	}
}

// boardPostTool queues a board_post request and reports how many entries were posted.
func (s *service) boardPostTool() tool {
	const description = "Posts notes, tasks, routes or a sell list to the player's in-game agent board, which the player " +
		"opens with /mcp board. Each entry has a category (sell, task, route, note), a title, an optional body, optional " +
		"item IDs rendered as clickable item links, and optional map coordinates (mapID + x,y in 0..100) rendered as a " +
		"clickable link that drops a TomTom waypoint. Task entries get a tick box the player marks done; read it back with " +
		"wow_board. Up to 25 entries per call. It writes ONLY the WowMcp_S* request-slot addon bodies under Interface/AddOns; " + pollNote + "."
	return tool{name: "wow_board_post", title: "Post to the agent board", writes: true, description: description,
		args: argNames[boardPostArgs](),
		add: func(server *mcp.Server, def *mcp.Tool) {
			mcp.AddTool(server, def, func(ctx context.Context, req *mcp.CallToolRequest, a boardPostArgs) (*mcp.CallToolResult, any, error) {
				if len(a.Entries) == 0 {
					return nil, nil, fmt.Errorf("entries is required: give at least one board entry")
				}
				if len(a.Entries) > maxBoardEntries {
					return nil, nil, fmt.Errorf("at most %d entries per call (got %d): split into several calls", maxBoardEntries, len(a.Entries))
				}
				entries := make([]any, 0, len(a.Entries))
				for i, e := range a.Entries {
					fields, err := boardEntryFields(e)
					if err != nil {
						return nil, nil, fmt.Errorf("entry %d: %w", i+1, err)
					}
					entries = append(entries, fields)
				}
				if _, err := s.sendRequest("board_post", map[string]any{"entries": entries}); err != nil {
					return nil, nil, err
				}
				return textResult(map[string]any{
					"posted": len(entries),
					"note":   "the player sees it in /mcp board within ~15 s",
				})
			})
		}}
}

// boardReadTool returns the "board" section from the latest snapshot: what the agent posted and what the
// player ticked done. It is a plain read of the addon's dump (no slot write).
func (s *service) boardReadTool() tool {
	return accountTool(s, "wow_board", "Read the agent board",
		"The current agent board as the addon recorded it in the last dump: count, byCategory and the entries "+
			"(id, category, title, done). Use it to see what you already posted with wow_board_post and which task "+
			"entries the player has ticked done. Empty until something is posted and the player runs /mcp reload."+freshness,
		func(db *snapshot.Database, _ noArgs) (map[string]any, error) {
			character, snap, err := db.Lookup("")
			if err != nil {
				return nil, err
			}
			out := map[string]any{"snapshot": s.Freshness.SnapshotMeta(character, snap)}
			if value, ok := snap["board"]; ok {
				out["board"] = value
			} else {
				out["board"] = map[string]any{"count": 0, "note": "no board entries; an agent posts with wow_board_post"}
			}
			return out, nil
		})
}
