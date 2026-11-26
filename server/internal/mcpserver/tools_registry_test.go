// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 Piekasz (GitHub: Piekario). Author attribution must be kept: see NOTICE.

package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"wowmcp/internal/fixtures"
	"wowmcp/internal/storage"
)

func connect(t *testing.T, cfg Config) *mcp.ClientSession {
	t.Helper()
	server := New(cfg)
	ct, st := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := server.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "t"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func callText(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	return res.Content[len(res.Content)-1].(*mcp.TextContent).Text, res.IsError
}

// Every tool declares annotations in the one place that registers them: all are read-only, idempotent and closed-world,
// except wow_goal_set, which writes only the server's own goals file and may remove a goal.
func TestEveryToolHasAnnotations(t *testing.T) {
	cs := connect(t, Config{Name: "wow-mcp", Version: "test", Snapshots: &storage.Store{File: fixtures.Testdata("WowMcp_forever.lua")}, Freshness: fixtures.At(0)})
	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	writers := 0
	for _, tl := range tools.Tools {
		a := tl.Annotations
		if a == nil || a.Title == "" {
			t.Errorf("%s: missing annotations or title: %+v", tl.Name, a)
			continue
		}
		if a.OpenWorldHint == nil || *a.OpenWorldHint {
			t.Errorf("%s: OpenWorldHint must be false", tl.Name)
		}
		if !a.ReadOnlyHint {
			writers++
			if !expectedWriters[tl.Name] || a.DestructiveHint == nil || !*a.DestructiveHint {
				t.Errorf("%s: unexpected writer: %+v", tl.Name, a)
			}
			continue
		}
		if !a.IdempotentHint {
			t.Errorf("%s: read-only tools are idempotent", tl.Name)
		}
	}
	if writers != len(expectedWriters) {
		t.Errorf("%d tools are not read-only, want exactly %d", writers, len(expectedWriters))
	}
}

// expectedWriters are the tools that change something; everything else must be read-only.
// wow_goal_set, wow_export_csv and the wiki write only in the server's own directory;
// the remote tools write request-slot bodies the in-game addon polls.
var expectedWriters = map[string]bool{
	"wow_goal_set":        true,
	"wow_export_csv":      true,
	"wow_waypoint_set":    true,
	"wow_map_focus":       true,
	"wow_waypoints_clear": true,
	"wow_wiki_write":      true,
	"wow_refresh":         true,
	"wow_board_post":      true,
	"wow_board_clear":     true,
}

func TestPickPathAndLimit(t *testing.T) {
	value := map[string]any{"a": map[string]any{"list": []any{"x", "y", "z"}}, "b": 1}
	got, err := pickPath(value, "a.list.1")
	if err != nil || got != "y" {
		t.Fatalf("got %v, %v", got, err)
	}
	if _, err := pickPath(value, "a.nope"); err == nil || !strings.Contains(err.Error(), "list") {
		t.Errorf("missing key must list the keys: %v", err)
	}
	if _, err := pickPath(value, "a.list.9"); err == nil {
		t.Error("index out of range")
	}
	if _, err := pickPath(value, "b.c"); err == nil {
		t.Error("cannot descend into a scalar")
	}
	list, cut := limitValue([]any{1, 2, 3}, 2)
	if len(list.([]any)) != 2 || cut["total"] != 3 {
		t.Errorf("list: %v %v", list, cut)
	}
	m, cut := limitValue(map[string]any{"b": 1, "a": 2, "c": 3}, 2)
	if _, ok := m.(map[string]any)["a"]; !ok || len(m.(map[string]any)) != 2 || cut["total"] != 3 {
		t.Errorf("map: %v %v", m, cut)
	}
	if same, cut := limitValue([]any{1}, 5); cut != nil || len(same.([]any)) != 1 {
		t.Errorf("nothing to cut: %v", cut)
	}
	// A section whose only key is a long list (dungeons -> activities): the list one level down is cut too.
	nested, cut := limitValue(map[string]any{"activities": []any{1, 2, 3, 4}}, 2)
	lists, _ := cut["lists"].(map[string]any)
	if got := nested.(map[string]any)["activities"].([]any); len(got) != 2 || cut["total"] != 1 || lists["activities"] == nil {
		t.Errorf("nested list: %v %v", nested, cut)
	}
	if lists["activities"].(map[string]any)["total"] != 4 || cut["listsNote"] == nil {
		t.Errorf("nested totals: %v", cut)
	}
	if same, cut := limitValue(map[string]any{"a": []any{1}, "b": 2}, 5); cut != nil || len(same.(map[string]any)) != 2 {
		t.Errorf("nothing to cut in a map: %v", cut)
	}
}

func TestSectionPathAndLimitThroughTool(t *testing.T) {
	cs := connect(t, Config{Name: "wow-mcp", Version: "test", Snapshots: &storage.Store{File: fixtures.Testdata("WowMcp_forever.lua")}, Freshness: fixtures.At(0)})
	out, isErr := callText(t, cs, "wow_section", map[string]any{"section": "completedQuests", "limit": 2})
	if isErr || !strings.Contains(out, `"limited"`) || !strings.Contains(out, `"shown": 2`) {
		t.Errorf("limit: %s", out)
	}
	out, isErr = callText(t, cs, "wow_section", map[string]any{"section": "dungeons", "limit": 1})
	if isErr || !strings.Contains(out, `"lists"`) || !strings.Contains(out, `"activities"`) || !strings.Contains(out, `"shown": 1`) {
		t.Errorf("limit one level down: %s", out)
	}
	out, isErr = callText(t, cs, "wow_section", map[string]any{"section": "character", "path": "level"})
	if isErr || !strings.Contains(out, `"path": "level"`) {
		t.Errorf("path: %s", out)
	}
	out, isErr = callText(t, cs, "wow_section", map[string]any{"section": "character", "path": "nope"})
	if !isErr || !strings.Contains(out, "Keys there") {
		t.Errorf("bad path must explain itself: %s", out)
	}
}

func TestDoctorTool(t *testing.T) {
	dir := t.TempDir()
	history := storage.NewHistory(filepath.Join(dir, "history"), 5)
	store := &storage.Store{File: fixtures.Testdata("WowMcp_forever.lua")}
	cs := connect(t, Config{Name: "wow-mcp", Version: "test", Snapshots: store, History: history, HistoryDir: history.Dir, Freshness: fixtures.At(0)})
	out, isErr := callText(t, cs, "wow_doctor", nil)
	if isErr {
		t.Fatal(out)
	}
	var rep struct {
		Status, Summary, NextStep string
		Checks                    []struct{ Check, Status, Detail, Fix string }
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Summary == "" || rep.NextStep == "" || len(rep.Checks) < 10 || rep.Checks[0].Check != "dump file" || rep.Checks[0].Status != "ok" {
		t.Errorf("report: %+v", rep)
	}
	for _, c := range rep.Checks {
		if c.Check == "history" && (c.Status != "ok" || !strings.Contains(c.Detail, "writable")) {
			t.Errorf("history: %+v", c)
		}
	}
	if _, isErr := callText(t, cs, "wow_doctor", map[string]any{"character": "Nobody"}); !isErr {
		t.Error("an unknown character is an error")
	}
}

// A missing dump is a diagnosis, not a tool error.
func TestDoctorReportsAnUnreadableDump(t *testing.T) {
	cs := connect(t, Config{Name: "wow-mcp", Version: "test", Snapshots: &storage.Store{File: filepath.Join(t.TempDir(), "missing.lua")}, Freshness: fixtures.At(0)})
	out, isErr := callText(t, cs, "wow_doctor", nil)
	if isErr || !strings.Contains(out, `"status": "fail"`) || !strings.Contains(out, "--wow-dir") {
		t.Errorf("isErr=%v\n%s", isErr, out)
	}
}

func TestDoctorFlagsAnUnwritableHistory(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "history")
	if err := os.WriteFile(blocker, []byte("a file, not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	history := storage.NewHistory(blocker, 5)
	cs := connect(t, Config{Name: "wow-mcp", Version: "test", Snapshots: &storage.Store{File: fixtures.Testdata("WowMcp_forever.lua")}, History: history, HistoryDir: blocker, Freshness: fixtures.At(0)})
	out, _ := callText(t, cs, "wow_doctor", nil)
	if !strings.Contains(out, "cannot be written") {
		t.Errorf("%s", out)
	}
}

func TestStatusAndDoctorNameTheAccounts(t *testing.T) {
	wow := t.TempDir()
	src, err := os.ReadFile(fixtures.Testdata("WowMcp_forever.lua"))
	if err != nil {
		t.Fatal(err)
	}
	for _, acc := range []string{"ALPHA", "BETA"} {
		d := filepath.Join(wow, "_forever_", "WTF", "Account", acc, "SavedVariables")
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, storage.SavedVariablesFile), src, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cs := connect(t, Config{Name: "wow-mcp", Version: "test", Snapshots: &storage.Store{WowDir: wow, Account: "alpha"}, Freshness: fixtures.At(0)})
	status, _ := callText(t, cs, "wow_status", nil)
	for _, want := range []string{`"account": "ALPHA"`, `"accountPinned": true`, `"BETA"`} {
		if !strings.Contains(status, want) {
			t.Errorf("status without %s:\n%.800s", want, status)
		}
	}
	doc, _ := callText(t, cs, "wow_doctor", nil)
	if !strings.Contains(doc, "is pinned") || !strings.Contains(doc, "BETA") {
		t.Errorf("doctor: %.1500s", doc)
	}
}
