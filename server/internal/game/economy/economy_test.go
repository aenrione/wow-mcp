// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 Piekasz (GitHub: Piekario). Author attribution must be kept: see NOTICE.

package economy

import (
	"testing"

	"wowmcp/internal/savedvars"
	"wowmcp/internal/snapshot"
)

// profiteerFixture mirrors a real Profiteer.lua: names and vendor shared at the top
// level, everything else per realm inside sets (here the "" realm, as on Forever).
const profiteerFixture = `
ProfiteerDB = {
["sessions"] = 27,
["vendor"] = {
[2318] = 7,
},
["names"] = {
[2318] = "Light Leather",
[15347] = "Dark Hide",
},
["sets"] = {
[""] = {
["lastScan"] = 1795622739,
["prices"] = {
[15347] = {
["min"] = 1997,
["ref"] = 2196,
["qty"] = 7,
["n"] = 7,
["time"] = 1795622739,
},
},
["chars"] = {
["Monjits Gpt-"] = {
["seen"] = 1795630800,
["class"] = "ROGUE",
},
},
["ledger"] = {
["2026-11-25"] = {
["vendorSales"] = 5080,
["repairs"] = -186,
},
["2026-11-24"] = {
["quests"] = 600,
},
["2026-10-01"] = {
["loot"] = 999999,
},
},
["worth"] = {
["2026-11-25"] = {
["Monjits Gpt-"] = {
["s"] = 22237,
["e"] = 3282,
},
},
["2026-10-01"] = {
["Monjits Gpt-"] = {
["s"] = 1,
["e"] = 2,
},
},
},
["myAuctions"] = {
["Monjits Gpt-"] = {
["t"] = 1795600000,
["items"] = {
{
["name"] = "Fine Leather Belt",
["n"] = 1,
["ask"] = 299,
["sold"] = true,
},
},
},
},
["inventory"] = {
["Monjits Gpt-"] = {
["time"] = 1795630802,
["bags"] = {
[2318] = 5,
[15347] = 2,
},
["bank"] = {
[15347] = 3,
},
},
},
["unlearned"] = {
["Monjits Gpt-"] = {
["Leatherworking"] = {
{
["name"] = "Chromatic Cloak",
["id"] = 227869,
},
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

func TestReadWindow(t *testing.T) {
	r := Read(parseDB(t), Query{Days: 14})
	if r.LedgerAnchor != "2026-11-25" {
		t.Fatalf("anchor: %q", r.LedgerAnchor)
	}
	if len(r.Ledger) != 2 { // 2026-10-01 is outside the 14-day window
		t.Errorf("ledger days: %+v", r.Ledger)
	}
	if r.Ledger[0].Date != "2026-11-25" || r.Ledger[0].Net() != 5080-186 {
		t.Errorf("newest day first with net: %+v", r.Ledger[0])
	}
	if len(r.Gold) != 1 || len(r.Gold[0].Points) != 1 || r.Gold[0].Points[0].End != 3282 {
		t.Errorf("gold curve window: %+v", r.Gold)
	}
}

func TestReadCharacterData(t *testing.T) {
	r := Read(parseDB(t), Query{Character: "Monjits-Gpt"})
	if len(r.Auctions) != 1 || r.Auctions[0].Items[0].Ask != 299 || !r.Auctions[0].Items[0].Sold {
		t.Errorf("auctions: %+v", r.Auctions)
	}
	if len(r.Inventories) != 1 {
		t.Fatalf("inventories: %+v", r.Inventories)
	}
	inv := r.Inventories[0]
	if inv.ItemsTotal != 2 || len(inv.Items) != 2 {
		t.Fatalf("inventory items: %+v", inv)
	}
	// Dark Hide: 2 bags + 3 bank at the scanned min 1997 = 9985; Light Leather: 5 * vendor 7 = 35.
	if inv.Items[0].Name != "Dark Hide" || inv.Items[0].Value != 5*1997 || inv.Items[0].PriceSource != "auction" {
		t.Errorf("top item by value: %+v", inv.Items[0])
	}
	if inv.Items[1].Name != "Light Leather" || inv.Items[1].Value != 35 || inv.Items[1].PriceSource != "vendor" {
		t.Errorf("vendor-priced item: %+v", inv.Items[1])
	}
	if inv.TotalValue != 5*1997+35 {
		t.Errorf("total value: %d", inv.TotalValue)
	}
	if len(r.Unlearned) != 1 || r.Unlearned[0].Profession != "Leatherworking" || r.UnlearnedTotal != 1 {
		t.Errorf("unlearned: %+v", r.Unlearned)
	}
}

func TestReadUnknownCharacterMatchesNothing(t *testing.T) {
	r := Read(parseDB(t), Query{Character: "Nobody-Here"})
	if len(r.Inventories) != 0 || len(r.Auctions) != 0 || len(r.Gold) != 0 {
		t.Errorf("unknown character should match nothing: %+v", r)
	}
}

func TestView(t *testing.T) {
	out := View(parseDB(t), Query{})
	summary := snapshot.AsMap(out["summary"])
	if summary["sessions"] != int64(27) {
		t.Errorf("sessions: %v", summary["sessions"])
	}
	realms := summary["realms"].([]any)
	if len(realms) != 1 || snapshot.AsMap(realms[0])["pricesKnown"] != 1 {
		t.Errorf("realms: %v", realms)
	}
	ledger := snapshot.AsMap(out["ledger"])
	if ledger["net"] != int64(5080-186+600) {
		t.Errorf("ledger net: %v", ledger["net"])
	}
}

func TestCSVRows(t *testing.T) {
	db := parseDB(t)
	prices := PricesRows(db)
	if len(prices) != 2 || prices[1][2] != "Dark Hide" || prices[1][4] != "1997" {
		t.Errorf("prices rows: %v", prices)
	}
	ledger := LedgerRows(db)
	if len(ledger) != 5 { // header + 2 categories on 11-25 + 1 on 11-24 + 1 on 10-01
		t.Errorf("ledger rows: %v", ledger)
	}
	inventory := InventoryRows(db, "Monjits-Gpt")
	if len(inventory) != 3 || inventory[1][3] != "Dark Hide" {
		t.Errorf("inventory rows: %v", inventory)
	}
	history := HistoryRows(db)
	if len(history) != 1 { // header only: the fixture has no history
		t.Errorf("history rows: %v", history)
	}
}

func TestCutoff(t *testing.T) {
	if got := cutoff("2026-11-25", 14); got != "2026-11-12" {
		t.Errorf("cutoff: %q", got)
	}
	if got := cutoff("", 14); got != "" {
		t.Errorf("no anchor should disable the filter: %q", got)
	}
}
