// SPDX-License-Identifier: GPL-3.0-or-later
// Forever Suite fork addition (port of wow-seer's screenshot watcher, src/server.ts startWatch).

package strip

import (
	"context"
	"errors"
	"image/png"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// scanEvery is how often the Screenshots folder is polled.
const scanEvery = 300 * time.Millisecond

// GotSink receives the ids of strip messages the server decoded, so the slot inbox's `got`
// list can carry them back to the addon (its dirty tracking stops resending those sections).
type GotSink interface {
	Got(ids []int64)
}

// Watcher polls a Screenshots directory for strip screenshots: it waits for each new PNG
// to stop growing, decodes it, deletes decoded strips (a player's own screenshots are left
// alone and never re-read) and calls OnMessage with the JSON payload of every completed message.
type Watcher struct {
	// Dir is the game's Screenshots folder: <wowDir>/<client>/Screenshots.
	Dir string
	// OnMessage receives the decoded JSON payload of each completed message (LiveStore.Set).
	OnMessage func(payload []byte)
	// Got, when set, is told the message ids the server received (the slot channel's inbox).
	Got GotSink

	asm       *Assembler
	seen      map[string]time.Time // processed files (path -> mtime): never read again
	sizes     map[string]int64     // last seen size of files not yet stable
	undeleted map[string]bool      // decoded strips Windows would not let us delete yet
	lastErr   string
}

// Run polls the folder every 300 ms until ctx is cancelled.
func (w *Watcher) Run(ctx context.Context) {
	tick := time.NewTicker(scanEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			w.pass()
		}
	}
}

// candidate is one PNG in the folder.
type candidate struct {
	path  string
	mtime time.Time
	size  int64
}

// pass is one poll: retry pending deletes, then read the new stable PNGs, newest first.
func (w *Watcher) pass() {
	if w.seen == nil {
		w.asm, w.seen, w.sizes, w.undeleted = NewAssembler(), map[string]time.Time{}, map[string]int64{}, map[string]bool{}
	}
	for path := range w.undeleted {
		if remove(path) {
			delete(w.undeleted, path)
		}
	}
	files, err := w.list()
	w.report(err)
	if err != nil {
		return
	}
	w.prune(files)
	for _, f := range files {
		if w.seen[f.path].Equal(f.mtime) {
			continue
		}
		// The game may still be writing the file: read it only once its size held for two polls.
		if w.sizes[f.path] != f.size || f.size == 0 {
			w.sizes[f.path] = f.size
			continue
		}
		delete(w.sizes, f.path)
		w.process(f.path, f.mtime)
	}
}

// list returns the folder's PNGs sorted newest first by mtime.
func (w *Watcher) list() ([]candidate, error) {
	entries, err := os.ReadDir(w.Dir)
	if err != nil {
		return nil, err
	}
	var files []candidate
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".png") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, candidate{path: filepath.Join(w.Dir, e.Name()), mtime: info.ModTime(), size: info.Size()})
	}
	for i := range files { // insertion sort, newest first; screenshot folders are small
		for j := i; j > 0 && files[j].mtime.After(files[j-1].mtime); j-- {
			files[j], files[j-1] = files[j-1], files[j]
		}
	}
	return files, nil
}

// prune forgets files no longer in the folder, so the seen set cannot grow without bound.
func (w *Watcher) prune(files []candidate) {
	present := map[string]bool{}
	for _, f := range files {
		present[f.path] = true
	}
	for path := range w.seen {
		if !present[path] {
			delete(w.seen, path)
		}
	}
	for path := range w.sizes {
		if !present[path] {
			delete(w.sizes, path)
		}
	}
}

// process reads and decodes one stable PNG. A decoded or broken strip is deleted (the addon
// painted it); a non-strip image is a player's screenshot: left in place, remembered, never re-read.
func (w *Watcher) process(path string, mtime time.Time) {
	file, err := os.Open(path)
	if err != nil {
		return // reread on the next pass
	}
	img, err := png.Decode(file)
	file.Close()
	if err != nil {
		w.seen[path] = mtime // not even a PNG inside: leave the file, never re-read it
		log.Printf("screenshot watcher: %s: %v", filepath.Base(path), err)
		return
	}
	frame, err := Decode(img)
	w.seen[path] = mtime
	if errors.Is(err, NotStrip) {
		return
	}
	w.discard(path)
	if err != nil {
		log.Printf("screenshot watcher: bad frame %s: %v", filepath.Base(path), err)
		return
	}
	w.accept(frame)
}

// accept feeds a frame to the assembler and emits the completed message.
func (w *Watcher) accept(frame Frame) {
	payload, done := w.asm.Add(frame)
	if !done {
		return
	}
	msg, err := Message(payload)
	if err != nil {
		log.Printf("screenshot watcher: message %d: %v", frame.MsgID, err)
		return
	}
	if w.Got != nil {
		w.Got.Got([]int64{int64(frame.MsgID)})
	}
	if w.OnMessage != nil {
		w.OnMessage(msg)
	}
}

// discard deletes a decoded strip; on Windows the game may still hold the screenshot open,
// so a failed delete is retried on every pass.
func (w *Watcher) discard(path string) {
	if !remove(path) {
		w.undeleted[path] = true
	}
}

// remove deletes the file; an already-missing file counts as removed.
func remove(path string) bool {
	err := os.Remove(path)
	return err == nil || errors.Is(err, fs.ErrNotExist)
}

// report logs folder errors only when they change (the folder may simply not exist yet).
func (w *Watcher) report(err error) {
	text := ""
	if err != nil {
		text = err.Error()
	}
	if text != w.lastErr && text != "" {
		log.Print("screenshot watcher: ", text)
	}
	w.lastErr = text
}
