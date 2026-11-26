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
function BoardEntry.itemLink(itemID)
    itemID = tonumber(itemID)
    if not itemID then return nil end
    callFirst({ "C_Item.RequestLoadItemDataByID" }, itemID)
    local name = firstValue({ "C_Item.GetItemInfo", "GetItemInfo" }, itemID)
    if type(name) ~= "string" or name == "" then name = "item:" .. itemID end
    return ("|cffffffff|Hitem:%d::::::::::::::|h[%s]|h|r"):format(itemID, name)
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
local function bodyText(entry)
    local parts = {}
    if type(entry.body) == "string" and entry.body ~= "" then parts[#parts + 1] = entry.body end
    local links = {}
    for i, itemID in ipairs(type(entry.items) == "table" and entry.items or {}) do
        if i > MAX_ITEMS_SHOWN then break end
        local link = BoardEntry.itemLink(itemID)
        if link then links[#links + 1] = link end
    end
    if #links > 0 then parts[#parts + 1] = table.concat(links, " ") end
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
    row.title = row:CreateFontString(nil, "OVERLAY", "GameFontNormalSmall")
    row.title:SetPoint("TOPLEFT", row, "TOPLEFT", 22, -1)
    row.title:SetWidth(ROW_WIDTH - 24)
    row.title:SetJustifyH("LEFT")
    row.body = row:CreateFontString(nil, "OVERLAY", "GameFontHighlightSmall")
    row.body:SetPoint("TOPLEFT", row.title, "BOTTOMLEFT", 0, -2)
    row.body:SetWidth(ROW_WIDTH - 24)
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
    if entry.category == "task" then
        row.check:Show()
        row.check:SetChecked(entry.done and true or false)
        row.check:SetScript("OnClick", function(self)
            ns.Board.setDone(entry.id, self:GetChecked() and true or false)
        end)
    else
        row.check:Hide()
    end
    local mark = entry.done and "|cff888888" or "|cffffd200"
    row.title:SetText(mark .. (type(entry.title) == "string" and entry.title or "(untitled)") .. "|r")
    local body = bodyText(entry)
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
