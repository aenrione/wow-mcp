// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 Piekasz (GitHub: Piekario). Author attribution must be kept: see NOTICE.

// Package pricecheck answers "what is this item worth" from Profiteer's SavedVariables:
// the vendor price, the last scanned auction price per realm, the price history and an
// estimated sales rate (both bounded the way the addon itself keeps them).
package pricecheck

// MaxMatches bounds the fuzzy name matches offered when the query is ambiguous.
const MaxMatches = 10

// MaxHistory bounds the history rows per item (the addon itself keeps at most 30).
const MaxHistory = 30

// minSoldSpan is the least observed time (seconds) before a sold-per-day estimate is given,
// the same 6 h threshold Profiteer uses.
const minSoldSpan = 6 * 3600

// Query holds the wow_price argument.
type Query struct {
	Item string `json:"item" jsonschema:"item name, name fragment or numeric item id"`
}

// Match is one item the query could mean.
type Match struct {
	ID   int64
	Name string
}

// PricePoint is the last scanned price of the item on one realm.
type PricePoint struct {
	Realm                  string
	Min, Ref, Qty, N, Time int64
}

// HistoryRow is one price-history snapshot: sold units (estimated) and the interval are
// present only when the previous scan was close enough to compare.
type HistoryRow struct {
	Realm                  string
	Min, Ref, Qty, N, Time int64
	Sold                   float64
	Dt                     int64
	HasSold                bool
}

// Price is everything known about one item.
type Price struct {
	ID      int64
	Name    string
	Vendor  int64 // copper per unit at a vendor; 0 = never seen at one
	Current []PricePoint
	History []HistoryRow
}

// soldPerDay estimates units sold per day the way Profiteer does: the summed sold
// estimates over the summed observed intervals, only with at least minSoldSpan observed.
func (p Price) soldPerDay() (float64, bool) {
	var sold float64
	var span int64
	for _, row := range p.History {
		if row.HasSold && row.Dt > 0 {
			sold += row.Sold
			span += row.Dt
		}
	}
	if span < minSoldSpan {
		return 0, false
	}
	return sold / float64(span) * 86400, true
}
