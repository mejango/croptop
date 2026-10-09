package mobile

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func mediaFixture(t *testing.T, data []byte) (string, string) {
	t.Helper()
	dir := t.TempDir()
	in := filepath.Join(dir, "upload.untrusted-name")
	if err := os.WriteFile(in, data, 0600); err != nil {
		t.Fatal(err)
	}
	return in, filepath.Join(dir, "normalized")
}

func fixturePNG(t *testing.T) []byte {
	t.Helper()
	im := image.NewNRGBA(image.Rect(0, 0, 3, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 3; x++ {
			im.SetNRGBA(x, y, color.NRGBA{uint8(30 + x*80), uint8(40 + y*100), 20, 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, im); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestNormalizeImagePNG(t *testing.T) {
	data := fixturePNG(t)
	// An ancillary text chunk and trailing script are not copied to publication.
	data = insertPNGChunk(data, "tEXt", []byte("GPS\x00private-location"))
	data = append(data, []byte("<script>private-caption</script>")...)
	in, out := mediaFixture(t, data)
	media, err := NormalizeImage(context.Background(), in, out)
	if err != nil {
		t.Fatal(err)
	}
	if media.Type != "image/png" || media.Width != 3 || media.Height != 2 {
		t.Fatalf("unexpected media: %+v", media)
	}
	normalized, err := os.ReadFile(media.Path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(normalized, []byte("private")) || bytes.Contains(normalized, []byte("tEXt")) {
		t.Fatal("metadata was retained")
	}
	hash := sha256.Sum256(normalized)
	if media.SHA256 != hex.EncodeToString(hash[:]) {
		t.Fatal("digest does not cover final image")
	}
	info, err := os.Stat(media.Path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("image not private: %v %v", info, err)
	}
	before, _ := png.Decode(bytes.NewReader(fixturePNG(t)))
	after, err := png.Decode(bytes.NewReader(normalized))
	if err != nil {
		t.Fatal(err)
	}
	for y := 0; y < 2; y++ {
		for x := 0; x < 3; x++ {
			if color.NRGBAModel.Convert(before.At(x, y)) != color.NRGBAModel.Convert(after.At(x, y)) {
				t.Fatalf("screenshot pixels changed at %d,%d", x, y)
			}
		}
	}
	again, err := NormalizeImage(context.Background(), in, out)
	if err != nil || again.SHA256 != media.SHA256 {
		t.Fatalf("normalization is not deterministic: %+v %v", again, err)
	}
	entries, _ := os.ReadDir(out)
	if len(entries) != 1 {
		t.Fatalf("temporary files leaked: %v", entries)
	}
}

func TestNormalizeImageEXIFOrientations(t *testing.T) {
	// Each table row lists the source pixel indexes in displayed order. Using
	// concrete expectations avoids reproducing the transform under test.
	want := [][]int{
		{0, 1, 2, 3, 4, 5}, {2, 1, 0, 5, 4, 3}, {5, 4, 3, 2, 1, 0}, {3, 4, 5, 0, 1, 2},
		{0, 3, 1, 4, 2, 5}, {3, 0, 4, 1, 5, 2}, {5, 2, 4, 1, 3, 0}, {2, 5, 1, 4, 0, 3},
	}
	for o := 1; o <= 8; o++ {
		t.Run(string(rune('0'+o)), func(t *testing.T) {
			data := insertPNGChunk(fixturePNG(t), "eXIf", exifTIFF(o))
			in, out := mediaFixture(t, data)
			media, err := NormalizeImage(context.Background(), in, out)
			if err != nil {
				t.Fatal(err)
			}
			encoded, _ := os.ReadFile(media.Path)
			if bytes.Contains(encoded, []byte("eXIf")) {
				t.Fatal("EXIF retained")
			}
			im, err := png.Decode(bytes.NewReader(encoded))
			if err != nil {
				t.Fatal(err)
			}
			w, h := 3, 2
			if o >= 5 {
				w, h = 2, 3
			}
			if media.Width != w || media.Height != h {
				t.Fatalf("wrong oriented dimensions: %+v", media)
			}
			for p, source := range want[o-1] {
				expected := color.NRGBA{uint8(30 + (source%3)*80), uint8(40 + (source/3)*100), 20, 255}
				if got := color.NRGBAModel.Convert(im.At(p%w, p/w)); got != expected {
					t.Fatalf("pixel %d: got %v want %v", p, got, expected)
				}
			}
		})
	}
}

func TestNormalizeImageJPEGAndWebP(t *testing.T) {
	im, _ := png.Decode(bytes.NewReader(fixturePNG(t)))
	var jpegBytes bytes.Buffer
	if err := jpeg.Encode(&jpegBytes, im, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	jpegData := jpegBytes.Bytes()
	exif := append([]byte("Exif\x00\x00"), exifTIFF(6)...)
	segment := []byte{0xff, 0xe1, byte((len(exif) + 2) >> 8), byte(len(exif) + 2)}
	segment = append(segment, exif...)
	jpegData = append(append(append([]byte{}, jpegData[:2]...), segment...), jpegData[2:]...)
	// Generated white 2x2 lossless WebP; keep the bytes inline to avoid requiring
	// an external encoder for the standard-format regression tests.
	webpData, err := base64.StdEncoding.DecodeString("UklGRh4AAABXRUJQVlA4TBEAAAAvAUAAAAfQ//73v/+BiOh/AAA=")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		data []byte
		w, h int
	}{
		{"JPEG", jpegData, 2, 3}, {"WebP", webpData, 2, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, out := mediaFixture(t, tc.data)
			media, err := NormalizeImage(context.Background(), in, out)
			if err != nil {
				t.Fatal(err)
			}
			if media.Width != tc.w || media.Height != tc.h {
				t.Fatalf("wrong dimensions: %+v", media)
			}
		})
	}
}

func TestNormalizeImageRejectsUntrustedInputs(t *testing.T) {
	oversizedHeader := append([]byte{}, fixturePNG(t)...)
	binary.BigEndian.PutUint32(oversizedHeader[16:20], 100_000)
	binary.BigEndian.PutUint32(oversizedHeader[20:24], 100_000)
	binary.BigEndian.PutUint32(oversizedHeader[29:33], crc32.ChecksumIEEE(oversizedHeader[12:29]))
	for _, tc := range []struct {
		name string
		data []byte
		want error
	}{
		{"HTML", []byte("<html><img src='x'></html>"), ErrUnsupportedImage},
		{"SVG", []byte("<svg xmlns='http://www.w3.org/2000/svg'></svg>"), ErrUnsupportedImage},
		{"GIF", []byte("GIF89a"), ErrUnsupportedImage},
		{"empty", nil, ErrUnsupportedImage},
		{"truncated PNG", []byte("\x89PNG\r\n\x1a\n"), ErrInvalidImage},
		{"corrupt JPEG", []byte{0xff, 0xd8, 0xff, 0xe0, 0, 100}, ErrInvalidImage},
		{"pixel bomb", oversizedHeader, ErrImagePixels},
		{"HEIF pixel bomb", fixtureHEIF(100_000, 100_000), ErrImagePixels},
		{"HEIF missing extents", heifBox("ftyp", []byte("heic\x00\x00\x00\x00heic")), ErrInvalidImage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, out := mediaFixture(t, tc.data)
			_, err := NormalizeImage(context.Background(), in, out)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
		})
	}
}

func TestNormalizeImageInputLimitAndCancellation(t *testing.T) {
	in, out := mediaFixture(t, fixturePNG(t))
	if err := os.Truncate(in, MaxImageBytes+1); err != nil {
		t.Fatal(err)
	}
	if _, err := NormalizeImage(context.Background(), in, out); !errors.Is(err, ErrImageTooLarge) {
		t.Fatalf("byte limit: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NormalizeImage(ctx, in, out); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("invalid request created output directory: %v", err)
	}
}

func TestNormalizeRejectsAnimatedPNG(t *testing.T) {
	control := make([]byte, 8)
	binary.BigEndian.PutUint32(control[:4], 2)
	data := insertPNGChunk(fixturePNG(t), "acTL", control)
	in, out := mediaFixture(t, data)
	if _, err := NormalizeImage(context.Background(), in, out); err == nil || !strings.Contains(err.Error(), "animated") {
		t.Fatalf("animated PNG accepted: %v", err)
	}
}

func TestHEIFConverterCapabilityAndFailure(t *testing.T) {
	t.Setenv("CROPTOP_HEIF_CONVERTER", "disabled")
	if strings.Contains(strings.Join(SupportedFormats(), ","), "heif") {
		t.Fatal("advertises unavailable HEIF")
	}
	in, out := mediaFixture(t, fixtureHEIF(3, 2))
	if _, err := NormalizeImage(context.Background(), in, out); !errors.Is(err, ErrHEIFUnavailable) {
		t.Fatalf("converter unavailable: %v", err)
	}
	files, _ := os.ReadDir(out)
	if len(files) != 0 {
		t.Fatal("unavailable converter left staging files")
	}
}

func TestHEIFConversionBoundsAndCleanup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX converter fixture")
	}
	dir := t.TempDir()
	converter := filepath.Join(dir, "heif-convert")
	pngPath := filepath.Join(dir, "safe.png")
	if err := os.WriteFile(pngPath, fixturePNG(t), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CROPTOP_HEIF_CONVERTER", converter)
	t.Setenv("CROPTOP_TEST_PNG", pngPath)
	for _, tc := range []struct {
		name, script string
		want         error
	}{
		{"success", "exec /bin/cp \"$CROPTOP_TEST_PNG\" \"$2\"", nil},
		{"bad output", "printf 'not a PNG' > \"$2\"", ErrInvalidImage},
		{"multiple images", "exec /bin/cp \"$CROPTOP_TEST_PNG\" \"${2%.png}-1.png\"", ErrUnsupportedImage},
		{"symlink", "exec /bin/ln -s \"$CROPTOP_TEST_PNG\" \"$2\"", ErrInvalidImage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(converter, []byte("#!/bin/sh\n"+tc.script+"\n"), 0700); err != nil {
				t.Fatal(err)
			}
			in, out := mediaFixture(t, fixtureHEIF(3, 2))
			media, err := NormalizeImage(context.Background(), in, out)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %+v %v want %v", media, err, tc.want)
			}
			files, _ := os.ReadDir(out)
			for _, f := range files {
				if strings.HasPrefix(f.Name(), ".") {
					t.Fatalf("staging leaked: %s", f.Name())
				}
			}
		})
	}
	// The background child inherits the converter's descriptors. Cancellation
	// must terminate it too, rather than waiting indefinitely on those pipes.
	if err := os.WriteFile(converter, []byte("#!/bin/sh\n/bin/sleep 10 &\nwait\n"), 0700); err != nil {
		t.Fatal(err)
	}
	in, out := mediaFixture(t, fixtureHEIF(3, 2))
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := NormalizeImage(ctx, in, out); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("converter deadline: %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("converter outlived deadline")
	}
	files, _ := os.ReadDir(out)
	if len(files) != 0 {
		t.Fatal("timed out converter retained draft copies")
	}
}

func TestHEIFLinuxResourceLimits(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux kernel resource limits")
	}
	dir := t.TempDir()
	converter, source := filepath.Join(dir, "heif-convert"), filepath.Join(dir, "source.png")
	if err := os.WriteFile(source, fixturePNG(t), 0600); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\n" +
		"test \"$(ulimit -v)\" -eq 1048576 && test \"$(ulimit -t)\" -eq 30 && " +
		"exec /bin/cp \"$CROPTOP_TEST_PNG\" \"$2\"\n"
	if err := os.WriteFile(converter, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CROPTOP_HEIF_CONVERTER", converter)
	t.Setenv("CROPTOP_TEST_PNG", source)
	in, out := mediaFixture(t, fixtureHEIF(3, 2))
	if _, err := NormalizeImage(context.Background(), in, out); err != nil {
		t.Fatalf("decoder did not receive resource limits: %v", err)
	}
}

// This exercises the actual installed decoder, not a shell fixture. The tiny
// white 32x16 HEIC was generated by Apple's ImageIO encoder from a local PNG.
// Device-produced SDR/HDR fixture acceptance remains separate.
func TestNormalizeRealHEIC(t *testing.T) {
	if _, err := findHEIFConverter(); err != nil {
		t.Skip("HEIF decoder not installed")
	}
	dir := t.TempDir()
	heicPath := filepath.Join(dir, "source.heic")
	data, err := base64.StdEncoding.DecodeString("AAAAJGZ0eXBoZWljAAAAAG1pZjFNaVBybWlhZk1pSEJoZWljAAABhm1ldGEAAAAAAAAAIWhkbHIAAAAAAAAAAHBpY3QAAAAAAAAAAAAAAAAAAAAAJGRpbmYAAAAcZHJlZgAAAAAAAAABAAAADHVybCAAAAABAAAADnBpdG0AAAAAAAEAAAAjaWluZgAAAAAAAQAAABVpbmZlAgAAAAABAABodmMxAAAAAOZpcHJwAAAAxWlwY28AAAATY29scm5jbHgAAgACAAaAAAAADGNsbGkAywBAAAAAFGlzcGUAAAAAAAAAIAAAABAAAAAJaXJvdAAAAAAQcGl4aQAAAAADCAgIAAAAcWh2Y0MBA3AAAACwAAAAAAAe8AD8/fj4AAALA6AAAQAXQAEMAf//A3AAAAMAsAAAAwAAAwAecCShAAEAI0IBAQNwAAADALAAAAMAAAMAHqAUIEHAgwziHuRZVNwICBgCogABAAlEAcBhcshAUyQAAAAZaXBtYQAAAAAAAAABAAEGgQIDhAWGAAAAHmlsb2MAAAAARAAAAQABAAAAAQAAAboAAAArAAAAAW1kYXQAAAAAAAAAOwAAACcoAa+ixkfsZdW/KaXaIf/+m40o45lf0CI62H/9gtNm1EaQ+PQI/mw=")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(heicPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	media, err := NormalizeImage(context.Background(), heicPath, filepath.Join(dir, "normalized"))
	if err != nil {
		t.Fatal(err)
	}
	if media.Width != 32 || media.Height != 16 || media.Type != "image/png" {
		t.Fatalf("HEIC conversion: %+v", media)
	}
	// HEIF uses a container transform, not only EXIF. Exercise that path through
	// the real decoder to catch preserving a rotation tag without rotating pixels.
	rotation := bytes.Index(data, []byte("irot"))
	if rotation < 0 {
		t.Fatal("fixture lacks rotation property")
	}
	data[rotation+4] = 1
	if err := os.WriteFile(heicPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	rotated, err := NormalizeImage(context.Background(), heicPath, filepath.Join(dir, "rotated"))
	if err != nil {
		t.Fatal(err)
	}
	if rotated.Width != 16 || rotated.Height != 32 {
		t.Fatalf("HEIC rotation lost: %+v", rotated)
	}
}

func TestEXIFParsersMalformed(t *testing.T) {
	for n := 0; n < 40; n++ {
		data := bytes.Repeat([]byte{0xff}, n)
		for _, typ := range []string{"png", "jpeg", "webp"} {
			if o := exifOrientation(data, typ); o != 1 {
				t.Fatalf("malformed EXIF orientation = %d", o)
			}
		}
		if tiffOrientation(data) != 1 {
			t.Fatal("malformed TIFF accepted")
		}
	}
	data := exifTIFF(6)
	binary.LittleEndian.PutUint32(data[4:8], 0xffffffff)
	if tiffOrientation(data) != 1 {
		t.Fatal("out-of-bounds IFD accepted")
	}
}

func insertPNGChunk(data []byte, typ string, payload []byte) []byte {
	chunk := make([]byte, 12+len(payload))
	binary.BigEndian.PutUint32(chunk[:4], uint32(len(payload)))
	copy(chunk[4:8], typ)
	copy(chunk[8:], payload)
	binary.BigEndian.PutUint32(chunk[8+len(payload):], crc32.ChecksumIEEE(chunk[4:8+len(payload)]))
	result := append([]byte{}, data[:33]...)
	result = append(result, chunk...)
	return append(result, data[33:]...)
}

func exifTIFF(orientation int) []byte {
	data := make([]byte, 26)
	copy(data, "II")
	binary.LittleEndian.PutUint16(data[2:4], 42)
	binary.LittleEndian.PutUint32(data[4:8], 8)
	binary.LittleEndian.PutUint16(data[8:10], 1)
	binary.LittleEndian.PutUint16(data[10:12], 0x112)
	binary.LittleEndian.PutUint16(data[12:14], 3)
	binary.LittleEndian.PutUint32(data[14:18], 1)
	binary.LittleEndian.PutUint16(data[18:20], uint16(orientation))
	return data
}

func heifBox(typ string, body []byte) []byte {
	data := make([]byte, 8+len(body))
	binary.BigEndian.PutUint32(data[:4], uint32(len(data)))
	copy(data[4:8], typ)
	copy(data[8:], body)
	return data
}

func fixtureHEIF(w, h uint32) []byte {
	ispe := make([]byte, 12)
	binary.BigEndian.PutUint32(ispe[4:8], w)
	binary.BigEndian.PutUint32(ispe[8:12], h)
	meta := append(make([]byte, 4), heifBox("iprp", heifBox("ipco", heifBox("ispe", ispe)))...)
	return append(heifBox("ftyp", []byte("heic\x00\x00\x00\x00heic")), heifBox("meta", meta)...)
}
