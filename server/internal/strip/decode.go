// SPDX-License-Identifier: GPL-3.0-or-later
// Forever Suite fork addition (port of wow-seer's frame decoder, src/frame.ts).

// Package strip is the realtime outbound channel from the game: the addon paints a pixel
// strip into the top-left corner of the screen and takes a screenshot; the server decodes
// the strip from the PNG, assembles multi-part messages and keeps the latest live state
// per character. The strip layout (all units are strip pixels before UI scaling):
//
//   - sync column at x=0..3: 4 px black/white blocks down the whole strip (vertical fit);
//   - sync row at y=0..3, x=4..67: 16 alternating 4 px blocks (horizontal fit);
//   - header at x=68: 54 cells of 4 px, 1 bit per channel = 20 bytes
//     (magic "WM", version, bits, cell, msgId u16, part, parts, payloadLen u16,
//     cols u16, rows u8, Adler-32 big-endian, a zero byte, byte-sum mod 256);
//   - tail alternators from x=284 to the right edge (fit refinement);
//   - payload at origin (4,8), row-major, `cell` px pitch, 2 bits per channel = 6 bits
//     per cell, colour levels {0, 1/3, 2/3, 1} quantized with round-half-even.
package strip

import (
	"errors"
	"fmt"
	"hash/adler32"
	"image"
	"math"
)

const (
	headerX     = 68
	headerCells = 54
	headerBytes = 20
	tailX       = headerX + 4*headerCells // 284
	payloadX    = 4
	payloadY    = 8
)

// NotStrip says the image is not a strip at all (a player's own screenshot): the caller
// must leave the file alone. Any other decode error means a broken strip from the addon.
var NotStrip = errors.New("not a strip image")

// Frame is one decoded strip: one part of a message.
type Frame struct {
	MsgID   int
	Part    int
	Parts   int
	Payload []byte
}

// header is the decoded 20-byte strip header.
type header struct {
	version, bits, cell, msg, part, parts, length, cols, rows int
	adler                                                     uint32
}

// pixels reads 8-bit channels from an image, reporting out-of-view reads.
type pixels struct {
	img image.Image
	b   image.Rectangle
}

func newPixels(img image.Image) pixels { return pixels{img: img, b: img.Bounds()} }

// at returns the 8-bit RGB of the pixel (x, y counted from the top-left corner);
// ok is false outside the image, which callers treat as black (like frame.ts's null pixel).
func (p pixels) at(x, y int) (r, g, b int, ok bool) {
	if x < 0 || y < 0 || x >= p.b.Dx() || y >= p.b.Dy() {
		return 0, 0, 0, false
	}
	r16, g16, b16, _ := p.img.At(p.b.Min.X+x, p.b.Min.Y+y).RGBA()
	return int(r16 >> 8), int(g16 >> 8), int(b16 >> 8), true
}

// white says all three channels are above 127; an out-of-view pixel is not white.
func (p pixels) white(x, y int) bool {
	r, g, b, ok := p.at(x, y)
	return ok && min(r, g, b) > 127
}

func trunc(v float64) int { return int(math.Trunc(v)) }

// roundHalfEven rounds to the nearest integer, ties to the even one — the same rule the
// client's 3..252 colour remap survives (frame.ts roundHalfEven).
func roundHalfEven(v float64) int {
	f := math.Floor(v)
	d := v - f
	switch {
	case d > 0.5:
		return int(f) + 1
	case d < 0.5:
		return int(f)
	case int(f)%2 == 0:
		return int(f)
	default:
		return int(f) + 1
	}
}

// fit is the least-squares line pixel = scale*unit + offset through the (unit, pixel) points.
func fit(units, pixelsAt []float64) (scale, offset float64) {
	n := float64(len(units))
	var mu, mp float64
	for i := range units {
		mu += units[i]
		mp += pixelsAt[i]
	}
	mu /= n
	mp /= n
	var num, den float64
	for i := range units {
		num += (units[i] - mu) * (pixelsAt[i] - mp)
		den += (units[i] - mu) * (units[i] - mu)
	}
	scale = num / den
	return scale, mp - scale*mu
}

// edgesRow finds up to count black/white transitions along row y, starting white at x=0.
func (p pixels) edgesRow(y, count int) []float64 {
	var found []float64
	state := true
	for i := 1; i < p.b.Dx() && len(found) < count; i++ {
		if s := p.white(i, y); s != state {
			found = append(found, float64(i))
			state = s
		}
	}
	return found
}

// edgesCol finds up to count transitions down column x, starting white at y=0.
func (p pixels) edgesCol(x, count int) []float64 {
	var found []float64
	state := true
	for i := 1; i < p.b.Dy() && len(found) < count; i++ {
		if s := p.white(x, i); s != state {
			found = append(found, float64(i))
			state = s
		}
	}
	return found
}

// nearestEdge looks for a transition within ±3 px of guess along row y.
func (p pixels) nearestEdge(guess, y int) (int, bool) {
	for _, d := range [...]int{0, -1, 1, -2, 2, -3, 3} {
		i := guess + d
		if i >= 1 && p.white(i, y) != p.white(i-1, y) {
			return i, true
		}
	}
	return 0, false
}

// Decode reads one strip frame out of a screenshot. It returns NotStrip when the image is
// not a strip (check with errors.Is); any other error is a broken strip.
func Decode(img image.Image) (Frame, error) {
	p := newPixels(img)
	if !p.white(0, 1) {
		return Frame{}, NotStrip
	}
	ex := p.edgesRow(1, 16)
	if len(ex) < 16 {
		return Frame{}, NotStrip
	}
	ux := make([]float64, 16)
	for j := range ux {
		ux[j] = float64(4 * (j + 1))
	}
	sx, x0 := fit(ux, ex)
	worst := 0.0
	for i := range ux {
		worst = math.Max(worst, math.Abs(x0+sx*ux[i]-ex[i]))
	}
	if !(sx > 0.3 && sx < 4) || worst > 1.5 {
		return Frame{}, NotStrip
	}
	h, err := p.readHeader(sx, x0)
	if err != nil {
		return Frame{}, err
	}
	width := max(payloadX+h.cols*h.cell, tailX+32)
	sx, x0 = p.refineTail(width, ux, ex, sx, x0)
	height := payloadY + h.rows*h.cell
	sy, y0, err := p.fitColumn(height, sx, x0)
	if err != nil {
		return Frame{}, err
	}
	payload, err := p.readPayload(h, sx, x0, sy, y0)
	if err != nil {
		return Frame{}, err
	}
	if adler32.Checksum(payload) != h.adler {
		return Frame{}, errors.New("payload checksum")
	}
	return Frame{MsgID: h.msg, Part: h.part, Parts: h.parts, Payload: payload}, nil
}

// readHeader samples the 54 header cells at 1 bit per channel and decodes the 20 bytes.
// A wrong magic is NotStrip (any screenshot can pass the sync checks by accident).
func (p pixels) readHeader(sx, x0 float64) (header, error) {
	bits := make([]int, 0, headerCells*3)
	y := trunc(2 * sx)
	for k := 0; k < headerCells; k++ {
		r, g, b, ok := p.at(trunc(x0+float64(headerX+2+4*k)*sx), y)
		if !ok {
			return header{}, errors.New("header out of view")
		}
		for _, v := range [...]int{r, g, b} {
			bit := 0
			if v > 127 {
				bit = 1
			}
			bits = append(bits, bit)
		}
	}
	h := make([]int, headerBytes)
	for i := range h {
		for j := 0; j < 8; j++ {
			h[i] = h[i]<<1 | bits[i*8+j]
		}
	}
	if h[0] != 'W' || h[1] != 'M' {
		return header{}, NotStrip
	}
	sum := 0
	for _, b := range h[:headerBytes-1] {
		sum += b
	}
	if sum%256 != h[headerBytes-1] {
		return header{}, errors.New("header checksum")
	}
	hd := header{version: h[2], bits: h[3], cell: h[4], msg: h[5]*256 + h[6], part: h[7], parts: h[8],
		length: h[9]*256 + h[10], cols: h[11]*256 + h[12], rows: h[13],
		adler: uint32(h[14])<<24 | uint32(h[15])<<16 | uint32(h[16])<<8 | uint32(h[17])}
	if hd.version != 1 || hd.bits != 2 {
		return header{}, fmt.Errorf("unsupported frame version %d bits %d", hd.version, hd.bits)
	}
	if hd.cell < 1 || hd.cols < 1 || hd.rows < 1 {
		return header{}, fmt.Errorf("empty frame geometry: cell %d cols %d rows %d", hd.cell, hd.cols, hd.rows)
	}
	return hd, nil
}

// refineTail sharpens the horizontal fit with the alternator edges right of the header.
func (p pixels) refineTail(width int, ux, ex []float64, sx, x0 float64) (float64, float64) {
	for m := 1; m < (width-tailX)/4; m++ {
		u := tailX + 4*m
		guess := roundHalfEven(x0 + float64(u)*sx)
		if e, ok := p.nearestEdge(guess, 1); ok {
			ux = append(ux, float64(u))
			ex = append(ex, float64(e))
			sx, x0 = fit(ux, ex)
		}
	}
	return sx, x0
}

// fitColumn fits the vertical scale from the sync column's block edges.
func (p pixels) fitColumn(height int, sx, x0 float64) (sy, y0 float64, err error) {
	nv := (height + 3) / 4
	ey := p.edgesCol(trunc(x0+2*sx), nv-1)
	if len(ey) < nv-1 {
		return 0, 0, errors.New("sync column cut")
	}
	uy := make([]float64, nv-1)
	for j := range uy {
		uy[j] = float64(4 * (j + 1))
	}
	sy, y0 = fit(uy, ey)
	return sy, y0, nil
}

// readPayload samples the payload cell centers: 4 cells of 6 bits = one 3-byte group.
func (p pixels) readPayload(h header, sx, x0, sy, y0 float64) ([]byte, error) {
	groups := (h.length + 2) / 3
	out := make([]byte, groups*3)
	for g := 0; g < groups; g++ {
		v := 0
		for c := 0; c < 4; c++ {
			n := 4*g + c
			x := trunc(x0 + (payloadX+(float64(n%h.cols)+0.5)*float64(h.cell))*sx)
			y := trunc(y0 + (payloadY+(float64(n/h.cols)+0.5)*float64(h.cell))*sy)
			r, gg, b, ok := p.at(x, y)
			if !ok {
				return nil, errors.New("payload out of view")
			}
			for _, ch := range [...]int{r, gg, b} {
				v = v<<2 | min(3, roundHalfEven(float64(ch)*3/255))
			}
		}
		out[3*g] = byte(v >> 16)
		out[3*g+1] = byte(v >> 8)
		out[3*g+2] = byte(v)
	}
	return out[:h.length], nil
}
