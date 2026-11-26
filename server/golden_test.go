// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 Piekasz (GitHub: Piekario). Author attribution must be kept: see NOTICE.

package main

// The "golden responses" test: a black box over the whole MCP server. The server is
// assembled as in main (newServer), connected to a client over an in-memory
// transport, and every tool and every prompt is called through the real
// MCP protocol. The full response goes to a file under testdata/golden/...; a change
// in server behavior (e.g. after a refactor) shows up as a difference in the files.
//
// Refreshing the golden files: go test -run Golden -update

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"wowmcp/internal/fixtures"
	"wowmcp/internal/game/auctions"
	"wowmcp/internal/game/eventlog"
	"wowmcp/internal/game/progress"
	"wowmcp/internal/mcpserver"
	"wowmcp/internal/snapshot"
	"wowmcp/internal/storage"
)

var updateGolden = flag.Bool("update", false, "write the testdata/golden files instead of comparing")

const goldenRoot = "testdata/golden"

// Character names used in the cases: an existing and a nonexistent one.
const (
	charKnown   = "Piekasz"
	charUnknown = "Unknown"
)

// goldenCase is one tool or prompt call.
type goldenCase struct {
	prompt  bool           // false = tools/call, true = prompts/get
	name    string         // tool or prompt name
	variant string         // short variant name (part of the file name)
	args    map[string]any // call arguments
	stale   bool           // true = the case is also part of the stale snapshot scenario
	// realOnly = a case only for the "real" scenario (the real dump has different character and item names).
	realOnly bool
	// inReal = a case with a non-default variant that is also part of the "real" scenario.
	inReal bool
}

func (c goldenCase) file() string { return c.name + "__" + c.variant + ".txt" }

type args = map[string]any

func tc(name, variant string, a args) goldenCase {
	return goldenCase{name: name, variant: variant, args: a}
}

// tcs is a tool case that is also part of the "stale" scenario.
func tcs(name, variant string, a args) goldenCase {
	c := tc(name, variant, a)
	c.stale = true
	return c
}

// ir marks a case as part of the "real" scenario, and ro as a case only for it.
func ir(c goldenCase) goldenCase { c.inReal = true; return c }
func ro(c goldenCase) goldenCase { c.realOnly = true; return c }

// All tools with a character field take the same three character variants.
func characterVariants(name string) []goldenCase {
	return []goldenCase{
		tcs(name, "default", args{}),
		tc(name, "named", args{"character": charKnown}),
		tc(name, "unknown_character", args{"character": charUnknown}),
	}
}

func goldenCases() []goldenCase {
	var cs []goldenCase
	add := func(list ...goldenCase) { cs = append(cs, list...) }

	add(tcs("wow_status", "default", args{}))
	add(characterVariants("wow_doctor")...)
	for _, n := range []string{"wow_character", "wow_quests", "wow_bags", "wow_equipment", "wow_spells", "wow_pet", "wow_trainer"} {
		add(characterVariants(n)...)
	}

	for _, sec := range []string{"actionBars", "macros", "completedQuests", "flightPaths", "vendor", "bank", "probe", "stats", "auras", "inspect", "buyback", "friends", "guild", "xpGains", "equipmentSets", "pvp"} {
		add(tc("wow_section", "section_"+sec, args{"section": sec}))
	}
	// Forever-suite tools run unwired in the golden server (no Extras/Slots/Wiki), so the answers are
	// the stable "off" errors plus argument validation; the wired paths have unit tests in their packages.
	add(
		tc("wow_rxp", "off", args{"character": charKnown}),
		tc("wow_quest_story", "off", args{"character": charKnown, "level": 10, "limit": 5, "since": 1764000000}),
		tc("wow_economy", "off", args{"character": charKnown, "days": 7}),
		tc("wow_price", "off", args{"item": "Linen Cloth"}),
		tc("wow_map_pins", "off", args{"character": charKnown}),
		tc("wow_export_csv", "off", args{"dataset": "prices", "character": charKnown}),
		tc("wow_waypoint_set", "off", args{"mapID": 1429, "x": 44.2, "y": 61.1, "title": "Golden", "zone": "Westfall", "character": charKnown}),
		tc("wow_map_focus", "off", args{"mapID": 1429, "x": 50, "y": 50}),
		tc("wow_waypoints_clear", "off", args{}),
		tc("wow_slots_status", "off", args{}),
		tc("wow_wiki_list", "off", args{}),
		tc("wow_wiki_read", "off", args{"slug": "gold-routes"}),
		tc("wow_wiki_search", "off", args{"query": "gold"}),
		tc("wow_wiki_write", "off", args{"slug": "gold-routes", "content": "# Gold routes"}),
	)

	add(
		tcs("wow_section", "section_vendor_stale", args{"section": "vendor"}),
		tc("wow_section", "unknown_section", args{"section": "nonexistent"}),
		tc("wow_section", "meta_section", args{"section": "dumpedAt"}),
		tc("wow_section", "empty_section", args{"section": ""}),
		tc("wow_section", "named", args{"section": "actionBars", "character": charKnown}),
		tc("wow_section", "unknown_character", args{"section": "actionBars", "character": charUnknown}),
		tc("wow_section", "missing_required", args{}),
		// reading a big section in pieces: path (dot path into the section) and limit
		tc("wow_section", "path_scalar", args{"section": "character", "path": "level"}),
		tc("wow_section", "path_unknown", args{"section": "character", "path": "nope"}),
		tc("wow_section", "limit_list", args{"section": "completedQuests", "limit": 2}),
		tc("wow_section", "limit_object", args{"section": "stats", "limit": 3}),
		tc("wow_section", "path_and_limit", args{"section": "character", "path": "zone", "limit": 1, "character": charKnown}),
		ro(tc("wow_section", "real_dungeons_limit", args{"section": "dungeons", "limit": 2})),
		ro(tc("wow_section", "real_vendor_path", args{"section": "vendor", "path": "items", "limit": 3})),
	)

	add(
		tcs("wow_reputation", "default", args{}),
		tc("wow_reputation", "query_match", args{"query": "Darn"}),
		tc("wow_reputation", "query_nomatch", args{"query": "Zzzz"}),
		tc("wow_reputation", "standing_match", args{"standing": "Friendly"}),
		tc("wow_reputation", "standing_nomatch", args{"standing": "Exalted"}),
		tc("wow_reputation", "query_and_standing", args{"query": "Darn", "standing": "Friendly"}),
		tc("wow_reputation", "named", args{"character": charKnown}),
		tc("wow_reputation", "unknown_character", args{"character": charUnknown}),
	)

	add(
		tcs("wow_bank", "default", args{}),
		tc("wow_bank", "query_match", args{"query": "Cloth"}),
		tc("wow_bank", "query_nomatch", args{"query": "Zzzz"}),
		tc("wow_bank", "type_match", args{"type": "Trade Goods"}),
		tc("wow_bank", "type_nomatch", args{"type": "Weapon"}),
		tc("wow_bank", "min_quality", args{"minQuality": 2}),
		tc("wow_bank", "sellable", args{"sellable": true}),
		tc("wow_bank", "all_filters", args{"character": charKnown, "query": "a", "type": "Armor", "minQuality": 1, "sellable": true}),
		tc("wow_bank", "unknown_character", args{"character": charUnknown}),
	)

	// --- wow_trainer: choosing a remembered trainer ---
	add(
		tc("wow_trainer", "npc_match", args{"npc": "astaia"}),
		tc("wow_trainer", "npc_nomatch", args{"npc": "Zzzz"}),
		tc("wow_trainer", "npc_named", args{"npc": "Einris", "character": charKnown}),
		ro(tc("wow_trainer", "real_npc_unknown", args{"npc": "Astaia"})),
	)

	add(
		tcs("wow_vendor", "default", args{}),
		tc("wow_vendor", "query_match", args{"query": "Arrow"}),
		tc("wow_vendor", "query_nomatch", args{"query": "Zzzz"}),
		tc("wow_vendor", "type_match", args{"type": "Weapon"}),
		tc("wow_vendor", "type_subtype", args{"type": "Bow"}),
		tc("wow_vendor", "min_quality", args{"minQuality": 1}),
		tc("wow_vendor", "affordable", args{"affordable": true}),
		tc("wow_vendor", "usable", args{"usable": true}),
		tc("wow_vendor", "equippable", args{"equippable": true}),
		tc("wow_vendor", "all_filters", args{"character": charKnown, "query": "Bow", "type": "Weapon", "minQuality": 0, "affordable": true, "usable": true, "equippable": true}),
		tc("wow_vendor", "unknown_character", args{"character": charUnknown}),
		tc("wow_vendor", "npc_match", args{"npc": "laird"}),
		tc("wow_vendor", "npc_nomatch", args{"npc": "Zzzz"}),
		tc("wow_vendor", "npc_with_filter", args{"npc": "Einris", "query": "Arrow", "character": charKnown}),
		tc("wow_vendor", "buyback_query", args{"query": "skinning"}),
		tc("wow_vendor", "buyback_query_nomatch", args{"query": "Zzzz"}),
		tc("wow_vendor", "npc_buyback_owner", args{"npc": "Einris"}),
		ro(tc("wow_vendor", "real_npc_lewis", args{"npc": "Lewis"})),
	)

	add(
		tcs("wow_compare_item", "name_match", args{"item": "Bow"}),
		tc("wow_compare_item", "id", args{"item": "2589"}),
		tc("wow_compare_item", "source_bags", args{"item": "Skinning", "source": "bags"}),
		tc("wow_compare_item", "source_bank", args{"item": "Cloth", "source": "bank"}),
		tc("wow_compare_item", "source_vendor", args{"item": "Bow", "source": "vendor"}),
		tc("wow_compare_item", "bad_source", args{"item": "Bow", "source": "moon"}),
		tc("wow_compare_item", "no_match", args{"item": "Zzzz"}),
		tc("wow_compare_item", "empty_item", args{"item": ""}),
		tc("wow_compare_item", "named", args{"item": "Bow", "character": charKnown}),
		tc("wow_compare_item", "unknown_character", args{"item": "Bow", "character": charUnknown}),
		tc("wow_compare_item", "missing_required", args{}),
	)

	// --- wow_changes (history: two older snapshots) ---
	add(
		tcs("wow_changes", "default", args{}),
		tc("wow_changes", "steps_1", args{"steps": 1}),
		tc("wow_changes", "steps_2", args{"steps": 2}),
		tc("wow_changes", "steps_too_many", args{"steps": 9}),
		tc("wow_changes", "since_hours_1", args{"sinceHours": 1.5}),
		tc("wow_changes", "since_hours_3", args{"sinceHours": 3}),
		tc("wow_changes", "since_hours_tiny", args{"sinceHours": 0.01}),
		tc("wow_changes", "steps_and_hours", args{"steps": 2, "sinceHours": 1.5}),
		tc("wow_changes", "named", args{"character": charKnown}),
		tc("wow_changes", "unknown_character", args{"character": charUnknown}),
		tc("wow_changes", "bad_type", args{"steps": "abc"}),
	)

	add(
		tcs("wow_quest_plan", "default", args{}),
		tc("wow_quest_plan", "zone_match", args{"zone": "Darkshore"}),
		tc("wow_quest_plan", "zone_nomatch", args{"zone": "Zzzz"}),
		tc("wow_quest_plan", "named", args{"character": charKnown}),
		tc("wow_quest_plan", "unknown_character", args{"character": charUnknown}),
	)

	add(
		tcs("wow_progress", "default", args{}),
		tc("wow_progress", "since_hours", args{"sinceHours": 1.5}),
		tc("wow_progress", "break_minutes", args{"breakMinutes": 10}),
		tc("wow_progress", "break_minutes_big", args{"breakMinutes": 600}),
		tc("wow_progress", "all_args", args{"character": charKnown, "sinceHours": 3, "breakMinutes": 45}),
		tc("wow_progress", "unknown_character", args{"character": charUnknown}),
	)

	add(
		tcs("wow_recipes", "default", args{}),
		tc("wow_recipes", "profession_match", args{"profession": "Leather"}),
		tc("wow_recipes", "profession_beast", args{"profession": "Beast Training"}),
		tc("wow_recipes", "profession_nomatch", args{"profession": "Zzzz"}),
		tc("wow_recipes", "query_match", args{"query": "Leather"}),
		tc("wow_recipes", "query_nomatch", args{"query": "Zzzz"}),
		tc("wow_recipes", "skill_up", args{"skillUp": true}),
		tc("wow_recipes", "craftable", args{"craftable": true}),
		tc("wow_recipes", "all_filters", args{"character": charKnown, "profession": "Leather", "query": "a", "skillUp": true, "craftable": true}),
		tc("wow_recipes", "unknown_character", args{"character": charUnknown}),
	)

	add(characterVariants("wow_mail")...)

	add(
		tcs("wow_talents", "default", args{}),
		tc("wow_talents", "tree_match", args{"tree": "Beast Mastery"}),
		tc("wow_talents", "tree_nomatch", args{"tree": "Zzzz"}),
		tc("wow_talents", "named", args{"character": charKnown}),
		tc("wow_talents", "unknown_character", args{"character": charUnknown}),
		tcs("wow_legacy", "default", args{}),
		tc("wow_legacy", "tree_match", args{"tree": "Adventure"}),
		tc("wow_legacy", "tree_nomatch", args{"tree": "Zzzz"}),
		tc("wow_legacy", "discovery", args{"discovery": true}),
		tc("wow_legacy", "named", args{"character": charKnown}),
		tc("wow_legacy", "unknown_character", args{"character": charUnknown}),
	)

	add(
		tcs("wow_auctions", "default", args{}),
		tc("wow_auctions", "query_match", args{"query": "Cloth"}),
		tc("wow_auctions", "query_nomatch", args{"query": "Zzzz"}),
		tc("wow_auctions", "mine", args{"mine": true}),
		tc("wow_auctions", "all_args", args{"character": charKnown, "query": "a", "mine": true}),
		tc("wow_auctions", "type_bow", args{"type": "Bow"}),
		tc("wow_auctions", "equippable", args{"equippable": true}),
		tc("wow_auctions", "affordable", args{"affordable": true}),
		tc("wow_auctions", "usable", args{"usable": true}),
		tc("wow_auctions", "all_filters", args{"character": charKnown, "query": "Bow", "type": "Weapon", "affordable": true, "usable": true, "equippable": true}),
		tc("wow_auctions", "unknown_character", args{"character": charUnknown}),
	)

	add(
		tcs("wow_group", "default", args{}),
		tc("wow_group", "player_match", args{"player": "Tankadin"}),
		tc("wow_group", "player_nomatch", args{"player": "Zzzz"}),
		tc("wow_group", "named", args{"character": charKnown}),
		tc("wow_group", "unknown_character", args{"character": charUnknown}),
	)

	add(
		tcs("wow_dungeons", "default", args{}),
		tc("wow_dungeons", "all", args{"all": true}),
		tc("wow_dungeons", "name_match", args{"name": "Deadmines"}),
		tc("wow_dungeons", "name_match_bfd", args{"name": "Blackfathom"}),
		tc("wow_dungeons", "name_nomatch", args{"name": "Zzzz"}),
		tc("wow_dungeons", "all_args", args{"character": charKnown, "all": true, "name": "a"}),
		tc("wow_dungeons", "unknown_character", args{"character": charUnknown}),
	)

	add(
		tcs("wow_export", "default", args{}),
		tc("wow_export", "format_discord", args{"format": "discord"}),
		tc("wow_export", "format_forum", args{"format": "forum"}),
		tc("wow_export", "format_text", args{"format": "text"}),
		tc("wow_export", "format_unknown", args{"format": "pdf"}),
		tc("wow_export", "short", args{"short": true}),
		tc("wow_export", "gold", args{"gold": true}),
		tc("wow_export", "english", args{"english": true}),
		tc("wow_export", "all_args", args{"character": charKnown, "format": "forum", "short": true, "gold": true, "english": true}),
		tc("wow_export", "unknown_character", args{"character": charUnknown}),
	)

	add(
		tcs("wow_search", "text_match", args{"query": "cloth"}),
		tc("wow_search", "quest_objective", args{"query": "death cap"}),
		tc("wow_search", "tooltip_match", args{"query": "damage"}),
		tc("wow_search", "trainer_and_spells", args{"query": "disengage"}),
		tc("wow_search", "many_sections", args{"query": "a"}),
		tc("wow_search", "no_match", args{"query": "Zzzz"}),
		tc("wow_search", "empty_query", args{"query": ""}),
		tc("wow_search", "named", args{"query": "Darnassus", "character": charKnown}),
		tc("wow_search", "unknown_character", args{"query": "cloth", "character": charUnknown}),
		tc("wow_search", "missing_required", args{}),
		tc("wow_search", "quest_rewards", args{"query": "dagger"}),
		tc("wow_search", "vendors_and_stable", args{"query": "owl"}),
		tc("wow_search", "remembered_npcs", args{"query": "arrow"}),
		tc("wow_search", "loot", args{"query": "linen"}),
	)

	add(characterVariants("wow_rested")...)
	add(characterVariants("wow_leftovers")...)

	// --- wow_gold_history (history: two older snapshots) ---
	add(
		tcs("wow_gold_history", "default", args{}),
		tc("wow_gold_history", "days_3", args{"days": 3}),
		tc("wow_gold_history", "days_negative", args{"days": -1}),
		tc("wow_gold_history", "days_big", args{"days": 365}),
		tc("wow_gold_history", "named", args{"character": charKnown}),
		tc("wow_gold_history", "unknown_character", args{"character": charUnknown}),
		tc("wow_gold_history", "bad_type", args{"days": "abc"}),
	)

	add(
		tcs("wow_ammo", "default", args{}),
		tc("wow_ammo", "hours_5", args{"hours": 5}),
		tc("wow_ammo", "hours_half", args{"hours": 0.5}),
		tc("wow_ammo", "hours_negative", args{"hours": -2}),
		tc("wow_ammo", "named", args{"character": charKnown}),
		tc("wow_ammo", "all_args", args{"character": charKnown, "hours": 1}),
		tc("wow_ammo", "unknown_character", args{"character": charUnknown}),
	)

	add(
		tcs("wow_account", "default", args{}),

		tcs("wow_cooldowns", "default", args{}),
		tc("wow_cooldowns", "all", args{"all": true}),
		tc("wow_cooldowns", "named", args{"character": charKnown}),
		tc("wow_cooldowns", "unknown_character", args{"character": charUnknown}),
		tc("wow_cooldowns", "all_args", args{"character": charKnown, "all": true}),

		tcs("wow_find_item", "name_match", args{"item": "Cloth"}),
		tc("wow_find_item", "id", args{"item": "2589"}),
		tc("wow_find_item", "no_match", args{"item": "Zzzz"}),
		tc("wow_find_item", "empty_item", args{"item": ""}),
		tc("wow_find_item", "named", args{"item": "Leather", "character": charKnown}),
		tc("wow_find_item", "unknown_character", args{"item": "Leather", "character": charUnknown}),
		tc("wow_find_item", "missing_required", args{}),
	)

	add(characterVariants("wow_bag_plan")...)
	add(characterVariants("wow_reagents")...)
	add(
		tcs("wow_journal", "default", args{}),
		tc("wow_journal", "sessions_1", args{"sessions": 1}),
		tc("wow_journal", "markdown", args{"markdown": true}),
		tc("wow_journal", "sessions_too_many", args{"sessions": 99}),
		tc("wow_journal", "named", args{"character": charKnown}),
		tc("wow_journal", "unknown_character", args{"character": charUnknown}),
		ir(tc("wow_journal", "all_args", args{"sessions": 2, "markdown": true})),
	)
	// Goals: the state of the goals file (temp) carries over between cases in table order.
	add(
		tcs("wow_goals", "default", args{}), // empty list
		ir(tc("wow_goal_set", "level", args{"type": "level", "target": 25, "note": "for Deadmines"})),
		ir(tc("wow_goal_set", "gold", args{"type": "gold", "target": 10})),
		ir(tc("wow_goal_set", "reputation_standing", args{"type": "reputation", "faction": "Darnassus", "standing": "Revered"})),
		ir(tc("wow_goal_set", "reputation_value", args{"type": "reputation", "faction": "Stormwind", "target": 9000})),
		ir(tc("wow_goal_set", "profession", args{"type": "profession", "name": "Mining", "target": 100})),
		ir(tc("wow_goal_set", "spell", args{"type": "spell", "name": "Aimed Shot"})),
		ir(tc("wow_goal_set", "item", args{"type": "item", "item": "Sharp Arrow", "target": 1500})),
		tc("wow_goal_set", "named_with_note", args{"type": "level", "target": 30, "character": charKnown, "note": "alt"}),
		tc("wow_goal_set", "bad_target", args{"type": "level", "target": 2.5}),
		tc("wow_goal_set", "bad_type", args{"type": "dream"}),
		tc("wow_goal_set", "missing_type", args{}),
		tc("wow_goal_set", "unknown_character", args{"type": "level", "target": 30, "character": charUnknown}),
		tc("wow_goal_set", "remove_and_type", args{"remove": "g1", "type": "level"}),
		ir(tc("wow_goals", "after_set", args{})),
		tc("wow_goals", "named", args{"character": charKnown}),
		tc("wow_goals", "unknown_character", args{"character": charUnknown}),
		ir(tc("wow_goal_set", "remove", args{"remove": "g2"})),
		tc("wow_goal_set", "remove_unknown", args{"remove": "g99"}),
		ir(tc("wow_goals", "after_remove", args{})),
	)
	add(characterVariants("wow_quest_rewards")...)
	add(
		tc("wow_quest_rewards", "quest_name", args{"quest": "defias"}),
		tc("wow_quest_rewards", "quest_id", args{"quest": "65"}),
		tc("wow_quest_rewards", "quest_nomatch", args{"quest": "Zzzz"}),
		tc("wow_quest_rewards", "weights", args{"weights": args{"agility": 1, "stamina": 0.5, "dps": 2}}),
		tc("wow_quest_rewards", "all_args", args{"character": charKnown, "quest": "Cave", "weights": args{"agility": 2}}),
		ro(tc("wow_quest_rewards", "real_weights", args{"weights": args{"agility": 1}})),
	)
	add(characterVariants("wow_shopping_list")...)
	add(
		tc("wow_shopping_list", "hours_5", args{"hours": 5}),
		tc("wow_shopping_list", "hours_negative", args{"hours": -1}),
		tc("wow_shopping_list", "all_args", args{"character": charKnown, "hours": 1}),
		ro(tc("wow_shopping_list", "real_hours_5", args{"hours": 5})),
	)

	// wow_compare_item with weights (the argument and, in the real scenario, the weights.json file)
	add(
		tc("wow_compare_item", "weights_arg", args{"item": "Skinning", "weights": args{"agility": 1, "stamina": 0.5, "dps": 2}}),
		tc("wow_compare_item", "weights_bad", args{"item": "Skinning", "weights": args{"agility": "a lot"}}),
	)

	add(characterVariants("wow_timeline")...)
	add(
		tc("wow_timeline", "kind_level_up", args{"kind": "level_up"}),
		tc("wow_timeline", "kind_death", args{"kind": "death"}),
		tc("wow_timeline", "bad_kind", args{"kind": "dance"}),
		tc("wow_timeline", "days_1", args{"days": 1}),
		tc("wow_timeline", "days_negative", args{"days": -1}),
		tc("wow_timeline", "limit_2", args{"limit": 2}),
		tc("wow_timeline", "limit_too_big", args{"limit": 9999}),
		ir(tc("wow_timeline", "all_args", args{"character": charKnown, "limit": 5, "kind": "new_spell", "days": 30})),
	)
	add(characterVariants("wow_buffs")...)
	add(characterVariants("wow_flights")...)
	add(characterVariants("wow_session_rating")...)

	add(characterVariants("wow_quest_route")...)
	add(characterVariants("wow_quest_lookup")...)
	add(
		tc("wow_quest_lookup", "quest_name", args{"quest": "cave"}),
		tc("wow_quest_lookup", "quest_id", args{"quest": "982"}),
		tc("wow_quest_lookup", "npc_name", args{"quest": "murkdeep"}),
		tc("wow_quest_lookup", "quest_nomatch", args{"quest": "Zzzz"}),
		tc("wow_quest_lookup", "all_args", args{"character": charKnown, "quest": "947"}),
	)
	add(characterVariants("wow_guide")...)
	add(characterVariants("wow_social")...)
	add(
		tc("wow_social", "include_offline", args{"includeOffline": true}),
		tc("wow_social", "include_offline_false", args{"includeOffline": false}),
		tc("wow_social", "bad_type", args{"includeOffline": "yes"}),
		ir(tc("wow_social", "all_args", args{"character": charKnown, "includeOffline": true})),
	)
	add(
		ir(tc("wow_compare_characters", "one_character_account", args{})), // the fixtures have one character: an error instead of a comparison
		tc("wow_compare_characters", "a_only", args{"a": charKnown}),
		tc("wow_compare_characters", "b_only", args{"b": charKnown}),
		tc("wow_compare_characters", "a_and_b", args{"a": charKnown, "b": "Other"}),
		tc("wow_compare_characters", "unknown_a", args{"a": charUnknown}),
		ir(tc("wow_compare_characters", "all_args", args{"a": charKnown, "b": charUnknown})),
	)
	add(
		tcs("wow_alt_mail", "default", args{}),
		tc("wow_alt_mail", "from", args{"from": charKnown}),
		tc("wow_alt_mail", "to", args{"to": charKnown}),
		tc("wow_alt_mail", "query", args{"query": "leather"}),
		tc("wow_alt_mail", "unknown_from", args{"from": charUnknown}),
		ir(tc("wow_alt_mail", "all_args", args{"from": charKnown, "to": charKnown, "query": "cloth"})),
	)
	add(
		tcs("wow_account_crafting", "default", args{}),
		tc("wow_account_crafting", "character", args{"character": charKnown}),
		tc("wow_account_crafting", "unknown_character", args{"character": charUnknown}),
		tc("wow_account_crafting", "profession", args{"profession": "Leather"}),
		tc("wow_account_crafting", "profession_nomatch", args{"profession": "Zzzz"}),
		tc("wow_account_crafting", "query", args{"query": "Light Leather"}),
		tc("wow_account_crafting", "craftable", args{"craftable": true}),
		tc("wow_account_crafting", "limit_1", args{"limit": 1}),
		ir(tc("wow_account_crafting", "all_args", args{"character": charKnown, "profession": "Leatherworking", "query": "Leather", "craftable": true, "limit": 3})),
	)
	add(characterVariants("wow_make_or_buy")...)
	add(
		tc("wow_make_or_buy", "profession", args{"profession": "Leather"}),
		tc("wow_make_or_buy", "profession_nomatch", args{"profession": "Zzzz"}),
		tc("wow_make_or_buy", "query", args{"query": "Gloves"}),
		tc("wow_make_or_buy", "sort_name", args{"sort": "name"}),
		tc("wow_make_or_buy", "sort_margin", args{"sort": "margin"}),
		tc("wow_make_or_buy", "limit_1", args{"limit": 1}),
		ir(tc("wow_make_or_buy", "all_args", args{"character": charKnown, "profession": "Leatherworking", "query": "Leather", "sort": "name", "limit": 2})),
	)

	// --- cases only for the real snapshot ---
	add(
		ro(tc("wow_compare_item", "real_skinning_knife", args{"item": "Skinning Knife"})),
		ro(tc("wow_compare_item", "real_mining_pick_weights_arg", args{"item": "Mining Pick", "weights": args{"agility": 2}})),
		ro(tc("wow_find_item", "real_arrow", args{"item": "Sharp Arrow"})),
		ro(tc("wow_search", "real_gnomeregan", args{"query": "gnomeregan"})),
		ro(tc("wow_section", "real_dungeons", args{"section": "dungeons"})),
		ro(tc("wow_talents", "real_survival", args{"tree": "Survival"})),
		ro(tc("wow_vendor", "real_recipe", args{"query": "Recipe"})),
		ro(tc("wow_quest_plan", "real_darkshore", args{"zone": "Darkshore"})),
		ro(tc("wow_reputation", "real_darnassus", args{"query": "Darn"})),
		ro(tc("wow_dungeons", "real_all", args{"all": true})),
		ro(tc("wow_ammo", "real_hours_5", args{"hours": 5})),
		ro(tc("wow_gold_history", "real_days_3", args{"days": 3})),
		ro(tc("wow_export", "real_forum", args{"format": "forum"})),
	)

	// --- MCP prompts: each with the variants of the character argument ---
	for _, name := range promptNames() {
		cs = append(cs,
			goldenCase{prompt: true, name: name, variant: "default", args: args{}, stale: true},
			goldenCase{prompt: true, name: name, variant: "named", args: args{"character": charKnown}},
			goldenCase{prompt: true, name: name, variant: "unknown_character", args: args{"character": charUnknown}},
		)
	}
	return cs
}

// promptNames are the names of the registered prompts (the reference list is in mcpserver,
// but the test checks consistency with prompts/list, so a new prompt gets cases anyway).
func promptNames() []string {
	return mcpserver.PromptNames()
}

type goldenScenario struct {
	dir     string                // subdirectory of testdata/golden
	fixture string                // file from testdata
	age     time.Duration         // server clock = dumpedAt + age
	history bool                  // whether the server has a history directory (with pre-seeded snapshots)
	missing bool                  // true = the Store points to a nonexistent file
	real    bool                  // true = the real dump from the game (fixtures.RealLua) instead of a testdata file
	ledger  bool                  // true = a long history (Series) with points older than the history files
	filter  func(goldenCase) bool // nil = all cases
}

var onlyStale = func(c goldenCase) bool { return c.stale }

// forLedger selects the history tools whose numbers reach back into the long history.
var forLedger = func(c goldenCase) bool {
	switch c.name + "/" + c.variant {
	case "wow_gold_history/days_big", "wow_progress/default", "wow_session_rating/default", "wow_timeline/default", "wow_auctions/default":
		return true
	}
	return false
}

// localizedTools are the tools whose conclusions depend on item classes, binding, stats, professions, chat
// parsing or English-only heuristics: on the deDE fixture they must say the same as on the English one
// (apart from the client's own text and the localeNote of English-only heuristics).
var localizedTools = map[string]bool{
	"wow_status": true, "wow_character": true, "wow_bags": true, "wow_equipment": true, "wow_pet": true,
	"wow_bag_plan": true, "wow_reagents": true, "wow_leftovers": true, "wow_ammo": true, "wow_shopping_list": true,
	"wow_auctions": true, "wow_recipes": true, "wow_journal": true, "wow_buffs": true, "wow_dungeons": true,
	"wow_flights": true, "wow_bank": true, "wow_vendor": true, "wow_compare_item": true, "wow_trainer": true,
}

// forLocalized selects, for the deDE scenario, the default cases of localizedTools and a few cases with arguments
// that exercise class IDs (a type filter in English words) and stat tokens.
var forLocalized = func(c goldenCase) bool {
	if c.prompt || !localizedTools[c.name] {
		return false
	}
	switch c.name + "/" + c.variant {
	case "wow_bank/type_match", "wow_vendor/type_match", "wow_compare_item/name_match":
		return true
	}
	return c.variant == "default"
}

// forRealSnapshot selects the cases for the real dump: everything with default arguments
// (prompts too) and the cases written only for it.
var forRealSnapshot = func(c goldenCase) bool { return c.variant == "default" || c.realOnly || c.inReal }

func goldenScenarios() []goldenScenario {
	fixtures := []string{"WowMcp", "WowMcp_legacy", "WowMcp_forever"}
	var out []goldenScenario
	for _, f := range fixtures {
		// fresh snapshot (30 min): the full set of cases
		out = append(out, goldenScenario{dir: f, fixture: f, age: 30 * time.Minute, history: true})
		// stale snapshot (3 h 20 min): the WARNING path
		out = append(out, goldenScenario{dir: f + "_stale", fixture: f, age: 3*time.Hour + 20*time.Minute, history: true, filter: onlyStale})
	}
	// a German client (deDE mock, addon 0.9.0): the tools whose logic used to read English client text
	out = append(out, goldenScenario{dir: "WowMcp_dede", fixture: "WowMcp_dede", age: 30 * time.Minute, history: true, filter: forLocalized})
	// the real dump from the game (addon 0.5.0, Forever 1.60.1) with a history built from itself
	out = append(out, goldenScenario{dir: "real", real: true, age: 30 * time.Minute, history: true, filter: forRealSnapshot})
	// a long history database with sessions from weeks before the oldest history file
	out = append(out, goldenScenario{dir: "WowMcp_forever_ledger", fixture: "WowMcp_forever", age: 30 * time.Minute, history: true, ledger: true, filter: forLedger})
	// history disabled: history-based tools must report an error
	out = append(out, goldenScenario{dir: "WowMcp_forever_nohistory", fixture: "WowMcp_forever", age: 30 * time.Minute, filter: onlyStale})
	// missing dump file: loading errors
	out = append(out, goldenScenario{dir: "missing_file", fixture: "WowMcp", age: 30 * time.Minute, history: true, missing: true, filter: onlyStale})
	return out
}

func TestGolden(t *testing.T) {
	pinGlobals(t)
	cases := goldenCases()

	if *updateGolden {
		if err := os.RemoveAll(goldenRoot); err != nil {
			t.Fatal(err)
		}
	}

	// Tool and prompt lists: once, regardless of the scenario.
	var listed listing
	t.Run("lists", func(t *testing.T) {
		env := newGoldenEnv(t, goldenScenarios()[0])
		listed = env.listing(t)
		checkGolden(t, "tools_list.json", listed.toolsJSON)
		checkGolden(t, "prompts_list.json", listed.promptsJSON)
	})
	if t.Failed() {
		return
	}
	checkCoverage(t, cases, listed)

	for _, sc := range goldenScenarios() {
		sc := sc
		t.Run(sc.dir, func(t *testing.T) {
			env := newGoldenEnv(t, sc)
			var want []string
			for _, c := range cases {
				if (sc.filter != nil && !sc.filter(c)) || (c.realOnly && !sc.real) {
					continue
				}
				want = append(want, c.file())
				t.Run(strings.TrimSuffix(c.file(), ".txt"), func(t *testing.T) {
					checkGolden(t, filepath.Join(sc.dir, c.file()), env.run(t, c))
				})
			}
			checkNoOrphans(t, sc.dir, want)
		})
	}
}

// pinGlobals sets a deterministic time zone (the clock and the "stale snapshot" threshold
// are given to the server in newGoldenEnv).
func pinGlobals(t *testing.T) {
	t.Helper()
	oldLocal := time.Local
	time.Local = time.UTC
	t.Cleanup(func() {
		time.Local = oldLocal
	})
}

// listing holds the normalized tools/list and prompts/list responses.
type listing struct {
	toolsJSON, promptsJSON string
	tools                  []*mcp.Tool
	prompts                []*mcp.Prompt
}

// goldenEnv is a connected client + server with one snapshot and (optionally) a history.
type goldenEnv struct {
	cs       *mcp.ClientSession
	scrubber pathScrubber
}

func newGoldenEnv(t *testing.T, sc goldenScenario) *goldenEnv {
	t.Helper()
	tmp := t.TempDir()
	base := int64(fixtures.ForeverDumpedAt)
	if sc.real {
		base = fixtures.RealDumpedAt
	}

	// A copy of the fixture with a pinned modification time.
	file := filepath.Join(tmp, storage.SavedVariablesFile)
	if sc.missing {
		file = filepath.Join(tmp, "missing", storage.SavedVariablesFile)
	} else {
		var src []byte
		if sc.real {
			src = fixtures.RealLua(t)
		} else {
			var err error
			if src, err = os.ReadFile(filepath.Join("testdata", sc.fixture+".lua")); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(file, src, 0o644); err != nil {
			t.Fatal(err)
		}
		mod := time.Unix(base+5, 0)
		if err := os.Chtimes(file, mod, mod); err != nil {
			t.Fatal(err)
		}
	}

	// The server clock and the stale snapshot threshold (default --stale-minutes=120).
	fresh := snapshot.Freshness{
		Now:        func() time.Time { return time.Unix(base, 0).Add(sc.age) },
		StaleAfter: 120 * time.Minute,
	}

	store := &storage.Store{File: file}
	histDir := filepath.Join(tmp, "history")
	var history *storage.History
	if sc.history {
		history = &storage.History{Dir: histDir, Keep: 50}
		if !sc.missing {
			seedHistory(t, file, history)
		}
	}

	goalsPath := filepath.Join(tmp, "goals.json")
	if sc.real { // a weights file for the Hunter class: wow_compare_item in the real scenario gets weightedDelta without an argument
		weights := `{"classes": {"HUNTER": {"agility": 1, "attackPower": 0.5, "stamina": 0.2, "dps": 3}}}`
		if err := os.WriteFile(filepath.Join(tmp, "weights.json"), []byte(weights), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var options []func(*mcpserver.Config)
	if sc.ledger {
		long := seedLedger(t, file)
		options = append(options, func(c *mcpserver.Config) { c.Ledger = long })
	}
	server := newServer(store, history, fresh, goalsPath, options...)
	ct, st := mcp.NewInMemoryTransports()
	ctx := context.Background()
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "golden-client", Version: "0"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cs.Close()
		ss.Close()
	})

	scrubber := newPathScrubber(map[string]string{goalsPath: "<GOALS>", file: "<FILE>", histDir: "<HISTORY>", tmp: "<TMP>"})
	return &goldenEnv{cs: cs, scrubber: scrubber}
}

// pathScrubber makes responses independent of the machine: absolute paths become markers, and on Windows the
// path separators after a marker and the OS error texts are written as on Unix, so one golden file fits every OS.
type pathScrubber struct {
	paths    *strings.Replacer
	afterDir *regexp.Regexp
	osErrors *strings.Replacer
}

func newPathScrubber(markers map[string]string) pathScrubber {
	var pairs []string
	// A path appears raw, JSON-escaped once (backslashes doubled) or twice (JSON text inside a JSON string).
	for _, level := range []int{2, 1, 0} {
		for path, marker := range markers {
			escaped := path
			for i := 0; i < level; i++ {
				escaped = strings.ReplaceAll(escaped, `\`, `\\`)
			}
			pairs = append(pairs, escaped, marker)
		}
	}
	// Longer paths first, so that <TMP> never takes a prefix of <FILE> or <HISTORY>.
	type pair struct{ old, new string }
	list := make([]pair, 0, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		list = append(list, pair{pairs[i], pairs[i+1]})
	}
	sort.SliceStable(list, func(i, j int) bool { return len(list[i].old) > len(list[j].old) })
	ordered := make([]string, 0, len(pairs))
	for _, p := range list {
		ordered = append(ordered, p.old, p.new)
	}
	return pathScrubber{
		paths:    strings.NewReplacer(ordered...),
		afterDir: regexp.MustCompile(`(<GOALS>|<FILE>|<HISTORY>|<TMP>)((?:\\+[^\\\s"]+)+)`),
		osErrors: strings.NewReplacer(
			"GetFileAttributesEx ", "stat ",
			"CreateFile ", "open ",
			"The system cannot find the path specified.", "no such file or directory",
			"The system cannot find the file specified.", "no such file or directory",
		),
	}
}

func (s pathScrubber) Replace(text string) string {
	text = s.paths.Replace(text)
	if runtime.GOOS != "windows" {
		return text
	}
	text = s.afterDir.ReplaceAllStringFunc(text, func(m string) string {
		return regexp.MustCompile(`\\+`).ReplaceAllString(m, "/")
	})
	// The only OS error in the golden files is a missing dump; Windows words it in the system language.
	return notFound.ReplaceAllString(s.osErrors.Replace(text), "${1}no such file or directory")
}

var notFound = regexp.MustCompile(`(stat <FILE>: )[^"\n]+\.`)

// seedHistory saves, through the History API, two older snapshots of the current character:
// a lower level, less XP and gold, fewer quests, a different bag composition, a shorter played time.
// This gives wow_changes and wow_progress something to compare against.
func seedHistory(t *testing.T, file string, h *storage.History) {
	t.Helper()
	db, err := (&storage.Store{File: file}).Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range db.CharacterNames() {
		cur := db.Characters[name]
		dumped, ok := cur.DumpedAt()
		if !ok {
			continue
		}
		older := []struct {
			back  int64
			shape func(map[string]any)
		}{
			{7200, func(s map[string]any) { // two hours ago: a level-up since then
				mutateCharacter(s, -1, -300, -2500)
				dropFirstQuest(s)
				dropLastCompleted(s)
				dropFirstBagItem(s)
				addBagItem(s, "Old Scroll", 99999)
				shiftPlayed(s, -7200)
				shiftAmmo(s, 400)
				resetDeaths(s)
			}},
			{3600, func(s map[string]any) { // an hour ago: the same level, less XP
				mutateCharacter(s, 0, -100, -1000)
				addBagItem(s, "Old Scroll", 99999)
				shiftPlayed(s, -3600)
				shiftAmmo(s, 200)
				resetDeaths(s)
			}},
		}
		for _, o := range older {
			s := cloneSnapshot(t, cur)
			o.shape(s)
			s["dumpedAt"] = dumped - o.back
			if _, ok := s["dumpedAtText"]; ok {
				s["dumpedAtText"] = time.Unix(dumped-o.back, 0).UTC().Format("2006-01-02 15:04:05")
			}
			if err := h.Save(name, s); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// fakeLedger is a long history that already holds older points, logged events and auction prices; it records nothing.
type fakeLedger struct {
	points map[string][]progress.Point
	events map[string][]eventlog.Entry
	prices map[string][]auctions.Observation // by realm
}

func (l fakeLedger) Points(character string) ([]progress.Point, error) {
	return l.points[character], nil
}
func (l fakeLedger) Record(*snapshot.Database, progress.History) {}

func (l fakeLedger) Events(character string, from, to int64) ([]eventlog.Entry, error) {
	var out []eventlog.Entry
	for _, e := range l.events[character] {
		if e.At >= from && e.At <= to {
			out = append(out, e)
		}
	}
	return out, nil
}

func (l fakeLedger) Prices(realm string, items []string) ([]auctions.Observation, error) {
	var out []auctions.Observation
	for _, o := range l.prices[realm] {
		if slices.Contains(items, o.Item) {
			out = append(out, o)
		}
	}
	return out, nil
}

// seedLedger gives every character two one-hour sessions, 20 and 10 days before the dump, with a lower level,
// less gold and less play time than the oldest history file (seedHistory, two hours back).
func seedLedger(t *testing.T, file string) fakeLedger {
	t.Helper()
	db, err := (&storage.Store{File: file}).Load()
	if err != nil {
		t.Fatal(err)
	}
	const day, hour = int64(86400), int64(3600)
	out := fakeLedger{points: map[string][]progress.Point{}, events: map[string][]eventlog.Entry{}, prices: map[string][]auctions.Observation{}}
	for _, name := range db.CharacterNames() {
		cur, ok := progress.PointOf(db.Characters[name])
		if !ok {
			continue
		}
		out.events[name] = seedEvents(cur)
		realm := snapshot.RealmOf(name)
		out.prices[realm] = append(out.prices[realm], seedPrices(db.Characters[name], cur.At)...)
		for i, back := range []int64{20*day + hour, 20 * day, 10*day + hour, 10 * day} {
			p := cur
			p.At = cur.At - back
			p.Level = max(cur.Level-int64(3-i/2), 1)
			p.XP = int64(100 + 600*(i%2))
			p.Money = cur.Money * int64(i+1) / 10
			p.CompletedQuests = cur.CompletedQuests * int64(i+1) / 10
			if cur.Played >= 0 {
				p.Played = cur.Played - 2*hour - int64(4-i)*hour
			}
			out.points[name] = append(out.points[name], p)
		}
	}
	return out
}

// seedEvents logs the level-up seedHistory shows between its two files (earlier than the file that shows it), the
// level before it and a quest turned in days before the oldest history file.
func seedEvents(cur progress.Point) []eventlog.Entry {
	const day = int64(86400)
	return []eventlog.Entry{
		{At: cur.At - 3*day, Kind: eventlog.KindQuestAccepted, QuestID: 90001, Title: "Seeded Errand"},
		{At: cur.At - 2*day - 3600, Kind: eventlog.KindLevel, Level: max(cur.Level-1, 1)},
		{At: cur.At - 2*day, Kind: eventlog.KindQuestTurnedIn, QuestID: 90001, XP: 450, Copper: 120},
		{At: cur.At - 2*day + 60, Kind: eventlog.KindMoney, Copper: cur.Money},
		{At: cur.At - 5000, Kind: eventlog.KindLevel, Level: cur.Level},
	}
}

// seedPrices gives every saved auction price two older visits at a higher buyout, 20 and 10 days before the dump,
// plus the price itself, as the real ledger would hold it.
func seedPrices(s snapshot.Snapshot, at int64) []auctions.Observation {
	const day = int64(86400)
	var out []auctions.Observation
	for _, o := range auctions.Observations(s) {
		out = append(out, auctions.Observation{Item: o.Item, SeenAt: at - 20*day, MinBuyout: o.MinBuyout * 4 / 3},
			auctions.Observation{Item: o.Item, SeenAt: at - 10*day, MinBuyout: o.MinBuyout * 6 / 5}, o)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].SeenAt < out[j].SeenAt })
	return out
}

// cloneSnapshot makes a deep copy through JSON, the same way History saves and loads snapshots.
func cloneSnapshot(t *testing.T, s snapshot.Snapshot) snapshot.Snapshot {
	t.Helper()
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var raw map[string]any
	if err := dec.Decode(&raw); err != nil {
		t.Fatal(err)
	}
	return storage.NormalizeJSON(raw).(map[string]any)
}

func mutateCharacter(s map[string]any, dLevel, dXP, dMoney int64) {
	c := snapshot.AsMap(s["character"])
	if c == nil {
		return
	}
	add := func(key string, d int64, min int64) {
		if n, ok := snapshot.AsInt(c[key]); ok {
			n += d
			if n < min {
				n = min
			}
			c[key] = n
		}
	}
	add("level", dLevel, 1)
	add("xp", dXP, 0)
	add("moneyCopper", dMoney, 0)
}

func dropFirstQuest(s map[string]any) {
	if q := snapshot.AsList(s["quests"]); len(q) > 0 {
		s["quests"] = q[1:]
	}
}

func dropLastCompleted(s map[string]any) {
	if q := snapshot.AsList(s["completedQuests"]); len(q) > 0 {
		s["completedQuests"] = q[:len(q)-1]
	}
}

func firstBagWithItems(s map[string]any) map[string]any {
	for _, raw := range snapshot.AsList(s["bags"]) {
		if b := snapshot.AsMap(raw); len(snapshot.AsList(b["items"])) > 0 {
			return b
		}
	}
	return nil
}

func dropFirstBagItem(s map[string]any) {
	if b := firstBagWithItems(s); b != nil {
		b["items"] = snapshot.AsList(b["items"])[1:]
	}
}

func addBagItem(s map[string]any, name string, id int64) {
	if b := firstBagWithItems(s); b != nil {
		items := snapshot.AsList(b["items"])
		b["items"] = append(items, map[string]any{"id": id, "name": name, "count": int64(1), "quality": int64(1)})
	}
}

// shiftAmmo adds arrows to the ammo slot: there used to be more of them, so consumption is visible.
func shiftAmmo(s map[string]any, d int64) {
	for _, raw := range snapshot.AsList(s["equipment"]) {
		if item := snapshot.AsMap(raw); item["slot"] == "ammo" {
			n, _ := snapshot.AsInt(item["count"])
			item["count"] = n + d
		}
	}
}

// resetDeaths sets the death counter back to zero (the death appeared only in a later session).
func resetDeaths(s map[string]any) {
	if d := snapshot.AsMap(s["deaths"]); d != nil {
		d["count"], d["recent"] = int64(0), []any{}
	}
}

func shiftPlayed(s map[string]any, d int64) {
	p := snapshot.AsMap(s["played"])
	if p == nil {
		return
	}
	for _, key := range []string{"totalSeconds", "levelSeconds"} {
		if n, ok := snapshot.AsInt(p[key]); ok && n+d > 0 {
			p[key] = n + d
		}
	}
}

// listing fetches tools/list and prompts/list and turns them into stable JSON.
func (e *goldenEnv) listing(t *testing.T) listing {
	t.Helper()
	ctx := context.Background()
	tools, err := e.cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	prompts, err := e.cs.ListPrompts(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	return listing{
		toolsJSON:   prettyJSON(t, tools.Tools),
		promptsJSON: prettyJSON(t, prompts.Prompts),
		tools:       tools.Tools,
		prompts:     prompts.Prompts,
	}
}

func prettyJSON(t *testing.T, v any) string {
	t.Helper()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return string(data) + "\n"
}

// run executes a case and returns the golden file text.
func (e *goldenEnv) run(t *testing.T, c goldenCase) string {
	t.Helper()
	ctx := context.Background()
	var b strings.Builder
	argsJSON, err := json.Marshal(c.args)
	if err != nil {
		t.Fatal(err)
	}
	kind := "tool"
	if c.prompt {
		kind = "prompt"
	}
	fmt.Fprintf(&b, "# %s %s variant=%s\n# args: %s\n", kind, c.name, c.variant, argsJSON)

	if c.prompt {
		strArgs := map[string]string{}
		for k, v := range c.args {
			strArgs[k] = fmt.Sprint(v)
		}
		res, err := e.cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: c.name, Arguments: strArgs})
		if err != nil {
			fmt.Fprintf(&b, "# PROTOCOL ERROR: %v\n", err)
			return e.scrubber.Replace(b.String())
		}
		fmt.Fprintf(&b, "# description: %s\n", res.Description)
		for i, m := range res.Messages {
			fmt.Fprintf(&b, "--- message[%d] role=%s ---\n%s\n", i, m.Role, renderContent(m.Content))
		}
		return e.scrubber.Replace(b.String())
	}

	res, err := e.cs.CallTool(ctx, &mcp.CallToolParams{Name: c.name, Arguments: c.args})
	if err != nil {
		fmt.Fprintf(&b, "# PROTOCOL ERROR: %v\n", err)
		return e.scrubber.Replace(b.String())
	}
	fmt.Fprintf(&b, "# isError: %v\n", res.IsError)
	if res.StructuredContent != nil {
		fmt.Fprintf(&b, "# structuredContent: %s\n", mustJSON(res.StructuredContent))
	}
	for i, content := range res.Content {
		fmt.Fprintf(&b, "--- content[%d] ---\n%s\n", i, renderContent(content))
	}
	return e.scrubber.Replace(b.String())
}

func mustJSON(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("<JSON error: %v>", err)
	}
	return string(data)
}

func renderContent(c mcp.Content) string {
	if tc, ok := c.(*mcp.TextContent); ok {
		return tc.Text
	}
	return fmt.Sprintf("<%T> %s", c, mustJSON(c))
}

func checkGolden(t *testing.T, rel, got string) {
	t.Helper()
	path := filepath.Join(goldenRoot, rel)
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing golden file %s (%v); run: go test -run Golden -update", path, err)
	}
	if string(want) != got {
		t.Errorf("the response differs from %s\n%s\nIf the change is intended: go test -run Golden -update, then review git diff testdata/golden",
			path, firstDiff(string(want), got))
	}
}

// firstDiff describes the first difference in lines (with context) so the error is readable.
func firstDiff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	n := len(w)
	if len(g) < n {
		n = len(g)
	}
	for i := 0; i < n; i++ {
		if w[i] != g[i] {
			return fmt.Sprintf("first difference at line %d:\n  want: %s\n  got:  %s", i+1, clip(w[i]), clip(g[i]))
		}
	}
	return fmt.Sprintf("the responses have different lengths: want %d lines, got %d lines", len(w), len(g))
}

func clip(s string) string {
	if len(s) > 300 {
		return s[:300] + "..."
	}
	return s
}

// checkNoOrphans reports golden files that no longer have a case.
func checkNoOrphans(t *testing.T, dir string, want []string) {
	t.Helper()
	if *updateGolden {
		return
	}
	entries, err := os.ReadDir(filepath.Join(goldenRoot, dir))
	if err != nil {
		t.Errorf("missing golden directory %s: %v", dir, err)
		return
	}
	known := map[string]bool{}
	for _, w := range want {
		known[w] = true
	}
	for _, e := range entries {
		if !known[e.Name()] {
			t.Errorf("orphaned golden file %s/%s (no case); delete it or run -update", dir, e.Name())
		}
	}
}

// checkCoverage makes sure every registered tool and prompt has cases,
// and every tool input field is used in at least one.
func checkCoverage(t *testing.T, cases []goldenCase, l listing) {
	t.Helper()
	usedArgs := map[string]map[string]bool{}
	promptCases := map[string]bool{}
	toolCases := map[string]bool{}
	for _, c := range cases {
		if c.prompt {
			promptCases[c.name] = true
			continue
		}
		toolCases[c.name] = true
		if usedArgs[c.name] == nil {
			usedArgs[c.name] = map[string]bool{}
		}
		for k := range c.args {
			usedArgs[c.name][k] = true
		}
	}

	registeredTools := map[string]bool{}
	for _, tool := range l.tools {
		registeredTools[tool.Name] = true
		if !toolCases[tool.Name] {
			t.Errorf("tool %s has no case in goldenCases", tool.Name)
		}
		var schema struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal([]byte(mustJSON(tool.InputSchema)), &schema); err != nil {
			t.Errorf("schema %s: %v", tool.Name, err)
			continue
		}
		var missing []string
		for prop := range schema.Properties {
			if !usedArgs[tool.Name][prop] {
				missing = append(missing, prop)
			}
		}
		sort.Strings(missing)
		if len(missing) > 0 {
			t.Errorf("tool %s: no case uses the fields %v", tool.Name, missing)
		}
	}
	for name := range toolCases {
		if !registeredTools[name] {
			t.Errorf("a case for an unregistered tool %s", name)
		}
	}

	registeredPrompts := map[string]bool{}
	for _, p := range l.prompts {
		registeredPrompts[p.Name] = true
		if !promptCases[p.Name] {
			t.Errorf("prompt %s has no case in goldenCases", p.Name)
		}
	}
	for name := range promptCases {
		if !registeredPrompts[name] {
			t.Errorf("a case for an unregistered prompt %s", name)
		}
	}
}
