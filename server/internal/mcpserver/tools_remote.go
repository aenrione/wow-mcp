// SPDX-License-Identifier: GPL-3.0-or-later
// Forever Suite fork addition.

// Remote tools: the request-slot channel to the game (internal/slots). These tools queue a
// request and rewrite the WowMcp_S* slot bodies; the addon in game polls a slot every ~15 s,
// reads the inbox and acts through other addons' public APIs (TomTom waypoints, OpenMap
// focus). This is the fork's one deviation from "the server writes nothing in the game
// directory": the slots package only ever writes Interface/AddOns/WowMcp_S\d{4}/body.lua.
// The group is wired by adding s.remoteTools() to tools() and a SlotChannel to the service.

package mcpserver

import (
	"context"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// SlotChannel is the port to the slot engine (slots.Engine binds a slots.Queue to the
// configured AddOns directory). nil = the channel is off (no --install-slots / --addons-dir).
type SlotChannel interface {
	// Request queues a request of the given kind with its fields and returns the request id.
	Request(kind string, fields map[string]any) (int64, error)
	// Flush rewrites every slot body now (force) or coalesced; it says whether it wrote.
	Flush(force bool) (bool, error)
	// Pending is the number of queued, unanswered requests.
	Pending() (int, error)
	// LastWrite is when the slot bodies were last rewritten (zero = never).
	LastWrite() time.Time
	// Installed counts the installed slot folders.
	Installed() (int, error)
}

func (s *service) slotChannel() SlotChannel { return s.Slots }

func (s *service) slotsOn() (SlotChannel, error) {
	ch := s.slotChannel()
	if ch == nil {
		return nil, fmt.Errorf("the slot channel is off: the server was started without the slot addons configured (install them with the install script's --slots step and wire --addons-dir)")
	}
	return ch, nil
}

const pollNote = "the addon in game polls a slot every ~15 s, so the request takes effect within about that time"

type waypointArgs struct {
	Character string  `json:"character,omitempty" jsonschema:"character the request is meant for (informational); empty = whoever is logged in"`
	MapID     int     `json:"mapID" jsonschema:"required uiMapID of the zone; this tool does NOT resolve a zone name - wow_quest_lookup and the RXP/Questie data give zone map and area IDs"`
	Zone      string  `json:"zone,omitempty" jsonschema:"zone name for the waypoint title only; NOT a substitute for mapID"`
	X         float64 `json:"x" jsonschema:"map X coordinate in 0..100 (as shown in game, e.g. 52.6)"`
	Y         float64 `json:"y" jsonschema:"map Y coordinate in 0..100"`
	Title     string  `json:"title,omitempty" jsonschema:"waypoint label shown on the TomTom arrow"`
}

type focusArgs struct {
	MapID int     `json:"mapID" jsonschema:"required uiMapID of the zone to focus the stitched map on"`
	X     float64 `json:"x" jsonschema:"map X coordinate in 0..100"`
	Y     float64 `json:"y" jsonschema:"map Y coordinate in 0..100"`
}

// checkCoords validates a mapID with 0..100 map coordinates.
func checkCoords(mapID int, x, y float64) error {
	if mapID <= 0 {
		return fmt.Errorf("mapID is required (a zone name is not resolved here): take the zone's uiMapID from wow_quest_lookup or the guide/quest data")
	}
	if x < 0 || x > 100 || y < 0 || y > 100 {
		return fmt.Errorf("x and y are map coordinates in 0..100 (got %v, %v)", x, y)
	}
	return nil
}

// sendRequest queues one request and flushes the slot bodies immediately.
func (s *service) sendRequest(kind string, fields map[string]any) (map[string]any, error) {
	ch, err := s.slotsOn()
	if err != nil {
		return nil, err
	}
	id, err := ch.Request(kind, fields)
	if err != nil {
		return nil, err
	}
	if _, err := ch.Flush(true); err != nil {
		return nil, fmt.Errorf("request %d is queued but the slot bodies could not be written: %w", id, err)
	}
	installed, err := ch.Installed()
	if err != nil {
		return nil, err
	}
	return map[string]any{"requestId": id, "slotsLeft": installed, "note": pollNote}, nil
}

// requestTool is a writes:true tool whose handler only queues a slot request.
func requestTool[In any](s *service, name, title, description string,
	build func(a In) (kind string, fields map[string]any, err error)) tool {

	return tool{name: name, title: title, description: description, writes: true, args: argNames[In](),
		add: func(server *mcp.Server, def *mcp.Tool) {
			mcp.AddTool(server, def, func(ctx context.Context, req *mcp.CallToolRequest, a In) (*mcp.CallToolResult, any, error) {
				kind, fields, err := build(a)
				if err != nil {
					return nil, nil, err
				}
				out, err := s.sendRequest(kind, fields)
				if err != nil {
					return nil, nil, err
				}
				return textResult(out)
			})
		}}
}

// remoteTools: requests to the game through the slot channel.
func (s *service) remoteTools() []tool {
	return []tool{
		requestTool(s, "wow_waypoint_set", "Set a TomTom waypoint",
			"Asks the addon in game to set a TomTom waypoint: mapID (required uiMapID; a zone name is NOT resolved here - wow_quest_lookup and the quest/guide data provide zone map and area IDs), x and y in 0..100, optional title and character. "+
				"It writes ONLY the WowMcp_S* request-slot addon bodies under Interface/AddOns; "+pollNote+". No gameplay automation: in game the waypoint is set through TomTom's public API.",
			func(a waypointArgs) (string, map[string]any, error) {
				if err := checkCoords(a.MapID, a.X, a.Y); err != nil {
					return "", nil, err
				}
				fields := map[string]any{"mapID": a.MapID, "x": a.X, "y": a.Y}
				if a.Title != "" {
					fields["title"] = a.Title
				}
				if a.Zone != "" {
					fields["zone"] = a.Zone
				}
				if a.Character != "" {
					fields["character"] = a.Character
				}
				return "waypoint", fields, nil
			}),

		requestTool(s, "wow_map_focus", "Focus the world map",
			"Asks the addon in game to focus the stitched world map (OpenMap) on mapID (uiMapID) at x, y in 0..100. "+
				"It writes ONLY the WowMcp_S* request-slot addon bodies; "+pollNote+".",
			func(a focusArgs) (string, map[string]any, error) {
				if err := checkCoords(a.MapID, a.X, a.Y); err != nil {
					return "", nil, err
				}
				return "focus", map[string]any{"mapID": a.MapID, "x": a.X, "y": a.Y}, nil
			}),

		requestTool(s, "wow_waypoints_clear", "Clear TomTom waypoints",
			"Asks the addon in game to clear all TomTom waypoints. It writes ONLY the WowMcp_S* request-slot addon bodies; "+pollNote+".",
			func(_ noArgs) (string, map[string]any, error) { return "clearwaypoints", nil, nil }),

		s.slotsStatusTool(),
	}
}

// slotsStatusTool reports the slot channel: installed slots, pending requests, last write.
func (s *service) slotsStatusTool() tool {
	return tool{name: "wow_slots_status", title: "Request slot status", args: argNames[noArgs](),
		description: "State of the request-slot channel to the game: how many WowMcp_S* slot addons are installed, how many requests are pending (queued but not yet acknowledged by the addon), when the slot bodies were last written, and a rough slotsLeftEstimate (each addon poll, ~15 s, consumes one slot until the next /reload in game).",
		add: func(server *mcp.Server, def *mcp.Tool) {
			mcp.AddTool(server, def, func(ctx context.Context, req *mcp.CallToolRequest, _ noArgs) (*mcp.CallToolResult, any, error) {
				out, err := s.slotsStatus()
				if err != nil {
					return nil, nil, err
				}
				return textResult(out)
			})
		}}
}

func (s *service) slotsStatus() (map[string]any, error) {
	ch, err := s.slotsOn()
	if err != nil {
		return nil, err
	}
	installed, err := ch.Installed()
	if err != nil {
		return nil, err
	}
	pending, err := ch.Pending()
	if err != nil {
		return nil, err
	}
	out := map[string]any{"installed": installed, "pending": pending, "slotsLeftEstimate": installed}
	if last := ch.LastWrite(); !last.IsZero() {
		out["lastWrite"] = last.Format("2006-01-02 15:04:05")
	} else {
		out["lastWrite"] = nil
	}
	return out, nil
}
