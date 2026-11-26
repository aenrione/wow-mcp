-- SPDX-License-Identifier: GPL-3.0-or-later
-- Copyright (C) 2026 Piekasz (GitHub: Piekario). Author attribution must be kept: see NOTICE.

-- The "Toggle board" key binding row and the "Create board button" button on the WowMcp options page. The row shows the
-- current WOWMCP_TOGGLEBOARD binding (the same one the default Key Bindings UI edits); left click captures a key, right
-- click or Escape clears or cancels. Everything runs in pcall and copes with the modern or the legacy binding-set API
-- (GetCurrentBindingSet may be missing: then SaveBindings(1)), so a client missing either simply shows "not bound".

local _, ns = ...
local chat = ns.chat

local OptionsKeybind = {}
ns.OptionsKeybind = OptionsKeybind

local COMMAND = "WOWMCP_TOGGLEBOARD"

local capturing, keyButton

-- Modifier-only and mouse keys are not stored as a binding on their own.
local IGNORED = { LSHIFT = true, RSHIFT = true, LCTRL = true, RCTRL = true, LALT = true, RALT = true,
    UNKNOWN = true, BUTTON1 = true, BUTTON2 = true }

local function boundKey()
    if type(GetBindingKey) ~= "function" then return nil end
    local ok, key = pcall(GetBindingKey, COMMAND)
    return ok and key or nil
end

local function keyText()
    local key = boundKey()
    if not key then return "|cffaaaaaanot bound|r" end
    if type(GetBindingText) == "function" then
        local ok, text = pcall(GetBindingText, key)
        if ok and type(text) == "string" and text ~= "" then return "|cff40ff40" .. text .. "|r" end
    end
    return "|cff40ff40" .. key .. "|r"
end

local function refresh()
    if keyButton then keyButton:SetText("Toggle board: " .. keyText()) end
end

local function saveBindings()
    local set = 1
    if type(GetCurrentBindingSet) == "function" then
        local ok, current = pcall(GetCurrentBindingSet)
        if ok and current then set = current end
    end
    if type(SaveBindings) == "function" then pcall(SaveBindings, set) end
end

local function unbind()
    if type(SetBinding) ~= "function" then return end
    local key = boundKey()
    while key do
        pcall(SetBinding, key)
        local nextKey = boundKey()
        if nextKey == key then break end
        key = nextKey
    end
    saveBindings()
end

local function stopCapture()
    capturing = false
    if keyButton then
        pcall(keyButton.EnableKeyboard, keyButton, false)
        pcall(keyButton.UnlockHighlight, keyButton)
    end
    refresh()
end

local function applyKey(key)
    if type(SetBinding) ~= "function" then return end
    unbind()
    pcall(SetBinding, key, COMMAND)
    saveBindings()
end

local function onKeyDown(_, key)
    if not capturing then return end
    if key == "ESCAPE" then stopCapture() return end
    if IGNORED[key] then return end
    local combo = key
    if IsShiftKeyDown and IsShiftKeyDown() then combo = "SHIFT-" .. combo end
    if IsControlKeyDown and IsControlKeyDown() then combo = "CTRL-" .. combo end
    if IsAltKeyDown and IsAltKeyDown() then combo = "ALT-" .. combo end
    pcall(applyKey, combo)
    stopCapture()
end

local function startCapture(self)
    capturing = true
    pcall(self.EnableKeyboard, self, true)
    pcall(self.LockHighlight, self)
    self:SetText("Press a key (Esc cancels, right click clears)")
end

local function onClick(self, mouseButton)
    if mouseButton == "RightButton" then
        if capturing then stopCapture() else pcall(unbind); refresh() end
        return
    end
    if capturing then stopCapture() else startCapture(self) end
end

-- Appends the key binding row and the "Create board button" button to the options panel; returns the y below them.
function OptionsKeybind.add(parent, y)
    local label = parent:CreateFontString(nil, "ARTWORK", "GameFontNormal")
    label:SetPoint("TOPLEFT", parent, "TOPLEFT", 16, y)
    label:SetText("Agent board")
    y = y - 22
    capturing = false
    keyButton = CreateFrame("Button", "WowMcpBoardKeybindButton", parent, "UIPanelButtonTemplate")
    keyButton:SetSize(260, 22)
    keyButton:SetPoint("TOPLEFT", parent, "TOPLEFT", 16, y)
    keyButton:RegisterForClicks("LeftButtonUp", "RightButtonUp")
    keyButton:SetScript("OnClick", onClick)
    keyButton:SetScript("OnKeyDown", onKeyDown)
    refresh()
    local macroButton = CreateFrame("Button", "WowMcpBoardMacroButton", parent, "UIPanelButtonTemplate")
    macroButton:SetSize(180, 22)
    macroButton:SetPoint("LEFT", keyButton, "RIGHT", 8, 0)
    macroButton:SetText("Create board button")
    macroButton:SetScript("OnClick", function()
        local _, message = ns.BoardButton.create()
        if message then chat(message) end
    end)
    return y - 28
end
