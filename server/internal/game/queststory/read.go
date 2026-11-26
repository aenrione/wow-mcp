// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 Piekasz (GitHub: Piekario). Author attribution must be kept: see NOTICE.

package queststory

import (
	"fmt"
	"sort"
	"strconv"

	"wowmcp/internal/game/charkey"
	"wowmcp/internal/snapshot"
)

// Read extracts one character's story from the QuestieConfig table. The character is
// the snapshot's "Name-Realm" key; Questie keys its char table "Name Realm" with a space.
func Read(questieConfig map[string]any, character string) (Story, error) {
	chars := snapshot.AsMap(questieConfig["char"])
	if len(chars) == 0 {
		return Story{}, fmt.Errorf("QuestieConfig has no char table: Questie has never saved for this account")
	}
	keys := make([]string, 0, len(chars))
	for k := range chars {
		keys = append(keys, k)
	}
	matched := charkey.Find(keys, character)
	if len(matched) == 0 {
		sort.Strings(keys)
		return Story{}, fmt.Errorf("Questie has no data for character %q; it knows: %v", character, keys)
	}
	entry := snapshot.AsMap(chars[matched[0]])
	return Story{Key: matched[0], Completed: readCompleted(entry), Journey: readJourney(entry)}, nil
}

// readCompleted reads complete = { [questID] = true }; the Lua parser turns the sparse
// integer keys into decimal strings.
func readCompleted(entry map[string]any) []int64 {
	var ids []int64
	for key, done := range snapshot.AsMap(entry["complete"]) {
		if truth, ok := done.(bool); !ok || !truth {
			continue
		}
		if id, err := strconv.ParseInt(key, 10, 64); err == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

// readJourney reads the journey list: {Event, SubType, Quest, Level, Timestamp}.
func readJourney(entry map[string]any) []Entry {
	var journey []Entry
	for _, raw := range snapshot.AsList(entry["journey"]) {
		row := snapshot.AsMap(raw)
		e := Entry{}
		e.Event, _ = row["Event"].(string)
		e.SubType, _ = row["SubType"].(string)
		e.Quest, _ = snapshot.AsInt(row["Quest"])
		e.Level, _ = snapshot.AsInt(row["Level"])
		e.Timestamp, _ = snapshot.AsInt(row["Timestamp"])
		journey = append(journey, e)
	}
	sort.SliceStable(journey, func(i, j int) bool { return journey[i].Timestamp < journey[j].Timestamp })
	return journey
}
