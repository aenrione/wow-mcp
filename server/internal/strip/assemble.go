// SPDX-License-Identifier: GPL-3.0-or-later
// Forever Suite fork addition (port of wow-seer's Assembler and messageText, src/frame.ts + server.ts).

package strip

import (
	"bytes"
	"compress/flate"
	"errors"
	"fmt"
	"hash/adler32"
	"io"
)

const (
	// keepMessages is how many incomplete messages stay in flight; older ones are dropped.
	keepMessages = 8
	// recentFrames is the duplicate-suppression window: the addon holds a strip up long
	// enough for several screenshots, so the same frame arrives more than once.
	recentFrames = 64
	// sliced marks a payload of 3-byte-length-prefixed raw-deflate slices ('Y').
	sliced = 0x59
)

// Assembler joins the parts of multi-part messages and drops repeated frames.
// It is not safe for concurrent use; the Watcher drives it from one goroutine.
type Assembler struct {
	parts  map[string]map[int][]byte
	order  []string // message keys in arrival order, for eviction
	recent map[string]bool
	seen   []string // recent keys in arrival order, for eviction
}

func NewAssembler() *Assembler {
	return &Assembler{parts: map[string]map[int][]byte{}, recent: map[string]bool{}}
}

// Add takes one decoded frame. When it completes its message, the full payload (the
// concatenated parts, before inflation) is returned with done=true. Frames already seen
// (same message, part and payload) are ignored.
func (a *Assembler) Add(f Frame) (payload []byte, done bool) {
	dup := fmt.Sprintf("%d/%d/%d/%d", f.MsgID, f.Part, f.Parts, adler32.Checksum(f.Payload))
	if a.recent[dup] {
		return nil, false
	}
	a.recent[dup] = true
	a.seen = append(a.seen, dup)
	for len(a.seen) > recentFrames {
		delete(a.recent, a.seen[0])
		a.seen = a.seen[1:]
	}
	if f.Part < 1 || f.Part > f.Parts {
		return nil, false
	}
	key := fmt.Sprintf("%d/%d", f.MsgID, f.Parts)
	got := a.parts[key]
	if got == nil {
		got = map[int][]byte{}
		a.parts[key] = got
		a.order = append(a.order, key)
	}
	got[f.Part] = f.Payload
	if len(got) == f.Parts {
		a.drop(key)
		var all []byte
		for i := 1; i <= f.Parts; i++ {
			all = append(all, got[i]...)
		}
		return all, true
	}
	for len(a.parts) > keepMessages {
		a.drop(a.order[0])
	}
	return nil, false
}

// drop forgets one in-flight message.
func (a *Assembler) drop(key string) {
	delete(a.parts, key)
	for i, k := range a.order {
		if k == key {
			a.order = append(a.order[:i], a.order[i+1:]...)
			break
		}
	}
}

// Message turns an assembled payload into JSON text: a payload starting with 'Y' is a
// sequence of [3-byte big-endian length][raw-deflate slice]; anything else is plain UTF-8 JSON.
func Message(payload []byte) ([]byte, error) {
	if len(payload) == 0 || payload[0] != sliced {
		return payload, nil
	}
	var out []byte
	for i := 1; i < len(payload); {
		if i+3 > len(payload) {
			return nil, errors.New("cut slice header")
		}
		n := int(payload[i])<<16 | int(payload[i+1])<<8 | int(payload[i+2])
		i += 3
		if i+n > len(payload) {
			return nil, errors.New("cut slice")
		}
		r := flate.NewReader(bytes.NewReader(payload[i : i+n]))
		data, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			return nil, fmt.Errorf("inflate slice at %d: %w", i, err)
		}
		out = append(out, data...)
		i += n
	}
	return out, nil
}
