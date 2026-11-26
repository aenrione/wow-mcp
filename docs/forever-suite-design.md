# Forever Suite — fork design notes

Fork goal (Alfredo, 2026-11-25): turn wow-mcp into a full companion for WoW: Forever
(client 1.60.1, Interface 16001). This fork deliberately breaks two upstream
invariants: the server MAY read other addons' SavedVariables, and MAY write
request-slot addons into the game directory. Everything else (read-only gameplay,
no automation, no AH actions) stays.

## Phases

1. **P1 — Ingestion** (this branch): new dump sections from other addons' in-game
   globals, plus server-side parsing of other SavedVariables files; new read tools
   and CSV export.
2. **P2 — Inbound slots**: seer-style LoadOnDemand request slots so the server can
   ask the addon to set TomTom waypoints / focus the map, no reload needed.
3. **P3 — Wiki**: server-owned markdown wiki with MCP tools for min-max guidance.
4. **P4 — Realtime pixel channel** (later): port wow-seer Strip.lua + Go decoder.

## Recon facts (verified 2026-11-25, local installs)

### Upstream wow-mcp architecture
- Addon mutates global `WowMcpDB` only; client flushes on ReloadUI()/logout.
  `/mcp reload` → `Commands.lua:215-225` → `ReloadUI()`. Logout-start dump exists
  (`WowMcp.lua:25-31`, events PLAYER_CAMPING/PLAYER_QUITING).
- New dump section = file in `addon/WowMcp/Sections/` calling
  `ns.Snapshot.addSection(name, collect)` (or `addWindowSection`), listed in
  `WowMcp.toc`. Bound everything (QuestDb caps at 60, Auctions at 400). All values
  through `ns.Compat.sanitizeTree`. Addon files ≤ 350 lines (architecture_test.go:25).
- Go: `savedvars.Parse` (server/internal/savedvars/parser.go:32) parses any SV lua.
  `storage.Store` reads only WowMcp.lua (store.go:23); account discovery
  `accountOf()` store.go:62-73. Retry 4×/600ms on read.
- Tools: one entry in a `[]tool` group; generic constructors `accountTool` /
  `characterTool` / `sectionTool` (tools.go:94-150). Domain logic in
  `server/internal/game/<pkg>`; domain pkgs may NOT import os/io/fs/filepath/net/log
  (architecture_test.go:33-36); funcs ≤ 80 lines. Golden list in
  server/testdata/golden/tools_list.json — regenerate `go test -run Golden -update ./server`.
- In-game integrations precedent: QuestDb.lua reads `_G.LibQuestieDB`;
  Sections/Guide.lua reads `_G.GuidelimeDataChar` + `hooksecurefunc(registerGuide)`.
  OptionalDeps in toc guarantee load order.

### RXPGuides (paid v4.11.30) — per-char SV `RXPCData` (+`RXPCTrackingData`)
- `currentStep` (index), `currentStepId` (hash), `currentGuideName`,
  `currentGuideGroup`, `guideProgress["group|subgroup|name"] = {step, stepId}`,
  `stepSkip`, `questObjectivesCache[questID] = {{type,numRequired,numFulfilled,text,finished},...}`,
  `questNameCache`, `flightPaths`.
- In-game globals: `RXPGuides.SaveGuideProgress` (RXPGuides.lua:1412), guide key
  format `group|subgroup|name` (GuideLoader.lua:797-805). Parsed current guide with
  step element tables is in memory on the addon table (grep `addon.currentGuide` /
  `RXPCData` usage in /Applications/World of Warcraft/_forever_/Interface/AddOns/RXPGuides).
- `RXPCTrackingData` per level: `levels[N] = {deaths, mobs[zone]={xp,count},
  quests[zone][questID]=xp, timestamp}`.

### Questie (Forever build) — ALL char state in ACCOUNT file Questie.lua
- `QuestieConfig.char["Monjits Gpt"].complete` = `[questID]=true` (~165).
- `.char[...].journey` = `{Event="Quest", SubType="Accept|Complete|Abandon",
  Quest=id, Level=n, Timestamp=unix}` — the quest timeline.
- In-progress objectives NOT persisted by Questie; use RXP questObjectivesCache.

### Profiteer v0.12.8 — account SV `ProfiteerDB` (3.8 MB)
- `sets[realmName]` with: `prices[itemID]={min,ref,qty,n,time}`,
  `history[itemID]={{min,ref,qty,n,t,sold,dt},...}` (≤30 snaps, 30d),
  `inventory[charKey]={bags,bank,time}`, `myAuctions[charKey]={t,items={{name,n,ask,sold}}}`,
  `recipes[charKey][prof]={{name,id,rid,yield,reagents},...}`, `unlearned`,
  `ledger[YYYY-MM-DD][category]=copper` (loot/quests/vendorSales/vendorBuys/ahBuys/
  mailIn/mailOut/repairs/deposits/training), `worth[day][charKey]={s,e}` (gold curve),
  `chars`. Top level: `vendor[itemID]=copper`, `names[itemID]=name`, `sessions`,
  `lastScan` (staleness check). Realm key here: "CAMWOW US1 Realm 4 PvP" and "".

### TomTom v4.4.1
- API: `TomTom:AddWaypoint(uiMapID, x, y, opts)` with x,y in 0..1;
  opts {title, from, persistent, minimap, world, crazy, cleardistance, arrivaldistance}.
  Returns uid. Also `ClearAllWaypoints()`, `RemoveWaypoint(uid)`,
  `AddWaypointToCurrentZone(x,y,desc)` (0..100). Slash `/way`.
- SV `TomTomWaypointsM.profiles[charProfile]` persistent waypoints.

### OpenMap v0.8.10
- Public read API global `OpenMapAPI` (API.lua): `ShowWorld(u,v,zoom)` **focuses the
  stitched map**, `WorldFromMap(uiMapID,x,y)→u,v`, `MapFromWorld`, `GetView`,
  `IsStitched`, callbacks via CallbackHandler. No public pin API (pins internal).
- SV: `OpenMapDB.worldmap.pins.list = {{id,map,x,y,note,color,at},...}`,
  `flightTimes`; per-char `OpenMapCharDB.worldmap.flightsKnown`, `.entered`.

### wow-seer inbound slots (port target, repo ~/Developer/wow-seer)
- N LoadOnDemand addons `Seer_S0001..`: toc (Interface 16001, LoadOnDemand: 1) +
  body.lua = `SeerInbox = {["t"]=unix,["reqs"]={...},["got"]={...}}`.
- Server rewrites ALL slot bodies atomically (temp+rename, Windows EPERM retry ≤10)
  at most every 30 s or when pending changes (game.ts:90-169, shared.ts:136-150).
- Addon Poll() every 10 s (alive) / 60 s (idle), never in combat:
  `SeerInbox=nil; pcall(C_AddOns.EnableAddOn,n); pcall(C_AddOns.LoadAddOn,n);
  local inbox=SeerInbox; SeerInbox=nil` — each slot body runs once per UI session;
  1000 slots ≈ 2.8 h at 10 s poll. Request ids monotonic, < 2^31 (32-bit Lua ints).
  Freshness `|time()-inbox.t| < 180`. Acks idempotent (re-ack already-done ids).
- Lua literal serializer: strings as decimal escapes `"\065..."`; floats python-repr.

### wow-seer in-game integrations worth copying (Integrations.lua)
- `Try()` pcall wrapper; `Clean()` strips |c|T|A escapes; guard `issecretvalue()`.
- Auctionator: `Auctionator.API.v1.GetAuctionPriceByItemLink/ByItemID(callerID,...)`,
  `GetAuctionAge*`, `GetDisenchantPrice*`.
- Pawn: `PawnIsReady()`, `PawnGetItemData(link).Values`, `PawnIsItemAnUpgrade`.
- What's Training: `WT_API.IsReady/GetSpells`. DataStore: `DataStore:GetCharacters/...`.
- Forever Leveling BiS: `ForeverLevelingBiS_API` (version ≥ 2) `GetGain(link)/GetWeights()/GetSpec()`.
- Push invalidation: subscribe to each addon's callback registry when offered.
- Installed locally today: Questie(DB), RXPGuides, Profiteer, ProfitHUB_Cell, TomTom,
  OpenMap(+MiniMap), WhatsTraining, ForeverWoWBiS, RareScanner, GoldSnap, Lorewalker.
  NOT installed: Auctionator, Pawn, DataStore, Polestar (code them behind Try()).

## Fork conventions
- New server files keep upstream patterns and architecture tests green.
- Tool groups for new features live in NEW files in package mcpserver
  (`tools_extras.go`, `tools_remote.go`, `tools_wiki.go`) exposing
  `(s *service) extrasTools() / remoteTools() / wikiTools() []tool`;
  wiring into `tools()` + instructions happens in one place afterwards.
- Everything the server writes goes next to history (own dir), EXCEPT slot bodies
  which intentionally write `Interface/AddOns/WowMcp_S*/body.lua` (document in
  docs/TOS_COMPLIANCE.md as a fork deviation; still zero gameplay automation —
  the addon only ever sets waypoints/map focus via other addons' public APIs).
- Game dir on this Mac: /Applications/World of Warcraft (client folder `_forever_`);
  on Windows (ssh alfre@windows): C:\Program Files (x86)\World of Warcraft.
  Account 76093156#1, char Monjits-Gpt, realm dir 91.
