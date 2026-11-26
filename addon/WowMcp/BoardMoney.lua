-- SPDX-License-Identifier: GPL-3.0-or-later
-- Copyright (C) 2026 Piekasz (GitHub: Piekario). Author attribution must be kept: see NOTICE.

-- Money rendering for the agent board: rewrites compact money expressions in free text (1g66s50c, 9s73,
-- 65s, 1s30, ...) into coloured digits followed by the client's gold/silver/copper coin icon textures.

local _, ns = ...

local COIN_ICON = {
    g = "Interface\\MoneyFrame\\UI-GoldIcon",
    s = "Interface\\MoneyFrame\\UI-SilverIcon",
    c = "Interface\\MoneyFrame\\UI-CopperIcon",
}

local function coinPart(amount, hex, kind)
    return ("|cff%s%d|r|T%s:12:12:2:0|t"):format(hex, amount, COIN_ICON[kind])
end

local function toCoin(copper)
    local g, s, c = math.floor(copper / 10000), math.floor((copper % 10000) / 100), copper % 100
    local out = {}
    if g > 0 then out[#out + 1] = coinPart(g, "ffd700", "g") end
    if s > 0 then out[#out + 1] = coinPart(s, "d0d0d8", "s") end
    if c > 0 or #out == 0 then out[#out + 1] = coinPart(c, "eda55f", "c") end
    return table.concat(out, " ")
end

-- Two-part forms first so "9s73" reads as 9 silver 73 copper, not just "9 silver". toCoin emits coloured
-- digits + |T coin icons; that output has no "digit+lowercase g/s/c" run, so the passes never re-process
-- an already-formatted amount. Applied only to plain text, never to links.
local function format(text)
    if type(text) ~= "string" or text == "" then return text end
    text = text:gsub("(%d+)g(%d+)s(%d+)c", function(g, s, c) return toCoin(g * 10000 + s * 100 + c) end)
    text = text:gsub("(%d+)g(%d+)s", function(g, s) return toCoin(g * 10000 + s * 100) end)
    text = text:gsub("(%d+)s(%d+)c", function(s, c) return toCoin(s * 100 + c) end)
    text = text:gsub("(%d+)g(%d+)", function(g, s) return toCoin(g * 10000 + s * 100) end)
    text = text:gsub("(%d+)s(%d+)", function(s, c) return toCoin(s * 100 + c) end)
    text = text:gsub("(%d+)g", function(g) return toCoin(g * 10000) end)
    text = text:gsub("(%d+)s", function(s) return toCoin(s * 100) end)
    text = text:gsub("(%d+)c", function(c) return toCoin(tonumber(c)) end)
    return text
end

ns.BoardMoney = { format = format }
