package render

import (
	_ "embed"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"strings"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

//go:embed fonts/PressStart2P-Regular.ttf
var coverFontTTF []byte

const (
	coverSize   = 512
	coverInset  = 32
	coverFontPx = 14
	coverLineH  = 22
)

// WriteCover draws Planet's "_cover.png" for text-only posts: white text on
// a 512x512 black square. Planet uses the Capsules font; that license is
// unclear so this uses Press Start 2P (OFL). Covers imported from the Mac app
// are kept as they are, so their CIDs do not change.
func WriteCover(path, text string) error {
	f, err := opentype.Parse(coverFontTTF)
	if err != nil {
		return err
	}
	face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: coverFontPx, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		return err
	}
	defer face.Close()

	img := image.NewRGBA(image.Rect(0, 0, coverSize, coverSize))
	draw.Draw(img, img.Bounds(), image.Black, image.Point{}, draw.Src)
	d := &font.Drawer{Dst: img, Src: image.NewUniform(color.White), Face: face}

	maxWidth := coverSize - 2*coverInset
	y := coverInset + coverFontPx
	for _, line := range wrap(d, text, maxWidth) {
		if y > coverSize-coverInset {
			break
		}
		d.Dot = fixed.P(coverInset, y)
		d.DrawString(line)
		y += coverLineH
	}
	out, err := os.Create(path)
	if err != nil {
		return err
	}
	defer out.Close()
	return png.Encode(out, img)
}

func wrap(d *font.Drawer, text string, maxWidth int) []string {
	var lines []string
	for _, para := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		words := strings.Fields(para)
		if len(words) == 0 {
			lines = append(lines, "")
			continue
		}
		line := ""
		for _, w := range words {
			try := w
			if line != "" {
				try = line + " " + w
			}
			if d.MeasureString(try).Ceil() <= maxWidth {
				line = try
				continue
			}
			if line != "" {
				lines = append(lines, line)
			}
			// a single word wider than the box is cut hard
			for d.MeasureString(w).Ceil() > maxWidth && len(w) > 1 {
				n := len(w) - 1
				for n > 1 && d.MeasureString(w[:n]).Ceil() > maxWidth {
					n--
				}
				lines = append(lines, w[:n])
				w = w[n:]
			}
			line = w
		}
		lines = append(lines, line)
	}
	return lines
}
