// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 Piekasz (GitHub: Piekario). Author attribution must be kept: see NOTICE.

package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wowmcp/internal/fixtures"
	"wowmcp/internal/snapshot"
)

// fakeExtrasDir builds a game directory with the WowMcp dump (so discovery works),
// an account-level Questie.lua and a per-character RXPGuides.lua. realmDir and charDir
// let tests exercise both layouts: numeric realm + "Name-Realm" folder (Forever) and
// realm-name + "Name" folder (classic).
func fakeExtrasDir(t *testing.T, realmDir, charDir string) *ExtrasStore {
	t.Helper()
	root := t.TempDir()
	account := filepath.Join(root, "_forever_", "WTF", "Account", "TESTACCOUNT")
	saved := filepath.Join(account, "SavedVariables")
	if err := os.MkdirAll(saved, 0o755); err != nil {
		t.Fatal(err)
	}
	dump, err := os.ReadFile(fixtures.Testdata("WowMcp.lua"))
	if err != nil {
		t.Fatal(err)
	}
	write := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(saved, SavedVariablesFile), string(dump))
	write(filepath.Join(saved, "Questie.lua"),
		"QuestieConfig = {\n[\"char\"] = {\n[\"Monjits Gpt\"] = {\n[\"complete\"] = {\n[935] = true,\n},\n},\n},\n}\n")
	write(filepath.Join(saved, "Questie.lua.bak"), "QuestieConfig = nil\n")
	charSaved := filepath.Join(account, realmDir, charDir, "SavedVariables")
	write(filepath.Join(charSaved, "RXPGuides.lua"), "RXPCData = {\n[\"currentStep\"] = 25,\n}\n")
	return &ExtrasStore{Store: &Store{WowDir: root, Sleep: func(d time.Duration) {}}}
}

func TestExtrasAccount(t *testing.T) {
	x := fakeExtrasDir(t, "91", "Monjits-Gpt")
	vars, mod, err := x.Account("Questie.lua")
	if err != nil {
		t.Fatal(err)
	}
	if mod.IsZero() {
		t.Error("mtime missing")
	}
	cfg := snapshot.AsMap(vars["QuestieConfig"])
	if snapshot.AsMap(snapshot.AsMap(cfg["char"])["Monjits Gpt"]) == nil {
		t.Errorf("parsed QuestieConfig: %v", vars)
	}
}

func TestExtrasAccountMissingIsNotFound(t *testing.T) {
	x := fakeExtrasDir(t, "91", "Monjits-Gpt")
	_, _, err := x.Account("TomTom.lua")
	if !IsNotFound(err) {
		t.Fatalf("want NotFound, got %v", err)
	}
}

func TestExtrasRejectsBadFileNames(t *testing.T) {
	x := fakeExtrasDir(t, "91", "Monjits-Gpt")
	for _, name := range []string{"", "Questie.lua.bak", "Questie.txt", filepath.Join("..", "Questie.lua")} {
		if _, _, err := x.Account(name); err == nil || IsNotFound(err) {
			t.Errorf("%q should be rejected, got %v", name, err)
		}
		if _, _, err := x.Character("Monjits-Gpt", name); err == nil || IsNotFound(err) {
			t.Errorf("character %q should be rejected, got %v", name, err)
		}
	}
}

func TestExtrasCharacterForeverLayout(t *testing.T) {
	x := fakeExtrasDir(t, "91", "Monjits-Gpt") // numeric realm dir, "Name-Realm" char dir
	vars, _, err := x.Character("monjits-gpt", "RXPGuides.lua")
	if err != nil {
		t.Fatal(err)
	}
	data := snapshot.AsMap(vars["RXPCData"])
	if step, _ := snapshot.AsInt(data["currentStep"]); step != 25 {
		t.Errorf("RXPCData: %v", vars)
	}
}

func TestExtrasCharacterClassicLayout(t *testing.T) {
	x := fakeExtrasDir(t, "Gpt", "Monjits") // realm-name dir, name-only char dir
	if _, _, err := x.Character("Monjits-Gpt", "RXPGuides.lua"); err != nil {
		t.Fatal(err)
	}
}

func TestExtrasCharacterMissing(t *testing.T) {
	x := fakeExtrasDir(t, "91", "Monjits-Gpt")
	_, _, err := x.Character("Nobody-Here", "RXPGuides.lua")
	if !IsNotFound(err) {
		t.Fatalf("missing character directory should be NotFound, got %v", err)
	}
	_, _, err = x.Character("Monjits-Gpt", "OpenMap.lua")
	if !IsNotFound(err) {
		t.Fatalf("missing character file should be NotFound, got %v", err)
	}
	if !strings.Contains(err.Error(), "OpenMap.lua") {
		t.Errorf("the error should name the file: %v", err)
	}
}
