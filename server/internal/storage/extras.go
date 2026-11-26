// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 Piekasz (GitHub: Piekario). Author attribution must be kept: see NOTICE.

package storage

// Forever Suite fork: reading OTHER addons' SavedVariables files (Questie, Profiteer,
// TomTom, OpenMap, RXPGuides) next to the WowMcp dump. Strictly read-only: the server
// never writes anything in the game directory, and it never reads the .lua.bak copies.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"wowmcp/internal/savedvars"
)

// NotFoundError says an extra SavedVariables file does not exist: the addon is not
// installed or has never saved. It is a state, not a failure, so tools report it
// as a note instead of an error.
type NotFoundError struct {
	File  string // file name, e.g. "Questie.lua"
	Where string // the directory that was searched
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("no %s in %s: the addon is not installed or has never saved its variables (log out or /reload in game)", e.File, e.Where)
}

// NotFound marks the error as "file missing" without the caller importing this package's type.
func (e *NotFoundError) NotFound() bool { return true }

// IsNotFound says whether err means a missing extra SavedVariables file.
func IsNotFound(err error) bool {
	var nf *NotFoundError
	return errors.As(err, &nf)
}

// ExtrasStore reads and parses SavedVariables files of other addons, reusing the
// Store's discovery of the game directory, client folder and account. It composes the
// Store and never modifies it; like the Store it re-reads the file on every call.
type ExtrasStore struct {
	Store *Store
}

// checkFileName accepts only a bare .lua file name: no directories, never a .bak copy.
func checkFileName(file string) error {
	switch {
	case file == "" || filepath.Base(file) != file:
		return fmt.Errorf("extras file name %q must be a bare file name without directories", file)
	case !strings.HasSuffix(strings.ToLower(file), ".lua"):
		return fmt.Errorf("extras file name %q must end in .lua (the .lua.bak copies are never read)", file)
	}
	return nil
}

// Account reads an account-level SavedVariables file (the same directory as WowMcp.lua)
// and returns every global it defines plus the file's modification time.
// A missing file is a *NotFoundError.
func (x *ExtrasStore) Account(file string) (map[string]any, time.Time, error) {
	if err := checkFileName(file); err != nil {
		return nil, time.Time{}, err
	}
	loc, err := x.Store.locate()
	if err != nil {
		return nil, time.Time{}, err
	}
	dir := filepath.Dir(loc.path)
	return x.read(filepath.Join(dir, file), file, dir)
}

// Character reads a per-character SavedVariables file
// (WTF/Account/<acc>/<realmDir>/<Char>/SavedVariables/<file>). The character is the
// snapshot's "Name-Realm" key; the realm and character directories are discovered by
// walking the account folder, matching names case-insensitively (the realm directory on
// disk may be a number, and the character directory may be "Name-Realm" or just "Name").
func (x *ExtrasStore) Character(character, file string) (map[string]any, time.Time, error) {
	if err := checkFileName(file); err != nil {
		return nil, time.Time{}, err
	}
	loc, err := x.Store.locate()
	if err != nil {
		return nil, time.Time{}, err
	}
	accountDir := filepath.Dir(filepath.Dir(loc.path)) // WTF/Account/<acc>
	charDir, err := findCharacterDir(accountDir, character)
	if err != nil {
		return nil, time.Time{}, err
	}
	if charDir == "" {
		// No directory for the character is the same state as a missing file: nothing saved.
		name, _, _ := strings.Cut(character, "-")
		return nil, time.Time{}, &NotFoundError{File: file,
			Where: fmt.Sprintf("%s (no realm directory holds a folder named %q or %q)", accountDir, character, name)}
	}
	dir := filepath.Join(charDir, "SavedVariables")
	return x.read(filepath.Join(dir, file), file, dir)
}

// findCharacterDir locates <realmDir>/<Char> under the account folder; "" when no
// directory matches (the caller reports it as NotFound). The full "Name-Realm" directory
// name wins; otherwise a directory equal to the name part, with a realm directory equal
// to the realm part preferred over any other realm directory.
func findCharacterDir(accountDir, character string) (string, error) {
	name, realm, _ := strings.Cut(character, "-")
	realms, err := os.ReadDir(accountDir)
	if err != nil {
		return "", fmt.Errorf("cannot list the account directory %q: %w", accountDir, err)
	}
	var byName, byNameAndRealm, full string
	for _, r := range realms {
		if !r.IsDir() || strings.EqualFold(r.Name(), "SavedVariables") {
			continue
		}
		realmDir := filepath.Join(accountDir, r.Name())
		chars, err := os.ReadDir(realmDir)
		if err != nil {
			continue
		}
		for _, c := range chars {
			switch {
			case !c.IsDir():
			case strings.EqualFold(c.Name(), character):
				full = filepath.Join(realmDir, c.Name())
			case strings.EqualFold(c.Name(), name):
				if strings.EqualFold(r.Name(), realm) && realm != "" {
					byNameAndRealm = filepath.Join(realmDir, c.Name())
				} else if byName == "" {
					byName = filepath.Join(realmDir, c.Name())
				}
			}
		}
	}
	for _, dir := range []string{full, byNameAndRealm, byName} {
		if dir != "" {
			return dir, nil
		}
	}
	return "", nil
}

// read loads and parses one extras file, with the same retry as the dump: the game
// rewrites SavedVariables on /reload and logout, so a read may hit a truncated file.
func (x *ExtrasStore) read(path, file, dir string) (map[string]any, time.Time, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, time.Time{}, &NotFoundError{File: file, Where: dir}
		}
		return nil, time.Time{}, fmt.Errorf("cannot open %s: %w", path, err)
	}
	vars, err := readExtraVars(path)
	for _, delay := range retryDelays {
		if err == nil {
			break
		}
		x.Store.sleep(delay)
		vars, err = readExtraVars(path)
	}
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("%w (after %d attempts; the game may be writing the file right now, try again in a moment)",
			err, len(retryDelays)+1)
	}
	return vars, modTime(path), nil
}

// readExtraVars reads and parses the file once, returning every global variable it defines.
func readExtraVars(path string) (map[string]any, error) {
	src, err := readLimited(path, MaxDumpBytes)
	if err != nil {
		return nil, err
	}
	vars, err := savedvars.Parse(src)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return vars, nil
}
