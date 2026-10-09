package mobile

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Synthetic standards-based Display-P3 matrix profile (D50 PCS, sRGB TRC).
// No platform vendor profiles or private metadata are copied into this fixture.
func testP3Profile() []byte {
	fixed := func(v float64) []byte {
		b := make([]byte, 4)
		binary.BigEndian.PutUint32(b, uint32(int32(math.Round(v*65536))))
		return b
	}
	xyz := func(x, y, z float64) []byte {
		b := append([]byte("XYZ \x00\x00\x00\x00"), fixed(x)...)
		b = append(b, fixed(y)...)
		return append(b, fixed(z)...)
	}
	trc := []byte("para\x00\x00\x00\x00\x00\x04\x00\x00")
	for _, v := range []float64{2.4, 1 / 1.055, 0.055 / 1.055, 1 / 12.92, 0.04045, 0, 0} {
		trc = append(trc, fixed(v)...)
	}
	tags := []struct {
		name string
		data []byte
	}{
		{"wtpt", xyz(0.9642, 1, 0.8249)},
		{"rXYZ", xyz(0.515102, 0.241182, -0.001049)},
		{"gXYZ", xyz(0.291965, 0.692236, 0.041881)},
		{"bXYZ", xyz(0.157153, 0.066581, 0.784378)},
		{"rTRC", trc}, {"gTRC", trc}, {"bTRC", trc},
	}
	data := make([]byte, 132+12*len(tags))
	binary.BigEndian.PutUint32(data[8:], 0x04300000)
	copy(data[12:], "mntrRGB XYZ ")
	copy(data[36:], "acsp")
	copy(data[68:], fixed(0.9642))
	copy(data[72:], fixed(1))
	copy(data[76:], fixed(0.8249))
	binary.BigEndian.PutUint32(data[128:], uint32(len(tags)))
	for i, tag := range tags {
		entry := data[132+i*12 : 144+i*12]
		copy(entry, tag.name)
		binary.BigEndian.PutUint32(entry[4:], uint32(len(data)))
		binary.BigEndian.PutUint32(entry[8:], uint32(len(tag.data)))
		data = append(data, tag.data...)
	}
	binary.BigEndian.PutUint32(data, uint32(len(data)))
	return data
}

func pngProfile(data, profile []byte) []byte {
	var compressed bytes.Buffer
	z := zlib.NewWriter(&compressed)
	_, _ = z.Write(profile)
	_ = z.Close()
	return insertPNGChunk(data, "iCCP", append([]byte("Private profile name\x00\x00"), compressed.Bytes()...))
}

func TestImageColorMetadataBounds(t *testing.T) {
	profile := testP3Profile()
	if !validRGBICC(profile) {
		t.Fatal("invalid test profile")
	}
	profiled := pngProfile(fixturePNG(t), profile)
	profileChunk := profiled[33 : 33+12+int(binary.BigEndian.Uint32(profiled[33:37]))]
	if got, err := imageColorProfile(append(fixturePNG(t), profileChunk...), "png"); err != nil || got != nil {
		t.Fatalf("color metadata after IEND changed the image: %v", err)
	}
	for _, tc := range []struct {
		name string
		data []byte
		typ  string
		want error
	}{
		{"P3 PNG", pngProfile(fixturePNG(t), profile), "png", nil},
		{"malformed profile", pngProfile(fixturePNG(t), []byte("fake ICC")), "png", ErrImageColor},
		{"compressed profile bomb", pngProfile(fixturePNG(t), make([]byte, maxICCProfileBytes+1)), "png", ErrImageColor},
		{"duplicate profile", pngProfile(pngProfile(fixturePNG(t), profile), profile), "png", ErrImageColor},
		{"PQ PNG", insertPNGChunk(fixturePNG(t), "cICP", []byte{9, 16, 0, 1}), "png", ErrImageColor},
		{"HLG PNG", insertPNGChunk(fixturePNG(t), "cICP", []byte{9, 18, 0, 1}), "png", ErrImageColor},
		{"sRGB PNG", insertPNGChunk(fixturePNG(t), "cICP", []byte{1, 13, 0, 1}), "png", nil},
		{"mixed cICP ICC", pngProfile(insertPNGChunk(fixturePNG(t), "cICP", []byte{1, 13, 0, 1}), profile), "png", ErrImageColor},
		{"unknown gamma", insertPNGChunk(fixturePNG(t), "gAMA", []byte{0, 1, 0x86, 0xa0}), "png", ErrImageColor},
		{"gamma overridden by ICC", pngProfile(insertPNGChunk(fixturePNG(t), "gAMA", []byte{0, 1, 0x86, 0xa0}), profile), "png", nil},
		{"SDR content light metadata", insertPNGChunk(fixturePNG(t), "cLLI", make([]byte, 8)), "png", nil},
		{"invalid content light metadata", insertPNGChunk(fixturePNG(t), "cLLI", make([]byte, 7)), "png", ErrImageColor},
		{"unsupported mastering display", insertPNGChunk(fixturePNG(t), "mDCV", make([]byte, 24)), "png", ErrImageColor},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := imageColorProfile(tc.data, tc.typ)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
			if err == nil && strings.Contains(tc.name, "P3") && !bytes.Equal(got, profile) {
				t.Fatal("profile changed")
			}
		})
	}
	deep := image.NewNRGBA64(image.Rect(0, 0, 1, 1))
	deep.SetNRGBA64(0, 0, color.NRGBA64{0xffff, 1, 1, 0xffff})
	var encoded bytes.Buffer
	_ = png.Encode(&encoded, deep)
	in, out := mediaFixture(t, encoded.Bytes())
	if _, err := NormalizeImage(context.Background(), in, out); !errors.Is(err, ErrImageColor) {
		t.Fatalf("16-bit PNG accepted: %v", err)
	}
}

func TestImageColorJPEGAndWebPProfiles(t *testing.T) {
	profile := testP3Profile()
	segment := func(body []byte) []byte {
		n := len(body) + 2
		return append([]byte{0xff, 0xe2, byte(n >> 8), byte(n)}, body...)
	}
	part1 := append([]byte("ICC_PROFILE\x00\x01\x02"), profile[:200]...)
	part2 := append([]byte("ICC_PROFILE\x00\x02\x02"), profile[200:]...)
	jpeg := append([]byte{0xff, 0xd8}, segment(part2)...)
	jpeg = append(jpeg, segment(part1)...)
	got, err := imageColorProfile(jpeg, "jpeg")
	if err != nil || !bytes.Equal(got, profile) {
		t.Fatalf("out of order JPEG pieces: %v", err)
	}
	for _, bad := range [][]byte{append([]byte{0xff, 0xd8}, segment(part1)...), append(jpeg, segment(part1)...), append([]byte{0xff, 0xd8}, segment([]byte("MPF\x00private gain map"))...)} {
		if _, err := imageColorProfile(bad, "jpeg"); !errors.Is(err, ErrImageColor) {
			t.Fatalf("bad JPEG color metadata accepted: %v", err)
		}
	}
	webp := append([]byte("RIFF\x00\x00\x00\x00WEBPICCP"), make([]byte, 4)...)
	binary.LittleEndian.PutUint32(webp[16:], uint32(len(profile)))
	webp = append(webp, profile...)
	if len(profile)&1 != 0 {
		webp = append(webp, 0)
	}
	got, err = imageColorProfile(webp, "webp")
	if err != nil || !bytes.Equal(got, profile) {
		t.Fatalf("WebP ICC: %v", err)
	}
}

func TestImageColorHEIFRejectsHDR(t *testing.T) {
	property := func(tag string, body []byte) []byte {
		return append(heifBox("ftyp", []byte("heic\x00\x00\x00\x00heic")), heifBox("meta", append(make([]byte, 4), heifBox("iprp", heifBox("ipco", heifBox(tag, body)))...))...)
	}
	for _, tc := range []struct {
		name, tag string
		body      []byte
	}{
		{"PQ", "colr", append([]byte("nclx"), 0, 9, 0, 16, 0, 9, 0x80)},
		{"HLG", "colr", append([]byte("nclx"), 0, 9, 0, 18, 0, 9, 0x80)},
		{"10-bit pixels", "pixi", []byte{0, 0, 0, 0, 3, 10, 10, 10}},
		{"gain map", "auxC", []byte("\x00\x00\x00\x00urn:com:apple:photo:2020:aux:hdrgainmap")},
		{"P3 without profile", "colr", append([]byte("nclx"), 0, 12, 0, 13, 0, 0, 0x80)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := imageColorProfile(property(tc.tag, tc.body), "heif"); !errors.Is(err, ErrImageColor) {
				t.Fatalf("HDR accepted: %v", err)
			}
		})
	}
	hvcc := make([]byte, 23)
	hvcc[17] = 2
	if _, err := imageColorProfile(property("hvcC", hvcc), "heif"); !errors.Is(err, ErrImageColor) {
		t.Fatalf("HEVC 10bit accepted: %v", err)
	}
	profile := testP3Profile()
	if got, err := imageColorProfile(property("colr", append([]byte("prof"), profile...)), "heif"); err != nil || !bytes.Equal(got, profile) {
		t.Fatalf("HEIF profile: %v", err)
	}
}

func TestColorConverterUnavailableFailsClosed(t *testing.T) {
	t.Setenv("CROPTOP_COLOR_CONVERTER", "disabled")
	in, out := mediaFixture(t, pngProfile(fixturePNG(t), testP3Profile()))
	if _, err := NormalizeImage(context.Background(), in, out); !errors.Is(err, ErrColorUnavailable) {
		t.Fatalf("ICC dropped without conversion: %v", err)
	}
}

func TestColorRealP3Transform(t *testing.T) {
	if _, err := findColorConverter(); err != nil {
		if os.Getenv("CROPTOP_REQUIRE_COLOR_CONVERTER") == "1" {
			t.Fatal("production color converter missing")
		}
		t.Skip("dedicated color converter not installed")
	}
	im := image.NewNRGBA(image.Rect(0, 0, 3, 1))
	im.SetNRGBA(0, 0, color.NRGBA{180, 100, 100, 255})
	im.SetNRGBA(1, 0, color.NRGBA{100, 180, 100, 97})
	im.SetNRGBA(2, 0, color.NRGBA{100, 100, 180, 0})
	var original bytes.Buffer
	_ = png.Encode(&original, im)
	input := pngProfile(original.Bytes(), testP3Profile())
	input = insertPNGChunk(input, "tEXt", []byte("GPS\x00Private home address"))
	input = insertPNGChunk(input, "eXIf", exifTIFF(6))
	in, out := mediaFixture(t, input)
	media, err := NormalizeImage(context.Background(), in, out)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := os.ReadFile(media.Path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte("iCCP")) || bytes.Contains(encoded, []byte("Private")) || bytes.Contains(encoded, []byte("tEXt")) {
		t.Fatal("private source metadata survived")
	}
	result, err := png.Decode(bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	// Independent D65 Display-P3 -> linear sRGB matrix, then sRGB transfer.
	if media.Width != 1 || media.Height != 3 {
		t.Fatalf("profile conversion lost orientation: %+v", media)
	}
	linear := func(v byte) float64 {
		x := float64(v) / 255
		if x <= 0.04045 {
			return x / 12.92
		}
		return math.Pow((x+0.055)/1.055, 2.4)
	}
	srgb := func(v float64) int {
		v = math.Max(0, math.Min(1, v))
		if v <= 0.0031308 {
			return int(math.Round(v * 12.92 * 255))
		}
		return int(math.Round((1.055*math.Pow(v, 1/2.4) - 0.055) * 255))
	}
	for x := 0; x < 3; x++ {
		before := im.NRGBAAt(x, 0)
		got := color.NRGBAModel.Convert(result.At(0, x)).(color.NRGBA)
		if got.A != before.A {
			t.Fatalf("alpha changed at %d: %d != %d", x, got.A, before.A)
		}
		if before.A == 0 {
			continue
		}
		r, g, b := linear(before.R), linear(before.G), linear(before.B)
		want := []int{srgb(1.224745*r - 0.224904*g), srgb(-0.042058*r + 1.042081*g), srgb(-0.019642*r - 0.078655*g + 1.098537*b)}
		for i, channel := range []byte{got.R, got.G, got.B} {
			if math.Abs(float64(int(channel)-want[i])) > 2 {
				t.Fatalf("P3 pixel %d: got %v want %v", x, got, want)
			}
		}
	}
	files, _ := os.ReadDir(out)
	if len(files) != 1 {
		t.Fatalf("private intermediates leaked: %v", files)
	}
	// A structurally valid profile with no usable TRCs must not fall back to
	// an assumed sRGB transform. Corrupt every curve's type, not its bounds.
	bad := testP3Profile()
	for start := 0; ; {
		i := bytes.Index(bad[start:], []byte("para"))
		if i < 0 {
			break
		}
		i += start
		copy(bad[i:i+4], "nope")
		start = i + 4
	}
	in2 := filepath.Join(filepath.Dir(in), "bad.png")
	if err := os.WriteFile(in2, pngProfile(original.Bytes(), bad), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := NormalizeImage(context.Background(), in2, out); !errors.Is(err, ErrImageColor) {
		t.Fatalf("malformed color profile silently fell back: %v", err)
	}
}

func TestColorConverterBoundsAndCleanup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX converter fixture")
	}
	dir := t.TempDir()
	converter := filepath.Join(dir, "croptop-color")
	t.Setenv("CROPTOP_COLOR_CONVERTER", converter)
	for _, tc := range []struct {
		name, script string
		want         error
	}{
		{"success", "exec /bin/cp \"$1\" \"$3\"", nil},
		{"bad output", "printf 'broken' > \"$3\"", ErrInvalidImage},
		{"symlink", "exec /bin/ln -s \"$1\" \"$3\"", ErrImageColor},
		{"conversion failure", "exit 1", ErrImageColor},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(converter, []byte("#!/bin/sh\n"+tc.script+"\n"), 0700); err != nil {
				t.Fatal(err)
			}
			in, out := mediaFixture(t, pngProfile(fixturePNG(t), testP3Profile()))
			_, err := NormalizeImage(context.Background(), in, out)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
			files, _ := os.ReadDir(out)
			for _, f := range files {
				if strings.HasPrefix(f.Name(), ".") {
					t.Fatalf("staging leaked: %v", files)
				}
			}
		})
	}
	if err := os.WriteFile(converter, []byte("#!/bin/sh\n/bin/sleep 10 &\nwait\n"), 0700); err != nil {
		t.Fatal(err)
	}
	in, out := mediaFixture(t, pngProfile(fixturePNG(t), testP3Profile()))
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := NormalizeImage(ctx, in, out); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline lost: %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("color converter outlived deadline")
	}
	files, _ := os.ReadDir(out)
	if len(files) != 0 {
		t.Fatalf("cancelled transform leaked private files: %v", files)
	}
}

func TestImageColorEXIFUncalibrated(t *testing.T) {
	data := append(exifTIFF(1), make([]byte, 18)...)
	// Replace the orientation IFD with its EXIF child pointer.
	binary.LittleEndian.PutUint16(data[10:], 0x8769)
	binary.LittleEndian.PutUint16(data[12:], 4)
	binary.LittleEndian.PutUint32(data[18:], 26)
	binary.LittleEndian.PutUint16(data[26:], 1)
	binary.LittleEndian.PutUint16(data[28:], 0xa001)
	binary.LittleEndian.PutUint16(data[30:], 3)
	binary.LittleEndian.PutUint32(data[32:], 1)
	binary.LittleEndian.PutUint16(data[36:], 0xffff)
	if !tiffNonSRGB(data) {
		t.Fatal("explicit uncalibrated EXIF accepted without ICC")
	}
	binary.LittleEndian.PutUint16(data[36:], 1)
	if tiffNonSRGB(data) {
		t.Fatal("sRGB EXIF rejected")
	}
}

func TestNormalizeRejectsCMYKJPEG(t *testing.T) {
	// A complete four-component baseline frame header is enough for Go's
	// DecodeConfig to identify CMYK before any pixel buffers are allocated.
	data := []byte{0xff, 0xd8, 0xff, 0xc0, 0, 20, 8, 0, 1, 0, 1, 4, 'C', 0x11, 0, 'M', 0x11, 0, 'Y', 0x11, 0, 'K', 0x11, 0,
		0xff, 0xda, 0, 14, 4, 'C', 0, 'M', 0, 'Y', 0, 'K', 0, 0, 63, 0, 0xff, 0xd9}
	in, out := mediaFixture(t, data)
	if _, err := NormalizeImage(context.Background(), in, out); !errors.Is(err, ErrImageColor) {
		t.Fatalf("CMYK did not fail closed: %v", err)
	}
}

func TestImageColorConvertedPNGInspection(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
		want error
	}{
		{"profile", pngProfile(fixturePNG(t), testP3Profile()), nil},
		{"HDR", insertPNGChunk(fixturePNG(t), "cICP", []byte{9, 16, 0, 1}), ErrImageColor},
		{"truncated", []byte("\x89PNG\r\n\x1a\n"), ErrInvalidImage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, _ := mediaFixture(t, tc.data)
			f, err := os.Open(in)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			got, err := convertedPNGColorProfile(f)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
			if tc.name == "profile" && !bytes.Equal(got, testP3Profile()) {
				t.Fatal("converted profile dropped")
			}
		})
	}
}
