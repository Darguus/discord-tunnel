// Command genicons renders the tray icons that internal/tray embeds.
//
// The icons are generated rather than committed as opaque binaries so that
// anyone reading this repository can see exactly what is in them, and change a
// colour without opening an image editor.
//
// Windows' LoadImage does not reliably decode PNG-compressed .ico files, so
// these are written in the classic 32bpp BMP-in-ICO form.
//
// Usage: go run ./tools/genicons
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"os"
	"path/filepath"
)

const size = 32

// The three states the tray can be in, and the colour each one shows.
var icons = map[string]color.RGBA{
	// Grey: the tunnel is off. Deliberately dim — nothing is being protected.
	"off": {R: 0x6E, G: 0x75, B: 0x7F, A: 0xFF},
	// Amber: connecting, or the watchdog is re-dialling after a drop.
	"connecting": {R: 0xF0, G: 0xB2, B: 0x32, A: 0xFF},
	// Green: traffic is confirmed flowing through the server.
	"on": {R: 0x3B, G: 0xA5, B: 0x5C, A: 0xFF},
	// Red: the tunnel failed and will not retry without the user.
	"error": {R: 0xD8, G: 0x3C, B: 0x3C, A: 0xFF},
}

func main() {
	outDir := filepath.Join("internal", "tray", "assets")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fail(err)
	}
	for name, c := range icons {
		ico, err := encodeICO(render(c))
		if err != nil {
			fail(err)
		}
		path := filepath.Join(outDir, name+".ico")
		if err := os.WriteFile(path, ico, 0o644); err != nil {
			fail(err)
		}
		fmt.Println("wrote", path)
	}
}

// render draws a filled disc with a lighter core, antialiased at the rim so the
// icon does not look jagged next to the other tray icons.
func render(c color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	draw.Draw(img, img.Bounds(), image.Transparent, image.Point{}, draw.Src)

	const (
		center = (size - 1) / 2.0
		radius = 14.0
	)
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			dx := float64(x) - center
			dy := float64(y) - center
			dist := math.Hypot(dx, dy)

			// Coverage falls from 1 to 0 across the last pixel of the rim, which is
			// what antialiases the edge.
			coverage := clamp(radius-dist, 0, 1)
			if coverage == 0 {
				continue
			}

			// A soft highlight toward the top-left gives the disc some dimension
			// instead of reading as a flat sticker.
			highlight := clamp((radius-dist)/radius, 0, 1) * 0.35
			px := color.RGBA{
				R: lift(c.R, highlight),
				G: lift(c.G, highlight),
				B: lift(c.B, highlight),
				A: uint8(coverage * 255),
			}
			img.SetRGBA(x, y, px)
		}
	}
	return img
}

func lift(v uint8, amount float64) uint8 {
	lifted := float64(v) + (255-float64(v))*amount
	return uint8(clamp(lifted, 0, 255))
}

func clamp(v, lo, hi float64) float64 {
	return math.Max(lo, math.Min(hi, v))
}

// encodeICO writes a single-image .ico in 32bpp BMP form.
func encodeICO(img *image.RGBA) ([]byte, error) {
	var xor bytes.Buffer
	// A BMP inside an ICO stores its rows bottom-up.
	for y := size - 1; y >= 0; y-- {
		for x := 0; x < size; x++ {
			c := img.RGBAAt(x, y)
			// BMP wants BGRA, and the pixels are already alpha-premultiplied by
			// image/draw, which is what Windows expects here.
			xor.Write([]byte{c.B, c.G, c.R, c.A})
		}
	}

	// The AND mask is legacy 1-bit transparency, superseded by the alpha channel
	// above. It must still be present and correctly sized, so it is all zeroes:
	// "no pixel is masked out".
	maskRowBytes := ((size + 31) / 32) * 4
	mask := make([]byte, maskRowBytes*size)

	var header bytes.Buffer
	// BITMAPINFOHEADER. The height is doubled because it must account for the
	// XOR bitmap and the AND mask stacked together — an ICO quirk that silently
	// corrupts the image if you get it wrong.
	writeAll(&header,
		uint32(40), int32(size), int32(size*2),
		uint16(1), uint16(32), uint32(0),
		uint32(xor.Len()+len(mask)),
		int32(0), int32(0), uint32(0), uint32(0),
	)

	imageBytes := header.Len() + xor.Len() + len(mask)

	var out bytes.Buffer
	// ICONDIR
	writeAll(&out, uint16(0), uint16(1), uint16(1))
	// ICONDIRENTRY
	out.Write([]byte{size, size, 0, 0})
	writeAll(&out,
		uint16(1), uint16(32),
		uint32(imageBytes),
		uint32(6+16), // the image starts right after ICONDIR + one ICONDIRENTRY
	)

	out.Write(header.Bytes())
	out.Write(xor.Bytes())
	out.Write(mask)
	return out.Bytes(), nil
}

func writeAll(buf *bytes.Buffer, values ...any) {
	for _, v := range values {
		if err := binary.Write(buf, binary.LittleEndian, v); err != nil {
			fail(err)
		}
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "genicons:", err)
	os.Exit(1)
}
