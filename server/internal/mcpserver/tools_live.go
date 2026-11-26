// SPDX-License-Identifier: GPL-3.0-or-later
// Forever Suite fork addition.

// Live tools: the realtime outbound channel (internal/strip). The addon paints the changed
// sections into a pixel strip and screenshots it; the server's screenshot watcher decodes
// the strips into a strip.LiveStore. wow_refresh queues a slots request for fresh data and
// waits for the next live payload; wow_live reads a section from the live state (a realtime
// sibling of wow_section). The group is wired by adding s.liveTools() to tools(), a
// `LiveData Live` field to Config and rewiring livePort below to return s.LiveData.
package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Live is the port to the live state kept by the screenshot watcher (strip.LiveStore).
// The addon sends deltas, so the state is merged per character with a time per section.
type Live interface {
	// Get returns the merged live sections of a character (the same shape as a snapshot:
	// section name -> data), the server time the last payload for it arrived, and whether
	// anything is known. Empty name = the character of the most recent payload.
	Get(character string) (map[string]any, time.Time, bool)
	// Section returns one live section with the server time its data last changed.
	Section(character, section string) (any, time.Time, bool)
}

// livePort is the live-state port. The orchestrator rewires it to `return s.LiveData` once
// Config gains a `LiveData Live` field (the same pre-wiring pattern slotChannel had).
func (s *service) livePort() Live { return s.LiveData }

// liveClock and liveSleep are the clock of the live tools; the orchestrator may rewire them
// (through Config) for tests. The handler cores take them as arguments either way.
func (s *service) liveClock() func() time.Time    { return time.Now }
func (s *service) liveSleep() func(time.Duration) { return time.Sleep }

func (s *service) liveOn() (Live, error) {
	if live := s.livePort(); live != nil {
		return live, nil
	}
	return nil, errors.New("the live channel is off: the server was started without the screenshot watcher (it needs the game's Screenshots folder and the WowMcp live addon)")
}

// livePoll is how often wow_refresh re-reads the live state while waiting.
const livePoll = 500 * time.Millisecond

// LiveWait polls live until a payload for the character (empty = any) arrives at or after
// since, or until timeout passes. It returns the time of the newest payload it saw and
// whether that payload is fresh (not older than since).
func LiveWait(live Live, character string, since time.Time, timeout time.Duration,
	clock func() time.Time, sleep func(time.Duration)) (time.Time, bool) {

	deadline := clock().Add(timeout)
	for {
		_, at, ok := live.Get(character)
		if ok && !at.Before(since) {
			return at, true
		}
		if !clock().Before(deadline) {
			return at, false
		}
		sleep(livePoll)
	}
}

type refreshArgs struct {
	TimeoutSeconds int      `json:"timeoutSeconds,omitempty" jsonschema:"how long to wait for the live payload, in seconds; default 20, at most 60"`
	Sections       []string `json:"sections,omitempty" jsonschema:"only collect these sections (e.g. bags, character); empty = whatever the addon finds changed"`
	Full           bool     `json:"full,omitempty" jsonschema:"true = ask for a full snapshot of every section instead of a delta of changes"`
}

type liveArgs struct {
	Section   string `json:"section" jsonschema:"live section name, e.g. character, bags, quests"`
	Character string `json:"character,omitempty" jsonschema:"character name; empty = the character of the most recent live payload"`
}

// liveTools: the realtime outbound channel.
func (s *service) liveTools() []tool {
	return []tool{
		{name: "wow_refresh", title: "Request live data", writes: true, args: argNames[refreshArgs](),
			description: "Asks the addon in game for fresh live data and waits for it to arrive: it queues a request through the WowMcp_S* slot channel (sections narrows it, full=true asks for a complete snapshot instead of a delta of changes) and polls the live state up to timeoutSeconds (default 20, at most 60). fresh=true means a payload arrived after the request; read it with wow_live. " + pollNote + ". No gameplay automation: the addon only paints pixels and takes a screenshot.",
			add: func(server *mcp.Server, def *mcp.Tool) {
				mcp.AddTool(server, def, func(ctx context.Context, req *mcp.CallToolRequest, a refreshArgs) (*mcp.CallToolResult, any, error) {
					live, err := s.liveOn()
					if err != nil {
						return nil, nil, err
					}
					ch, err := s.slotsOn()
					if err != nil {
						return nil, nil, err
					}
					out, err := refreshRun(live, ch, s.liveClock(), s.liveSleep(), a)
					if err != nil {
						return nil, nil, err
					}
					return textResult(out)
				})
			}},

		{name: "wow_live", title: "Live section", args: argNames[liveArgs](),
			description: "One section of the live state the addon streams through screenshots (a realtime sibling of wow_section): the section's data with its own age in seconds (the addon sends deltas, so each section has the time it last changed) and the age of the last live payload. When the data is too old or the section is missing, ask for it with wow_refresh.",
			add: func(server *mcp.Server, def *mcp.Tool) {
				mcp.AddTool(server, def, func(ctx context.Context, req *mcp.CallToolRequest, a liveArgs) (*mcp.CallToolResult, any, error) {
					live, err := s.liveOn()
					if err != nil {
						return nil, nil, err
					}
					out, err := liveSectionView(live, s.liveClock(), a)
					if err != nil {
						return nil, nil, err
					}
					return textResult(out)
				})
			}},
	}
}

// refreshRun queues the refresh request, flushes the slot bodies and waits for a live payload.
func refreshRun(live Live, ch SlotChannel, clock func() time.Time, sleep func(time.Duration),
	a refreshArgs) (map[string]any, error) {

	timeout := time.Duration(a.TimeoutSeconds) * time.Second
	if a.TimeoutSeconds <= 0 {
		timeout = 20 * time.Second
	}
	timeout = min(timeout, 60*time.Second)
	kind := "refresh"
	if a.Full {
		kind = "refreshfull"
	}
	fields := map[string]any{}
	if len(a.Sections) > 0 {
		fields["sections"] = a.Sections
	}
	since := clock()
	id, err := ch.Request(kind, fields)
	if err != nil {
		return nil, err
	}
	if _, err := ch.Flush(true); err != nil {
		return nil, fmt.Errorf("request %d is queued but the slot bodies could not be written: %w", id, err)
	}
	at, fresh := LiveWait(live, "", since, timeout, clock, sleep)
	out := map[string]any{"fresh": fresh, "requestId": id}
	if !at.IsZero() {
		out["ageSeconds"] = int64(clock().Sub(at).Seconds())
	}
	if fresh {
		out["note"] = "a live payload arrived after the request; read it with wow_live"
	} else {
		out["note"] = fmt.Sprintf("no live payload arrived within %d s: the game may be loading, the addon may be off, or the player did not pass a slot poll yet (~15 s); ageSeconds is the age of the last payload, when there was one", int(timeout.Seconds()))
	}
	return out, nil
}

// liveSectionView reads one section of the live state with its per-section age (wow_live).
func liveSectionView(live Live, clock func() time.Time, a liveArgs) (map[string]any, error) {
	if a.Section == "" {
		return nil, errors.New("section is required, e.g. character or bags; wow_refresh asks the addon for fresh live data")
	}
	sections, at, ok := live.Get(a.Character)
	if !ok {
		who := "any character"
		if a.Character != "" {
			who = fmt.Sprintf("character %q", a.Character)
		}
		return nil, fmt.Errorf("no live data for %s yet: ask for it with wow_refresh (the addon streams it through screenshots)", who)
	}
	data, sectionAt, ok := live.Section(a.Character, a.Section)
	if !ok {
		names := make([]string, 0, len(sections))
		for name := range sections {
			names = append(names, name)
		}
		sort.Strings(names)
		return nil, fmt.Errorf("no live section %q. Sections received so far: %v. Ask for it with wow_refresh (sections: [%q])",
			a.Section, names, a.Section)
	}
	now := clock()
	out := map[string]any{
		"live":              true,
		"section":           a.Section,
		a.Section:           data,
		"ageSeconds":        int64(now.Sub(sectionAt).Seconds()),
		"payloadAgeSeconds": int64(now.Sub(at).Seconds()),
	}
	if a.Character != "" {
		out["character"] = a.Character
	}
	return out, nil
}
