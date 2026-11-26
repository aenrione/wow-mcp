-- SPDX-License-Identifier: GPL-3.0-or-later
-- Copyright (C) 2026 Piekasz (GitHub: Piekario). Author attribution must be kept: see NOTICE.

-- Board entry rendering: a pooled row per board entry (a tick box for tasks, a title line and a body
-- with clickable item links and a clickable coordinate link that drops a TomTom waypoint). The links are
-- real hyperlinks on the scroll child: an item link behaves like any WoW item link (tooltip, shift-click to
-- chat), the coordinate link is a custom |Hwowmcpboard| type the frame's OnHyperlinkClick sends to TomTom.

local _, ns = ...
local plain, callFirst, firstValue = ns.Compat.plain, ns.Compat.callFirst, ns.Compat.firstValue

local BoardEntry = {}
ns.BoardEntry = BoardEntry

local LINK_TYPE = "wowmcpboard"
local SOURCE = "WowMcp"
local MAX_ITEMS_SHOWN = 12
local ROW_WIDTH = 240

-- Per-category accent: hex for the header label, rgb for the header rule and the row's left accent bar.
local COLORS = {
    sell  = { hex = "ffd200", r = 1.00, g = 0.82, b = 0.00 },
    task  = { hex = "4ec9ff", r = 0.31, g = 0.79, b = 1.00 },
    route = { hex = "5fe08a", r = 0.37, g = 0.88, b = 0.54 },
    note  = { hex = "c6a6ff", r = 0.78, g = 0.65, b = 1.00 },
}
local DEFAULT_COLOR = { hex = "cccccc", r = 0.80, g = 0.80, b = 0.80 }
local function colorOf(name) return COLORS[name] or DEFAULT_COLOR end

local rows, headers = {}, {}   -- pools reused across refreshes
local rowUsed, headerUsed = 0, 0

-- Starts a refresh: the pools are reused from the first widget again.
function BoardEntry.reset()
    rowUsed, headerUsed = 0, 0
end

-- Clickable item link text; the client is asked to load the item data so its name resolves next refresh.
-- Quality colour of an item as a bare AARRGGBB hex (green default so a reference always reads as a
-- clickable item). ITEM_QUALITY_COLORS[q].hex on this client already carries a "|c" prefix, so strip
-- any escape and keep only the 8 hex digits.
local function itemHex(itemID)
    local get = _G.GetItemInfo or (_G.C_Item and _G.C_Item.GetItemInfo)
    local quality
    if type(get) == "function" then
        local ok, q = pcall(function() return select(3, get(itemID)) end)
        if ok then quality = q end
    end
    local qc = _G.ITEM_QUALITY_COLORS
    if quality and qc and qc[quality] and type(qc[quality].hex) == "string" then
        local hex = qc[quality].hex:gsub("|c", ""):gsub("|r", "")
        if hex:match("^%x%x%x%x%x%x%x%x$") then return hex end
    end
    return "ff40d050"
end

function BoardEntry.itemLink(itemID)
    itemID = tonumber(itemID)
    if not itemID then return nil end
    callFirst({ "C_Item.RequestLoadItemDataByID" }, itemID)
    local name = firstValue({ "C_Item.GetItemInfo", "GetItemInfo" }, itemID)
    if type(name) ~= "string" or name == "" then name = "item:" .. itemID end
    return ("|c%s|Hitem:%d::::::::::::::|h[%s]|h|r"):format(itemHex(itemID), itemID, name)
end

-- Clickable coordinate link text, or plain coordinates when there is no map id.
function BoardEntry.coordLink(coords)
    if type(coords) ~= "table" then return nil end
    local mapID, x, y = tonumber(coords.mapID), tonumber(coords.x), tonumber(coords.y)
    if not (mapID and x and y) then return nil end
    local label = ("(%.1f, %.1f)"):format(x, y)
    return ("|cff71d5ff|H%s:%d:%s:%s|h[%s]|h|r"):format(LINK_TYPE, mapID, x, y, label)
end

local function dropWaypoint(mapID, x, y, title)
    local tomtom = _G.TomTom
    if type(tomtom) ~= "table" or type(tomtom.AddWaypoint) ~= "function" then
        ns.chat("TomTom is not loaded, so the waypoint cannot be set.")
        return
    end
    pcall(tomtom.AddWaypoint, tomtom, mapID, x / 100, y / 100,
        { title = type(title) == "string" and title or "WowMcp board", from = SOURCE, crazy = true })
end

-- Handles a click on a link in the board body. Item links go to the default handler (tooltip popup,
-- shift-click to chat); our coordinate links set a TomTom waypoint.
local function onHyperlinkClick(_, link, text, button)
    local mapID, x, y = tostring(link):match("^" .. LINK_TYPE .. ":(%d+):([%d%.]+):([%d%.]+)$")
    if mapID then
        dropWaypoint(tonumber(mapID), tonumber(x), tonumber(y))
        return
    end
    if type(_G.SetItemRef) == "function" then pcall(_G.SetItemRef, link, text, button) end
end

local function onHyperlinkEnter(self, link)
    if not GameTooltip then return end
    if link:find("^item:") then
        GameTooltip:SetOwner(self, "ANCHOR_RIGHT")
        pcall(GameTooltip.SetHyperlink, GameTooltip, link)
        GameTooltip:Show()
    elseif link:find("^" .. LINK_TYPE .. ":") then
        local x, y = link:match("^" .. LINK_TYPE .. ":%d+:([%-%d.]+):([%-%d.]+)")
        GameTooltip:SetOwner(self, "ANCHOR_RIGHT")
        GameTooltip:AddLine("Ruta sugerida")
        GameTooltip:AddLine(("(%s, %s) - clic para fijar waypoint"):format(x or "?", y or "?"), 0.7, 0.7, 0.7)
        GameTooltip:Show()
    end
end

local function onHyperlinkLeave()
    if GameTooltip then GameTooltip:Hide() end
end

-- Installs the hyperlink scripts on the scroll child once; all board links share them.
function BoardEntry.install(child)
    if not child or child.wowmcpLinks then return end
    child.wowmcpLinks = true
    if child.SetHyperlinksEnabled then child:SetHyperlinksEnabled(true) end
    child:SetScript("OnHyperlinkClick", onHyperlinkClick)
    child:SetScript("OnHyperlinkEnter", onHyperlinkEnter)
    child:SetScript("OnHyperlinkLeave", onHyperlinkLeave)
end

-- The body text of an entry: its body line, then its item links and its coordinate link.
-- Live count of an item the player currently holds, so a "sell"/"collect" entry reacts to the bags.
local function countSuffix(itemID)
    local get = _G.GetItemCount
    if type(get) ~= "function" then return "" end
    local ok, n = pcall(get, itemID)
    if not ok or type(n) ~= "number" then return "" end
    local color = n > 0 and "ffe6cc80" or "ff6a6a6a"
    return (" |c%sx%d|r"):format(color, n)
end

local formatMoney = ns.BoardMoney.format

-- Texture of an item's icon, for the row's left gutter. A valid icon is a positive fileID or a texture path.
local function validTexture(v)
    if type(v) == "number" then return v > 0 end
    return type(v) == "string" and v ~= ""
end
local function itemIcon(itemID)
    local inst = _G.GetItemInfoInstant or (_G.C_Item and _G.C_Item.GetItemInfoInstant)
    if type(inst) == "function" then
        local ok, _, _, _, _, icon = pcall(inst, itemID)
        if ok and validTexture(icon) then return icon end
    end
    local get = _G.GetItemIcon or (_G.C_Item and _G.C_Item.GetItemIconByID)
    if type(get) == "function" then
        local ok, tex = pcall(get, itemID)
        if ok and validTexture(tex) then return tex end
    end
    return nil
end

-- A coloured progress tag for an entry's inventory goal: "sell 7 left", "bank 10/25", "collect 3/25".
local function goalTag(entry)
    local g = entry.goal
    if type(g) ~= "table" or type(ns.BoardGoals) ~= "table" or type(ns.BoardGoals.state) ~= "function" then return nil end
    local done, have, target = ns.BoardGoals.state(g)
    if done == nil then return nil end
    local label
    if g.mode == "sell" then
        label = ("sell: %d left"):format(math.max(0, have))
    elseif g.mode == "bank" then
        label = ("bank: %d/%d"):format(have, target)
    else
        label = ("collect: %d/%d"):format(have, target)
    end
    return ("|c%s%s|r"):format(done and "ff5fe08a" or "ffe6cc80", label)
end

-- Resolves an entry's items into { id, name, token (link + ×N), icon }, capped at MAX_ITEMS_SHOWN.
local function resolveItems(entry)
    local out = {}
    for i, itemID in ipairs(type(entry.items) == "table" and entry.items or {}) do
        if i > MAX_ITEMS_SHOWN then break end
        local id = tonumber(itemID)
        local link = id and BoardEntry.itemLink(id)
        if link then
            local name = firstValue({ "C_Item.GetItemInfo", "GetItemInfo" }, id)
            out[#out + 1] = {
                id = id,
                name = type(name) == "string" and name ~= "" and name or nil,
                token = link .. countSuffix(id),
                icon = itemIcon(id),
            }
        end
    end
    return out
end

-- Replaces the first plain-text occurrence of name with token; reopen re-applies a colour after the
-- token's own |r so the surrounding text keeps its colour. Returns the new text and whether it matched.
local function replaceInline(text, name, token, reopen)
    if type(text) ~= "string" or type(name) ~= "string" or name == "" then return text, false end
    local s, e = text:find(name, 1, true)
    if not s then return text, false end
    return text:sub(1, s - 1) .. token .. (reopen or "") .. text:sub(e + 1), true
end

-- Builds the body: the entry body with any item not already shown in the title placed inline (or appended),
-- then the waypoint link. placed[i] marks items the title already consumed.
local function buildBody(entry, items, placed)
    local body = formatMoney(type(entry.body) == "string" and entry.body or "")
    for i, it in ipairs(items) do
        if not placed[i] and it.name then
            local nb, ok = replaceInline(body, it.name, it.token, "")
            if ok then body = nb; placed[i] = true end
        end
    end
    local extra = {}
    for i, it in ipairs(items) do
        if not placed[i] then extra[#extra + 1] = it.token end
    end
    local parts = {}
    if body ~= "" then parts[#parts + 1] = body end
    if #extra > 0 then parts[#parts + 1] = table.concat(extra, " ") end
    local coord = BoardEntry.coordLink(entry.coords)
    if coord then
        local title = type(entry.coords) == "table" and plain(entry.coords.title)
        parts[#parts + 1] = coord .. (type(title) == "string" and title ~= "" and (" " .. title) or "")
    end
    return table.concat(parts, "\n")
end

local function makeRow(child)
    local row = CreateFrame("Frame", nil, child)
    row:SetSize(ROW_WIDTH, 28)
    row:EnableMouse(true)
    BoardEntry.install(row)
    row.stripe = row:CreateTexture(nil, "BACKGROUND")
    row.stripe:SetPoint("TOPLEFT", row, "TOPLEFT", -2, 1)
    row.stripe:SetPoint("BOTTOMRIGHT", row, "BOTTOMRIGHT", 2, -3)
    row.stripe:SetColorTexture(1, 1, 1, 0.04)
    row.accent = row:CreateTexture(nil, "ARTWORK")
    row.accent:SetWidth(2)
    row.accent:SetPoint("TOPLEFT", row, "TOPLEFT", -4, 1)
    row.accent:SetPoint("BOTTOMLEFT", row, "BOTTOMLEFT", -4, -3)
    row.check = CreateFrame("CheckButton", nil, row, "UICheckButtonTemplate")
    if row.check.SetSize then row.check:SetSize(18, 18) end
    row.check:SetPoint("TOPLEFT", row, "TOPLEFT", 0, 0)
    row.icon = row:CreateTexture(nil, "ARTWORK")
    row.icon:SetSize(16, 16)
    row.icon:SetPoint("TOPLEFT", row, "TOPLEFT", 1, -1)
    row.icon:SetTexCoord(0.08, 0.92, 0.08, 0.92)
    row.title = row:CreateFontString(nil, "OVERLAY", "GameFontNormalSmall")
    row.title:SetPoint("TOPLEFT", row, "TOPLEFT", 20, -1)
    row.title:SetWidth(ROW_WIDTH - 22)
    row.title:SetJustifyH("LEFT")
    row.body = row:CreateFontString(nil, "OVERLAY", "GameFontHighlightSmall")
    row.body:SetPoint("TOPLEFT", row.title, "BOTTOMLEFT", 0, -2)
    row.body:SetWidth(ROW_WIDTH - 22)
    row.body:SetJustifyH("LEFT")
    if row.body.SetWordWrap then row.body:SetWordWrap(true) end
    return row
end

local function makeHeader(child)
    local h = CreateFrame("Frame", nil, child)
    h:SetSize(ROW_WIDTH, 18)
    h.text = h:CreateFontString(nil, "OVERLAY", "GameFontNormal")
    h.text:SetPoint("BOTTOMLEFT", h, "BOTTOMLEFT", 0, 3)
    h.rule = h:CreateTexture(nil, "ARTWORK")
    h.rule:SetHeight(1)
    h.rule:SetPoint("BOTTOMLEFT", h, "BOTTOMLEFT", 0, 0)
    h.rule:SetPoint("BOTTOMRIGHT", h, "BOTTOMRIGHT", -2, 0)
    return h
end

-- Draws a category header at the given y offset; returns the next y offset.
function BoardEntry.drawHeader(child, name, yOffset)
    headerUsed = headerUsed + 1
    local h = headers[headerUsed]
    if not h then h = makeHeader(child); headers[headerUsed] = h end
    local c = colorOf(name)
    h:ClearAllPoints()
    h:SetPoint("TOPLEFT", child, "TOPLEFT", 6, yOffset)
    h.text:SetText("|cff" .. c.hex .. tostring(name):upper() .. "|r")
    h.rule:SetColorTexture(c.r, c.g, c.b, 0.5)
    h:Show()
    return yOffset - 22
end

-- Draws one entry row at the given y offset in the scroll child; returns the next y offset.
function BoardEntry.draw(child, entry, yOffset)
    rowUsed = rowUsed + 1
    local row = rows[rowUsed]
    if not row then row = makeRow(child); rows[rowUsed] = row end
    row:ClearAllPoints()
    row:SetPoint("TOPLEFT", child, "TOPLEFT", 10, yOffset)
    local c = colorOf(entry.category)
    row.accent:SetColorTexture(c.r, c.g, c.b, entry.done and 0.25 or 0.9)
    row.stripe:SetShown(rowUsed % 2 == 0)
    local items = resolveItems(entry)
    local primaryIcon = items[1] and items[1].icon
    if entry.category == "task" then
        row.check:Show()
        row.check:SetChecked(entry.done and true or false)
        row.check:SetScript("OnClick", function(self)
            ns.Board.setDone(entry.id, self:GetChecked() and true or false)
        end)
        row.icon:Hide()
    elseif primaryIcon then
        row.check:Hide()
        row.icon:SetTexture(primaryIcon)
        row.icon:SetDesaturated(entry.done and true or false)
        row.icon:Show()
    else
        row.check:Hide()
        row.icon:Hide()
    end
    local mark = entry.done and "|cff888888" or "|cffffd200"
    local placed = {}
    local titleText = formatMoney(type(entry.title) == "string" and entry.title or "(untitled)")
    for i, it in ipairs(items) do
        if it.name then
            local nt, ok = replaceInline(titleText, it.name, it.token, mark)
            if ok then titleText = nt; placed[i] = true end
        end
    end
    local check = entry.done and "|cff5fe08a\226\156\148|r " or ""
    row.title:SetText(check .. mark .. titleText .. "|r")
    local body = buildBody(entry, items, placed)
    local tag = goalTag(entry)
    if tag then body = tag .. (body ~= "" and ("\n" .. body) or "") end
    row.body:SetText(body)
    row.body:SetShown(body ~= "")
    row:Show()
    -- Measure the wrapped text so a long title or body never overflows the row or overlaps the next one.
    local titleH = math.max(10, row.title:GetStringHeight() or 10)
    local bodyH = (body ~= "" and math.max(10, row.body:GetStringHeight() or 10)) or 0
    local rowH = 1 + titleH + (bodyH > 0 and (2 + bodyH) or 0) + 6
    row:SetHeight(rowH)
    return yOffset - rowH
end

-- Hides the pooled rows and headers left unused by this refresh.
function BoardEntry.finish()
    for i = rowUsed + 1, #rows do rows[i]:Hide() end
    for i = headerUsed + 1, #headers do headers[i]:Hide() end
end
