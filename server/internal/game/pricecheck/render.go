// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 Piekasz (GitHub: Piekario). Author attribution must be kept: see NOTICE.

package pricecheck

import (
	"fmt"
	"math"
	"time"
)

// View answers wow_price from the parsed ProfiteerDB table.
func View(db map[string]any, q Query) (map[string]any, error) {
	if q.Item == "" {
		return nil, fmt.Errorf("give the item as a name, a name fragment or a numeric id")
	}
	match, matches := Resolve(db, q.Item)
	if match.ID == 0 {
		if len(matches) == 0 {
			return nil, fmt.Errorf("Profiteer knows no item matching %q; prices appear after scanning the auction house in game", q.Item)
		}
		list := []any{}
		for _, m := range matches {
			list = append(list, map[string]any{"id": m.ID, "name": m.Name})
		}
		return map[string]any{
			"query": q.Item, "matches": list,
			"note": "Several items match; ask again with one of these names or ids.",
		}, nil
	}
	return renderPrice(Read(db, match.ID), q.Item), nil
}

func renderPrice(p Price, query string) map[string]any {
	out := map[string]any{"query": query, "itemId": p.ID, "name": p.Name}
	if p.Vendor > 0 {
		out["vendorPrice"] = p.Vendor
	}
	current := []any{}
	for _, point := range p.Current {
		current = append(current, map[string]any{
			"realm": point.Realm, "min": point.Min, "ref": point.Ref,
			"qty": point.Qty, "listings": point.N, "scanned": formatUnix(point.Time),
		})
	}
	out["current"] = current
	history := []any{}
	for _, row := range p.History {
		entry := map[string]any{
			"realm": row.Realm, "min": row.Min, "ref": row.Ref,
			"qty": row.Qty, "listings": row.N, "time": formatUnix(row.Time),
		}
		if row.HasSold {
			entry["sold"] = row.Sold
			entry["dtSeconds"] = row.Dt
		}
		history = append(history, entry)
	}
	out["history"] = history
	if perDay, ok := p.soldPerDay(); ok {
		out["estimatedSoldPerDay"] = math.Round(perDay*10) / 10
		out["soldNote"] = "Sales cannot be told apart from cancellations, so this is a rough, slightly high estimate."
	} else if len(p.History) > 0 {
		out["soldNote"] = "Not enough observed time between scans to estimate sales per day."
	}
	if len(current) == 0 && p.Vendor == 0 {
		out["note"] = "No price saved for this item yet: scan the auction house in game."
	}
	return out
}

func formatUnix(unix int64) string {
	if unix == 0 {
		return ""
	}
	return time.Unix(unix, 0).Format("2006-01-02 15:04:05")
}
