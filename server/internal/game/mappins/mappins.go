// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 Piekasz (GitHub: Piekario). Author attribution must be kept: see NOTICE.

// Package mappins unifies the map pins the player saved in OpenMap
// (OpenMapDB.worldmap.pins.list) and the persistent TomTom waypoints
// (TomTomWaypointsM.profiles), plus the flight points OpenMap knows per character.
package mappins

import "sort"

// Query holds the wow_map_pins argument.
type Query struct {
	Character string `json:"character,omitempty" jsonschema:"character name; empty = most recently dumped character"`
}

// Pin is one map pin or waypoint, whoever saved it. Coordinates are 0..1 on the map.
type Pin struct {
	Source  string // openmap or tomtom
	Map     int64  // uiMapID
	X, Y    float64
	Title   string // OpenMap note or TomTom title/desc
	At      int64  // Unix save time (OpenMap only; 0 = unknown)
	Profile string // the TomTom profile the waypoint belongs to ("" for OpenMap pins)
}

// Flights are the flight points OpenMap has seen the character know.
type Flights struct {
	Known []string
}

// sortPins orders pins by map, then position, so the answer is stable.
func sortPins(pins []Pin) {
	sort.SliceStable(pins, func(i, j int) bool {
		a, b := pins[i], pins[j]
		if a.Map != b.Map {
			return a.Map < b.Map
		}
		if a.X != b.X {
			return a.X < b.X
		}
		return a.Y < b.Y
	})
}
