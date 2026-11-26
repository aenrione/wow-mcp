// SPDX-License-Identifier: GPL-3.0-or-later
// Forever Suite fork addition.

package strip

import (
	"testing"
	"time"
)

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

func live(t *testing.T, s *LiveStore, payload string) {
	t.Helper()
	if err := s.Set([]byte(payload)); err != nil {
		t.Fatal(err)
	}
}

func TestLiveStoreMergesDeltas(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1_764_000_000, 0)}
	s := &LiveStore{Now: clock.Now}
	t0 := clock.now

	live(t, s, `{"v":1,"at":1000,"character":"Monjits-Gpt","full":true,"sections":{"character":{"level":12},"bags":{"free":5}}}`)
	clock.now = t0.Add(10 * time.Second)
	live(t, s, `{"v":1,"at":1010,"character":"Monjits-Gpt","sections":{"bags":{"free":2}}}`)

	sections, at, ok := s.Get("")
	if !ok || !at.Equal(t0.Add(10*time.Second)) {
		t.Fatalf("ok %v at %v", ok, at)
	}
	if len(sections) != 2 {
		t.Fatalf("a delta must merge, not replace: %v", sections)
	}
	bags, bagsAt, _ := s.Section("Monjits-Gpt", "bags")
	if bags.(map[string]any)["free"] != float64(2) || !bagsAt.Equal(t0.Add(10*time.Second)) {
		t.Fatalf("bags %v at %v", bags, bagsAt)
	}
	char, charAt, _ := s.Section("Monjits-Gpt", "character")
	if char.(map[string]any)["level"] != float64(12) || !charAt.Equal(t0) {
		t.Fatalf("character %v must keep its original time %v, got %v", char, t0, charAt)
	}
}

// Screenshots are processed newest first, so an older pending strip can be decoded after a
// newer one: its sections must not overwrite fresher data.
func TestLiveStoreSkipsStaleData(t *testing.T) {
	s := &LiveStore{Now: (&fakeClock{now: time.Unix(1_764_000_000, 0)}).Now}
	live(t, s, `{"v":1,"at":1010,"character":"Monjits-Gpt","sections":{"bags":{"free":2}}}`)
	live(t, s, `{"v":1,"at":1000,"character":"Monjits-Gpt","sections":{"bags":{"free":9},"quests":[1]}}`)

	bags, _, _ := s.Section("", "bags")
	if bags.(map[string]any)["free"] != float64(2) {
		t.Fatalf("a stale delta must not overwrite a newer section: %v", bags)
	}
	if _, _, ok := s.Section("", "quests"); !ok {
		t.Fatal("the stale payload's new section must still be merged in")
	}

	live(t, s, `{"v":1,"at":900,"character":"Monjits-Gpt","full":true,"sections":{"bags":{"free":7}}}`)
	if sections, _, _ := s.Get(""); len(sections) != 2 {
		t.Fatalf("a stale full snapshot must be ignored, not erase newer deltas: %v", sections)
	}
}

func TestLiveStoreFullReplaces(t *testing.T) {
	s := &LiveStore{Now: (&fakeClock{now: time.Unix(1_764_000_000, 0)}).Now}
	live(t, s, `{"v":1,"at":1000,"character":"Monjits-Gpt","sections":{"bags":{"free":5},"quests":[1]}}`)
	live(t, s, `{"v":1,"at":1020,"character":"Monjits-Gpt","full":true,"sections":{"character":{"level":13}}}`)

	sections, _, _ := s.Get("Monjits-Gpt")
	if len(sections) != 1 || sections["character"] == nil {
		t.Fatalf("full=true must replace the whole state: %v", sections)
	}
}

func TestLiveStoreCharacters(t *testing.T) {
	s := &LiveStore{Now: (&fakeClock{now: time.Unix(1_764_000_000, 0)}).Now}
	live(t, s, `{"v":1,"at":1,"character":"Monjits-Gpt","sections":{"bags":{}}}`)
	live(t, s, `{"v":1,"at":2,"character":"Alt","sections":{"character":{}}}`)

	if _, _, ok := s.Get(""); !ok {
		t.Fatal("empty name must resolve to the most recent character")
	}
	if sections, _, ok := s.Get(""); !ok || sections["character"] == nil {
		t.Fatalf("empty name must be the newest payload's character: %v", sections)
	}
	if _, _, ok := s.Get("monjits"); !ok {
		t.Fatal("a case-insensitive prefix must resolve")
	}
	if _, _, ok := s.Get("Nobody"); ok {
		t.Fatal("an unknown character must not resolve")
	}

	if err := s.Set([]byte(`{"v":1,"at":1,"sections":{}}`)); err == nil {
		t.Fatal("a payload without a character must error")
	}
	if err := s.Set([]byte(`not json`)); err == nil {
		t.Fatal("a broken payload must error")
	}
}
