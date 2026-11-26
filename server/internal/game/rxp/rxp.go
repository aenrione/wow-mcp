// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 Piekasz (GitHub: Piekario). Author attribution must be kept: see NOTICE.

// Package rxp reads the per-character state the RXPGuides leveling addon saves
// (RXPCData and RXPCTrackingData): the current guide and step, progress through every
// guide, the cached quest objective state and the per-level kill/XP tracking.
// This is the server-side read of the SavedVariables file, independent from any
// in-game dump section.
package rxp

import "sort"

// MaxGuideProgress bounds the guideProgress list in the answer.
const MaxGuideProgress = 40

// TrackedLevels is how many of the newest levels the tracking summary keeps.
const TrackedLevels = 6

// Query holds the wow_rxp argument.
type Query struct {
	Character string `json:"character,omitempty" jsonschema:"character name; empty = most recently dumped character"`
}

// GuideMark is the saved position in one guide (key format group|subgroup|name).
type GuideMark struct {
	Guide        string
	Step, StepID int64
}

// Objective is one cached quest objective.
type Objective struct {
	Text                string
	Type                string
	Fulfilled, Required int64
	Finished            bool
}

// QuestProgress is the cached objective state of one quest.
type QuestProgress struct {
	QuestID    int64
	Name       string // from questNameCache; "" when not cached
	Finished   bool
	Objectives []Objective
}

// ZoneKills are the tracked mob kills and XP in one zone during one level.
type ZoneKills struct {
	Zone      string
	XP, Count int64
}

// LevelTrack is the tracking summary of one character level.
type LevelTrack struct {
	Level, Deaths, MobXP, MobKills, QuestXP int64
	Zones                                   []ZoneKills
}

// Data is everything the two files say about the character.
type Data struct {
	GuideName, GuideGroup string
	Step                  int64
	Progress              []GuideMark
	Quests                []QuestProgress
	Levels                []LevelTrack
	TrackingKey           string // the RXPCTrackingData profile matched; "" when none
}

// boundedProgress sorts the guide marks and keeps the first MaxGuideProgress.
func (d Data) boundedProgress() ([]GuideMark, int) {
	marks := make([]GuideMark, len(d.Progress))
	copy(marks, d.Progress)
	sort.Slice(marks, func(i, j int) bool { return marks[i].Guide < marks[j].Guide })
	total := len(marks)
	if len(marks) > MaxGuideProgress {
		marks = marks[:MaxGuideProgress]
	}
	return marks, total
}

// lastLevels keeps the newest TrackedLevels levels, ascending.
func (d Data) lastLevels() []LevelTrack {
	levels := make([]LevelTrack, len(d.Levels))
	copy(levels, d.Levels)
	sort.Slice(levels, func(i, j int) bool { return levels[i].Level < levels[j].Level })
	if len(levels) > TrackedLevels {
		levels = levels[len(levels)-TrackedLevels:]
	}
	return levels
}
