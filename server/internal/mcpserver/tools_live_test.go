// SPDX-License-Identifier: GPL-3.0-or-later
// Forever Suite fork addition.

package mcpserver

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"wowmcp/internal/strip"
)

// The live state kept by the screenshot watcher satisfies the Live port.
var _ Live = (*strip.LiveStore)(nil)

// fakeLive is a Live whose payload can arrive mid-wait.
type fakeLive struct {
	sections map[string]any
	secAt    map[string]time.Time
	at       time.Time
	ok       bool
}

func (f *fakeLive) Get(string) (map[string]any, time.Time, bool) {
	if !f.ok {
		return nil, time.Time{}, false
	}
	return f.sections, f.at, true
}

func (f *fakeLive) Section(_, section string) (any, time.Time, bool) {
	if !f.ok {
		return nil, time.Time{}, false
	}
	data, ok := f.sections[section]
	return data, f.secAt[section], ok
}

// fakeChannel records the one request wow_refresh queues.
type fakeChannel struct {
	kind    string
	fields  map[string]any
	flushed bool
	reqErr  error
}

func (f *fakeChannel) Request(kind string, fields map[string]any) (int64, error) {
	f.kind, f.fields = kind, fields
	return 42, f.reqErr
}
func (f *fakeChannel) Flush(force bool) (bool, error) { f.flushed = force; return true, nil }
func (f *fakeChannel) Pending() (int, error)          { return 0, nil }
func (f *fakeChannel) LastWrite() time.Time           { return time.Time{} }
func (f *fakeChannel) Installed() (int, error)        { return 1, nil }

// stepClock advances only when the poll loop sleeps.
type stepClock struct {
	now     time.Time
	onSleep func(now time.Time)
}

func (c *stepClock) clock() time.Time { return c.now }
func (c *stepClock) sleep(d time.Duration) {
	c.now = c.now.Add(d)
	if c.onSleep != nil {
		c.onSleep(c.now)
	}
}

func TestRefreshRunFresh(t *testing.T) {
	t0 := time.Unix(1_764_000_000, 0)
	live := &fakeLive{}
	ch := &fakeChannel{}
	c := &stepClock{now: t0}
	c.onSleep = func(now time.Time) { // the payload lands one poll after the request
		live.ok, live.at = true, now
	}
	out, err := refreshRun(live, ch, c.clock, c.sleep, refreshArgs{Sections: []string{"bags"}, Full: true})
	if err != nil {
		t.Fatal(err)
	}
	if out["fresh"] != true || out["ageSeconds"] != int64(0) || out["requestId"] != int64(42) {
		t.Fatalf("out %v", out)
	}
	if ch.kind != "refreshfull" || !ch.flushed {
		t.Fatalf("kind %q flushed %v", ch.kind, ch.flushed)
	}
	if sections, _ := ch.fields["sections"].([]string); len(sections) != 1 || sections[0] != "bags" {
		t.Fatalf("fields %v", ch.fields)
	}
}

func TestRefreshRunTimesOut(t *testing.T) {
	t0 := time.Unix(1_764_000_000, 0)
	live := &fakeLive{ok: true, at: t0.Add(-time.Hour)} // only an old payload exists
	c := &stepClock{now: t0}
	ch := &fakeChannel{}
	out, err := refreshRun(live, ch, c.clock, c.sleep, refreshArgs{})
	if err != nil {
		t.Fatal(err)
	}
	if out["fresh"] != false {
		t.Fatalf("out %v", out)
	}
	if ch.kind != "refresh" || len(ch.fields) != 0 {
		t.Fatalf("kind %q fields %v", ch.kind, ch.fields)
	}
	if age := out["ageSeconds"].(int64); age < 3600 {
		t.Fatalf("ageSeconds %d must describe the stale payload", age)
	}
	if elapsed := c.now.Sub(t0); elapsed < 20*time.Second || elapsed > 21*time.Second {
		t.Fatalf("the default wait is 20 s, waited %v", elapsed)
	}

	c = &stepClock{now: t0}
	out, err = refreshRun(&fakeLive{}, ch, c.clock, c.sleep, refreshArgs{TimeoutSeconds: 999})
	if err != nil {
		t.Fatal(err)
	}
	if _, hasAge := out["ageSeconds"]; hasAge {
		t.Fatalf("no payload ever: out %v", out)
	}
	if elapsed := c.now.Sub(t0); elapsed > 61*time.Second {
		t.Fatalf("the wait is capped at 60 s, waited %v", elapsed)
	}
}

func TestLiveSectionView(t *testing.T) {
	t0 := time.Unix(1_764_000_000, 0)
	live := &fakeLive{
		ok:       true,
		at:       t0,
		sections: map[string]any{"bags": map[string]any{"free": 3}, "character": map[string]any{"level": 12}},
		secAt:    map[string]time.Time{"bags": t0, "character": t0.Add(-30 * time.Second)},
	}
	clock := func() time.Time { return t0.Add(5 * time.Second) }

	out, err := liveSectionView(live, clock, liveArgs{Section: "character", Character: "Monjits-Gpt"})
	if err != nil {
		t.Fatal(err)
	}
	if out["ageSeconds"] != int64(35) || out["payloadAgeSeconds"] != int64(5) {
		t.Fatalf("out %v", out)
	}
	if out["character"] != "Monjits-Gpt" || out["section"] != "character" || out["live"] != true {
		t.Fatalf("out %v", out)
	}

	if _, err := liveSectionView(live, clock, liveArgs{Section: "mail"}); err == nil ||
		!strings.Contains(err.Error(), "bags") || !strings.Contains(err.Error(), "wow_refresh") {
		t.Fatalf("a missing section must list the known ones and point at wow_refresh: %v", err)
	}
	if _, err := liveSectionView(live, clock, liveArgs{}); err == nil {
		t.Fatal("section is required")
	}
	if _, err := liveSectionView(&fakeLive{}, clock, liveArgs{Section: "bags"}); err == nil ||
		!strings.Contains(err.Error(), "wow_refresh") {
		t.Fatalf("no data yet must point at wow_refresh: %v", err)
	}
}

// Unwired (the orchestrator has not rewired livePort yet) the tools answer with the stable
// "off" errors, the same shape the other forever-suite tools have in the golden scenario.
func TestLiveToolsOffErrors(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "t"}, nil)
	svc := &service{Config: Config{Name: "t", Version: "t"}}
	for _, tl := range svc.liveTools() {
		tl.register(server)
	}
	ct, st := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := server.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "t"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	for name, args := range map[string]map[string]any{
		"wow_refresh": {"timeoutSeconds": 1, "sections": []any{"bags"}, "full": true},
		"wow_live":    {"section": "bags"},
	} {
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		text := res.Content[len(res.Content)-1].(*mcp.TextContent).Text
		if !res.IsError || !strings.Contains(text, "live channel is off") {
			t.Errorf("%s unwired: isError %v text %q", name, res.IsError, text)
		}
	}
}
