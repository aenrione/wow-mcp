// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 Piekasz (GitHub: Piekario). Author attribution must be kept: see NOTICE.

package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"wowmcp/internal/fixtures"
	"wowmcp/internal/game/addon"
	"wowmcp/internal/snapshot"
	"wowmcp/internal/storage"
)

func TestStaleWarningLine(t *testing.T) {
	meta := fixtures.At(3*time.Hour+20*time.Minute).SnapshotMeta("Piekasz", fixtures.Forever(t))
	res, _, err := textResult(map[string]any{"snapshot": meta})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Content) != 2 || !strings.HasPrefix(res.Content[0].(*mcp.TextContent).Text, "WARNING:") {
		t.Errorf("the warning is not the first line of the response: %#v", res.Content)
	}
}

// writeDump writes a snapshot in the SavedVariables format (JSON rewritten into Lua by a simple encoder is enough).
func writeDump(t *testing.T, path string, s snapshot.Snapshot) {
	t.Helper()
	var b strings.Builder
	b.WriteString("WowMcpDB = ")
	encodeLua(&b, map[string]any{"last": "Piekasz", "characters": map[string]any{"Piekasz": map[string]any(s)}})
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func encodeLua(b *strings.Builder, v any) {
	switch x := v.(type) {
	case map[string]any:
		b.WriteString("{\n")
		for k, item := range x {
			js, _ := json.Marshal(k)
			b.WriteString("[" + string(js) + "] = ")
			encodeLua(b, item)
			b.WriteString(",\n")
		}
		b.WriteString("}")
	case []any:
		b.WriteString("{\n")
		for _, item := range x {
			encodeLua(b, item)
			b.WriteString(",\n")
		}
		b.WriteString("}")
	case nil:
		b.WriteString("nil")
	default:
		js, _ := json.Marshal(x)
		b.Write(js)
	}
}

// TestServerEndToEnd connects an MCP client to an in-memory server and checks the tools and prompts.
func TestServerEndToEnd(t *testing.T) {
	dir := t.TempDir()
	dump := filepath.Join(dir, storage.SavedVariablesFile)
	history := &storage.History{Dir: filepath.Join(dir, "history"), Keep: 10}
	store := &storage.Store{File: dump}

	writeDump(t, dump, fixtures.Forever(t))
	first, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	history.Record(first)
	writeDump(t, dump, fixtures.Later(t))

	server := New(Config{Name: "wow-mcp", Version: "test", Snapshots: store, History: history,
		HistoryDir: history.Dir, Freshness: fixtures.At(5 * time.Hour)})
	st, ct := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := server.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	call := func(name string, args map[string]any) string {
		t.Helper()
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var parts []string
		for _, c := range res.Content {
			parts = append(parts, c.(*mcp.TextContent).Text)
		}
		text := strings.Join(parts, "\n")
		if res.IsError {
			t.Fatalf("%s: tool error: %s", name, text)
		}
		return text
	}

	changes := call("wow_changes", nil)
	for _, want := range []string{"+1g 0s 0c", "Death Cap", "WANTED: Murkdeep!", "WARNING:"} {
		if !strings.Contains(changes, want) {
			t.Errorf("wow_changes without %q:\n%s", want, changes)
		}
	}
	if out := call("wow_vendor", map[string]any{"query": "arrow"}); !strings.Contains(out, "Sharp Arrow") || strings.Contains(out, "Laminated") {
		t.Errorf("wow_vendor affordable:\n%s", out)
	}
	if out := call("wow_bank", map[string]any{"sellable": true}); !strings.Contains(out, "totalVendorValue") {
		t.Errorf("wow_bank:\n%s", out)
	}
	if out := call("wow_reputation", nil); !strings.Contains(out, "Darnassus") {
		t.Errorf("wow_reputation:\n%s", out)
	}
	if out := call("wow_compare_item", map[string]any{"item": "Laminated Recurve Bow"}); !strings.Contains(out, "mainHand") {
		t.Errorf("wow_compare_item:\n%s", out)
	}

	newTools := map[string]struct {
		args map[string]any
		want []string
	}{
		"wow_quest_plan":    {nil, []string{"Darkshore", `"difficulty"`, `"log"`}},
		"wow_progress":      {nil, []string{`"xpGained": 20600`, "nextLevel", "play time from /played"}},
		"wow_recipes":       {map[string]any{"profession": "Leatherworking"}, []string{"Embossed Leather Gloves", "Coarse Thread 2"}},
		"wow_mail":          {nil, []string{"Auction House", "Linen Cloth x20"}},
		"wow_cooldowns":     {map[string]any{"all": true}, []string{"Hearthstone", `"character": "Piekasz"`}},
		"wow_account":       {nil, []string{"totalMoney", "Piekasz"}},
		"wow_find_item":     {map[string]any{"item": "Linen Cloth"}, []string{`"where": "mail"`, `"total": 54`}},
		"wow_character":     {nil, []string{"Honor Points", "playedTime"}},
		"wow_bags":          {nil, []string{"ammo"}},
		"wow_equipment":     {nil, []string{"overallPercent", "lastRepairQuote"}},
		"wow_talents":       {nil, []string{"canBuyNow", "Efficiency"}},
		"wow_auctions":      {map[string]any{"mine": true}, []string{"sellOnAH", "Light Leather"}},
		"wow_group":         {map[string]any{"player": "Tankadin"}, []string{"Curvewood Dagger"}},
		"wow_dungeons":      {map[string]any{"all": true}, []string{"Wailing Caverns", "lockouts"}},
		"wow_export":        {map[string]any{"format": "forum"}, []string{"[b]Talents:[/b]"}},
		"wow_search":        {map[string]any{"query": "death cap"}, []string{`"totalHits"`, "Cave Mushrooms", `"section": "bags"`}},
		"wow_rested":        {nil, []string{"classic rule, approximate", `"restedXp": 4000`, `"coversRestOfLevel"`}},
		"wow_gold_history":  {map[string]any{"days": 3}, []string{`"totalChange": "+1g 0s 0c"`, `"biggestGains"`}},
		"wow_leftovers":     {nil, []string{`"questItems": 0`}},
		"wow_ammo":          {map[string]any{"hours": 3}, []string{"Sharp Arrow", `"total": 812`, `"consumption"`}},
		"wow_quest_rewards": {nil, []string{"Curvewood Dagger", `"best"`, `"scoring"`}},
		"wow_shopping_list": {map[string]any{"hours": 2}, []string{`"budget"`, `"returnToTown"`, "Disengage"}},
		"wow_status": {nil, []string{`"serverVersion": "test"`, `"expectedAddonVersion": "` + addon.ExpectedVersion + `"`, `"addonVersion": "` + addon.ExpectedVersion + `"`, `"dumpTimeMs": ` + dumpTimeMs(t),
			`"alerts"`, `"note"`, "I want Deadmines today"}},
	}
	for name, tc := range newTools {
		out := call(name, tc.args)
		for _, w := range tc.want {
			if !strings.Contains(out, w) {
				t.Errorf("%s without %s:\n%.1500s", name, w, out)
			}
		}
	}

	listed, err := cs.ListPrompts(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Prompts) != len(prompts) {
		t.Errorf("prompts: %d", len(listed.Prompts))
	}
	for _, p := range prompts {
		res, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: p.name})
		if err != nil {
			t.Fatalf("%s: %v", p.name, err)
		}
		text := res.Messages[0].Content.(*mcp.TextContent).Text
		if !strings.Contains(text, "```json") || !strings.HasPrefix(text, "WARNING:") {
			t.Errorf("%s: bad body:\n%.300s", p.name, text)
		}
	}
	for name, wants := range map[string][]string{
		"before_logout": {"bindLocation", "Auberdine", "classic rule, approximate", "myAuctions"},
		"first_run":     {"windows", `"captured": true`, "/mcp reload", "expectedAddonVersion"},
		"pet_care":      {"Growl Rank 3", `"exists": true`, "foodNote"},
	} {
		res, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: name})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		body := res.Messages[0].Content.(*mcp.TextContent).Text
		for _, w := range wants {
			if !strings.Contains(body, w) {
				t.Errorf("prompt %s without %q:\n%.800s", name, w, body)
			}
		}
	}
	plan, _ := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: "session_plan"})
	text := plan.Messages[0].Content.(*mcp.TextContent).Text
	for _, want := range []string{"Cave Mushrooms", "Disengage", "sinceLastSnapshot", "playerNote", "I want Deadmines today", "goal of this session"} {
		if !strings.Contains(text, want) {
			t.Errorf("session_plan without %q", want)
		}
	}
}

// The server instructions must mention every tool and every prompt, so they do not drift from the registry.
func TestInstructionsListEverything(t *testing.T) {
	svc := &service{Config: Config{Freshness: fixtures.RealTime()}}
	for _, tl := range svc.tools() {
		if !strings.Contains(instructions, tl.name) {
			t.Errorf("the instructions do not mention tool %s", tl.name)
		}
	}
	for _, name := range PromptNames() {
		if !strings.Contains(instructions, name) {
			t.Errorf("the instructions do not mention prompt %s", name)
		}
	}
}

// The WARNING line concerns stale data only: a note about the export length must not imitate or obscure it.
func TestLengthNoteIsNotStaleWarning(t *testing.T) {
	res, _, err := textResult(map[string]any{"lengthNote": "The text is over 2000 characters"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Content) != 1 {
		t.Errorf("lengthNote is not a stale-data warning: %#v", res.Content)
	}
	stale := map[string]any{"lengthNote": "long text", "snapshot": map[string]any{"warning": "The data is 5 h old."}}
	if got := staleWarning(stale); got != "The data is 5 h old." {
		t.Errorf("staleWarning = %q", got)
	}
}

// A corrupt goals file: wow_goals returns an error with the path, and wow_goal_set does not overwrite the file.
func TestCorruptGoalsFileIsReportedNotOverwritten(t *testing.T) {
	dir := t.TempDir()
	goalsPath := filepath.Join(dir, "goals.json")
	if err := os.WriteFile(goalsPath, []byte("{this is not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	dump := filepath.Join(dir, storage.SavedVariablesFile)
	writeDump(t, dump, fixtures.Forever(t))
	server := New(Config{Name: "wow-mcp", Version: "test", Snapshots: &storage.Store{File: dump}, Freshness: fixtures.At(0),
		Goals: storage.OwnFile{Path: goalsPath}, GoalsFile: goalsPath})
	st, ct := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := server.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	for name, args := range map[string]map[string]any{"wow_goals": nil, "wow_goal_set": {"type": "level", "target": 30}} {
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil || !res.IsError {
			t.Fatalf("%s: expected a tool error: %v %v", name, res, err)
		}
		if text := res.Content[0].(*mcp.TextContent).Text; !(strings.Contains(text, goalsPath) || strings.Contains(text, strconv.Quote(goalsPath))) || !strings.Contains(text, "corrupt") {
			t.Errorf("%s: error without the path and a diagnosis: %s", name, text)
		}
	}
	if data, _ := os.ReadFile(goalsPath); string(data) != "{this is not json" {
		t.Errorf("the corrupt file was overwritten: %q", data)
	}
	// The writing tools have no read-only marker; the others do.
	tools, _ := cs.ListTools(ctx, nil)
	for _, tl := range tools.Tools {
		if expectedWriters[tl.Name] == tl.Annotations.ReadOnlyHint {
			t.Errorf("%s: ReadOnlyHint = %v", tl.Name, tl.Annotations.ReadOnlyHint)
		}
	}
}

// dumpTimeMs returns the dump time from the Forever fixture as the JSON response shows it.
func dumpTimeMs(t *testing.T) string {
	t.Helper()
	ms, ok := addon.DumpTimeMs(fixtures.Forever(t))
	if !ok {
		t.Fatal("the fixture has no timings.totalMs")
	}
	return strconv.FormatFloat(ms, 'f', -1, 64)
}
