// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 Piekasz (GitHub: Piekario). Author attribution must be kept: see NOTICE.

package mappins

import "time"

// View answers wow_map_pins: the unified pin list from the parsed OpenMapDB and
// TomTomWaypointsM tables (either may be nil when its addon never saved) and the
// character's known flight points from OpenMapCharDB (nil when missing).
func View(openMapDB, tomtomWaypoints, openMapCharDB map[string]any, character string) map[string]any {
	pins := readOpenMapPins(openMapDB)
	pins = append(pins, readTomTom(tomtomWaypoints, character)...)
	sortPins(pins)
	list := []any{}
	for _, pin := range pins {
		list = append(list, pin.render())
	}
	out := map[string]any{"pins": list, "count": len(list)}
	if openMapDB == nil && tomtomWaypoints == nil {
		out["note"] = "Neither OpenMap nor TomTom has saved variables for this account."
	}
	if openMapCharDB != nil {
		flights := readFlights(openMapCharDB)
		out["flightsKnown"] = map[string]any{"count": len(flights.Known), "names": flights.Known}
	}
	return out
}

func (p Pin) render() map[string]any {
	entry := map[string]any{"source": p.Source, "map": p.Map, "x": p.X, "y": p.Y}
	if p.Title != "" {
		entry["title"] = p.Title
	}
	if p.At > 0 {
		entry["at"] = time.Unix(p.At, 0).Format("2006-01-02 15:04:05")
	}
	if p.Profile != "" {
		entry["profile"] = p.Profile
	}
	return entry
}
