-- SPDX-License-Identifier: GPL-3.0-or-later
-- Copyright (C) 2026 Piekasz (GitHub: Piekario). Author attribution must be kept: see NOTICE.

-- Options page in the game menu (Interface / AddOns). It lists the same switches as the /mcp commands and calls the same
-- setting functions (Snapshot.setEnabled, MinimapButton.setEnabled, Panel.setCompact, AuctionTooltip.setEnabled,
-- Played.setTracking), so the page and the commands cannot disagree. It never saves a dump and never does anything in
-- the game: a checkbox only changes a setting.
-- Both the modern API (Settings.RegisterCanvasLayoutCategory) and the old one (InterfaceOptions_AddCategory) are
-- supported. Everything runs in pcall: a client with neither API, or with a different one, simply has no page and /mcp works.

local _, ns = ...
local Snapshot, chat = ns.Snapshot, ns.chat

local Options = {}
ns.Options = Options

local PAGE_NAME = "WowMcp"
local COLUMNS, COLUMN_WIDTH, ROW_HEIGHT = 3, 200, 24

local panel          -- the frame registered in the options (nil: not registered)
local category       -- category object of the modern API
local attempted = false
local populated = false
local checkboxes = {} -- { box = frame, switch = switch }

local function sectionSwitch(name)
    return {
        label = name,
        hint = "Collect the " .. name .. " section in the dump. Unchecked: it disappears from the dump (/mcp off " .. name .. ").",
        get = function() return not Snapshot.isDisabled(name) end,
        set = function(enabled)
            Snapshot.setEnabled(name, enabled)
            ns.Panel.refresh()
        end,
    }
end

-- The switches of the page: the general ones, then one per section that can be disabled. Data only (no frames),
-- so the list is the same however the page is drawn.
function Options.generalSwitches()
    return {
        {
            label = "Minimap button",
            hint = "Minimap button; left click shows the panel (/mcp minimap).",
            get = ns.MinimapButton.isEnabled,
            set = function(enabled) ns.MinimapButton.setEnabled(enabled) end,
        },
        {
            label = "Compact panel",
            hint = "One-line panel: dump age and the Save button (/mcp compact).",
            get = ns.Panel.isCompact,
            set = function(enabled) ns.Panel.setCompact(enabled) end,
        },
        {
            label = "Auction house price in the item tooltip",
            hint = "Shows the last known auction house price in item tooltips (/mcp ahtip).",
            get = ns.AuctionTooltip.isEnabled,
            set = function(enabled) ns.AuctionTooltip.setEnabled(enabled) end,
        },
        {
            label = "Request played time after login",
            hint = "Used by the progress statistics (/mcp played).",
            get = ns.Played.isTracking,
            set = function(enabled) ns.Played.setTracking(enabled) end,
        },
    }
end

function Options.sectionSwitches()
    local switches = {}
    for _, name in ipairs(Snapshot.sectionNames()) do
        if Snapshot.canDisable(name) then switches[#switches + 1] = sectionSwitch(name) end
    end
    return switches
end

local function addText(parent, template, text, x, y)
    local fontString = parent:CreateFontString(nil, "ARTWORK", template)
    fontString:SetPoint("TOPLEFT", parent, "TOPLEFT", x, y)
    fontString:SetText(text)
    return fontString
end

local function addCheckbox(parent, switch, x, y)
    local box = CreateFrame("CheckButton", nil, parent, "UICheckButtonTemplate")
    box:SetSize(24, 24)
    box:SetPoint("TOPLEFT", parent, "TOPLEFT", x, y)
    local label = parent:CreateFontString(nil, "ARTWORK", "GameFontHighlight")
    label:SetPoint("LEFT", box, "RIGHT", 2, 0)
    label:SetText(switch.label)
    box:SetScript("OnClick", function(self)
        pcall(switch.set, self:GetChecked() and true or false)
    end)
    box:SetScript("OnEnter", function(self)
        if not GameTooltip then return end
        GameTooltip:SetOwner(self, "ANCHOR_RIGHT")
        GameTooltip:SetText(switch.hint, 1, 1, 1, 1, true)
        GameTooltip:Show()
    end)
    box:SetScript("OnLeave", function() if GameTooltip then GameTooltip:Hide() end end)
    checkboxes[#checkboxes + 1] = { box = box, switch = switch }
end

-- Draws the checkboxes (once, when the page is first shown).
local function populate(parent)
    local y = -16
    addText(parent, "GameFontNormalLarge", "WowMcp", 16, y)
    y = y - 28
    addText(parent, "GameFontHighlightSmall",
        "The same switches as the /mcp commands. Nothing here saves or does anything in the game: only the Save button or /mcp reload writes to disk.", 16, y)
    y = y - 30
    for _, switch in ipairs(Options.generalSwitches()) do
        addCheckbox(parent, switch, 16, y)
        y = y - ROW_HEIGHT
    end
    y = y - 12
    addText(parent, "GameFontNormal", "Sections in the dump (unchecked: not collected and removed from the dump)", 16, y)
    y = y - 26
    local switches = Options.sectionSwitches()
    local rows = math.ceil(#switches / COLUMNS)
    for index, switch in ipairs(switches) do
        local column, row = math.floor((index - 1) / rows), (index - 1) % rows
        addCheckbox(parent, switch, 16 + column * COLUMN_WIDTH, y - row * ROW_HEIGHT)
    end
    pcall(ns.OptionsKeybind.add, parent, y - rows * ROW_HEIGHT - 14)
end

-- Sets every checkbox from the current settings (the player may have used /mcp in the meantime).
local function refreshBoxes()
    for _, entry in ipairs(checkboxes) do
        local ok, value = pcall(entry.switch.get)
        if ok then entry.box:SetChecked(value and true or false) end
    end
end

local function onShow()
    if not populated then
        populated = true
        pcall(populate, panel)
    end
    pcall(refreshBoxes)
end

local function hasModernApi()
    return type(Settings) == "table" and type(Settings.RegisterCanvasLayoutCategory) == "function"
        and type(Settings.RegisterAddOnCategory) == "function"
end

local function hasLegacyApi()
    return type(InterfaceOptions_AddCategory) == "function"
end

local function createPanel()
    if not (hasModernApi() or hasLegacyApi()) or type(CreateFrame) ~= "function" then return nil end
    local frame = CreateFrame("Frame", "WowMcpOptionsPanel", UIParent)
    frame.name = PAGE_NAME
    frame:SetScript("OnShow", onShow)
    if hasModernApi() then
        category = Settings.RegisterCanvasLayoutCategory(frame, PAGE_NAME)
        Settings.RegisterAddOnCategory(category)
    else
        InterfaceOptions_AddCategory(frame)
    end
    return frame
end

-- Creates and registers the page (once). Returns whether the page exists. Never raises an error.
function Options.register()
    if panel then return true end
    if attempted then return false end
    attempted = true
    local ok, frame = pcall(createPanel)
    if ok and frame then panel = frame end
    return panel ~= nil
end

local function openModern()
    local id = category.GetID and category:GetID() or category.ID
    Settings.OpenToCategory(id)
end

local function openLegacy()
    -- The first call of the old function only opens the menu in some clients, so it is called twice.
    InterfaceOptionsFrame_OpenToCategory(panel)
    InterfaceOptionsFrame_OpenToCategory(panel)
end

function Options.open()
    if not Options.register() then
        chat("This client has no options page for addons. The /mcp commands work: /mcp help")
        return false
    end
    local opener = (category and Settings and Settings.OpenToCategory) and openModern
        or (type(InterfaceOptionsFrame_OpenToCategory) == "function" and openLegacy) or nil
    if not opener or not pcall(opener) then
        chat("Could not open the options page. Open the game menu, Options, AddOns, WowMcp.")
        return false
    end
    return true
end
