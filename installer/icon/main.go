// Draws the app icon: a pink square with a white pixel C. Run from the repo
// root: go run ./installer/icon
package main

import (
	"image"
	"image/color"
	"image/png"
	"os"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

func main() {
	ttf, err := os.ReadFile("internal/render/fonts/PressStart2P-Regular.ttf")
	if err != nil {
		panic(err)
	}
	f, err := opentype.Parse(ttf)
	if err != nil {
		panic(err)
	}
	const size = 1024
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	pink := color.RGBA{0xf0, 0x56, 0xc1, 0xff}
	// rounded square, like macOS icons
	r := size * 22 / 100
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			cx, cy := clamp(x, r, size-r), clamp(y, r, size-r)
			if (x-cx)*(x-cx)+(y-cy)*(y-cy) <= r*r {
				img.Set(x, y, pink)
			}
		}
	}
	face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: 560, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		panic(err)
	}
	d := &font.Drawer{Dst: img, Src: image.White, Face: face}
	w := d.MeasureString("C").Ceil()
	d.Dot = fixed.P((size-w)/2, size/2+230)
	d.DrawString("C")
	out, err := os.Create("installer/icon.png")
	if err != nil {
		panic(err)
	}
	defer out.Close()
	png.Encode(out, img)
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
