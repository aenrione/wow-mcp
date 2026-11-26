-- SPDX-License-Identifier: GPL-3.0-or-later
-- Copyright (C) 2026 Piekasz (GitHub: Piekario). Author attribution must be kept: see NOTICE.

-- Refresh requests from the server (Inbox.lua): a fresh in-memory dump sent out through the pixel
-- strip (Strip.lua) as a DELTA, never the whole snapshot. The encoded JSON of each section from the
-- last ACKNOWLEDGED send is cached; a refresh sends only the sections whose encoding changed (all of
-- them while the cache is empty, or on kind = "refreshfull"), and req.sections asks for exactly those
-- sections even when unchanged. The cache advances only when a later inbox confirms the message id in
-- got, so a lost screenshot keeps its sections dirty and the next refresh re-sends them.
-- Modelled on wow-seer's send path (Seer.lua, MIT, (C) 2026 heto-m).

local _, ns = ...

local floor = math.floor

local Refresh = {}
ns.Refresh = Refresh

local MIN_GAP = 5          -- seconds: refresh requests closer than this are ignored
local COMPRESS_FROM = 200  -- below this deflate never shrinks the JSON
-- In game a deflate call costs ~3 ms plus ~0.5 ms per KB, so big messages are deflated in slices, one per frame.
local SLICE = 4096
-- Fields that describe the dump itself (or this channel): they would mark every refresh dirty.
local META = { dumpedAt = true, dumpedAtText = true, reason = true, timings = true, inbox = true }

local acked = {}      -- section name -> encoded JSON of the last send the server confirmed
local pending         -- { msg, sections = { name -> encoded } }: sent, not confirmed yet
local job             -- deflate staging: { text, pos, slices, sections }
local lastAt          -- GetTime() of the last accepted refresh
local startedMs
local status = {}     -- lastRefreshAt, lastRefreshSkipped, lastSendOk, lastSendMs, stripError

local Clock = debugprofilestop or function() return GetTime() * 1000 end

local stager = CreateFrame("Frame")
stager:Hide()

function Refresh.status()
    return status
end

-- The server confirmed a message id (inbox.got): its sections become the shared state.
function Refresh.ack(id)
    if not pending or pending.msg ~= id then return end
    for name, text in pairs(pending.sections) do acked[name] = text end
    pending = nil
end

local function finish(ok, why)
    status.lastSendOk = ok and true or false
    status.lastSendMs = floor(Clock() - startedMs + 0.5)
    status.stripError = not ok and tostring(why) or nil
end

local function transmit(text, sections)
    job = nil
    stager:Hide()
    local msg = (WowMcpDB.stripMsgId or 0) % 65535 + 1
    WowMcpDB.stripMsgId = msg
    if ns.Strip.Send(msg, text, finish) then
        pending = { msg = msg, sections = sections }
    else
        finish(false, ns.Strip.lastError or "strip refused")
    end
end

-- "Y" marks length-prefixed raw-deflate slices for the server; JSON never starts with it.
local function packSlice()
    local lib = ns.LibDeflate
    local st = job
    local ok, packed = pcall(lib.CompressDeflate, lib, st.text:sub(st.pos, st.pos + SLICE - 1), { level = 1 })
    if not ok or type(packed) ~= "string" then return transmit(st.text, st.sections) end
    local n = #packed
    st.slices[#st.slices + 1] = string.char(floor(n / 65536) % 256, floor(n / 256) % 256, n % 256) .. packed
    st.pos = st.pos + SLICE
    if st.pos <= #st.text then return end
    local text = "Y" .. table.concat(st.slices)
    transmit(#text < #st.text and text or st.text, st.sections)
end

stager:SetScript("OnUpdate", function(self)
    if not job then self:Hide() return end
    if InCombatLockdown and InCombatLockdown() then
        job = nil
        self:Hide()
        finish(false, "combat")
        return
    end
    local ok, err = pcall(packSlice)
    if not ok then
        job = nil
        self:Hide()
        finish(false, err)
    end
end)

-- The sections to send, as name -> encoded JSON: the requested ones, or (against the ack cache)
-- the changed ones; full ignores the cache.
local function encodeSections(snapshot, wanted, full)
    local out = {}
    if wanted then
        for _, name in ipairs(wanted) do
            if snapshot[name] ~= nil then out[name] = ns.Json.encode(snapshot[name]) end
        end
        return out
    end
    for name, value in pairs(snapshot) do
        if not META[name] then
            local text = ns.Json.encode(value)
            if full or text ~= acked[name] then out[name] = text end
        end
    end
    return out
end

-- Splices the envelope from the per-section strings instead of encoding the dump twice.
local function message(sections, full)
    local parts = {}
    for name, text in pairs(sections) do
        parts[#parts + 1] = ns.Json.encode(name) .. ":" .. text
    end
    local head = ns.Json.encode({ v = 1, at = time(), character = ns.Store.characterKey(), full = full == true })
    return head:sub(1, -2) .. ',"sections":{' .. table.concat(parts, ",") .. "}}"
end

-- kind = "refresh" sends the dirty sections (or exactly req.sections), "refreshfull" everything.
-- The dump is taken fresh in memory (Snapshot.dump), never through ReloadUI.
function Refresh.request(req)
    local now = GetTime()
    if lastAt and now - lastAt < MIN_GAP then return end
    if job or ns.Strip.Busy() then return end
    if InCombatLockdown and InCombatLockdown() then return end
    lastAt = now
    status.lastRefreshAt = time()
    status.lastRefreshSkipped = nil
    startedMs = Clock()
    local ok, snapshot = pcall(ns.Snapshot.dump, "refresh")
    if not ok or type(snapshot) ~= "table" then
        finish(false, tostring(snapshot))
        return
    end
    local full = type(req) == "table" and req.kind == "refreshfull"
    local wanted = type(req) == "table" and not full and type(req.sections) == "table" and req.sections or nil
    local okEnc, sections = pcall(encodeSections, snapshot, wanted, full)
    if not okEnc then
        finish(false, tostring(sections))
        return
    end
    if next(sections) == nil then
        status.lastRefreshSkipped = true
        return
    end
    local text = message(sections, full)
    if ns.LibDeflate == nil or #text < COMPRESS_FROM then
        transmit(text, sections)
        return
    end
    job = { text = text, pos = 1, slices = {}, sections = sections }
    stager:Show()
end
