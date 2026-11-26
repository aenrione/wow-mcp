// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 Piekasz (GitHub: Piekario). Author attribution must be kept: see NOTICE.

package economy

import (
	"sort"
	"strconv"

	"wowmcp/internal/game/charkey"
	"wowmcp/internal/snapshot"
)

// realmSet is one per-realm data set of ProfiteerDB.sets.
type realmSet struct {
	name string
	set  map[string]any
}

// realmSets lists the per-realm sets, sorted by realm name.
func realmSets(db map[string]any) []realmSet {
	var out []realmSet
	for name, raw := range snapshot.AsMap(db["sets"]) {
		if set := snapshot.AsMap(raw); set != nil {
			out = append(out, realmSet{name: name, set: set})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

// matchedChars are the character keys of one char-keyed table that name the character
// ("" = all), sorted.
func matchedChars(table map[string]any, character string) []string {
	keys := make([]string, 0, len(table))
	for k := range table {
		keys = append(keys, k)
	}
	return charkey.Find(keys, character)
}

// Read turns the parsed ProfiteerDB into a typed, bounded Report.
func Read(db map[string]any, q Query) Report {
	sets := realmSets(db)
	r := Report{}
	r.Sessions, _ = snapshot.AsInt(db["sessions"])
	for _, rs := range sets {
		r.Realms = append(r.Realms, readRealmInfo(rs))
		if scan, _ := snapshot.AsInt(rs.set["lastScan"]); scan > r.LastScan {
			r.LastScan = scan
		}
	}
	r.LedgerAnchor = newestLedgerDay(sets)
	since := cutoff(r.LedgerAnchor, q.Days)
	r.Ledger = readLedger(sets, since)
	r.Gold = readWorth(sets, q.Character, since)
	r.Auctions = readAuctions(sets, q.Character)
	r.Inventories = readInventories(db, sets, q.Character)
	r.Unlearned, r.UnlearnedTotal = readUnlearned(sets, q.Character)
	return r
}

func readRealmInfo(rs realmSet) RealmInfo {
	info := RealmInfo{Name: rs.name, PricesKnown: len(snapshot.AsMap(rs.set["prices"]))}
	info.LastScan, _ = snapshot.AsInt(rs.set["lastScan"])
	for key := range snapshot.AsMap(rs.set["chars"]) {
		info.Chars = append(info.Chars, key)
	}
	sort.Strings(info.Chars)
	return info
}

// newestLedgerDay is the latest YYYY-MM-DD key of any set's ledger.
func newestLedgerDay(sets []realmSet) string {
	newest := ""
	for _, rs := range sets {
		for day := range snapshot.AsMap(rs.set["ledger"]) {
			if day > newest {
				newest = day
			}
		}
	}
	return newest
}

// readLedger merges the per-realm ledgers into days (newest first), from since on.
func readLedger(sets []realmSet, since string) []LedgerDay {
	merged := map[string]map[string]int64{}
	for _, rs := range sets {
		for day, raw := range snapshot.AsMap(rs.set["ledger"]) {
			if day < since {
				continue
			}
			if merged[day] == nil {
				merged[day] = map[string]int64{}
			}
			for category, value := range snapshot.AsMap(raw) {
				if copper, ok := snapshot.AsInt(value); ok {
					merged[day][category] += copper
				}
			}
		}
	}
	var out []LedgerDay
	for day, categories := range merged {
		out = append(out, LedgerDay{Date: day, Categories: categories})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date > out[j].Date })
	return out
}

// readWorth reads the gold curve worth[day][charKey] = {s, e}, per realm and character.
func readWorth(sets []realmSet, character, since string) []GoldCurve {
	var out []GoldCurve
	for _, rs := range sets {
		byChar := map[string][]WorthPoint{}
		for day, raw := range snapshot.AsMap(rs.set["worth"]) {
			if day < since {
				continue
			}
			chars := snapshot.AsMap(raw)
			for _, key := range matchedChars(chars, character) {
				point := snapshot.AsMap(chars[key])
				p := WorthPoint{Day: day}
				p.Start, _ = snapshot.AsInt(point["s"])
				p.End, _ = snapshot.AsInt(point["e"])
				byChar[key] = append(byChar[key], p)
			}
		}
		for key, points := range byChar {
			sort.Slice(points, func(i, j int) bool { return points[i].Day < points[j].Day })
			out = append(out, GoldCurve{Realm: rs.name, Char: key, Points: points})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Realm+"|"+out[i].Char < out[j].Realm+"|"+out[j].Char
	})
	return out
}

// readAuctions reads myAuctions[charKey] = {t, items = {{name, n, ask, sold}}}.
func readAuctions(sets []realmSet, character string) []AuctionList {
	var out []AuctionList
	for _, rs := range sets {
		table := snapshot.AsMap(rs.set["myAuctions"])
		for _, key := range matchedChars(table, character) {
			entry := snapshot.AsMap(table[key])
			list := AuctionList{Realm: rs.name, Char: key}
			list.Time, _ = snapshot.AsInt(entry["t"])
			for _, raw := range snapshot.AsList(entry["items"]) {
				item := snapshot.AsMap(raw)
				a := Auction{}
				a.Name, _ = item["name"].(string)
				a.Count, _ = snapshot.AsInt(item["n"])
				a.Ask, _ = snapshot.AsInt(item["ask"])
				a.Sold, _ = item["sold"].(bool)
				list.Items = append(list.Items, a)
			}
			out = append(out, list)
		}
	}
	return out
}

// readInventories reads inventory[charKey] = {bags, bank, time} and values each item
// with the realm's auction price (prices[id].min) or the shared vendor price.
func readInventories(db map[string]any, sets []realmSet, character string) []Inventory {
	names, vendor := snapshot.AsMap(db["names"]), snapshot.AsMap(db["vendor"])
	var out []Inventory
	for _, rs := range sets {
		table := snapshot.AsMap(rs.set["inventory"])
		prices := snapshot.AsMap(rs.set["prices"])
		for _, key := range matchedChars(table, character) {
			entry := snapshot.AsMap(table[key])
			inv := Inventory{Realm: rs.name, Char: key}
			inv.Time, _ = snapshot.AsInt(entry["time"])
			items := mergeCounts(snapshot.AsMap(entry["bags"]), snapshot.AsMap(entry["bank"]))
			for _, item := range items {
				item.Name = itemName(names, item.ID)
				item.UnitPrice, item.PriceSource = itemPrice(prices, vendor, item.ID)
				item.Value = (item.Bags + item.Bank) * item.UnitPrice
				inv.TotalValue += item.Value
				inv.Items = append(inv.Items, item)
			}
			inv.ItemsTotal = len(inv.Items)
			inv.Items = topByValue(inv.Items, TopInventory)
			out = append(out, inv)
		}
	}
	return out
}

// mergeCounts joins the bags and bank counts ([itemID] = n, ids as decimal strings).
func mergeCounts(bags, bank map[string]any) []InvItem {
	byID := map[int64]*InvItem{}
	add := func(table map[string]any, intoBank bool) {
		for key, raw := range table {
			id, err := strconv.ParseInt(key, 10, 64)
			n, ok := snapshot.AsInt(raw)
			if err != nil || !ok {
				continue
			}
			if byID[id] == nil {
				byID[id] = &InvItem{ID: id}
			}
			if intoBank {
				byID[id].Bank += n
			} else {
				byID[id].Bags += n
			}
		}
	}
	add(bags, false)
	add(bank, true)
	var out []InvItem
	for _, item := range byID {
		out = append(out, *item)
	}
	return out
}

// itemName resolves an item id in the shared names table; "item <id>" when unknown.
func itemName(names map[string]any, id int64) string {
	key := strconv.FormatInt(id, 10)
	if name, _ := names[key].(string); name != "" {
		return name
	}
	return "item " + key
}

// itemPrice is the unit price: the realm's last scanned minimum, else the vendor price.
func itemPrice(prices, vendor map[string]any, id int64) (int64, string) {
	key := strconv.FormatInt(id, 10)
	if entry := snapshot.AsMap(prices[key]); entry != nil {
		if minPrice, ok := snapshot.AsInt(entry["min"]); ok && minPrice > 0 {
			return minPrice, "auction"
		}
	}
	if copper, ok := snapshot.AsInt(vendor[key]); ok && copper > 0 {
		return copper, "vendor"
	}
	return 0, "none"
}

// readUnlearned reads unlearned[charKey][profession] = {{name, id, ...}}, bounded.
func readUnlearned(sets []realmSet, character string) ([]Recipe, int) {
	var out []Recipe
	total := 0
	for _, rs := range sets {
		table := snapshot.AsMap(rs.set["unlearned"])
		for _, key := range matchedChars(table, character) {
			professions := snapshot.AsMap(table[key])
			names := make([]string, 0, len(professions))
			for prof := range professions {
				names = append(names, prof)
			}
			sort.Strings(names)
			for _, prof := range names {
				for _, raw := range snapshot.AsList(professions[prof]) {
					entry := snapshot.AsMap(raw)
					total++
					if len(out) >= MaxUnlearned {
						continue
					}
					r := Recipe{Realm: rs.name, Char: key, Profession: prof}
					r.Name, _ = entry["name"].(string)
					r.ID, _ = snapshot.AsInt(entry["id"])
					out = append(out, r)
				}
			}
		}
	}
	return out, total
}
