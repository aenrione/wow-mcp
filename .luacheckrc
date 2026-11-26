-- luacheck config for the WowMcp addon (the WoW client uses Lua 5.1).
-- Run from the repository root: luacheck addon
std = "lua51"
max_line_length = 160
codes = true
include_files = { "addon/WowMcp/*.lua", "addon/WowMcp/Sections/*.lua" }

-- Globals written by the addon: SavedVariables and the slash command.
globals = {
    "WowMcpDB",
    "SLASH_WOWMCP1",
    "SLASH_WOWMCP2",
    "SlashCmdList",
    "BINDING_HEADER_WOWMCP",
    "BINDING_NAME_WOWMCP_SAVE",
    "WowMcp_SaveAndReload",
}

-- WoW client globals that the addon only reads (list derived from the addon code).
read_globals = {
    -- C_* namespaces and Enum
    "C_AddOns", "C_ClassTalents", "C_Container", "C_CurrencyInfo", "C_Map", "C_QuestLog", "C_Reputation", "C_SkillInfo",
    "C_Spell", "C_SpellBook", "C_TaxiMap", "C_Timer", "C_TooltipInfo", "C_TradeSkillUI", "C_Traits", "C_UnitAuras",
    "Enum", "TooltipDataProcessor",
    -- frames and templates
    "BackdropTemplateMixin", "CreateFrame", "InterfaceOptionsFrame_OpenToCategory", "InterfaceOptions_AddCategory", "Settings", "DEFAULT_CHAT_FRAME", "GameTooltip", "InspectFrame", "Minimap", "UIParent",
    -- constants
    "ATTACHMENTS_MAX_RECEIVE", "MAX_ACCOUNT_MACROS", "MAX_QUESTLOG_QUESTS", "NUM_BAG_SLOTS", "NUM_BANKBAGSLOTS",
    "NUM_PET_ACTION_SLOTS", "NUM_TOTAL_EQUIPPED_BAG_SLOTS",
    -- classic and mainline API functions
    "GetBuildInfo", "GetContainerItemInfo", "hooksecurefunc", "GetCursorPosition", "GetFactionInfo", "GetMacroInfo", "GetMerchantNumItems", "GetMoney",
    "GetNumFactions", "GetNumMacros", "GetNumQuestLeaderBoards", "GetNumQuestLogEntries", "GetNumSkillLines",
    "GetNumSpellTabs", "GetNumTalentTabs", "GetNumTalents", "GetNumTrainerServices", "GetPetActionInfo",
    "GetProfessionInfo", "GetProfessions", "GetQuestLogLeaderBoard", "GetQuestLogSelection", "GetQuestLogTitle",
    "GetRealmName", "GetSkillLineInfo", "GetSpellBookItemName", "GetSpellTabInfo", "GetTalentInfo",
    "GetTalentTabInfo", "GetTaxiMapID", "GetTime", "GetTrainerServiceCost", "GetTrainerServiceInfo", "InCombatLockdown",
    "GetTrainerServiceLevelReq", "NumTaxiNodes", "ReloadUI", "SelectQuestLogEntry", "TaxiNodeCost",
    "TaxiNodeGetType", "TaxiNodeName", "UnitClass", "UnitCreatureFamily", "UnitExists", "UnitFactionGroup",
    "UnitIsDead", "UnitLevel", "UnitName", "UnitRace", "UnitXP", "UnitXPMax", "date", "debugprofilestop",
    "issecretvalue", "time",
    -- achievements (Legacy challenges)
    "GetAchievementInfo", "GetCategoryInfo", "GetCategoryList", "GetCategoryNumAchievements",
}
