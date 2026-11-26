-- SPDX-License-Identifier: GPL-3.0-or-later
-- Copyright (C) 2026 Piekasz (GitHub: Piekario). Author attribution must be kept: see NOTICE.

-- Section companions: read-only snapshots from other addons' public APIs (What's Training, Forever BiS,
-- Auctionator, Pawn, OpenMap). Each integration is optional and sits behind Try (pcall): a missing or broken
-- addon leaves its sub-table out, and `sources` names the ones that answered.
--
-- Only reads through each addon's public API. Nothing here changes their data.

local _, ns = ...
local plain, stripMarkup = ns.Compat.plain, ns.Compat.stripMarkup

local CALLER = "WowMcp"
local TRAINING_AHEAD = 5
local MAX_TRAINING = 30
local MAX_PRICED = 60
local MAX_PAWN = 20
local MAX_BIS = 20
local EQUIPPED_SLOTS = 18

-- Statuses What's Training gives a spell the player has not learned yet.
local TRAINABLE = {
    available = true, nextLevel = true, missingReqs = true, notLevel = true, missingTalent = true,
    weaponAvailable = true, weaponNextLevel = true,
}

local function Try(fn, ...)
    if type(fn) ~= "function" then return nil end
    local ok, a, b, c = pcall(fn, ...)
    if ok then return a, b, c end
end

local function clean(value)
    local text = plain(value)
    if type(text) ~= "string" then return nil end
    text = stripMarkup(text)
    if text:find("%S") then return text end
    return nil
end

-- Distinct items in the player's bags: { id, link }, at most `max`.
local function bagItems(max)
    local container = _G.C_Container
    if type(container) ~= "table" or type(container.GetContainerItemInfo) ~= "function" then return {} end
    local out, seen = {}, {}
    for bag = 0, 4 do
        for slot = 1, Try(container.GetContainerNumSlots, bag) or 0 do
            local info = Try(container.GetContainerItemInfo, bag, slot)
            local id = type(info) == "table" and plain(info.itemID)
            if type(id) == "number" and not seen[id] then
                seen[id] = true
                out[#out + 1] = { id = id, link = plain(info.hyperlink) }
                if #out >= max then return out end
            end
        end
    end
    return out
end

local function whatsTraining()
    local api = _G.WT_API
    if type(api) ~= "table" or Try(api.IsReady) ~= true then return nil end
    local level = plain(UnitLevel("player")) or 0
    local out = {}
    for _, spell in ipairs(Try(api.GetSpells) or {}) do
        if #out >= MAX_TRAINING then break end
        if type(spell) == "table" and TRAINABLE[plain(spell.status)] and (plain(spell.level) or 0) <= level + TRAINING_AHEAD then
            out[#out + 1] = { name = clean(spell.name), level = plain(spell.level), cost = plain(spell.cost) }
        end
    end
    table.sort(out, function(a, b) return (a.level or 0) < (b.level or 0) end)
    if #out == 0 then return nil end
    return out
end

local function foreverBiS()
    local api = _G.ForeverLevelingBiS_API
    if type(api) == "table" and (plain(api.version) or 0) >= 2 then
        local out = { source = "ForeverLevelingBiS", spec = plain(Try(api.GetSpec)) }
        local weights = Try(api.GetWeights)
        if type(weights) == "table" then
            out.weights = {}
            for stat, weight in pairs(weights) do
                if type(stat) == "string" and type(weight) == "number" then out.weights[stat] = weight end
            end
        end
        if type(api.GetGain) == "function" then
            out.equippedGains = {}
            for slot = 1, EQUIPPED_SLOTS do
                if #out.equippedGains >= MAX_BIS then break end
                local link = plain(Try(_G.GetInventoryItemLink, "player", slot))
                local gain = link and Try(api.GetGain, link)
                if type(gain) == "number" then
                    out.equippedGains[#out.equippedGains + 1] = { slot = slot, gainPct = gain }
                end
            end
        end
        return out
    end
    -- The installed ForeverWoWBiS (2026.11) exposes no function API, only its data tables: record the fact.
    local data = _G.ForeverWoW_Data
    if type(data) == "table" then
        return { source = "ForeverWoWBiS", version = plain(data.version), build = plain(data.build), noApi = true }
    end
    return nil
end

local function auctionator()
    local root = _G.Auctionator
    local api = type(root) == "table" and type(root.API) == "table" and root.API.v1
    if type(api) ~= "table" then return nil end
    local out = {}
    for _, item in ipairs(bagItems(MAX_PRICED)) do
        local price = Try(api.GetAuctionPriceByItemID, CALLER, item.id)
        if type(price) == "number" then
            out[#out + 1] = { id = item.id, price = price, age = plain(Try(api.GetAuctionAgeByItemID, CALLER, item.id)) }
        end
    end
    if #out == 0 then return nil end
    return out
end

local function pawn()
    if Try(_G.PawnIsReady) ~= true then return nil end
    local out = {}
    for _, item in ipairs(bagItems(MAX_PRICED)) do
        if #out >= MAX_PAWN then break end
        local data = item.link and Try(_G.PawnGetItemData, item.link)
        local upgrades = data and Try(_G.PawnIsItemAnUpgrade, data)
        if type(upgrades) == "table" then
            local entry = { id = item.id, upgrades = {} }
            for _, upgrade in ipairs(upgrades) do
                if type(upgrade) == "table" and type(upgrade.PercentUpgrade) == "number" then
                    entry.upgrades[#entry.upgrades + 1] = {
                        scale = clean(upgrade.LocalizedScaleName) or clean(upgrade.ScaleName),
                        pct = math.floor(upgrade.PercentUpgrade * 100 + 0.5),
                    }
                end
            end
            if #entry.upgrades > 0 then out[#out + 1] = entry end
        end
    end
    if #out == 0 then return nil end
    return out
end

local function openMap()
    local api = _G.OpenMapAPI
    if type(api) ~= "table" then return nil end
    local out = { stitched = Try(api.IsStitched) == true }
    local u, v, zoom = Try(api.GetView)
    if type(u) == "number" then out.view = { u = u, v = plain(v), zoom = plain(zoom) } end
    return out
end

local PARTS = {
    { "whatsTraining", whatsTraining },
    { "foreverBiS", foreverBiS },
    { "auctionator", auctionator },
    { "pawn", pawn },
    { "openMap", openMap },
}

local function collect()
    local section = { sources = {} }
    for _, part in ipairs(PARTS) do
        local name, read = part[1], part[2]
        local ok, result = pcall(read)
        if ok and result then
            section[name] = result
            section.sources[#section.sources + 1] = name
        end
    end
    if #section.sources == 0 then return nil end
    return section
end

-- Registered only when at least one companion addon is installed (they load first via ## OptionalDeps),
-- so without them the dump and the section timings stay exactly as before this section existed.
if type(_G.WT_API) == "table" or type(_G.Auctionator) == "table" or type(_G.PawnIsReady) == "function"
    or type(_G.OpenMapAPI) == "table" or type(_G.ForeverLevelingBiS_API) == "table"
    or type(_G.ForeverWoW_Data) == "table" then
    ns.Snapshot.addSection("companions", collect)
end
