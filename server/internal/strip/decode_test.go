// SPDX-License-Identifier: GPL-3.0-or-later
// Forever Suite fork addition.

package strip

import (
	"bytes"
	"compress/flate"
	"errors"
	"hash/adler32"
	"image"
	"image/color"
	"strings"
	"testing"
)

// encodeHeader builds the 20 header bytes the way the Lua addon packs them.
func encodeHeader(msg, part, parts, cell, cols, rows, length int, adler uint32) []int {
	h := make([]int, headerBytes)
	h[0], h[1] = 'W', 'M'
	h[2], h[3], h[4] = 1, 2, cell
	h[5], h[6] = msg>>8, msg&255
	h[7], h[8] = part, parts
	h[9], h[10] = length>>8, length&255
	h[11], h[12] = cols>>8, cols&255
	h[13] = rows
	h[14], h[15], h[16], h[17] = int(adler>>24)&255, int(adler>>16)&255, int(adler>>8)&255, int(adler)&255
	h[18] = 0
	sum := 0
	for _, b := range h[:headerBytes-1] {
		sum += b
	}
	h[19] = sum % 256
	return h
}

// fillRect paints a rectangle of the image.
func fillRect(img *image.RGBA, x0, y0, x1, y1 int, c color.RGBA) {
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			img.SetRGBA(x, y, c)
		}
	}
}

var (
	blackPx = color.RGBA{0, 0, 0, 255}
	whitePx = color.RGBA{255, 255, 255, 255}
)

// renderStrip paints a strip image the way the addon's Lua does: sync column, sync row,
// header cells, tail alternators and the 2-bit payload cells at levels {0, 85, 170, 255}.
// The adler goes into the header as given, so tests can write a wrong one.
func renderStrip(msg, part, parts, cell, cols int, payload []byte, adler uint32) *image.RGBA {
	groups := (len(payload) + 2) / 3
	rows := (groups*4 + cols - 1) / cols
	width := max(payloadX+cols*cell, tailX+32)
	height := payloadY + rows*cell
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	fillRect(img, 0, 0, width, height, blackPx)
	for y := 0; y < height; y++ { // sync column: 4 px black/white blocks
		if (y/4)%2 == 0 {
			fillRect(img, 0, y, 4, y+1, whitePx)
		}
	}
	for x := payloadX; x < headerX; x++ { // sync row alternators
		if (x/4)%2 == 0 {
			fillRect(img, x, 0, x+1, 4, whitePx)
		}
	}
	for x := tailX; x < width; x++ { // tail alternators
		if (x/4)%2 == 0 {
			fillRect(img, x, 0, x+1, 4, whitePx)
		}
	}
	bits := make([]int, 0, headerCells*3)
	for _, b := range encodeHeader(msg, part, parts, cell, cols, rows, len(payload), adler) {
		for j := 7; j >= 0; j-- {
			bits = append(bits, (b>>j)&1)
		}
	}
	bits = append(bits, 0, 0) // 54 cells carry 162 bits; the last two are padding
	for k := 0; k < headerCells; k++ {
		c := color.RGBA{uint8(bits[3*k] * 255), uint8(bits[3*k+1] * 255), uint8(bits[3*k+2] * 255), 255}
		fillRect(img, headerX+4*k, 0, headerX+4*k+4, 4, c)
	}
	renderPayload(img, cell, cols, payload)
	return img
}

// renderPayload paints the payload cells: 3 bytes -> 24 bits -> 4 cells of 6 bits (2 per channel).
func renderPayload(img *image.RGBA, cell, cols int, payload []byte) {
	levels := [4]uint8{0, 85, 170, 255}
	for g := 0; g < (len(payload)+2)/3; g++ {
		v := 0
		for b := 0; b < 3; b++ {
			byteVal := 0
			if 3*g+b < len(payload) {
				byteVal = int(payload[3*g+b])
			}
			v = v<<8 | byteVal
		}
		for c := 0; c < 4; c++ {
			n := 4*g + c
			cellBits := (v >> (24 - 6*(c+1))) & 63
			colr := color.RGBA{levels[(cellBits>>4)&3], levels[(cellBits>>2)&3], levels[cellBits&3], 255}
			x, y := payloadX+(n%cols)*cell, payloadY+(n/cols)*cell
			fillRect(img, x, y, x+cell, y+cell, colr)
		}
	}
}

func render(msg, part, parts, cell, cols int, payload []byte) *image.RGBA {
	return renderStrip(msg, part, parts, cell, cols, payload, adler32.Checksum(payload))
}

func TestDecodeRoundTrip(t *testing.T) {
	payload := []byte(`{"v":1,"at":1764000000,"character":"Monjits-Gpt","sections":{"bags":{"free":5}}}`)
	for _, cell := range []int{1, 2, 3, 4} {
		frame, err := Decode(render(513, 1, 1, cell, 40, payload))
		if err != nil {
			t.Fatalf("cell %d: %v", cell, err)
		}
		if frame.MsgID != 513 || frame.Part != 1 || frame.Parts != 1 {
			t.Fatalf("cell %d: frame %+v", cell, frame)
		}
		if !bytes.Equal(frame.Payload, payload) {
			t.Fatalf("cell %d: payload %q", cell, frame.Payload)
		}
	}
}

// The client remaps colour levels {0,85,170,255} to {3,87,168,252}; round-half-even
// quantization must still decode every level exactly.
func TestDecodeClientRemap(t *testing.T) {
	payload := []byte(`{"v":1,"at":1,"character":"Monjits-Gpt","sections":{"character":{"level":12}}}`)
	img := render(7, 1, 1, 2, 30, payload)
	remap := map[uint8]uint8{0: 3, 85: 87, 170: 168, 255: 252}
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := img.RGBAAt(x, y)
			img.SetRGBA(x, y, color.RGBA{remap[c.R], remap[c.G], remap[c.B], 255})
		}
	}
	frame, err := Decode(img)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(frame.Payload, payload) {
		t.Fatalf("payload %q", frame.Payload)
	}
}

func TestDecodeNotStrip(t *testing.T) {
	dark := image.NewRGBA(image.Rect(0, 0, 400, 120)) // all black: the sync pixel is not white
	light := image.NewRGBA(image.Rect(0, 0, 400, 120))
	fillRect(light, 0, 0, 400, 120, whitePx) // all white: no sync transitions
	for name, img := range map[string]image.Image{"dark": dark, "light": light} {
		if _, err := Decode(img); !errors.Is(err, NotStrip) {
			t.Errorf("%s: expected NotStrip, got %v", name, err)
		}
	}
}

func TestDecodeHeaderChecksum(t *testing.T) {
	img := render(9, 1, 1, 2, 30, []byte(`{"v":1}`))
	fillRect(img, headerX+4*14, 0, headerX+4*14+4, 4, whitePx) // cell 14 flips bits of the msg byte
	_, err := Decode(img)
	if err == nil || !strings.Contains(err.Error(), "header checksum") {
		t.Fatalf("expected a header checksum error, got %v", err)
	}
}

func TestDecodePayloadChecksum(t *testing.T) {
	payload := []byte(`{"v":1,"at":1,"character":"X","sections":{}}`)
	img := render(9, 1, 1, 2, 30, payload)
	fillRect(img, payloadX, payloadY, payloadX+2, payloadY+2, whitePx) // repaint the first cell
	_, err := Decode(img)
	if err == nil || !strings.Contains(err.Error(), "payload checksum") {
		t.Fatalf("expected a payload checksum error, got %v", err)
	}
	if errors.Is(err, NotStrip) {
		t.Fatal("a broken strip must not be NotStrip")
	}
}

func TestAssembleTwoParts(t *testing.T) {
	text := []byte(`{"v":1,"at":2,"character":"Monjits-Gpt","sections":{"bags":{"free":1},"quests":[1,2,3]}}`)
	half := len(text) / 2
	a := NewAssembler()
	one, _ := Decode(render(100, 1, 2, 2, 32, text[:half]))
	two, _ := Decode(render(100, 2, 2, 2, 32, text[half:]))
	if _, done := a.Add(one); done {
		t.Fatal("one part must not complete a two-part message")
	}
	full, done := a.Add(two)
	if !done || !bytes.Equal(full, text) {
		t.Fatalf("done %v payload %q", done, full)
	}
	if _, done := a.Add(two); done { // the addon holds a strip up for several screenshots
		t.Fatal("a repeated frame must be suppressed")
	}
	msg, err := Message(full)
	if err != nil || !bytes.Equal(msg, text) {
		t.Fatalf("message %q err %v", msg, err)
	}
}

func TestAssemblerEvictsOldMessages(t *testing.T) {
	a := NewAssembler()
	for msg := 1; msg <= 9; msg++ { // 9 half-open messages; the first falls out at 8 in flight
		a.Add(Frame{MsgID: msg, Part: 1, Parts: 2, Payload: []byte{byte(msg)}})
	}
	if _, done := a.Add(Frame{MsgID: 1, Part: 2, Parts: 2, Payload: []byte{200}}); done {
		t.Fatal("message 1 was evicted; its late part must not complete it")
	}
	if _, done := a.Add(Frame{MsgID: 9, Part: 2, Parts: 2, Payload: []byte{201}}); !done {
		t.Fatal("message 9 is still in flight and must complete")
	}
}

// deflateRaw compresses data as one raw-deflate stream (no zlib header).
func deflateRaw(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := flate.NewWriter(&buf, flate.BestCompression)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestMessageDeflateSlices(t *testing.T) {
	text := []byte(`{"v":1,"at":3,"character":"Monjits-Gpt","sections":{"character":{"level":13,"zone":"Barrens"}}}`)
	half := len(text) / 2
	payload := []byte{'Y'}
	for _, slice := range [][]byte{text[:half], text[half:]} {
		packed := deflateRaw(t, slice)
		payload = append(payload, byte(len(packed)>>16), byte(len(packed)>>8), byte(len(packed)))
		payload = append(payload, packed...)
	}
	frame, err := Decode(render(42, 1, 1, 2, 32, payload))
	if err != nil {
		t.Fatal(err)
	}
	msg, err := Message(frame.Payload)
	if err != nil || !bytes.Equal(msg, text) {
		t.Fatalf("message %q err %v", msg, err)
	}
	if _, err := Message([]byte{'Y', 0, 0}); err == nil {
		t.Fatal("a cut slice header must error")
	}
}
