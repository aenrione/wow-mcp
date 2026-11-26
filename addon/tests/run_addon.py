# SPDX-License-Identifier: GPL-3.0-or-later
# Copyright (C) 2026 Piekasz (GitHub: Piekario). Author attribution must be kept: see NOTICE.
"""Runs the WowMcp addon in Lua 5.1 with game API stubs.

Checks the syntax and dump logic outside the game, and writes the result in the
format the WoW client uses for SavedVariables. That file then serves as
test data for the parser in the Go server.

Usage: python3 run_addon.py <modern|legacy|forever> <output_file>
       python3 run_addon.py lifecycle   (only the lifecycle from the real game; CB_ADDON_DIR = another addon copy)
"""

import os
import sys
from pathlib import Path

import lupa.lua51 as lua51

# CB_ADDON_DIR lets the same checks run on another copy of the addon (e.g. an old version from git).
ADDON_DIR = Path(os.environ.get("CB_ADDON_DIR") or Path(__file__).resolve().parent.parent / "WowMcp")
TOC = ADDON_DIR / "WowMcp.toc"


def addon_files() -> list[Path]:
    """Addon files in .toc order (like the client: skips blank lines and # comments)."""
    files = []
    for line in TOC.read_text(encoding="utf-8").splitlines():
        line = line.strip()
        if line and not line.startswith("#"):
            files.append(ADDON_DIR.joinpath(*line.replace("\\", "/").split("/")))
    return files

COMMON_STUBS = r"""
chatlog = {}

-- Item data for the auction house tests (addon 0.13.0): 2506 is in the client cache (details expected), 2507 is not (only the
-- static GetItemInfoInstant fields). GetItemInfo and GetItemStats on an uncached item would ask the server in the real
-- client, so the mock records such a call in uncachedLookups and the harness requires that list to stay empty.
uncachedLookups = {}
ahItemData = {
    [2506] = { cached = true, name = "Hornwood Recurve Bow", quality = 2, itemLevel = 19, requiredLevel = 14, sellPrice = 1500,
               stats = { ITEM_MOD_DAMAGE_PER_SECOND_SHORT = 9.7, ITEM_MOD_AGILITY_SHORT = 3 } },
    [2507] = { cached = false, name = "Ranger's Shortbow", quality = 2 },
}
for _, d in pairs(ahItemData) do d.type, d.subType, d.equipLoc, d.classID, d.subclassID = "Weapon", "Bows", "INVTYPE_RANGEDRIGHT", 2, 2 end
-- A cached item that is not gear: the price entry stays without details (size).
ahItemData[2508] = { cached = true, name = "Silk Thread", quality = 1, itemLevel = 1, requiredLevel = 0, sellPrice = 40, stats = {},
                     type = "Trade Goods", subType = "Cloth", equipLoc = "INVTYPE_NON_EQUIP_IGNORE", classID = 7, subclassID = 5 }
function ahItemId(link) return tonumber(tostring(link):match("item:(%d+)")) or tonumber(link) end
function GetItemInfoInstant(link)
    local id = ahItemId(link)
    local d = ahItemData[id]
    if d then return id, d.type, d.subType, d.equipLoc, 134, d.classID, d.subclassID end
end
function IsItemDataCachedByID(id) local d = ahItemData[id] return d ~= nil and d.cached end
function ahItemInfo(link)
    local id = ahItemId(link)
    local d = ahItemData[id]
    if not d then return nil end
    if not d.cached then uncachedLookups[#uncachedLookups + 1] = "GetItemInfo " .. tostring(id) return nil end
    return d.name, link, d.quality, d.itemLevel, d.requiredLevel, d.type, d.subType, 1, d.equipLoc, 134, d.sellPrice, d.classID, d.subclassID, 2
end
function ahItemStats(link)
    local id = ahItemId(link)
    local d = ahItemData[id]
    if not d then return nil end
    if not d.cached then uncachedLookups[#uncachedLookups + 1] = "GetItemStats " .. tostring(id) return {} end
    return d.stats
end
GetItemInfo, GetItemStats = ahItemInfo, ahItemStats
DEFAULT_CHAT_FRAME = { AddMessage = function(self, text) chatlog[#chatlog + 1] = text end }
-- Frame stub: every unknown method is an empty function, scripts land in frame.scripts.
-- handlers are the scripts of the first frame (the addon event frame).
handlers = nil
frames = {}
rejectedEvents = {} -- events the given client does not know (RegisterEvent raises an error)
-- Tooltip: records what the addon showed in it.
tooltip = { lines = {}, shown = false }
GameTooltip = {
    SetOwner = function(self, owner) tooltip.lines, tooltip.owner = {}, owner end,
    SetText = function(self, text) tooltip.lines[#tooltip.lines + 1] = text end,
    AddLine = function(self, text) tooltip.lines[#tooltip.lines + 1] = text end,
    Show = function(self) tooltip.shown = true end,
    Hide = function(self) tooltip.shown = false end,
    -- Item in the tooltip (GetItem) and scripts attached via HookScript.
    GetItem = function(self) return tooltip.item end,
    HookScript = function(self, script, fn) tooltip.hooks = tooltip.hooks or {}; tooltip.hooks[script] = fn end,
}
npcName = "Einris Brightspear"
UIParent = {}
local frameMeta = { __index = function() return function() end end }
function CreateFrame(kind, name, parent, template)
    local f = setmetatable({ kind = kind, frameName = name, template = template, scripts = {} }, frameMeta)
    f.SetScript = function(self, script, fn) self.scripts[script] = fn end
    f.SetText = function(self, text) self.label = text end
    f.CreateFontString = function(self) return CreateFrame("FontString") end
    f.CreateTexture = function(self) return CreateFrame("Texture") end
    f.Show = function(self) self.shown = true; if self.scripts.OnShow then self.scripts.OnShow(self) end end
    f.Hide = function(self) self.shown = false end
    f.GetPoint = function() return "TOPRIGHT", nil, "TOPRIGHT", -100, -150 end
    f.SetChecked = function(self, value) self.checked = value and true or false end
    f.GetChecked = function(self) return self.checked end
    f.LockHighlight = function(self) self.highlighted = true end
    f.UnlockHighlight = function(self) self.highlighted = false end
    f.RegisterEvent = function(self, event)
        if rejectedEvents[event] then error("Frame:RegisterEvent(): Unknown event: " .. event) end
    end
    frames[#frames + 1] = f
    if not handlers then handlers = f.scripts end
    return f
end
SlashCmdList = {}
-- Calls the addon must not make on its own initiative (server requests): each is recorded,
-- and the test at the end checks that the list is empty. RequestTimePlayed has its own stub (playedRequested).
forbiddenCalls = {}
function forbid(name) return function() forbiddenCalls[#forbiddenCalls + 1] = name end end
ShowFriends, GuildRoster, SendWho, NotifyInspect = forbid("ShowFriends"), forbid("GuildRoster"), forbid("SendWho"), forbid("NotifyInspect")
C_FriendList = { ShowFriends = forbid("C_FriendList.ShowFriends"), SendWho = forbid("C_FriendList.SendWho") }
C_GuildInfo = { GuildRoster = forbid("C_GuildInfo.GuildRoster") }
reloaded = false
function ReloadUI() reloaded = true end
-- Game clock: the test can move it (clockNow), by default a fixed dump moment.
clockNow = 1794760000
function time() return clockNow end
function date(fmt) return "2026-11-15 16:50:00" end

function UnitName(unit)
    if unit == "player" then return "Piekasz" end
    if unit == "pet" then return "Cat" end
    if unit == "npc" then return npcName end
end
function UnitLevel(unit) if unit == "pet" then return 19 end return 20 end
function GetBindLocation() return "Auberdine" end
function UnitClass() return "Hunter", "HUNTER" end
function GetLocale() return "enUS" end
function UnitRace() return "Night Elf", "NightElf" end
function UnitFactionGroup() return "Alliance" end
playedRequested = false
function RequestTimePlayed() playedRequested = true end
function GetMoney() return 5212 end
function UnitXP() return 1200 end
function UnitXPMax() return 21300 end
function GetXPExhaustion() return 4000 end
function IsResting() return true end
function GetRealZoneText() return "Stormwind City" end
function GetSubZoneText() return "Old Town" end
function GetRealmName() return "" end
guildRoster = {
    { "Tankadin", "Officer", 1, 22, "Paladin", "Stormwind City", "main tank", "", true, 0, "PALADIN" },
    { "Healbot", "Member", 3, 19, "Priest", "Darkshore", "", "", true, 0, "PRIEST" },
    { "Oldfriend", "Veteran", 2, 60, "Mage", "Dalaran", "", "", false, 0, "MAGE" },
}
function IsInGuild() return true end
function GetGuildInfo(unit) if unit == "player" then return "Night Watch", "Hunter Rank", 4 end end
function GetNumGuildMembers()
    local online = 0
    for _, m in ipairs(guildRoster) do if m[9] then online = online + 1 end end
    return #guildRoster, online, online
end
function GetGuildRosterInfo(i) local m = guildRoster[i]; if m then return unpack(m) end end
function GetBuildInfo() return "1.60.1", "70938", "Nov 10 2026", 16001 end
function UnitExists(unit) return unit == "pet" end
function UnitCreatureFamily() return "Cat" end
function UnitIsDead() return false end
function GetPetHappiness() return 3, 125 end
function GetPetLoyalty() return "Loyalty Level 3" end
function GetPetTrainingPoints() return 38, 21 end
function GetPetFoodTypes() return "Meat", "Fish" end

local equipped = {
    [16] = "|cff1eff00|Hitem:5586:0:0:0|h[Curvewood Dagger]|h|r",
    [17] = "|cff1eff00|Hitem:5540:0:0:0|h[Webwood Slicer]|h|r",
    [18] = "|cffffffff|Hitem:2780:0:0:0|h[Light Hunting Bow]|h|r",
}
function GetInventoryItemLink(unit, slot) return equipped[slot] end
function GetInventoryItemID(unit, slot)
    local link = equipped[slot]
    return link and tonumber(link:match("item:(%d+)"))
end
function GetInventoryItemQuality(unit, slot) return equipped[slot] and 2 or nil end
function GetInventoryItemDurability(slot) if equipped[slot] then return 30, 35 end end

function GetNumTrainerServices() return 4 end
function GetTrainerServiceInfo(i)
    if i == 1 then return "Hunter", "", "header", true end
    if i == 2 then return "Disengage", "Rank 1", "available", false end
    if i == 3 then return "Track Undead", "", "available", false end
    if i == 4 then return "Volley", "Rank 1", "unavailable", false end
end
function GetTrainerServiceCost(i) return ({ 0, 2100, 1600, 180000 })[i], 0, 0 end
function GetTrainerServiceLevelReq(i) return ({ 0, 20, 18, 40 })[i] end
"""

MODERN_STUBS = r"""
C_Map = {
    GetBestMapForUnit = function() return 1453 end,
    GetPlayerMapPosition = function()
        return { GetXY = function() return 0.6789, 0.1543 end }
    end,
}

local questEntries = {
    { title = "Darkshore", isHeader = true },
    { title = "Cave Mushrooms", questID = 947, level = 17, isHeader = false },
    { title = "WANTED: Murkdeep!", questID = 4740, level = 18, isHeader = false },
    { title = "Hidden tracker", questID = 99999, level = 1, isHeader = false, isHidden = true },
}
local objectives = {
    [947] = {
        { text = "Scaber Stalk: 5/5", finished = true, numFulfilled = 5, numRequired = 5 },
        { text = "Death Cap: 0/1", finished = false, numFulfilled = 0, numRequired = 1 },
    },
    [4740] = { { text = "Murkdeep slain: 0/1", finished = false, numFulfilled = 0, numRequired = 1 } },
}
C_QuestLog = {
    GetNumQuestLogEntries = function() return #questEntries, 2 end,
    GetInfo = function(i) return questEntries[i] end,
    IsComplete = function(id) return false end,
    IsFailed = function(id) return false end,
    GetQuestObjectives = function(id) return objectives[id] end,
}

NUM_TOTAL_EQUIPPED_BAG_SLOTS = 5
local bagContents = {
    [0] = { size = 16, name = "Backpack", items = {
        [1] = { itemID = 2589, stackCount = 14, quality = 1, hyperlink = "|cffffffff|Hitem:2589:0|h[Linen Cloth]|h|r" },
        [3] = { itemID = 2318, stackCount = 7, quality = 1, hyperlink = "|cffffffff|Hitem:2318:0|h[Light Leather]|h|r" },
        [4] = { itemID = 7005, stackCount = 1, quality = 1, hyperlink = "|cffffffff|Hitem:7005:0|h[Skinning Knife]|h|r" },
    } },
    [1] = { size = 6, name = "Small Brown Pouch", items = {
        [2] = { itemID = 4540, stackCount = 5, quality = 1, hyperlink = "|cffffffff|Hitem:4540:0|h[Tough Hunk of Bread]|h|r" },
    } },
    [4] = { size = 10, name = "Medium Quiver", items = {
        [1] = { itemID = 2515, stackCount = 200, quality = 1, hyperlink = "|cffffffff|Hitem:2515:0|h[Sharp Arrow]|h|r" },
    } },
}
for _, bag in pairs(bagContents) do for _, item in pairs(bag.items) do item.isBound = false end end
C_Container = {
    GetContainerNumSlots = function(bag) return bagContents[bag] and bagContents[bag].size or 0 end,
    GetBagName = function(bag) return bagContents[bag] and bagContents[bag].name end,
    GetContainerItemInfo = function(bag, slot)
        return bagContents[bag] and bagContents[bag].items[slot]
    end,
    -- No quest items in the mock bags: the client then returns isQuestItem = false and no questID.
    GetContainerItemQuestInfo = function(bag, slot) return { isQuestItem = false, isActive = false } end,
}

Enum = { SpellBookSpellBank = { Player = 0, Pet = 1 } }
local lines = {
    { name = "General", itemIndexOffset = 0, numSpellBookItems = 1 },
    { name = "Beast Mastery", itemIndexOffset = 1, numSpellBookItems = 2 },
    { name = "Marksmanship", itemIndexOffset = 3, numSpellBookItems = 2 },
}
local items = {
    { spellID = 6603, name = "Auto Attack", subName = "", isPassive = false },
    { spellID = 136, name = "Mend Pet", subName = "Rank 2", isPassive = false },
    { spellID = 13165, name = "Aspect of the Hawk", subName = "Rank 2", isPassive = false, iconID = 136076 },
    { spellID = 19434, name = "Aimed Shot", subName = "Rank 1", isPassive = false, iconID = 135130 },
    { spellID = 1130, name = "Hunter's Mark", subName = "Rank 1", isPassive = false },
}
C_SpellBook = {
    GetNumSpellBookSkillLines = function() return #lines end,
    GetSpellBookSkillLineInfo = function(i) return lines[i] end,
    GetSpellBookItemInfo = function(i, bank) return items[i] end,
}

function GetNumTalentTabs() return 3 end
function GetTalentTabInfo(tab) return ({ "Beast Mastery", "Marksmanship", "Survival" })[tab] end
function GetNumTalents(tab) return tab == 1 and 3 or 0 end
function GetTalentInfo(tab, index)
    local t = ({
        { "Endurance Training", 5, 5 },
        { "Focused Fire", 2, 2 },
        { "Improved Revive Pet", 0, 2 },
    })[index]
    return t[1], nil, index, 1, t[2], t[3], false, index < 3
end

function UnitCharacterPoints() return 1 end

-- v0.4.0: classic auction house (results page and own auctions), group, LFG.
local auctionList = {
    -- name, texture, count, quality, canUse, level, levelColHeader, minBid, minIncrement, buyout, ..., itemId (17)
    { "Light Leather", 134, 5, 1, true, 0, "", 50, 5, 100, 0, false, "", "Seller", "", 0, 2318 },
    { "Light Leather", 134, 10, 1, true, 0, "", 80, 5, 150, 0, false, "", "Seller", "", 0, 2318 },
    { "Linen Cloth", 134, 20, 1, true, 0, "", 100, 5, 0, 0, false, "", "Seller", "", 0, 2589 },
    { "Hornwood Recurve Bow", 134, 1, 2, true, 14, "", 3000, 5, 4200, 0, false, "", "Seller", "", 0, 2506 },
    { "Ranger's Shortbow", 134, 1, 2, true, 12, "", 2000, 5, 3100, 0, false, "", "Seller", "", 0, 2507 },
    { "Silk Thread", 134, 20, 1, true, 0, "", 600, 5, 800, 0, false, "", "Seller", "", 0, 2508 },
}
local ownedList = { { "Copper Ore", 134, 10, 1, true, 0, "", 200, 5, 400, 0, false, "", "Piekasz", "", 1, 2770 } }
function GetNumAuctionItems(kind) if kind == "list" then return #auctionList, 120 end if kind == "owner" then return #ownedList, #ownedList end return 0, 0 end
function GetAuctionItemInfo(kind, i)
    local r = (kind == "list" and auctionList or ownedList)[i]
    if r then return unpack(r) end
end
function GetAuctionItemLink(kind, i) return nil end

partyUnits = { party1 = { "Tankadin", 22, "Paladin", "PALADIN", "Human", "TANK" }, party2 = { "Healbot", 19, "Priest", "PRIEST", "Dwarf", "HEALER" } }
local unitExists, unitName, unitLevel, unitClass, unitRace = UnitExists, UnitName, UnitLevel, UnitClass, UnitRace
function UnitExists(unit) return partyUnits[unit] ~= nil or unit == "player" or unitExists(unit) end
function UnitName(unit) if partyUnits[unit] then return partyUnits[unit][1] end return unitName(unit) end
function UnitLevel(unit) if partyUnits[unit] then return partyUnits[unit][2] end return unitLevel(unit) end
function UnitClass(unit) if partyUnits[unit] then return partyUnits[unit][3], partyUnits[unit][4] end return unitClass(unit) end
function UnitRace(unit) if partyUnits[unit] then return partyUnits[unit][5] end return unitRace(unit) end
function UnitGroupRolesAssigned(unit) return partyUnits[unit] and partyUnits[unit][6] or "DAMAGER" end
function UnitIsGroupLeader(unit) return unit == "party1" end
function UnitIsConnected() return true end
function UnitInRange(unit) return unit == "party1", true end
function IsInRaid() return false end
function GetNumSubgroupMembers() return 2 end
function GetLootMethod() return "group", 0 end

function GetLFDChoiceOrder() return { 18, 1 } end
function GetLFGDungeonInfo(id)
    -- ... maxPlayers (13), description, isHoliday, bonusRep, minPlayers, isTimewalker, name2, minGearLevel, isScaling, lfgMapID (22)
    if id == 18 then return "The Deadmines", 1, 0, 15, 26, 21, 17, 26, 0, 12, "", 1, 5, "", false, 0, 5, false, "", 0, false, 36 end
    if id == 1 then return "Ragefire Chasm", 1, 0, 10, 20, 15, 13, 18, 0, 0, "", 1, 5, "", false, 0, 5, false, "", 0, false, 389 end
end

-- v0.6.0: quest rewards by quest ID (without selecting in the log), own loot, stable.
local questChoices = {
    [947] = {
        { "Curvewood Dagger", 135, 1, 2, true, 5586 },
        { "Webwood Slicer", 135, 1, 2, false, 5540 },
    },
}
local questRewards = { [947] = { { "Linen Cloth", 134, 5, 1, true, 2589 } } }
function GetNumQuestLogChoices(questID) return questChoices[questID] and #questChoices[questID] or 0 end
function GetQuestLogChoiceInfo(i, questID) local c = questChoices[questID]; if c and c[i] then return unpack(c[i]) end end
function GetNumQuestLogRewards(questID) return questRewards[questID] and #questRewards[questID] or 0 end
function GetQuestLogRewardInfo(i, questID) local r = questRewards[questID]; if r and r[i] then return unpack(r[i]) end end
function GetQuestLogRewardMoney(questID) return questID == 947 and 350 or 0 end
function GetQuestLogRewardXP(questID) return questID == 4740 and 1450 or 0 end

LOOT_ITEM = "%s receives loot: %s."
LOOT_ITEM_MULTIPLE = "%s receives loot: %sx%d."
LOOT_ITEM_SELF = "You receive loot: %s."
LOOT_ITEM_SELF_MULTIPLE = "You receive loot: %sx%d."
LOOT_ITEM_PUSHED_SELF = "You receive item: %s."
LOOT_ITEM_PUSHED_SELF_MULTIPLE = "You receive item: %sx%d."

local stabled = { [0] = { 132, "Cat", 19, "Cat", "Loyalty Level 3" }, [1] = { 132, "Bear", 15, "Bear", "Loyalty Level 2" } }
function GetNumStableSlots() return 2 end
function GetStablePetInfo(slot) local p = stabled[slot]; if p then return unpack(p) end end

function GetProfessions() return 1, 2, nil, nil, 3 end
function GetProfessionInfo(i)
    local p = ({ { "Skinning", 96, 150, 393 }, { "Mining", 71, 75, 186 }, { "Cooking", 52, 75, 185 } })[i]
    return p[1], nil, p[2], p[3], 0, 0, p[4]
end

-- v0.7.0: equipment sets, buyback list, friends, PvP, XP, minimap.
C_EquipmentSet = {
    GetEquipmentSetIDs = function() return { 0, 1 } end,
    GetEquipmentSetInfo = function(id)
        if id == 0 then return "Leveling", 134, 0, true, 3, 3, 3, 0, 0 end
        return "Tanking", 135, 1, false, 2, 1, 1, 1, 0
    end,
    GetItemIDs = function(id)
        if id == 0 then return { [1] = 0, [16] = 5586, [17] = 5540, [18] = 1 } end
        return { [16] = 7005 }
    end,
}
buybackItems = {
    { "Linen Cloth", 134, 150, 20, -1, true, "|cffffffff|Hitem:2589:0|h[Linen Cloth]|h|r" },
    { "Skinning Knife", 135, 16, 1, -1, true, "|cffffffff|Hitem:7005:0|h[Skinning Knife]|h|r" },
}
function GetNumBuybackItems() return #buybackItems end
function GetBuybackItemInfo(i) local b = buybackItems[i]; if b then return b[1], b[2], b[3], b[4], b[5], b[6] end end
function GetBuybackItemLink(i) local b = buybackItems[i]; return b and b[7] end

C_FriendList.GetNumFriends = function() return 3 end
C_FriendList.GetFriendInfoByIndex = function(i)
    if i == 1 then return { name = "Tankadin", level = 22, className = "Paladin", area = "Stormwind City", connected = true, notes = "dungeon buddy" } end
    if i == 2 then return { name = "Healbot", level = 19, className = "Priest", area = "Darkshore", connected = true } end
    return { name = "Oldfriend", level = 60, className = "Mage", area = "", connected = false, notes = "" }
end

function GetPVPLifetimeStats() return 1234, 5, 0 end
function UnitHonor() return 340 end
function UnitHonorMax() return 500 end
function UnitHonorLevel() return 7 end
function UnitPVPRank() return 8 end
function GetPVPRankInfo(rank) return "Knight", rank - 4 end

COMBATLOG_XPGAIN_FIRSTPERSON = "%s dies, you gain %d experience."
COMBATLOG_XPGAIN_FIRSTPERSON_UNNAMED = "You gain %d experience."
COMBATLOG_XPGAIN_EXHAUSTION1 = "%s dies, you gain %d experience. (%s exp %s bonus)"

cursorX, cursorY = 1000, 700
function GetCursorPosition() return cursorX, cursorY end
Minimap = setmetatable({
    GetCenter = function() return 1000, 500 end,
    GetEffectiveScale = function() return 1 end,
}, { __index = function() return function() end end })
"""

LEGACY_STUBS = r"""
function GetNumQuestLogEntries() return 2 end
function GetQuestLogTitle(i)
    if i == 1 then return "Darkshore", 0, 0, true end
    return "Cave Mushrooms", 17, 0, false, false, nil, 1, 947
end
function GetNumQuestLeaderBoards(i) return 2 end
function GetQuestLogLeaderBoard(j, i)
    if j == 1 then return "Scaber Stalk: 5/5", "item", true end
    return "Death Cap: 0/1", "item", nil
end

function GetContainerNumSlots(bag) return bag == 0 and 16 or 0 end
function GetBagName(bag) return "Backpack" end
function GetContainerItemInfo(bag, slot)
    if slot == 1 then
        return 134, 14, false, 1, false, false, "|cffffffff|Hitem:2589:0|h[Linen Cloth]|h|r", false, false, 2589, false
    end
end
function GetContainerItemQuestInfo(bag, slot) return false, nil, false end

-- The old rewards API reads the selected quest: the addon selects it briefly and restores the player's selection.
questLogSelection = 7
function GetQuestLogSelection() return questLogSelection end
function SelectQuestLogEntry(i) questLogSelection = i end
function GetNumQuestLogChoices() return questLogSelection == 2 and 1 or 0 end
function GetQuestLogChoiceInfo(i) if questLogSelection == 2 and i == 1 then return "Mushroom Cap", 134, 2, 1, true end end
function GetQuestLogItemLink(kind, i) if questLogSelection == 2 and kind == "choice" and i == 1 then return "|cffffffff|Hitem:4605:0|h[Mushroom Cap]|h|r" end end
function GetNumQuestLogRewards() return 0 end
function GetQuestLogRewardMoney() return questLogSelection == 2 and 120 or 0 end

function GetNumFriends() return 2 end
function GetFriendInfo(i)
    if i == 1 then return "Tankadin", 22, "Paladin", "Stormwind City", true, "", "dungeon buddy" end
    return "Oldfriend", 60, "Mage", "", false, "", ""
end
-- Equipment sets in the old API (by number and by name).
function GetNumEquipmentSets() return 1 end
function GetEquipmentSetInfo(i) if i == 1 then return "Leveling", 134, 3, false, 2, 1, 1, 1, 0 end end
function GetEquipmentSetItemIDs(name) if name == "Leveling" then return { [16] = 5586, [17] = 0, [18] = 2780 } end end

function GetNumSpellTabs() return 1 end
function GetSpellTabInfo(tab) return "Marksmanship", nil, 0, 1 end
function GetSpellBookItemName(i, kind) return "Aimed Shot", "Rank 1" end

function GetNumSkillLines() return 2 end
function GetSkillLineInfo(i)
    if i == 1 then return "Professions", true end
    return "Skinning", false, false, 96, 0, 0, 150
end
"""

# Forever client: no classic talent and pet functions, talents on
# C_Traits (one tree, three tab groups), happiness in C_PetInfo.
FOREVER_STUBS = r"""
GetNumTalentTabs, GetTalentTabInfo, GetNumTalents, GetTalentInfo = nil, nil, nil, nil
GetPetHappiness, GetPetLoyalty, GetPetTrainingPoints, GetPetFoodTypes = nil, nil, nil, nil

-- Client "secrets": the test puts here values the client should hide (e.g. an NPC name, message text).
secretValues = {}
function issecretvalue(v) return v ~= nil and secretValues[v] == true end
function UnitPower(unit, kind) return 870 end
function UnitPowerMax(unit, kind) return 1050 end
function GetPetExperience() return 1200, 4300 end

C_PetInfo = {
    GetPetHappiness = function() return 3, 125, 1 end,
    GetPetTamableSpecs = function() return {} end,
}

-- Weapon skills: the character knows daggers, bows and two-handed swords.
local weaponSkills = { [173] = { "Daggers", 98, 100 }, [45] = { "Bows", 100, 100 }, [55] = { "Two-Handed Swords", 1, 100 },
    -- Profession skill lines (the addon reads only their names to find skillID for the old Skills window).
    [393] = { "Skinning" }, [186] = { "Mining" }, [185] = { "Cooking" } }
C_SkillInfo = {
    GetSkillLineInfoByID = function(id)
        local s = weaponSkills[id]
        if s then return s[1], s[2], s[3], 0 end
    end,
}

C_ClassTalents = { GetActiveConfigID = function() return 501 end }
-- Positions as in the real client: pixels in a 600 grid (row 1 = 2130, tab columns
-- from 1020, 5620, 9080), one position 10 times too large (27300) and one jitter of +10 (3940).
local nodes = {
    [1] = { activeRank = 5, maxRanks = 5, groupIDs = { 900, 11 }, entryIDs = { 101 }, posX = 1020, posY = 2130, isAvailable = true, canPurchaseRank = false },
    [2] = { activeRank = 2, maxRanks = 2, groupIDs = { 11 }, entryIDs = { 102 }, posX = 1620, posY = 2130, isAvailable = true, canPurchaseRank = false },
    [3] = { activeRank = 0, maxRanks = 5, groupIDs = { 12 }, entryIDs = { 103 }, posX = 5620, posY = 2130, isAvailable = true, canPurchaseRank = true },
    [4] = { activeRank = 4, maxRanks = 5, groupIDs = { 12 }, entryIDs = { 104 }, posX = 6220, posY = 27300, isAvailable = true, canPurchaseRank = true },
    [5] = { activeRank = 0, maxRanks = 1, groupIDs = { 13 }, entryIDs = { 105, 106 }, posX = 9080, posY = 3940, isAvailable = false },
    -- Node of another class (group without a display name): skipped.
    [6] = { activeRank = 0, maxRanks = 3, groupIDs = { 77 }, entryIDs = { 107 } },
    [7] = { activeRank = 0, maxRanks = 1, groupIDs = { 11 }, entryIDs = { 108 }, isVisible = false },
    [8] = { activeRank = 0, maxRanks = 5, groupIDs = { 13 }, entryIDs = { 109 }, posX = 9680, posY = 2130, isAvailable = true, canPurchaseRank = false },
}
local names = { [101] = "Endurance Training", [102] = "Focused Fire", [103] = "Efficiency", [104] = "Lethal Shots",
    [105] = "Wyvern Sting", [106] = "Counterattack", [107] = "Shield Wall", [108] = "Hidden", [109] = "Deflection" }
-- Talent spells; only some have an API description (the rest: an empty string, as in the client before data loads).
local talentSpells = { [102] = 1223755, [104] = 19426, [109] = 19295 }
C_Traits = {
    GetConfigInfo = function(configID) return { treeIDs = { 1120 } } end,
    GetGroupDisplayInfoByTreeID = function(treeID)
        return {
            { groupID = 12, displayName = "Marksmanship", orderIndex = 2 },
            { groupID = 11, displayName = "Beast Mastery", orderIndex = 1 },
            { groupID = 13, displayName = "Survival", orderIndex = 3 },
        }
    end,
    GetTreeCurrencyInfo = function(configID, treeID, excludeStaged)
        return { { traitCurrencyID = 3820, spent = 11, quantity = 0, maxQuantity = 11 } }
    end,
    GetTreeNodes = function(treeID) return { 1, 2, 3, 4, 5, 6, 7, 8 } end,
    GetNodeInfo = function(configID, nodeID) return nodes[nodeID] end,
    GetEntryInfo = function(configID, entryID) return { definitionID = entryID } end,
    GetDefinitionInfo = function(definitionID)
        if definitionID == 103 then return { spellID = 19416 } end
        return { overrideName = names[definitionID], spellID = talentSpells[definitionID] }
    end,
}

-- v0.2.0: stats, item descriptions, action bars, reputation, vendor, bank, flights.
function UnitHealthMax(unit) return unit == "pet" and 620 or 780 end
function UnitStat(unit, index) return 20 + index, 30 + index, 10, 0 end
function UnitArmor() return 300, 412, 412, 112, 0 end
function UnitAttackPower() return 120, 14, 0 end
function UnitRangedAttackPower() return 150, 20, 0 end
function GetCritChance() return 6.04 end
function GetRangedCritChance() return 8.26 end
function GetDodgeChance() return 9.5 end
function GetParryChance() return 5 end
function UnitRangedDamage() return 2.5, 31.2, 58.9, 0, 0, 1 end
function UnitDamage() return 21.4, 33.8, 10, 14, 0, 0, 1 end
function UnitAttackSpeed() return 1.8, 1.5 end

ITEM_MOD_AGILITY_SHORT = "Agility"
ITEM_MOD_DAMAGE_PER_SECOND_SHORT = "Damage Per Second"
-- Format strings the client has in its own language; the addon finds weapon damage, speed and DPS with them.
DAMAGE_TEMPLATE = "%s - %s Damage"
DPS_TEMPLATE = "(%s damage per second)"
WEAPON_SPEED = "Speed"
-- GetItemInfo also returns classID, subclassID and bindType (returns 12-14): Enum.ItemClass 2 Weapon / 15 Dagger,
-- 7 Trade Goods / 5 Cloth; Enum.ItemBind 2 = binds when equipped. The numbers match the type texts of the mock.
C_Item = {
    GetItemInfo = function(link)
        local id = tonumber(tostring(link):match("item:(%d+)")) or link
        if ahItemData[id] then return ahItemInfo(link) end
        if id == 5586 then return "Curvewood Dagger", link, 2, 18, 13, "Weapon", "Dagger", 1, "INVTYPE_WEAPON", 135, 812, 2, 15, 2 end
        if id == 7005 then return "Skinning Knife", link, 1, 4, 1, "Weapon", "Dagger", 1, "INVTYPE_WEAPON", 135, 16, 2, 15, 0 end
        return "Item " .. tostring(id), link, 1, 10, 1, "Trade Goods", "Cloth", 20, "INVTYPE_NON_EQUIP_IGNORE", 134, 13, 7, 5, 0
    end,
    -- Stat tokens; the DPS values equal what the server computes from the mock tooltips (9-18 / 1.6 and 2-5 / 1.6).
    GetItemInfoInstant = GetItemInfoInstant,
    IsItemDataCachedByID = IsItemDataCachedByID,
    GetItemStats = function(link)
        if ahItemData[ahItemId(link)] then return ahItemStats(link) end
        if tostring(link):find("item:5586") then return { ITEM_MOD_AGILITY_SHORT = 3, ITEM_MOD_STAMINA_SHORT = 2.04, ITEM_MOD_DAMAGE_PER_SECOND_SHORT = 8.4 } end
        if tostring(link):find("item:7005") then return { ITEM_MOD_DAMAGE_PER_SECOND_SHORT = 2.2 } end
        -- The equipped Webwood Slicer and Light Hunting Bow share the mock tooltip "9 - 18 Damage, Speed 1.60".
        if tostring(link):find("item:5540") or tostring(link):find("item:2780") then return { ITEM_MOD_DAMAGE_PER_SECOND_SHORT = 8.4 } end
        return {}
    end,
    GetItemNameByID = function(id) return "Item " .. tostring(id) end,
}
C_TooltipInfo = {
    GetInventoryItem = function(unit, slot)
        return { lines = {
            { leftText = "|cff1eff00Curvewood Dagger|r" },
            { leftText = "One-Hand", rightText = "Dagger" },
            { leftText = "9 - 18 Damage", rightText = "Speed 1.60" },
            { leftText = " " },
        } }
    end,
    GetBagItem = function(bag, slot) return { lines = { { leftText = "Skinning Knife" }, { leftText = "2 - 5 Damage", rightText = "Speed 1.60" } } } end,
    GetMerchantItem = function(index) return { lines = { { leftText = "Laminated Recurve Bow" } } } end,
    GetItemByID = function(id) if id == 5586 then return { lines = { { leftText = "Curvewood Dagger" }, { leftText = "One-Hand", rightText = "Dagger" } } } end end,
    GetQuestItem = function(kind, i) if kind == "choice" and i == 1 then return { lines = { { leftText = "Curvewood Dagger" }, { leftText = "9 - 18 Damage", rightText = "Speed 1.60" } } } end end,
    -- Spell tooltip: the first line is the name, then the description (Focused Fire has no data yet).
    GetSpellByID = function(id)
        if id == 1225451 then return { lines = { { leftText = "Working Overtime" }, { leftText = "Passive" } } } end
        if id == 1225440 then return { lines = { { leftText = "Gourmand" }, { leftText = "Passive" }, { leftText = "Food heals you more." } } } end
        if id == 19426 then return { lines = { { leftText = "Lethal Shots" }, { leftText = "Increases your critical strike chance with ranged weapons by |cffffffff1%|r." } } } end
        if id == 19295 then return { lines = { { leftText = "Deflection" }, { leftText = "Increases your Parry chance by 1%." } } } end
    end,
}
C_UnitAuras = {
    GetAuraDataByIndex = function(unit, index, filter)
        -- The first aura lasts 600 s and ends in 450 s (GetTime clock); the second is an effect without a duration.
        if filter == "HELPFUL" and index == 1 then
            return { name = "Aspect of the Hawk", spellId = 13165, icon = 136076, applications = 0, duration = 600, expirationTime = 1450 }
        end
        if filter == "HARMFUL" and index == 1 then return { name = "Ghost", spellId = 8326, icon = 136135, applications = 0, duration = 0, expirationTime = 0 } end
    end,
}
C_QuestLog.GetAllCompletedQuestIDs = function() return { 918, 456, 457 } end

spellDataRequested = {}
local actions = { [1] = { "spell", 3044 }, [2] = { "macro", 1 }, [13] = { "item", 4540 } }
function HasAction(slot) return actions[slot] ~= nil end
function GetActionInfo(slot) local a = actions[slot]; if a then return a[1], a[2], "" end end
C_Spell = {
    IsSelfBuff = function(id) return id == 13165 end,
    GetSpellName = function(id) if id == 3044 then return "Arcane Shot" end if id == 19416 then return "Efficiency" end end,
    GetSpellDescription = function(id) if id == 19416 then return "Reduces the Mana cost of your Shots and Stings by |cffffffff2%|r." end return "" end,
    RequestLoadSpellData = function(id) spellDataRequested[#spellDataRequested + 1] = id end,
    -- Aspect of the Hawk has (unrealistically) a 5 min cooldown here, Hunter's Mark only the global one.
    GetSpellCooldown = function(id)
        if id == 13165 then return { startTime = 990, duration = 300, isEnabled = true } end
        if id == 1130 then return { startTime = 999, duration = 1.5, isEnabled = true } end
        return { startTime = 0, duration = 0, isEnabled = true }
    end,
}
function GetTime() return 1000 end
function GetBindingKey(command) return command == "ACTIONBUTTON1" and "1" or nil end
function GetNumMacros() return 1, 1 end
function GetMacroInfo(index) if index == 1 then return "Assist", 134400, "/petattack\n/cast Hunter's Mark" end if index == 121 then return "Feed", 134400, "/cast Feed Pet" end end

NUM_PET_ACTION_SLOTS = 10
PET_ACTION_ATTACK = "Attack"
function GetPetActionInfo(slot)
    if slot == 1 then return "PET_ACTION_ATTACK", 132152, true, false, false, false, nil end
    if slot == 4 then return "Growl", 132270, false, false, true, true, 2649 end
    -- An ability with auto-cast turned off: autoCast must be false, not missing.
    if slot == 5 then return "Claw", 132152, false, false, true, false, 16827 end
end
C_SpellBook.HasPetSpells = function() return 2, "PET" end
Enum.SpellBookItemType = { None = 0, Spell = 1, FutureSpell = 2, PetAction = 3, Flyout = 4 }
local petItems = { { spellID = 2649, name = "Growl", subName = "Rank 3", itemType = 1 }, { spellID = 16827, name = "Claw", subName = "Rank 2", itemType = 1 } }
local playerItemInfo = C_SpellBook.GetSpellBookItemInfo
C_SpellBook.GetSpellBookItemInfo = function(i, bank)
    if bank == 1 then return petItems[i] end
    return playerItemInfo(i, bank)
end

FACTION_STANDING_LABEL5 = "Friendly"
C_Reputation = {
    GetNumFactions = function() return 2 end,
    GetFactionDataByIndex = function(i)
        if i == 1 then return { name = "Alliance", isHeader = true } end
        return { name = "Darnassus", reaction = 5, currentStanding = 4200, currentReactionThreshold = 3000, nextReactionThreshold = 9000 }
    end,
}

function GetMerchantNumItems() return 2 end
function CanMerchantRepair() return true end
C_MerchantFrame = {
    GetItemInfo = function(i)
        if i == 1 then return { name = "Sharp Arrow", price = 300, stackCount = 200, numAvailable = -1, isUsable = true } end
        return { name = "Laminated Recurve Bow", price = 7295, stackCount = 1, numAvailable = 1, isUsable = true }
    end,
}
function GetMerchantItemLink(i)
    if i == 1 then return "|cffffffff|Hitem:2515:0|h[Sharp Arrow]|h|r" end
    return "|cffffffff|Hitem:5586:0|h[Laminated Recurve Bow]|h|r"
end

NUM_BANKBAGSLOTS = 2
Enum.BagIndex = { Backpack = 0, Bank = -1, BankBag_1 = 6, Reagent = 5 }
local bankContents = { [-1] = { size = 24, name = "Bank", items = { [1] = { itemID = 2589, stackCount = 20, quality = 1, hyperlink = "|cffffffff|Hitem:2589:0|h[Linen Cloth]|h|r" } } } }
local playerSlots, playerBagName, playerItem = C_Container.GetContainerNumSlots, C_Container.GetBagName, C_Container.GetContainerItemInfo
C_Container.GetContainerNumSlots = function(bag) if bankContents[bag] then return bankContents[bag].size end return playerSlots(bag) end
C_Container.GetBagName = function(bag) if bankContents[bag] then return bankContents[bag].name end return playerBagName(bag) end
C_Container.GetContainerItemInfo = function(bag, slot) if bankContents[bag] then return bankContents[bag].items[slot] end return playerItem(bag, slot) end

function GetTaxiMapID() return 1439 end

-- v0.3.0: Hearthstone in the backpack with a cooldown, ammo, currencies, professions, mail, played time.
local hearth = { itemID = 6948, stackCount = 1, quality = 1, isBound = true, hyperlink = "|cffffffff|Hitem:6948:0|h[Hearthstone]|h|r" }
local itemInfo2 = C_Container.GetContainerItemInfo
C_Container.GetContainerItemInfo = function(bag, slot) if bag == 0 and slot == 2 then return hearth end return itemInfo2(bag, slot) end
C_Container.GetContainerItemLink = function(bag, slot) local i = C_Container.GetContainerItemInfo(bag, slot); return i and i.hyperlink end
C_Container.GetContainerItemCooldown = function(bag, slot) if bag == 0 and slot == 2 then return 400, 3600, 1 end return 0, 0, 1 end
C_Container.GetContainerNumFreeSlots = function(bag) return bag == 0 and 12 or 0 end
equippedAmmo = "|cffffffff|Hitem:2515:0|h[Sharp Arrow]|h|r"
local invLink = GetInventoryItemLink
function GetInventoryItemLink(unit, slot) if slot == 0 then return equippedAmmo end return invLink(unit, slot) end
function GetInventoryItemCount(unit, slot) if slot == 0 then return 812 end return 0 end
function GetQuestGreenRange() return 9 end
MAX_QUESTLOG_QUESTS = 20
function GetRepairAllCost() return 1234, true end

C_CurrencyInfo = {
    GetCurrencyListSize = function() return 3 end,
    GetCurrencyListInfo = function(i)
        if i == 1 then return { name = "Player vs. Player", isHeader = true } end
        if i == 2 then return { name = "Honor Points", isHeader = false, quantity = 1520, maxQuantity = 75000 } end
        return { name = "Darkmoon Prize Ticket", isHeader = false, quantity = 12, maxQuantity = 0 }
    end,
    GetCurrencyListLink = function(i) if i == 2 then return "|cff00aa00|Hcurrency:1901:0|h[Honor Points]|h|r" end end,
}

-- Professions in Forever: only C_TradeSkillUI (the client has no classic GetTradeSkill* and GetCraft*, per /mcp probe).
local recipeInfo = {
    [2149] = { name = "Light Armor Kit", relativeDifficulty = 3, numAvailable = 7, learned = true },
    [3756] = { name = "Embossed Leather Gloves", relativeDifficulty = 0, numAvailable = 0, learned = true },
    [3816] = { name = "Cured Light Hide", relativeDifficulty = 2, numAvailable = 0, learned = true },
    [9999] = { name = "Unlearned Pattern", relativeDifficulty = 0, numAvailable = 0, learned = false },
}
local schematics = {
    [2149] = { outputItemID = 2304, reagentSlotSchematics = { { reagentType = 1, quantityRequired = 1, reagents = { { itemID = 2318 } } } } },
    [3756] = { outputItemID = 4239, reagentSlotSchematics = {
        { reagentType = 1, quantityRequired = 3, reagents = { { itemID = 2318 } } },
        { reagentType = 1, quantityRequired = 2, reagents = { { itemID = 2320 } } },
        { reagentType = 0, quantityRequired = 1, reagents = { { itemID = 7777 } } },
    } },
    [3816] = { outputItemID = 4231, reagentSlotSchematics = { { reagentType = 1, quantityRequired = 1, reagents = { { itemID = 783 } } } } },
}
C_TradeSkillUI = {
    GetBaseProfessionInfo = function() return { professionName = "Leatherworking", skillLevel = 87, maxSkillLevel = 150 } end,
    GetAllRecipeIDs = function() return { 2149, 3756, 3816, 9999 } end,
    GetRecipeInfo = function(id) return recipeInfo[id] end,
    GetRecipeSchematic = function(id, isRecraft) return schematics[id] end,
    GetRecipeCooldown = function(id) if id == 3816 then return 43200 end return 0 end,
}
C_Item.GetItemCount = function(id) if id == 2318 then return 7 end return 0 end

ATTACHMENTS_MAX_RECEIVE = 2
function GetInboxNumItems() return 2, 2 end
function GetInboxHeaderInfo(i)
    if i == 1 then return nil, nil, "Auction House", "Auction successful: Copper Ore", 5400, 0, 2.5, false, true, false end
    return nil, nil, "Altpiekasz", "mats", 0, 0, 29.9, true, false, false
end
function GetInboxItem(i, j) if i == 2 and j == 1 then return "Linen Cloth", 2589, 134, 20, 1, true end end
function GetInboxItemLink(i, j) if i == 2 and j == 1 then return "|cffffffff|Hitem:2589:0|h[Linen Cloth]|h|r" end end
local function at(x, y) return { GetXY = function() return x, y end } end
-- The flight map (1439) has no zones; zones come from the player's continent (1414, parent of map 1453) and its children.
-- Auberdine: from the zone's point list, Rut'theran: from the map position, Astranaar: no position and outside the lists.
C_TaxiMap = {
    GetAllTaxiNodes = function(mapID)
        return {
            { nodeID = 26, name = "Auberdine, Darkshore", state = 0, position = at(0.2, 0.4) },
            { nodeID = 27, name = "Rut'theran Village, Teldrassil", state = 1, position = at(0.4, 0.2) },
            { nodeID = 28, name = "Astranaar, Ashenvale", state = 2 },
        }
    end,
    GetTaxiNodesForMap = function(mapID)
        if mapID == 1439 then return { { nodeID = 26, name = "Auberdine, Darkshore" } } end
        return {}
    end,
}
local maps = {
    [1453] = { mapID = 1453, name = "Stormwind City", mapType = 3, parentMapID = 1414 },
    [1414] = { mapID = 1414, name = "Kalimdor", mapType = 2, parentMapID = 947 },
}
C_Map.GetMapInfo = function(id) return maps[id] end
C_Map.GetFallbackWorldMapID = function() return 947 end
C_Map.GetMapChildrenInfo = function(id, kind, all)
    if id == 947 then return { { mapID = 1414, name = "Kalimdor", mapType = 2 } } end
    if id == 1414 then return { { mapID = 1439, name = "Darkshore", mapType = 3 }, { mapID = 1438, name = "Teldrassil", mapType = 3 } } end
    return {}
end
C_Map.GetMapInfoAtPosition = function(mapID, x, y)
    if x < 0.3 then return { mapID = 1439, name = "Darkshore" } end
    return { mapID = 1438, name = "Teldrassil" }
end

"""

# v0.4.0 in Forever: new auction house API, raid, Inspect, C_LFGList, lockouts.
FOREVER_V4_STUBS = r"""
GetNumAuctionItems, GetAuctionItemInfo = nil, nil
local names = { [2318] = "Light Leather", [2589] = "Linen Cloth", [2770] = "Copper Ore", [5586] = "Curvewood Dagger", [2320] = "Coarse Thread", [2506] = "Hornwood Recurve Bow", [2507] = "Ranger's Shortbow", [2508] = "Silk Thread" }
-- Auction rows as the modern window gives them: a random suffix is part of the item key and only the suffixed link has
-- its stats; the trinket's data is not loaded until the addon asks for it, and the Ranger's Shortbow never loads.
ahItemData[12020] = { cached = true, name = "Thallium Choker", quality = 2, itemLevel = 36, requiredLevel = 31, sellPrice = 3969, stats = {},
                      type = "Armor", subType = "Miscellaneous", equipLoc = "INVTYPE_NECK", classID = 4, subclassID = 0 }
ahItemData[1404] = { cached = false, name = "Tidal Charm", quality = 2, itemLevel = 45, requiredLevel = 40, sellPrice = 2500, stats = {},
                      type = "Armor", subType = "Miscellaneous", equipLoc = "INVTYPE_TRINKET", classID = 4, subclassID = 0 }
names[12020] = "Thallium Choker"
local suffixes = {
    [585] = { "of the Owl", "+4 Intellect", { ITEM_MOD_INTELLECT_SHORT = 4, ITEM_MOD_SPIRIT_SHORT = 4 } },
    [600] = { "of the Monkey", "+4 Agility", { ITEM_MOD_AGILITY_SHORT = 4, ITEM_MOD_STAMINA_SHORT = 4 } },
}
local function linkSuffix(link) return suffixes[tonumber(tostring(link):match("item:%d+::::::(%d+)"))] end
local baseItemStats = C_Item.GetItemStats
C_Item.GetItemStats = function(link) local s = linkSuffix(link) if s then return s[3] end return baseItemStats(link) end
C_Item.GetItemNameByID = function(id)
    local d = ahItemData[id]
    if id == 1404 then return d.cached and d.name or nil end
    return names[id]
end
itemDataRequests, browseReads = {}, 0
C_Item.RequestLoadItemDataByID = function(id)
    itemDataRequests[#itemDataRequests + 1] = id
    if id == 1404 then ahItemData[id].cached = true end
end
local function chokerLines(s) return { lines = { { leftText = "Thallium Choker " .. s[1] }, { leftText = "Neck" }, { leftText = s[2] } } } end
C_TooltipInfo.GetItemKey = function(id, level, suffix)
    local d = ahItemData[id]
    if not (d and d.cached) then uncachedLookups[#uncachedLookups + 1] = "GetItemKey " .. tostring(id) return nil end
    if id == 12020 and suffixes[suffix] then return chokerLines(suffixes[suffix]) end
    if id == 1404 then return { lines = { { leftText = d.name }, { leftText = "Trinket" } } } end
    return { lines = { { leftText = d.name } } }
end
-- The link's tooltip has the Use effect the item key tooltip leaves out; the bow's names another item and is ignored.
C_TooltipInfo.GetHyperlink = function(link)
    local s = linkSuffix(link)
    if s then return chokerLines(s) end
    if ahItemId(link) == 1404 then return { lines = { { leftText = "Tidal Charm" }, { leftText = "Trinket" }, { leftText = "Use: Stuns the target for 3 sec." } } } end
    if ahItemId(link) == 2506 then return { lines = { { leftText = "Some Other Bow" } } } end
end
-- Server requests and actions in the auction house: the addon must not call them (forbid records every call).
C_AuctionHouse = {
    SendBrowseQuery = forbid("C_AuctionHouse.SendBrowseQuery"), SendSearchQuery = forbid("C_AuctionHouse.SendSearchQuery"),
    ReplicateItems = forbid("C_AuctionHouse.ReplicateItems"), QueryOwnedAuctions = forbid("C_AuctionHouse.QueryOwnedAuctions"),
    RefreshCommoditySearchResults = forbid("C_AuctionHouse.RefreshCommoditySearchResults"),
    RefreshItemSearchResults = forbid("C_AuctionHouse.RefreshItemSearchResults"),
    PlaceBid = forbid("C_AuctionHouse.PlaceBid"), PostItem = forbid("C_AuctionHouse.PostItem"),
    GetItemKeyInfo = function(key) local name = C_Item.GetItemNameByID(key.itemID) if name then return { itemID = key.itemID, itemName = name } end end,
    GetBrowseResults = function()
        browseReads = browseReads + 1
        -- Light Leather appears twice (different keys, the same name): minimum 25 and total quantity 150 must remain.
        return {
            { itemKey = { itemID = 2318, itemLevel = 5 }, minPrice = 30, totalQuantity = 10 },
            { itemKey = { itemID = 2318 }, minPrice = 25, totalQuantity = 140 },
            { itemKey = { itemID = 5586 }, minPrice = 9500, totalQuantity = 1 },
            { itemKey = { itemID = 2506 }, minPrice = 4200, totalQuantity = 2 },
            { itemKey = { itemID = 2507 }, minPrice = 3100, totalQuantity = 1 },
            { itemKey = { itemID = 2508 }, minPrice = 40, totalQuantity = 80 },
            { itemKey = { itemID = 12020, itemLevel = 36, itemSuffix = 585 }, minPrice = 8001, totalQuantity = 3 },
            { itemKey = { itemID = 12020, itemLevel = 36, itemSuffix = 600 }, minPrice = 9500, totalQuantity = 2 },
            { itemKey = { itemID = 1404, itemLevel = 45 }, minPrice = 52000, totalQuantity = 1 },
        }
    end,
    GetNumCommoditySearchResults = function(id) return id == 2589 and 2 or 0 end,
    GetCommoditySearchResultInfo = function(id, i)
        if i == 1 then return { itemID = 2589, quantity = 40, unitPrice = 12 } end
        return { itemID = 2589, quantity = 100, unitPrice = 15 }
    end,
    GetNumItemSearchResults = function(key) return 1 end,
    GetItemSearchResultInfo = function(key, i)
        if key.itemID == 12020 then return { quantity = 1, buyoutAmount = 9400, itemLink = "|cff1eff00|Hitem:12020::::::600:77|h[Thallium Choker of the Monkey]|h|r" } end
        return { quantity = 1, buyoutAmount = 9000, bidAmount = 7000 }
    end,
    GetNumOwnedAuctions = function() return 1 end,
    GetOwnedAuctionInfo = function(i)
        return { itemKey = { itemID = 2770 }, itemLink = "|cffffffff|Hitem:2770:0|h[Copper Ore]|h|r", status = 0, quantity = 10, buyoutAmount = 600, timeLeftSeconds = 7200 }
    end,
}

raidUnits = { raid1 = { "Piekasz", 20 }, raid2 = { "Tankadin", 22 }, raid3 = { "Healbot", 19 } }
partyUnits = { target = { "Tankadin", 22, "Paladin", "PALADIN", "Human", "TANK" } }
for unit, r in pairs(raidUnits) do
    if unit ~= "raid1" then partyUnits[unit] = { r[1], r[2], "Paladin", "PALADIN", "Human", "TANK" } end
end
function IsInRaid() return true end
function GetNumGroupMembers() return 3 end
function UnitExists(unit) return partyUnits[unit] ~= nil or unit == "raid1" or unit == "player" or unit == "pet" end
local un = UnitName
function UnitName(unit) if unit == "raid1" then return "Piekasz" end return un(unit) end
function GetRaidRosterInfo(i)
    local r = raidUnits["raid" .. i]
    return r[1], i == 2 and 2 or 0, 1, r[2], "Paladin", "PALADIN", "Blackfathom Deeps", true, false, nil, i == 2
end
function UnitGUID(unit) if unit == "target" then return "Player-1-TANK" end end
InspectFrame = { unit = "target" }
local inspectGear = { [1] = "|cff1eff00|Hitem:5586:0|h[Curvewood Dagger]|h|r", [16] = "|cff1eff00|Hitem:5586:0|h[Curvewood Dagger]|h|r" }
local invLink2 = GetInventoryItemLink
function GetInventoryItemLink(unit, slot) if unit == "target" then return inspectGear[slot] end return invLink2(unit, slot) end
local guildInfo = GetGuildInfo
function GetGuildInfo(unit) if unit == "target" then return "Night Watch" end return guildInfo(unit) end

C_LFGList = {
    GetAvailableCategories = function() return { 2 } end,
    GetLfgCategoryInfo = function(id) return { name = "Dungeons" } end,
    GetAvailableActivities = function(id) return { 801, 802 } end,
    GetActivityInfoTable = function(id)
        if id == 801 then return { fullName = "Blackfathom Deeps", categoryID = 2, groupFinderActivityGroupID = 103, minLevel = 19, maxLevel = 0, minLevelSuggestion = 24, maxLevelSuggestion = 32, maxNumPlayers = 5 } end
        return { fullName = "Wailing Caverns", minLevel = 10, maxLevelSuggestion = 25, minLevelSuggestion = 17, maxNumPlayers = 5 }
    end,
}
-- v0.6.0: quest window at an NPC (offer and turn-in), stable from the new API, tooltip via TooltipDataProcessor.
questWindowTitle = "The Defias Brotherhood"
function GetTitleText() return questWindowTitle end
function GetQuestID() return 65 end
function GetObjectiveText() return "Kill 15 Defias Trappers." end
function GetRewardText() return "Well done, Piekasz." end
local un3 = UnitName
function UnitName(unit) if unit == "questnpc" then return "Gryan Stoutmantle" end return un3(unit) end
local giverChoices = { { "Curvewood Dagger", 135, 1, 2, true, 5586 }, { "Webwood Slicer", 135, 1, 2, false, 5540 } }
function GetNumQuestChoices() return #giverChoices end
function GetNumQuestRewards() return 1 end
function GetQuestItemInfo(kind, i)
    if kind == "choice" and giverChoices[i] then return unpack(giverChoices[i]) end
    if kind == "reward" and i == 1 then return "Linen Cloth", 134, 5, 1, true, 2589 end
end
function GetQuestItemLink(kind, i) if kind == "choice" and i == 1 then return "|cff1eff00|Hitem:5586:0|h[Curvewood Dagger]|h|r" end end
function GetRewardMoney() return 1500 end
function GetRewardXP() return 1150 end

-- Stable only via C_StableInfo (Forever has neither the classic GetStablePetInfo nor GetNumStableSlots).
GetNumStableSlots, GetStablePetInfo = nil, nil
C_StableInfo = {
    GetStabledPetList = function() return { { slotID = 6, name = "Owl", level = 18, familyName = "Owl", specialization = "Cunning" } } end,
    GetActivePetList = function() return { { slotID = 1, name = "Cat", level = 19, familyName = "Cat", specialization = "Ferocity" } } end,
    GetNumStableSlots = function() return 5 end,
    GetNumStablePets = function() return 2 end,
    GetStablePetInfo = function(slot) if slot == 6 then return { slotID = 6, name = "Owl", level = 18, familyName = "Owl" } end end,
}
-- Forever has no classic PvP ranks (UnitPVPRank, GetPVPRankInfo); it has GetPVPLifetimeStats and UnitHonor*.
UnitPVPRank, GetPVPRankInfo = nil, nil

Enum.TooltipDataType = { Item = 0, Spell = 1 }
tooltipPostCalls = {}
TooltipDataProcessor = { AddTooltipPostCall = function(kind, fn) tooltipPostCalls[kind] = fn end }

function GetNumSavedInstances() return 1 end
function GetSavedInstanceInfo(i) return "Blackfathom Deeps", 4242, 3600, 1, true, false, 0, false, 5, "Normal", 7, 3, false, 48 end

-- Functions the real Forever client lacks (globals checked via /mcp probe on 2026-11-15),
-- and their client equivalents.
GetAuctionItemLink, GetLootMethod, GetQuestGreenRange, UnitCharacterPoints = nil, nil, nil, nil
C_PetInfo.GetPetTamableSpecs = nil
C_PartyInfo = { GetLootMethod = function() return 3 end } -- Enum.LootMethod.Group
function UnitQuestTrivialLevelRange() return 9 end
"""

# Addon 0.15.0: a mock of the Forever Legacy system. The client documents no API for it, so the mock covers what the
# section looks for: a non-combat C_Traits config, a "Legacy" achievement category, and names for discovery (a
# namespace with readers and an action that must never be called, a global function and a frame carrying the configID).
# Addon 0.17.0: shaped like the first real dump (2026-11-22): a Generic config with trees 1118 (empty), 1187, 1188 and
# 1189 and no named groups, one trait currency shared by the trees, and spell tooltips that show only the spell type
# before the spell data loads. Names and IDs of the real perks; texts and point values invented.
LEGACY_TREE_STUBS = r"""
Enum.TraitConfigType = { Invalid = 0, Combat = 1, Profession = 2, Generic = 3 }
legacyActionCalls = {}
local legacyNodes = {
    [201] = { activeRank = 2, maxRanks = 5, groupIDs = { 22 }, entryIDs = { 301 }, posX = 1000, posY = 1000, isAvailable = true, canPurchaseRank = true,
              visibleEdges = { { targetNode = 202, isActive = true } } },
    [202] = { activeRank = 0, maxRanks = 5, groupIDs = { 22 }, entryIDs = { 302 }, posX = 1600, posY = 1000, isAvailable = false, canPurchaseRank = false,
              meetsEdgeRequirements = false },
    [203] = { activeRank = 0, maxRanks = 5, groupIDs = { 22 }, entryIDs = { 303 }, posX = 1000, posY = 1600, isAvailable = true, canPurchaseRank = true },
    [204] = { activeRank = 0, maxRanks = 1, groupIDs = { 22 }, entryIDs = { 304 }, posX = 2200, posY = 1000, isAvailable = false, canPurchaseRank = false,
              conditionIDs = { 9001 } },
    [205] = { activeRank = 0, maxRanks = 5, groupIDs = { 21 }, entryIDs = { 305 }, posX = 1000, posY = 1000, isAvailable = true, canPurchaseRank = true },
    [206] = { activeRank = 0, maxRanks = 3, groupIDs = { 23 }, entryIDs = { 306 }, posX = 1000, posY = 1000, isAvailable = true, canPurchaseRank = true },
    [207] = { activeRank = 0, maxRanks = 1, groupIDs = { 23 }, entryIDs = { 307 }, isVisible = false },
}
local legacyNames = { [301] = "Well Rested", [302] = "Talented", [303] = "Thrill of Adventure", [304] = "Frequent Flier",
    [305] = "Working Overtime", [306] = "Gourmand", [307] = "Hidden Perk" }
local legacyTexts = { [301] = "Your rested experience accumulates 4% faster and your rested experience cap is increased by 4%.",
    [303] = "You gain 1% of your maximum Health and Mana over 10 sec each time you deliver the killing blow to a non-trivial enemy." }
local configInfo, groups, currencies, nodeInfo, definition = C_Traits.GetConfigInfo, C_Traits.GetGroupDisplayInfoByTreeID,
    C_Traits.GetTreeCurrencyInfo, C_Traits.GetNodeInfo, C_Traits.GetDefinitionInfo
local treeNodes = C_Traits.GetTreeNodes
C_Traits.GetConfigsByType = function(configType)
    if configType == 1 then return { 501 } end
    if configType == 3 then return { 7001 } end
    return {}
end
local legacyTrees = { [1118] = {}, [1187] = { 205 }, [1188] = { 201, 202, 203, 204 }, [1189] = { 206, 207 } }
C_Traits.GetConfigInfo = function(configID)
    if configID == 7001 then return { ID = 7001, type = 3, name = "", treeIDs = { 1118, 1187, 1188, 1189 } } end
    return configInfo(configID)
end
C_Traits.GetGroupDisplayInfoByTreeID = function(treeID)
    if legacyTrees[treeID] then return {} end
    return groups(treeID)
end
C_Traits.GetTreeInfo = function(configID, treeID)
    if legacyTrees[treeID] then return { ID = treeID, gates = {}, hideSingleRankNumbers = false } end
end
C_Traits.GetTreeCurrencyInfo = function(configID, treeID, excludeStaged)
    if treeID == 1118 then return {} end
    if legacyTrees[treeID] then return { { traitCurrencyID = 4100, spent = 2, quantity = 3, maxQuantity = 5 } } end
    return currencies(configID, treeID, excludeStaged)
end
C_Traits.GetTreeNodes = function(treeID)
    if legacyTrees[treeID] then return legacyTrees[treeID] end
    return treeNodes(treeID)
end
C_Traits.GetNodeInfo = function(configID, nodeID)
    if configID == 7001 then return legacyNodes[nodeID] end
    return nodeInfo(configID, nodeID)
end
-- Working Overtime: a tooltip with only the spell type (no description yet); Gourmand: a full tooltip.
local legacySpells = { [305] = 1225451, [306] = 1225440 }
C_Traits.GetDefinitionInfo = function(definitionID)
    if legacyNames[definitionID] then return { overrideName = legacyNames[definitionID], spellID = legacySpells[definitionID] } end
    return definition(definitionID)
end
C_Traits.GetTraitDescription = function(entryID, rank) return legacyTexts[entryID] or "" end
C_Traits.GetConditionInfo = function(configID, conditionID)
    if conditionID == 9001 then return { isMet = false, spentAmountRequired = 5 } end
end
C_Traits.GetTraitCurrencyInfo = function(currencyID)
    if currencyID == 4100 then return 0, 1, 3500, 1 end
end
C_Traits.GetMaxAvailableTraitCurrency = function(currencyID) return 16 end
C_Traits.PurchaseRank = function() legacyActionCalls[#legacyActionCalls + 1] = "PurchaseRank" end
C_Traits.CommitConfig = function() legacyActionCalls[#legacyActionCalls + 1] = "CommitConfig" end
local currencyInfo = C_CurrencyInfo.GetCurrencyInfo
C_CurrencyInfo.GetCurrencyInfo = function(id)
    if id == 3500 then return { name = "Legacy Points", quantity = 3, totalEarned = 5, maxQuantity = 65 } end
    if currencyInfo then return currencyInfo(id) end
end

-- Challenges: a "Legacy" category with a subcategory; another category is skipped.
local categories = { [92] = { "General", -1 }, [15500] = { "Legacy", -1 }, [15501] = { "Classes", 15500 } }
function GetCategoryList() return { 92, 15500, 15501 } end
function GetCategoryInfo(id) local c = categories[id] if c then return c[1], c[2] end end
function GetCategoryNumAchievements(id) if id == 15501 then return 2, 1, 1 end if id == 92 then return 1, 1, 0 end return 0, 0, 0 end
function GetAchievementInfo(categoryID, index)
    if categoryID == 92 then return 6, "Level 10", 10, true end
    if index == 1 then return 70001, "Hunter: Level 25", 1, true, 11, 20, 26, "Reach level 25 with a Hunter.", 0, 1, "Reward: 1 Legacy Point", false, true end
    return 70002, "Warlock: Level 25", 1, false, nil, nil, nil, "Reach level 25 with a Warlock.", 0, 1, "", false, false
end

-- Discovery: a namespace (two readers, one reader that needs an argument, one action), a namespace named for challenges,
-- a global function and a frame (a child table is listed by name only).
C_LegacyMock = {
    GetPointsInfo = function() return { earned = 5, spent = 2, perCharacterCap = 16 } end,
    IsEnabled = function() return true end,
    GetPerkInfo = function(perkID) return perkID.name end,
    LearnPerk = function() legacyActionCalls[#legacyActionCalls + 1] = "LearnPerk" end,
}
function ToggleLegacyFrameMock() legacyActionCalls[#legacyActionCalls + 1] = "ToggleLegacyFrameMock" end
LegacyFrameMock = { configID = 7001, title = "Legacy", GetObjectType = function() return "Frame" end, IsShown = function() return false end,
    ChallengesTab = { label = "Challenges" } }
C_PlayerChallengesMock = { GetActiveChallenges = function() return { { id = 1, name = "Reach level 25" } } end }
-- A function named for challenges inside an unrelated namespace (the real client has no C_*Legacy* namespace).
C_GameRules = C_GameRules or {}
C_GameRules.GetLegacyChallengeList = function() return { { challengeID = 9, completed = true } } end
C_GameRules.SetLegacyChallengeTracked = function() legacyActionCalls[#legacyActionCalls + 1] = "SetLegacyChallengeTracked" end
"""

# Clock for timing sections: a deterministic, increasing counter (steps of 0.5, 2.5 and 1.0 ms
# in turn), so the dump is the same every time. The older (legacy) client does not have it.
# Addon 0.14.0: a mock of the QuestieDB public API (global LibQuestieDB: entity Get / Exists / IdsByName) and of
# Guidelime (registerGuide, GuidelimeDataChar). All quest, NPC and guide data below is invented for the tests; the
# addon ships no quest or guide data and reads these only when the player has those addons installed.
QUEST_SOURCES_STUBS = r"""
local areaNames = { [148] = "Darkshore", [331] = "Ashenvale", [719] = "Blackfathom Deeps" }
C_Map.GetAreaInfo = function(areaID) return areaNames[areaID] end
C_QuestLog.IsQuestFlaggedCompleted = function(id) return id == 918 or id == 456 or id == 457 end

local rows = {
    Quest = {
        [947] = { name = "Cave Mushrooms", questLevel = 17, requiredLevel = 10, zoneOrSort = 148, nextQuestInChain = 948,
                  startedBy = { { 3583 } }, finishedBy = { { 3583 } },
                  objectivesText = { "Bring 5 Scaber Stalks and 1 Death Cap to Barithras Moonshade in Auberdine." },
                  objectives = { nil, { { 174795, "Scaber Stalk" } }, { { 5270 } } } },
        [948] = { name = "Onu", questLevel = 17, requiredLevel = 10, zoneOrSort = 148, startedBy = { { 3583 } }, finishedBy = { { 3616 } } },
        [4740] = { name = "WANTED: Murkdeep!", questLevel = 18, requiredLevel = 12, zoneOrSort = 148,
                   startedBy = { nil, { 176115 } }, finishedBy = { { 3693 } }, objectives = { { { 10323 } } } },
        [982] = { name = "Deep Ocean, Vast Sea", questLevel = 17, requiredLevel = 13, zoneOrSort = 148,
                  startedBy = { { 3644 } }, finishedBy = { { 3644 }, { 9999001 } }, objectives = { { { 2165, "Grizzled Thistle Bear slain" } } } },
        [-1] = { name = "Deleted quest" },
    },
    Npc = {
        [3583] = { name = "Barithras Moonshade", subName = "Mushroom Collector", friendlyToFaction = "A", minLevel = 22, maxLevel = 22,
                   zoneID = 148, spawns = { [148] = { { 37.32, 43.64 } } } },
        [3616] = { name = "Onu", friendlyToFaction = "A", zoneID = 148, spawns = { [148] = { { 43.56, 76.29 } } } },
        [3693] = { name = "Terenthis", friendlyToFaction = "A", zoneID = 148, spawns = { [148] = { { 39.37, 43.48 } } } },
        [3644] = { name = "Cerellean Whiteclaw", friendlyToFaction = "A", zoneID = 148, spawns = { [148] = { { 35.74, 43.71 } } } },
        [10323] = { name = "Murkdeep", minLevel = 19, maxLevel = 19, zoneID = 148, spawns = { [148] = { { 36.4, 76.6 } } } },
        -- A creature with spawn points in two zones and inside an instance: the main zone comes first.
        [2165] = { name = "Grizzled Thistle Bear", minLevel = 16, maxLevel = 17, zoneID = 148,
                   spawns = { [331] = { { 12.5, 10.25 } }, [148] = { { 41.2, 80.1 }, { 42.7, 83.9 }, { 39.9, 85.3 }, { 44.1, 88.6 } }, [719] = { { -1, -1 } } } },
    },
    Object = {
        [176115] = { name = "Wanted Poster", zoneID = 148, spawns = { [148] = { { 37.2, 44.2 } } } },
        [174795] = { name = "Scaber Stalk", zoneID = 148, spawns = { [148] = { { 55.3, 34.7 }, { 54.9, 33.2 } } } },
    },
    Item = { [5270] = { name = "Death Cap" } },
}
questDbReads = 0
local function entity(kind)
    local data = rows[kind]
    return {
        Get = function(id, key) questDbReads = questDbReads + 1; return data[id] and data[id][key] end,
        Exists = function(id) return data[id] ~= nil end,
        IdsByName = function(name)
            local ids = {}
            for id, row in pairs(data) do if row.name == name then ids[#ids + 1] = id end end
            table.sort(ids)
            if #ids == 0 then return nil end
            return ids
        end,
    }
end
LibQuestieDB = { Quest = entity("Quest"), Npc = entity("Npc"), Object = entity("Object"), Item = entity("Item"),
                 addonName = "QuestieDB", contractVersion = 4, readMode = "baked" }
C_AddOns = { GetAddOnMetadata = function(name, key) if name == "QuestieDB" and key == "Version" then return "1.1.3" end end }
-- Addon 0.18.0: the zone support data (area ID -> UI map ID) as the Lua source text QuestieDB ships, and the client's
-- link handler API (Forever: LinkUtil.RegisterLinkHandler, as Questie uses it). TomTom is absent unless a check adds it.
LibQuestieDB.Support = { Get = function(name)
    if name ~= "ZoneDB" then return nil end
    return { private = { areaIdToUiMapId = "return { [148] = 1, [331] = 1440 }", areaIdToUiMapIdOverride = "return { [148] = 1439 }" } }
end }
linkHandlers = {}
LinkProcessorResponse = { Unhandled = 1, Handled = 2 }
LinkUtil = { RegisterLinkHandler = function(linkType, handler) linkHandlers[linkType] = handler end }
modifiedClick = false
IsModifiedClick = function() return modifiedClick end

-- Guidelime: the registry function a guide addon calls, and the per-character progress data.
guidelimeRegistered = 0
Guidelime = { registerGuide = function(guide, group) guidelimeRegistered = guidelimeRegistered + 1; return {} end }
function hooksecurefunc(tbl, name, hook)
    local original = tbl[name]
    tbl[name] = function(...) local a, b, c = original(...); hook(...); return a, b, c end
end
GuidelimeDataChar = {
    currentGuide = "Mock Guides 17-19 Darkshore",
    progress = { ["Mock Guides 17-19 Darkshore"] = { [7] = { completed = true, stepText = "Set your [H] to Auberdine at [G37, 44.1Darkshore]" } } },
}
"""

# Registered after the addon loads, as guide addons do (they depend on Guidelime and load later).
MOCK_GUIDES = r"""
Guidelime.registerGuide([[
[GA Alliance]
[N19-21 Ashenvale]
[D Another mock guide: must not be picked.]
Accept: [QA4740]
]], "Mock Guides")
Guidelime.registerGuide([[
[GA Alliance]
[N17-19 Darkshore]
[NX19-21 Ashenvale]
[D A mock guide for the WowMcp tests.]
Accept: [QA947]
Accept: [QA4740]
Set your [H] to Auberdine at [G37, 44.1Darkshore]
TurnIn: [QT918]
Do: [QC947,1]
Run to [G55.2, 34.9Darkshore][A Dwarf, Gnome]
[O]Do alongside other quests: [QC4740- ]
Fly to [F Auberdine]
TurnIn: [QT947]
Accept: [QA948]
TurnIn: [QT4740]
Accept: [QA982][A Hunter]
Accept: [QA983][A Warrior]
Use your [H] Hearthstone.
]], "Mock Guides")
"""

CLOCK_STUBS = r"""
local steps, ticks, now = { 0.5, 2.5, 1.0 }, 0, 0
function debugprofilestop()
    ticks = ticks + 1
    now = now + steps[ticks % 3 + 1]
    return now
end
"""

# Output in the WoW client's SavedVariables format.
SERIALIZER = r"""
local function quote(s)
    return '"' .. s:gsub('\\', '\\\\'):gsub('"', '\\"'):gsub('\n', '\\n'):gsub('\r', '\\r') .. '"'
end

local function serialize(value, depth, out)
    local t = type(value)
    if t == "table" then
        out[#out + 1] = "{\n"
        local n = #value
        for i = 1, n do
            serialize(value[i], depth + 1, out)
            out[#out + 1] = ", -- [" .. i .. "]\n"
        end
        local keys = {}
        for k in pairs(value) do
            if not (type(k) == "number" and k >= 1 and k <= n and math.floor(k) == k) then
                keys[#keys + 1] = k
            end
        end
        table.sort(keys, function(a, b) return tostring(a) < tostring(b) end)
        for _, k in ipairs(keys) do
            if type(k) == "number" then
                out[#out + 1] = "[" .. k .. "] = "
            else
                out[#out + 1] = "[" .. quote(k) .. "] = "
            end
            serialize(value[k], depth + 1, out)
            out[#out + 1] = ",\n"
        end
        out[#out + 1] = "}"
    elseif t == "string" then
        out[#out + 1] = quote(value)
    else
        out[#out + 1] = tostring(value)
    end
end

function SerializeSavedVariable(name, value)
    local out = { "\n", name, " = " }
    serialize(value, 0, out)
    out[#out + 1] = "\n"
    return table.concat(out)
end
"""


# Events rejected by the real Forever client (probe section, events = false).
FOREVER_REJECTED_EVENTS = (
    "AUCTION_ITEM_LIST_UPDATE", "AUCTION_OWNED_LIST_UPDATE", "BROWSE_RESULTS_ADDED", "BROWSE_RESULTS_UPDATED",
    "CRAFT_SHOW", "CRAFT_UPDATE", "TRADE_SKILL_UPDATE",
)


# A German (deDE) client: the same mock game as "forever", with the text the client translates in German.
# Replacements in the stub source (old -> new), applied to every stub block. IDs, tokens and numbers stay the same,
# so the server must reach the same conclusions from the deDE dump as from the English one.
DEDE_TEXT = (
    ('function GetLocale() return "enUS" end', 'function GetLocale() return "deDE" end'),
    ('function UnitClass() return "Hunter", "HUNTER" end', 'function UnitClass() return "Jäger", "HUNTER" end'),
    ('function UnitRace() return "Night Elf", "NightElf" end', 'function UnitRace() return "Nachtelf", "NightElf" end'),
    ('return "Hunter", "", "header", true', 'return "Jäger", "", "header", true'),
    ('"Rank ', '"Rang '),
    ('[Skinning Knife]', '[Kürschnermesser]'), ('"Skinning Knife"', '"Kürschnermesser"'),
    ('[Sharp Arrow]', '[Scharfer Pfeil]'), ('"Sharp Arrow"', '"Scharfer Pfeil"'),
    ('[Tough Hunk of Bread]', '[Zähes Stück Brot]'), ('[Hearthstone]', '[Ruhestein]'),
    ('"Weapon", "Dagger"', '"Waffe", "Dolch"'), ('"Trade Goods", "Cloth"', '"Handwerkswaren", "Stoff"'),
    ('{ leftText = "One-Hand", rightText = "Dagger" }', '{ leftText = "Einhändig", rightText = "Dolch" }'),
    ('"9 - 18 Damage", rightText = "Speed 1.60"', '"9 - 18 Schaden", rightText = "Tempo 1,60"'),
    ('"2 - 5 Damage", rightText = "Speed 1.60"', '"2 - 5 Schaden", rightText = "Tempo 1,60"'),
    ('DAMAGE_TEMPLATE = "%s - %s Damage"', 'DAMAGE_TEMPLATE = "%s - %s Schaden"'),
    ('DPS_TEMPLATE = "(%s damage per second)"', 'DPS_TEMPLATE = "(%s Schaden pro Sekunde)"'),
    ('WEAPON_SPEED = "Speed"', 'WEAPON_SPEED = "Tempo"'),
    ('ITEM_MOD_AGILITY_SHORT = "Agility"', 'ITEM_MOD_AGILITY_SHORT = "Beweglichkeit"\nITEM_MOD_STAMINA_SHORT = "Ausdauer"'),
    ('ITEM_MOD_DAMAGE_PER_SECOND_SHORT = "Damage Per Second"', 'ITEM_MOD_DAMAGE_PER_SECOND_SHORT = "Schaden pro Sekunde"'),
    ('{ "Skinning", 96, 150, 393 }, { "Mining", 71, 75, 186 }, { "Cooking", 52, 75, 185 }',
     '{ "Kürschnerei", 96, 150, 393 }, { "Bergbau", 71, 75, 186 }, { "Kochkunst", 52, 75, 185 }'),
    ('[393] = { "Skinning" }, [186] = { "Mining" }, [185] = { "Cooking" }', '[393] = { "Kürschnerei" }, [186] = { "Bergbau" }, [185] = { "Kochkunst" }'),
    ('{ "Daggers", 98, 100 }, [45] = { "Bows", 100, 100 }, [55] = { "Two-Handed Swords", 1, 100 }',
     '{ "Dolche", 98, 100 }, [45] = { "Bögen", 100, 100 }, [55] = { "Zweihandschwerter", 1, 100 }'),
    ('professionName = "Leatherworking"', 'professionName = "Lederverarbeitung"'),
    ('PET_ACTION_ATTACK = "Attack"', 'PET_ACTION_ATTACK = "Angriff"'),
    ('FACTION_STANDING_LABEL5 = "Friendly"', 'FACTION_STANDING_LABEL5 = "Freundlich"'),
    ('LOOT_ITEM = "%s receives loot: %s."', 'LOOT_ITEM = "%s bekommt Beute: %s."'),
    ('LOOT_ITEM_MULTIPLE = "%s receives loot: %sx%d."', 'LOOT_ITEM_MULTIPLE = "%s bekommt Beute: %sx%d."'),
    ('LOOT_ITEM_SELF = "You receive loot: %s."', 'LOOT_ITEM_SELF = "Ihr erhaltet Beute: %s."'),
    ('LOOT_ITEM_SELF_MULTIPLE = "You receive loot: %sx%d."', 'LOOT_ITEM_SELF_MULTIPLE = "Ihr erhaltet Beute: %sx%d."'),
    ('LOOT_ITEM_PUSHED_SELF = "You receive item: %s."', 'LOOT_ITEM_PUSHED_SELF = "Ihr erhaltet einen Gegenstand: %s."'),
    ('LOOT_ITEM_PUSHED_SELF_MULTIPLE = "You receive item: %sx%d."', 'LOOT_ITEM_PUSHED_SELF_MULTIPLE = "Ihr erhaltet einen Gegenstand: %sx%d."'),
    ('COMBATLOG_XPGAIN_FIRSTPERSON = "%s dies, you gain %d experience."', 'COMBATLOG_XPGAIN_FIRSTPERSON = "%s stirbt, Ihr bekommt %d Erfahrung."'),
    ('COMBATLOG_XPGAIN_FIRSTPERSON_UNNAMED = "You gain %d experience."', 'COMBATLOG_XPGAIN_FIRSTPERSON_UNNAMED = "Ihr bekommt %d Erfahrung."'),
    ('COMBATLOG_XPGAIN_EXHAUSTION1 = "%s dies, you gain %d experience. (%s exp %s bonus)"',
     'COMBATLOG_XPGAIN_EXHAUSTION1 = "%s stirbt, Ihr bekommt %d Erfahrung. (%s EP %s Bonus)"'),
)

# Text the assertions expect, per mode (English unless the deDE client translates it).
DEDE_EXPECT = {
    "One-Hand | Dagger": "Einhändig | Dolch", "9 - 18 Damage | Speed 1.60": "9 - 18 Schaden | Tempo 1,60",
    "Hearthstone": "Ruhestein", "Leatherworking": "Lederverarbeitung", "Attack": "Angriff",
}


def localize(mode: str, stub: str) -> str:
    """The stub source for the mode: German text for "dede" (every replacement must apply), unchanged otherwise."""
    if mode != "dede":
        return stub
    for old, new in DEDE_TEXT:
        stub = stub.replace(old, new)
    return stub


def check_dede_text_applied() -> None:
    """Every deDE replacement must hit some stub, or the deDE mock silently stays English."""
    source = COMMON_STUBS + MODERN_STUBS + FOREVER_STUBS + FOREVER_V4_STUBS
    missing = [old for old, _ in DEDE_TEXT if old not in source]
    assert not missing, f"deDE replacements with no match: {missing}"


FOREVER_MODES = ("forever", "dede")


def expect(mode: str, text: str) -> str:
    return DEDE_EXPECT.get(text, text) if mode == "dede" else text


def event_dispatcher(g):
    """Fires an event like the client: only when the addon registered it and the client accepted it."""
    raw = g.handlers["OnEvent"]

    def on_event(frame, event, *args):
        if g.rejectedEvents[event]:
            return None
        return raw(frame, event, *args)
    return on_event


def load_addon(mode: str, before_addon: str = ""):
    """Loads the stubs for the mode, then `before_addon` (Lua), then the addon files; returns (runtime, globals, addon ns)."""
    runtime = lua51.LuaRuntime(unpack_returned_tuples=True)
    runtime.execute(localize(mode, COMMON_STUBS))
    runtime.execute(LEGACY_STUBS if mode == "legacy" else localize(mode, MODERN_STUBS))
    if mode != "legacy":
        runtime.execute(CLOCK_STUBS)
    if mode in FOREVER_MODES:
        # Events the real Forever 1.60.1 client did not accept (/mcp probe from 2026-11-15).
        runtime.execute("for _, e in ipairs({ " + ", ".join(f'"{e}"' for e in FOREVER_REJECTED_EVENTS) + " }) do rejectedEvents[e] = true end")
        runtime.execute(localize(mode, FOREVER_STUBS))
        runtime.execute(localize(mode, FOREVER_V4_STUBS))
        runtime.execute(QUEST_SOURCES_STUBS)
        runtime.execute(LEGACY_TREE_STUBS)
    if before_addon:
        runtime.execute(before_addon)
    # The client passes every addon file the same arguments: the addon name and a shared table.
    addon_ns = runtime.table()
    for path in addon_files():
        runtime.execute(path.read_text(encoding="utf-8"), "WowMcp", addon_ns)
    if mode in FOREVER_MODES:
        runtime.execute(MOCK_GUIDES)
    runtime.execute(SERIALIZER)
    return runtime, runtime.globals(), addon_ns


def check_robustness(runtime, g, on_event, mode: str) -> None:
    """Checks after the file is written (they change stub state, so they cannot affect the server test data)."""
    current = g.WowMcpDB["characters"]["Piekasz"]
    # The same friends list state is not saved again (capturedAt stays).
    captured = current["friends"]["capturedAt"]
    g.clockNow += 60
    on_event(None, "FRIENDLIST_UPDATE")
    assert current["friends"]["capturedAt"] == captured
    # A change in the list (a new friend) refreshes the record; limit of 100 entries.
    runtime.execute('GetNumFriends = function() return 150 end; GetFriendInfo = function(i) return "F" .. i, 10, "Mage", "Zone", true, "", "" end')
    runtime.execute('C_FriendList.GetNumFriends = GetNumFriends; C_FriendList.GetFriendInfoByIndex = nil')
    on_event(None, "FRIENDLIST_UPDATE")
    assert current["friends"]["total"] == 150 and len(current["friends"]["friends"]) == 100, current["friends"]["total"]

    # A large guild (250 people): only online, with the onlineOnly marker; the limit of 500 is not exceeded.
    runtime.execute('''
        guildRoster = {}
        for i = 1, 250 do
            guildRoster[i] = { "M" .. i, "Member", 3, 20, "Mage", "Zone", "", "", i % 10 == 0, 0, "MAGE" }
        end
    ''')
    g.clockNow += 60
    on_event(None, "GUILD_ROSTER_UPDATE")
    big = current["guild"]
    assert big["total"] == 250 and big["online"] == 25 and big["onlineOnly"] is True and len(big["members"]) == 25, dict(big)
    # List not loaded yet (0 people): the previous members stay, and the guild name is refreshed.
    runtime.execute('guildRoster = {}')
    g.clockNow += 60
    on_event(None, "PLAYER_GUILD_UPDATE")
    assert len(current["guild"]["members"]) == 25 and current["guild"]["name"] == "Night Watch"
    # Leaving the guild: only the fact inGuild = false remains instead of the list.
    runtime.execute('function IsInGuild() return false end')
    g.clockNow += 60
    on_event(None, "GUILD_ROSTER_UPDATE")
    assert current["guild"]["inGuild"] is False and current["guild"]["members"] is None, dict(current["guild"])

    if mode in FOREVER_MODES:
        # An empty buyback list with the vendor window open replaces the old record; a closed window does not touch it.
        runtime.execute('buybackItems = {}')
        on_event(None, "MERCHANT_UPDATE")
        assert len(current["buyback"]["items"]) == 0
    if mode != "legacy":
        # No API in the client: sections vanish without errors (manual dump, no /reload).
        runtime.execute('''
            C_EquipmentSet, GetPVPLifetimeStats, UnitHonor, UnitHonorMax, UnitHonorLevel, UnitPVPRank = nil, nil, nil, nil, nil, nil
            C_UnitAuras, C_TaxiMap, GetNumBuybackItems = nil, nil, nil
        ''')
        g.SlashCmdList["WOWMCP"]("")
        dumped = g.WowMcpDB["characters"]["Piekasz"]
        assert not list(dumped["errors"].values()), list(dumped["errors"].values())
        assert dumped["pvp"] is None and dumped["equipmentSets"] is None
        assert g.forbiddenCalls is not None and len(g.forbiddenCalls) == 0
    if mode in FOREVER_MODES:
        # Addon 0.17.0: Legacy names are scanned once per session; a frame that appears later is found only after the
        # client loads an addon named for Legacy (the load-on-demand Legacy window), not on every dump.
        runtime.execute('LegacySystemFrameMock = { GetObjectType = function() return "Frame" end, IsShown = function() return true end }')
        g.SlashCmdList["WOWMCP"]("")
        frames = lambda: [f["name"] for f in g.WowMcpDB["characters"]["Piekasz"]["legacy"]["discovery"]["frames"].values()]
        assert frames() == ["LegacyFrameMock"], frames()
        on_event(None, "ADDON_LOADED", "Blizzard_GuildUI")
        g.SlashCmdList["WOWMCP"]("")
        assert frames() == ["LegacyFrameMock"], frames()
        on_event(None, "ADDON_LOADED", "Blizzard_LegacySystem")
        g.SlashCmdList["WOWMCP"]("")
        assert frames() == ["LegacyFrameMock", "LegacySystemFrameMock"], frames()


UNLOADED_CLIENT = r"""
-- Client on a real exit from the game: the character is already unloaded (addon 0.5.0 dump from 2026-11-15).
function UnitHealthMax() return 0 end
function GetMoney() return 0 end
function GetInventoryItemLink() return nil end
C_SpellBook.GetNumSpellBookSkillLines = function() return 0 end
C_Container.GetContainerNumSlots = function() return 0 end
"""

LINEN_X2 = "You receive loot: |cffffffff|Hitem:2589:0|h[Linen Cloth]|h|rx2."
COPPER = "You receive loot: |cffffffff|Hitem:2770:0|h[Copper Ore]|h|r."


def bag_items(snapshot) -> dict:
    """Bag items by item ID."""
    return {i["id"]: i for bag in snapshot["bags"].values() for i in bag["items"].values()}


def check_auction_item_details(snapshot, g, mode: str) -> None:
    """Addon 0.13.0: a price entry carries item details, but only from the client cache and never a tooltip."""
    prices = snapshot["auctions"]["prices"]
    cached, uncached = prices["Hornwood Recurve Bow"], prices[expect(mode, "Ranger's Shortbow")]
    assert cached["id"] == 2506 and cached["equipLoc"] == "INVTYPE_RANGEDRIGHT" and cached["classID"] == 2 and cached["subclassID"] == 2, dict(cached)
    assert (cached["itemLevel"], cached["requiredLevel"], cached["quality"], cached["sellPrice"]) == (19, 14, 2, 1500), dict(cached)
    assert cached["dps"] == 9.7 and dict(cached["statTokens"]) == {"ITEM_MOD_DAMAGE_PER_SECOND_SHORT": 9.7, "ITEM_MOD_AGILITY_SHORT": 3}, dict(cached)
    if mode in FOREVER_MODES:
        check_auction_item_keys(prices, g)
    else:
        assert cached["tooltip"] is None, "the classic list has no item key to read a tooltip from"
    # Not cached: only the static fields of GetItemInfoInstant, the price stays, and no call that could query the server.
    assert uncached["id"] == 2507 and uncached["equipLoc"] == "INVTYPE_RANGEDRIGHT" and uncached["classID"] == 2, dict(uncached)
    for key in ("itemLevel", "requiredLevel", "stats", "statTokens", "dps", "sellPrice", "tooltip"):
        assert uncached[key] is None, (key, dict(uncached))
    assert uncached["minBuyout"] in (3100,), dict(uncached)
    # An item the client knows nothing about keeps the plain price.
    assert prices["Light Leather"]["classID"] is None and prices["Light Leather"]["equipLoc"] is None
    # Cached but not gear: no details at all (the section is bounded).
    thread = prices["Silk Thread"]
    assert thread["id"] == 2508 and thread["minBuyout"] is not None, dict(thread)
    for key in ("type", "classID", "itemLevel", "sellPrice", "statTokens", "equipLoc", "quality"):
        assert thread[key] is None, (key, dict(thread))
    assert cached["stats"] is None, "localized stat labels are not stored on a price entry"
    assert len(g.uncachedLookups) == 0, dict(g.uncachedLookups)


def check_auction_item_keys(prices, g) -> None:
    """Addon 0.19.2: a random suffix is its own price entry with its own stats, and a row whose data loads later is kept."""
    owl, monkey = prices["Thallium Choker of the Owl"], prices["Thallium Choker of the Monkey"]
    assert prices["Thallium Choker"] is None, "the base item has none of the suffix stats"
    assert owl["suffix"] == 585 and dict(owl["statTokens"]) == {"ITEM_MOD_INTELLECT_SHORT": 4, "ITEM_MOD_SPIRIT_SHORT": 4}, dict(owl)
    assert list(owl["tooltip"].values()) == ["Thallium Choker of the Owl", "Neck", "+4 Intellect"] and owl["minBuyout"] == 8001, dict(owl)
    # The listing the player opened: the price, stats and tooltip come from its own link.
    assert monkey["source"] == "item" and monkey["minBuyout"] == 9400 and monkey["suffix"] == 600, dict(monkey)
    assert dict(monkey["statTokens"]) == {"ITEM_MOD_AGILITY_SHORT": 4, "ITEM_MOD_STAMINA_SHORT": 4}, dict(monkey)
    trinket = prices["Tidal Charm"]
    assert trinket["equipLoc"] == "INVTYPE_TRINKET" and trinket["minBuyout"] == 52000 and trinket["requiredLevel"] == 40, dict(trinket)
    assert list(trinket["tooltip"].values())[2].startswith("Use:"), dict(trinket["tooltip"])
    assert list(prices["Hornwood Recurve Bow"]["tooltip"].values()) == ["Hornwood Recurve Bow"]
    requested = set(g.itemDataRequests.values())
    assert requested == {2507, 1404}, requested


def check_identifiers(snapshot, mode: str) -> None:
    """Addon 0.9.0: locale, item class IDs, binding, stat tokens, profession skill IDs (and deDE parsing)."""
    character = snapshot["character"]
    assert character["locale"] == ("deDE" if mode == "dede" else "enUS"), character["locale"]
    assert character["classToken"] == "HUNTER" and character["raceToken"] == "NightElf"
    items = bag_items(snapshot)
    if mode == "legacy":
        assert items[2589]["isBound"] is False and items[2589]["isQuestItem"] is None, dict(items[2589])
        professions = {p["name"]: p for p in snapshot["professions"].values()}
        assert professions["Skinning"]["skillID"] is None, "the old Skills window without C_SkillInfo has no skillID"
        return
    assert items[2589]["isBound"] is False and items[2589]["isQuestItem"] is None and items[2589]["questID"] is None
    professions = {p["skillID"]: p for p in snapshot["professions"].values()}
    assert {393, 186, 185} <= set(professions), list(professions)
    if mode == "modern":
        assert items[2589]["classID"] is None, "the modern mock has no item data"
        return
    # forever and dede: the same numbers whatever the language of the text.
    assert items[6948]["isBound"] is True
    knife, cloth = items[7005], items[2589]
    assert (knife["classID"], knife["subclassID"], knife["bindType"]) == (2, 15, 0), dict(knife)
    assert (cloth["classID"], cloth["subclassID"], cloth["bindType"]) == (7, 5, 0), dict(cloth)
    assert dict(knife["statTokens"]) == {"ITEM_MOD_DAMAGE_PER_SECOND_SHORT": 2.2}, dict(knife["statTokens"])
    dagger = {i["id"]: i for i in snapshot["equipment"].values()}[5586]
    assert dict(dagger["statTokens"]) == {"ITEM_MOD_AGILITY_SHORT": 3, "ITEM_MOD_STAMINA_SHORT": 2, "ITEM_MOD_DAMAGE_PER_SECOND_SHORT": 8.4}
    assert professions[173]["group"] == "weapon"
    if mode == "forever":
        assert cloth["type"] == "Trade Goods" and knife["name"] == "Skinning Knife" and dagger["stats"]["Agility"] == 3
        return
    # dede: German text in the dump, the same IDs; German chat parsed, English chat ignored.
    assert character["class"] == "Jäger" and cloth["type"] == "Handwerkswaren" and knife["name"] == "Kürschnermesser"
    assert dagger["stats"]["Beweglichkeit"] == 3 and professions[393]["name"] == "Kürschnerei"
    loot = snapshot["loot"]
    names = {i["name"]: i["count"] for i in loot["items"].values()}
    assert names == {"Linen Cloth": 3, "Scharfer Pfeil": 1}, names
    assert (loot["messagesSeen"], loot["messagesParsed"]) == (4, 2), dict(loot)  # every loot message is seen
    xp = snapshot["xpGains"]
    assert xp["kills"]["xp"] == 180 and xp["other"]["xp"] == 250 and xp["total"] == 430, dict(xp)
    assert (xp["messagesSeen"], xp["messagesParsed"]) == (4, 3), dict(xp)


def check_numeric_ids(snapshot, mode: str, runtime, addon_ns) -> None:
    """Addon 0.10.0: weapon numbers without English tooltip text, zone/node/instance IDs, icons for buff matching."""
    if mode in ("modern", "legacy"):
        quests = {q["title"]: q for q in snapshot["quests"].values()}
        assert quests["Cave Mushrooms"]["zoneMapID"] is None, "without a map API there is no zone ID"
        return
    # Weapons: speed, damage range and DPS as numbers, also from the German tooltip ("Tempo 1,60").
    dagger = {i["id"]: i for i in snapshot["equipment"].values()}[5586]
    assert (dagger["speed"], dagger["dps"], dagger["damageMin"], dagger["damageMax"]) == (1.6, 8.4, 9, 18), dict(dagger)
    knife = bag_items(snapshot)[7005]
    assert (knife["speed"], knife["dps"], knife["damageMin"], knife["damageMax"]) == (1.6, 2.2, 2, 5), dict(knife)
    assert bag_items(snapshot)[2589]["speed"] is None, "only weapons have a speed"
    # Without a stat token DPS comes from the DPS line, and without that from damage and speed; the speed may be on its own line.
    german = mode == "dede"
    damage = "12 - 20 Schaden" if german else "12 - 20 Damage"
    speed_line = "Tempo 2,40" if german else "Speed 2.40"
    dps_line = "(9,5 Schaden pro Sekunde)" if german else "(9.5 damage per second)"
    staff = runtime.table()
    addon_ns.Items.attachTooltip(staff, runtime.eval('{ "%s", "%s" }' % (damage, speed_line)))
    assert (staff["speed"], staff["dps"], staff["damageMin"], staff["damageMax"]) == (2.4, 6.7, 12, 20), dict(staff)
    staff = runtime.table()
    addon_ns.Items.attachTooltip(staff, runtime.eval('{ "%s | %s", "%s" }' % (damage, speed_line, dps_line)))
    assert (staff["speed"], staff["dps"]) == (2.4, 9.5), dict(staff)
    armor = runtime.table()
    addon_ns.Items.attachTooltip(armor, runtime.eval('{ "Cloth", "5 Armor" }'))
    assert armor["speed"] is None and armor["dps"] is None and armor["tooltip"][1] == "Cloth"
    # Zone IDs: the quest log header and the flight points get the zone's UiMapID; node IDs come from the client.
    quests = {q["title"]: q for q in snapshot["quests"].values()}
    assert quests["Cave Mushrooms"]["zoneMapID"] == 1439 and quests["Cave Mushrooms"]["zone"] == "Darkshore"
    nodes = {n["name"]: n for n in snapshot["flightPaths"]["nodes"].values()}
    assert (nodes["Auberdine, Darkshore"]["nodeID"], nodes["Auberdine, Darkshore"]["zoneMapID"]) == (26, 1439)
    assert (nodes["Rut'theran Village, Teldrassil"]["nodeID"], nodes["Rut'theran Village, Teldrassil"]["zoneMapID"]) == (27, 1438)
    assert nodes["Astranaar, Ashenvale"]["nodeID"] == 28 and nodes["Astranaar, Ashenvale"]["zoneMapID"] is None
    # Dungeons: activity category and group, dungeon map ID, lockout instance ID and difficulty ID.
    dungeons = snapshot["dungeons"]
    assert (dungeons["activities"][1]["categoryID"], dungeons["activities"][1]["groupID"]) == (2, 103), dict(dungeons["activities"][1])
    deadmines = dungeons["lfg"][1]
    assert (deadmines["mapID"], deadmines["typeID"], deadmines["groupID"]) == (36, 1, 12), dict(deadmines)
    assert (dungeons["lockouts"][1]["instanceID"], dungeons["lockouts"][1]["difficultyID"]) == (48, 1), dict(dungeons["lockouts"][1])
    # Spell and aura icons (buff matching without names), self buff flag.
    spells = {sp["id"]: sp for sp in snapshot["spells"].values()}
    assert spells[13165]["icon"] == 136076 and spells[13165]["selfBuff"] is True, dict(spells[13165])
    assert spells[19434]["icon"] == 135130 and spells[19434]["selfBuff"] is None
    assert spells[6603]["icon"] is None, "no icon in the mock data"
    auras = {a["id"]: a for a in snapshot["auras"].values()}
    assert auras[13165]["icon"] == 136076 and auras[8326]["icon"] == 136135


def saved_character(g):
    return g.WowMcpDB["characters"]["Piekasz"]


# Options API variants of the client (addon 0.11.0): modern Settings, old InterfaceOptions, a broken one, none.
OPTIONS_API = {
    "modern": r"""
        optionsCalls = {}
        Settings = {
            RegisterCanvasLayoutCategory = function(frame, name)
                local category = { frame = frame, name = name, GetID = function() return 77 end }
                optionsCalls.category = category
                return category
            end,
            RegisterAddOnCategory = function(category) optionsCalls.registered = category end,
            OpenToCategory = function(id) optionsCalls.openedId = id end,
        }
    """,
    "legacy": r"""
        optionsCalls = { opened = 0 }
        function InterfaceOptions_AddCategory(frame) optionsCalls.added = frame end
        function InterfaceOptionsFrame_OpenToCategory(frame) optionsCalls.opened = optionsCalls.opened + 1 end
    """,
    "broken": r"""
        optionsCalls = {}
        Settings = {
            RegisterCanvasLayoutCategory = function() error("API changed") end,
            RegisterAddOnCategory = function() error("API changed") end,
        }
        function InterfaceOptions_AddCategory() error("API changed") end
    """,
    "none": "",
}


def relog(saved_text: str, options_api: str = "", clock: int | None = None):
    """A new UI session (after /reload or re-entering): addon files, then SavedVariables, then PLAYER_LOGIN.
    clock: the game clock to continue from (by default every session starts at the fixed dump moment)."""
    runtime, g, addon_ns = load_addon("forever")
    if clock is not None:
        g.clockNow = clock
    runtime.execute("function UnitXP() return playerXp end; playerXp = 1200")
    runtime.execute(OPTIONS_API.get(options_api, ""))
    runtime.execute(saved_text)
    on_event = event_dispatcher(g)
    on_event(None, "PLAYER_LOGIN")
    return runtime, g, addon_ns, on_event


REAL_PROBE = Path(__file__).resolve().parents[2] / "server" / "testdata" / "real" / "probe_2026-11-15_addon070.json"


def check_rejected_events_match_probe() -> None:
    """The stub rejects exactly the events the real client did not accept in /mcp probe (2026-11-15)."""
    import json
    events = json.loads(REAL_PROBE.read_text(encoding="utf-8"))["probe"]["events"]
    rejected = {name for name, accepted in events.items() if accepted is False}
    assert rejected == set(FOREVER_REJECTED_EVENTS), (sorted(rejected), sorted(FOREVER_REJECTED_EVENTS))


def check_sanitized_saves(runtime, g, addon_ns, on_event) -> None:
    # Review 2026-11-16: whatever a section puts into the saved dump, the game only ever gets plain values.
    # Secret values and keys, functions and too deep tables are removed at logout, and a list stays a list.
    saved = saved_character(g)
    runtime.execute('secretValues["SECRET"] = true')
    runtime.eval('''function(saved, depth)
        -- note is carried from dump to dump as it is, so a value put there reaches the logout save unchanged
        saved.note = { text = "x", cbTest = { list = { "a", "SECRET", print, "b" }, map = { ok = 1, bad = "SECRET", fn = print }, SECRET = 1 } }
        local deep = saved.note.cbTest
        for _ = 1, depth do deep.next = {}; deep = deep.next end
        deep.leaf = 1
    end''')(saved, addon_ns.Compat.MAX_SAVED_DEPTH + 5)
    on_event(None, "PLAYER_LOGOUT")
    test = saved_character(g)["note"]["cbTest"]
    assert list(test["list"].values()) == ["a", "b"] and sorted(test["list"].keys()) == [1, 2], dict(test["list"])
    assert dict(test["map"]) == {"ok": 1}, dict(test["map"])
    assert test["SECRET"] is None
    depth, node = 1, test
    while node["next"] is not None:
        node, depth = node["next"], depth + 1
    assert depth <= addon_ns.Compat.MAX_SAVED_DEPTH, depth
    text = g.SerializeSavedVariable("WowMcpDB", g.WowMcpDB)
    assert "SECRET" not in text and "function" not in text
    saved_character(g)["note"] = None

    # firstValue keeps a boolean false (an offline group member must be online = false, not missing).
    first_value = runtime.eval('function(ns) return ns.Compat.firstValue({ "CbTestFalse" }) end')
    assert first_value(addon_ns) is None
    runtime.execute("function CbTestFalse() return false end")
    assert first_value(addon_ns) is False
    # The note is cut on a UTF-8 character boundary (Note.clip), never inside a character.
    clip = runtime.eval("function(ns, text, max) return ns.Note.clip(text, max) end")
    assert clip(addon_ns, "abcé", 4) == "abc" and clip(addon_ns, "abcé", 5) == "abcé" and clip(addon_ns, "ab", 5) == "ab"
    # Loot entries from older dumps without lastAt do not break the 100-item limit.
    loot = saved_character(g)["loot"]
    runtime.eval('''function(loot, ns)
        loot.items = {}
        for i = 1, ns.Loot.MAX_ITEMS do loot.items[i] = { id = 100000 + i, name = "Old " .. i, count = 1 } end
    end''')(loot, addon_ns)
    on_event(None, "CHAT_MSG_LOOT", COPPER)
    items = saved_character(g)["loot"]["items"]
    assert len(items) == addon_ns.Loot.MAX_ITEMS and any(i["name"] == "Copper Ore" for i in items.values()), len(items)
    saved_character(g)["loot"]["items"] = runtime.eval("{}")


def last_chat(g) -> str:
    return list(g.chatlog.values())[-1]


def check_options_page() -> None:
    """Addon 0.11.0: the options page works on the modern and the old API, and a missing or changed API never breaks anything."""
    # Modern API: the page is registered at login, drawn on first show, and uses the same settings as /mcp.
    runtime, g, addon_ns, on_event = relog("", "modern")
    assert g.optionsCalls["registered"] is not None and g.optionsCalls["category"]["name"] == "WowMcp"
    frame = g.optionsCalls["category"]["frame"]
    assert frame["name"] == "WowMcp"
    boxes = [f for f in g.frames.values() if f.kind == "CheckButton"]
    assert not boxes, "the page is drawn lazily, on first show"
    g.SlashCmdList["WOWMCP"]("options")
    assert g.optionsCalls["openedId"] == 77
    frame.scripts["OnShow"](frame)
    boxes = [f for f in g.frames.values() if f.kind == "CheckButton"]
    general = len(addon_ns.Options.generalSwitches())
    section_switches = addon_ns.Options.sectionSwitches()
    sections = [section_switches[i]["label"] for i in range(1, len(section_switches) + 1)]
    assert general == 4 and len(boxes) == general + len(sections) and "macros" in sections and "character" not in sections
    assert [i for i, b in enumerate(boxes) if b.checked is not True] == [1], "all on by default except the compact panel"
    minimap, compact, ahtip, played = boxes[0], boxes[1], boxes[2], boxes[3]
    settings = g.WowMcpDB["settings"]
    for box, name, off_value in ((minimap, "minimap", False), (compact, "compact", None),
                                 (ahtip, "ahTooltip", False), (played, "played", False)):
        box.checked = False
        box.scripts["OnClick"](box)
        assert settings[name] == off_value, (name, settings[name])
    assert settings["compact"] is None, "compact stays off (the box was unchecked, the default)"
    g.SlashCmdList["WOWMCP"]("compact off")
    g.SlashCmdList["WOWMCP"]("minimap on")
    frame.scripts["OnShow"](frame)  # the page follows what /mcp changed meanwhile
    assert minimap.checked is True and compact.checked is False and played.checked is False
    macros = boxes[general + sections.index("macros")]
    macros.checked = False
    macros.scripts["OnClick"](macros)
    assert addon_ns.Snapshot.isDisabled("macros") is True
    assert list(settings["disabledSections"].keys()) == ["macros"]
    g.SlashCmdList["WOWMCP"]("on macros")
    frame.scripts["OnShow"](frame)
    assert macros.checked is True and addon_ns.Snapshot.isDisabled("macros") is False
    assert len(g.forbiddenCalls) == 0 and not g.reloaded, "the page does not save or call the server"

    # Old API.
    runtime, g, addon_ns, on_event = relog("", "legacy")
    frame = g.optionsCalls["added"]
    assert frame["name"] == "WowMcp"
    g.SlashCmdList["WOWMCP"]("options")
    assert g.optionsCalls["opened"] >= 1

    # An API that raises, and no API at all: the addon loads, /mcp works, /mcp options says so.
    for variant in ("broken", "none"):
        runtime, g, addon_ns, on_event = relog("", variant)
        assert addon_ns.Options.register() is False
        g.SlashCmdList["WOWMCP"]("options")
        assert "no options page" in last_chat(g), last_chat(g)
        g.SlashCmdList["WOWMCP"]("")
        assert saved_character(g)["dumpedAt"] is not None
        assert not list(saved_character(g)["errors"].values())


def check_legacy_scan_only_when_needed() -> None:
    """Addon 0.19.1: the scan of _G for Legacy names runs only when no perk tree was found or after /mcp probe."""
    count_scans = "ScanCount = 0; local realPairs = pairs; function pairs(t) if t == _G then ScanCount = ScanCount + 1 end return realPairs(t) end"
    runtime, g, _ = load_addon("forever", count_scans)
    on_event = event_dispatcher(g)
    legacy = lambda: g.WowMcpDB["characters"]["Piekasz"]["legacy"]
    g.SlashCmdList["WOWMCP"]("")
    on_event(None, "PLAYER_LOGOUT")
    assert legacy()["discovery"] is None and len(legacy()["configs"]) == 1 and g.ScanCount == 0, g.ScanCount
    g.SlashCmdList["WOWMCP"]("probe")
    assert legacy()["discovery"]["frames"][1]["name"] == "LegacyFrameMock" and g.ScanCount == 2, g.ScanCount  # the probe section walks _G too
    on_event(None, "PLAYER_LOGOUT")
    assert legacy()["discovery"] is not None and g.ScanCount == 2, g.ScanCount

    runtime, g, _ = load_addon("forever", count_scans)
    runtime.execute("C_Traits.GetConfigsByType = function() return {} end")
    g.SlashCmdList["WOWMCP"]("")
    assert g.ScanCount == 1 and legacy()["discovery"] is not None, g.ScanCount
    assert [c["typeName"] for c in legacy()["configs"].values()] == ["frame"], dict(legacy())


def check_guard_ignores_disabled_sections() -> None:
    """Review follow-up: /mcp off on the sections the logout guard looks at does not switch the guard off."""
    for disabled in (("stats", "spells", "equipment", "bags"), ("spells", "equipment", "bags")):
        runtime, g, addon_ns, on_event = relog("")
        g.SlashCmdList["WOWMCP"]("")
        good = saved_character(g)["dumpedAt"]
        for name in disabled:
            g.SlashCmdList["WOWMCP"]("off " + name)
        g.clockNow += 300
        # The client is still loaded: a normal logout dump is written (the guard does not fire).
        on_event(None, "PLAYER_LOGOUT")
        normal = saved_character(g)
        assert normal["reason"] == "logout" and normal["lastSkippedLogoutAt"] is None and normal["dumpedAt"] > good
        for name in disabled:
            assert normal[name] is None, name
        # The client has unloaded the character: the previous dump is kept although the sign sections are off.
        g.clockNow += 300
        kept_from = normal["dumpedAt"]
        runtime.execute(UNLOADED_CLIENT)
        on_event(None, "PLAYER_LOGOUT")
        kept = saved_character(g)
        assert kept["dumpedAt"] == kept_from and kept["lastSkippedLogoutAt"] == g.clockNow, dict(kept)
        assert "clientUnloaded" not in kept or kept["clientUnloaded"] is None
        assert kept["character"]["moneyCopper"] == 5212

    # A plain logout: the dump taken when the logout timer starts is the one that stays on disk.
    for start in ("PLAYER_CAMPING", "PLAYER_QUITING"):
        runtime, g, addon_ns, on_event = relog("")
        g.SlashCmdList["WOWMCP"]("")
        g.clockNow += 300
        g.reloaded = False
        on_event(None, start)
        fresh = saved_character(g)
        assert fresh["reason"] == "logoutStart" and fresh["dumpedAt"] == g.clockNow and not g.reloaded, dict(fresh)
        g.clockNow += 20
        runtime.execute(UNLOADED_CLIENT)
        on_event(None, "PLAYER_LOGOUT")
        kept = saved_character(g)
        assert kept["reason"] == "logoutStart" and kept["dumpedAt"] == g.clockNow - 20, dict(kept)
        assert kept["lastSkippedLogoutAt"] == g.clockNow


def periods_of(section) -> list:
    """The carried closed periods of a loot or xpGains section as a Python list (oldest first)."""
    periods = section["periods"]
    return [] if periods is None else [periods[i] for i in range(1, len(periods) + 1)]


def check_carried_periods() -> None:
    """Addon 0.12.0: the addon never assumes the server read a dump. Real sequence (2026-11-16): /mcp reload with a
    whole session of loot and XP, logout 10 s later; the second save must not lose the first period."""
    runtime, g, addon_ns, on_event = relog("")
    session_start = saved_character(g)["loot"]["since"]
    on_event(None, "CHAT_MSG_COMBAT_XP_GAIN", "Defias Looter dies, you gain 152 experience.")
    on_event(None, "CHAT_MSG_LOOT", LINEN_X2)
    g.clockNow += 3600
    g.SlashCmdList["WOWMCP"]("reload")
    on_event(None, "PLAYER_LOGOUT")
    first_save = saved_character(g)["dumpedAt"]
    assert periods_of(saved_character(g)["loot"]) == [], "the open period is not carried yet"

    # (a) Save, save, then the server reads: the first period travels in the second dump.
    runtime, g, addon_ns, on_event = relog(g.SerializeSavedVariable("WowMcpDB", g.WowMcpDB), clock=g.clockNow)
    g.clockNow += 10
    on_event(None, "PLAYER_LOGOUT")
    second = saved_character(g)
    assert second["loot"]["since"] == first_save and len(second["loot"]["items"]) == 0, dict(second["loot"])
    [loot_period] = periods_of(second["loot"])
    assert (loot_period["since"], loot_period["until"]) == (session_start, first_save), dict(loot_period)
    assert [i["name"] for i in loot_period["items"].values()] == ["Linen Cloth"] and loot_period["periods"] is None
    [xp_period] = periods_of(second["xpGains"])
    assert xp_period["kills"]["xp"] == 152 and xp_period["until"] == first_save, dict(xp_period)
    second_save = second["dumpedAt"]

    # (d) An empty period (the 10 s between the two saves) is not stored.
    runtime, g, addon_ns, on_event = relog(g.SerializeSavedVariable("WowMcpDB", g.WowMcpDB), clock=g.clockNow)
    current = saved_character(g)
    assert current["loot"]["since"] == second_save and len(periods_of(current["loot"])) == 1
    assert len(periods_of(current["xpGains"])) == 1

    # (c) A garbage logout dump in between: the period continues and the carried history survives it.
    g.clockNow += 600
    on_event(None, "CHAT_MSG_LOOT", COPPER)
    runtime.execute(UNLOADED_CLIENT)
    g.clockNow += 5
    on_event(None, "PLAYER_LOGOUT")
    kept = saved_character(g)
    assert kept["dumpedAt"] == second_save and kept["lastSkippedLogoutAt"] == g.clockNow
    assert len(periods_of(kept["loot"])) == 1 and [i["name"] for i in kept["loot"]["items"].values()] == ["Copper Ore"]
    runtime, g, addon_ns, on_event = relog(g.SerializeSavedVariable("WowMcpDB", g.WowMcpDB), clock=g.clockNow)
    current = saved_character(g)
    assert current["loot"]["since"] == second_save and len(periods_of(current["loot"])) == 1, dict(current["loot"])
    g.clockNow += 60
    g.SlashCmdList["WOWMCP"]("reload")
    on_event(None, "PLAYER_LOGOUT")
    third_save = saved_character(g)["dumpedAt"]
    runtime, g, addon_ns, on_event = relog(g.SerializeSavedVariable("WowMcpDB", g.WowMcpDB), clock=g.clockNow)
    loot_periods = periods_of(saved_character(g)["loot"])
    assert [(p["since"], p["until"]) for p in loot_periods] == [(session_start, first_save), (second_save, third_save)]
    assert [i["name"] for i in loot_periods[1]["items"].values()] == ["Copper Ore"]
    assert len(periods_of(saved_character(g)["xpGains"])) == 1, "no XP in the continued period"

    # (e) Everything carried passes the sanitizer unchanged and stays plain data.
    before = g.SerializeSavedVariable("WowMcpDB", g.WowMcpDB)
    addon_ns.Snapshot.sanitizeCurrent()
    assert g.SerializeSavedVariable("WowMcpDB", g.WowMcpDB) == before, "the sanitizer removed carried data"

    # (b) Bounds: at most MAX_PERIODS of the newest, none older than MAX_PERIOD_AGE.
    max_periods, max_age = addon_ns.Periods.MAX_PERIODS, addon_ns.Periods.MAX_PERIOD_AGE
    assert (max_periods, max_age) == (20, 7 * 24 * 3600)
    now = g.clockNow
    fill = runtime.eval('''function(section, now, count, maxAge)
        section.periods = {}
        for i = 1, count do
            local ended = now - (count - i) * 3600
            if i <= 3 then ended = now - maxAge - 100 * i end -- three periods older than a week
            section.periods[i] = { since = ended - 60, ["until"] = ended, items = { { id = i, name = "Item " .. i, count = 1 } } }
        end
        section.periods[count + 1] = "garbage"
    end''')
    fill(saved_character(g)["loot"], now, 30, max_age)
    g.SlashCmdList["WOWMCP"]("")
    runtime, g, addon_ns, on_event = relog(g.SerializeSavedVariable("WowMcpDB", g.WowMcpDB), clock=g.clockNow)
    bounded = periods_of(saved_character(g)["loot"])
    assert len(bounded) == max_periods, len(bounded)
    assert all(g.clockNow - p["until"] <= max_age for p in bounded)
    assert bounded[-1]["items"][1]["name"] == "Item 30", "the newest periods are kept"
    # Age alone: after more than a week without play everything carried is dropped at the next login.
    g.clockNow += max_age + 1
    g.SlashCmdList["WOWMCP"]("reload")
    on_event(None, "PLAYER_LOGOUT")
    runtime, g, addon_ns, on_event = relog(g.SerializeSavedVariable("WowMcpDB", g.WowMcpDB), clock=g.clockNow)
    assert periods_of(saved_character(g)["loot"]) == [], periods_of(saved_character(g)["loot"])
    # A disabled section keeps nothing.
    g.SlashCmdList["WOWMCP"]("off loot")
    assert saved_character(g)["loot"] is None
    assert len(g.forbiddenCalls) == 0, list(g.forbiddenCalls.values())


def event_log_entries(g) -> list:
    log = saved_character(g)["eventLog"]
    entries = None if log is None else log["entries"]
    return [] if entries is None else [dict(entries[i]) for i in range(1, len(entries) + 1)]


def check_event_log() -> None:
    """Addon eventLog section: kinds, throttling, de-duplication, bounds, and carrying across saves."""
    runtime, g, addon_ns, on_event = relog("")
    runtime.execute("""
        C_QuestLog.GetTitleForQuestID = function(id) if id == 947 then return "The Glowing Shard" end end
        local copper, zone = 100, "Darkshore"
        function GetMoney() return copper end
        function GetRealZoneText() return zone end
        function setMoney(value) copper = value end
        function setZone(value) zone = value end
    """)
    start = g.clockNow
    on_event(None, "PLAYER_LEVEL_UP", 21)
    on_event(None, "PLAYER_MONEY")
    g.clockNow += 100
    runtime.eval("setMoney")(250)
    on_event(None, "PLAYER_MONEY")
    g.clockNow += 300
    runtime.eval("setMoney")(900)
    on_event(None, "PLAYER_MONEY")
    on_event(None, "QUEST_ACCEPTED", 3, 947)
    on_event(None, "QUEST_ACCEPTED", 4740)
    on_event(None, "QUEST_TURNED_IN", 947, 250, 350)
    on_event(None, "ZONE_CHANGED_NEW_AREA")
    on_event(None, "ZONE_CHANGED_NEW_AREA")
    runtime.eval("setZone")("Ashenvale")
    on_event(None, "ZONE_CHANGED_NEW_AREA")
    on_event(None, "PLAYER_EQUIPMENT_CHANGED", 16, False)
    on_event(None, "PLAYER_EQUIPMENT_CHANGED", 5, True)
    kinds = [(e["kind"]) for e in event_log_entries(g)]
    assert kinds == ["level", "money", "money", "questAccepted", "questAccepted", "questTurnedIn", "zone", "zone",
                     "equipment", "equipment"], kinds
    entries = event_log_entries(g)
    assert entries[0] == {"at": start, "kind": "level", "level": 21}, entries[0]
    assert entries[1]["copper"] == 250 and entries[1]["at"] == start, "a money change within 300 s overwrites"
    assert entries[2]["copper"] == 900 and entries[2]["at"] == start + 400
    assert (entries[3]["questID"], entries[3]["title"]) == (947, "The Glowing Shard"), entries[3]
    assert entries[4]["questID"] == 4740 and "title" not in entries[4], entries[4]
    assert (entries[5]["questID"], entries[5]["xp"], entries[5]["copper"]) == (947, 250, 350), entries[5]
    assert [e["zone"] for e in entries[6:8]] == ["Darkshore", "Ashenvale"]
    assert (entries[8]["slot"], entries[8]["itemID"]) == (16, 5586) and "itemID" not in entries[9], entries[8:]

    for level in range(22, 330):
        on_event(None, "PLAYER_LEVEL_UP", level)
    capped = event_log_entries(g)
    assert len(capped) == 300 and capped[-1]["level"] == 329 and capped[0]["kind"] == "level", capped[0]

    g.SlashCmdList["WOWMCP"]("reload")
    saved = g.SerializeSavedVariable("WowMcpDB", g.WowMcpDB)
    runtime, g, addon_ns, on_event = relog(saved, clock=g.clockNow)
    assert len(event_log_entries(g)) == 300, "the log survives a reload and login"
    runtime, g, addon_ns, on_event = relog(saved, clock=g.clockNow + 15 * 24 * 3600)
    assert event_log_entries(g) == [], "entries older than 14 days are dropped at login"

    g.SlashCmdList["WOWMCP"]("off eventLog")
    on_event(None, "PLAYER_LEVEL_UP", 5)
    assert saved_character(g)["eventLog"] is None, "a disabled section stores nothing"


def check_continent_is_no_zone() -> None:
    """Addon 0.12.0: a node position that only resolves to a continent (addon 0.7.0 wrote "Eastern Kingdoms" as every
    point's zone) is stored as continent, never as zone."""
    runtime, g, addon_ns, on_event = relog("")
    runtime.execute('''C_Map.GetMapInfoAtPosition = function(mapID, x, y)
        if x < 0.3 then return { mapID = 1439, name = "Darkshore", mapType = 3 } end
        return { mapID = 1414, name = "Kalimdor", mapType = 2 }
    end''')
    on_event(None, "TAXIMAP_OPENED")
    nodes = {n["name"]: n for n in saved_character(g)["flightPaths"]["nodes"].values()}
    teldrassil = nodes["Rut'theran Village, Teldrassil"]
    assert teldrassil["zone"] is None and teldrassil["continent"] == "Kalimdor", dict(teldrassil)
    assert nodes["Auberdine, Darkshore"]["zone"] == "Darkshore" and nodes["Auberdine, Darkshore"]["continent"] is None


def check_real_lifecycle() -> None:
    """The real order from the game (2026-11-15): windows and combat, /mcp reload (manual dump, ReloadUI, dump
    from PLAYER_LOGOUT), another PLAYER_LOGIN, and then exit from the game once the client has unloaded the character."""
    check_rejected_events_match_probe()
    runtime, g, addon_ns, on_event = relog("")
    current = saved_character(g)
    # The loot and XP period starts right away (empty sections with counters), so the lack of data is visible.
    assert current["loot"]["messagesSeen"] == 0 and len(current["loot"]["items"]) == 0, dict(current["loot"])
    assert current["xpGains"]["total"] == 0 and current["xpGains"]["messagesSeen"] == 0, dict(current["xpGains"])
    login_at = g.clockNow

    # A vendor whose name the client does not reveal ("secret"): saved without an error, and the buyback list too.
    g.npcName = "Quartermaster Lewis"
    on_event(None, "MERCHANT_SHOW")
    runtime.execute('secretValues["Hidden Merchant"] = true')
    g.clockNow += 30
    g.npcName = "Hidden Merchant"
    on_event(None, "MERCHANT_SHOW")
    current = saved_character(g)
    # The secret name itself never reaches the dump (plain), only the offer does.
    assert current["vendor"]["npc"] is None and current["buyback"] is not None, dict(current["vendor"])
    assert set(current["vendors"].keys()) == {"Quartermaster Lewis"}, list(current["vendors"].keys())
    g.npcName = "Einris Brightspear"

    # Combat: a kill (message and XP bar), loot, a message hidden by the client.
    on_event(None, "CHAT_MSG_COMBAT_XP_GAIN", "Defias Looter dies, you gain 152 experience.")
    g.playerXp = 1352
    on_event(None, "PLAYER_XP_UPDATE", "player")
    on_event(None, "CHAT_MSG_LOOT", LINEN_X2)
    runtime.execute('secretValues["You gain 40 experience."] = true')
    on_event(None, "CHAT_MSG_COMBAT_XP_GAIN", "You gain 40 experience.")
    xp = saved_character(g)["xpGains"]
    assert (xp["messagesSeen"], xp["messagesParsed"], xp["messagesSecret"]) == (2, 1, 1), dict(xp)
    assert xp["kills"]["xp"] == 152 and xp["barXp"] == 152, dict(xp)

    # An error in one handler does not stop the next ones and goes to the dump's errors.
    reached = runtime.eval("{}")
    addon_ns.Events.on("CB_TEST_EVENT", runtime.eval('function() error("boom") end'))
    addon_ns.Events.on("CB_TEST_EVENT", runtime.eval("function(t) return function() t.ok = true end end")(reached))
    on_event(None, "CB_TEST_EVENT")
    assert reached["ok"] is True

    # /mcp reload: manual dump, ReloadUI, then PLAYER_LOGOUT of the same reload (data is still in the client).
    g.clockNow += 60
    g.SlashCmdList["WOWMCP"]("reload")
    assert g.reloaded
    on_event(None, "CHAT_MSG_LOOT", COPPER)  # between the dump and logout it counts too
    g.clockNow += 2
    on_event(None, "PLAYER_LOGOUT")
    reloaded = saved_character(g)
    assert reloaded["reason"] == "logout" and reloaded["lastSkippedLogoutAt"] is None
    # A guild without an open window: only the fact of membership (name and rank from client memory, no list).
    guild = reloaded["guild"]
    assert guild["inGuild"] is True and guild["name"] == "Night Watch" and guild["members"] is None, dict(guild)
    # The friends window was not open: there is no friends section (different from an empty list).
    assert reloaded["friends"] is None
    assert set(reloaded["vendors"].keys()) == {"Quartermaster Lewis"} and reloaded["buyback"] is not None
    assert reloaded["loot"]["since"] == login_at and reloaded["xpGains"]["kills"]["count"] == 1
    assert {i["name"] for i in reloaded["loot"]["items"].values()} == {"Linen Cloth", "Copper Ore"}
    assert any("CB_TEST_EVENT" in e and "boom" in e for e in reloaded["errors"].values()), list(reloaded["errors"].values())
    on_disk = reloaded["dumpedAt"]
    check_sanitized_saves(runtime, g, addon_ns, on_event)
    text = g.SerializeSavedVariable("WowMcpDB", g.WowMcpDB)

    # After the reload: a new period from the dump on disk; windows and NPC history stay.
    runtime, g, addon_ns, on_event = relog(text)
    current = saved_character(g)
    assert current["loot"]["since"] == on_disk and len(current["loot"]["items"]) == 0, dict(current["loot"])
    assert current["xpGains"]["since"] == on_disk and current["xpGains"]["total"] == 0
    assert set(current["vendors"].keys()) == {"Quartermaster Lewis"}

    # The game goes on, then a real exit: the client no longer has the character, so the PLAYER_LOGOUT dump does not overwrite the good one.
    g.clockNow += 600
    on_event(None, "CHAT_MSG_LOOT", COPPER)
    runtime.execute(UNLOADED_CLIENT)
    g.clockNow += 300
    on_event(None, "PLAYER_LOGOUT")
    kept = saved_character(g)
    assert kept["dumpedAt"] == on_disk and kept["lastSkippedLogoutAt"] == g.clockNow, dict(kept)
    assert kept["character"]["moneyCopper"] == 5212 and len(kept["bags"]) > 0 and kept["stats"]["healthMax"] > 0
    assert [i["name"] for i in kept["loot"]["items"].values()] == ["Copper Ore"]
    text = g.SerializeSavedVariable("WowMcpDB", g.WowMcpDB)

    # Next login: the period continues (the dump on disk is old), and /mcp reload ends it normally.
    runtime, g, addon_ns, on_event = relog(text)
    current = saved_character(g)
    assert current["loot"]["since"] == on_disk and [i["name"] for i in current["loot"]["items"].values()] == ["Copper Ore"]
    g.clockNow += 3600
    g.SlashCmdList["WOWMCP"]("reload")
    on_event(None, "PLAYER_LOGOUT")
    fresh = saved_character(g)
    assert fresh["dumpedAt"] == g.clockNow and fresh["lastSkippedLogoutAt"] is None and fresh["loot"]["since"] == on_disk

    # A character without a guild: every dump saves inGuild = false; an empty friends list is total = 0.
    runtime.execute('function IsInGuild() return false end; C_FriendList.GetNumFriends = function() return 0 end')
    on_event(None, "FRIENDLIST_UPDATE")
    g.SlashCmdList["WOWMCP"]("")
    lonely = saved_character(g)
    assert lonely["guild"]["inGuild"] is False and lonely["guild"]["name"] is None, dict(lonely["guild"])
    assert lonely["friends"]["total"] == 0 and len(lonely["friends"]["friends"]) == 0, dict(lonely["friends"])
    assert len(g.forbiddenCalls) == 0, list(g.forbiddenCalls.values())

    # First login with no dump at all and an immediate exit with an empty client: saved with a warning.
    runtime, g, addon_ns, on_event = relog("")
    runtime.execute(UNLOADED_CLIENT)
    on_event(None, "PLAYER_LOGOUT")
    assert saved_character(g)["clientUnloaded"] is True
    assert len(g.forbiddenCalls) == 0, list(g.forbiddenCalls.values())

    check_guard_ignores_disabled_sections()
    check_options_page()
    check_carried_periods()
    check_event_log()
    check_continent_is_no_zone()


def values(table) -> list:
    return list(table.values()) if table is not None else []


def check_legacy_tree(legacy, g) -> None:
    """Addon 0.15.0: the Legacy perks come from the non-combat trait config, challenges from the Legacy category,
    and discovery lists the Legacy names; nothing that learns, applies or toggles is ever called.
    Addon 0.17.0: trees without named groups (real client), a spell-type-only tooltip is no description."""
    assert not list(g.legacyActionCalls.values()), list(g.legacyActionCalls.values())
    assert legacy["source"] == "traits"
    configs = list(legacy["configs"].values())
    assert [c["configID"] for c in configs] == [7001], [c["configID"] for c in configs]
    config = configs[0]
    assert config["typeName"] == "Generic", dict(config)
    trees = list(config["trees"].values())
    assert [(t["name"], t["treeID"], t["points"], t["nodes"]) for t in trees] == [
        ("tree 1118", 1118, 0, 0), ("tree 1187", 1187, 0, 1), ("tree 1188", 1188, 2, 4), ("tree 1189", 1189, 0, 1)], \
        [(t["name"], t["treeID"], t["points"], t["nodes"]) for t in trees]
    assert trees[1]["info"]["hideSingleRankNumbers"] is False
    nodes = {n["name"]: n for n in config["nodes"].values()}
    assert "Hidden Perk" not in nodes and len(nodes) == 6, list(nodes)
    well = nodes["Well Rested"]
    assert well["rank"] == 2 and well["maxRank"] == 5 and well["tree"] == "tree 1188" and well["descriptionSource"] == "trait"
    assert well["row"] == 1 and well["column"] == 1, (well["row"], well["column"])
    assert list(nodes["Talented"]["requires"].values()) == ["Well Rested"] and nodes["Talented"]["prereqMet"] is False
    assert nodes["Frequent Flier"]["locked"] is True and nodes["Frequent Flier"]["requiresSpent"] == 5
    assert nodes["Working Overtime"]["description"] is None, dict(nodes["Working Overtime"])
    gourmand = nodes["Gourmand"]
    assert gourmand["description"] == "Passive\nFood heals you more." and gourmand["descriptionSource"] == "tooltip", dict(gourmand)
    assert config["missingDescriptions"] == 3, config["missingDescriptions"]
    currencies = list(config["currencies"].values())
    assert len(currencies) == 3 and {c["treeID"] for c in currencies} == {1187, 1188, 1189}
    currency = currencies[0]
    assert (currency["available"], currency["spent"], currency["max"], currency["maxAvailable"]) == (3, 2, 5, 16), dict(currency)
    assert currency["name"] == "Legacy Points" and currency["totalEarned"] == 5, dict(currency)
    challenges = list(legacy["challenges"]["list"].values())
    assert [(c["name"], c["completed"], c["category"]) for c in challenges] == [
        ("Hunter: Level 25", True, "Classes"), ("Warlock: Level 25", False, "Classes")], challenges
    assert challenges[0]["reward"] == "Reward: 1 Legacy Point" and challenges[1]["reward"] is None
    discovery = legacy["discovery"]
    assert list(discovery["namespaces"]["C_LegacyMock"].values()) == ["GetPerkInfo", "GetPointsInfo", "IsEnabled", "LearnPerk"]
    assert list(discovery["namespaces"]["C_PlayerChallengesMock"].values()) == ["GetActiveChallenges"]
    readings = discovery["readings"]
    assert readings["C_LegacyMock.GetPointsInfo"][1]["earned"] == 5 and readings["C_LegacyMock.IsEnabled"][1] is True
    assert readings["C_LegacyMock.GetPerkInfo"] is None and readings["C_LegacyMock.LearnPerk"] is None
    assert readings["C_PlayerChallengesMock.GetActiveChallenges"][1][1]["name"] == "Reach level 25"
    assert "ToggleLegacyFrameMock" in list(discovery["globals"].values())
    functions = list(discovery["members"].values())
    assert "C_GameRules.GetLegacyChallengeList" in functions and "C_GameRules.SetLegacyChallengeTracked" in functions, functions
    assert readings["C_GameRules.GetLegacyChallengeList"][1][1]["challengeID"] == 9
    assert readings["C_GameRules.SetLegacyChallengeTracked"] is None
    frame = discovery["frames"][1]
    assert frame["name"] == "LegacyFrameMock" and frame["shown"] is False and frame["fields"]["configID"] == 7001
    assert list(frame["children"].values()) == ["ChallengesTab"], frame["children"]
    assert "Enum.TraitConfigType.Generic=3" in list(discovery["enums"].values())


def check_quest_sources(snapshot, g, mode: str) -> None:
    """Addon 0.14.0: quest locations from the installed quest database and the next steps of the Guidelime guide."""
    if mode not in FOREVER_MODES:
        assert snapshot["questDb"] is None and snapshot["guide"] is None and snapshot["questLookups"] is None
        g.SlashCmdList["WOWMCP"]("quest 947")
        assert "No quest database found" in last_chat(g), last_chat(g)
        return
    section = snapshot["questDb"]
    assert dict(section["source"]) == {"name": "QuestieDB", "version": "1.1.3", "contract": 4, "readMode": "baked"}
    quests = {q["id"]: q for q in values(section["quests"])}
    # Log quests first (by ID), then the quests of the next guide steps; the hidden log entry is unknown to the database.
    assert list(quests) == [947, 4740, 948, 982], list(quests)
    assert values(section["unknownQuestIDs"]) == [99999], values(section["unknownQuestIDs"])
    cave = quests[947]
    assert cave["inLog"] and cave["inGuide"] and cave["zone"] == "Darkshore" and cave["nextQuest"]["name"] == "Onu"
    giver = values(cave["finishedBy"])[0]
    where = values(giver["locations"])[0]
    assert (giver["name"], giver["title"], where["zone"], where["x"], where["y"]) == \
        ("Barithras Moonshade", "Mushroom Collector", "Darkshore", 37.3, 43.6), dict(giver)
    assert giver["spawnCount"] == 1 and giver["zones"] is None
    kinds = [(o["kind"], o["name"], o["text"]) for o in values(cave["objectives"])]
    assert kinds == [("object", "Scaber Stalk", "Scaber Stalk"), ("item", "Death Cap", None)], kinds
    assert values(quests[4740]["startedBy"])[0]["kind"] == "object"
    assert quests[948]["inLog"] is None and quests[948]["inGuide"]
    # Spawns in several zones: the creature's main zone first, an instance point without coordinates, a finisher
    # the database does not know keeps its ID.
    ocean = quests[982]
    bear = values(ocean["objectives"])[0]
    assert bear["spawnCount"] == 6 and bear["zoneCount"] == 3 and values(bear["zones"]) == ["Darkshore", "Ashenvale", "Blackfathom Deeps"]
    assert [(l["zone"], l["x"]) for l in values(bear["locations"])] == [("Darkshore", 41.2), ("Darkshore", 42.7), ("Darkshore", 39.9)]
    finishers = [(f["kind"], f["id"], f["name"]) for f in values(ocean["finishedBy"])]
    assert finishers == [("npc", 3644, "Cerellean Whiteclaw"), ("object", 9999001, None)], finishers

    guide = snapshot["guide"]
    assert guide["name"] == "Mock Guides 17-19 Darkshore" and guide["nextGuide"] == "19-21 Ashenvale", dict(guide)
    # 14 lines are steps; two are for other races or classes. Done: both accepts (in the log), the hearth step the
    # player ticked, the turn-in of a completed quest and objective 1 of Cave Mushrooms.
    assert (guide["totalSteps"], guide["doneSteps"], guide["notForPlayer"]) == (12, 5, 2), dict(guide)
    steps = values(guide["next"])
    assert [s["line"] for s in steps] == [11, 12, 13, 14, 15, 16, 18], [s["line"] for s in steps]
    optional = steps[0]
    assert optional["optional"] and dict(values(optional["quests"])[0]) == {"id": 4740, "action": "complete"}
    assert steps[1]["text"] == "Fly to [F Auberdine]" and steps[1]["quests"] is None
    assert dict(values(steps[2]["quests"])[0]) == {"id": 947, "action": "turnin"}
    assert g.guidelimeRegistered == 2, "the listener must not swallow the guide registration"

    lookups = values(snapshot["questLookups"])
    assert [l["query"] for l in lookups] == ["947", "No Such Quest", "Murkdeep", "deep ocean, vast sea"], [l["query"] for l in lookups]
    assert values(lookups[0]["quests"])[0]["name"] == "Cave Mushrooms"
    assert values(lookups[1]["quests"]) == [] and values(lookups[1]["npcs"]) == []
    assert values(lookups[2]["npcs"])[0]["id"] == 10323
    assert values(lookups[3]["quests"])[0]["id"] == 982
    chat = [str(line) for line in g.chatlog.values()]
    assert any("ends: Barithras Moonshade, Darkshore 37.3, 43.6" in line for line in chat), chat
    assert any('Nothing found for "No Such Quest"' in line for line in chat), chat


def check_passed_guide_steps() -> None:
    """Addon 0.14.1: many open steps before the furthest done one must not push the current step off the list."""
    runtime, g, _ = load_addon("forever")
    accepts = "\n".join("Accept: [QA%d]" % (61000 + i) for i in range(20))
    runtime.execute('Guidelime.registerGuide([[\n[N1-2 Long]\n' + accepts + '\nTicked by hand\nAccept: [QA62000]\nAccept: [QA62001]\n]], "Mock Guides")')
    runtime.execute('GuidelimeDataChar.currentGuide = "Mock Guides 1-2 Long"; GuidelimeDataChar.progress = { ["Mock Guides 1-2 Long"] = { [22] = { completed = true } } }')
    event_dispatcher(g)(None, "PLAYER_LOGIN")
    g.SlashCmdList["WOWMCP"]("reload")
    guide = saved_character(g)["guide"]
    steps = values(guide["next"])
    assert (guide["totalSteps"], guide["doneSteps"], guide["passedSteps"]) == (23, 1, 20), dict(guide)
    # Five passed steps, then the steps after the ticked one.
    assert [s["passed"] for s in steps] == [True] * 5 + [None, None], [s["passed"] for s in steps]
    assert [values(s["quests"])[0]["id"] for s in steps[5:]] == [62000, 62001]


def check_realmless_key() -> None:
    """Addon 0.14.1: a client build that gives no realm name keeps the character's saved "Name-Realm" key."""
    runtime, g, addon_ns = load_addon("forever")
    store = addon_ns.Store
    runtime.execute("""
        WowMcpDB.characters = {
            ["Piekasz-Realm 7"] = { dumpedAt = 100, character = { classToken = "HUNTER", raceToken = "NightElf" }, note = "old" },
            ["Piekasz-Realm 2"] = { dumpedAt = 300, character = { classToken = "WARLOCK", raceToken = "Scourge" } },
            ["Piekasz"] = { dumpedAt = 200, character = { classToken = "HUNTER", raceToken = "NightElf" }, note = "new" },
        }
        WowMcpDB.last = "Piekasz"
    """)
    assert store.characterKey() == "Piekasz-Realm 7", store.characterKey()
    event_dispatcher(g)(None, "PLAYER_LOGIN")
    characters = g.WowMcpDB["characters"]
    # The newer bare dump moved under the realm key; the other character is untouched.
    assert characters["Piekasz"] is None and characters["Piekasz-Realm 7"]["note"] == "new"
    assert characters["Piekasz-Realm 2"]["dumpedAt"] == 300 and g.WowMcpDB["last"] == "Piekasz-Realm 7"
    # The key stays for the session even when the client no longer tells the class (character released at exit).
    runtime.execute("function UnitClass() return nil end")
    assert store.characterKey() == "Piekasz-Realm 7"
    g.SlashCmdList["WOWMCP"]("reload")
    assert g.WowMcpDB["characters"]["Piekasz"] is None and g.WowMcpDB["last"] == "Piekasz-Realm 7"

    # Two saved characters with the same name, class and race: no guess, the bare name stays.
    runtime, g, addon_ns = load_addon("forever")
    runtime.execute("""
        local same = { classToken = "HUNTER", raceToken = "NightElf" }
        WowMcpDB.characters = { ["Piekasz-A"] = { character = same }, ["Piekasz-B"] = { character = same } }
    """)
    assert addon_ns.Store.characterKey() == "Piekasz"
    # No realm name and no saved realm key: the realm number from the GUID keeps same-named characters apart,
    # and the dump saved under the bare name moves to the new key.
    runtime, g, addon_ns = load_addon("forever")
    runtime.execute("""
        function UnitGUID(unit) if unit == "player" then return "Player-5827-0A1B2C3D" end end
        WowMcpDB.characters = { ["Piekasz"] = { dumpedAt = 200, character = { classToken = "HUNTER", raceToken = "NightElf" }, note = "bare" } }
        WowMcpDB.last = "Piekasz"
    """)
    assert addon_ns.Store.characterKey() == "Piekasz-5827", addon_ns.Store.characterKey()
    event_dispatcher(g)(None, "PLAYER_LOGIN")
    assert g.WowMcpDB["characters"]["Piekasz"] is None and g.WowMcpDB["characters"]["Piekasz-5827"]["note"] == "bare"
    g.SlashCmdList["WOWMCP"]("")
    assert g.WowMcpDB["characters"]["Piekasz-5827"]["character"]["realmId"] == "5827"
    # A GUID in another shape changes nothing.
    runtime, g, addon_ns = load_addon("forever")
    runtime.execute('function UnitGUID() return "0x0000000001" end')
    assert addon_ns.Store.characterKey() == "Piekasz"
    # A realm name from the client always wins; the normalized name is the second choice.
    runtime, g, addon_ns = load_addon("forever")
    runtime.execute('function GetNormalizedRealmName() return "Realm2" end')
    assert addon_ns.Store.characterKey() == "Piekasz-Realm2"


TOMTOM_STUB = r"""
tomTomWaypoints = {}
TomTom = { AddWaypoint = function(self, mapID, x, y, opts)
    tomTomWaypoints[#tomTomWaypoints + 1] = { mapID = mapID, x = x, y = y, title = opts.title, from = opts.from, crazy = opts.crazy }
    return tomTomWaypoints[#tomTomWaypoints]
end }
"""


def check_waypoint_links() -> None:
    """Addon 0.18.0: with TomTom, /mcp quest coordinates are links that set a waypoint on click; without it, plain text."""
    # Without TomTom: plain coordinates and a one-time tip.
    runtime, g, _ = load_addon("forever")
    event_dispatcher(g)(None, "PLAYER_LOGIN")
    g.SlashCmdList["WOWMCP"]("quest 947")
    g.SlashCmdList["WOWMCP"]("quest Murkdeep")
    chat = [str(line) for line in g.chatlog.values()]
    assert any(line.endswith("ends: Barithras Moonshade, Darkshore 37.3, 43.6") for line in chat), chat
    assert any("objectives: Scaber Stalk, Darkshore 55.3, 34.7, 2 spawn points; Death Cap, item" in line for line in chat), chat
    assert not any("|H" in line for line in chat), chat
    assert sum("install TomTom" in line for line in chat) == 1, chat

    # With TomTom: links whose map comes from the database's zone data (the override wins), a title per target.
    runtime.execute(TOMTOM_STUB)
    before = len(g.chatlog)
    g.SlashCmdList["WOWMCP"]("quest 947")
    chat = [str(line) for line in list(g.chatlog.values())[before:]]
    link = "|cff71d5ff|Hwowmcp:way:1439:37.3:43.6|h[Darkshore 37.3, 43.6]|h|r"
    assert any(line.endswith("ends: Barithras Moonshade, " + link) for line in chat), chat
    assert not any("install TomTom" in line for line in chat), chat
    handler = g.linkHandlers["wowmcp"]
    assert handler("wowmcp:way:1439:37.3:43.6", "[Darkshore 37.3, 43.6]") == 2
    waypoint = g.tomTomWaypoints[1]
    assert (waypoint.mapID, round(waypoint.x, 3), round(waypoint.y, 3), waypoint.title, waypoint["from"], waypoint.crazy) == (1439, 0.373, 0.436, "Barithras Moonshade (Cave Mushrooms)", "WowMcp", True), dict(waypoint)
    # A shift-click keeps the client's default, another addon's link is not ours, nothing is set without a click.
    g.modifiedClick = True
    assert handler("wowmcp:way:1439:37.3:43.6") == 1
    g.modifiedClick = False
    assert handler("questie:947:0") == 1
    assert len(g.tomTomWaypoints) == 1
    # A link from before a /reload has no stored title: the waypoint still gets set, with a generic one.
    assert handler("wowmcp:way:1439:50:50") == 2 and g.tomTomWaypoints[2].title == "WowMcp waypoint"

    # No zone data in the database: the map comes from the zone name through the client's map data.
    runtime, g, _ = load_addon("forever")
    runtime.execute("LibQuestieDB.Support = nil")
    runtime.execute(TOMTOM_STUB)
    event_dispatcher(g)(None, "PLAYER_LOGIN")
    g.SlashCmdList["WOWMCP"]("quest Murkdeep")
    assert str(list(g.chatlog.values())[-1]) == "|cff66ccff[WowMcp]|r NPC Murkdeep, |cff71d5ff|Hwowmcp:way:1439:36.4:76.6|h[Darkshore 36.4, 76.6]|h|r", \
        list(g.chatlog.values())

    # A client without the link handler API: plain text even with TomTom, and no tip.
    runtime, g, _ = load_addon("forever", before_addon="LinkUtil = nil")
    runtime.execute(TOMTOM_STUB)
    event_dispatcher(g)(None, "PLAYER_LOGIN")
    g.SlashCmdList["WOWMCP"]("quest 947")
    chat = [str(line) for line in g.chatlog.values()]
    assert not any("|H" in line or "install TomTom" in line for line in chat), chat


def check_secret_group_range() -> None:
    """Addon 0.18.0 fix: in a real group (Forever, 2026-11-22) UnitInRange returned a secret "range checked" value and
    the boolean test on it failed the whole group section. Secrets are now dropped: the group is saved without inRange."""
    runtime, g, _ = load_addon("forever")
    runtime.execute("""
        secretValues["SECRET_IN_RANGE"], secretValues["SECRET_CHECKED"] = true, true
        function UnitInRange() return "SECRET_IN_RANGE", "SECRET_CHECKED" end
    """)
    event_dispatcher(g)(None, "PLAYER_LOGIN")
    g.SlashCmdList["WOWMCP"]("reload")
    snapshot = saved_character(g)
    assert not values(snapshot["errors"]), values(snapshot["errors"])
    members = values(snapshot["group"]["members"])
    assert len(members) == 3 and all(m["inRange"] is None for m in members), [dict(m) for m in members]


def check_quest_sources_absent() -> None:
    """Without the other addons (or with broken ones) the dump has no error and no section."""
    runtime, g, _ = load_addon("forever")
    runtime.execute("""
        LibQuestieDB.Quest.Get = function() error("database changed") end
        Guidelime, GuidelimeDataChar = nil, nil
    """)
    event_dispatcher(g)(None, "PLAYER_LOGIN")
    g.SlashCmdList["WOWMCP"]("reload")
    snapshot = saved_character(g)
    assert not values(snapshot["errors"]), values(snapshot["errors"])
    assert snapshot["guide"] is None
    # Quests still exist in the database, but every field read fails: IDs only, nothing invented.
    quest = values(snapshot["questDb"]["quests"])[0]
    assert quest["id"] == 947 and quest["name"] is None and quest["finishedBy"] is None, dict(quest)

    # A guide that was registered before the addon loaded is not visible: the section says so instead of guessing.
    runtime, g, _ = load_addon("forever")
    runtime.execute('GuidelimeDataChar.currentGuide = "Other Guides 1-6 Nowhere"')
    event_dispatcher(g)(None, "PLAYER_LOGIN")
    g.SlashCmdList["WOWMCP"]("reload")
    guide = saved_character(g)["guide"]
    assert guide["textUnavailable"] and guide["registeredGuides"] == 2 and guide["next"] is None, dict(guide)


RXP_STUB = r"""
RXPCData = {
    currentGuideName = "1-6 Mulgore", currentGuideGroup = "RestedXP", currentStep = 3, currentStepId = 12345,
    stepSkip = 2,
    guideProgress = {},
    questObjectivesCache = {
        [947] = { { type = "item", numRequired = 5, numFulfilled = 5, text = "|cff00ff00Scaber Stalk: 5/5|r", finished = true } },
        [888] = { { type = "monster", numRequired = 1, numFulfilled = 0, text = "Not in the log", finished = false } },
    },
}
for i = 1, 45 do RXPCData.guideProgress[string.format("RestedXP|Leveling|Guide %02d", i)] = { step = i, stepId = 100 + i } end
RXPCTrackingData = { levels = {} }
for level = 14, 20 do
    RXPCTrackingData.levels[level] = {
        deaths = level - 14,
        mobs = { Mulgore = { xp = 100 * level, count = 10 }, Barrens = { xp = 50, count = 2 } },
        quests = { Mulgore = { [900 + level] = 200 } },
        timestamp = 1000 + level,
    }
end
RXPGuides = { RegisterGuide = function() end }
local mockGuide = { name = "Mulgore", group = "RestedXP", displayname = "1-6 |cffff0000Mulgore|r", steps = {} }
for i = 1, 20 do
    mockGuide.steps[i] = { index = i, elements = {
        { text = "|cffff0000Step " .. i .. "|r text", questId = 2000 + i, tag = "accept" },
        { text = "Go to spot", zone = 1439, x = 40 + i, y = 60 },
    } }
end
RXPFrame = { ScrollChild = { framePool = { [1] = { guide = mockGuide } } } }
"""


def check_rxp_section() -> None:
    """Forever fork: the rxp section reads RXPCData/RXPCTrackingData and the parsed guide behind RXPFrame."""
    runtime, g, _ = load_addon("forever", before_addon=RXP_STUB)
    event_dispatcher(g)(None, "PLAYER_LOGIN")
    g.SlashCmdList["WOWMCP"]("reload")
    snapshot = saved_character(g)
    assert not values(snapshot["errors"]), values(snapshot["errors"])
    rxp = snapshot["rxp"]
    assert (rxp["currentGuideName"], rxp["currentGuideGroup"]) == ("1-6 Mulgore", "RestedXP"), dict(rxp)
    assert (rxp["currentStep"], rxp["currentStepId"], rxp["stepSkip"]) == (3, 12345, 2)
    assert rxp["totalSteps"] == 20 and rxp["guideDisplayName"] == "1-6 Mulgore"
    # The current step and the next 8, with markup stripped and element details kept.
    steps = values(rxp["steps"])
    assert [s["index"] for s in steps] == [3, 4, 5, 6, 7, 8, 9, 10, 11], [s["index"] for s in steps]
    assert steps[0]["text"] == "Step 3 text Go to spot", steps[0]["text"]
    elements = values(steps[0]["elements"])
    assert elements[0]["questId"] == 2003 and elements[0]["tag"] == "accept", dict(elements[0])
    assert (elements[1]["zone"], elements[1]["x"], elements[1]["y"]) == (1439, 43, 60), dict(elements[1])
    # Progress bounded to 40 entries with the real total next to it.
    progress = values(rxp["guideProgress"])
    assert len(progress) == 40 and rxp["guideProgressTotal"] == 45
    assert progress[0]["guide"] == "RestedXP|Leveling|Guide 01" and progress[0]["step"] == 1, dict(progress[0])
    # Objectives only for quests in the log (947 yes, 888 no).
    quests = values(rxp["questObjectives"])
    assert [q["questId"] for q in quests] == [947], [dict(q) for q in quests]
    objective = values(quests[0]["objectives"])[0]
    assert objective["text"] == "Scaber Stalk: 5/5" and objective["finished"] is True, dict(objective)
    # Tracking summary: the last 5 levels with XP totals per level.
    levels = values(rxp["levels"])
    assert [lv["level"] for lv in levels] == [16, 17, 18, 19, 20], [dict(lv) for lv in levels]
    assert (levels[0]["deaths"], levels[0]["totalMobXp"], levels[0]["totalQuestXp"]) == (2, 1650, 200), dict(levels[0])


COMPANIONS_STUB = r"""
WT_API = { IsReady = function() return true end, GetSpells = function()
    local spells = { { name = "Known Spell", level = 1, cost = 0, status = "known" } }
    for i = 1, 40 do spells[#spells + 1] = { name = "Spell " .. i, level = i, cost = i * 10, status = "available" } end
    return spells
end }
auctionatorCallers = {}
Auctionator = { API = { v1 = {
    GetAuctionPriceByItemID = function(caller, id)
        auctionatorCallers[#auctionatorCallers + 1] = caller
        if id == 2589 then return 15 end
    end,
    GetAuctionAgeByItemID = function(caller, id) return 2 end,
} } }
function PawnIsReady() return true end
function PawnGetItemData(link) if tostring(link):find("item:7005") then return { Values = { { "BM", 10 } } } end end
function PawnIsItemAnUpgrade(item) return { { ScaleName = "Beast Mastery", PercentUpgrade = 0.123 } } end
shownWorld = nil
OpenMapAPI = {
    IsStitched = function() return true end,
    GetView = function() return 0.4, 0.6, 1.5 end,
    WorldFromMap = function(mapID, x, y) return x + 1, y + 1 end,
    ShowWorld = function(u, v, zoom) shownWorld = { u = u, v = v, zoom = zoom } end,
}
ForeverLevelingBiS_API = {
    version = 2,
    GetSpec = function() return "Beast Mastery" end,
    GetWeights = function() return { agility = 1.0, stamina = 0.1, bogus = "text" } end,
    GetGain = function(link) if tostring(link):find("item:5586") then return 12.5 end return 0 end,
}
"""


def check_companions_section() -> None:
    """Forever fork: the companions section reads the other addons' public APIs behind pcall."""
    runtime, g, _ = load_addon("forever", before_addon=COMPANIONS_STUB)
    event_dispatcher(g)(None, "PLAYER_LOGIN")
    g.SlashCmdList["WOWMCP"]("reload")
    snapshot = saved_character(g)
    assert not values(snapshot["errors"]), values(snapshot["errors"])
    companions = snapshot["companions"]
    assert list(companions["sources"].values()) == ["whatsTraining", "foreverBiS", "auctionator", "pawn", "openMap"]
    # What's Training: only trainable spells up to player level + 5 (level 20 -> 25).
    training = values(companions["whatsTraining"])
    assert len(training) == 25 and training[0]["name"] == "Spell 1" and training[-1]["level"] == 25
    assert training[4]["cost"] == 50 and not any(t["name"] == "Known Spell" for t in training)
    # Forever Leveling BiS: spec, numeric weights only, a gain per equipped item (slots 16-18 in the mock).
    bis = companions["foreverBiS"]
    assert bis["spec"] == "Beast Mastery" and bis["weights"]["agility"] == 1.0 and bis["weights"]["bogus"] is None
    gains = values(bis["equippedGains"])
    assert [(gain["slot"], gain["gainPct"]) for gain in gains] == [(16, 12.5), (17, 0), (18, 0)], [dict(gain) for gain in gains]
    # Auctionator: only bag items it has a price for, asked with the WowMcp caller id.
    prices = values(companions["auctionator"])
    assert [(p["id"], p["price"], p["age"]) for p in prices] == [(2589, 15, 2)], [dict(p) for p in prices]
    assert set(g.auctionatorCallers.values()) == {"WowMcp"}
    # Pawn: the one bag item Pawn values, with the upgrade percent rounded.
    pawn = values(companions["pawn"])
    assert len(pawn) == 1 and pawn[0]["id"] == 7005, [dict(p) for p in pawn]
    upgrade = values(pawn[0]["upgrades"])[0]
    assert (upgrade["scale"], upgrade["pct"]) == ("Beast Mastery", 12), dict(upgrade)
    assert companions["openMap"]["stitched"] is True and dict(companions["openMap"]["view"]) == {"u": 0.4, "v": 0.6, "zoom": 1.5}

    # A broken integration drops only its own sub-table, without a section error.
    runtime.execute("WT_API.GetSpells = function() error('boom') end")
    g.SlashCmdList["WOWMCP"]("")
    snapshot = saved_character(g)
    assert not values(snapshot["errors"]), values(snapshot["errors"])
    assert snapshot["companions"]["whatsTraining"] is None and snapshot["companions"]["pawn"] is not None


INBOX_STUB = r"""
timerAfter, tickers = {}, {}
C_Timer = {
    After = function(seconds, fn) timerAfter[#timerAfter + 1] = { seconds = seconds, fn = fn } end,
    NewTicker = function(seconds, fn) tickers[#tickers + 1] = { seconds = seconds, fn = fn } end,
}
inCombat = false
function InCombatLockdown() return inCombat end
slotBodies, loadedSlots = {}, {}
C_AddOns.GetNumAddOns = function() return 3 end
C_AddOns.GetAddOnInfo = function(i) return ({ "WowMcp_S0001", "WowMcp_S0002", "SomeOtherAddon" })[i] end
C_AddOns.EnableAddOn = function(name) end
C_AddOns.LoadAddOn = function(name)
    loadedSlots[#loadedSlots + 1] = name
    local body = slotBodies[name]
    if body then body() end
    return true
end
tomTomWaypoints, tomTomRemoved = {}, {}
TomTom = {
    AddWaypoint = function(self, mapID, x, y, opts)
        local uid = { mapID = mapID, x = x, y = y, title = opts.title, from = opts.from, crazy = opts.crazy }
        tomTomWaypoints[#tomTomWaypoints + 1] = uid
        return uid
    end,
    RemoveWaypoint = function(self, uid) tomTomRemoved[#tomTomRemoved + 1] = uid end,
    ClearAllWaypoints = function(self) tomTomCleared = true end,
}
shownWorld = nil
OpenMapAPI = {
    IsStitched = function() return true end,
    GetView = function() return 0.1, 0.2, 1 end,
    WorldFromMap = function(mapID, x, y) return x + 1, y + 1 end,
    ShowWorld = function(u, v, zoom) shownWorld = { u = u, v = v, zoom = zoom } end,
}
"""


def check_inbox_slots() -> None:
    """Forever fork: request slots are polled outside combat, each request runs once, and the dump reports them."""
    runtime, g, _ = load_addon("forever", before_addon=INBOX_STUB)
    on_event = event_dispatcher(g)
    on_event(None, "PLAYER_LOGIN")
    on_event(None, "PLAYER_ENTERING_WORLD")
    # The ticker starts 8 s after entering the world.
    assert g.timerAfter[1]["seconds"] == 8 and len(g.tickers) == 0
    g.timerAfter[1]["fn"]()
    assert g.tickers[1]["seconds"] == 15
    tick = g.tickers[1]["fn"]
    runtime.execute(r"""
        slotBodies["WowMcp_S0001"] = function()
            WowMcpInbox = { t = time(), reqs = {
                { id = 1, kind = "waypoint", mapID = 1439, x = 37.3, y = 43.6, title = "Cave" },
                { id = 2, kind = "ping" },
                { id = 3, kind = "focus", mapID = 1439, x = 50, y = 50 },
                { id = 1, kind = "waypoint", mapID = 1439, x = 1, y = 1 },
                { id = 2 ^ 31, kind = "ping" },
            } }
        end
        slotBodies["WowMcp_S0002"] = function()
            WowMcpInbox = { t = time(), reqs = { { id = 4, kind = "clearwaypoints" } } }
        end
    """)
    # Never in combat.
    g.inCombat = True
    tick()
    assert len(g.loadedSlots) == 0
    g.inCombat = False
    tick()
    assert list(g.loadedSlots.values()) == ["WowMcp_S0001"]
    assert g.WowMcpInbox is None, "the inbox global must be cleared after a poll"
    # The waypoint request reaches TomTom with 0-1 coordinates; its duplicate id is skipped, the huge id rejected.
    assert len(g.tomTomWaypoints) == 1
    waypoint = g.tomTomWaypoints[1]
    assert (waypoint["mapID"], round(waypoint["x"], 4), round(waypoint["y"], 4)) == (1439, 0.373, 0.436), dict(waypoint)
    assert (waypoint["title"], waypoint["from"], waypoint["crazy"]) == ("Cave", "WowMcp", True)
    # The focus request glides the stitched map through OpenMapAPI.
    assert dict(g.shownWorld) == {"u": 1.5, "v": 1.5, "zoom": 0.8}, dict(g.shownWorld)
    assert list(g.WowMcpDB["inboxHandled"].values()) == [1, 2, 3]
    # The next poll loads the next slot; clearwaypoints removes the waypoints set earlier by request id.
    tick()
    assert list(g.loadedSlots.values()) == ["WowMcp_S0001", "WowMcp_S0002"]
    assert len(g.tomTomRemoved) == 1 and g.tomTomRemoved[1]["title"] == "Cave" and g.tomTomCleared is None
    assert list(g.WowMcpDB["inboxHandled"].values()) == [1, 2, 3, 4]
    # Out of slots: exactly one chat line telling the player to /reload.
    before = len(g.chatlog)
    tick()
    tick()
    chat = [str(line) for line in list(g.chatlog.values())[before:]]
    assert sum("/reload" in line for line in chat) == 1, chat
    # The dump reports the slot state.
    g.SlashCmdList["WOWMCP"]("")
    snapshot = saved_character(g)
    assert not values(snapshot["errors"]), values(snapshot["errors"])
    inbox = snapshot["inbox"]
    assert (inbox["slotsTotal"], inbox["slotsUsed"], inbox["handledCount"]) == (2, 2, 4), dict(inbox)
    assert inbox["lastInboxAt"] == 1794760000 and inbox["lastError"] is None, dict(inbox)


def main() -> int:
    if len(sys.argv) < 2 or (sys.argv[1] != "lifecycle" and len(sys.argv) < 3):
        print("usage: run_addon.py modern|legacy|forever|dede <file.lua> or run_addon.py lifecycle")
        return 2
    if sys.argv[1] == "lifecycle":  # only the lifecycle from the real game (no output file)
        check_real_lifecycle()
        print("lifecycle from the real game: OK")
        return 0
    mode, output = sys.argv[1], Path(sys.argv[2])
    check_dede_text_applied()
    runtime, g, addon_ns = load_addon(mode)
    on_event = event_dispatcher(g)
    on_event(None, "PLAYER_LOGIN")
    assert g.playedRequested, "after login the addon should request played time"
    on_event(None, "TIME_PLAYED_MSG", 360000, 7200)
    if mode in FOREVER_MODES:
        # Earlier (10 min before the dump) another trainer and another vendor: vendors/trainers keep both.
        g.clockNow, g.npcName = 1794759400, "Astaia"
        on_event(None, "TRAINER_SHOW")
        g.npcName = "Laird"
        on_event(None, "MERCHANT_SHOW")
        g.clockNow, g.npcName = 1794760000, "Einris Brightspear"
    # Own loot (the same item twice, an item with a count) and other players' loot, which must be skipped.
    if mode in ("modern", "legacy"):
        for message in ("You receive loot: |cffffffff|Hitem:2589:0|h[Linen Cloth]|h|rx3.",
                        "You receive loot: |cffffffff|Hitem:2589:0|h[Linen Cloth]|h|rx2.",
                        "You receive loot: |cffffffff|Hitem:2318:0|h[Light Leather]|h|r.",
                        "Tankadin receives loot: |cffffffff|Hitem:2770:0|h[Copper Ore]|h|r.",
                        "Tankadin receives loot: |cffffffff|Hitem:2589:0|h[Linen Cloth]|h|rx4."):
            on_event(None, "CHAT_MSG_LOOT", message)
        # XP: two kills (one with a bonus), quest XP counted once despite the message (before and after
        # the turn-in event), XP without a source and a quest without XP (skipped). Only own messages.
        for message in ("Boar dies, you gain 100 experience.",
                        "Wolf dies, you gain 80 experience. (+40 exp Rested bonus)",
                        "You gain 250 experience."):
            on_event(None, "CHAT_MSG_COMBAT_XP_GAIN", message)
        on_event(None, "QUEST_TURNED_IN", 947, 250, 350)
        on_event(None, "QUEST_TURNED_IN", 4740, 900, 0)
        on_event(None, "CHAT_MSG_COMBAT_XP_GAIN", "You gain 900 experience.")
        on_event(None, "CHAT_MSG_COMBAT_XP_GAIN", "You gain 40 experience.")
        on_event(None, "CHAT_MSG_COMBAT_XP_GAIN", "Welcome to level 21")
        on_event(None, "QUEST_TURNED_IN", 5, 0, 10)
    # Friends and guild from client memory (events the client sends by itself after the windows open).
    on_event(None, "FRIENDLIST_UPDATE")
    on_event(None, "GUILD_ROSTER_UPDATE")
    if mode == "modern":
        on_event(None, "PET_STABLE_SHOW")
    # Trainer window, then a manual dump with a reload.
    on_event(None, "TRAINER_SHOW")
    if mode == "dede":
        # German loot and XP messages; an English message must not be read on a German client.
        for message in ("Ihr erhaltet Beute: |cffffffff|Hitem:2589:0|h[Linen Cloth]|h|rx3.",
                        "Ihr erhaltet Beute: |cffffffff|Hitem:2515:0|h[Scharfer Pfeil]|h|r.",
                        "Tankadin bekommt Beute: |cffffffff|Hitem:2589:0|h[Linen Cloth]|h|rx4.",
                        "You receive loot: |cffffffff|Hitem:2770:0|h[Copper Ore]|h|r."):
            on_event(None, "CHAT_MSG_LOOT", message)
        for message in ("Eber stirbt, Ihr bekommt 100 Erfahrung.",
                        "Wolf stirbt, Ihr bekommt 80 Erfahrung. (+40 EP Erholt Bonus)",
                        "Ihr bekommt 250 Erfahrung.",
                        "Boar dies, you gain 999 experience."):
            on_event(None, "CHAT_MSG_COMBAT_XP_GAIN", message)
    if mode in FOREVER_MODES:
        on_event(None, "QUEST_DETAIL")
        g.questWindowTitle = "Red Leather Bandanas"
        on_event(None, "QUEST_COMPLETE")
        on_event(None, "PET_STABLE_SHOW")
        for event in ("MERCHANT_SHOW", "BANKFRAME_OPENED", "TAXIMAP_OPENED", "TRADE_SKILL_SHOW", "CRAFT_SHOW", "MAIL_SHOW"):
            on_event(None, event)
        # A second event in the same second does not scan the professions again.
        on_event(None, "TRADE_SKILL_UPDATE")
        # BROWSE_RESULTS_UPDATED is an old name the client does not accept (the stub will not deliver it).
        for event in ("AUCTION_HOUSE_SHOW", "BROWSE_RESULTS_UPDATED", "AUCTION_HOUSE_BROWSE_RESULTS_UPDATED", "OWNED_AUCTIONS_UPDATED"):
            on_event(None, event)
        on_event(None, "COMMODITY_SEARCH_RESULTS_UPDATED", 2589)
        on_event(None, "ITEM_SEARCH_RESULTS_UPDATED", runtime.eval("{ itemID = 5586 }"))
        # The trinket's data arrives after the browse; the Ranger's Shortbow never loads, and the retries stay bounded.
        for _ in range(30):
            on_event(None, "GET_ITEM_INFO_RECEIVED", 1404, True)
        assert 2 <= g.browseReads <= 9, g.browseReads
        on_event(None, "ITEM_SEARCH_RESULTS_UPDATED", runtime.eval("{ itemID = 12020, itemSuffix = 600 }"))
        on_event(None, "INSPECT_READY", "Player-1-TANK")
        # Death and note (letter case and spaces in the text stay).
        on_event(None, "PLAYER_DEAD")
        g.SlashCmdList["WOWMCP"]("note I want Deadmines today")
        # Quest lookups by name (any letter case), by ID and with no match; the same query twice is stored once.
        for query in ("deep ocean, vast sea", "947", "Murkdeep", "No Such Quest", "947"):
            g.SlashCmdList["WOWMCP"]("quest " + query)
    elif mode == "modern":
        for event in ("AUCTION_HOUSE_SHOW", "AUCTION_ITEM_LIST_UPDATE", "AUCTION_OWNED_LIST_UPDATE"):
            on_event(None, event)
        # A disabled section does not reach the dump; in the other modes the macros stay.
        g.SlashCmdList["WOWMCP"]("off macros")
    g.SlashCmdList["WOWMCP"]("probe" if mode in FOREVER_MODES else "reload")
    g.SlashCmdList["WOWMCP"]("status")

    for line in g.chatlog.values():
        print("chat:", line)
    assert g.reloaded, "the /mcp reload command should call ReloadUI"

    snapshot = g.WowMcpDB["characters"]["Piekasz"]
    errors = list(snapshot["errors"].values())
    assert not errors, f"section errors: {errors}"
    assert snapshot["character"]["bindLocation"] == "Auberdine"
    if mode == "legacy":
        assert snapshot["timings"] is None, "without debugprofilestop there is no timing"
        assert snapshot["disabledSections"] is None
    else:
        timings = snapshot["timings"]
        assert timings["totalMs"] > 0 and 1 <= len(timings["slowest"]) <= 5, dict(timings)
    if mode == "modern":
        assert snapshot["macros"] is None and list(snapshot["disabledSections"].values()) == ["macros"]
    if mode in FOREVER_MODES:
        for section in ("stats", "auras", "completedQuests", "actionBars", "reputation", "macros",
                        "vendor", "bank", "flightPaths", "probe", "trainer",
                        "currencies", "cooldowns", "played", "recipes", "mail"):
            assert snapshot[section] is not None, f"missing section {section}"
        cooldowns = {c["name"]: c for c in snapshot["cooldowns"].values()}
        assert set(cooldowns) == {"Aspect of the Hawk", expect(mode, "Hearthstone")}, f"cooldowns: {list(cooldowns)}"
        assert cooldowns[expect(mode, "Hearthstone")]["remainingSeconds"] == 3000
        assert snapshot["played"]["totalSeconds"] == 360000
        recipes = snapshot["recipes"]
        # Forever: only C_TradeSkillUI (no Craft window); an unknown recipe skipped.
        assert set(recipes.keys()) == {expect(mode, "Leatherworking")}, list(recipes.keys())
        lw = {r["name"]: r for r in recipes[expect(mode, "Leatherworking")]["recipes"].values()}
        assert set(lw) == {"Light Armor Kit", "Embossed Leather Gloves", "Cured Light Hide"}, list(lw)
        gloves = lw["Embossed Leather Gloves"]
        assert gloves["itemId"] == 4239 and gloves["difficulty"] == "optimal", dict(gloves)
        reagents = [(r["id"], r["need"], r["have"]) for r in gloves["reagents"].values()]
        assert reagents == [(2318, 3, 7), (2320, 2, 0)], reagents
        assert lw["Cured Light Hide"]["cooldownSeconds"] == 43200 and lw["Light Armor Kit"]["canCraft"] == 7
        assert snapshot["mail"]["messages"][2]["items"][1]["count"] == 20
        ammo = [e for e in snapshot["equipment"].values() if e["slot"] == "ammo"]
        assert ammo and ammo[0]["count"] == 812, "no ammo in equipment"
        assert snapshot["vendor"]["repairCostCopper"] == 1234
        assert snapshot["character"]["questGreenRange"] == 9
        assert snapshot["character"]["bindLocation"] == "Auberdine"
        deaths = snapshot["deaths"]
        assert deaths["count"] == 1 and len(deaths["recent"]) == 1, dict(deaths)
        assert deaths["recent"][1]["zone"] == "Stormwind City" and deaths["recent"][1]["level"] == 20
        assert snapshot["note"]["text"] == "I want Deadmines today", dict(snapshot["note"])
        assert snapshot["probe"]["events"]["PLAYER_DEAD"] is True
        assert snapshot["probe"]["events"]["AUCTION_ITEM_LIST_UPDATE"] is False
        pet_bar = {a["name"]: a for a in snapshot["pet"]["actionBar"].values()}
        assert pet_bar["Claw"]["autoCast"] is False and pet_bar["Growl"]["autoCast"] is True
        assert pet_bar[expect(mode, "Attack")]["autoCast"] is None
        assert snapshot["disabledSections"] is None
        check_legacy_tree(snapshot["legacy"], g)

        # Panel: created after login, shows the dump age, and the Save button takes a dump and /reload.
        panel = next(f for f in g.frames.values() if f.frameName == "WowMcpPanel")
        g.reloaded = False
        panel.scripts["OnUpdate"](panel, 6)  # refresh every 5 s
        assert "Dump:" in panel.ageText.label and "just now" in panel.ageText.label, panel.ageText.label
        assert "Free slots: 12" in panel.infoText.label, panel.infoText.label
        # Windows (trainer, bank, ...) delivered data that is not on disk: the panel only reminds.
        assert "click Save" in panel.hintText.label, panel.hintText.label
        assert panel.saveButton.highlighted is True
        assert not g.reloaded, "the reminder must not /reload by itself"
        panel.scripts["OnEnter"](panel)
        assert g.tooltip.shown and "no section errors" in g.tooltip.lines[1], list(g.tooltip.lines.values())
        panel.scripts["OnLeave"](panel)
        assert not g.tooltip.shown
        g.reloaded = False
        g.WowMcp_SaveAndReload()
        assert g.reloaded and g.WowMcpDB["characters"]["Piekasz"]["reason"] == "keybind", "the key binding saves like the button"
        assert g.BINDING_NAME_WOWMCP_SAVE and "WOWMCP_SAVE" in (ADDON_DIR / "Bindings.xml").read_text(encoding="utf-8")
        g.reloaded = False
        panel.saveButton.scripts["OnClick"](panel.saveButton)
        assert g.reloaded, "the Save button should call ReloadUI"
        assert g.WowMcpDB["characters"]["Piekasz"]["reason"] == "panel"
        g.SlashCmdList["WOWMCP"]("panel")
        assert g.WowMcpDB["settings"]["panelHidden"] is True
        g.SlashCmdList["WOWMCP"]("help")
        snapshot = g.WowMcpDB["characters"]["Piekasz"]

        # Minimap button: left click toggles the panel (no save or /reload), right click does nothing.
        settings = g.WowMcpDB["settings"]
        button = next(f for f in g.frames.values() if f.frameName == "WowMcpMinimapButton")
        g.reloaded = False
        button.scripts["OnClick"](button, "RightButton")
        assert settings["panelHidden"] is True, "right click should change nothing"
        button.scripts["OnClick"](button, "LeftButton")
        assert settings["panelHidden"] is None and panel.shown is True, "left click should show the panel"
        button.scripts["OnClick"](button, "LeftButton")
        assert settings["panelHidden"] is True, "the second left click should hide the panel"
        assert not g.reloaded, "the minimap button must not reload the UI"
        assert g.WowMcpDB["characters"]["Piekasz"]["reason"] == "panel", "the minimap button must not take a dump"
        # Dragging: the cursor over the minimap center = 90 degrees; after release the refresh ends.
        g.cursorX, g.cursorY = 1000, 700
        button.scripts["OnDragStart"](button)
        button.scripts["OnUpdate"]()
        assert abs(settings["minimapAngle"] - 90) < 1e-6, settings["minimapAngle"]
        button.scripts["OnDragStop"](button)
        assert button.scripts["OnUpdate"] is None
        g.SlashCmdList["WOWMCP"]("minimap off")
        assert settings["minimap"] is False and button.shown is False
        g.SlashCmdList["WOWMCP"]("minimap on")
        assert settings["minimap"] is None and button.shown is True

        # Compact mode: one line with the dump age, without the other lines; the Save button stays.
        g.SlashCmdList["WOWMCP"]("compact on")
        assert settings["compact"] is True
        assert "Dump:" in panel.ageText.label and panel.infoText.label == "" and panel.hintText.label == "", panel.ageText.label
        assert panel.titleText.shown is False and panel.closeButton.shown is False and panel.saveButton.shown is not False
        g.SlashCmdList["WOWMCP"]("compact off")
        assert settings["compact"] is None and "Free slots" in panel.infoText.label
        assert panel.titleText.shown is True
        assert not g.reloaded

    if mode in ("modern", "legacy"):
        loot = snapshot["loot"]
        items = {i["name"]: i for i in loot["items"].values()}
        assert set(items) == {"Linen Cloth", "Light Leather"}, list(items)
        assert items["Linen Cloth"]["count"] == 5 and items["Linen Cloth"]["id"] == 2589, dict(items["Linen Cloth"])
        assert loot["since"] == 1794760000
    if mode == "legacy":
        assert g.questLogSelection == 7, "the quest log selection should return to the player's choice"
        rewards = snapshot["quests"][1]["rewards"]
        assert rewards["choices"][1]["name"] == "Mushroom Cap" and rewards["choices"][1]["id"] == 4605, dict(rewards)
        assert rewards["choices"][1]["count"] == 2 and rewards["money"] == 120
    if mode in ("modern",) + FOREVER_MODES:
        quests = {q["title"]: q for q in snapshot["quests"].values()}
        rewards = quests["Cave Mushrooms"]["rewards"]
        choices = list(rewards["choices"].values())
        assert [c["name"] for c in choices] == ["Curvewood Dagger", "Webwood Slicer"], [dict(c) for c in choices]
        assert choices[1]["usable"] is False and rewards["items"][1]["count"] == 5 and rewards["money"] == 350
        assert quests["WANTED: Murkdeep!"]["rewards"]["xp"] == 1450
        if mode in FOREVER_MODES:
            assert choices[0]["equipLoc"] == "INVTYPE_WEAPON" and choices[0]["tooltip"][2] == expect(mode, "One-Hand | Dagger"), dict(choices[0])
    if mode == "modern":
        pets = list(snapshot["stable"]["pets"].values())
        assert [(p["slot"], p["name"]) for p in pets] == [(0, "Cat"), (1, "Bear")] and pets[0]["current"] is True
        # Price in the tooltip (old OnTooltipSetItem hook): the AH line, and after /mcp ahtip off no more.
        g.tooltip.item, g.tooltip.lines = "Light Leather", runtime.table()
        g.tooltip.hooks["OnTooltipSetItem"](g.GameTooltip)
        assert list(g.tooltip.lines.values()) == ["AH: 15c (just now)"], list(g.tooltip.lines.values())
        g.SlashCmdList["WOWMCP"]("ahtip off")
        g.tooltip.lines = runtime.table()
        g.tooltip.hooks["OnTooltipSetItem"](g.GameTooltip)
        assert len(g.tooltip.lines) == 0 and g.WowMcpDB["settings"]["ahTooltip"] is False
        g.SlashCmdList["WOWMCP"]("ahtip on")
        assert g.WowMcpDB["settings"]["ahTooltip"] is None
        g.tooltip.item = None
    if mode == "modern":
        talents = snapshot["talents"]
        assert talents["unspent"] == 1 and talents["spent"] == 7, dict(talents)
        assert len(talents["nodes"]) == 3 and talents["nodes"][3]["prereqMet"] is False
        check_auction_item_details(snapshot, g, mode)
        prices = snapshot["auctions"]["prices"]
        # Light Leather: 100/5 = 20 and 150/10 = 15 per item -> minimum 15, 15 items in 2 auctions.
        leather = prices["Light Leather"]
        assert leather["minBuyout"] == 15 and leather["quantity"] == 15 and leather["listings"] == 2, dict(leather)
        assert prices["Linen Cloth"]["minBuyout"] is None and prices["Linen Cloth"]["minBid"] == 5
        assert snapshot["auctions"]["lastSearchPartial"]["total"] == 120
        owned = snapshot["auctions"]["owned"][1]
        assert owned["name"] == "Copper Ore" and owned["sold"] is True, dict(owned)
        group = snapshot["group"]
        assert group["type"] == "party" and group["size"] == 3, dict(group)
        assert group["members"][2]["leader"] is True and group["members"][2]["role"] == "TANK"
        lfg = snapshot["dungeons"]["lfg"]
        assert lfg[1]["name"] == "The Deadmines" and lfg[1]["minRecLevel"] == 17
    if mode in FOREVER_MODES:
        talents = snapshot["talents"]
        nodes = {n["name"]: n for n in talents["nodes"].values()}
        assert set(nodes) == {"Endurance Training", "Focused Fire", "Efficiency", "Lethal Shots", "Wyvern Sting", "Deflection"}, list(nodes)
        assert nodes["Efficiency"]["description"] == "Reduces the Mana cost of your Shots and Stings by 2%."
        assert nodes["Efficiency"]["descriptionSource"] is None
        # Empty API description: text from the spell tooltip (without the name line).
        lethal = nodes["Lethal Shots"]
        assert lethal["description"] == "Increases your critical strike chance with ranged weapons by 1%.", lethal["description"]
        assert lethal["descriptionSource"] == "tooltip"
        # No description and no tooltip: a spell data request and a counter for the server.
        assert nodes["Focused Fire"]["description"] is None and talents["missingDescriptions"] == 1
        assert 1223755 in list(g.spellDataRequested.values())
        # Grid: numbers from 1 in a tab; 27300 is 2730 (row 2), 3940 is 3930 (row 4).
        grid = {name: (n["row"], n["column"]) for name, n in nodes.items()}
        assert grid == {"Endurance Training": (1, 1), "Focused Fire": (1, 2), "Efficiency": (1, 1), "Lethal Shots": (2, 2),
                        "Wyvern Sting": (4, 1), "Deflection": (1, 2)}, grid
        assert lethal["posY"] == 27300 and lethal["posX"] == 6220
        assert nodes["Efficiency"]["canPurchase"] is True and nodes["Efficiency"]["spellID"] == 19416
        assert list(nodes["Wyvern Sting"]["choices"].values()) == ["Wyvern Sting", "Counterattack"]
        assert len(talents["points"]) == 3
        check_auction_item_details(snapshot, g, mode)
        prices = snapshot["auctions"]["prices"]
        assert prices["Light Leather"]["minBuyout"] == 25 and prices["Light Leather"]["source"] == "browse"
        assert prices["Light Leather"]["quantity"] == 150, dict(prices["Light Leather"])
        assert prices["Linen Cloth"]["minBuyout"] == 12 and prices["Linen Cloth"]["quantity"] == 140
        assert prices["Curvewood Dagger"]["minBuyout"] == 9000 and prices["Curvewood Dagger"]["minBid"] == 7000
        assert snapshot["auctions"]["owned"][1]["timeLeftSeconds"] == 7200
        group = snapshot["group"]
        assert group["type"] == "raid" and group["size"] == 3, dict(group)
        assert group["members"][2]["masterLooter"] is True and group["members"][2]["zone"] == "Blackfathom Deeps"
        tank = snapshot["inspect"]["Tankadin"]
        assert tank["guild"] == "Night Watch" and len(tank["equipment"]) == 2 and tank["averageItemLevel"] == 18, dict(tank)
        d = snapshot["dungeons"]
        assert d["activities"][1]["maxLevelSuggestion"] == 32 and d["lockouts"][1]["killed"] == 3
        offer, turn_in = snapshot["questOffer"], snapshot["questTurnIn"]
        assert offer["title"] == "The Defias Brotherhood" and offer["npc"] == "Gryan Stoutmantle" and offer["id"] == 65, dict(offer)
        assert offer["text"] == "Kill 15 Defias Trappers." and offer["rewards"]["money"] == 1500 and offer["rewards"]["xp"] == 1150
        assert offer["rewards"]["choices"][1]["tooltip"][2] == expect(mode, "9 - 18 Damage | Speed 1.60"), dict(offer["rewards"]["choices"][1])
        assert offer["rewards"]["items"][1]["name"] == "Linen Cloth" and offer["rewards"]["items"][1]["count"] == 5
        assert turn_in["title"] == "Red Leather Bandanas" and turn_in["text"] == "Well done, Piekasz."
        assert len(turn_in["rewards"]["choices"]) == 2
        assert set(snapshot["vendors"].keys()) == {"Laird", "Einris Brightspear"}, list(snapshot["vendors"].keys())
        assert set(snapshot["trainers"].keys()) == {"Astaia", "Einris Brightspear"}, list(snapshot["trainers"].keys())
        assert snapshot["vendors"]["Laird"]["capturedAt"] == 1794759400 and snapshot["vendor"]["npc"] == "Einris Brightspear"
        pets = list(snapshot["stable"]["pets"].values())
        assert [(p["name"], p["active"]) for p in pets] == [("Cat", True), ("Owl", None)], [dict(p) for p in pets]
        owl = pets[1]
        assert owl["slot"] == 6 and owl["family"] == "Owl", dict(owl)
        # Price in the tooltip via TooltipDataProcessor; name from the ID in the tooltip data, age after 2 hours.
        g.clockNow = 1794760000 + 7200
        g.tooltip.lines = runtime.table()
        g.tooltipPostCalls[0](g.GameTooltip, runtime.eval("{ id = 2318 }"))
        assert list(g.tooltip.lines.values()) == ["AH: 25c (2 h ago)"], list(g.tooltip.lines.values())
        g.clockNow = 1794760000

    assert len(g.forbiddenCalls) == 0, f"addon called a server request: {list(g.forbiddenCalls.values())}"
    friends = snapshot["friends"]
    if mode == "legacy":
        assert friends["total"] == 2 and friends["online"] == 1, dict(friends)
        assert snapshot["pvp"] is None and snapshot["buyback"] is None and snapshot["auras"] is not None
    else:
        assert friends["total"] == 3 and friends["online"] == 2, dict(friends)
        assert snapshot["pvp"]["honorableKills"] == 1234 and snapshot["pvp"]["honor"] == 340
        if mode == "modern":
            assert snapshot["pvp"]["rank"] == 4 and snapshot["pvp"]["rankName"] == "Knight", dict(snapshot["pvp"])
        else:
            assert snapshot["pvp"]["rank"] is None, "Forever has no UnitPVPRank"
    first_friend = friends["friends"][1]
    assert first_friend["name"] == "Tankadin" and first_friend["level"] == 22 and first_friend["class"] == "Paladin"
    assert first_friend["area"] == "Stormwind City" and first_friend["connected"] is True and first_friend["notes"] == "dungeon buddy"
    assert friends["friends"][len(friends["friends"])]["connected"] is False
    guild = snapshot["guild"]
    assert guild["name"] == "Night Watch" and guild["rank"] == "Hunter Rank" and guild["total"] == 3 and guild["online"] == 2, dict(guild)
    assert len(guild["members"]) == 3 and guild["onlineOnly"] is None, "up to 200 people we save everyone"
    assert guild["members"][1]["name"] == "Tankadin" and guild["members"][1]["zone"] == "Stormwind City"
    assert guild["members"][3]["online"] is False
    sets = snapshot["equipmentSets"]
    if mode == "legacy":
        assert len(sets) == 1 and sets[1]["name"] == "Leveling" and sets[1]["isEquipped"] is False, dict(sets[1])
        assert [i["slot"] for i in sets[1]["items"].values()] == ["mainHand", "ranged"]
    else:
        assert [x["name"] for x in sets.values()] == ["Leveling", "Tanking"] and sets[1]["isEquipped"] is True
        slots = {i["slot"]: i for i in sets[1]["items"].values()}
        assert set(slots) == {"mainHand", "offHand"} and slots["mainHand"]["id"] == 5586, "slots 0 and 1 are empty and ignored"
    if mode in FOREVER_MODES:
        auras = {a["name"]: a for a in snapshot["auras"].values()}
        hawk = auras["Aspect of the Hawk"]
        assert hawk["duration"] == 600 and hawk["expiresAt"] == 1794760000 + 450, dict(hawk)
        assert auras["Ghost"]["expiresAt"] is None and auras["Ghost"]["duration"] is None, "an aura without a duration has no end"
    if mode in ("modern", "legacy"):
        xp = snapshot["xpGains"]
        assert xp["since"] == 1794760000 and xp["total"] == 100 + 80 + 250 + 900 + 40, dict(xp)
        assert (xp["kills"]["count"], xp["kills"]["xp"]) == (2, 180), dict(xp["kills"])
        assert (xp["quests"]["count"], xp["quests"]["xp"]) == (2, 1150), dict(xp["quests"])
        assert (xp["other"]["count"], xp["other"]["xp"]) == (1, 40), dict(xp["other"])
    if mode in FOREVER_MODES:
        bb = snapshot["buyback"]
        assert bb["npc"] == "Einris Brightspear" and len(bb["items"]) == 2, dict(bb)
        assert bb["items"][1]["name"] == "Linen Cloth" and bb["items"][1]["id"] == 2589
        assert bb["items"][1]["count"] == 20 and bb["items"][1]["priceCopper"] == 150, dict(bb["items"][1])
        flights = snapshot["flightPaths"]
        nodes = {n["name"]: n for n in flights["nodes"].values()}
        assert nodes["Auberdine, Darkshore"]["zone"] == "Darkshore" and nodes["Rut'theran Village, Teldrassil"]["zone"] == "Teldrassil"
        assert nodes["Astranaar, Ashenvale"]["zone"] is None and nodes["Auberdine, Darkshore"]["state"] == "current"
        # Data shape diagnostics: the flight map, raw node fields, zone sources.
        assert flights["taxiMapID"] == 1439 and "nodeID:number" in list(flights["nodeFields"].values()), dict(flights)
        assert dict(flights["zoneSources"]) == {"zoneMap": 1, "position": 1}, dict(flights["zoneSources"])
        assert snapshot["probe"]["events"]["FRIENDLIST_UPDATE"] is True and snapshot["probe"]["events"]["GUILD_ROSTER_UPDATE"] is True
    if mode == "legacy":
        assert not any(f.frameName == "WowMcpMinimapButton" for f in g.frames.values()), "without a minimap there is no button"
    else:
        assert any(f.frameName == "WowMcpMinimapButton" for f in g.frames.values())

    # Locale-independent identifiers next to the client's text.
    check_identifiers(snapshot, mode)
    check_numeric_ids(snapshot, mode, runtime, addon_ns)
    check_quest_sources(snapshot, g, mode)

    dump = g.SerializeSavedVariable("WowMcpDB", g.WowMcpDB)
    assert "INVTYPE_NON_EQUIP_IGNORE" not in dump, "equipLoc = INVTYPE_NON_EQUIP_IGNORE should not reach the dump"

    output.write_text(g.SerializeSavedVariable("WowMcpDB", g.WowMcpDB), encoding="utf-8")
    print(f"written {output} ({output.stat().st_size} bytes)")
    check_robustness(runtime, g, on_event, mode)
    if mode == "forever":
        check_quest_sources_absent()
        check_waypoint_links()
        check_secret_group_range()
        check_passed_guide_steps()
        check_legacy_scan_only_when_needed()
        check_realmless_key()
        check_rxp_section()
        check_companions_section()
        check_inbox_slots()
        check_real_lifecycle()
        print("lifecycle from the real game: OK")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
