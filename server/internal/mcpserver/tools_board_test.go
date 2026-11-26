// SPDX-License-Identifier: GPL-3.0-or-later
// Forever Suite fork addition.

package mcpserver

import "testing"

func TestBoardEntryFields(t *testing.T) {
	ok, err := boardEntryFields(boardEntryArg{
		Category: "SELL", Title: "Dump greys", Body: "vendor these",
		Items: []int{2589, 0, 7005}, MapID: 1429, X: 44.2, Y: 61.1, WaypointTitle: "Sengir",
	})
	if err != nil {
		t.Fatalf("valid entry: %v", err)
	}
	if ok["category"] != "sell" || ok["title"] != "Dump greys" || ok["body"] != "vendor these" {
		t.Errorf("fields: %+v", ok)
	}
	items, _ := ok["items"].([]any)
	if len(items) != 2 || items[0] != 2589 || items[1] != 7005 {
		t.Errorf("items not filtered to positive ids: %+v", ok["items"])
	}
	coords, _ := ok["coords"].(map[string]any)
	if coords == nil || coords["mapID"] != 1429 || coords["title"] != "Sengir" {
		t.Errorf("coords: %+v", ok["coords"])
	}
}

func TestBoardEntryFieldsRejectsBadInput(t *testing.T) {
	if _, err := boardEntryFields(boardEntryArg{Category: "spam", Title: "x"}); err == nil {
		t.Error("an unknown category must be rejected")
	}
	if _, err := boardEntryFields(boardEntryArg{Category: "note", Title: "  "}); err == nil {
		t.Error("a blank title must be rejected")
	}
	if _, err := boardEntryFields(boardEntryArg{Category: "route", Title: "go", MapID: 1429, X: 150, Y: 10}); err == nil {
		t.Error("out-of-range coordinates must be rejected")
	}
	ok, err := boardEntryFields(boardEntryArg{Category: "note", Title: "plain"})
	if err != nil || ok["coords"] != nil || ok["items"] != nil || ok["body"] != nil {
		t.Errorf("a minimal entry carries only category and title: %+v (%v)", ok, err)
	}
}

func TestBoardToolsShape(t *testing.T) {
	tools := (&service{}).boardTools()
	writes := map[string]bool{}
	for _, tl := range tools {
		writes[tl.name] = tl.writes
	}
	for _, name := range []string{"wow_board_post", "wow_board_clear", "wow_board"} {
		if _, ok := writes[name]; !ok {
			t.Errorf("missing tool %s", name)
		}
	}
	if !writes["wow_board_post"] || !writes["wow_board_clear"] {
		t.Error("wow_board_post and wow_board_clear must be writers")
	}
	if writes["wow_board"] {
		t.Error("wow_board is a read tool")
	}
}
