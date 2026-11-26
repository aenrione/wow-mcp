-- SPDX-License-Identifier: GPL-3.0-or-later
-- Copyright (C) 2026 Piekasz (GitHub: Piekario). Author attribution must be kept: see NOTICE.

-- Inbox: requests from the local MCP server through LoadOnDemand slot addons (WowMcp_S0001, ...).
--
-- The game never rereads a file, but it CAN load an addon it has not loaded yet. The server rewrites the
-- slot bodies on disk; each Poll loads the next unused slot, whose body sets the global WowMcpInbox once.
-- A UI session therefore consumes one slot per poll; when they run out, a /reload makes all of them new again.
--
-- Requests only drive other addons' public APIs (TomTom waypoints, OpenMap focus) and never automate
-- gameplay. Polling never happens in combat and starts 8 s after PLAYER_ENTERING_WORLD.

local _, ns = ...

local Inbox = {}
ns.Inbox = Inbox

local SLOT_FORMAT = "WowMcp_S%04d"
local SLOT_PATTERN = "^WowMcp_S(%d%d%d%d)$"
local TICK_SECONDS = 15
local IDLE_SECONDS = 60 -- between polls while the server has not been seen
local ALIVE_FOR = 180   -- an inbox stamped within this many seconds marks the server alive
local START_DELAY = 8
local MAX_HANDLED = 200
local MAX_ID = 2 ^ 31   -- request ids must fit 32-bit Lua integers

local slotCount, nextSlot = 0, 1
local lastPoll, aliveSince, lastInboxAt, lastError
local started, warnedSlots = false, false
local waypointUids = {} -- request id -> TomTom waypoint uid, for clearwaypoints

local function countSlots()
    local api = _G.C_AddOns
    if type(api) ~= "table" or type(api.GetNumAddOns) ~= "function" or type(api.GetAddOnInfo) ~= "function" then
        return 0
    end
    local ok, total = pcall(api.GetNumAddOns)
    if not ok or type(total) ~= "number" then return 0 end
    local count = 0
    for i = 1, total do
        local okInfo, name = pcall(api.GetAddOnInfo, i)
        local index = okInfo and tonumber(tostring(name or ""):match(SLOT_PATTERN))
        if index and index > count then count = index end
    end
    return count
end

local function handledIds()
    return type(WowMcpDB.inboxHandled) == "table" and WowMcpDB.inboxHandled or nil
end

local function isHandled(id)
    for _, handled in ipairs(handledIds() or {}) do
        if handled == id then return true end
    end
    return false
end

local function rememberHandled(id)
    if type(WowMcpDB.inboxHandled) ~= "table" then WowMcpDB.inboxHandled = {} end
    local handled = WowMcpDB.inboxHandled
    handled[#handled + 1] = id
    while #handled > MAX_HANDLED do table.remove(handled, 1) end
end

local function addWaypoint(req)
    local tomtom = _G.TomTom
    if type(tomtom) ~= "table" or type(tomtom.AddWaypoint) ~= "function" then return end
    local mapID, x, y = tonumber(req.mapID), tonumber(req.x), tonumber(req.y)
    if not (mapID and x and y) then return end
    local title = type(req.title) == "string" and req.title or "WowMcp"
    local ok, uid = pcall(tomtom.AddWaypoint, tomtom, mapID, x / 100, y / 100,
        { title = title, from = "WowMcp", crazy = true })
    if ok and uid ~= nil then waypointUids[req.id] = uid end
end

-- Removes the waypoints set by earlier requests; with none on record (e.g. right after a /reload)
-- it falls back to clearing all of TomTom's waypoints.
local function clearWaypoints()
    local tomtom = _G.TomTom
    if type(tomtom) ~= "table" then return end
    if next(waypointUids) == nil then
        if type(tomtom.ClearAllWaypoints) == "function" then pcall(tomtom.ClearAllWaypoints, tomtom) end
        return
    end
    for id, uid in pairs(waypointUids) do
        if type(tomtom.RemoveWaypoint) == "function" then pcall(tomtom.RemoveWaypoint, tomtom, uid) end
        waypointUids[id] = nil
    end
end

local function focusMap(req)
    local api = _G.OpenMapAPI
    if type(api) ~= "table" or type(api.WorldFromMap) ~= "function" or type(api.ShowWorld) ~= "function" then
        return
    end
    local mapID, x, y = tonumber(req.mapID), tonumber(req.x), tonumber(req.y)
    if not (mapID and x and y) then return end
    local frame = _G.WorldMapFrame
    -- Opening the map is safe here: polls never run in combat, so no protected frame is touched in combat.
    if type(frame) == "table" and type(frame.IsShown) == "function" and not frame:IsShown()
        and type(_G.ToggleWorldMap) == "function" then
        pcall(_G.ToggleWorldMap)
    end
    local ok, u, v = pcall(api.WorldFromMap, mapID, x / 100, y / 100)
    if ok and type(u) == "number" and type(v) == "number" then pcall(api.ShowWorld, u, v, 0.8) end
end

-- kind = "ping" (and any unknown kind) is handled with no action: it only confirms liveness.
local function handleRequest(req)
    if type(req) ~= "table" then return end
    local id = req.id
    if type(id) ~= "number" or id < 0 or id >= MAX_ID or id % 1 ~= 0 then return end
    if isHandled(id) then return end -- the id stays in WowMcpDB.inboxHandled: the saved list re-acks it
    rememberHandled(id)
    local kind = req.kind
    if kind == "waypoint" then
        addWaypoint(req)
    elseif kind == "clearwaypoints" then
        clearWaypoints()
    elseif kind == "focus" then
        focusMap(req)
    end
end

function Inbox.poll()
    lastPoll = GetTime()
    if nextSlot > slotCount then
        if not warnedSlots then
            warnedSlots = true
            ns.chat("All server request slots are used up: /reload to refresh them.")
        end
        return
    end
    local name = SLOT_FORMAT:format(nextSlot)
    nextSlot = nextSlot + 1
    local api = _G.C_AddOns
    _G.WowMcpInbox = nil
    pcall(api.EnableAddOn, name)
    local ok = pcall(api.LoadAddOn, name)
    local inbox = _G.WowMcpInbox
    _G.WowMcpInbox = nil
    if not ok or type(inbox) ~= "table" or type(inbox.t) ~= "number" then return end
    lastInboxAt = inbox.t
    if math.abs(time() - inbox.t) < ALIVE_FOR then aliveSince = GetTime() end
    for _, req in ipairs(type(inbox.reqs) == "table" and inbox.reqs or {}) do
        local okReq, err = pcall(handleRequest, req)
        if not okReq then lastError = tostring(err) end
    end
end

local function alive()
    return aliveSince ~= nil and GetTime() - aliveSince < ALIVE_FOR
end

function Inbox.tick()
    if type(_G.InCombatLockdown) == "function" and _G.InCombatLockdown() then return end
    if not alive() and lastPoll and GetTime() - lastPoll < IDLE_SECONDS then return end
    local ok, err = pcall(Inbox.poll)
    if not ok then lastError = tostring(err) end
end

local function start()
    if started then return end
    local timer = _G.C_Timer
    if type(timer) ~= "table" or type(timer.NewTicker) ~= "function" then return end
    started = true
    timer.NewTicker(TICK_SECONDS, Inbox.tick)
end

local function onEnteringWorld()
    local timer = _G.C_Timer
    if type(timer) == "table" and type(timer.After) == "function" then timer.After(START_DELAY, start) end
end

local function collect()
    local handled = handledIds()
    return {
        slotsTotal = slotCount,
        slotsUsed = nextSlot - 1,
        lastInboxAt = lastInboxAt,
        handledCount = handled and #handled or 0,
        lastError = lastError,
    }
end

-- Wired only when the server has installed slot addons, so without them nothing polls, no extra event is
-- registered (the probe section lists registered events) and the dump stays exactly as before.
slotCount = countSlots()
if slotCount > 0 then
    ns.Events.on("PLAYER_ENTERING_WORLD", onEnteringWorld)
    ns.Snapshot.addSection("inbox", collect)
end
