-- SPDX-License-Identifier: GPL-3.0-or-later
-- Copyright (C) 2026 Piekasz (GitHub: Piekario). Author attribution must be kept: see NOTICE.

-- JSON encoder for the realtime strip channel (Strip.lua): the SavedVariables dump keeps the
-- client's Lua format, but what goes out through a screenshot is JSON the server decodes directly.
-- Ported from wow-seer's Json.lua (MIT, (C) 2026 heto-m).
--
-- A table with t[1] (or nothing at all) becomes an array, anything else an object with string
-- keys. Integers below 2^53 print exactly, other numbers as %.6g; NaN, infinities, client
-- "secrets" (issecretvalue) and unknown types become null. Control characters escape as \u00XX.

local _, ns = ...

local escapes = { ['"'] = '\\"', ["\\"] = "\\\\", ["\n"] = "\\n", ["\r"] = "\\r", ["\t"] = "\\t" }

local function escapeChar(c)
    return escapes[c] or string.format("\\u%04x", c:byte())
end

local function isSecret(v)
    return issecretvalue ~= nil and issecretvalue(v)
end

local encode

local function encodeTable(t, out)
    if t[1] ~= nil or next(t) == nil then
        out[#out + 1] = "["
        for i = 1, #t do
            if i > 1 then out[#out + 1] = "," end
            encode(t[i], out)
        end
        out[#out + 1] = "]"
        return
    end
    out[#out + 1] = "{"
    local first = true
    for k, v in pairs(t) do
        if not isSecret(k) and not isSecret(v) then
            if not first then out[#out + 1] = "," end
            first = false
            encode(tostring(k), out)
            out[#out + 1] = ":"
            encode(v, out)
        end
    end
    out[#out + 1] = "}"
end

encode = function(v, out)
    local kind = type(v)
    if isSecret(v) or v == nil then
        out[#out + 1] = "null"
    elseif kind == "string" then
        out[#out + 1] = '"' .. v:gsub('[%c"\\]', escapeChar) .. '"'
    elseif kind == "number" then
        if v ~= v or v == math.huge or v == -math.huge then
            out[#out + 1] = "null"
        elseif v == math.floor(v) and math.abs(v) < 2 ^ 53 then
            out[#out + 1] = string.format("%d", v)
        else
            out[#out + 1] = string.format("%.6g", v)
        end
    elseif kind == "boolean" then
        out[#out + 1] = v and "true" or "false"
    elseif kind == "table" then
        encodeTable(v, out)
    else
        out[#out + 1] = "null"
    end
end

ns.Json = {}

function ns.Json.encode(v)
    local out = {}
    encode(v, out)
    return table.concat(out)
end
