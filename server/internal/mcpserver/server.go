// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 Piekasz (GitHub: Piekario). Author attribution must be kept: see NOTICE.

// Package mcpserver is the MCP adapter: registration of tools and prompts, mapping
// of arguments to domain queries and writing responses (JSON with a WARNING line for stale
// data). Game logic lives in the internal/game packages; here we only wire them together.
package mcpserver

import (
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"wowmcp/internal/game/auctions"
	"wowmcp/internal/game/eventlog"
	"wowmcp/internal/game/goals"
	"wowmcp/internal/game/items"
	"wowmcp/internal/game/progress"
	"wowmcp/internal/snapshot"
)

// Snapshots is the snapshot source: it reloads the SavedVariables file on every call.
type Snapshots interface {
	Load() (*snapshot.Database, error)
}

// History is the dump history: the progress tools read it, and every dump the server reads is recorded in it.
type History interface {
	progress.History
	Record(db *snapshot.Database)
}

// Ledger is the long history: it keeps the numbers, logged events and auction prices of every dump it is given,
// beyond the History limit.
type Ledger interface {
	progress.Series
	eventlog.Store
	auctions.PriceLog
	Record(db *snapshot.Database, files progress.History)
}

// Config holds the server dependencies, wired in main.
type Config struct {
	Name, Version string
	Snapshots     Snapshots
	History       History // nil = history disabled
	Ledger        Ledger  // nil = no long history
	HistoryDir    string  // shown in wow_status; "" when history is disabled
	Freshness     snapshot.Freshness
	Goals         goals.Store        // nil = goals disabled (wow_goals and wow_goal_set return an error)
	GoalsFile     string             // shown in wow_status
	Weights       items.WeightsStore // nil = no weights file (wow_compare_item)
	// MaxResponseKB caps the size of one tool answer (flag --max-response-kb); bigger answers become a compact
	// envelope with a narrowing hint. 0 = no cap.
	MaxResponseKB int
	Extras        Extras      // nil = other-addon SavedVariables ingestion disabled (wow_rxp, wow_quest_story, ...)
	ExportDir     string      // where wow_export_csv writes; "" disables the tool
	Slots         SlotChannel // nil = request slots disabled (wow_waypoint_set, wow_map_focus, ...)
	Wiki          WikiStore   // nil = wiki disabled (wow_wiki_*)
	LiveData      Live        // nil = live strip channel disabled (wow_refresh, wow_live)
}

const instructions = "The tools read World of Warcraft: Forever character state saved by the WowMcp addon. " +
	"The data refreshes only after /mcp reload in game, so check snapshot.ageMinutes and ask " +
	"for a refresh when it is old (the tools then add a WARNING line themselves). " +
	"If a tool errors, returns nothing or the data looks wrong, call wow_doctor (or wow_status) first. " +
	"An answer over the size limit comes back as {truncated: true, hint, topLevel, partial}: follow the hint and ask for less. " +
	"Tools: " +
	"status and version: wow_status (also alerts: mail, auctions, gear, bags and which account was read), wow_doctor (the first thing to call when something looks wrong: ordered checks with fixes), wow_section (any section, big ones in pieces with path and limit); " +
	"character: wow_character (with the player's note and deaths), wow_equipment, wow_bags, wow_spells, wow_talents, wow_legacy (Legacy perks, points and challenges), " +
	"wow_reputation, wow_pet (also missing ranks, training point plan and stable), wow_quests; " +
	"windows from the last visit: wow_trainer and wow_vendor (optionally npc: up to 10 remembered), wow_bank, wow_mail, wow_auctions, wow_recipes; " +
	"shopping and quests: wow_shopping_list (trainer, repairs, ammo, food, who sells it, when to return to town), wow_quest_rewards (which quest reward to pick); " +
	"planning and comparisons: wow_quest_plan, wow_dungeons, wow_group, wow_compare_item, wow_cooldowns; " +
	"progress: wow_changes (what changed since the previous snapshot), wow_progress (XP and gold pace, time to level, deaths in the session), " +
	"wow_gold_history, wow_rested, wow_ammo, wow_reagents (stocks of ammo, food, bandages, potions and their consumption), " +
	"wow_journal (session journal and records, optionally Markdown), wow_timeline (timeline: level-ups, skills, gear, quests, deaths), wow_session_rating (pace of the last session versus the median); " +
	"goals: wow_goals (goal progress and ETA), wow_goal_set (the only tool that writes: adds or removes a goal in the server's goals file); " +
	"bags: wow_bag_plan (what to do with full bags: keep, bank, sell, auction; destroys nothing); " +
	"account and search: wow_account, wow_find_item, wow_search (text across one character's whole snapshot), wow_leftovers (leftover quest items), wow_compare_characters (two characters side by side), wow_alt_mail (what to mail between characters), wow_account_crafting (what the account can craft from combined materials), wow_make_or_buy (craft or buy on the auction house); " +
	"auras and flight: wow_buffs (buffs, debuffs, missing class buffs), wow_flights (flight points versus quest zones), wow_quest_route (suggested order of zones with quests); quest locations and guide: wow_quest_lookup (where a quest starts and ends, from the quest database installed in the game; always use it instead of answering quest locations from memory), wow_guide (next steps of the player's Guidelime / TUGs leveling guide); friends and guild: wow_social; " +
	"wow_export (a summary to paste elsewhere); " +
	"other installed addons: wow_rxp (RestedXP guide progress and quest objectives), wow_quest_story (Questie quest timeline: accepts, completions, abandons with level and time), " +
	"wow_economy and wow_price (Profiteer auction prices with history, income/expense ledger, gold curve, alt inventories), wow_map_pins (OpenMap pins and TomTom waypoints), " +
	"wow_export_csv (writes a CSV of prices, ledger, inventory or history in the server's export directory); " +
	"remote requests: wow_waypoint_set, wow_map_focus and wow_waypoints_clear queue a request the in-game addon picks up within ~15 s and executes through TomTom/OpenMap (wow_slots_status shows the channel); " +
	"live data without a /reload: wow_refresh asks the addon for a fresh snapshot (only changed or requested sections travel; expect a few seconds) and wow_live reads a section of it with its own age — prefer these over a stale file snapshot; " +
	"wiki: wow_wiki_list, wow_wiki_read, wow_wiki_search and wow_wiki_write keep the assistant's own min-maxing notes as markdown in the server's directory; " +
	"player-facing board: wow_board_post puts notes/tasks/routes (with clickable item and waypoint references) on an in-game panel the player opens with /mcp board, wow_board_clear removes them and wow_board reads back what is posted and what the player ticked done. " +
	"Ready-made prompts: session_plan, trainer_shopping, what_to_sell, gear_upgrades, session_summary, account_overview, " +
	"profession_progress, talent_planning, what_to_auction, dungeon_finder, before_logout, first_run, pet_care, week_in_game. " +
	"Answer in the user's language and keep in-game names (items, spells, zones) in English. " +
	"The server performs no gameplay actions; its only reach into the game is the queued waypoint/map-focus requests above, " +
	"and it writes the snapshot history, goals, wiki pages and CSV exports in its own directory plus the WowMcp_S request-slot files in Interface/AddOns."

func New(cfg Config) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: cfg.Name, Version: cfg.Version}, &mcp.ServerOptions{
		Instructions: instructions,
	})
	svc := &service{Config: cfg}
	if cfg.History != nil {
		svc.progress.History = cfg.History
	}
	if cfg.Ledger != nil {
		svc.progress.Series = cfg.Ledger
		svc.progress.Log = cfg.Ledger
	}
	limiter := responseCap{limitKB: cfg.MaxResponseKB, args: map[string][]string{}}
	for _, t := range svc.tools() {
		t.register(server)
		limiter.args[t.name] = t.args
	}
	server.AddReceivingMiddleware(limiter.middleware)
	svc.registerResources(server)
	for _, p := range prompts {
		p.register(server, svc)
	}
	return server
}

type service struct {
	Config
	progress progress.Tracker
	// goalsMu serializes wow_goal_set: adding or removing a goal reads, changes and rewrites the whole goals file,
	// and MCP requests may arrive concurrently (two adds at once would otherwise lose one goal).
	goalsMu sync.Mutex
}

// loadDatabase reads the dump and records it in the history and the ledger, so a tool that reads them sees this dump too.
func (s *service) loadDatabase() (*snapshot.Database, error) {
	db, err := s.Snapshots.Load()
	if err != nil {
		return nil, err
	}
	if s.History != nil {
		adoptFormerKeys(db, s.History, s.Ledger)
		s.History.Record(db)
	}
	if s.Ledger != nil {
		s.Ledger.Record(db, s.progress.History)
	}
	return db, nil
}

// pricePast is the long price history of the character's realm; empty without a ledger.
func (s *service) pricePast(character string) auctions.Past {
	if s.Ledger == nil {
		return auctions.Past{}
	}
	return auctions.Past{Log: s.Ledger, Realm: snapshot.RealmOf(character)}
}

// load reads the file and returns the snapshot of the given character (empty name = most recently dumped).
func (s *service) load(character string) (string, snapshot.Snapshot, error) {
	db, err := s.loadDatabase()
	if err != nil {
		return "", nil, err
	}
	return db.Lookup(character)
}
