// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 Piekasz (GitHub: Piekario). Author attribution must be kept: see NOTICE.

package charkey

import (
	"reflect"
	"testing"
)

func TestMatches(t *testing.T) {
	cases := []struct {
		character, addonKey string
		want                bool
	}{
		{"Monjits-Gpt", "Monjits Gpt", true},                        // Questie, TomTom
		{"Monjits-Gpt", "monjits gpt", true},                        // case-insensitive
		{"Monjits-Gpt", "Monjits Gpt-", true},                       // Profiteer, empty realm set
		{"Monjits-Gpt", "Monjits Gpt-CAMWOW US1 Realm 4 PvP", true}, // Profiteer, named realm set
		{"Monjits-Gpt", "Monjits-Gpt", true},                        // the snapshot key itself
		{"Monjits-Gpt", "Monjitsx Gpt", false},                      // different name
		{"Monjits-Gpt", "Monjits", false},                           // shorter than the character key
		{"Monjits-Gpt", "", false},
		{"", "Monjits Gpt", false},
	}
	for _, c := range cases {
		if got := Matches(c.character, c.addonKey); got != c.want {
			t.Errorf("Matches(%q, %q) = %v, want %v", c.character, c.addonKey, got, c.want)
		}
	}
}

func TestFind(t *testing.T) {
	keys := []string{"Zz Gpt", "Monjits Gpt-", "Monjits Gpt-CAMWOW US1 Realm 4 PvP"}
	got := Find(keys, "Monjits-Gpt")
	want := []string{"Monjits Gpt-", "Monjits Gpt-CAMWOW US1 Realm 4 PvP"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Find = %v, want %v", got, want)
	}
	if all := Find(keys, ""); len(all) != 3 {
		t.Errorf("empty character should match every key, got %v", all)
	}
}
