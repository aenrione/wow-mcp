-- SPDX-License-Identifier: GPL-3.0-or-later
-- Copyright (C) 2026 Piekasz (GitHub: Piekario). Author attribution must be kept: see NOTICE.

-- Inventory goals for the agent board: an entry can carry a goal that auto-completes it from the bags/bank,
-- like RXP's to-do list. sell = sell down to qty (bags), collect = reach qty in the bags, bank = reach qty
-- in the bank. The board re-evaluates on bag/bank events and marks the entry done when the goal is met.

local _, ns = ...
local plain = ns.Compat.plain

local G = {}
ns.BoardGoals = G

local MODES = { sell = true, collect = true, bank = true }

-- Parses a posted goal into { item, qty, mode }, or nil when it has no valid item.
function G.parse(value)
    if type(value) ~= "table" then return nil end
    local item = tonumber(plain(value.item))
    if not (item and item > 0) then return nil end
    local mode = tostring(plain(value.mode) or "collect"):lower()
    if not MODES[mode] then mode = "collect" end
    local qty = tonumber(plain(value.qty)) or 0
    if qty < 0 then qty = 0 end
    return { item = math.floor(item), qty = math.floor(qty), mode = mode }
end

local function ownedCount(itemID, includeBank)
    local f = _G.GetItemCount
    if type(f) ~= "function" then return 0 end
    local ok, n = pcall(f, itemID, includeBank and true or false)
    return (ok and type(n) == "number") and n or 0
end

-- Evaluates a goal: returns done, current amount and target.
function G.state(g)
    if type(g) ~= "table" or type(g.item) ~= "number" then return nil end
    local qty = type(g.qty) == "number" and g.qty or 0
    if g.mode == "sell" then
        local have = ownedCount(g.item, false)
        return have <= qty, have, qty
    elseif g.mode == "bank" then
        local have = ownedCount(g.item, true) - ownedCount(g.item, false)
        return have >= qty, have, qty
    end
    local have = ownedCount(g.item, false)
    return have >= qty, have, qty
end

local function readBoard()
    local b = _G.WowMcpDB and _G.WowMcpDB.board
    if type(b) == "table" and type(b.entries) == "table" and type(b.order) == "table" then return b end
end

-- Re-marks every goal entry's done from the current inventory; returns whether any changed.
function G.evaluate()
    local b = readBoard()
    if not b then return false end
    local changed = false
    for _, id in ipairs(b.order) do
        local e = b.entries[id]
        if type(e) == "table" and type(e.goal) == "table" then
            local done = G.state(e.goal)
            if done ~= nil and (e.done and true or false) ~= done then
                e.done = done
                changed = true
            end
        end
    end
    return changed
end
