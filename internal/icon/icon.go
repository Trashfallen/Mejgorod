// Package icon рисует значок программы (кнопка питания на скруглённом квадрате)
// без внешних файлов: для exe, трея и favicon окна.
package icon

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"math"
)

type Palette struct{ Top, Bottom color.NRGBA }

var (
	On  = Palette{color.NRGBA{0x2d, 0xd4, 0xbf, 0xff}, color.NRGBA{0x0f, 0x76, 0x6e, 0xff}}
	Off = Palette{color.NRGBA{0xa1, 0xa8, 0xb3, 0xff}, color.NRGBA{0x4b, 0x52, 0x5e, 0xff}}
	Err = Palette{color.NRGBA{0xf8, 0x71, 0x71, 0xff}, color.NRGBA{0xb9, 0x1c, 0x1c, 0xff}}
)

const (
	gap   = 38 * math.Pi / 180 // половина разрыва кольца сверху
	ringR = 0.25
	cx    = 0.5
	cy    = 0.54
)

// Render рисует значок size x size с 4x4 суперсэмплингом.
func Render(size int, p Palette) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	s := float64(size)
	// на мелких размерах линии толще, иначе значок в трее расплывается
	t := 0.085
	if size <= 24 {
		t = 0.11
	}
	const ss = 4
	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			var bg, fg float64
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					x := (float64(px) + (float64(sx)+0.5)/ss) / s
					y := (float64(py) + (float64(sy)+0.5)/ss) / s
					if !inRoundRect(x, y, 0.03, 0.03, 0.97, 0.97, 0.22) {
						continue
					}
					bg++
					if inPower(x, y, t) {
						fg++
					}
				}
			}
			if bg == 0 {
				continue
			}
			a := bg / (ss * ss)
			f := fg / bg
			k := float64(py) / s
			base := [3]float64{
				lerp(float64(p.Top.R), float64(p.Bottom.R), k),
				lerp(float64(p.Top.G), float64(p.Bottom.G), k),
				lerp(float64(p.Top.B), float64(p.Bottom.B), k),
			}
			img.SetNRGBA(px, py, color.NRGBA{
				R: uint8(base[0]*(1-f) + 255*f + 0.5),
				G: uint8(base[1]*(1-f) + 255*f + 0.5),
				B: uint8(base[2]*(1-f) + 255*f + 0.5),
				A: uint8(a*255 + 0.5),
			})
		}
	}
	return img
}

func lerp(a, b, k float64) float64 { return a + (b-a)*k }

func inRoundRect(x, y, x0, y0, x1, y1, r float64) bool {
	if x < x0 || x > x1 || y < y0 || y > y1 {
		return false
	}
	dx := math.Max(math.Max(x0+r-x, 0), x-(x1-r))
	dy := math.Max(math.Max(y0+r-y, 0), y-(y1-r))
	return dx*dx+dy*dy <= r*r
}

func inPower(x, y, t float64) bool {
	h := t / 2
	// кольцо с разрывом сверху
	d := math.Hypot(x-cx, y-cy)
	if math.Abs(d-ringR) <= h && math.Abs(math.Atan2(x-cx, cy-y)) > gap {
		return true
	}
	// скруглённые концы кольца
	ex, ey := ringR*math.Sin(gap), cy-ringR*math.Cos(gap)
	if math.Hypot(x-(cx-ex), y-ey) <= h || math.Hypot(x-(cx+ex), y-ey) <= h {
		return true
	}
	// вертикальная черта со скруглёнными концами
	top, bottom := cy-ringR-0.045, cy-0.04
	if y >= top && y <= bottom && math.Abs(x-cx) <= h {
		return true
	}
	return math.Hypot(x-cx, y-top) <= h || math.Hypot(x-cx, y-bottom) <= h
}

// PNG кодирует значок в PNG.
func PNG(size int, p Palette) []byte {
	var b bytes.Buffer
	_ = png.Encode(&b, Render(size, p))
	return b.Bytes()
}

// ICO собирает .ico: мелкие размеры как 32-битные DIB (их надёжно понимает
// LoadImage для трея), 256 - как PNG.
func ICO(p Palette, sizes ...int) []byte {
	type entry struct {
		size int
		data []byte
	}
	var entries []entry
	for _, sz := range sizes {
		if sz >= 256 {
			entries = append(entries, entry{sz, PNG(sz, p)})
		} else {
			entries = append(entries, entry{sz, dib(Render(sz, p))})
		}
	}
	var b bytes.Buffer
	le := binary.LittleEndian
	_ = binary.Write(&b, le, [3]uint16{0, 1, uint16(len(entries))})
	offset := 6 + 16*len(entries)
	for _, e := range entries {
		dim := uint8(e.size)
		if e.size >= 256 {
			dim = 0
		}
		b.Write([]byte{dim, dim, 0, 0})
		_ = binary.Write(&b, le, uint16(1))  // planes
		_ = binary.Write(&b, le, uint16(32)) // bpp
		_ = binary.Write(&b, le, uint32(len(e.data)))
		_ = binary.Write(&b, le, uint32(offset))
		offset += len(e.data)
	}
	for _, e := range entries {
		b.Write(e.data)
	}
	return b.Bytes()
}

func dib(img *image.NRGBA) []byte {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	maskRow := ((w + 31) / 32) * 4
	var b bytes.Buffer
	le := binary.LittleEndian
	_ = binary.Write(&b, le, struct {
		Size                 uint32
		Width, Height        int32
		Planes, BitCount     uint16
		Compression, SizeImg uint32
		XPPM, YPPM           int32
		ClrUsed, ClrImp      uint32
	}{40, int32(w), int32(h * 2), 1, 32, 0, uint32(w*h*4 + maskRow*h), 0, 0, 0, 0})
	for y := h - 1; y >= 0; y-- {
		for x := 0; x < w; x++ {
			c := img.NRGBAAt(x, y)
			b.Write([]byte{c.B, c.G, c.R, c.A})
		}
	}
	b.Write(make([]byte, maskRow*h))
	return b.Bytes()
}
