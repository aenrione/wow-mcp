-- SPDX-License-Identifier: GPL-3.0-or-later
-- Copyright (C) 2026 Piekasz (GitHub: Piekario). Author attribution must be kept: see NOTICE.

-- Key binding "Save character state" (Bindings.xml): the same as the panel's Save button, on a key the player
-- picks in the game's Key Bindings. No key is bound by default.

local _, ns = ...

BINDING_HEADER_WOWMCP = "WowMcp"
BINDING_NAME_WOWMCP_SAVE = "Save character state (dump and /reload)"
BINDING_NAME_WOWMCP_TOGGLEBOARD = "Toggle the agent board"

function WowMcp_SaveAndReload()
    ns.Panel.saveAndReload("keybind")
end

function WowMcp_ToggleBoard()
    if ns.Board then ns.Board.toggle() end
end
