// Package icon - значок программы из картинок в art/: человек с флагом на холме.
// Цвет флага показывает состояние: зелёный - подключено, белый - отключено,
// красный - ошибка. Картинки 256x256, мелкие размеры (трей) получаются
// уменьшением с усреднением по площади.
package icon

import (
	"bytes"
	_ "embed"
	"encoding/binary"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"sync"
)

// State - какую картинку взять.
type State int

const (
	On  State = iota // подключено: зелёный флаг
	Off              // отключено: белый флаг
	Err              // ошибка: красный флаг
)

var (
	//go:embed art/connected.png
	connectedPNG []byte
	//go:embed art/disconnected.png
	disconnectedPNG []byte
	//go:embed art/error.png
	errorPNG []byte

	srcOnce sync.Once
	src     [3]*image.NRGBA
)

func source(s State) *image.NRGBA {
	srcOnce.Do(func() {
		for i, b := range [][]byte{connectedPNG, disconnectedPNG, errorPNG} {
			img, err := png.Decode(bytes.NewReader(b))
			if err != nil {
				panic("icon: " + err.Error())
			}
			n := image.NewNRGBA(img.Bounds())
			draw.Draw(n, n.Bounds(), img, img.Bounds().Min, draw.Src)
			src[i] = n
		}
	})
	return src[s]
}

// Render - значок size x size.
func Render(size int, s State) *image.NRGBA {
	return scale(source(s), size)
}

// scale уменьшает картинку усреднением по площади с учётом прозрачности:
// так пиксель-арт в 16 px остаётся узнаваемым, а края не темнеют.
func scale(img *image.NRGBA, size int) *image.NRGBA {
	sb := img.Bounds()
	dst := image.NewNRGBA(image.Rect(0, 0, size, size))
	fx, fy := float64(sb.Dx())/float64(size), float64(sb.Dy())/float64(size)
	for dy := 0; dy < size; dy++ {
		y0, y1 := float64(dy)*fy, float64(dy+1)*fy
		for dx := 0; dx < size; dx++ {
			x0, x1 := float64(dx)*fx, float64(dx+1)*fx
			var r, g, b, a, w float64
			for sy := int(y0); sy < int(math.Ceil(y1)) && sy < sb.Dy(); sy++ {
				wy := math.Min(y1, float64(sy+1)) - math.Max(y0, float64(sy))
				for sx := int(x0); sx < int(math.Ceil(x1)) && sx < sb.Dx(); sx++ {
					wx := math.Min(x1, float64(sx+1)) - math.Max(x0, float64(sx))
					c := img.NRGBAAt(sb.Min.X+sx, sb.Min.Y+sy)
					k, al := wx*wy, float64(c.A)/255
					r += float64(c.R) * al * k
					g += float64(c.G) * al * k
					b += float64(c.B) * al * k
					a += al * k
					w += k
				}
			}
			if a > 0 {
				dst.SetNRGBA(dx, dy, color.NRGBA{uint8(r/a + .5), uint8(g/a + .5), uint8(b/a + .5), uint8(a/w*255 + .5)})
			}
		}
	}
	return dst
}

// PNG кодирует значок в PNG.
func PNG(size int, s State) []byte {
	var b bytes.Buffer
	_ = png.Encode(&b, Render(size, s))
	return b.Bytes()
}

// ICO собирает .ico: мелкие размеры как 32-битные DIB (их надёжно понимает
// LoadImage для трея), 256 - как PNG.
func ICO(s State, sizes ...int) []byte {
	type entry struct {
		size int
		data []byte
	}
	var entries []entry
	for _, sz := range sizes {
		if sz >= 256 {
			entries = append(entries, entry{sz, PNG(sz, s)})
		} else {
			entries = append(entries, entry{sz, dib(Render(sz, s))})
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
