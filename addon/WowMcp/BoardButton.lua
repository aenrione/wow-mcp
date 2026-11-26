-- SPDX-License-Identifier: GPL-3.0-or-later
-- Copyright (C) 2026 Piekasz (GitHub: Piekario). Author attribution must be kept: see NOTICE.

-- "Create board button" helper: makes a character macro named WowMcpBoard whose body is /mcp board, so the player can
-- drag it from the macro list onto any action bar. Toggling a frame is not a protected action, so a plain macro runs
-- out of combat and never taints; no secure action template is used. Shared by the /mcp boardbutton command and the
-- options page button. All in pcall and behind availability checks: a client without CreateMacro, or being in combat,
-- only reports why and changes nothing.

local _, ns = ...

local BoardButton = {}
ns.BoardButton = BoardButton

BoardButton.NAME = "WowMcpBoard"
BoardButton.ICON = "Interface\\Icons\\INV_Misc_Note_01" -- texture path (minimap button uses this too)
BoardButton.ICON_NAME = "INV_Misc_Note_01"             -- icon name CreateMacro expects
BoardButton.BODY = "/mcp board"

local function onCursor(index)
    if type(PickupMacro) == "function" then pcall(PickupMacro, index) end
end

-- Creates the macro (once) and puts it on the cursor to drop on an action bar. Returns ok, message; never raises.
function BoardButton.create()
    if type(CreateMacro) ~= "function" or type(GetMacroIndexByName) ~= "function" then
        return false, "This client cannot create a macro from an addon. Open /macro and make one with the body " .. BoardButton.BODY .. "."
    end
    if type(InCombatLockdown) == "function" and InCombatLockdown() then
        return false, "Can't make a macro in combat. Try /mcp boardbutton again out of combat."
    end
    local ok, index = pcall(GetMacroIndexByName, BoardButton.NAME)
    if ok and type(index) == "number" and index > 0 then
        onCursor(index)
        return true, "The " .. BoardButton.NAME .. " macro already exists; drag it onto your action bar (it runs " .. BoardButton.BODY .. ")."
    end
    local created
    ok, created = pcall(CreateMacro, BoardButton.NAME, BoardButton.ICON_NAME, BoardButton.BODY, false)
    if not ok or not created then
        return false, "Could not create the macro. The character macro list may be full; make room in /macro and try again."
    end
    onCursor(created)
    return true, "Created the " .. BoardButton.NAME .. " macro; drag it from /macro onto your action bar (it runs " .. BoardButton.BODY .. ")."
end
