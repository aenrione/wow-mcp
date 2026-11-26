// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 Piekasz (GitHub: Piekario). Author attribution must be kept: see NOTICE.

// Package charkey matches the snapshot's "Name-Realm" character keys against the
// character keys other addons save. The same character appears as "Monjits-Gpt" in the
// WowMcp snapshot, "Monjits Gpt" in Questie and TomTom, and "Monjits Gpt-" or
// "Monjits Gpt-CAMWOW US1 Realm 4 PvP" in Profiteer (name, realm and realm-set joined
// with spaces or dashes), so keys are compared word by word, ignoring case.
package charkey

import (
	"sort"
	"strings"
)

// Canon lowercases a key and joins its words (split on spaces and dashes) with single
// spaces: "Monjits-Gpt", "Monjits Gpt" and "Monjits Gpt-" all become "monjits gpt".
func Canon(key string) string {
	words := strings.FieldsFunc(strings.ToLower(key), func(r rune) bool {
		return r == ' ' || r == '-'
	})
	return strings.Join(words, " ")
}

// Matches says whether an addon's character key names the snapshot character: the
// canonical addon key equals the canonical character key or extends it by more words
// (e.g. the realm-set suffix Profiteer appends).
func Matches(character, addonKey string) bool {
	who, key := Canon(character), Canon(addonKey)
	if who == "" || key == "" {
		return false
	}
	return key == who || strings.HasPrefix(key, who+" ")
}

// Find returns the addon keys that name the character, sorted. An empty character
// matches every key (the caller asked for all characters).
func Find(keys []string, character string) []string {
	var out []string
	for _, k := range keys {
		if character == "" || Matches(character, k) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
