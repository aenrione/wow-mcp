// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 Piekasz (GitHub: Piekario). Author attribution must be kept: see NOTICE.

package mappins

import (
	"sort"

	"wowmcp/internal/game/charkey"
	"wowmcp/internal/snapshot"
)

// readOpenMapPins reads OpenMapDB.worldmap.pins.list: {id, map, x, y, note, color, at}.
func readOpenMapPins(openMapDB map[string]any) []Pin {
	worldmap := snapshot.AsMap(openMapDB["worldmap"])
	list := snapshot.AsList(snapshot.AsMap(worldmap["pins"])["list"])
	var pins []Pin
	for _, raw := range list {
		entry := snapshot.AsMap(raw)
		pin := Pin{Source: "openmap"}
		pin.Map, _ = snapshot.AsInt(entry["map"])
		pin.X, _ = snapshot.AsFloat(entry["x"])
		pin.Y, _ = snapshot.AsFloat(entry["y"])
		pin.Title, _ = entry["note"].(string)
		pin.At, _ = snapshot.AsInt(entry["at"])
		pins = append(pins, pin)
	}
	return pins
}

// readTomTom reads the persistent waypoints of TomTomWaypointsM.profiles. A waypoint is
// a mixed Lua table: positional 1..3 hold uiMapID, x and y (the parser keys them "1".."3"
// because named options sit next to them), the options hold title/desc.
func readTomTom(waypoints map[string]any, character string) []Pin {
	profiles := snapshot.AsMap(waypoints["profiles"])
	names := make([]string, 0, len(profiles))
	for name := range profiles {
		names = append(names, name)
	}
	var matched []string
	if character == "" {
		sort.Strings(names)
		matched = names
	} else {
		matched = charkey.Find(names, character)
	}
	var pins []Pin
	for _, profile := range matched {
		for _, rawMap := range snapshot.AsMap(profiles[profile]) {
			for _, rawPoint := range snapshot.AsMap(rawMap) {
				if pin, ok := readWaypoint(snapshot.AsMap(rawPoint), profile); ok {
					pins = append(pins, pin)
				}
			}
		}
	}
	return pins
}

func readWaypoint(entry map[string]any, profile string) (Pin, bool) {
	pin := Pin{Source: "tomtom", Profile: profile}
	var okMap, okX, okY bool
	pin.Map, okMap = snapshot.AsInt(entry["1"])
	pin.X, okX = snapshot.AsFloat(entry["2"])
	pin.Y, okY = snapshot.AsFloat(entry["3"])
	if !okMap || !okX || !okY {
		return Pin{}, false
	}
	if title, _ := entry["title"].(string); title != "" {
		pin.Title = title
	} else {
		pin.Title, _ = entry["desc"].(string)
	}
	return pin, true
}

// readFlights reads OpenMapCharDB.worldmap.flightsKnown = {["name"] = true}.
func readFlights(openMapCharDB map[string]any) Flights {
	worldmap := snapshot.AsMap(openMapCharDB["worldmap"])
	f := Flights{}
	for name, known := range snapshot.AsMap(worldmap["flightsKnown"]) {
		if truth, ok := known.(bool); ok && truth {
			f.Known = append(f.Known, name)
		}
	}
	sort.Strings(f.Known)
	return f
}
