// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 Piekasz (GitHub: Piekario). Author attribution must be kept: see NOTICE.

// wow-mcp: a local MCP server that reads the character state saved by the
// WowMcp addon in WoW SavedVariables and exposes it as tools.
//
// The server never touches the game process and only reads one text file from
// the WTF directory of the game. The only thing it writes is the snapshot history
// in its own directory (by default "history" next to wow-mcp.exe, or in <user config>/wow-mcp
// when the exe directory is not writable), from which it computes changes between snapshots.
//
// Launch (done by the MCP client, e.g. your MCP client):
//
//	wow-mcp.exe --wow-dir "<path to World of Warcraft>"
//
// This file is only the composition root: flags, wiring dependencies, --check and --version.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"wowmcp/internal/ledger"
	"wowmcp/internal/mcpserver"
	"wowmcp/internal/slots"
	"wowmcp/internal/snapshot"
	"wowmcp/internal/storage"
	"wowmcp/internal/strip"
	"wowmcp/internal/wiki"
)

const (
	serverName    = "wow-mcp"
	serverVersion = "0.20.0"
	watchInterval = 5 * time.Second
)

func main() {
	wowDir := flag.String("wow-dir", os.Getenv("WOW_DIR"),
		`game directory, e.g. "<path to World of Warcraft>" (can also be set with the WOW_DIR environment variable); every client folder in it (_retail_, _classic_era_, _forever_...) is searched and the newest dump wins`)
	client := flag.String("client", os.Getenv("WOW_CLIENT"),
		`read only this client folder of the game directory, e.g. "_classic_era_" or "forever" (can also be set with WOW_CLIENT); empty = all`)
	account := flag.String("account", os.Getenv("WOW_ACCOUNT"),
		`pin one account folder under WTF/Account (e.g. "MYACCOUNT"; can also be set with WOW_ACCOUNT); empty = the account whose dump was modified most recently, and wow_status / wow_doctor name it and the others`)
	file := flag.String("file", "",
		"full path to WowMcp.lua; skips searching the game directory")
	check := flag.Bool("check", false,
		"check that the dump file can be found and read, print a summary and exit")
	historyDir := flag.String("history-dir", defaultHistoryDir(),
		`snapshot history directory (server-only, never inside the game directory; by default next to the exe, or in <user config>/wow-mcp when the exe directory is not writable; a relative path is resolved from that server directory); "" disables history`)
	historyKeep := flag.Int("history-keep", 50, "how many of the latest snapshots to keep per character")
	historyMaxMB := flag.Int("history-max-mb", 200,
		"size limit of one character's history in MB (oldest snapshots go first, the newest stays); 0 = no limit")
	historyCompressDays := flag.Int("history-compress-days", storage.DefaultCompressDays,
		"snapshots older than this many days are gzip-compressed (reading works as before); a negative value disables compression")
	logPath := flag.String("log", "",
		"file the server appends errors and startup information to (a relative path is resolved from the server directory: the exe directory, or <user config>/wow-mcp when it is not writable; never inside the game directory)")
	goalsFile := flag.String("goals-file", "",
		"goals file (wow_goal_set); defaults to goals.json next to the history directory (a relative path is resolved from the server directory; never inside the game directory). The server also reads an optional weights.json next to it")
	maxResponseKB := flag.Int("max-response-kb", mcpserver.DefaultMaxResponseKB,
		"limit of one tool answer in KB: a bigger answer is replaced by a compact envelope with a hint on how to ask for less; 0 = no limit")
	staleMinutes := flag.Int("stale-minutes", 120, "after how many minutes tools warn that the snapshot is stale; 0 disables")
	fv := registerForeverFlags()
	version := flag.Bool("version", false, "print the version and exit")
	flag.Parse()

	if *version {
		fmt.Println(serverName, serverVersion)
		return
	}

	if *fv.installSlots > 0 {
		runInstallSlots(*fv.addonsDir, *fv.installSlots)
		return
	}

	*historyDir = storage.ResolveBeside(*historyDir, ownDir())
	goalsPath := resolveGoalsFile(*goalsFile, *historyDir)
	*fv.wikiDir = storage.ResolveBeside(*fv.wikiDir, ownDir())
	*fv.exportDir = storage.ResolveBeside(*fv.exportDir, ownDir())
	// The server writes nothing in the game directory: the one given with --wow-dir and the one --file points into.
	gameDirs := []string{*wowDir, storage.GameDirOf(*file)}
	closeLog, err := setupLogging(*logPath, *historyDir, gameDirs)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ERROR:", err)
		os.Exit(1)
	}
	defer closeLog()

	fresh := snapshot.Freshness{Now: time.Now, StaleAfter: time.Duration(*staleMinutes) * time.Minute}
	store := &storage.Store{WowDir: *wowDir, Client: *client, File: *file, Account: *account}
	var history *storage.History
	if *historyDir != "" {
		history = storage.NewHistory(*historyDir, *historyKeep,
			storage.WithMaxMB(*historyMaxMB), storage.WithCompressDays(*historyCompressDays))
	}

	requireOwnPaths(gameDirs, closeLog, map[string]string{"goals file (--goals-file)": goalsPath,
		"wiki directory (--wiki-dir)": *fv.wikiDir, "export directory (--export-dir)": *fv.exportDir})

	if *check {
		if err := runCheck(store, history, fresh); err != nil {
			log.Print("ERROR: ", err)
			closeLog()
			os.Exit(1)
		}
		return
	}

	log.Printf("start: version %s, game directory %q, client %q, history %q", serverVersion, *wowDir, *client, *historyDir)
	if err := serve(store, history, fresh, goalsPath, *maxResponseKB,
		foreverOption(store, *fv.addonsDir, *fv.wikiDir, *fv.exportDir)); err != nil {
		log.Print("ERROR: ", err)
		closeLog()
		os.Exit(1)
	}
}

// serve runs the MCP server on stdio. With history it also opens the long history database and starts the watcher
// that records every save of the dump file between tool calls.
func serve(store *storage.Store, history *storage.History, fresh snapshot.Freshness, goalsPath string, maxResponseKB int, extra ...func(*mcpserver.Config)) error {
	options := []func(*mcpserver.Config){func(c *mcpserver.Config) { c.MaxResponseKB = maxResponseKB }}
	options = append(options, extra...)
	if history != nil {
		long, closeLedger := withLedger(history.Dir)
		defer closeLedger()
		options = append(options, long)
	}
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	if history != nil {
		go mcpserver.Watch(ctx, serverConfig(store, history, fresh, goalsPath, options...), watchInterval)
	}
	return newServer(store, history, fresh, goalsPath, options...).Run(ctx, &mcp.StdioTransport{})
}

// setupLogging sends the standard log to stderr (stdout belongs to the MCP protocol) and, when
// --log is given, to a file as well. It rejects paths inside the game directory: the server writes nothing there.
// The same rule applies to the history directory. It returns a function that closes the log file.
func setupLogging(logPath, historyDir string, gameDirs []string) (closeLog func(), err error) {
	log.SetPrefix(serverName + ": ")
	log.SetOutput(os.Stderr)
	closeLog = func() {}

	if err := storage.RequireOutside("history directory (--history-dir)", historyDir, gameDirs...); err != nil {
		return closeLog, err
	}
	if logPath == "" {
		return closeLog, nil
	}
	logPath = storage.ResolveBeside(logPath, ownDir())
	if err := storage.RequireOutside("log file (--log)", logPath, gameDirs...); err != nil {
		return closeLog, err
	}
	f, err := storage.OpenAppend(logPath)
	if err != nil {
		return closeLog, fmt.Errorf("cannot open log file %q: %w", logPath, err)
	}
	log.SetOutput(io.MultiWriter(os.Stderr, f))
	return func() { f.Close() }, nil
}

// resolveGoalsFile picks the goals file: the given one (a relative path is resolved from the exe directory) or goals.json next to
// the history directory, or next to the exe when there is no history. The weights.json file lives in the same directory.
func resolveGoalsFile(flagValue, historyDir string) string {
	if flagValue != "" {
		return storage.ResolveBeside(flagValue, ownDir())
	}
	dir := ownDir()
	if historyDir != "" {
		dir = filepath.Dir(historyDir)
	}
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "goals.json")
}

// foreverFlags are the Forever-suite flags, registered apart to keep main within the function-length rule.
type foreverFlags struct {
	addonsDir          *string
	installSlots       *int
	wikiDir, exportDir *string
}

func registerForeverFlags() foreverFlags {
	return foreverFlags{
		addonsDir: flag.String("addons-dir", os.Getenv("WOW_ADDONS_DIR"),
			`the game's Interface/AddOns folder, enables the request-slot channel (wow_waypoint_set, wow_map_focus, ...); empty = slots disabled (can also be set with WOW_ADDONS_DIR)`),
		installSlots: flag.Int("install-slots", 0,
			"create this many WowMcp_S#### request-slot addons in --addons-dir and exit (0 = no; usual count 600)"),
		wikiDir: flag.String("wiki-dir", "wiki",
			`the server's own wiki directory (wow_wiki_*); a relative path is resolved from the server directory; never inside the game directory`),
		exportDir: flag.String("export-dir", "exports",
			`where wow_export_csv writes; a relative path is resolved from the server directory; never inside the game directory`),
	}
}

// requireOwnPaths aborts when any of the server's own paths would land inside the game directory.
func requireOwnPaths(gameDirs []string, closeLog func(), paths map[string]string) {
	for name, dir := range paths {
		if err := storage.RequireOutside(name, dir, gameDirs...); err != nil {
			fmt.Fprintln(os.Stderr, "ERROR:", err)
			closeLog()
			os.Exit(1)
		}
	}
}

// runInstallSlots creates the request-slot addons (--install-slots) and reports the result.
func runInstallSlots(addonsDir string, n int) {
	if err := slots.Install(addonsDir, n); err != nil {
		fmt.Fprintln(os.Stderr, "ERROR:", err)
		os.Exit(1)
	}
	fmt.Printf("installed %d request slots in %s\n", n, addonsDir)
}

// foreverOption wires the Forever-suite dependencies: other-addon ingestion, CSV exports, the wiki and,
// when --addons-dir is given, the request-slot channel. The slot bodies are the one thing the server
// writes inside the game directory; slots.ValidAddOnsDir fences it to WowMcp_S#### folders only.
func foreverOption(store *storage.Store, addonsDir, wikiDir, exportDir string) func(*mcpserver.Config) {
	return func(c *mcpserver.Config) {
		c.Extras = &storage.ExtrasStore{Store: store}
		c.ExportDir = exportDir
		w := wiki.Wiki{Dir: wikiDir}
		if err := w.Seed(); err != nil {
			log.Print("WARN: wiki seed: ", err)
		}
		c.Wiki = w
		if addonsDir == "" {
			return
		}
		if err := slots.ValidAddOnsDir(addonsDir); err != nil {
			log.Print("WARN: request slots disabled: ", err)
			return
		}
		engine := slots.Engine{Queue: &slots.Queue{File: filepath.Join(ownDir(), "slots.json")}, AddOnsDir: addonsDir}
		c.Slots = engine
		c.LiveData = startLiveChannel(engine)
	}
}

// startLiveChannel starts the realtime strip pipeline: a watcher on the game's Screenshots folder
// (derived from the AddOns dir: <client>/Interface/AddOns → <client>/Screenshots) decodes strip
// screenshots into the live store, feeds acknowledged message ids back into the slot inbox, and a
// heartbeat keeps the slot bodies stamped so the addon sees the server as alive.
func startLiveChannel(engine slots.Engine) *strip.LiveStore {
	live := &strip.LiveStore{}
	dir := filepath.Join(filepath.Dir(filepath.Dir(engine.AddOnsDir)), "Screenshots")
	watcher := &strip.Watcher{Dir: dir, Got: engine.Queue, OnMessage: func(payload []byte) {
		if err := live.Set(payload); err != nil {
			log.Print("WARN: live payload: ", err)
		}
		if _, err := engine.Flush(true); err != nil {
			log.Print("WARN: slot ack flush: ", err)
		}
	}}
	go watcher.Run(context.Background())
	go func() {
		for range time.Tick(30 * time.Second) {
			if _, err := engine.Flush(false); err != nil {
				log.Print("WARN: slot heartbeat: ", err)
			}
		}
	}()
	return live
}

// newServer assembles the MCP server from the snapshot store and the freshness policy. Shared by main and tests.
// An empty goalsPath means goals are disabled. Options adjust the configuration (e.g. the response size limit).
func newServer(store *storage.Store, history *storage.History, fresh snapshot.Freshness, goalsPath string, options ...func(*mcpserver.Config)) *mcp.Server {
	return mcpserver.New(serverConfig(store, history, fresh, goalsPath, options...))
}

func serverConfig(store *storage.Store, history *storage.History, fresh snapshot.Freshness, goalsPath string, options ...func(*mcpserver.Config)) mcpserver.Config {
	cfg := mcpserver.Config{Name: serverName, Version: serverVersion, Snapshots: store, Freshness: fresh}
	if goalsPath != "" {
		cfg.Goals = storage.OwnFile{Path: goalsPath}
		cfg.GoalsFile = goalsPath
		cfg.Weights = storage.OwnFile{Path: filepath.Join(filepath.Dir(goalsPath), "weights.json")}
	}
	if history != nil && history.Dir != "" {
		cfg.History, cfg.HistoryDir = history, history.Dir
	}
	for _, option := range options {
		option(&cfg)
	}
	return cfg
}

// runCheck prints where the snapshot is read from, which characters it contains and how old the data is (--check).
func runCheck(store *storage.Store, history *storage.History, fresh snapshot.Freshness) error {
	db, err := store.Load()
	if err != nil {
		return err
	}
	history.Record(db)
	fmt.Println("File:", db.Path)
	fmt.Println("Modified:", db.ModTime.Format("2006-01-02 15:04:05"))
	if db.Account != "" {
		fmt.Println("Account:", db.Account)
		if len(db.OtherAccounts) > 0 {
			fmt.Println("Other accounts with a dump:", strings.Join(db.OtherAccounts, ", "), "(pin one with --account)")
		}
	}
	for _, name := range db.CharacterNames() {
		meta := fresh.SnapshotMeta(name, db.Characters[name])
		fmt.Printf("Character: %s, snapshot: %v\n", name, meta["dumpedAt"])
		if w, ok := meta["warning"]; ok {
			fmt.Println("  Warning:", w)
		}
	}
	if history != nil {
		fmt.Println("History:", history.Dir)
	}
	return nil
}

// withLedger opens the long history database, history.db inside the history directory (so never in the game
// directory), and returns the option that gives it to the server. When it cannot be opened the server runs without it.
func withLedger(historyDir string) (option func(*mcpserver.Config), closeLedger func()) {
	err := os.MkdirAll(historyDir, 0o755)
	var long *ledger.Ledger
	if err == nil {
		long, err = ledger.Open(filepath.Join(historyDir, "history.db"))
	}
	if err != nil {
		log.Print("the long history is off, the dump history still works: ", err)
		return func(*mcpserver.Config) {}, func() {}
	}
	return func(c *mcpserver.Config) { c.Ledger = long }, func() { long.Close() }
}

// exeDir is the directory of the server executable; "" when it cannot be determined.
func exeDir() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Dir(exe)
}

// ownDir is the server's own directory: next to the executable when it is writable, otherwise <user config>/wow-mcp.
func ownDir() string { return storage.DefaultDir(exeDir(), os.UserConfigDir) }

// defaultHistoryDir is the "history" directory in the server's own directory.
func defaultHistoryDir() string {
	dir := ownDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "history")
}
