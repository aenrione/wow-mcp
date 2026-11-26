// SPDX-License-Identifier: GPL-3.0-or-later
// Forever Suite fork addition.

package strip

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// LiveStore keeps the newest live state per character, merged from delta payloads.
// The addon sends {v, at, character, full, sections}: full=true replaces the character's
// whole state, otherwise only the sections present in the payload are merged in, each with
// its own receive time. Memory stays bounded: one merged state per character.
// It satisfies the mcpserver Live port (Get, Section).
type LiveStore struct {
	// Now is the clock; nil means time.Now (tests inject their own).
	Now func() time.Time

	mu    sync.Mutex
	chars map[string]*liveCharacter
	last  string // character of the most recent payload
}

type liveCharacter struct {
	at       time.Time // server time the last payload for this character arrived
	sent     float64   // the last payload's game time (envelope at)
	sections map[string]liveSection
}

type liveSection struct {
	data any
	at   time.Time // server time this section's data last changed
	sent float64   // game time it was collected (envelope at of its payload)
}

// envelope is the JSON shape of one decoded strip message.
type envelope struct {
	V         int            `json:"v"`
	At        float64        `json:"at"` // game time (unix seconds) the payload was collected
	Character string         `json:"character"`
	Full      bool           `json:"full"` // true = a full snapshot, replaces the merged state
	Sections  map[string]any `json:"sections"`
}

func (l *LiveStore) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

// Set merges one decoded JSON payload into the character's live state. Sections whose
// stored game time is newer than the payload's are kept (screenshots are processed newest
// first, so an older pending strip may be decoded after a newer one).
func (l *LiveStore) Set(payload []byte) error {
	var env envelope
	if err := json.Unmarshal(payload, &env); err != nil {
		return fmt.Errorf("live payload: %w", err)
	}
	if env.Character == "" {
		return errors.New("live payload without a character")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.chars == nil {
		l.chars = map[string]*liveCharacter{}
	}
	c := l.chars[env.Character]
	if c == nil {
		c = &liveCharacter{sections: map[string]liveSection{}}
		l.chars[env.Character] = c
	}
	if env.Full {
		if env.At < c.sent {
			return nil // a stale full snapshot would erase newer deltas
		}
		c.sections = map[string]liveSection{}
	}
	now := l.now()
	for name, data := range env.Sections {
		if old, ok := c.sections[name]; ok && old.sent > env.At {
			continue
		}
		c.sections[name] = liveSection{data: data, at: now, sent: env.At}
	}
	c.at = now
	c.sent = max(c.sent, env.At)
	l.last = env.Character
	return nil
}

// Get returns the character's merged live sections (the same shape as a snapshot: section
// name -> data), the server time the last payload for it arrived, and whether anything is
// known. An empty name means the character of the most recent payload.
func (l *LiveStore) Get(character string) (map[string]any, time.Time, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, c := l.lookup(character)
	if c == nil {
		return nil, time.Time{}, false
	}
	out := make(map[string]any, len(c.sections))
	for name, s := range c.sections {
		out[name] = s.data
	}
	return out, c.at, true
}

// Section returns one live section with the server time its data last changed.
func (l *LiveStore) Section(character, section string) (any, time.Time, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, c := l.lookup(character)
	if c == nil {
		return nil, time.Time{}, false
	}
	s, ok := c.sections[section]
	if !ok {
		return nil, time.Time{}, false
	}
	return s.data, s.at, true
}

// lookup resolves a character name: empty = the most recent payload's character; otherwise
// an exact match, then a unique case-insensitive prefix (so "monjits" finds "Monjits-Gpt").
func (l *LiveStore) lookup(character string) (string, *liveCharacter) {
	if character == "" {
		character = l.last
	}
	if c := l.chars[character]; c != nil {
		return character, c
	}
	name, found := "", (*liveCharacter)(nil)
	for n, c := range l.chars {
		if strings.HasPrefix(strings.ToLower(n), strings.ToLower(character)) {
			if found != nil {
				return "", nil // ambiguous
			}
			name, found = n, c
		}
	}
	return name, found
}
