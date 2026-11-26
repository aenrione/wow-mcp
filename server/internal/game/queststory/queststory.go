// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 Piekasz (GitHub: Piekario). Author attribution must be kept: see NOTICE.

// Package queststory is the quest history Questie keeps per character in its account
// SavedVariables (QuestieConfig): the set of completed quests and the journey timeline
// (quest accepted / completed / abandoned, with level and time).
package queststory

import "sort"

// MaxCompleted bounds the completed quest id list in the answer.
const MaxCompleted = 500

// DefaultLimit is the default number of journey entries returned (the newest).
const DefaultLimit = 100

// Query holds the wow_quest_story arguments.
type Query struct {
	Character string `json:"character,omitempty" jsonschema:"character name; empty = most recently dumped character"`
	Limit     int    `json:"limit,omitempty" jsonschema:"journey entries to return, newest last (default 100)"`
	Since     int64  `json:"since,omitempty" jsonschema:"only journey entries at or after this Unix time"`
	Level     int64  `json:"level,omitempty" jsonschema:"only journey entries recorded at this character level"`
}

// Entry is one journey event: a quest accepted, completed or abandoned.
type Entry struct {
	Event     string // "Quest"
	SubType   string // Accept, Complete, Abandon
	Quest     int64  // quest id
	Level     int64  // character level at the time
	Timestamp int64  // Unix
}

// Story is one character's quest history as Questie saved it.
type Story struct {
	Key       string // the key Questie uses for the character, e.g. "Monjits Gpt"
	Completed []int64
	Journey   []Entry
}

// filtered applies since and level and keeps the newest limit entries, oldest first.
func (s Story) filtered(q Query) (entries []Entry, total int) {
	for _, e := range s.Journey {
		if q.Since != 0 && e.Timestamp < q.Since {
			continue
		}
		if q.Level != 0 && e.Level != q.Level {
			continue
		}
		entries = append(entries, e)
	}
	total = len(entries)
	limit := q.Limit
	if limit <= 0 {
		limit = DefaultLimit
	}
	if len(entries) > limit {
		entries = entries[len(entries)-limit:]
	}
	return entries, total
}

// boundedCompleted is the sorted completed quest id list, capped at MaxCompleted.
func (s Story) boundedCompleted() []int64 {
	ids := make([]int64, len(s.Completed))
	copy(ids, s.Completed)
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	if len(ids) > MaxCompleted {
		ids = ids[:MaxCompleted]
	}
	return ids
}
