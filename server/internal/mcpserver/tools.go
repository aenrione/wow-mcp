// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 Piekasz (GitHub: Piekario). Author attribution must be kept: see NOTICE.

package mcpserver

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"wowmcp/internal/game/accountcraft"
	"wowmcp/internal/game/addon"
	"wowmcp/internal/game/alerts"
	"wowmcp/internal/game/altmail"
	"wowmcp/internal/game/ammo"
	"wowmcp/internal/game/auctions"
	"wowmcp/internal/game/bagplan"
	"wowmcp/internal/game/buffs"
	"wowmcp/internal/game/character"
	"wowmcp/internal/game/compare"
	"wowmcp/internal/game/cooldowns"
	"wowmcp/internal/game/dungeons"
	"wowmcp/internal/game/export"
	"wowmcp/internal/game/flights"
	"wowmcp/internal/game/goals"
	"wowmcp/internal/game/group"
	"wowmcp/internal/game/items"
	"wowmcp/internal/game/journal"
	"wowmcp/internal/game/leftovers"
	"wowmcp/internal/game/legacy"
	"wowmcp/internal/game/mail"
	"wowmcp/internal/game/makeorbuy"
	"wowmcp/internal/game/pet"
	"wowmcp/internal/game/professions"
	"wowmcp/internal/game/progress"
	"wowmcp/internal/game/questguide"
	"wowmcp/internal/game/questrewards"
	"wowmcp/internal/game/questroute"
	"wowmcp/internal/game/quests"
	"wowmcp/internal/game/reagents"
	"wowmcp/internal/game/reputation"
	"wowmcp/internal/game/rested"
	"wowmcp/internal/game/search"
	"wowmcp/internal/game/sessionrating"
	"wowmcp/internal/game/shopping"
	"wowmcp/internal/game/social"
	"wowmcp/internal/game/talents"
	"wowmcp/internal/game/timeline"
	"wowmcp/internal/game/trainer"
	"wowmcp/internal/snapshot"
)

type characterArgs struct {
	Character string `json:"character,omitempty" jsonschema:"character name; empty = most recently dumped character"`
}

type noArgs struct{}

type timelineArgs struct {
	characterArgs
	timeline.Query
}

type sectionArgs struct {
	Section   string `json:"section" jsonschema:"snapshot section name, e.g. actionBars, reputation, vendor, bank, flightPaths, macros, completedQuests, stats, auras, probe"`
	Character string `json:"character,omitempty" jsonschema:"character name; empty = most recently dumped character"`
	Path      string `json:"path,omitempty" jsonschema:"optional dot path into the section to read only a part of a big section, e.g. namespaces.C_AuctionHouse (probe) or events; list items by 0-based index"`
	Limit     int    `json:"limit,omitempty" jsonschema:"optional: keep only the first N items of the list, or the first N keys (alphabetical) of the object, at the end of the path; lists directly inside that object are cut to N items too; the answer says how many there are in total (limited)"`
}

// freshness ends the description of tools that read one character's snapshot.
const freshness = " Data comes from the last /reload or logout in game; the snapshot.ageMinutes field says how many minutes old it is."

// tool is one entry of the tool registry: a description for the MCP client and a handler with a typed input.
type tool struct {
	name, title, description string
	add                      func(server *mcp.Server, def *mcp.Tool)
	writes                   bool     // true = writes to the server's own file (not read-only)
	args                     []string // argument names of the input type (used for the narrowing hint of oversized answers)
}

func (t tool) register(server *mcp.Server) {
	ann := readOnly(t.title)
	if t.writes {
		ann = ownFileWriter(t.title)
	}
	t.add(server, &mcp.Tool{Name: t.name, Description: t.description, Annotations: ann})
}

// accountTool is a tool that receives the whole snapshot database (all characters).
func accountTool[In any](s *service, name, title, description string,
	handle func(db *snapshot.Database, args In) (map[string]any, error)) tool {

	return tool{name: name, title: title, description: description, args: argNames[In](), add: func(server *mcp.Server, def *mcp.Tool) {
		mcp.AddTool(server, def, func(ctx context.Context, req *mcp.CallToolRequest, args In) (*mcp.CallToolResult, any, error) {
			db, err := s.loadDatabase()
			if err != nil {
				return nil, nil, err
			}
			out, err := handle(db, args)
			if err != nil {
				return nil, nil, err
			}
			return textResult(out)
		})
	}}
}

// characterTool is a tool that builds a response from one character's snapshot; it adds the snapshot field with the age of the data.
func characterTool[In any](s *service, name, title, description string, who func(In) string,
	view func(name string, snap snapshot.Snapshot, args In) (map[string]any, error)) tool {

	return tool{name: name, title: title, description: description, args: argNames[In](), add: func(server *mcp.Server, def *mcp.Tool) {
		mcp.AddTool(server, def, func(ctx context.Context, req *mcp.CallToolRequest, args In) (*mcp.CallToolResult, any, error) {
			character, snap, err := s.load(who(args))
			if err != nil {
				return nil, nil, err
			}
			out, err := view(character, snap, args)
			if err != nil {
				return nil, nil, err
			}
			out["snapshot"] = s.Freshness.SnapshotMeta(character, snap)
			return textResult(out)
		})
	}}
}

// sectionTool is a tool that returns the chosen snapshot sections unchanged;
// summarize may add computed fields (e.g. totals).
func sectionTool(s *service, name, title, description string, sections []string,
	summarize func(snap snapshot.Snapshot, out map[string]any)) tool {

	return characterTool(s, name, title, description, characterOf,
		func(_ string, snap snapshot.Snapshot, _ characterArgs) (map[string]any, error) {
			out := map[string]any{}
			for _, section := range sections {
				if value, ok := snap[section]; ok {
					out[section] = value
				}
			}
			if summarize != nil {
				summarize(snap, out)
			}
			return out, nil
		})
}

func characterOf(a characterArgs) string { return a.Character }

func (s *service) tools() []tool {
	return slices.Concat(
		s.statusTools(),
		s.characterTools(),
		s.windowTools(),
		s.shoppingTools(),
		s.questingTools(),
		s.progressTools(),
		s.goalTools(),
		s.accountTools(),
		s.extrasTools(s.Extras, s.ExportDir),
		s.remoteTools(),
		s.wikiTools(),
	)
}

// statusTools: status, diagnosis and raw sections.
func (s *service) statusTools() []tool {
	return []tool{
		accountTool(s, "wow_status", "Snapshot status",
			"Connection status with the game: server version and expected addon version, where the snapshot file is read from, which characters it contains and how old the data is. For each character: addon version, snapshot collection time (dumpTimeMs), disabled sections, the player's note (note) and alerts, i.e. what needs attention (mail and auctions about to expire, broken gear, full bags, a slow dump, addon errors). Start here when another tool returns an error or the data looks stale; for a full diagnosis with fixes call wow_doctor.",
			func(db *snapshot.Database, _ noArgs) (map[string]any, error) { return s.status(db), nil }),

		s.doctorTool(),

		accountTool(s, "wow_section", "Snapshot section",
			"Any snapshot section by name, without filters. Reputation, bank, vendor, recipes, mail, cooldowns, talents, auctions, group (and inspect) and dungeons have more convenient tools: wow_reputation, wow_bank, wow_vendor, wow_recipes, wow_mail, wow_cooldowns, wow_talents, wow_auctions, wow_group, wow_dungeons. Big sections (probe is about 60 kB) can be read in pieces: path (a dot path into the section, e.g. namespaces.C_AuctionHouse or events) and limit (first N items or keys). Other sections: actionBars (what sits on which action bar button), macros, completedQuests (IDs of all completed quests; for checking prerequisite quests), flightPaths (flight points from the last opened flight map), probe (list of client API functions after /mcp probe), loot (own loot since the previous save, plus periods: earlier saves carried by the addon; summarized by wow_changes and wow_journal), xpGains (own XP by source: kills, quests, the rest, with periods like loot; also in wow_changes and wow_journal as xpSources), buyback (the vendor buyback list; see wow_vendor), friends and guild (see wow_social), equipmentSets (see wow_equipment), pvp (see wow_character), vendors and trainers (up to 10 remembered NPCs: name -> offer; more convenient via npc in wow_vendor and wow_trainer), legacy (Forever Legacy perk trees, challenges and API discovery; see wow_legacy), stable (stabled pets; see wow_pet), questOffer and questTurnIn (NPC quest windows; see wow_quest_rewards). The list of sections available for a character is given by wow_status."+freshness,
			s.section),
	}
}

// characterTools: the character itself.
func (s *service) characterTools() []tool {
	fresh := s.Freshness
	return []tool{
		sectionTool(s, "wow_character", "Character",
			"Basic character data: level, class, race, faction, gold, experience, zone and coordinates, stats (attributes, armor, attack power, crit chances, weapon damage and speed), active buffs and debuffs, professions together with weapon skills (group = weapon or Weapon Skills), spent talent points, currencies and tokens (currencies), played time from /played (playedTime), the player's note for the assistant (note, set with /mcp note in game) and deaths (deaths: counter and the last death with its zone). The character section also has resting (whether the character is resting in an inn or city) and bindLocation (where the Hearthstone returns). Auras have expiresAt (Unix end time) and duration when the client provides them. The pvp section (from addon 0.7.0, if the client provides it): honorable kills, honor, rank."+freshness,
			[]string{"character", "stats", "auras", "professions", "talents", "currencies", "pvp"}, func(snap snapshot.Snapshot, out map[string]any) { character.SummarizeSheet(snap, out, fresh) }),

		sectionTool(s, "wow_equipment", "Equipment",
			"Equipped gear: slot, ID, name, quality, durability, type, item level, stats and the full in-game tooltip text (tooltip field: damage, speed, DPS, bonuses); the ammo slot is the ammunition with its count. The durability field summarizes durability (lowest percentage, items close to breaking) and the repair cost from the last vendor who repairs. The equipmentSets section (from addon 0.7.0) holds the in-game equipment sets: name, isEquipped (the set is currently worn), missing (how many items are missing) and items (slot, ID, name). To compare with an item from the bags, also use wow_bags."+freshness,
			[]string{"equipment", "equipmentSets"}, func(snap snapshot.Snapshot, out map[string]any) { character.SummarizeEquipment(snap, out, fresh) }),

		sectionTool(s, "wow_bags", "Bags",
			"Bag contents: each bag with its size, number of free slots and items (ID, name, count, quality 0-5, type, level, vendor sell price, stats; equippable items also have the full tooltip text in the tooltip field). Use it for questions about inventory space, what to sell, or whether the character has a given item."+freshness,
			[]string{"bags"}, items.SummarizeBags),

		sectionTool(s, "wow_spells", "Spells",
			"Known spells from the spellbook: name, rank, tab, whether passive. Use it to find out what the character already knows before advising on a rotation or trainer purchases."+freshness,
			[]string{"spells"}, nil),

		characterTool(s, "wow_talents", "Talents",
			"Character talents: the point layout (split, e.g. 11/0/0), free points, learned talents, those not maxed out, and the full trees (fullTrees) with every talent, its rank, position (row/column), the in-game description and a canBuyNow field; canBuyNow at the top collects the talents in which a point can be spent now. Forever trees differ from classic ones, so advise based on the descriptions in this snapshot, not on classic builds. The tree filter narrows it to one tab."+freshness,
			func(a talents.Query) string { return a.Character },
			func(_ string, snap snapshot.Snapshot, q talents.Query) (map[string]any, error) {
				return talents.View(snap, q), nil
			}),

		characterTool(s, "wow_legacy", "Legacy perks",
			"The Forever Legacy system: unspent and spent Legacy Points of the character (points: unspent, spent, max = the points this character can have now, and the account currency with accountTotalEarned when the client links it), the three perk trees (Professions, Adventure, Resourcefulness) with every perk, its rank, the in-game description, what it requires (an arrow from another perk, requiresSpent points in the tree) and status (available, locked, maxed), canLearnNow with the perks a point can go to right now, and the Legacy challenges when the client exposes them as achievements in a Legacy category (the current client does not; perks marked unrevealed are the \"?\" perks, do not recommend them). Advise only from the descriptions in this snapshot. The tool never spends points: the player learns perks and clicks Apply Changes in the Legacy window. The tree filter narrows it to one tab; discovery=true adds what the addon found about the client's Legacy API (shown anyway when no perk tree was found; since addon 0.19.1 the dump has it only then or after /mcp probe)."+freshness,
			func(a legacy.Query) string { return a.Character },
			func(_ string, snap snapshot.Snapshot, q legacy.Query) (map[string]any, error) {
				return legacy.View(snap, q), nil
			}),

		characterTool(s, "wow_reputation", "Reputation",
			"Faction reputation with filtering by name and standing. Each faction has progress within the current standing (progress, percent) and how much is left to the next one (toNextStanding)."+freshness,
			func(a reputation.Query) string { return a.Character },
			func(_ string, snap snapshot.Snapshot, q reputation.Query) (map[string]any, error) {
				return reputation.View(snap, q), nil
			}),

		sectionTool(s, "wow_pet", "Pet",
			"The active Hunter pet: name, level, family, happiness (1-3), loyalty, training points (trainingPointsFree = free), experience, food types, the pet action bar (autoCast = whether the ability is cast automatically) and learned abilities with ranks. exists=false means no pet is summoned. Extras derived from snapshot data: rankReview (ranks the Hunter knows from the Beast Training window that the pet lacks; ranks from trainer offers the Hunter does not know yet), trainingPlan (which lessons fit into the free training points) and stable (stabled pets compared with the active one by level and family; data from the Stable Master window)."+freshness,
			[]string{"pet"}, func(snap snapshot.Snapshot, out map[string]any) { pet.Summarize(snap, out, fresh) }),

		sectionTool(s, "wow_quests", "Quest log",
			"Quest log: title, ID, level, zone, whether completed, and the progress of every objective. Use it for questions about quests, missing prerequisite quests, or what to turn in."+freshness,
			[]string{"quests"}, quests.SummarizeLog),
	}
}

// windowTools: windows remembered from the last visit.
func (s *service) windowTools() []tool {
	fresh := s.Freshness
	return []tool{
		characterTool(s, "wow_trainer", "Trainer offer",
			"The trainer offer from the last visit (or from the one given by npc): NPC name and the list of services with rank, category (available = can be bought, unavailable = not yet, used = already known), price and required level. Every entry has a canAfford field computed from current gold. The addon remembers up to 10 recent trainers (class, profession, weapons): known holds their names. Empty if the character never opened a trainer window."+freshness,
			func(a trainer.Query) string { return a.Character },
			func(_ string, snap snapshot.Snapshot, q trainer.Query) (map[string]any, error) {
				return trainer.View(snap, q, fresh), nil
			}),

		characterTool(s, "wow_vendor", "Vendor",
			"The offer of the most recently opened vendor (or the one given by npc; known holds the names of up to 10 remembered) with filters: name fragment, type/subtype, minimum quality, affordable (can afford), usable (required level reached), equippable (only equippable items). Every entry has the price, canAfford, levelOk, and equippable items also have vsEquipped: the difference in stats, DPS and item level versus the equipped item. The capturedAgeMinutes field says how long ago the window was open. The buyback field (from addon 0.7.0) is the buyback list from the vendor window: what can still be bought back, for how much and how long ago it was saved. This is an assumption: the client loses the buyback list after logout, so do not treat an old list as certain."+freshness,
			func(a items.VendorQuery) string { return a.Character },
			func(_ string, snap snapshot.Snapshot, q items.VendorQuery) (map[string]any, error) {
				return items.Vendor(snap, q, fresh), nil
			}),

		characterTool(s, "wow_bank", "Bank",
			"Bank contents from the last visit with filters: name fragment, type/subtype, minimum quality, sellable=true (only what a vendor will buy). Every item has vendorValue (sell price x count), and the response has the total totalVendorValue and the free slots. Good for questions like: what can I sell from the bank. The capturedAgeMinutes field says how long ago the bank was open."+freshness,
			func(a items.BankQuery) string { return a.Character },
			func(_ string, snap snapshot.Snapshot, q items.BankQuery) (map[string]any, error) {
				return items.Bank(snap, q, fresh), nil
			}),

		characterTool(s, "wow_mail", "Mail",
			"The mailbox from the last time it was opened: sender, subject, gold, cash on delivery (COD), attachments, whether the letter was returned and when it expires (expiringSoon = less than 3 days). The capturedAgeMinutes field says how long ago the mailbox was open."+freshness,
			characterOf,
			func(_ string, snap snapshot.Snapshot, _ characterArgs) (map[string]any, error) {
				return mail.View(snap, fresh), nil
			}),

		characterTool(s, "wow_auctions", "Auction house",
			"Auction house prices saved when the player browsed search results (lowest buyout and bid per unit, quantity, number of auctions, when seen, trend over recent visits, or since the first visit the server's price history kept for the realm), the player's own auctions (myAuctions) and an assessment of what is worth listing (sellOnAH): for items in the bags and bank, the price after the 5% fee compared with the vendor price, sorted by highest profit. checkPrices lists valuable items without a saved price. Gear prices also carry the item details read while the player browsed (type, equipLoc, itemLevel, requiredLevel, quality, statTokens, DPS and, from addon 0.19.2, the row's tooltip lines with Equip and Use effects; an item with a random suffix such as \"of the Eagle\" is saved under its full name with its own stats, and statsNote marks older entries that have only the base item's stats), plus canAfford, levelOk and, for equippable items, vsEquipped: the difference in stats, DPS and item level versus the equipped item, the same as wow_vendor. Use it for \"which bow should I buy\": equippable=true with type (e.g. Bow) lists the best upgrade first. Filters: query, mine (only what the character has), type, affordable, usable, equippable. Prices saved by an older addon have no details (detailsNote says to update and browse again)."+freshness,
			func(a auctions.Query) string { return a.Character },
			func(name string, snap snapshot.Snapshot, q auctions.Query) (map[string]any, error) {
				return auctions.View(snap, q, fresh, s.pricePast(name)), nil
			}),

		characterTool(s, "wow_recipes", "Recipes",
			"Recipes known to the character from the last opened window of each profession (including the Hunter's Beast Training with its training point cost): difficulty color (whether it still gives skill points), how many can be made from the materials in the bags (canCraft), materials with counts in the bags and in the bank, missing materials and recipe cooldowns. The skillUpCraftableNow field says what to craft now to raise the skill. Filters: profession, query, skillUp, craftable."+freshness,
			func(a professions.RecipeQuery) string { return a.Character },
			func(_ string, snap snapshot.Snapshot, q professions.RecipeQuery) (map[string]any, error) {
				return professions.Recipes(snap, q, fresh), nil
			}),
	}
}

// shoppingTools: shopping, gear choices and upkeep.
func (s *service) shoppingTools() []tool {
	fresh := s.Freshness
	return []tool{
		characterTool(s, "wow_shopping_list", "Shopping list",
			"What to buy before leaving town: spells at the trainer (available from all remembered trainers, the newest offer of each, with the total cost and flags for an old or outdated offer), repairs, ammo (how much to buy for hours of play), pet food, running-low consumables, with information on WHO sells them and for how much (from remembered vendors), plus a budget in priority order (trainer, repairs, ammo, food) and whether the gold covers everything. returnToTown says when to return to town: in how many levels there are new spells at the trainer, in how many game hours the bags fill up, how many hours the ammo lasts and the durability state (from snapshot history; when there is too little of it, the field says: no data). It buys nothing."+freshness,
			func(a shopping.Query) string { return a.Character },
			func(name string, snap snapshot.Snapshot, q shopping.Query) (map[string]any, error) {
				return shopping.View(name, snap, s.progress, q, fresh)
			}),

		characterTool(s, "wow_quest_rewards", "Quest rewards",
			"Assessment of the quest reward choices against the equipped gear: for the quest being turned in or currently offered by an NPC (questTurnIn, questOffer; at the top when the window is fresh) and for quests in the log. Each reward has the slot, the stat differences, a score (from stat weights: the weights argument or weights.json; without them item level + DPS) and the vendor value; best points to a choice with a justification (an upgrade, and when there is none, the highest vendor price). It also lists fixed rewards, gold and XP. WARNING: the addon reads rewards by quest ID; when several quests in the log have an identical reward list they are marked suspect and have no recommendation (the client may have returned another quest's rewards). The quest filter: name or ID."+freshness,
			func(a questrewards.Query) string { return a.Character },
			func(name string, snap snapshot.Snapshot, q questrewards.Query) (map[string]any, error) {
				return questrewards.View(snap, q, items.Scoring{Store: s.Weights, Key: name}, fresh)
			}),

		characterTool(s, "wow_compare_item", "Item comparison",
			"Compares an item from the bags, bank or a vendor with what the character has equipped in the matching slot (rings, trinkets and one-handed weapons in both slots; a two-handed weapon against the sum of mainHand + offHand). Returns the difference in stats, DPS, armor and item level, the required level, the price and the character's weapon skill. Give the item as a name, a name fragment or an ID. Optional weights (e.g. {\"agility\":1,\"attackPower\":0.5}) or a weights.json file next to the goals file add weightedDelta: the sum of stat differences times weights; the server has no built-in class builds."+freshness,
			func(a items.CompareQuery) string { return a.Character },
			func(name string, snap snapshot.Snapshot, q items.CompareQuery) (map[string]any, error) {
				return items.CompareViewScored(snap, q, items.Scoring{Store: s.Weights, Key: name})
			}),

		accountTool(s, "wow_cooldowns", "Cooldowns",
			"Long cooldowns: Hearthstone and other items, spells, profession cooldowns (e.g. transmutes, Mooncloth). For each: the time left relative to now, or ready = true if it has already passed. all = true shows all characters on the account, e.g. to check which alt has a profession cooldown ready."+freshness,
			s.cooldowns),

		characterTool(s, "wow_buffs", "Buffs and debuffs",
			"The character's active effects (auras): buffs and debuffs with stack counts, and a list of missing typical class buffs (e.g. Hunter's Aspect, Mage's Arcane Intellect, Paladin's Blessing, no pet while Call Pet is known). The missing-buff warning is a heuristic: it appears only when the character knows the spell and none of the effects of its group is among the auras. The server gives the time left on an aura only when the snapshot contains its end time (addon 0.7.0+ saves expiresAt when the client gives it; otherwise the response says so in timingNote). State at the time of saving."+freshness,
			characterOf,
			func(_ string, snap snapshot.Snapshot, _ characterArgs) (map[string]any, error) {
				return buffs.View(snap, fresh), nil
			}),

		characterTool(s, "wow_bag_plan", "Bag plan",
			"What to do with full bags: for each item (by name) an action keep, bank, sell or auction with a one-sentence reason, plus totals (slots freed, vendor value, auction profit after the fee). It uses quest assessments (wow_leftovers), recipes, auction prices, the pet and gear comparisons. It sells or deletes nothing and never advises destroying items; a quest item without a quest goes to the bank."+freshness,
			characterOf,
			func(_ string, snap snapshot.Snapshot, _ characterArgs) (map[string]any, error) {
				return bagplan.View(snap, fresh), nil
			}),
	}
}

// questingTools: where to go next.
func (s *service) questingTools() []tool {
	fresh := s.Freshness
	return []tool{
		characterTool(s, "wow_quest_plan", "Quest plan",
			"Quest helper: the log grouped by zone (zones with the most things to do first), each quest with its difficulty color as in the game (red, orange, yellow, green, gray), objective progress in percent and the list of objectives still to do. It warns about quests that are too hard, gray (almost no XP), failed, group quests, and about a full quest log. The zone filter narrows it to one zone."+freshness,
			func(a quests.PlanQuery) string { return a.Character },
			func(_ string, snap snapshot.Snapshot, q quests.PlanQuery) (map[string]any, error) {
				return quests.Plan(snap, q), nil
			}),

		characterTool(s, "wow_dungeons", "Dungeons",
			"Dungeons matching the character's level (fitNow), those available in a few levels (soon) and, with all=true, the rest including raids. Each dungeon has its level range, entrance zone and the quests from the log that relate to it (with difficulty color and progress). Ranges come from the in-game group finder, and when the client does not provide them, from an approximate table for the classic version (the source field says which). lockouts are saved instance lockouts with the time until reset. name narrows it to one dungeon."+freshness,
			func(a dungeons.Query) string { return a.Character },
			func(_ string, snap snapshot.Snapshot, q dungeons.Query) (map[string]any, error) {
				return dungeons.View(snap, q, fresh), nil
			}),

		characterTool(s, "wow_group", "Group and raid",
			"The composition of the group or raid at the time of the snapshot: name, level, class, race, role (tank/healer/dps), leader, subgroup and zone in a raid, online, health and max mana/energy, range; a summary of classes, roles and levels with warnings (no tank or healer, someone offline or far away, a large level difference). For players examined with the Inspect window: average item level and guild; the player parameter returns their whole equipment. myAverageItemLevel is the character's average item level."+freshness,
			func(a group.Query) string { return a.Character },
			func(_ string, snap snapshot.Snapshot, q group.Query) (map[string]any, error) {
				return group.View(snap, q, fresh)
			}),

		characterTool(s, "wow_flights", "Flight points",
			"Matches known flight points (the flightPaths section from the flight master window) with the zones of quests in the log: in which zones with quests the character knows a flight point and in which it does not. Points and quests are matched by the zone's map ID when both have one; otherwise the zone comes from the point's zone field or, for older dumps, is guessed from the point name (an assumption described in the assumption field); the unreachable state has no confirmed meaning. Requires opening the flight map earlier and /mcp reload."+freshness,
			characterOf,
			func(_ string, snap snapshot.Snapshot, _ characterArgs) (map[string]any, error) {
				return flights.View(snap, fresh), nil
			}),

		characterTool(s, "wow_quest_route", "Quest zone order",
			"Suggests an order for visiting zones with quests from the log: first the character's current zone, then zones by the number of quests ready to turn in and of all quests, and last the red (too hard) quests. Each zone comes with its quests (status, difficulty) and known flight points. The server has no world map or distances between zones, so this is NOT the shortest route (see assumption). Flight points require opening the flight map earlier and /mcp reload."+freshness,
			characterOf,
			func(_ string, snap snapshot.Snapshot, _ characterArgs) (map[string]any, error) {
				return questroute.View(snap, fresh), nil
			}),

		characterTool(s, "wow_quest_lookup", "Quest locations",
			"Where a quest is picked up, done and turned in: the NPC or object, the zone and map coordinates, the objective targets and the next quest of the chain, with nextStep as a one-line answer for the quest's current state (ready to turn in, in progress, not in log). USE THIS before telling the player where to go for a quest: World of Warcraft: Forever differs from Classic and the quest text is often out of date, so never answer quest locations from memory. The data is read by the addon from the quest database the player has installed in the game (QuestieDB, what Questie shows on the map) and covers the quests in the log, the quests of the next guide steps and the answers to /mcp quest <name or ID> typed in game. Without quest it lists the quest log; quest is an ID or a fragment of a quest or NPC name. When the quest is not in the dump, the answer says what the player should type in game."+freshness,
			func(a questguide.Query) string { return a.Character },
			func(_ string, snap snapshot.Snapshot, q questguide.Query) (map[string]any, error) {
				return questguide.Lookup(snap, q), nil
			}),

		characterTool(s, "wow_guide", "Leveling guide steps",
			"The next steps of the leveling guide the player follows in the Guidelime addon (for example a TUGs guide): the guide name, how many steps are done, and up to 15 open steps in guide order as readable text, each quest with its name, state and the place to go (from the installed quest database). USE THIS for \"what should I do next\" when the player uses a guide: follow the guide's order instead of inventing a route. Steps for other races or classes are left out; quest steps count as done from the quest log, other steps when the player ticked them or moved past them. Requires addon 0.14.0, Guidelime with a guide selected and /mcp reload."+freshness,
			characterOf,
			func(_ string, snap snapshot.Snapshot, _ characterArgs) (map[string]any, error) {
				return questguide.Guide(snap), nil
			}),
	}
}

// progressTools: progress over the dump history.
func (s *service) progressTools() []tool {
	fresh := s.Freshness
	return []tool{
		characterTool(s, "wow_changes", "Changes since the previous snapshot",
			"What changed since an earlier snapshot: gold, XP and level-ups, items gained or lost in the bags, equipment changes, new, progressing, ready-to-turn-in and turned-in quests, reputation, new spells, professions and talents. It also shows new deaths, own loot (loot) and XP by source (xpSources: kills, quests, the rest) gained after the older snapshot, including periods of saves the server never read (periodsMerged). The server remembers every snapshot it has read (list in the history field). By default it compares with the previous one; steps or sinceHours reach further back, e.g. sinceHours=4 summarizes the whole session."+freshness,
			func(a progress.ChangesQuery) string { return a.Character }, s.progress.Changes),

		characterTool(s, "wow_progress", "Progress and pace",
			"Progress statistics from the snapshot history: XP and gold per hour in the current session and over the whole history (or the last sinceHours), how many quests were turned in, level-ups, how much XP is missing to the next level and the estimated time to level at the current pace. When the addon records played time (/played), pace is computed from in-game time rather than wall-clock time. If the death counter increased, the session shows the number of deaths. When snapshots have an xpGains section (addon 0.7.0), session and overall have xpSources: XP from kills, quests and the rest."+freshness,
			func(a progress.PaceQuery) string { return a.Character }, s.progress.Pace),

		characterTool(s, "wow_gold_history", "Gold history",
			"Character gold over time from the snapshot history: day by day (first, last, change), the three largest inflows and three largest expenses between consecutive snapshots, and the change over the whole window. days (default 14) is the window counted from the newest snapshot. This is a balance, not a breakdown by source. Requires snapshot history (the --history-dir \"\" flag disables it and gives an error)."+freshness,
			func(a progress.GoldQuery) string { return a.Character }, s.progress.GoldHistory),

		characterTool(s, "wow_rested", "Rested XP",
			"Rested XP: how much is in the pool, what percent of a level, how many levels and bubbles of the bar that is, how much is missing to level up, whether the pool covers the rest of the level, whether the character is resting now (resting) and when the pool will be full if the character logs out in an inn (and when outside one). The accumulation rules (5% of a level per 8 h in an inn, cap 150%) come from classic WoW and are approximate (source: classic rule, approximate): Forever may count differently."+freshness,
			characterOf,
			func(_ string, snap snapshot.Snapshot, _ characterArgs) (map[string]any, error) {
				return rested.View(snap, fresh), nil
			}),

		characterTool(s, "wow_ammo", "Ammo",
			"Ammunition (arrows, bullets): how much is in the ammo slot and in the bags, how much is used per game hour (from snapshot history, computed as in wow_progress: the current session, or the whole history when it is too short), how many hours it lasts and how much to buy for hours of play (default 2). Without history it returns just the stock. For a character without ammunition it returns an explanation."+freshness,
			func(a ammo.Query) string { return a.Character },
			func(name string, snap snapshot.Snapshot, q ammo.Query) (map[string]any, error) {
				return ammo.View(name, snap, s.progress, q)
			}),

		characterTool(s, "wow_reagents", "Supplies and consumption",
			"The character's consumables: ammunition, food and water, bandages, potions, other consumables and reagents. For each: how much there is now (bags and equipped, inBank separately), how much is used per game hour (from snapshot history, the same window as wow_progress and wow_ammo) and how many hours it lasts (hoursLeft); lowStock is items that last under an hour. petFood is food in the bags and the pet's diet. Without history it returns just the stock."+freshness,
			characterOf,
			func(name string, snap snapshot.Snapshot, _ characterArgs) (map[string]any, error) {
				return reagents.View(name, snap, s.progress), nil
			}),

		characterTool(s, "wow_journal", "Session journal",
			"A journal from the snapshot history: the last N sessions (the sessions argument, default 5, the same splitting rule as wow_progress), each with its start and end, duration, levels and XP gained, gold change, quests turned in and accepted, notable new items (from Uncommon quality), own loot from the loot section (counts and the more notable ones, by quality), XP by source from the xpGains section (xpSources: kills, quests, the rest), deaths and zones. records are the longest session, the best by gold and the fastest level (from /played). markdown=true adds ready Markdown text in the text field; the server itself does not write .md files."+freshness,
			func(a journal.Query) string { return a.Character },
			func(name string, snap snapshot.Snapshot, q journal.Query) (map[string]any, error) {
				return journal.View(name, snap, s.progress, q)
			}),

		characterTool(s, "wow_timeline", "Character timeline",
			"The character timeline from snapshot history: a chronological list of events (level-ups, new spells, equipment changes, quests turned in, deaths, new professions and the 75/150/225/300 thresholds). An event gets the time of the first snapshot in which it is visible (the game saves data only at /reload and logout), so the real time may be earlier; deaths have an exact time, and so do level-ups and quests turned in that the addon's event log recorded (exact: true; the server keeps that log, so it also reaches back before the kept snapshots). Arguments: limit (default 50, the newest are kept), kind (level_up, new_spell, equipment, quest_turned_in, death, profession) and days (the last N days from the latest snapshot). Requires snapshot history."+freshness,
			func(a timelineArgs) string { return a.Character },
			func(name string, snap snapshot.Snapshot, a timelineArgs) (map[string]any, error) {
				return timeline.View(name, snap, s.progress, a.Query)
			}),

		characterTool(s, "wow_session_rating", "Session rating",
			"Compares the pace of the last game session with the median of earlier sessions: XP, gold balance change and quests turned in per hour, with a ratio (faster, similar, slower) and a short verdict. Sessions and played time are computed the same way as in wow_progress and wow_journal. It needs at least three sessions with at least 2 snapshots and 5 minutes of play each; otherwise it says what is missing. Gold is a balance, not income. Requires snapshot history."+freshness,
			characterOf,
			func(name string, snap snapshot.Snapshot, _ characterArgs) (map[string]any, error) {
				return sessionrating.View(name, snap, s.progress)
			}),
	}
}

// goalTools: goals.
func (s *service) goalTools() []tool {
	return []tool{
		characterTool(s, "wow_goals", "Goals",
			"The player's goals saved with wow_goal_set (level, gold, reputation, profession, trainer spell, items): for each the current state, how much is missing, the percentage and, when history allows, the ETA in game hours and sessions (pace from wow_progress). It shows the goals of the chosen character; goals of others are only counted."+freshness,
			func(a characterArgs) string { return a.Character },
			func(name string, snap snapshot.Snapshot, _ characterArgs) (map[string]any, error) {
				all, err := s.goalBook().All()
				if err != nil {
					return nil, err
				}
				return goals.View(all, goals.Env{Character: name, Snap: snap, History: s.progress}), nil
			}),

		s.goalSetTool(),
	}
}

// accountTools: the whole account, search and export.
func (s *service) accountTools() []tool {
	fresh := s.Freshness
	return []tool{
		accountTool(s, "wow_account", "All characters",
			"Overview of all characters on the account: level, class, zone, gold (and the account total), XP percent, rested XP, professions, free bag slots, played time, mail and ready cooldowns. Each character has the age of its data (ageMinutes). Use it for questions about alts or total gold.",
			func(db *snapshot.Database, _ noArgs) (map[string]any, error) {
				return character.Account(db, fresh), nil
			}),

		accountTool(s, "wow_find_item", "Find item",
			"Searches for an item across all characters on the account at once: in the bags, equipped gear, bank and mail. Returns where it is and how many, with a total for each item. Give the item as a name, a name fragment or an ID; character narrows it to one character.",
			func(db *snapshot.Database, q items.FindQuery) (map[string]any, error) {
				return items.FindOnAccount(db, q, fresh)
			}),

		characterTool(s, "wow_search", "Snapshot search",
			"Searches for text (case-insensitive) across one character's whole snapshot at once: quests (title, objectives, zone), items in the bags, bank and equipped (name, type, description), spells, trainer and vendor offers (including remembered ones), quest rewards, quests from NPC windows, stable, loot, recipes and their materials, mail, reputation, prices and own auctions, professions, currencies, macros and flight points. Returns hits grouped by section, with the location (path) and a short summary; up to 20 hits per section, truncated says how many were cut. To find an item across all characters use wow_find_item."+freshness,
			func(a search.Query) string { return a.Character },
			func(_ string, snap snapshot.Snapshot, q search.Query) (map[string]any, error) {
				return search.Run(snap, q)
			}),

		characterTool(s, "wow_leftovers", "Leftover quest items",
			"Quest items (type Quest) in the bags and bank whose quest is no longer in the log: keep are those needed by quests in the log or that start a quest, leftovers are candidates for leftovers with a confidence rating (confidence). The assessment is by name and is never certain; the tool deletes nothing and does not advise deleting automatically. The bank is the state from the last visit."+freshness,
			characterOf,
			func(_ string, snap snapshot.Snapshot, _ characterArgs) (map[string]any, error) {
				return leftovers.View(snap), nil
			}),

		accountTool(s, "wow_compare_characters", "Character comparison",
			"Puts two characters of the account side by side: level, class, race, gold, played time, professions, average item level and equipment slot by slot (better = which side has the higher ilvl), with a short summary of differences and the snapshot age of each character (it warns when the snapshots differ a lot in age). a (empty = most recently dumped) and b (empty = the only other character) accept a full name, a name without the realm, or an unambiguous prefix.",
			func(db *snapshot.Database, q compare.Query) (map[string]any, error) {
				return compare.View(db, q, fresh)
			}),

		accountTool(s, "wow_alt_mail", "Mail between characters",
			"Suggests what to mail between characters of the account: materials from one character's bags and bank that are ingredients of another's saved recipes (evidence=recipe, strong evidence; recipientHas is how many the recipient already has). When the recipient has no saved recipes of a profession, entries matched only by material kind have evidence=profession, weakEvidence=true and an assumption text: a weak lead to check in game. It skips bound and quest items. Filters: from (sender), to (recipient), query (material name fragment). It sends nothing; it assumes the same realm and faction."+freshness,
			func(db *snapshot.Database, q altmail.Query) (map[string]any, error) {
				return altmail.Plan(db, q, fresh)
			}),

		accountTool(s, "wow_account_crafting", "Account-wide crafting",
			"What can be crafted on the account and from whose materials: for every saved recipe of every character it computes how many can be made from the crafter's own materials (crafterCanMake) and from the bags and banks of all characters together (accountCanMake), shows who holds each material, what is missing (missing) and where to mail it from (borrowFrom). Filters: character, profession, query (recipe or material), craftable=true (only what can be made from account stock), limit (recipes per profession, default 40). Banks are the state from the last visits."+freshness,
			func(db *snapshot.Database, q accountcraft.Query) (map[string]any, error) {
				return accountcraft.View(db, q, fresh)
			}),

		characterTool(s, "wow_make_or_buy", "Craft or buy",
			"Compares the cost of crafting a recipe with the price of the finished item on the auction house: ingredients at saved auction house or vendor prices (whichever is cheaper), the finished item at the auction price; the verdict is make, buy or unknown, with the margin and the margin after the 5% fee. It assumes one craft yields one item and never treats a missing price as zero (then unknown and missingPrices; prices older than 48 h in stalePrices). Filters: profession, query, sort (margin or name), limit (default 50)."+freshness,
			func(a makeorbuy.Query) string { return a.Character },
			func(_ string, snap snapshot.Snapshot, q makeorbuy.Query) (map[string]any, error) {
				return makeorbuy.View(snap, q, fresh), nil
			}),

		characterTool(s, "wow_social", "Friends and guild",
			"The character's friends and guild lists from the most recently opened windows (from addon 0.7.0): who is online, level, class, zone, guild rank, counts and the age of the save (capturedAge). By default it shows only online guild members; includeOffline=true shows everyone. The addon reads only client memory and NEVER asks the game server for the friends or guild list itself, so when a list is missing, the response says which window to open in game and that /mcp reload is needed afterwards. The list is the state at the time of saving, not live. PvP stats are in wow_character (pvp)."+freshness,
			func(a social.Query) string { return a.Character },
			func(_ string, snap snapshot.Snapshot, q social.Query) (map[string]any, error) {
				return social.View(snap, q, fresh), nil
			}),

		characterTool(s, "wow_export", "Character export",
			"A ready-made character summary to paste elsewhere: format discord (Markdown, default), forum (BBCode with quality colors) or text. Contains level, race, class, faction, talent layout, average item level, professions, pet, played time, equipment and talents (short=true omits them). Gold only with gold=true. Labels are in English. Show the text field in a code block."+freshness,
			func(a export.Query) string { return a.Character },
			func(_ string, snap snapshot.Snapshot, q export.Query) (map[string]any, error) {
				return export.View(snap, q)
			}),
	}
}

// status describes the snapshot file, the characters in it and their age (wow_status).
func (s *service) status(db *snapshot.Database) map[string]any {
	characters := []any{}
	for _, name := range db.CharacterNames() {
		snap := db.Characters[name]
		entry := s.Freshness.SnapshotMeta(name, snap)
		entry["sections"] = snap.SectionNames()
		if c, ok := snap["character"].(map[string]any); ok {
			entry["level"] = c["level"]
			entry["class"] = c["class"]
			entry["race"] = c["race"]
		}
		snapshot.Merge(entry, addon.Describe(snap))
		if note := character.Note(snap, s.Freshness); note != nil {
			entry["note"] = note
		}
		if list := alerts.For(snap, s.Freshness); len(list) > 0 {
			entry["alerts"] = list
		}
		characters = append(characters, entry)
	}
	out := map[string]any{
		"serverVersion":        s.Version,
		"expectedAddonVersion": addon.ExpectedVersion,
		"file":                 db.Path,
		"fileModified":         db.ModTime.Format("2006-01-02 15:04:05"),
		"lastCharacter":        db.Last,
		"characters":           characters,
		"howToRefresh":         addon.RefreshHint(),
	}
	if name, snap, err := db.Lookup(""); err == nil {
		if w, ok := s.Freshness.SnapshotMeta(name, snap)["warning"].(string); ok {
			out["warning"] = w
		}
	}
	if db.Account != "" {
		out["account"] = db.Account
		out["accountPinned"] = db.AccountPinned
		if len(db.OtherAccounts) > 0 {
			out["otherAccounts"] = db.OtherAccounts
			out["accountNote"] = "Several accounts have a dump. This one was read; the characters of the others are not shown. Pin an account with --account (or WOW_ACCOUNT) and restart the server."
		}
	}
	if s.HistoryDir != "" {
		out["historyDir"] = s.HistoryDir
	}
	if s.GoalsFile != "" {
		out["goalsFile"] = s.GoalsFile
	}
	return out
}

// section returns any snapshot section by name, with the age of sections saved when a window is opened (wow_section).
func (s *service) section(db *snapshot.Database, args sectionArgs) (map[string]any, error) {
	character, snap, err := db.Lookup(args.Character)
	if err != nil {
		return nil, err
	}
	whole, ok := snap[args.Section]
	if !ok || snapshot.IsMetaField(args.Section) {
		return nil, fmt.Errorf("no section %q in the snapshot of character %s. Available: %s. The trainer, vendor, bank and flightPaths sections appear after opening the matching window in game and running /mcp reload",
			args.Section, character, strings.Join(snap.SectionNames(), ", "))
	}
	out := map[string]any{"snapshot": s.Freshness.SnapshotMeta(character, snap)}
	snapshot.Merge(out, s.Freshness.CapturedMeta(snapshot.AsMap(whole)))
	value, err := pickPath(whole, args.Path)
	if err != nil {
		return nil, fmt.Errorf("section %s, path %q: %w", args.Section, args.Path, err)
	}
	value, cut := limitValue(value, args.Limit)
	out[args.Section] = value
	if args.Path != "" {
		out["path"] = args.Path
	}
	if cut != nil {
		out["limited"] = cut
	}
	return out, nil
}

// cooldowns returns the cooldowns of one character (with the age of its snapshot) or of the whole account (wow_cooldowns).
func (s *service) cooldowns(db *snapshot.Database, q cooldowns.Query) (map[string]any, error) {
	out, err := cooldowns.View(db, q, s.Freshness)
	if err != nil {
		return nil, err
	}
	if !q.All {
		who, snap, _ := db.Lookup(q.Character)
		out["snapshot"] = s.Freshness.SnapshotMeta(who, snap)
	}
	return out, nil
}
