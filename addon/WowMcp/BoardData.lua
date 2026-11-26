-- SPDX-License-Identifier: GPL-3.0-or-later
-- Copyright (C) 2026 Piekasz (GitHub: Piekario). Author attribution must be kept: see NOTICE.

-- Agent board data model: parsing and storing the entries an agent posts (Inbox board_post), kept in
-- WowMcpDB.board. The frame, rendering and dump section live in Board.lua / BoardEntry.lua.

local _, ns = ...
local plain = ns.Compat.plain
local chat = ns.chat
local Board = ns.Board

local MAX_ENTRIES = 100
local MAX_ITEMS = 20
local MAX_TITLE = 120
local MAX_BODY = 500

local function store()
    if type(WowMcpDB.board) ~= "table" then WowMcpDB.board = {} end
    local b = WowMcpDB.board
    if type(b.entries) ~= "table" then b.entries = {} end
    if type(b.order) ~= "table" then b.order = {} end
    if type(b.seq) ~= "number" then b.seq = 0 end
    return b
end
ns.Board.store = store

local function clamp(value, max)
    local text = plain(value)
    if type(text) ~= "string" then return nil end
    if #text > max then text = text:sub(1, max) end
    return text
end

local function category(value)
    local text = plain(value)
    if type(text) ~= "string" or text == "" then return "note" end
    return text:lower()
end

local function itemList(value)
    if type(value) ~= "table" then return nil end
    local out = {}
    for _, id in ipairs(value) do
        local n = tonumber(plain(id))
        if n and n > 0 and n % 1 == 0 then
            out[#out + 1] = n
            if #out >= MAX_ITEMS then break end
        end
    end
    return #out > 0 and out or nil
end

local function coords(value)
    if type(value) ~= "table" then return nil end
    local mapID, x, y = tonumber(plain(value.mapID)), tonumber(plain(value.x)), tonumber(plain(value.y))
    if not (mapID and x and y) then return nil end
    return { mapID = mapID, x = x, y = y, title = clamp(value.title, MAX_TITLE) }
end

local function assignId(b)
    b.seq = b.seq + 1
    return "a" .. b.seq
end

local function bound(b)
    while #b.order > MAX_ENTRIES do
        local oldest = table.remove(b.order, 1)
        b.entries[oldest] = nil
    end
end

-- Merges or appends agent entries; an entry id already on the board is updated in place (idempotent),
-- keeping its done tick and creation time. Returns how many entries were posted.
function Board.post(entries)
    if type(entries) ~= "table" then return 0 end
    local b = store()
    local posted = 0
    for _, raw in ipairs(entries) do
        if type(raw) == "table" then
            local id = plain(raw.id)
            if type(id) ~= "string" or id == "" then id = assignId(b) end
            local existing = b.entries[id]
            local entry = existing or { id = id, at = time(), source = "agent", done = false }
            entry.category = category(raw.category)
            entry.title = clamp(raw.title, MAX_TITLE) or "(untitled)"
            entry.body = clamp(raw.body, MAX_BODY)
            entry.items = itemList(raw.items)
            entry.coords = coords(raw.coords)
            entry.goal = ns.BoardGoals.parse(raw.goal)
            if not existing then
                b.entries[id] = entry
                b.order[#b.order + 1] = id
            end
            posted = posted + 1
        end
    end
    bound(b)
    Board.ensureSection()
    Board.onPosted(posted)
    return posted
end

-- Removes the given entry ids, or every entry when ids is nil/empty (a server "clear").
function Board.clear(ids)
    local b = store()
    if type(ids) == "table" and #ids > 0 then
        local drop = {}
        for _, id in ipairs(ids) do if type(plain(id)) == "string" then drop[id] = true end end
        local kept = {}
        for _, id in ipairs(b.order) do
            if drop[id] then b.entries[id] = nil else kept[#kept + 1] = id end
        end
        b.order = kept
    else
        b.entries, b.order = {}, {}
    end
    Board.refresh()
end

function Board.setDone(id, done)
    local b = store()
    local entry = b.entries[id]
    if type(entry) == "table" then
        entry.done = done and true or false
        Board.refresh()
    end
end

-- After new entries: hint once so the player notices what the agent left, then redraw if open.
function Board.onPosted(posted)
    if posted == 0 then return end
    local b = store()
    if not b.hinted then
        b.hinted = true
        chat("Your agent left notes on the board: /mcp board to see them.")
    end
    Board.refresh()
end
