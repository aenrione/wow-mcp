// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 Piekasz (GitHub: Piekario). Author attribution must be kept: see NOTICE.

package mappins

import (
	"testing"

	"wowmcp/internal/savedvars"
	"wowmcp/internal/snapshot"
)

// The TomTom waypoint mixes positional coordinates with named options, exactly as the
// game saves it (the parser keys the positionals "1".."3" because named keys sit beside them).
const fixture = `
OpenMapDB = {
["worldmap"] = {
["pins"] = {
["nextId"] = 3,
["list"] = {
{
["map"] = 1453,
["x"] = 0.6212,
["y"] = 0.7079,
["id"] = 1,
["note"] = "AH",
["color"] = 1,
["at"] = 1794968910,
},
{
["map"] = 1429,
["x"] = 0.3335,
["y"] = 0.3506,
["id"] = 2,
["note"] = "",
["color"] = 1,
["at"] = 1795136228,
},
},
},
},
}
TomTomWaypointsM = {
["profiles"] = {
["Monjits Gpt"] = {
[1457] = {
["1457:400000000:420000000:Banco"] = {
1457,
0.4,
0.42,
["world"] = true,
["persistent"] = true,
["desc"] = "Banco",
["title"] = "Banco",
["from"] = "TomTom/way",
},
},
},
["Other Char"] = {
[1453] = {
["uid"] = {
1453,
0.5,
0.5,
["title"] = "Elsewhere",
},
},
},
},
}
OpenMapCharDB = {
["worldmap"] = {
["flightsKnown"] = {
["Auberdine, Darkshore"] = true,
["Ironforge, Dun Morogh"] = true,
},
["entered"] = {
[3065] = true,
},
},
}
`

func parseAll(t *testing.T) (openMap, tomtom, charDB map[string]any) {
	t.Helper()
	vars, err := savedvars.Parse([]byte(fixture))
	if err != nil {
		t.Fatal(err)
	}
	return snapshot.AsMap(vars["OpenMapDB"]), snapshot.AsMap(vars["TomTomWaypointsM"]), snapshot.AsMap(vars["OpenMapCharDB"])
}

func TestViewUnifiesSources(t *testing.T) {
	openMap, tomtom, charDB := parseAll(t)
	out := View(openMap, tomtom, charDB, "Monjits-Gpt")
	pins := out["pins"].([]any)
	if out["count"] != 3 || len(pins) != 3 { // 2 OpenMap + 1 TomTom (the other profile is filtered out)
		t.Fatalf("pins: %v", pins)
	}
	sources := map[string]int{}
	for _, raw := range pins {
		pin := snapshot.AsMap(raw)
		sources[pin["source"].(string)]++
		if pin["source"] == "tomtom" {
			if pin["map"] != int64(1457) || pin["x"] != 0.4 || pin["title"] != "Banco" {
				t.Errorf("tomtom pin: %v", pin)
			}
		}
	}
	if sources["openmap"] != 2 || sources["tomtom"] != 1 {
		t.Errorf("sources: %v", sources)
	}
	flights := snapshot.AsMap(out["flightsKnown"])
	if flights["count"] != 2 {
		t.Errorf("flightsKnown: %v", flights)
	}
}

func TestViewAllProfilesWithoutCharacter(t *testing.T) {
	openMap, tomtom, _ := parseAll(t)
	out := View(openMap, tomtom, nil, "")
	if out["count"] != 4 {
		t.Errorf("all profiles: %v", out["count"])
	}
	if _, has := out["flightsKnown"]; has {
		t.Error("no character file, no flightsKnown")
	}
}

func TestViewNothingSaved(t *testing.T) {
	out := View(nil, nil, nil, "Monjits-Gpt")
	if out["count"] != 0 || out["note"] == nil {
		t.Errorf("empty answer should carry a note: %v", out)
	}
}
