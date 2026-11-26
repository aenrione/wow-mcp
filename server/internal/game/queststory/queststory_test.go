// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 Piekasz (GitHub: Piekario). Author attribution must be kept: see NOTICE.

package queststory

import (
	"strings"
	"testing"

	"wowmcp/internal/savedvars"
	"wowmcp/internal/snapshot"
)

// questieFixture mirrors the shape of a real Questie.lua: char state keyed "Name Realm"
// in the account file, complete as a sparse integer-keyed table, journey as a list.
const questieFixture = `
QuestieConfig = {
["char"] = {
["Monjits Gpt"] = {
["complete"] = {
[935] = true,
[99050] = true,
[922] = true,
},
["journey"] = {
{
["Event"] = "Quest",
["SubType"] = "Accept",
["Quest"] = 99050,
["Level"] = 10,
["Timestamp"] = 1794491768,
},
{
["Event"] = "Quest",
["SubType"] = "Complete",
["Quest"] = 99050,
["Level"] = 10,
["Timestamp"] = 1794492361,
},
{
["Event"] = "Quest",
["SubType"] = "Complete",
["Quest"] = 922,
["Level"] = 11,
["Timestamp"] = 1794492789,
},
},
},
},
}
`

func parseFixture(t *testing.T) map[string]any {
	t.Helper()
	vars, err := savedvars.Parse([]byte(questieFixture))
	if err != nil {
		t.Fatal(err)
	}
	return snapshot.AsMap(vars["QuestieConfig"])
}

func TestViewMatchesSnapshotKey(t *testing.T) {
	out, err := View(parseFixture(t), "Monjits-Gpt", Query{})
	if err != nil {
		t.Fatal(err)
	}
	if out["questieKey"] != "Monjits Gpt" || out["completedCount"] != 3 {
		t.Errorf("questieKey/completedCount: %v / %v", out["questieKey"], out["completedCount"])
	}
	ids := out["completedQuestIds"].([]int64)
	if len(ids) != 3 || ids[0] != 922 || ids[2] != 99050 {
		t.Errorf("completed ids not sorted: %v", ids)
	}
	journey := out["journey"].([]any)
	if len(journey) != 3 {
		t.Fatalf("journey: %v", journey)
	}
	first := snapshot.AsMap(journey[0])
	if first["subType"] != "Accept" || first["questId"] != int64(99050) {
		t.Errorf("journey order: %v", first)
	}
}

func TestViewFilters(t *testing.T) {
	cfg := parseFixture(t)
	out, err := View(cfg, "Monjits-Gpt", Query{Since: 1794492400})
	if err != nil {
		t.Fatal(err)
	}
	if out["journeyTotal"] != 1 {
		t.Errorf("since filter: %v", out["journeyTotal"])
	}
	out, err = View(cfg, "Monjits-Gpt", Query{Level: 10, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if out["journeyTotal"] != 2 || out["journeyShown"] != 1 {
		t.Errorf("level filter with limit: total %v shown %v", out["journeyTotal"], out["journeyShown"])
	}
	// the newest matching entry is kept
	kept := snapshot.AsMap(out["journey"].([]any)[0])
	if kept["subType"] != "Complete" {
		t.Errorf("limit should keep the newest entries: %v", kept)
	}
}

func TestViewUnknownCharacter(t *testing.T) {
	_, err := View(parseFixture(t), "Nobody-Here", Query{})
	if err == nil || !strings.Contains(err.Error(), "Monjits Gpt") {
		t.Errorf("want an error listing the known keys, got %v", err)
	}
}

func TestBoundedCompleted(t *testing.T) {
	story := Story{}
	for i := int64(0); i < MaxCompleted+10; i++ {
		story.Completed = append(story.Completed, i)
	}
	if got := story.boundedCompleted(); len(got) != MaxCompleted {
		t.Errorf("bound: %d", len(got))
	}
}
