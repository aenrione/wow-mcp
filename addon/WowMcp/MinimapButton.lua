-- SPDX-License-Identifier: GPL-3.0-or-later
-- Copyright (C) 2026 Piekasz (GitHub: Piekario). Author attribution must be kept: see NOTICE.

-- Minimap button: left click shows or hides the panel (the same as /mcp panel).
-- Saves and reloads nothing; right click does nothing. The player can drag it
-- around the minimap. Disabled by /mcp minimap off (setting minimap = false).
-- Created in pcall: when that fails (another client, no minimap), there is simply no button.

local _, ns = ...
local Store, Panel = ns.Store, ns.Panel

local MinimapButton = {}
ns.MinimapButton = MinimapButton

local RADIUS = 80
local DEFAULT_ANGLE = 225 -- degrees, bottom-left corner of the minimap
local button

local function angleSetting()
    local angle = tonumber(Store.settings().minimapAngle)
    return angle or DEFAULT_ANGLE
end

local function place(angleDegrees)
    local radians = math.rad(angleDegrees)
    button:ClearAllPoints()
    button:SetPoint("CENTER", Minimap, "CENTER", math.cos(radians) * RADIUS, math.sin(radians) * RADIUS)
end

-- Angle from the minimap center to the cursor (degrees). Only reads the mouse position.
local function cursorAngle()
    local centerX, centerY = Minimap:GetCenter()
    local cursorX, cursorY = GetCursorPosition()
    local scale = Minimap:GetEffectiveScale()
    if not (centerX and cursorX and scale and scale > 0) then return nil end
    local atan2 = math.atan2 or math.atan
    return math.deg(atan2(cursorY / scale - centerY, cursorX / scale - centerX))
end

local function startDrag(self)
    self:SetScript("OnUpdate", function()
        local angle = cursorAngle()
        if angle then
            Store.settings().minimapAngle = angle
            place(angle)
        end
    end)
end

local function stopDrag(self)
    self:SetScript("OnUpdate", nil)
end

local function showTooltip(self)
    if not GameTooltip then return end
    GameTooltip:SetOwner(self, "ANCHOR_LEFT")
    GameTooltip:SetText("WowMcp")
    GameTooltip:AddLine("Left click: show or hide the panel", 1, 1, 1)
    GameTooltip:AddLine("Only the Save button on the panel saves", 0.7, 0.7, 0.7)
    GameTooltip:Show()
end

local function build()
    local b = CreateFrame("Button", "WowMcpMinimapButton", Minimap)
    b:SetSize(28, 28)
    b:SetFrameStrata("MEDIUM")
    b:SetFrameLevel(8)
    local icon = b:CreateTexture(nil, "ARTWORK")
    icon:SetTexture((ns.BoardButton and ns.BoardButton.ICON) or "Interface\\Icons\\INV_Misc_Book_09")
    icon:SetSize(18, 18)
    icon:SetPoint("CENTER", b, "CENTER", 0, 0)
    local ring = b:CreateTexture(nil, "OVERLAY")
    ring:SetTexture("Interface\\Minimap\\MiniMap-TrackingBorder")
    ring:SetSize(52, 52)
    ring:SetPoint("TOPLEFT", b, "TOPLEFT", 0, 0)
    b:RegisterForClicks("LeftButtonUp", "RightButtonUp")
    b:RegisterForDrag("LeftButton")
    b:SetScript("OnClick", function(_, mouseButton)
        if mouseButton == "LeftButton" then Panel.toggle() end
    end)
    b:SetScript("OnDragStart", startDrag)
    b:SetScript("OnDragStop", stopDrag)
    b:SetScript("OnEnter", showTooltip)
    b:SetScript("OnLeave", function() if GameTooltip then GameTooltip:Hide() end end)
    return b
end

-- Creates the button (once) or shows it; returns the frame or nil. Does nothing when minimap = false.
function MinimapButton.create()
    if Store.settings().minimap == false then return nil end
    if not (CreateFrame and Minimap) then return nil end
    if not button then
        button = build()
        place(angleSetting())
    end
    button:Show()
    return button
end

function MinimapButton.isEnabled()
    return Store.settings().minimap ~= false
end

function MinimapButton.setEnabled(enabled)
    if enabled then
        Store.settings().minimap = nil
        local ok, created = pcall(MinimapButton.create)
        return ok and created ~= nil
    end
    Store.settings().minimap = false
    if button then button:Hide() end
    return true
end
