-- SPDX-License-Identifier: GPL-3.0-or-later
-- Copyright (C) 2026 Piekasz (GitHub: Piekario). Author attribution must be kept: see NOTICE.

-- Section rxp: where the player is in their RestedXP (RXPGuides) leveling guide, so the assistant can answer
-- "what is my next step" from the guide the player actually follows.
--
-- RXPGuides keeps its parsed guide on a private addon table, but the classic guide window stores a reference
-- to that same table on its named frame (GuideWindow.lua: frame.guide = addon.currentGuide on every step frame
-- of RXPFrame.ScrollChild.framePool). Progress and the tracked XP history live in the per-character
-- SavedVariables RXPCData and RXPCTrackingData, which are plain globals once RXPGuides has loaded
-- (## OptionalDeps guarantees it loads before WowMcp).
--
-- Only reads. Nothing here calls into RXPGuides or changes its data.

local _, ns = ...
local plain, stripMarkup = ns.Compat.plain, ns.Compat.stripMarkup

local MAX_PROGRESS = 40
local MAX_OBJECTIVE_QUESTS = 30
local MAX_OBJECTIVES_PER_QUEST = 8
local MAX_LEVELS = 5
local NEXT_STEPS = 8
local MAX_ELEMENTS = 12
local MAX_TEXT = 300

local function clean(value)
    local text = plain(value)
    if type(text) ~= "string" then return nil end
    text = stripMarkup(text)
    if #text > MAX_TEXT then text = text:sub(1, MAX_TEXT) end
    if text:find("%S") then return text end
    return nil
end

-- The parsed guide the player follows, reached through the guide window's step frames.
local function currentGuideTable()
    local frame = _G.RXPFrame
    local child = type(frame) == "table" and frame.ScrollChild
    local pool = type(child) == "table" and child.framePool
    local first = type(pool) == "table" and pool[1]
    local guide = type(first) == "table" and first.guide
    if type(guide) == "table" and type(guide.steps) == "table" then return guide end
    return nil
end

local function elementSummary(element)
    if type(element) ~= "table" then return nil end
    local out = {
        tag = plain(element.tag),
        text = clean(element.text) or clean(element.textOnly ~= true and element.rawtext),
        questId = plain(element.questId),
        zone = plain(element.zone),
        x = plain(element.x),
        y = plain(element.y),
    }
    if next(out) == nil then return nil end
    return out
end

local function stepSummary(step)
    local out = { index = plain(step.index), optional = plain(step.optional), sticky = plain(step.sticky) }
    local texts, elements = {}, {}
    for i, element in ipairs(type(step.elements) == "table" and step.elements or {}) do
        if i > MAX_ELEMENTS then break end
        local summary = elementSummary(element)
        if summary then
            elements[#elements + 1] = summary
            if summary.text then texts[#texts + 1] = summary.text end
        end
    end
    if #texts > 0 then
        local text = table.concat(texts, " ")
        out.text = #text > MAX_TEXT and text:sub(1, MAX_TEXT) or text
    end
    out.elements = #elements > 0 and elements or nil
    return out
end

-- The current step and the next NEXT_STEPS steps of the active guide.
local function guideSteps(guide, currentStep)
    local from = type(currentStep) == "number" and currentStep or 1
    if from < 1 then from = 1 end
    local steps = {}
    for index = from, math.min(from + NEXT_STEPS, #guide.steps) do
        local step = guide.steps[index]
        if type(step) == "table" then steps[#steps + 1] = stepSummary(step) end
    end
    if #steps > 0 then return steps end
    return nil
end

-- guideProgress["group|subgroup|name"] = { step, stepId }: the first MAX_PROGRESS by key, plus the real total.
local function progressSummary(progress)
    if type(progress) ~= "table" then return nil, nil end
    local keys = {}
    for key in pairs(progress) do
        if type(key) == "string" then keys[#keys + 1] = key end
    end
    table.sort(keys)
    local out = {}
    for i = 1, math.min(#keys, MAX_PROGRESS) do
        local entry = progress[keys[i]]
        if type(entry) == "table" then
            out[#out + 1] = { guide = keys[i], step = plain(entry.step), stepId = plain(entry.stepId) }
        end
    end
    if #out == 0 then return nil, nil end
    return out, #keys
end

-- questObjectivesCache for the quests in the player's log only (the cache holds abandoned quests too).
local function objectivesSummary(cache)
    if type(cache) ~= "table" then return nil end
    local logIDs = {}
    for questID in pairs(ns.Guide.logState()) do logIDs[#logIDs + 1] = questID end
    table.sort(logIDs)
    local out = {}
    for _, questID in ipairs(logIDs) do
        if #out >= MAX_OBJECTIVE_QUESTS then break end
        local objectives = cache[questID]
        if type(objectives) == "table" then
            local quest = { questId = questID, objectives = {} }
            for i, objective in ipairs(objectives) do
                if i > MAX_OBJECTIVES_PER_QUEST then break end
                if type(objective) == "table" then
                    quest.objectives[#quest.objectives + 1] = {
                        type = plain(objective.type),
                        text = clean(objective.text),
                        numRequired = plain(objective.numRequired),
                        numFulfilled = plain(objective.numFulfilled),
                        finished = plain(objective.finished),
                    }
                end
            end
            out[#out + 1] = quest
        end
    end
    if #out == 0 then return nil end
    return out
end

local function xpOf(zones, nested)
    local total = 0
    for _, entry in pairs(type(zones) == "table" and zones or {}) do
        if nested then
            for _, xp in pairs(type(entry) == "table" and entry or {}) do
                if type(xp) == "number" then total = total + xp end
            end
        elseif type(entry) == "table" and type(entry.xp) == "number" then
            total = total + entry.xp
        end
    end
    return total
end

-- RXPCTrackingData.levels[N] = { deaths, mobs[zone] = {xp, count}, quests[zone][questID] = xp }:
-- one row per level for the last MAX_LEVELS levels.
local function levelSummary(tracking)
    local levels = type(tracking) == "table" and tracking.levels
    if type(levels) ~= "table" then return nil end
    local numbers = {}
    for level in pairs(levels) do
        if type(level) == "number" then numbers[#numbers + 1] = level end
    end
    table.sort(numbers)
    local out = {}
    for i = math.max(1, #numbers - MAX_LEVELS + 1), #numbers do
        local level = levels[numbers[i]]
        if type(level) == "table" then
            out[#out + 1] = {
                level = numbers[i],
                deaths = plain(level.deaths),
                totalMobXp = xpOf(level.mobs, false),
                totalQuestXp = xpOf(level.quests, true),
            }
        end
    end
    if #out == 0 then return nil end
    return out
end

local function collect()
    local data = _G.RXPCData
    if type(_G.RXPGuides) ~= "table" and type(data) ~= "table" then return nil end
    local section = { addon = "RXPGuides" }
    if type(data) == "table" then
        section.currentGuideName = plain(data.currentGuideName)
        section.currentGuideGroup = plain(data.currentGuideGroup)
        section.currentStep = plain(data.currentStep)
        section.currentStepId = plain(data.currentStepId)
        section.stepSkip = plain(data.stepSkip)
        section.guideProgress, section.guideProgressTotal = progressSummary(data.guideProgress)
        section.questObjectives = objectivesSummary(data.questObjectivesCache)
    end
    section.levels = levelSummary(_G.RXPCTrackingData)
    local guide = currentGuideTable()
    if guide then
        section.totalSteps = #guide.steps
        section.guideDisplayName = clean(guide.displayname) or clean(guide.name)
        section.steps = guideSteps(guide, section.currentStep)
    end
    return section
end

-- Registered only when RXPGuides is installed (it loads first via ## OptionalDeps), so without it the
-- dump and the section timings stay exactly as before this section existed.
if type(_G.RXPGuides) == "table" or type(_G.RXPCData) == "table" then
    ns.Snapshot.addSection("rxp", collect)
end
