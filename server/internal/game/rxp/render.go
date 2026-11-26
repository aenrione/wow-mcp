// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 Piekasz (GitHub: Piekario). Author attribution must be kept: see NOTICE.

package rxp

// View answers wow_rxp from the parsed RXPCData and RXPCTrackingData globals.
func View(rxpcData, trackingData map[string]any, character string) map[string]any {
	d := Read(rxpcData, trackingData, character)
	out := map[string]any{"character": character}
	current := map[string]any{"guide": d.GuideName, "group": d.GuideGroup, "step": d.Step}
	out["currentGuide"] = current
	if d.GuideName == "" {
		out["note"] = "RXPGuides has no current guide saved for this character."
	}
	marks, total := d.boundedProgress()
	progress := []any{}
	for _, mark := range marks {
		progress = append(progress, map[string]any{"guide": mark.Guide, "step": mark.Step, "stepId": mark.StepID})
	}
	out["guideProgress"] = progress
	out["guideProgressTotal"] = total
	out["questObjectives"] = renderQuests(d.Quests)
	out["tracking"] = renderTracking(d)
	return out
}

func renderQuests(quests []QuestProgress) []any {
	list := []any{}
	for _, quest := range quests {
		objectives := []any{}
		for _, o := range quest.Objectives {
			objectives = append(objectives, map[string]any{
				"text": o.Text, "type": o.Type, "fulfilled": o.Fulfilled,
				"required": o.Required, "finished": o.Finished,
			})
		}
		entry := map[string]any{"questId": quest.QuestID, "finished": quest.Finished, "objectives": objectives}
		if quest.Name != "" {
			entry["name"] = quest.Name
		}
		list = append(list, entry)
	}
	return list
}

func renderTracking(d Data) map[string]any {
	levels := []any{}
	for _, track := range d.lastLevels() {
		zones := []any{}
		for _, z := range track.Zones {
			zones = append(zones, map[string]any{"zone": z.Zone, "xp": z.XP, "kills": z.Count})
		}
		levels = append(levels, map[string]any{
			"level": track.Level, "deaths": track.Deaths, "mobKills": track.MobKills,
			"mobXp": track.MobXP, "questXp": track.QuestXP, "zones": zones,
		})
	}
	out := map[string]any{"levels": levels, "levelsShown": len(levels), "levelsTotal": len(d.Levels)}
	if d.TrackingKey != "" {
		out["profile"] = d.TrackingKey
	}
	return out
}
