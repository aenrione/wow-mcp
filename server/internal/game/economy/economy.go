// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 Piekasz (GitHub: Piekario). Author attribution must be kept: see NOTICE.

// Package economy reads the account economy Profiteer keeps in its SavedVariables
// (ProfiteerDB): the gold ledger by day and category, the gold curve per character,
// own auctions, scanned inventories and unlearned recipes. Prices and names are shared
// account-wide; everything else lives per realm set (ProfiteerDB.sets[realm]).
package economy

import (
	"sort"
	"time"
)

const (
	// DefaultDays is the default ledger and gold-curve window, counted back from the newest ledger day.
	DefaultDays = 14
	// TopInventory is how many inventory items (by value) are listed per character.
	TopInventory = 20
	// MaxUnlearned bounds the unlearned recipe list.
	MaxUnlearned = 50
)

// Query holds the wow_economy arguments.
type Query struct {
	Character string `json:"character,omitempty" jsonschema:"character name; empty = every character Profiteer has seen"`
	Days      int    `json:"days,omitempty" jsonschema:"ledger and gold-curve window in days, counted back from the newest ledger day (default 14)"`
}

// RealmInfo summarizes one realm set.
type RealmInfo struct {
	Name        string
	Chars       []string
	PricesKnown int
	LastScan    int64
}

// LedgerDay is one day of the gold ledger: category -> copper (negative = spent).
type LedgerDay struct {
	Date       string
	Categories map[string]int64
}

// Net is the day's balance: income minus expenses.
func (d LedgerDay) Net() int64 {
	var net int64
	for _, c := range d.Categories {
		net += c
	}
	return net
}

// WorthPoint is one day of a character's gold curve: copper at first sight and latest.
type WorthPoint struct {
	Day        string
	Start, End int64
}

// GoldCurve is one character's gold over time on one realm.
type GoldCurve struct {
	Realm, Char string
	Points      []WorthPoint
}

// Auction is one listed auction of the player.
type Auction struct {
	Name       string
	Count, Ask int64
	Sold       bool
}

// AuctionList is what one character has listed on one realm.
type AuctionList struct {
	Realm, Char string
	Time        int64
	Items       []Auction
}

// InvItem is one inventory item with its estimated unit value.
type InvItem struct {
	ID                           int64
	Name                         string
	Bags, Bank, UnitPrice, Value int64
	PriceSource                  string // auction, vendor or none
}

// Inventory is one character's scanned bags and bank on one realm, top items by value.
type Inventory struct {
	Realm, Char string
	Time        int64
	ItemsTotal  int
	TotalValue  int64
	Items       []InvItem
}

// Recipe is one unlearned recipe Profiteer knows about.
type Recipe struct {
	Realm, Char, Profession, Name string
	ID                            int64
}

// Report is everything wow_economy answers with, already typed and bounded.
type Report struct {
	Realms         []RealmInfo
	Sessions       int64
	LastScan       int64
	LedgerAnchor   string // the newest ledger day; the window counts back from it
	Ledger         []LedgerDay
	Gold           []GoldCurve
	Auctions       []AuctionList
	Inventories    []Inventory
	Unlearned      []Recipe
	UnlearnedTotal int
}

// cutoff is the first day inside a window of days ending at anchor (ISO dates compare
// lexicographically). An unparsable anchor disables the filter.
func cutoff(anchor string, days int) string {
	if days <= 0 {
		days = DefaultDays
	}
	t, err := time.Parse("2006-01-02", anchor)
	if err != nil {
		return ""
	}
	return t.AddDate(0, 0, -(days - 1)).Format("2006-01-02")
}

// topByValue sorts items by value (then name) and keeps the first n.
func topByValue(items []InvItem, n int) []InvItem {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Value != items[j].Value {
			return items[i].Value > items[j].Value
		}
		return items[i].Name < items[j].Name
	})
	if len(items) > n {
		items = items[:n]
	}
	return items
}
