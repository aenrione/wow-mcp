-- SPDX-License-Identifier: GPL-3.0-or-later
-- Copyright (C) 2026 Piekasz (GitHub: Piekario). Author attribution must be kept: see NOTICE.

-- Board: a toggleable, draggable in-game panel that shows notes/tasks/routes an MCP agent posted through the
-- request-slot channel (Inbox board_post). Entries are grouped by category (sell, task, route, note) and carry
-- clickable item links and a clickable coordinate link (BoardEntry). Tasks get a tick box the player can mark
-- done; the state is kept locally in WowMcpDB.board. The agent reads it back through the dump "board" section.

local _, ns = ...
local plain = ns.Compat.plain
local Store, chat = ns.Store, ns.chat

local Board = {}
ns.Board = Board

local MAX_ENTRIES = 100
local MAX_ITEMS = 20
local MAX_TITLE = 120
local MAX_BODY = 500
local MAX_SECTION_ENTRIES = 40
local CATEGORIES = { "sell", "task", "route", "note" }

local board
local boardEventsOn = false

local sectionRegistered = false

local function store()
    if type(WowMcpDB.board) ~= "table" then WowMcpDB.board = {} end
    local b = WowMcpDB.board
    if type(b.entries) ~= "table" then b.entries = {} end
    if type(b.order) ~= "table" then b.order = {} end
    if type(b.seq) ~= "number" then b.seq = 0 end
    return b
end

-- The board, read-only: nil when nothing was ever posted (so a plain dump creates no board table).
local function readBoard()
    local b = WowMcpDB.board
    if type(b) ~= "table" or type(b.entries) ~= "table" or type(b.order) ~= "table" then return nil end
    return b
end

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

-- After new entries: show the board, or hint once, so the player notices what the agent left.
function Board.onPosted(posted)
    if posted == 0 then return end
    local b = store()
    if not b.hinted then
        b.hinted = true
        chat("Your agent left notes on the board: /mcp board to see them.")
    end
    Board.refresh()
end

-- Entries in board order, newest last.
local function orderedEntries()
    local b = readBoard()
    if not b then return {} end
    local out = {}
    for _, id in ipairs(b.order) do
        local entry = b.entries[id]
        if type(entry) == "table" then out[#out + 1] = entry end
    end
    return out
end

-- Dump section "board": a summary the agent reads back (what it posted and what the player ticked done).
local function collect()
    local entries = orderedEntries()
    if #entries == 0 then return nil end
    local byCategory, list = {}, {}
    for _, entry in ipairs(entries) do
        byCategory[entry.category] = (byCategory[entry.category] or 0) + 1
        if #list < MAX_SECTION_ENTRIES then
            list[#list + 1] = { id = entry.id, category = entry.category, title = entry.title, done = entry.done }
        end
    end
    return { count = #entries, byCategory = byCategory, entries = list }
end

-- Registers the "board" dump section the first time the board is used. Doing it lazily (not at load) keeps a
-- character that never got a post free of the section, exactly like the inbox and companion sections.
function Board.ensureSection()
    if sectionRegistered then return end
    sectionRegistered = true
    ns.Snapshot.addSection("board", collect)
end

local function placeFrame(f)
    local p = Store.settings().boardPoint
    if type(p) == "table" and type(p[1]) == "string" then
        f:SetPoint(p[1], UIParent, p[2] or p[1], tonumber(p[3]) or 0, tonumber(p[4]) or 0)
    else
        f:SetPoint("TOPLEFT", UIParent, "TOPLEFT", 240, -220)
    end
end

local function makeDraggable(f)
    f:SetClampedToScreen(true)
    f:EnableMouse(true)
    f:SetMovable(true)
    f:RegisterForDrag("LeftButton")
    f:SetScript("OnDragStart", function(self) self:StartMoving() end)
    f:SetScript("OnDragStop", function(self)
        self:StopMovingOrSizing()
        local point, _, relativePoint, x, y = self:GetPoint()
        if point then Store.settings().boardPoint = { point, relativePoint, x, y } end
    end)
end

local function addBackdrop(f)
    if not f.SetBackdrop then return end
    f:SetBackdrop({
        bgFile = "Interface\\Tooltips\\UI-Tooltip-Background",
        edgeFile = "Interface\\Tooltips\\UI-Tooltip-Border",
        tile = true, tileSize = 16, edgeSize = 12,
        insets = { left = 3, right = 3, top = 3, bottom = 3 },
    })
    f:SetBackdropColor(0, 0, 0, 0.85)
end

local function addChrome(f)
    local title = f:CreateFontString(nil, "OVERLAY", "GameFontNormal")
    title:SetPoint("TOPLEFT", f, "TOPLEFT", 10, -9)
    title:SetText("WowMcp Board")
    local close = CreateFrame("Button", nil, f, "UIPanelCloseButton")
    close:SetPoint("TOPRIGHT", f, "TOPRIGHT", -1, -1)
    close:SetScript("OnClick", function()
        f:Hide()
        Store.settings().boardHidden = true
        chat("Board hidden. /mcp board shows it again.")
    end)
    local clear = CreateFrame("Button", nil, f, "UIPanelButtonTemplate")
    clear:SetSize(110, 20)
    clear:SetPoint("BOTTOMLEFT", f, "BOTTOMLEFT", 8, 8)
    clear:SetText("Clear waypoints")
    clear:SetScript("OnClick", function()
        local tomtom = _G.TomTom
        if type(tomtom) == "table" and type(tomtom.ClearAllWaypoints) == "function" then
            pcall(tomtom.ClearAllWaypoints, tomtom)
        end
    end)
    local save = CreateFrame("Button", nil, f, "UIPanelButtonTemplate")
    save:SetSize(70, 20)
    save:SetPoint("BOTTOMRIGHT", f, "BOTTOMRIGHT", -8, 8)
    save:SetText("Save")
    save:SetScript("OnClick", function()
        pcall(ns.Snapshot.dump, "board")
        ReloadUI()
    end)
    save.tooltip = "Save character state and reload (same as /mcp reload) so an agent sees fresh data."
    save:SetScript("OnEnter", function(self)
        GameTooltip:SetOwner(self, "ANCHOR_TOP")
        GameTooltip:AddLine(self.tooltip, nil, nil, nil, true)
        GameTooltip:Show()
    end)
    save:SetScript("OnLeave", function() GameTooltip:Hide() end)
    f.titleText = title
end

local function addScroll(f)
    local scroll = CreateFrame("ScrollFrame", "WowMcpBoardScroll", f,
        "UIPanelScrollFrameTemplate")
    scroll:SetPoint("TOPLEFT", f, "TOPLEFT", 8, -30)
    scroll:SetPoint("BOTTOMRIGHT", f, "BOTTOMRIGHT", -28, 34)
    local child = CreateFrame("Frame", "WowMcpBoardList", scroll)
    child:SetSize(240, 1)
    scroll:SetScrollChild(child)
    ns.BoardEntry.install(child)
    f.scroll, f.list = scroll, child
    f.empty = child:CreateFontString(nil, "OVERLAY", "GameFontDisableSmall")
    f.empty:SetPoint("TOPLEFT", child, "TOPLEFT", 6, -6)
    f.empty:SetText("No board entries yet. An agent posts with wow_board_post.")
end

-- Redraws the list from the current entries, grouped by category.
function Board.refresh()
    if not board or not board.list then return end
    local entries = orderedEntries()
    board.empty:SetText(#entries == 0 and "No board entries yet. An agent posts with wow_board_post." or "")
    local groups, order = {}, {}
    for _, name in ipairs(CATEGORIES) do groups[name] = {}; order[#order + 1] = name end
    for _, entry in ipairs(entries) do
        if not groups[entry.category] then
            groups[entry.category] = {}
            order[#order + 1] = entry.category
        end
        local g = groups[entry.category]
        g[#g + 1] = entry
    end
    ns.BoardEntry.reset()
    local y = -6
    for _, name in ipairs(order) do
        local g = groups[name]
        if g and #g > 0 then
            y = ns.BoardEntry.drawHeader(board.list, name, y)
            for _, entry in ipairs(g) do
                y = ns.BoardEntry.draw(board.list, entry, y)
            end
        end
    end
    ns.BoardEntry.finish()
    board.list:SetHeight(math.max(1, -y + 10))
end

function Board.create()
    if board then return board end
    if not (CreateFrame and UIParent) then return nil end
    local f = CreateFrame("Frame", "WowMcpBoard", UIParent, BackdropTemplateMixin and "BackdropTemplate" or nil)
    f:SetSize(280, 360)
    placeFrame(f)
    f:SetFrameStrata("MEDIUM")
    makeDraggable(f)
    addBackdrop(f)
    addChrome(f)
    addScroll(f)
    f:SetScript("OnShow", Board.refresh)
    f:Hide()
    board = f
    -- Live reactivity: when the bags change, redraw the open board so item counts (×N) track what the
    -- player holds, like RXP's to-do list. Registered lazily here so the dump fixtures stay unchanged.
    if ns.Events and not boardEventsOn then
        boardEventsOn = true
        ns.Events.on("BAG_UPDATE_DELAYED", function()
            if board and board:IsShown() then Board.refresh() end
        end)
    end
    Board.refresh()
    return f
end

function Board.toggle()
    local ok, f = pcall(Board.create)
    if not ok or not f then
        chat("Could not create the board: " .. tostring(f))
        return
    end
    local settings = Store.settings()
    if f:IsShown() then
        settings.boardHidden = true
        f:Hide()
        chat("Board hidden. /mcp board shows it again.")
    else
        settings.boardHidden = nil
        f:Show()
        chat("Board visible. You can drag it with the mouse.")
    end
end

-- The board starts hidden; it is shown only after the first agent post (or /mcp board). The dump section is
-- registered now only when a previous session already left entries (persisted in WowMcpDB.board); otherwise it
-- is registered on the first post, so a character that never used the board keeps its dump unchanged.
do
    local persisted = readBoard()
    if persisted and #persisted.order > 0 then Board.ensureSection() end
end
