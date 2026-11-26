// SPDX-License-Identifier: GPL-3.0-or-later
// Forever Suite fork addition.

package strip

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func writePNG(t *testing.T, path string, img image.Image) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

type gotRecorder struct{ ids []int64 }

func (g *gotRecorder) Got(ids []int64) { g.ids = append(g.ids, ids...) }

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestWatcherDecodesAndDeletesStrips(t *testing.T) {
	dir := t.TempDir()
	var messages [][]byte
	got := &gotRecorder{}
	w := &Watcher{Dir: dir, OnMessage: func(p []byte) { messages = append(messages, p) }, Got: got}

	payload := []byte(`{"v":1,"at":10,"character":"Monjits-Gpt","sections":{"bags":{"free":3}}}`)
	strip := filepath.Join(dir, "WoWScrnShot_112520_120000.png")
	writePNG(t, strip, render(300, 1, 1, 2, 40, payload))

	w.pass() // first sight: the size is recorded, nothing is read yet
	if len(messages) != 0 || !exists(strip) {
		t.Fatal("the file must not be read before its size held for two polls")
	}
	w.pass() // stable: decoded, emitted, deleted
	if len(messages) != 1 || string(messages[0]) != string(payload) {
		t.Fatalf("messages %q", messages)
	}
	if exists(strip) {
		t.Fatal("a decoded strip must be deleted")
	}
	if len(got.ids) != 1 || got.ids[0] != 300 {
		t.Fatalf("got ids %v", got.ids)
	}
}

func TestWatcherLeavesPlayerScreenshotsAlone(t *testing.T) {
	dir := t.TempDir()
	count := 0
	w := &Watcher{Dir: dir, OnMessage: func([]byte) { count++ }}

	shot := filepath.Join(dir, "WoWScrnShot_sunset.png")
	writePNG(t, shot, image.NewRGBA(image.Rect(0, 0, 200, 100))) // all black: no strip

	for i := 0; i < 4; i++ {
		w.pass()
	}
	if count != 0 || !exists(shot) {
		t.Fatalf("a player screenshot must stay: count %d exists %v", count, exists(shot))
	}
	if _, ok := w.seen[shot]; !ok {
		t.Fatal("a non-strip file must be remembered so it is never re-read")
	}
}

func TestWatcherRetriesUndeletedFiles(t *testing.T) {
	dir := t.TempDir()
	count := 0
	w := &Watcher{Dir: dir, OnMessage: func([]byte) { count++ }}

	payload := []byte(`{"v":1,"at":11,"character":"Monjits-Gpt","sections":{}}`)
	strip := filepath.Join(dir, "WoWScrnShot_1.png")
	writePNG(t, strip, render(301, 1, 1, 2, 40, payload))

	w.pass()
	if err := os.Chmod(dir, 0o555); err != nil { // the delete fails like Windows' EBUSY
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o755)
	w.pass()
	if count != 1 {
		t.Fatalf("the strip must still be decoded: count %d", count)
	}
	if !w.undeleted[strip] || !exists(strip) {
		t.Fatal("a file the OS would not delete must go to the retry set")
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	w.pass()
	if exists(strip) || len(w.undeleted) != 0 {
		t.Fatal("the retry pass must delete the file")
	}
}

func TestWatcherAssemblesPartsAcrossFiles(t *testing.T) {
	dir := t.TempDir()
	var messages [][]byte
	w := &Watcher{Dir: dir, OnMessage: func(p []byte) { messages = append(messages, p) }}

	text := []byte(`{"v":1,"at":12,"character":"Monjits-Gpt","sections":{"quests":[{"id":5}]}}`)
	half := len(text) / 2
	writePNG(t, filepath.Join(dir, "a.png"), render(302, 1, 2, 2, 40, text[:half]))
	writePNG(t, filepath.Join(dir, "b.png"), render(302, 2, 2, 2, 40, text[half:]))

	w.pass()
	w.pass()
	if len(messages) != 1 || string(messages[0]) != string(text) {
		t.Fatalf("messages %q", messages)
	}
	if exists(filepath.Join(dir, "a.png")) || exists(filepath.Join(dir, "b.png")) {
		t.Fatal("both strip parts must be deleted")
	}
}
