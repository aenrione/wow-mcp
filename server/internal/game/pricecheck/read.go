// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 Piekasz (GitHub: Piekario). Author attribution must be kept: see NOTICE.

package pricecheck

import (
	"sort"
	"strconv"
	"strings"

	"wowmcp/internal/snapshot"
)

// Resolve finds the item the query means in the shared names table. A numeric query is
// an item id; otherwise the name is matched case-insensitively: an exact name wins, and
// a substring with a single hit resolves too. More hits come back as matches (bounded).
func Resolve(db map[string]any, query string) (Match, []Match) {
	names := snapshot.AsMap(db["names"])
	q := strings.TrimSpace(query)
	if id, err := strconv.ParseInt(q, 10, 64); err == nil {
		name, _ := names[q].(string)
		return Match{ID: id, Name: name}, nil
	}
	low := strings.ToLower(q)
	var matches []Match
	for key, raw := range names {
		name, _ := raw.(string)
		if name == "" || !strings.Contains(strings.ToLower(name), low) {
			continue
		}
		id, err := strconv.ParseInt(key, 10, 64)
		if err != nil {
			continue
		}
		if strings.EqualFold(name, q) {
			return Match{ID: id, Name: name}, nil
		}
		matches = append(matches, Match{ID: id, Name: name})
	}
	sort.Slice(matches, func(i, j int) bool {
		if len(matches[i].Name) != len(matches[j].Name) {
			return len(matches[i].Name) < len(matches[j].Name)
		}
		return matches[i].Name < matches[j].Name
	})
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > MaxMatches {
		matches = matches[:MaxMatches]
	}
	return Match{}, matches
}

// Read gathers everything ProfiteerDB knows about one item id.
func Read(db map[string]any, id int64) Price {
	key := strconv.FormatInt(id, 10)
	p := Price{ID: id}
	names := snapshot.AsMap(db["names"])
	p.Name, _ = names[key].(string)
	p.Vendor, _ = snapshot.AsInt(snapshot.AsMap(db["vendor"])[key])
	for realm, raw := range snapshot.AsMap(db["sets"]) {
		set := snapshot.AsMap(raw)
		if entry := snapshot.AsMap(snapshot.AsMap(set["prices"])[key]); entry != nil {
			p.Current = append(p.Current, readPoint(realm, entry))
		}
		for _, rawRow := range snapshot.AsList(snapshot.AsMap(set["history"])[key]) {
			p.History = append(p.History, readHistoryRow(realm, snapshot.AsMap(rawRow)))
		}
	}
	sort.Slice(p.Current, func(i, j int) bool { return p.Current[i].Time > p.Current[j].Time })
	sort.Slice(p.History, func(i, j int) bool { return p.History[i].Time > p.History[j].Time })
	if len(p.History) > MaxHistory {
		p.History = p.History[:MaxHistory]
	}
	return p
}

func readPoint(realm string, entry map[string]any) PricePoint {
	point := PricePoint{Realm: realm}
	point.Min, _ = snapshot.AsInt(entry["min"])
	point.Ref, _ = snapshot.AsInt(entry["ref"])
	point.Qty, _ = snapshot.AsInt(entry["qty"])
	point.N, _ = snapshot.AsInt(entry["n"])
	point.Time, _ = snapshot.AsInt(entry["time"])
	return point
}

func readHistoryRow(realm string, entry map[string]any) HistoryRow {
	row := HistoryRow{Realm: realm}
	row.Min, _ = snapshot.AsInt(entry["min"])
	row.Ref, _ = snapshot.AsInt(entry["ref"])
	row.Qty, _ = snapshot.AsInt(entry["qty"])
	row.N, _ = snapshot.AsInt(entry["n"])
	row.Time, _ = snapshot.AsInt(entry["t"])
	if sold, ok := snapshot.AsFloat(entry["sold"]); ok {
		row.Sold, row.HasSold = sold, true
		row.Dt, _ = snapshot.AsInt(entry["dt"])
	}
	return row
}
