// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 Piekasz (GitHub: Piekario). Author attribution must be kept: see NOTICE.

package economy

import (
	"sort"
	"strconv"
	"time"

	"wowmcp/internal/snapshot"
)

// View answers wow_economy from the parsed ProfiteerDB table.
func View(db map[string]any, q Query) map[string]any {
	r := Read(db, q)
	days := q.Days
	if days <= 0 {
		days = DefaultDays
	}
	out := map[string]any{
		"summary":    renderSummary(r),
		"ledger":     renderLedger(r, days),
		"goldCurve":  renderGold(r),
		"myAuctions": renderAuctions(r),
		"inventory":  renderInventories(r),
		"unlearned":  renderUnlearned(r),
	}
	if len(r.Realms) == 0 {
		out["note"] = "ProfiteerDB has no realm sets: Profiteer has never saved for this account."
	}
	return out
}

func renderSummary(r Report) map[string]any {
	realms := []any{}
	for _, info := range r.Realms {
		entry := map[string]any{"realm": info.Name, "characters": info.Chars, "pricesKnown": info.PricesKnown}
		if info.LastScan > 0 {
			entry["lastScan"] = formatUnix(info.LastScan)
		}
		realms = append(realms, entry)
	}
	out := map[string]any{"realms": realms, "sessions": r.Sessions}
	if r.LastScan > 0 {
		out["lastScan"] = formatUnix(r.LastScan)
	}
	return out
}

func renderLedger(r Report, days int) map[string]any {
	list := []any{}
	totals := map[string]int64{}
	var net int64
	for _, day := range r.Ledger {
		categories := map[string]any{}
		for category, copper := range day.Categories {
			categories[category] = copper
			totals[category] += copper
		}
		net += day.Net()
		list = append(list, map[string]any{"date": day.Date, "categories": categories, "net": day.Net()})
	}
	out := map[string]any{"days": list, "windowDays": days, "net": net}
	if r.LedgerAnchor != "" {
		out["newestDay"] = r.LedgerAnchor
	}
	byCategory := map[string]any{}
	for category, copper := range totals {
		byCategory[category] = copper
	}
	out["totals"] = byCategory
	return out
}

func renderGold(r Report) []any {
	out := []any{}
	for _, curve := range r.Gold {
		points := []any{}
		for _, p := range curve.Points {
			points = append(points, map[string]any{"day": p.Day, "start": p.Start, "end": p.End})
		}
		out = append(out, map[string]any{"realm": curve.Realm, "character": curve.Char, "points": points})
	}
	return out
}

func renderAuctions(r Report) []any {
	out := []any{}
	for _, list := range r.Auctions {
		items := []any{}
		for _, a := range list.Items {
			items = append(items, map[string]any{"name": a.Name, "count": a.Count, "ask": a.Ask, "sold": a.Sold})
		}
		entry := map[string]any{"realm": list.Realm, "character": list.Char, "items": items}
		if list.Time > 0 {
			entry["seen"] = formatUnix(list.Time)
		}
		out = append(out, entry)
	}
	return out
}

func renderInventories(r Report) []any {
	out := []any{}
	for _, inv := range r.Inventories {
		items := []any{}
		for _, item := range inv.Items {
			items = append(items, map[string]any{
				"id": item.ID, "name": item.Name, "bags": item.Bags, "bank": item.Bank,
				"unitPrice": item.UnitPrice, "priceSource": item.PriceSource, "value": item.Value,
			})
		}
		entry := map[string]any{
			"realm": inv.Realm, "character": inv.Char, "topItems": items,
			"itemsTotal": inv.ItemsTotal, "totalValue": inv.TotalValue,
		}
		if inv.Time > 0 {
			entry["scanned"] = formatUnix(inv.Time)
		}
		out = append(out, entry)
	}
	return out
}

func renderUnlearned(r Report) map[string]any {
	list := []any{}
	for _, recipe := range r.Unlearned {
		list = append(list, map[string]any{
			"realm": recipe.Realm, "character": recipe.Char,
			"profession": recipe.Profession, "name": recipe.Name, "id": recipe.ID,
		})
	}
	return map[string]any{"recipes": list, "shown": len(r.Unlearned), "total": r.UnlearnedTotal}
}

func formatUnix(unix int64) string {
	return time.Unix(unix, 0).Format("2006-01-02 15:04:05")
}

// CSV exports (wow_export_csv). Each returns a header row followed by data rows;
// the adapter writes the file, the domain only shapes the rows.

// PricesRows lists every scanned auction price with the shared vendor price and name.
func PricesRows(db map[string]any) [][]string {
	names, vendor := snapshot.AsMap(db["names"]), snapshot.AsMap(db["vendor"])
	rows := [][]string{{"realm", "itemId", "name", "vendorCopper", "minCopper", "refCopper", "qty", "listings", "scannedAt"}}
	for _, rs := range realmSets(db) {
		prices := snapshot.AsMap(rs.set["prices"])
		for _, key := range sortedKeys(prices) {
			entry := snapshot.AsMap(prices[key])
			id, err := strconv.ParseInt(key, 10, 64)
			if err != nil {
				continue
			}
			vendorCopper, _ := snapshot.AsInt(vendor[key])
			rows = append(rows, []string{
				rs.name, key, itemName(names, id), formatInt(vendorCopper),
				intField(entry, "min"), intField(entry, "ref"), intField(entry, "qty"),
				intField(entry, "n"), intField(entry, "time"),
			})
		}
	}
	return rows
}

// LedgerRows lists the whole gold ledger: one row per realm, day and category.
func LedgerRows(db map[string]any) [][]string {
	rows := [][]string{{"realm", "date", "category", "copper"}}
	for _, rs := range realmSets(db) {
		ledger := snapshot.AsMap(rs.set["ledger"])
		for _, day := range sortedKeys(ledger) {
			categories := snapshot.AsMap(ledger[day])
			for _, category := range sortedKeys(categories) {
				copper, _ := snapshot.AsInt(categories[category])
				rows = append(rows, []string{rs.name, day, category, formatInt(copper)})
			}
		}
	}
	return rows
}

// InventoryRows lists every scanned inventory item (all of them, not only the top ones).
func InventoryRows(db map[string]any, character string) [][]string {
	names := snapshot.AsMap(db["names"])
	vendor := snapshot.AsMap(db["vendor"])
	rows := [][]string{{"realm", "character", "itemId", "name", "bags", "bank", "unitPriceCopper", "priceSource", "valueCopper"}}
	for _, rs := range realmSets(db) {
		table := snapshot.AsMap(rs.set["inventory"])
		prices := snapshot.AsMap(rs.set["prices"])
		for _, key := range matchedChars(table, character) {
			entry := snapshot.AsMap(table[key])
			items := mergeCounts(snapshot.AsMap(entry["bags"]), snapshot.AsMap(entry["bank"]))
			for i := range items {
				items[i].Name = itemName(names, items[i].ID)
				items[i].UnitPrice, items[i].PriceSource = itemPrice(prices, vendor, items[i].ID)
				items[i].Value = (items[i].Bags + items[i].Bank) * items[i].UnitPrice
			}
			items = topByValue(items, len(items))
			for _, item := range items {
				rows = append(rows, []string{
					rs.name, key, formatInt(item.ID), item.Name, formatInt(item.Bags), formatInt(item.Bank),
					formatInt(item.UnitPrice), item.PriceSource, formatInt(item.Value),
				})
			}
		}
	}
	return rows
}

// HistoryRows lists every price-history snapshot of every item.
func HistoryRows(db map[string]any) [][]string {
	names := snapshot.AsMap(db["names"])
	rows := [][]string{{"realm", "itemId", "name", "time", "minCopper", "refCopper", "qty", "listings", "sold", "dtSeconds"}}
	for _, rs := range realmSets(db) {
		history := snapshot.AsMap(rs.set["history"])
		for _, key := range sortedKeys(history) {
			id, err := strconv.ParseInt(key, 10, 64)
			if err != nil {
				continue
			}
			for _, raw := range snapshot.AsList(history[key]) {
				entry := snapshot.AsMap(raw)
				rows = append(rows, []string{
					rs.name, key, itemName(names, id), intField(entry, "t"),
					intField(entry, "min"), intField(entry, "ref"), intField(entry, "qty"),
					intField(entry, "n"), floatField(entry, "sold"), intField(entry, "dt"),
				})
			}
		}
	}
	return rows
}

func sortedKeys(table map[string]any) []string {
	keys := make([]string, 0, len(table))
	for k := range table {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func formatInt(n int64) string { return strconv.FormatInt(n, 10) }

func intField(entry map[string]any, field string) string {
	n, ok := snapshot.AsInt(entry[field])
	if !ok {
		return ""
	}
	return formatInt(n)
}

func floatField(entry map[string]any, field string) string {
	f, ok := snapshot.AsFloat(entry[field])
	if !ok {
		return ""
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}
