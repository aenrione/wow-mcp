// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 Piekasz (GitHub: Piekario). Author attribution must be kept: see NOTICE.

package pricecheck

import (
	"strings"
	"testing"

	"wowmcp/internal/savedvars"
	"wowmcp/internal/snapshot"
)

const profiteerFixture = `
ProfiteerDB = {
["vendor"] = {
[2318] = 7,
},
["names"] = {
[2318] = "Light Leather",
[15347] = "Dark Hide",
[2319] = "Medium Leather",
[4234] = "Heavy Leather",
},
["sets"] = {
[""] = {
["prices"] = {
[15347] = {
["min"] = 1997,
["ref"] = 2196,
["qty"] = 7,
["n"] = 7,
["time"] = 1795622739,
},
},
["history"] = {
[15347] = {
{
["min"] = 1800,
["ref"] = 2000,
["qty"] = 9,
["n"] = 9,
["t"] = 1795500000,
},
{
["min"] = 1997,
["ref"] = 2196,
["qty"] = 7,
["n"] = 7,
["t"] = 1795622739,
["sold"] = 4.5,
["dt"] = 43200,
},
},
},
},
},
}
`

func parseDB(t *testing.T) map[string]any {
	t.Helper()
	vars, err := savedvars.Parse([]byte(profiteerFixture))
	if err != nil {
		t.Fatal(err)
	}
	return snapshot.AsMap(vars["ProfiteerDB"])
}

func TestResolve(t *testing.T) {
	db := parseDB(t)
	if m, _ := Resolve(db, "15347"); m.ID != 15347 || m.Name != "Dark Hide" {
		t.Errorf("numeric id: %+v", m)
	}
	if m, _ := Resolve(db, "dark hide"); m.ID != 15347 {
		t.Errorf("exact name, any case: %+v", m)
	}
	if m, _ := Resolve(db, "dark"); m.ID != 15347 {
		t.Errorf("single substring hit resolves: %+v", m)
	}
	m, matches := Resolve(db, "leather")
	if m.ID != 0 || len(matches) != 3 {
		t.Errorf("ambiguous fragment: %+v %v", m, matches)
	}
	if matches[0].Name != "Heavy Leather" { // shortest name first, ties alphabetically
		t.Errorf("match order: %v", matches)
	}
}

func TestViewPrice(t *testing.T) {
	out, err := View(parseDB(t), Query{Item: "Dark Hide"})
	if err != nil {
		t.Fatal(err)
	}
	current := out["current"].([]any)
	if len(current) != 1 || snapshot.AsMap(current[0])["min"] != int64(1997) {
		t.Errorf("current price: %v", current)
	}
	history := out["history"].([]any)
	if len(history) != 2 {
		t.Fatalf("history: %v", history)
	}
	newest := snapshot.AsMap(history[0])
	if newest["sold"] != 4.5 || newest["dtSeconds"] != int64(43200) {
		t.Errorf("newest first with sold: %v", newest)
	}
	// 4.5 sold over 43200 s = 9 per day
	if out["estimatedSoldPerDay"] != 9.0 {
		t.Errorf("soldPerDay: %v", out["estimatedSoldPerDay"])
	}
}

func TestViewAmbiguousAndMissing(t *testing.T) {
	db := parseDB(t)
	out, err := View(db, Query{Item: "leather"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out["matches"].([]any)) != 3 {
		t.Errorf("ambiguous answer: %v", out)
	}
	if _, err := View(db, Query{Item: "no such thing"}); err == nil || !strings.Contains(err.Error(), "no item") {
		t.Errorf("missing item: %v", err)
	}
	if _, err := View(db, Query{}); err == nil {
		t.Error("empty query should error")
	}
}

func TestHistoryBound(t *testing.T) {
	p := Price{}
	for i := 0; i < MaxHistory+5; i++ {
		p.History = append(p.History, HistoryRow{Time: int64(i)})
	}
	if _, ok := p.soldPerDay(); ok {
		t.Error("no sold data, no estimate")
	}
}
