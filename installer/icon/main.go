// Wraps the original Croptop scissors PNG artwork in a Windows ICO.
// icon.png is exported from Croptop.icns using iconutil. Run from the repo root:
// go run ./installer/icon
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image/png"
	"os"
)

func main() {
	data, err := os.ReadFile("installer/icon.png")
	must(err)
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	must(err)
	width := cfg.Width
	if width < 1 || width > 256 || width != cfg.Height {
		panic("icon.png must be square and at most 256px")
	}
	best := data
	// ICO supports an unmodified PNG payload. A zero dimension represents 256px.
	header := make([]byte, 22)
	binary.LittleEndian.PutUint16(header[2:4], 1)
	binary.LittleEndian.PutUint16(header[4:6], 1)
	header[6], header[7] = byte(width%256), byte(width%256)
	binary.LittleEndian.PutUint16(header[10:12], 1)
	binary.LittleEndian.PutUint16(header[12:14], 32)
	binary.LittleEndian.PutUint32(header[14:18], uint32(len(best)))
	binary.LittleEndian.PutUint32(header[18:22], 22)
	must(os.WriteFile("installer/Croptop.ico", append(header, best...), 0644))
	fmt.Printf("Exported original scissors artwork (%dpx) to ICO\n", width)
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
