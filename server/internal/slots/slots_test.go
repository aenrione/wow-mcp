// SPDX-License-Identifier: GPL-3.0-or-later
// Forever Suite fork addition.

package slots

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInstallCreatesSlotAddons(t *testing.T) {
	addons := addOnsDir(t)
	if err := Install(addons, 3); err != nil {
		t.Fatal(err)
	}
	names, err := SlotDirs(addons)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(names, ",") != "WowMcp_S0001,WowMcp_S0002,WowMcp_S0003" {
		t.Fatalf("slot folders: %v", names)
	}
	toc, err := os.ReadFile(filepath.Join(addons, "WowMcp_S0002", "WowMcp_S0002.toc"))
	if err != nil {
		t.Fatal(err)
	}
	want := "## Interface: 16001\n## Title: WowMcp slot 0002\n## Notes: Request slot for WowMcp.\n## Group: WowMcp\n## LoadOnDemand: 1\n\nbody.lua\n"
	if string(toc) != want {
		t.Errorf("toc:\n%s\nwant:\n%s", toc, want)
	}
	body, err := os.ReadFile(filepath.Join(addons, "WowMcp_S0001", "body.lua"))
	if err != nil {
		t.Fatal(err)
	}
	// The install body is an empty inbox; the install scripts write this exact shape too.
	s := string(body)
	if !strings.HasPrefix(s, `WowMcpInbox = {["t"]=`) || !strings.HasSuffix(s, ",[\"reqs\"]={},[\"got\"]={}}\n") {
		t.Errorf("install body: %s", s)
	}
}

func TestInstallRemovesSlotsAboveN(t *testing.T) {
	addons := addOnsDir(t)
	if err := Install(addons, 5); err != nil {
		t.Fatal(err)
	}
	if err := Install(addons, 2); err != nil {
		t.Fatal(err)
	}
	names, _ := SlotDirs(addons)
	if strings.Join(names, ",") != "WowMcp_S0001,WowMcp_S0002" {
		t.Fatalf("after shrinking to 2: %v", names)
	}
}

// The slot writer must refuse every target that is not an Interface/AddOns folder.
func TestSlotsRefuseNonAddOnsPaths(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{
		root,
		filepath.Join(root, "WTF"),
		filepath.Join(root, "WTF", "Account"),
		filepath.Join(root, "Interface"),                   // the parent of AddOns
		filepath.Join(root, "AddOns"),                      // AddOns without Interface above it
		filepath.Join(root, "Interface", "AddOns", "Some"), // below AddOns
		filepath.Join(root, "SavedVariables"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := Install(dir, 1); err == nil {
			t.Errorf("Install accepted %s", dir)
		}
		at := time.Unix(1_764_000_000, 0)
		q := &Queue{File: filepath.Join(t.TempDir(), "slots.json"), Now: func() time.Time { return at }}
		if err := q.WriteAll(dir); err == nil {
			t.Errorf("WriteAll accepted %s", dir)
		}
		if _, err := SlotDirs(dir); err == nil {
			t.Errorf("SlotDirs accepted %s", dir)
		}
	}
}

func TestInstallBounds(t *testing.T) {
	addons := addOnsDir(t)
	if err := Install(addons, 0); err == nil {
		t.Error("0 slots must be refused")
	}
	if err := Install(addons, 10_000); err == nil {
		t.Error("more than 9999 slots cannot be named")
	}
}

func TestEngineCountsAndFlushes(t *testing.T) {
	addons := addOnsDir(t)
	if err := Install(addons, 4); err != nil {
		t.Fatal(err)
	}
	at := time.Unix(1_764_000_000, 0)
	e := Engine{Queue: &Queue{File: filepath.Join(t.TempDir(), "slots.json"), Now: func() time.Time { return at }}, AddOnsDir: addons}
	installed, err := e.Installed()
	if err != nil || installed != 4 {
		t.Fatalf("installed: %d %v", installed, err)
	}
	if _, err := e.Request("waypoint", map[string]any{"map": 1426}); err != nil {
		t.Fatal(err)
	}
	if wrote, err := e.Flush(true); err != nil || !wrote {
		t.Fatalf("flush: %v %v", wrote, err)
	}
	if n, _ := e.Pending(); n != 1 {
		t.Fatalf("pending: %d", n)
	}
	if e.LastWrite() != at {
		t.Fatalf("lastWrite: %v", e.LastWrite())
	}
}
