// SPDX-License-Identifier: GPL-3.0-or-later
// Forever Suite fork addition (port of wow-seer's request slots).

// Package slots is the inbound request channel to the game: N tiny LoadOnDemand addons
// (WowMcp_S0001..) whose body.lua the server rewrites. The addon in game loads one unused
// slot per poll (~15 s), which runs the body once and hands it the current request inbox.
// This is the ONE place in the fork that writes inside the game directory; the paths are
// guarded here: the target directory must end in Interface/AddOns, and the only folders
// ever written are those matching ^WowMcp_S\d{4}$.
package slots

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// DefaultSlots is the usual number of request slots (about 2.5 h of polling at 15 s per slot).
const DefaultSlots = 600

// tocInterface is the client interface number of WoW: Forever 1.60.1.
const tocInterface = "16001"

var slotRe = regexp.MustCompile(`^WowMcp_S\d{4}$`)

// SlotName is the folder (and .toc) name of slot n: WowMcp_S0001, WowMcp_S0002, ...
func SlotName(n int) string {
	return fmt.Sprintf("WowMcp_S%04d", n)
}

// ValidAddOnsDir rejects any target that is not an Interface/AddOns folder: the slot
// writer must not be pointable at WTF, SavedVariables, a parent directory or anywhere else.
func ValidAddOnsDir(dir string) error {
	clean := filepath.Clean(dir)
	parent := filepath.Dir(clean)
	if !strings.EqualFold(filepath.Base(clean), "AddOns") || !strings.EqualFold(filepath.Base(parent), "Interface") {
		return fmt.Errorf("request slots go only into an Interface/AddOns folder, not %q", dir)
	}
	return nil
}

// Install creates (or refreshes) the slot addons WowMcp_S0001..WowMcp_S<n> in addOnsDir:
// each folder gets its .toc and a body.lua with an empty inbox. Slot folders above n are
// removed, so shrinking the count leaves no stale bodies. Nothing else is touched.
func Install(addOnsDir string, n int) error {
	if n < 1 {
		return fmt.Errorf("the number of slots must be at least 1, got %d", n)
	}
	if n > 9999 {
		return fmt.Errorf("at most 9999 slots fit the WowMcp_S\\d{4} naming, got %d", n)
	}
	if err := ValidAddOnsDir(addOnsDir); err != nil {
		return err
	}
	body := emptyInbox(time.Now())
	for i := 1; i <= n; i++ {
		name := SlotName(i)
		folder := filepath.Join(addOnsDir, name)
		if err := os.MkdirAll(folder, 0o755); err != nil {
			return err
		}
		if err := writeFileAtomic(filepath.Join(folder, name+".toc"), []byte(slotToc(i))); err != nil {
			return err
		}
		if err := writeFileAtomic(filepath.Join(folder, "body.lua"), []byte(body)); err != nil {
			return err
		}
	}
	return removeSlotsAbove(addOnsDir, n)
}

// slotToc is the .toc content of slot n.
func slotToc(n int) string {
	return "## Interface: " + tocInterface + "\n" +
		fmt.Sprintf("## Title: WowMcp slot %04d\n", n) +
		"## Notes: Request slot for WowMcp.\n" +
		"## Group: WowMcp\n" +
		"## LoadOnDemand: 1\n" +
		"\n" +
		"body.lua\n"
}

// emptyInbox is the body written at install time: an inbox with no requests yet. The
// install scripts (install.sh --slots, install.ps1 -Slots) write this exact text too.
func emptyInbox(now time.Time) string {
	return fmt.Sprintf("WowMcpInbox = {[\"t\"]=%d,[\"reqs\"]={},[\"got\"]={}}\n", now.Unix())
}

// SlotDirs lists the installed slot folders (names matching the slot pattern) in addOnsDir, sorted.
func SlotDirs(addOnsDir string) ([]string, error) {
	if err := ValidAddOnsDir(addOnsDir); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(addOnsDir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() && slotRe.MatchString(e.Name()) {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

// removeSlotsAbove deletes slot folders whose number is above n; only names matching the
// slot pattern are ever removed.
func removeSlotsAbove(addOnsDir string, n int) error {
	names, err := SlotDirs(addOnsDir)
	if err != nil {
		return err
	}
	for _, name := range names {
		num, err := strconv.Atoi(name[len("WowMcp_S"):])
		if err != nil || num <= n {
			continue
		}
		if err := os.RemoveAll(filepath.Join(addOnsDir, name)); err != nil {
			return err
		}
	}
	return nil
}

// writeFileAtomic writes data to a temporary file in the same directory and renames it over
// path, so a reader (the game) never sees half a file. On Windows the rename is retried up
// to 10 times with a growing pause: the game holds slot bodies open briefly while loading them.
func writeFileAtomic(path string, data []byte) (err error) {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return renameRetry(tmp.Name(), path)
}

// renameRetry renames tmp over path; on Windows EPERM/EACCES/EBUSY it retries up to 10
// times with a backoff of 10 ms times the attempt (the pattern of wow-seer's replaceFile).
func renameRetry(tmp, path string) error {
	for attempt := 1; ; attempt++ {
		err := os.Rename(tmp, path)
		if err == nil {
			return nil
		}
		if runtime.GOOS != "windows" || attempt >= 10 || !busyError(err) {
			os.Remove(tmp)
			return err
		}
		time.Sleep(time.Duration(attempt) * 10 * time.Millisecond)
	}
}

// busyError says whether the error is one Windows gives while another process holds the
// file: EPERM and EACCES map to fs.ErrPermission; EBUSY is matched by name.
func busyError(err error) bool {
	if errors.Is(err, fs.ErrPermission) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "EBUSY") || strings.Contains(msg, "being used by another process")
}
