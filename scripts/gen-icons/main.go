// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Command gen-icons draws the icon of GreenLens: a green lens with a leaf
// inside and an amber handle, on the dark green of the interface. It
// writes the SVG and the PNG sizes that browsers and desktops ask for,
// drawn from the same shapes with the standard library, so the output is
// the same on every machine. Run it from the repository root with
// "make icons".
package main

import (
	"bytes"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
)

// The shapes, in units of the side of the icon. The lens and the leaf stay
// inside the central 80%, the safe zone of maskable icons.
const (
	lensX, lensY, lensR, lensW = 0.44, 0.44, 0.22, 0.065
	handleX0, handleY0         = 0.60, 0.60
	handleX1, handleY1         = 0.76, 0.76
	handleW                    = 0.075
	cornerR                    = 0.22
)

var (
	background = color.NRGBA{0x0a, 0x1f, 0x17, 0xff}
	green      = color.NRGBA{0x34, 0xd3, 0x99, 0xff}
	amber      = color.NRGBA{0xf2, 0xa5, 0x41, 0xff}
)

const svg = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100">
  <!-- SPDX-License-Identifier: AGPL-3.0-only -->
  <!-- Copyright (C) 2026 Mario Ureña García -->
  <rect width="100" height="100" rx="22" fill="#0a1f17"/>
  <circle cx="44" cy="44" r="22" fill="none" stroke="#34d399" stroke-width="6.5"/>
  <path d="M33 51 C33 39 41 32 54 32 C54 45 46 52 35 52 Z" fill="#34d399"/>
  <path d="M33 53 L44 42" stroke="#0a1f17" stroke-width="2" stroke-linecap="round"/>
  <line x1="60" y1="60" x2="76" y2="76" stroke="#f2a541" stroke-width="7.5" stroke-linecap="round"/>
</svg>
`

// leaf reports whether a point lies in the leaf: the intersection of two
// discs, the shape of the curves of the SVG.
func leaf(x, y float64) bool {
	d1 := math.Hypot(x-0.50, y-0.48)
	d2 := math.Hypot(x-0.38, y-0.36)
	return d1 < 0.155 && d2 < 0.155 && !(math.Abs((x-0.33)-(0.53-y)) < 0.012 && x > 0.33 && x < 0.44)
}

func segmentDistance(x, y, x0, y0, x1, y1 float64) float64 {
	dx, dy := x1-x0, y1-y0
	t := math.Max(0, math.Min(1, ((x-x0)*dx+(y-y0)*dy)/(dx*dx+dy*dy)))
	return math.Hypot(x-(x0+t*dx), y-(y0+t*dy))
}

// shade returns the color of a point, or false outside the rounded square.
func shade(x, y float64) (color.NRGBA, bool) {
	cx, cy := math.Max(cornerR-x, math.Max(0, x-(1-cornerR))), math.Max(cornerR-y, math.Max(0, y-(1-cornerR)))
	if math.Hypot(cx, cy) > cornerR {
		return color.NRGBA{}, false
	}
	switch {
	case segmentDistance(x, y, handleX0, handleY0, handleX1, handleY1) < handleW/2:
		return amber, true
	case math.Abs(math.Hypot(x-lensX, y-lensY)-lensR) < lensW/2:
		return green, true
	case leaf(x, y):
		return green, true
	}
	return background, true
}

// draw renders the icon at size with 4 by 4 samples per pixel.
func draw(size int) *image.NRGBA {
	const ss = 4
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	for py := range size {
		for px := range size {
			var r, g, b, a float64
			for sy := range ss {
				for sx := range ss {
					x := (float64(px) + (float64(sx)+0.5)/ss) / float64(size)
					y := (float64(py) + (float64(sy)+0.5)/ss) / float64(size)
					if c, ok := shade(x, y); ok {
						r, g, b, a = r+float64(c.R), g+float64(c.G), b+float64(c.B), a+255
					}
				}
			}
			n := float64(ss * ss)
			if a == 0 {
				continue
			}
			// Colors are averaged over the covered samples only, so edges
			// fade in alpha instead of darkening.
			cov := a / 255
			img.SetNRGBA(px, py, color.NRGBA{uint8(r/cov + 0.5), uint8(g/cov + 0.5), uint8(b/cov + 0.5), uint8(a/n + 0.5)})
		}
	}
	return img
}

func main() {
	out := flag.String("out", "web/assets/icons", "output directory")
	flag.Parse()
	if err := run(*out); err != nil {
		fmt.Fprintln(os.Stderr, "gen-icons:", err)
		os.Exit(1)
	}
}

func run(out string) error {
	if err := os.MkdirAll(out, 0o755); err != nil { //nolint:gosec // repository tree
		return err
	}
	if err := os.WriteFile(filepath.Join(out, "icon.svg"), []byte(svg), 0o644); err != nil { //nolint:gosec // public asset
		return err
	}
	for _, size := range []int{192, 512} {
		var b bytes.Buffer
		if err := (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&b, draw(size)); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(out, fmt.Sprintf("icon-%d.png", size)), b.Bytes(), 0o644); err != nil { //nolint:gosec // public asset
			return err
		}
	}
	return nil
}
