// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 Piekasz (GitHub: Piekario). Author attribution must be kept: see NOTICE.

package mcpserver

// Forever Suite fork, phase 1: tools over OTHER addons' SavedVariables files (Questie,
// Profiteer, TomTom, OpenMap, RXPGuides) read through the Extras port, plus a CSV export
// written to the server's own export directory (never the game directory).
//
// Wiring (done in one place later): give the service an Extras implementation
// (storage.ExtrasStore composes the snapshot Store) and an export directory that main.go
// has checked with storage.RequireOutside, then append s.extrasTools(extras, exportDir)
// in tools().

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"wowmcp/internal/game/economy"
	"wowmcp/internal/game/mappins"
	"wowmcp/internal/game/pricecheck"
	"wowmcp/internal/game/queststory"
	"wowmcp/internal/game/rxp"
	"wowmcp/internal/snapshot"
)

// Extras is the port to other addons' SavedVariables files. Account reads a file next to
// WowMcp.lua; Character reads one from the character's own SavedVariables directory
// (character is the snapshot's "Name-Realm" key). Both return every global the file
// defines plus its modification time, and a NotFound()-marked error when it is missing.
// Implementations never write anything and never read the .lua.bak copies.
type Extras interface {
	Account(file string) (map[string]any, time.Time, error)
	Character(character, file string) (map[string]any, time.Time, error)
}

// fileNotFound matches storage.NotFoundError without importing the storage package.
type fileNotFound interface{ NotFound() bool }

func isFileNotFound(err error) bool {
	var nf fileNotFound
	return errors.As(err, &nf)
}

var errExtrasOff = errors.New("reading other addons' SavedVariables is not wired: the server was started without the extras reader")

// extraFile is one loaded extras file: the global's table (nil when the global is absent)
// and the file's freshness. found is false when the file does not exist.
type extraFile struct {
	table map[string]any
	mod   time.Time
	found bool
}

// accountGlobal loads one global from an account-level extras file; a missing file is
// found=false, not an error.
func accountGlobal(x Extras, file, global string) (extraFile, error) {
	vars, mod, err := x.Account(file)
	if err != nil {
		if isFileNotFound(err) {
			return extraFile{}, nil
		}
		return extraFile{}, err
	}
	return extraFile{table: snapshot.AsMap(vars[global]), mod: mod, found: true}, nil
}

// characterGlobals loads per-character globals from one extras file; a missing file or
// character directory is found=false, not an error.
func characterGlobals(x Extras, character, file string, globals ...string) ([]map[string]any, extraFile, error) {
	vars, mod, err := x.Character(character, file)
	if err != nil {
		if isFileNotFound(err) {
			return make([]map[string]any, len(globals)), extraFile{}, nil
		}
		return nil, extraFile{}, err
	}
	tables := make([]map[string]any, len(globals))
	for i, name := range globals {
		tables[i] = snapshot.AsMap(vars[name])
	}
	return tables, extraFile{mod: mod, found: true}, nil
}

// requireFound turns a missing extras file into a readable error naming the addon.
func requireFound(f extraFile, file, addon string) error {
	if !f.found {
		return fmt.Errorf("no %s for this account: the %s addon is not installed or has never saved (log out or /reload in game once)", file, addon)
	}
	if f.table == nil {
		return fmt.Errorf("%s exists but holds no data yet: log out or /reload in game", file)
	}
	return nil
}

// extraMeta describes one source file's freshness for the answer.
func (s *service) extraMeta(file string, f extraFile) map[string]any {
	meta := map[string]any{"file": file}
	if !f.found {
		meta["found"] = false
		return meta
	}
	meta["modified"] = f.mod.Format("2006-01-02 15:04:05")
	if s.Freshness.Now != nil {
		meta["ageMinutes"] = int64(s.Freshness.Age(f.mod.Unix()).Minutes())
	}
	return meta
}

// extrasNote ends the description of every extras tool.
const extrasNote = " The file refreshes when the game saves SavedVariables (/reload or logout); the source field says how old it is. Fork feature: the server reads another addon's file, still read-only."

// extrasTool builds one extras tool; the shared handler guards the unwired port.
func extrasTool[In any](s *service, x Extras, name, title, description string,
	handle func(args In) (map[string]any, error)) tool {

	return tool{name: name, title: title, description: description, args: argNames[In](), add: func(server *mcp.Server, def *mcp.Tool) {
		mcp.AddTool(server, def, func(ctx context.Context, req *mcp.CallToolRequest, args In) (*mcp.CallToolResult, any, error) {
			if x == nil {
				return nil, nil, errExtrasOff
			}
			out, err := handle(args)
			if err != nil {
				return nil, nil, err
			}
			return textResult(out)
		})
	}}
}

// extrasTools are the Forever Suite phase-1 tools. x may be nil (the tools then explain
// they are not wired); exportDir is where wow_export_csv writes, checked by main to lie
// outside the game directory.
func (s *service) extrasTools(x Extras, exportDir string) []tool {
	return []tool{
		s.questStoryTool(x),
		s.economyTool(x),
		s.priceTool(x),
		s.mapPinsTool(x),
		s.rxpTool(x),
		s.exportCSVTool(x, exportDir),
	}
}

func (s *service) questStoryTool(x Extras) tool {
	return extrasTool(s, x, "wow_quest_story", "Quest history (Questie)",
		"The character's quest history from Questie's own SavedVariables (QuestieConfig): how many quests are completed, the completed quest ids (up to 500) and the journey timeline (quest accepted, completed or abandoned, with the level and time). Filters: limit (journey entries, default 100, newest kept), since (Unix time) and level."+extrasNote,
		func(args queststory.Query) (map[string]any, error) {
			character, _, err := s.load(args.Character)
			if err != nil {
				return nil, err
			}
			f, err := accountGlobal(x, "Questie.lua", "QuestieConfig")
			if err != nil {
				return nil, err
			}
			if err := requireFound(f, "Questie.lua", "Questie"); err != nil {
				return nil, err
			}
			out, err := queststory.View(f.table, character, args)
			if err != nil {
				return nil, err
			}
			out["source"] = s.extraMeta("Questie.lua", f)
			return out, nil
		})
}

func (s *service) economyTool(x Extras) tool {
	return extrasTool(s, x, "wow_economy", "Account economy (Profiteer)",
		"The account economy from Profiteer's SavedVariables (ProfiteerDB): per-realm summary (characters, prices known, last auction scan), the gold ledger by day and category (loot, quests, vendor sales and buys, auction buys, mail, repairs, deposits, training), each character's gold curve, own auctions, scanned inventories (top items by value) and unlearned recipes (up to 50). days (default 14) is the ledger and gold-curve window counted back from the newest ledger day; character narrows the per-character parts."+extrasNote,
		func(args economy.Query) (map[string]any, error) {
			if args.Character != "" {
				character, _, err := s.load(args.Character)
				if err != nil {
					return nil, err
				}
				args.Character = character
			}
			f, err := accountGlobal(x, "Profiteer.lua", "ProfiteerDB")
			if err != nil {
				return nil, err
			}
			if err := requireFound(f, "Profiteer.lua", "Profiteer"); err != nil {
				return nil, err
			}
			out := economy.View(f.table, args)
			out["source"] = s.extraMeta("Profiteer.lua", f)
			return out, nil
		})
}

func (s *service) priceTool(x Extras) tool {
	return extrasTool(s, x, "wow_price", "Item price check (Profiteer)",
		"What an item is worth from Profiteer's scans: the vendor price, the last scanned auction price per realm (min, reference, quantity, listings, when), up to 30 price-history snapshots and an estimated sold-per-day rate (a rough estimate: sales cannot be told apart from cancellations). Give the item as a name, a name fragment or a numeric id; an ambiguous fragment returns up to 10 matches to choose from."+extrasNote,
		func(args pricecheck.Query) (map[string]any, error) {
			f, err := accountGlobal(x, "Profiteer.lua", "ProfiteerDB")
			if err != nil {
				return nil, err
			}
			if err := requireFound(f, "Profiteer.lua", "Profiteer"); err != nil {
				return nil, err
			}
			out, err := pricecheck.View(f.table, args)
			if err != nil {
				return nil, err
			}
			out["source"] = s.extraMeta("Profiteer.lua", f)
			return out, nil
		})
}

func (s *service) mapPinsTool(x Extras) tool {
	return extrasTool(s, x, "wow_map_pins", "Map pins and waypoints",
		"Every pin the player saved on the map, unified from OpenMap (OpenMapDB pins) and TomTom (persistent waypoints): source, uiMapID, x/y (0..1), title or note and when it was saved. Also the flight points OpenMap has seen the character learn (flightsKnown). Addons that never saved are simply skipped."+extrasNote,
		func(args mappins.Query) (map[string]any, error) {
			character, _, err := s.load(args.Character)
			if err != nil {
				return nil, err
			}
			openMap, err := accountGlobal(x, "OpenMap.lua", "OpenMapDB")
			if err != nil {
				return nil, err
			}
			tomtom, err := accountGlobal(x, "TomTom.lua", "TomTomWaypointsM")
			if err != nil {
				return nil, err
			}
			charTables, charFile, err := characterGlobals(x, character, "OpenMap.lua", "OpenMapCharDB")
			if err != nil {
				return nil, err
			}
			out := mappins.View(openMap.table, tomtom.table, charTables[0], character)
			out["character"] = character
			out["source"] = map[string]any{
				"OpenMap.lua":             s.extraMeta("OpenMap.lua", openMap),
				"TomTom.lua":              s.extraMeta("TomTom.lua", tomtom),
				"OpenMap.lua (character)": s.extraMeta("OpenMap.lua", charFile),
			}
			return out, nil
		})
}

func (s *service) rxpTool(x Extras) tool {
	return extrasTool(s, x, "wow_rxp", "Leveling guide state (RXPGuides)",
		"The character's RXPGuides state from its own SavedVariables (RXPCData and RXPCTrackingData): the current guide, group and step, the saved position in every guide (up to 40), the cached quest objective state (quest id, name, each objective's progress) and a per-level tracking summary of the last 6 levels (deaths, mob kills and XP by zone, quest XP)."+extrasNote,
		func(args rxp.Query) (map[string]any, error) {
			character, _, err := s.load(args.Character)
			if err != nil {
				return nil, err
			}
			tables, f, err := characterGlobals(x, character, "RXPGuides.lua", "RXPCData", "RXPCTrackingData")
			if err != nil {
				return nil, err
			}
			if !f.found {
				return nil, fmt.Errorf("no RXPGuides.lua for character %s: the RXPGuides addon is not installed or has never saved (log out or /reload in game once)", character)
			}
			out := rxp.View(tables[0], tables[1], character)
			out["source"] = s.extraMeta("RXPGuides.lua", f)
			return out, nil
		})
}

type exportCSVArgs struct {
	Dataset   string `json:"dataset" jsonschema:"what to export: prices (last scanned auction prices), ledger (the gold ledger by day and category), inventory (scanned bags and bank with values) or history (every price-history snapshot)"`
	Character string `json:"character,omitempty" jsonschema:"only this character's rows (inventory dataset); empty = all characters"`
}

func (s *service) exportCSVTool(x Extras, exportDir string) tool {
	t := extrasTool(s, x, "wow_export_csv", "Export economy data as CSV",
		"Writes one dataset from Profiteer's SavedVariables as a CSV file in the server's own export directory (never the game directory) and returns the absolute path and row count. dataset: prices, ledger, inventory or history; character narrows the inventory dataset. The file <dataset>.csv is overwritten on every call.",
		func(args exportCSVArgs) (map[string]any, error) {
			return s.exportCSV(x, exportDir, args)
		})
	t.writes = true
	return t
}

func (s *service) exportCSV(x Extras, exportDir string, args exportCSVArgs) (map[string]any, error) {
	if exportDir == "" {
		return nil, errors.New("CSV export is disabled: the server has no export directory set")
	}
	if args.Character != "" {
		character, _, err := s.load(args.Character)
		if err != nil {
			return nil, err
		}
		args.Character = character
	}
	f, err := accountGlobal(x, "Profiteer.lua", "ProfiteerDB")
	if err != nil {
		return nil, err
	}
	if err := requireFound(f, "Profiteer.lua", "Profiteer"); err != nil {
		return nil, err
	}
	var rows [][]string
	switch args.Dataset {
	case "prices":
		rows = economy.PricesRows(f.table)
	case "ledger":
		rows = economy.LedgerRows(f.table)
	case "inventory":
		rows = economy.InventoryRows(f.table, args.Character)
	case "history":
		rows = economy.HistoryRows(f.table)
	default:
		return nil, fmt.Errorf("unknown dataset %q: use prices, ledger, inventory or history", args.Dataset)
	}
	path, err := writeCSV(exportDir, args.Dataset+".csv", rows)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"file": path, "dataset": args.Dataset, "rows": len(rows) - 1, "columns": rows[0],
		"source": s.extraMeta("Profiteer.lua", f),
	}, nil
}

// writeCSV writes the rows to <dir>/<name> and returns the absolute path.
func writeCSV(dir, name string, rows [][]string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("cannot create the export directory %q: %w", dir, err)
	}
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	if err := w.WriteAll(rows); err != nil {
		return "", err
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return "", fmt.Errorf("cannot write %q: %w", path, err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return path, nil
	}
	return abs, nil
}
