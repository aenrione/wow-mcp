// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 Piekasz (GitHub: Piekario). Author attribution must be kept: see NOTICE.

package rxp

import (
	"sort"
	"strconv"

	"wowmcp/internal/game/charkey"
	"wowmcp/internal/snapshot"
)

// Read turns the parsed RXPCData and RXPCTrackingData globals into typed Data.
// Either map may be nil (the file was missing or the global absent).
func Read(rxpcData, trackingData map[string]any, character string) Data {
	d := Data{}
	d.GuideName, _ = rxpcData["currentGuideName"].(string)
	d.GuideGroup, _ = rxpcData["currentGuideGroup"].(string)
	d.Step, _ = snapshot.AsInt(rxpcData["currentStep"])
	d.Progress = readProgress(rxpcData)
	d.Quests = readQuests(rxpcData)
	d.Levels, d.TrackingKey = readTracking(trackingData, character)
	return d
}

// readProgress reads guideProgress["group|subgroup|name"] = {step, stepId}; the table
// also carries scalar strays like playerGUID, which are skipped.
func readProgress(rxpcData map[string]any) []GuideMark {
	var marks []GuideMark
	for guide, raw := range snapshot.AsMap(rxpcData["guideProgress"]) {
		entry := snapshot.AsMap(raw)
		if entry == nil {
			continue
		}
		mark := GuideMark{Guide: guide}
		mark.Step, _ = snapshot.AsInt(entry["step"])
		mark.StepID, _ = snapshot.AsInt(entry["stepId"])
		marks = append(marks, mark)
	}
	return marks
}

// readQuests reads questObjectivesCache[questID] = objectives, naming quests from
// questNameCache. Each cache entry is a Lua table of positional objectives plus a named
// finished flag, so the parser gives either a map keyed "1".."n" + "finished" or, when
// the flag is absent, a plain list.
func readQuests(rxpcData map[string]any) []QuestProgress {
	names := snapshot.AsMap(rxpcData["questNameCache"])
	var quests []QuestProgress
	for key, raw := range snapshot.AsMap(rxpcData["questObjectivesCache"]) {
		id, err := strconv.ParseInt(key, 10, 64)
		if err != nil {
			continue
		}
		quest := QuestProgress{QuestID: id}
		quest.Name, _ = names[key].(string)
		if entry := snapshot.AsMap(raw); entry != nil {
			quest.Finished, _ = entry["finished"].(bool)
			for i := 1; ; i++ {
				objective := snapshot.AsMap(entry[strconv.Itoa(i)])
				if objective == nil {
					break
				}
				quest.Objectives = append(quest.Objectives, readObjective(objective))
			}
		} else {
			for _, rawObjective := range snapshot.AsList(raw) {
				quest.Objectives = append(quest.Objectives, readObjective(snapshot.AsMap(rawObjective)))
			}
		}
		quests = append(quests, quest)
	}
	sort.Slice(quests, func(i, j int) bool { return quests[i].QuestID < quests[j].QuestID })
	return quests
}

func readObjective(entry map[string]any) Objective {
	o := Objective{}
	o.Text, _ = entry["text"].(string)
	o.Type, _ = entry["type"].(string)
	o.Fulfilled, _ = snapshot.AsInt(entry["numFulfilled"])
	o.Required, _ = snapshot.AsInt(entry["numRequired"])
	o.Finished, _ = entry["finished"].(bool)
	return o
}

// readTracking reads RXPCTrackingData.profiles[profile].levels[N] =
// {deaths, mobs[zone] = {xp, count}, quests[zone][questID] = xp, timestamp}.
func readTracking(trackingData map[string]any, character string) ([]LevelTrack, string) {
	profiles := snapshot.AsMap(trackingData["profiles"])
	keys := make([]string, 0, len(profiles))
	for k := range profiles {
		keys = append(keys, k)
	}
	matched := charkey.Find(keys, character)
	if len(matched) == 0 {
		return nil, ""
	}
	profile := snapshot.AsMap(profiles[matched[0]])
	var levels []LevelTrack
	for key, raw := range snapshot.AsMap(profile["levels"]) {
		level, err := strconv.ParseInt(key, 10, 64)
		if err != nil {
			continue
		}
		levels = append(levels, readLevel(level, snapshot.AsMap(raw)))
	}
	return levels, matched[0]
}

func readLevel(level int64, entry map[string]any) LevelTrack {
	track := LevelTrack{Level: level}
	track.Deaths, _ = snapshot.AsInt(entry["deaths"])
	for zone, raw := range snapshot.AsMap(entry["mobs"]) {
		mob := snapshot.AsMap(raw)
		kills := ZoneKills{Zone: zone}
		kills.XP, _ = snapshot.AsInt(mob["xp"])
		kills.Count, _ = snapshot.AsInt(mob["count"])
		track.MobXP += kills.XP
		track.MobKills += kills.Count
		track.Zones = append(track.Zones, kills)
	}
	sort.Slice(track.Zones, func(i, j int) bool { return track.Zones[i].Zone < track.Zones[j].Zone })
	for _, rawZone := range snapshot.AsMap(entry["quests"]) {
		for _, rawXP := range snapshot.AsMap(rawZone) {
			if xp, ok := snapshot.AsInt(rawXP); ok {
				track.QuestXP += xp
			}
		}
	}
	return track
}
