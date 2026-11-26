// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 Piekasz (GitHub: Piekario). Author attribution must be kept: see NOTICE.

package queststory

import "time"

// View answers wow_quest_story from the parsed QuestieConfig table.
func View(questieConfig map[string]any, character string, q Query) (map[string]any, error) {
	story, err := Read(questieConfig, character)
	if err != nil {
		return nil, err
	}
	entries, total := story.filtered(q)
	timeline := []any{}
	for _, e := range entries {
		timeline = append(timeline, e.render())
	}
	out := map[string]any{
		"character":      character,
		"questieKey":     story.Key,
		"completedCount": len(story.Completed),
		"journey":        timeline,
		"journeyShown":   len(entries),
		"journeyTotal":   total,
	}
	ids := story.boundedCompleted()
	out["completedQuestIds"] = ids
	if len(story.Completed) > MaxCompleted {
		out["completedNote"] = "Only the first 500 quest ids (ascending) are listed; completedCount is the real total."
	}
	return out, nil
}

func (e Entry) render() map[string]any {
	row := map[string]any{"event": e.Event, "subType": e.SubType, "questId": e.Quest, "level": e.Level, "timestamp": e.Timestamp}
	if e.Timestamp > 0 {
		row["time"] = time.Unix(e.Timestamp, 0).Format("2006-01-02 15:04:05")
	}
	return row
}
