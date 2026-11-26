-- SPDX-License-Identifier: GPL-3.0-or-later
-- Copyright (C) 2026 Piekasz (GitHub: Piekario). Author attribution must be kept: see NOTICE.

-- Slash commands /mcp and /wmcp. Each command is an entry in the COMMANDS table;
-- an unknown command (and bare /mcp) takes a dump without /reload.

local _, ns = ...
local Store, Snapshot, Panel, Played, chat = ns.Store, ns.Snapshot, ns.Panel, ns.Played, ns.chat

local HELP = {
    "/mcp reload - dump and save to disk (reloads the UI)",
    "/mcp - dump only; it reaches the disk on the next /reload or logout",
    "/mcp status - this character's last dump, dump time, disabled sections and errors",
    "/mcp panel - show or hide the panel with the Save button",
    "/mcp board - show or hide the agent board (notes, tasks and routes an MCP agent posted); item and coordinate links are clickable",
    "/mcp played on|off - whether to request played time after login (for progress statistics)",
    "/mcp note <text> - note for the assistant in the dump (e.g. what you plan today); bare /mcp note removes it",
    "/mcp quest <id or name> - where a quest starts and ends, from the Questie database you have installed; the answer also goes into the dump."
        .. " With TomTom installed, click a coordinate to set a waypoint",
    "/mcp off <section> - stop collecting this section; bare /mcp off lists disabled sections and all section names",
    "/mcp on <section> - collect the section again",
    "/mcp minimap on|off - minimap button (left click: panel; on by default)",
    "/mcp compact on|off - one-line panel: dump age and Save button (off by default)",
    "/mcp ahtip on|off - auction house price in the item tooltip (on by default)",
    "/mcp options - open the WowMcp page in the game's options (the same switches as the commands above)",
    "/mcp probe - diagnostics: list of client API functions (spells, items, maps, achievements...), the events the client accepted"
        .. " and the client's Legacy API names",
    "Windows saved automatically when opened: trainer and vendor (last 10), bank, flights, professions, mail,"
        .. " auction house (search results), Inspect, stable, NPC quest (offer and turn-in),"
        .. " buyback list (at the vendor), friends and guild (from client memory once the window is open; the addon requests nothing from the server)",
    "Deaths, your own loot and experience (kills, quests) since the last save are recorded automatically."
        .. " The panel highlights Save after new window data, but never saves by itself.",
}

local function reportErrors(errors)
    for _, err in ipairs(errors) do
        chat("|cffff5555error|r " .. err)
    end
end

local function showHelp()
    for _, line in ipairs(HELP) do chat(line) end
end

-- Settings take the first word, case-insensitive.
local function firstWord(rest)
    return (rest or ""):lower():match("^(%S*)")
end

local function setPlayedTracking(rest)
    if firstWord(rest) == "off" then
        Played.setTracking(false)
        chat("Played time will no longer be requested. Progress statistics will be computed from dump times.")
    else
        Played.setTracking(true)
        chat("Played time enabled: the addon requests it after login.")
    end
end

local function setAuctionTooltip(rest)
    local enabled = firstWord(rest) ~= "off"
    ns.AuctionTooltip.setEnabled(enabled)
    if enabled then
        chat("The auction house price will show in the item tooltip (when the addon knows it). Turn off: /mcp ahtip off")
    else
        chat("The auction house price will no longer show in the item tooltip. Turn on: /mcp ahtip on")
    end
end

local function setMinimapButton(rest)
    local enabled = firstWord(rest) ~= "off"
    local ok = ns.MinimapButton.setEnabled(enabled)
    if not enabled then
        chat("Minimap button hidden. Turn on: /mcp minimap on")
    elseif ok then
        chat("Minimap button enabled (left click: panel). Turn off: /mcp minimap off")
    else
        chat("Could not create the minimap button. The panel still works: /mcp panel")
    end
end

local function setCompactPanel(rest)
    local enabled = firstWord(rest) ~= "off"
    Panel.setCompact(enabled)
    if enabled then
        chat("Panel in compact mode (one line). Normal look: /mcp compact off")
    else
        chat("Panel in normal look. Compact mode: /mcp compact on")
    end
end

local function setNote(rest)
    if Snapshot.isDisabled("note") then
        chat("The note section is disabled. Enable it: /mcp on note")
        return
    end
    local note = ns.Note.set(rest)
    if note then
        chat("Note saved: " .. note.text)
    else
        chat("Note removed.")
    end
end

local function lookUpQuest(rest)
    if (rest or "") == "" then
        chat("Usage: /mcp quest <quest ID, exact quest name or NPC name>")
        return
    end
    local entry = ns.QuestLookup.run(rest)
    if not entry then
        chat("No quest database found. Install Questie (with QuestieDB) to use /mcp quest.")
        return
    end
    local lines, coordinates = ns.QuestLookup.lines(entry, ns.Waypoints.chatText)
    for _, line in ipairs(lines) do chat(line) end
    if coordinates then ns.Waypoints.suggestTomTom() end
    Panel.refresh()
end

local function listSections()
    local disabled = Snapshot.disabledNames()
    chat("Disabled sections: " .. (#disabled > 0 and table.concat(disabled, ", ") or "none"))
    chat("All sections: " .. table.concat(Snapshot.sectionNames(), ", "))
end

-- Section name from the player's input, or nil (with a message saying why not).
local function sectionFromInput(rest)
    local name = Snapshot.resolveName(firstWord(rest))
    if not name then
        chat("Unknown section: " .. firstWord(rest) .. ". Type /mcp off to see the names.")
        return nil
    end
    return name
end

local function disableSection(rest)
    if firstWord(rest) == "" then
        listSections()
        return
    end
    local name = sectionFromInput(rest)
    if not name then return end
    if not Snapshot.canDisable(name) then
        chat("Section " .. name .. " cannot be disabled.")
        return
    end
    Snapshot.setEnabled(name, false)
    Panel.refresh()
    chat("Section " .. name .. " disabled: it disappears from the dump (including this section's saved data). Enable it: /mcp on " .. name)
end

local function enableSection(rest)
    if firstWord(rest) == "" then
        chat("Usage: /mcp on <section>. Section list: /mcp off")
        return
    end
    local name = sectionFromInput(rest)
    if not name then return end
    Snapshot.setEnabled(name, true)
    chat("Section " .. name .. " enabled. It will appear in the next dump.")
end

local function auctionPriceCount(snapshot)
    local prices = 0
    if type(snapshot.auctions) == "table" and type(snapshot.auctions.prices) == "table" then
        for _ in pairs(snapshot.auctions.prices) do prices = prices + 1 end
    end
    return prices
end

local function reportTimings(timings)
    if type(timings) ~= "table" or type(timings.totalMs) ~= "number" then return end
    local slow = {}
    for _, entry in ipairs(timings.slowest or {}) do
        slow[#slow + 1] = entry.name .. " " .. entry.ms .. " ms"
    end
    local line = "Dump collected in " .. timings.totalMs .. " ms"
    if #slow > 0 then line = line .. ". Slowest: " .. table.concat(slow, ", ") end
    if timings.totalMs > ns.Timing.WARNING_TOTAL_MS then
        chat("|cffffcc00Warning:|r " .. line .. ". The dump is heavy (over " .. ns.Timing.WARNING_TOTAL_MS
            .. " ms); disable unneeded sections with /mcp off.")
    else
        chat(line)
    end
end

-- Dump state of the character being played (not the account's last one).
local function showStatus()
    local snapshot = Store.savedCurrentCharacter()
    if not snapshot or not snapshot.dumpedAtText then
        local lastKey = Store.lastSaved()
        chat("No dump for character " .. Store.characterKey() .. ". Type /mcp reload."
            .. (lastKey and (" Last dump on the account: " .. lastKey .. ".") or ""))
        return
    end
    chat("Version " .. ns.ADDON_VERSION .. ". Last dump: " .. Store.characterKey() .. ", " .. snapshot.dumpedAtText
        .. (Snapshot.isUnsaved() and " (not on disk yet)" or ""))
    if ns.Periods.continues(snapshot) then
        chat("At the last logout the client no longer had character data, so the dump from "
            .. snapshot.dumpedAtText .. " was kept. Fresh save: /mcp reload")
    end
    local prices = auctionPriceCount(snapshot)
    if prices > 0 then chat("Auction house prices: " .. prices .. " items.") end
    if type(snapshot.disabledSections) == "table" and #snapshot.disabledSections > 0 then
        chat("Disabled sections: " .. table.concat(snapshot.disabledSections, ", "))
    end
    reportTimings(snapshot.timings)
    reportErrors(snapshot.errors or {})
end

-- Manual dump. With reload it is saved to disk immediately via /reload.
local function dumpManually(reload)
    local snapshot = Snapshot.dump("manual")
    local questCount = snapshot.quests and #snapshot.quests or 0
    local errorCount = #snapshot.errors
    chat(("Dump ready: %d quests, section errors: %d."):format(questCount, errorCount))
    reportErrors(snapshot.errors)

    if reload then
        ReloadUI()
    else
        Snapshot.markUnsaved()
        Panel.refresh()
        chat("Data reaches the disk on /reload or logout. Shortcut: /mcp reload or the Save button on the panel")
    end
end

local function probeAndReload()
    ns.LegacyDiscovery.request()
    Snapshot.capture("probe")
    dumpManually(true)
end

local COMMANDS = {
    help = showHelp,
    ["?"] = showHelp,
    panel = Panel.toggle,
    board = function() ns.Board.toggle() end,
    played = setPlayedTracking,
    ahtip = setAuctionTooltip,
    minimap = setMinimapButton,
    compact = setCompactPanel,
    note = setNote,
    quest = lookUpQuest,
    off = disableSection,
    on = enableSection,
    status = showStatus,
    options = function() ns.Options.open() end,
    probe = probeAndReload,
    reload = function() dumpManually(true) end,
}

SLASH_WOWMCP1 = "/mcp"
SLASH_WOWMCP2 = "/wmcp"

SlashCmdList["WOWMCP"] = function(input)
    -- The command is case-insensitive; the rest (e.g. note text) stays as typed.
    local command, rest = (input or ""):match("^%s*(%S*)%s*(.-)%s*$")
    local run = COMMANDS[command:lower()]
    if run then
        run(rest)
    else
        dumpManually(false)
    end
end
