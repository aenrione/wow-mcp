// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 Piekasz (GitHub: Piekario). Author attribution must be kept: see NOTICE.

package rxp

import (
	"testing"

	"wowmcp/internal/savedvars"
	"wowmcp/internal/snapshot"
)

// A questObjectivesCache entry mixes a positional objective list with a named finished
// flag, exactly as RXPGuides saves it; guideProgress carries a scalar stray (playerGUID).
const fixture = `
RXPCData = {
["currentStep"] = 25,
["currentGuideName"] = "20-21 Darkshore/Ashenvale",
["currentGuideGroup"] = "RestedXP Forever Guide (A)",
["guideProgress"] = {
["RestedXP Forever Guide (A)|Speedrun Guide 1-20|19-20 Redridge"] = {
["step"] = 20,
["stepId"] = 3235849873,
},
["playerGUID"] = "Player-6922-02086BD3",
["RestedXP Forever Guide (A)|Speedrun Guide 1-20|14-16 Darkshore"] = {
["step"] = 263,
["stepId"] = 2208765752,
},
},
["questNameCache"] = {
[2459] = "Tallonkai's Jewel",
},
["questObjectivesCache"] = {
[2459] = {
{
["type"] = "monster",
["numRequired"] = 7,
["finished"] = false,
["text"] = "0/7  ",
["numFulfilled"] = 0,
},
{
["type"] = "item",
["numRequired"] = 1,
["finished"] = true,
["text"] = "1/1 Tallonkai's Jewel",
["numFulfilled"] = 1,
},
["finished"] = false,
},
},
}
RXPCTrackingData = {
["profiles"] = {
["Monjits Gpt"] = {
["levels"] = {
[11] = {
["deaths"] = 1,
["mobs"] = {
["Teldrassil"] = {
["xp"] = 510,
["count"] = 3,
},
["Darkshore"] = {
["xp"] = 420,
["count"] = 1,
},
},
["quests"] = {
["Teldrassil"] = {
[2459] = 450,
},
},
},
[12] = {
["deaths"] = 0,
["mobs"] = {
["Darkshore"] = {
["xp"] = 900,
["count"] = 5,
},
},
},
},
},
},
}
`

func parseAll(t *testing.T) (data, tracking map[string]any) {
	t.Helper()
	vars, err := savedvars.Parse([]byte(fixture))
	if err != nil {
		t.Fatal(err)
	}
	return snapshot.AsMap(vars["RXPCData"]), snapshot.AsMap(vars["RXPCTrackingData"])
}

func TestView(t *testing.T) {
	data, tracking := parseAll(t)
	out := View(data, tracking, "Monjits-Gpt")
	current := snapshot.AsMap(out["currentGuide"])
	if current["guide"] != "20-21 Darkshore/Ashenvale" || current["step"] != int64(25) {
		t.Errorf("currentGuide: %v", current)
	}
	progress := out["guideProgress"].([]any)
	if len(progress) != 2 || out["guideProgressTotal"] != 2 { // playerGUID is skipped
		t.Errorf("guideProgress: %v", progress)
	}
	quests := out["questObjectives"].([]any)
	if len(quests) != 1 {
		t.Fatalf("quests: %v", quests)
	}
	quest := snapshot.AsMap(quests[0])
	if quest["questId"] != int64(2459) || quest["name"] != "Tallonkai's Jewel" || quest["finished"] != false {
		t.Errorf("quest: %v", quest)
	}
	objectives := quest["objectives"].([]any)
	if len(objectives) != 2 {
		t.Fatalf("objectives: %v", objectives)
	}
	second := snapshot.AsMap(objectives[1])
	if second["fulfilled"] != int64(1) || second["required"] != int64(1) || second["finished"] != true {
		t.Errorf("objective order and fields: %v", second)
	}
}

func TestViewTracking(t *testing.T) {
	data, tracking := parseAll(t)
	out := View(data, tracking, "Monjits-Gpt")
	track := snapshot.AsMap(out["tracking"])
	if track["profile"] != "Monjits Gpt" || track["levelsTotal"] != 2 {
		t.Errorf("tracking meta: %v", track)
	}
	levels := track["levels"].([]any)
	first := snapshot.AsMap(levels[0])
	if first["level"] != int64(11) || first["deaths"] != int64(1) ||
		first["mobKills"] != int64(4) || first["mobXp"] != int64(930) || first["questXp"] != int64(450) {
		t.Errorf("level 11 summary: %v", first)
	}
}

func TestLastLevelsBound(t *testing.T) {
	d := Data{}
	for i := int64(1); i <= 10; i++ {
		d.Levels = append(d.Levels, LevelTrack{Level: i})
	}
	levels := d.lastLevels()
	if len(levels) != TrackedLevels || levels[0].Level != 5 || levels[len(levels)-1].Level != 10 {
		t.Errorf("lastLevels: %+v", levels)
	}
}

func TestViewMissingFiles(t *testing.T) {
	out := View(nil, nil, "Monjits-Gpt")
	if out["note"] == nil {
		t.Errorf("no RXPCData should carry a note: %v", out)
	}
}
