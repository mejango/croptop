package mobile

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"image/png"
	"mime/multipart"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mejango/croptop/internal/store"
)

// Only the encoded white 32x16 HEVC sample and codec configuration are reused
// from TestNormalizeRealHEIC's generated fixture. All item metadata below is
// synthetic; neither camera files nor their metadata are kept in the repository.
type testHEIFProperty struct {
	tag       string
	body      []byte
	essential bool
}

type testHEIFItem struct {
	id         uint16
	typ        string
	properties []testHEIFProperty
	data       []byte
}

type testHEIFReference struct {
	typ  string
	from uint16
	to   []uint16
}

type testPrimaryHEIF struct {
	primary            uint16
	items              []testHEIFItem
	refs               []testHEIFReference
	separateProperties bool
}

const (
	testHEVC64Unspecified = "AQFgAAAAkAAAAAAAHvAA/P34+AAADwOgAAEAGEABDAH//wFgAAADAJAAAAMAAAMAHpWUCaEAAQAsQgEBAWAAAAMAkAAAAwAAAwAeoCCBBZZWVKTC8BbgQEDAgAAAAwCAAAADAISiAAEABkQBwHPAiQ=="
	testHEVC64BT2020      = "AQFgAAAAkAAAAAAAHvAA/P34+AAADwOgAAEAGEABDAH//wFgAAADAJAAAAMAAAMAHpWUCaEAAQAsQgEBAWAAAAMAkAAAAwAAAwAeoCCBBZZWVKTC8BbhICEggAAAAwCAAAADAISiAAEABkQBwHPAiQ=="
	testHEVC64PQ          = "AQFgAAAAkAAAAAAAHvAA/P34+AAADwOgAAEAGEABDAH//wFgAAADAJAAAAMAAAMAHpWUCaEAAQAsQgEBAWAAAAMAkAAAAwAAAwAeoCCBBZZWVKTC8BbhIgEggAAAAwCAAAADAISiAAEABkQBwHPAiQ=="
	testHEVC64HLG         = "AQFgAAAAkAAAAAAAHvAA/P34+AAADwOgAAEAGEABDAH//wFgAAADAJAAAAMAAAMAHpWUCaEAAQAsQgEBAWAAAAMAkAAAAwAAAwAeoCCBBZZWVKTC8BbhIkEggAAAAwCAAAADAISiAAEABkQBwHPAiQ=="
	testHEVC64TenBit      = "AQIgAAAAkAAAAAAAHvAA/P36+gAADwOgAAEAGEABDAH//wIgAAADAJAAAAMAAAMAHpWUCaEAAQAtQgEBAiAAAAMAkAAAAwAAAwAeoCCBBNllZUpMLwFuBAQMCAAAAwAIAAADAAhAogABAAZEAcBzwIk="
	testHEVC64SDR         = "AQFgAAAAkAAAAAAAHvAA/P34+AAADwOgAAEAGEABDAH//wFgAAADAJAAAAMAAAMAHpWUCaEAAQAsQgEBAWAAAAMAkAAAAwAAAwAeoCCBBZZWVKTC8BbgQCDAgAAAAwCAAAADAISiAAEABkQBwHPAiQ=="
	testHEVC64P3          = "AQFgAAAAkAAAAAAAHvAA/P34+AAADwOgAAEAGEABDAH//wFgAAADAJAAAAMAAAMAHpWUCaEAAQAsQgEBAWAAAAMAkAAAAwAAAwAeoCCBBZZWVKTC8BbhgaDAgAAAAwCAAAADAISiAAEABkQBwHPAiQ=="
	// Independently generated 8- and 10-bit solid-white encodes happen to have
	// identical coded-slice bytes; the SPS/configuration determines their depth.
	testHEVC64White = "AAAADSgBrE7XH3/1Ppyv6vg="
)

func testHEIFU16(n uint16) []byte {
	b := make([]byte, 2)
	binary.BigEndian.PutUint16(b, n)
	return b
}

func testHEIFU32(n uint32) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, n)
	return b
}

func testHEIFExtent(w, h uint32) testHEIFProperty {
	b := append(make([]byte, 4), testHEIFU32(w)...)
	return testHEIFProperty{tag: "ispe", body: append(b, testHEIFU32(h)...)}
}

func newTestPrimaryHEIF(t *testing.T) *testPrimaryHEIF {
	t.Helper()
	codec, err := base64.StdEncoding.DecodeString("AQNwAAAAsAAAAAAAHvAA/P34+AAACwOgAAEAF0ABDAH//wNwAAADALAAAAMAAAMAHnAkoQABACNCAQEDcAAAAwCwAAADAAADAB6gFCBBwIMM4h7kWVTcCAgYAqIAAQAJRAHAYXLIQFMk")
	if err != nil {
		t.Fatal(err)
	}
	pixels, err := base64.StdEncoding.DecodeString("AAAAJygBr6LGR+xl1b8ppdoh//6bjSjjmV/QIjrYf/2C02bURpD49Aj+bA==")
	if err != nil {
		t.Fatal(err)
	}
	color := testHEIFProperty{tag: "colr", body: []byte("nclx\x00\x02\x00\x02\x00\x06\x80")}
	pixi := testHEIFProperty{tag: "pixi", body: []byte{0, 0, 0, 0, 3, 8, 8, 8}}
	leaf := func(id uint16) testHEIFItem {
		return testHEIFItem{id: id, typ: "hvc1", data: append([]byte(nil), pixels...), properties: []testHEIFProperty{
			testHEIFExtent(32, 16), color, pixi, {tag: "hvcC", body: append([]byte(nil), codec...), essential: true},
		}}
	}
	gain := leaf(4)
	gain.properties = append(gain.properties, testHEIFProperty{tag: "auxC", body: append(make([]byte, 4), append([]byte(appleSDRGainMap), 0)...), essential: true})
	return &testPrimaryHEIF{primary: 3, items: []testHEIFItem{
		leaf(1), leaf(2),
		{id: 3, typ: "grid", data: []byte{0, 0, 0, 1, 0, 64, 0, 16}, properties: []testHEIFProperty{
			testHEIFExtent(64, 16), color, pixi, {tag: "irot", body: []byte{0}, essential: true},
		}}, gain,
	}, refs: []testHEIFReference{{typ: "dimg", from: 3, to: []uint16{1, 2}}, {typ: "auxl", from: 4, to: []uint16{3}}}}
}

// Generated offline with ffmpeg/libx265 from a solid-white 64x64 lavfi source,
// info=0, colorprim=2, transfer=2, colormatrix=6, range=full. No encoder is
// required at test/runtime. The alternate configuration changes only the SPS
// VUI to BT.2020 primaries/BT.709 transfer/matrix9, still 8-bit SDR.
func testHEIFWithBitstreamColor(t *testing.T, hiddenDifferentColor bool) *testPrimaryHEIF {
	t.Helper()
	fixture := newTestPrimaryHEIF(t)
	for _, id := range []uint16{1, 2, 4} {
		config := testHEVC64Unspecified
		if id == 2 && hiddenDifferentColor {
			config = testHEVC64BT2020
		}
		codec, err := base64.StdEncoding.DecodeString(config)
		if err != nil {
			t.Fatal(err)
		}
		pixels, err := base64.StdEncoding.DecodeString(testHEVC64White)
		if err != nil {
			t.Fatal(err)
		}
		fixture.property(id, "hvcC").body = codec
		fixture.property(id, "ispe").body = testHEIFExtent(64, 64).body
		fixture.item(id).data = pixels
	}
	if hiddenDifferentColor {
		item := fixture.item(2)
		var kept []testHEIFProperty
		for _, property := range item.properties {
			if property.tag != "colr" {
				kept = append(kept, property)
			}
		}
		item.properties = kept // only the encoded VUI reveals the color change
	}
	fixture.property(3, "ispe").body = testHEIFExtent(128, 64).body
	fixture.item(3).data = []byte{0, 0, 0, 1, 0, 128, 0, 64}
	return fixture
}

func (f *testPrimaryHEIF) item(id uint16) *testHEIFItem {
	for i := range f.items {
		if f.items[i].id == id {
			return &f.items[i]
		}
	}
	panic("missing synthetic HEIF item")
}

func (f *testPrimaryHEIF) directPrimary(id uint16) {
	f.items = []testHEIFItem{*f.item(id), *f.item(4)}
	f.primary = id
	f.refs = []testHEIFReference{{typ: "auxl", from: 4, to: []uint16{id}}}
}

func (f *testPrimaryHEIF) property(id uint16, tag string) *testHEIFProperty {
	item := f.item(id)
	for i := range item.properties {
		if item.properties[i].tag == tag {
			// Fixture entries initially share harmless color/pixel declarations.
			// Mutations must affect only the selected item under test.
			item.properties[i].body = append([]byte(nil), item.properties[i].body...)
			return &item.properties[i]
		}
	}
	panic("missing synthetic HEIF property")
}

func (f *testPrimaryHEIF) bytes() []byte {
	var entries, properties, idat []byte
	associations := append(make([]byte, 4), testHEIFU32(uint32(len(f.items)))...)
	// iloc v1 construction method 1 addresses idat, so fixture mutations never
	// need fragile absolute-file-offset recalculation.
	locations := append([]byte{1, 0, 0, 0, 0x44, 0}, testHEIFU16(uint16(len(f.items)))...)
	propertyIndex := byte(0)
	propertyIndexes := make(map[string]byte)
	for _, item := range f.items {
		entry := []byte{2, 0, 0, 0}
		if item.id != f.primary {
			entry[3] = 1 // hidden tiles and auxiliaries are not top-level photos
		}
		entry = append(entry, testHEIFU16(item.id)...)
		entry = append(entry, 0, 0) // unprotected item
		entry = append(entry, []byte(item.typ)...)
		entry = append(entry, []byte("synthetic private item name\x00")...)
		entries = append(entries, heifBox("infe", entry)...)
		associations = append(associations, testHEIFU16(item.id)...)
		associations = append(associations, byte(len(item.properties)))
		for _, prop := range item.properties {
			key := prop.tag + string(prop.body)
			index := propertyIndexes[key]
			if f.separateProperties {
				index = 0
			}
			if index == 0 {
				propertyIndex++
				index = propertyIndex
				propertyIndexes[key] = index
				properties = append(properties, heifBox(prop.tag, prop.body)...)
			}
			if prop.essential {
				index |= 0x80
			}
			associations = append(associations, index)
		}
		locations = append(locations, testHEIFU16(item.id)...)
		locations = append(locations, 0, 1, 0, 0, 0, 1) // method 1, data reference 0, one extent
		locations = append(locations, testHEIFU32(uint32(len(idat)))...)
		locations = append(locations, testHEIFU32(uint32(len(item.data)))...)
		idat = append(idat, item.data...)
	}
	iinf := append(make([]byte, 4), testHEIFU16(uint16(len(f.items)))...)
	iinf = append(iinf, entries...)
	refs := make([]byte, 4)
	for _, ref := range f.refs {
		entry := append(testHEIFU16(ref.from), testHEIFU16(uint16(len(ref.to)))...)
		for _, id := range ref.to {
			entry = append(entry, testHEIFU16(id)...)
		}
		refs = append(refs, heifBox(ref.typ, entry)...)
	}
	meta := make([]byte, 4)
	handler := append(make([]byte, 8), []byte("pict")...)
	handler = append(handler, make([]byte, 13)...)
	meta = append(meta, heifBox("hdlr", handler)...)
	meta = append(meta, heifBox("pitm", append(make([]byte, 4), testHEIFU16(f.primary)...))...)
	meta = append(meta, heifBox("iinf", iinf)...)
	meta = append(meta, heifBox("iloc", locations)...)
	meta = append(meta, heifBox("iprp", append(heifBox("ipco", properties), heifBox("ipma", associations)...))...)
	meta = append(meta, heifBox("iref", refs)...)
	meta = append(meta, heifBox("idat", idat)...)
	return append(heifBox("ftyp", []byte("heic\x00\x00\x00\x00mif1heic")), heifBox("meta", meta)...)
}

func requirePrimaryHEIFConverter(t *testing.T) {
	t.Helper()
	if _, err := findHEIFPrimaryConverter(); err != nil {
		if os.Getenv("CROPTOP_REQUIRE_COLOR_CONVERTER") == "1" {
			t.Fatal("production primary HEIF converter missing")
		}
		t.Skip("primary HEIF converter not installed")
	}
}

func TestHEIFPrimaryRecognition(t *testing.T) {
	for _, tc := range []struct {
		name string
		typ  string
		want bool
	}{
		{"Apple SDR gain map", appleSDRGainMap, true},
		{"unknown gain map", "urn:example:hdrgainmap", false},
		{"prefix is not identity", appleSDRGainMap + "-unknown", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newTestPrimaryHEIF(t)
			fixture.property(4, "auxC").body = append(make([]byte, 4), append([]byte(tc.typ), 0)...)
			got, err := hasAppleSDRGainMap(fixture.bytes())
			if err != nil || got != tc.want {
				t.Fatalf("recognition = %v, %v; want %v", got, err, tc.want)
			}
		})
	}
}

func TestHEIFPrimaryMissingConverterNeverFallsBack(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX converter fixture")
	}
	dir := t.TempDir()
	legacy, marker := filepath.Join(dir, "heif-convert"), filepath.Join(dir, "legacy-was-called")
	if err := os.WriteFile(legacy, []byte("#!/bin/sh\n: > \"$CROPTOP_TEST_LEGACY_MARKER\"\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CROPTOP_HEIF_CONVERTER", legacy)
	t.Setenv("CROPTOP_TEST_LEGACY_MARKER", marker)
	t.Setenv("CROPTOP_HEIF_PRIMARY_CONVERTER", "disabled")
	in, out := mediaFixture(t, newTestPrimaryHEIF(t).bytes())
	if _, err := NormalizeImage(context.Background(), in, out); !errors.Is(err, ErrHEIFUnavailable) {
		t.Fatalf("missing primary converter: %v", err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("primary-only input invoked legacy converter")
	}
	if files, _ := os.ReadDir(out); len(files) != 0 {
		t.Fatal("unavailable converter retained staged source")
	}
}

func TestHEIFPrimaryConverterContractAndCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX converter fixture")
	}
	dir := t.TempDir()
	converter, source := filepath.Join(dir, "croptop-heif"), filepath.Join(dir, "source.png")
	if err := os.WriteFile(source, fixturePNG(t), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CROPTOP_HEIF_PRIMARY_CONVERTER", converter)
	t.Setenv("CROPTOP_TEST_PNG", source)
	script := "#!/bin/sh\n" +
		"test \"$#\" -eq 4 && test \"$3\" -eq 40000000 && test \"$4\" -eq 1048576 && " +
		"exec /bin/cp \"$CROPTOP_TEST_PNG\" \"$2\"\n"
	if err := os.WriteFile(converter, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	in, out := mediaFixture(t, newTestPrimaryHEIF(t).bytes())
	if _, err := NormalizeImage(context.Background(), in, out); err != nil {
		t.Fatalf("primary helper CLI contract: %v", err)
	}
	if err := os.WriteFile(converter, []byte("#!/bin/sh\n/bin/sleep 10 &\nwait\n"), 0700); err != nil {
		t.Fatal(err)
	}
	in, out = mediaFixture(t, newTestPrimaryHEIF(t).bytes())
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := NormalizeImage(ctx, in, out); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("primary helper cancellation: %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("primary helper or descendant outlived cancellation")
	}
	if files, _ := os.ReadDir(out); len(files) != 0 {
		t.Fatal("canceled primary helper retained staged image")
	}
}

func TestHEIFPrimaryNormalizeRealGrid(t *testing.T) {
	requirePrimaryHEIFConverter(t)
	for _, rotation := range []byte{0, 1} {
		t.Run(string(rune('0'+rotation)), func(t *testing.T) {
			fixture := newTestPrimaryHEIF(t)
			fixture.property(3, "irot").body = []byte{rotation}
			data := fixture.bytes()
			in, out := mediaFixture(t, data)
			media, err := NormalizeImage(context.Background(), in, out)
			if err != nil {
				t.Fatal(err)
			}
			width, height := 64, 16
			if rotation == 1 {
				width, height = height, width
			}
			if media.Type != "image/png" || filepath.Ext(media.Path) != ".png" || media.Width != width || media.Height != height {
				t.Fatalf("primary result: %+v", media)
			}
			pngData, err := os.ReadFile(media.Path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := png.Decode(bytes.NewReader(pngData)); err != nil {
				t.Fatalf("output is not PNG: %v", err)
			}
			for _, private := range []string{"synthetic private", "auxC", appleSDRGainMap, "Exif", "iCCP", "eXIf", "iTXt", "tEXt"} {
				if bytes.Contains(pngData, []byte(private)) {
					t.Fatalf("normalized PNG retained source metadata %q", private)
				}
			}
			original, err := os.ReadFile(in)
			if err != nil || !bytes.Equal(original, data) {
				t.Fatal("normalization modified original HEIC")
			}
		})
	}
}

func TestHEIFPrimaryOperationNormalizesAutomatically(t *testing.T) {
	requirePrimaryHEIFConverter(t)
	f := newServiceFixture(t)
	f.enable(t)
	id := store.NewID()
	source := newTestPrimaryHEIF(t).bytes()
	var body bytes.Buffer
	m := multipart.NewWriter(&body)
	if err := m.WriteField("id", id); err != nil {
		t.Fatal(err)
	}
	part, err := m.CreateFormFile("image", "camera.heic")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(source); err != nil {
		t.Fatal(err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", apiPrefix+"/operations", &body)
	r.Header.Set("Content-Type", m.FormDataContentType())
	r.Header.Set("Authorization", "Bearer "+f.token)
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	if w.Code != 200 && w.Code != 202 {
		t.Fatalf("HEIC upload rejected: %d %s", w.Code, w.Body.String())
	}
	op := f.wait(t, id, "needs_signature")
	if op.MediaType != "image/png" || op.Proposal == nil {
		t.Fatalf("operation did not prepare PNG automatically: %+v", op)
	}
	f.server.mu.Lock()
	retained := f.server.operations[operationKey(f.name, id)]
	if retained == nil {
		f.server.mu.Unlock()
		t.Fatal("prepared HEIC operation was not retained")
	}
	mediaPath, originalPath := retained.MediaPath, filepath.Join(f.server.operationDir(retained), "upload")
	f.server.mu.Unlock()
	if filepath.Ext(mediaPath) != ".png" {
		t.Fatalf("operation retained HEIC extension: %s", filepath.Ext(mediaPath))
	}
	preview := f.request(t, "GET", "/operations/"+id+"/image", nil, f.token)
	requireStatus(t, preview, 200)
	if preview.Header().Get("Content-Type") != "image/png" || !bytes.HasPrefix(preview.Body.Bytes(), []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatal("operation preview is not served as PNG")
	}
	original, err := os.ReadFile(originalPath)
	if err != nil || !bytes.Equal(original, source) {
		t.Fatal("preparing the operation changed original HEIC bytes")
	}
	f.publisher.mu.Lock()
	defer f.publisher.mu.Unlock()
	if len(f.publisher.prepared) != 1 || len(f.publisher.prepared[0].Files) != 1 || !strings.HasSuffix(f.publisher.prepared[0].Files[0], ".png") || f.publisher.commits != 0 {
		t.Fatal("prepared publication did not use exactly one normalized PNG before signing")
	}
}

func TestHEIFPrimaryDirectImageUsesNativeColorCheck(t *testing.T) {
	requirePrimaryHEIFConverter(t)
	fixture := newTestPrimaryHEIF(t)
	fixture.directPrimary(1)
	in, out := mediaFixture(t, fixture.bytes())
	media, err := NormalizeImage(context.Background(), in, out)
	if err != nil || media.Type != "image/png" || media.Width != 32 || media.Height != 16 {
		t.Fatalf("direct SDR primary failed: %+v %v", media, err)
	}
	fixture = testHEIFWithBitstreamColor(t, true)
	codec, err := base64.StdEncoding.DecodeString(testHEVC64PQ)
	if err != nil {
		t.Fatal(err)
	}
	fixture.property(2, "hvcC").body = codec
	fixture.directPrimary(2)
	in, out = mediaFixture(t, fixture.bytes())
	if media, err := NormalizeImage(context.Background(), in, out); err == nil {
		t.Fatalf("direct primary with resolved PQ VUI produced PNG: %+v", media)
	}
}

func TestHEIFPrimaryBitstreamColorMustMatch(t *testing.T) {
	requirePrimaryHEIFConverter(t)
	for _, hiddenDifferentColor := range []bool{false, true} {
		fixture := testHEIFWithBitstreamColor(t, hiddenDifferentColor)
		in, out := mediaFixture(t, fixture.bytes())
		media, err := NormalizeImage(context.Background(), in, out)
		if !hiddenDifferentColor {
			if err != nil || media.Width != 128 || media.Height != 64 {
				t.Fatalf("valid matching VUI baseline rejected: %+v %v", media, err)
			}
		} else if err == nil {
			t.Fatalf("unannounced tile VUI color mismatch produced PNG: %+v", media)
		}
	}
}

func TestHEIFPrimaryRejectsResolvedBitstreamHDRAndDepth(t *testing.T) {
	requirePrimaryHEIFConverter(t)
	// Same offline solid-white encoder source as testHEIFWithBitstreamColor,
	// with transfer=16/18, or yuv420p10le. The item's color declaration is
	// absent, so libheif resolves color from the VUI; the depth case deliberately
	// lies in hvcC and pixi. This does not claim a separate raw-SPS color parser.
	for _, tc := range []struct {
		name, codec string
		falseDepth  bool
	}{
		{"PQ only in bitstream", testHEVC64PQ, false},
		{"HLG only in bitstream", testHEVC64HLG, false},
		{"10-bit pixels with 8-bit declaration", testHEVC64TenBit, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := testHEIFWithBitstreamColor(t, true)
			codec, err := base64.StdEncoding.DecodeString(tc.codec)
			if err != nil {
				t.Fatal(err)
			}
			if tc.falseDepth {
				codec[17] &^= 7
				codec[18] &^= 7
			}
			fixture.property(2, "hvcC").body = codec
			in, out := mediaFixture(t, fixture.bytes())
			if media, err := NormalizeImage(context.Background(), in, out); err == nil {
				t.Fatalf("unsupported bitstream produced PNG: %+v", media)
			}
			if files, _ := os.ReadDir(out); len(files) != 0 {
				t.Fatal("rejected bitstream retained staged images")
			}
		})
	}
}

func TestHEIFPrimaryIgnoresUndecodedAuxiliaryColor(t *testing.T) {
	requirePrimaryHEIFConverter(t)
	fixture := newTestPrimaryHEIF(t)
	// Deliberately incompatible auxiliary metadata would fail if borrowed for
	// the primary or if this auxiliary were accidentally decoded. Its pixels
	// are never part of the selected SDR grid.
	fixture.property(4, "colr").body[7] = 16
	fixture.property(4, "hvcC").body[17] |= 2
	fixture.property(4, "pixi").body[5] = 10
	in, out := mediaFixture(t, fixture.bytes())
	media, err := NormalizeImage(context.Background(), in, out)
	if err != nil || media.Width != 64 || media.Height != 16 {
		t.Fatalf("unselected auxiliary contaminated the primary: %+v %v", media, err)
	}
}

func TestHEIFPrimaryEquivalentColorProperties(t *testing.T) {
	requirePrimaryHEIFConverter(t)
	for _, separate := range []bool{false, true} {
		fixture := newTestPrimaryHEIF(t)
		fixture.separateProperties = separate
		fixture.item(3).properties = append(fixture.item(3).properties, *fixture.property(3, "colr"))
		in, out := mediaFixture(t, fixture.bytes())
		media, err := NormalizeImage(context.Background(), in, out)
		if err != nil || media.Width != 64 || media.Height != 16 {
			t.Fatalf("equivalent color declaration rejected (separate boxes %v): %+v %v", separate, media, err)
		}
	}
}

func TestHEIFPrimaryUsesActualDecodedDepth(t *testing.T) {
	requirePrimaryHEIFConverter(t)
	fixture := newTestPrimaryHEIF(t)
	// Raw pixi hints do not override the decoder's hvcC and actual native pixel
	// depth. No high-bit-depth pixels are converted or silently truncated here.
	fixture.property(3, "pixi").body[5] = 10
	fixture.property(2, "pixi").body[5] = 10
	in, out := mediaFixture(t, fixture.bytes())
	media, err := NormalizeImage(context.Background(), in, out)
	if err != nil || media.Type != "image/png" || media.Width != 64 || media.Height != 16 {
		t.Fatalf("proven 8-bit primary rejected because of an unrelated depth hint: %+v %v", media, err)
	}
}

func TestHEIFPrimaryP3ProfileReachesColorTransform(t *testing.T) {
	requirePrimaryHEIFConverter(t)
	if _, err := findColorConverter(); err != nil {
		if os.Getenv("CROPTOP_REQUIRE_COLOR_CONVERTER") == "1" {
			t.Fatal("production color converter missing")
		}
		t.Skip("dedicated color converter not installed")
	}
	fixture := newTestPrimaryHEIF(t)
	for _, id := range []uint16{1, 2, 3} {
		fixture.item(id).properties = append(fixture.item(id).properties, testHEIFProperty{tag: "colr", body: append([]byte("prof"), testP3Profile()...)})
	}
	in, out := mediaFixture(t, fixture.bytes())
	media, err := NormalizeImage(context.Background(), in, out)
	if err != nil || media.Type != "image/png" || media.Width != 64 || media.Height != 16 {
		t.Fatalf("profiled primary failed color normalization: %+v %v", media, err)
	}
	// A second run without the color helper must fail rather than silently
	// discard the primary's ICC. This proves the HEIF helper retained it.
	t.Setenv("CROPTOP_COLOR_CONVERTER", "disabled")
	in, out = mediaFixture(t, fixture.bytes())
	if _, err := NormalizeImage(context.Background(), in, out); !errors.Is(err, ErrColorUnavailable) {
		t.Fatalf("source ICC was dropped before color conversion: %v", err)
	}
}

func TestHEIFPrimaryICCOnlyWithNativeSDRColor(t *testing.T) {
	requirePrimaryHEIFConverter(t)
	if _, err := findColorConverter(); err != nil {
		if os.Getenv("CROPTOP_REQUIRE_COLOR_CONVERTER") == "1" {
			t.Fatal("production color converter missing")
		}
		t.Skip("dedicated color converter not installed")
	}
	for _, tc := range []struct{ name, codec string }{
		// cp2/tc1 reproduces the camera metadata pattern without retaining a
		// camera file. cp12/tc13 additionally covers explicit Display-P3 SDR.
		{"unspecified primaries BT709 transfer", testHEVC64SDR},
		{"Display P3 sRGB transfer", testHEVC64P3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, direct := range []bool{false, true} {
				fixture := testHEIFWithBitstreamColor(t, false)
				for _, id := range []uint16{1, 2, 3} {
					fixture.property(id, "colr").body = append([]byte("prof"), testP3Profile()...)
					if id != 3 {
						codec, err := base64.StdEncoding.DecodeString(tc.codec)
						if err != nil {
							t.Fatal(err)
						}
						fixture.property(id, "hvcC").body = codec
					}
				}
				width := 128
				if direct {
					fixture.directPrimary(1)
					width = 64
				}
				in, out := mediaFixture(t, fixture.bytes())
				media, err := NormalizeImage(context.Background(), in, out)
				if err != nil || media.Type != "image/png" || media.Width != width || media.Height != 64 {
					t.Fatalf("ICC-authoritative SDR primary failed (direct=%v): %+v %v", direct, media, err)
				}
				// A missing color transform must not turn this into unprofiled
				// SDR merely because its NCLX container declaration is absent.
				t.Run("requires color transform", func(t *testing.T) {
					t.Setenv("CROPTOP_COLOR_CONVERTER", "disabled")
					in, out := mediaFixture(t, fixture.bytes())
					if _, err := NormalizeImage(context.Background(), in, out); !errors.Is(err, ErrColorUnavailable) {
						t.Fatalf("ICC-only primary lost its required transform: %v", err)
					}
				})
			}
		})
	}
}

func TestHEIFPrimaryRejectsUnprofiledBT2020(t *testing.T) {
	requirePrimaryHEIFConverter(t)
	fixture := testHEIFWithBitstreamColor(t, false)
	for _, id := range []uint16{1, 2, 3} {
		fixture.property(id, "colr").body = []byte("nclx\x00\x09\x00\x01\x00\x09\x80")
		if id != 3 {
			codec, err := base64.StdEncoding.DecodeString(testHEVC64BT2020)
			if err != nil {
				t.Fatal(err)
			}
			fixture.property(id, "hvcC").body = codec
		}
	}
	in, out := mediaFixture(t, fixture.bytes())
	if media, err := NormalizeImage(context.Background(), in, out); err == nil {
		t.Fatalf("matching BT2020 declarations were relabeled as unprofiled SDR: %+v", media)
	}
}

func TestHEIFPrimaryRejectsUnsupportedDependencies(t *testing.T) {
	requirePrimaryHEIFConverter(t)
	for _, tc := range []struct {
		name   string
		mutate func(*testPrimaryHEIF)
	}{
		{"primary PQ", func(f *testPrimaryHEIF) { f.property(3, "colr").body[7] = 16 }},
		{"primary HLG", func(f *testPrimaryHEIF) { f.property(3, "colr").body[7] = 18 }},
		{"tile PQ", func(f *testPrimaryHEIF) { f.property(2, "colr").body[7] = 16 }},
		{"tile HLG", func(f *testPrimaryHEIF) { f.property(2, "colr").body[7] = 18 }},
		{"first tile 10-bit codec", func(f *testPrimaryHEIF) { f.property(1, "hvcC").body[17] |= 2 }},
		{"later tile 10-bit codec", func(f *testPrimaryHEIF) { f.property(2, "hvcC").body[17] |= 2 }},
		{"different tile primaries", func(f *testPrimaryHEIF) { f.property(2, "colr").body[5] = 9 }},
		{"different tile transfer", func(f *testPrimaryHEIF) { f.property(2, "colr").body[7] = 13 }},
		{"different tile matrix", func(f *testPrimaryHEIF) { f.property(2, "colr").body[9] = 1 }},
		{"different tile range", func(f *testPrimaryHEIF) { f.property(2, "colr").body[10] = 0 }},
		{"different tile ICC", func(f *testPrimaryHEIF) {
			for _, id := range []uint16{1, 2, 3} {
				profile := testP3Profile()
				if id == 2 {
					profile[84] = 1 // different valid profile ID, not broken structure
				}
				f.item(id).properties = append(f.item(id).properties, testHEIFProperty{tag: "colr", body: append([]byte("prof"), profile...)})
			}
		}},
		{"conflicting duplicate primary PQ", func(f *testPrimaryHEIF) {
			f.item(3).properties = append(f.item(3).properties, testHEIFProperty{tag: "colr", body: []byte("nclx\x00\x09\x00\x10\x00\x09\x80")})
		}},
		{"conflicting duplicate primary ICC", func(f *testPrimaryHEIF) {
			f.item(3).properties = append(f.item(3).properties,
				testHEIFProperty{tag: "colr", body: append([]byte("prof"), testP3Profile()...)},
				testHEIFProperty{tag: "colr", body: []byte("profnot-an-ICC")})
		}},
		{"malformed primary ICC", func(f *testPrimaryHEIF) {
			f.item(3).properties = append(f.item(3).properties, testHEIFProperty{tag: "colr", body: []byte("profnot-an-ICC")})
		}},
		{"truncated primary ICC", func(f *testPrimaryHEIF) {
			profile := testP3Profile()
			f.item(3).properties = append(f.item(3).properties, testHEIFProperty{tag: "colr", body: append([]byte("prof"), profile[:len(profile)-20]...)})
		}},
		{"unknown essential primary property", func(f *testPrimaryHEIF) {
			f.item(3).properties = append(f.item(3).properties, testHEIFProperty{tag: "zzzz", body: []byte{1}, essential: true})
		}},
		{"unknown essential tile property", func(f *testPrimaryHEIF) {
			f.item(2).properties = append(f.item(2).properties, testHEIFProperty{tag: "zzzz", body: []byte{1}, essential: true})
		}},
		{"primary alpha auxiliary", func(f *testPrimaryHEIF) {
			alpha := *f.item(4)
			alpha.id = 5
			alpha.properties = append([]testHEIFProperty(nil), alpha.properties...)
			f.items = append(f.items, alpha)
			f.property(5, "auxC").body = append(make([]byte, 4), []byte("urn:mpeg:hevc:2015:auxid:1\x00")...)
			f.refs = append(f.refs, testHEIFReference{typ: "auxl", from: 5, to: []uint16{3}})
		}},
		{"primary premultiplied", func(f *testPrimaryHEIF) {
			f.refs = append(f.refs, testHEIFReference{typ: "prem", from: 3, to: []uint16{4}})
		}},
		{"derived tile", func(f *testPrimaryHEIF) { f.item(2).typ = "iden" }},
		{"tone-map primary", func(f *testPrimaryHEIF) { f.item(3).typ = "tmap" }},
		{"dangling grid reference", func(f *testPrimaryHEIF) { f.refs[0].to[1] = 99 }},
		{"recursive grid", func(f *testPrimaryHEIF) { f.refs[0].to[1] = 3 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newTestPrimaryHEIF(t)
			tc.mutate(fixture)
			in, out := mediaFixture(t, fixture.bytes())
			if media, err := NormalizeImage(context.Background(), in, out); err == nil {
				t.Fatalf("unsupported dependency produced PNG: %+v", media)
			}
			if files, _ := os.ReadDir(out); len(files) != 0 {
				t.Fatal("rejected HEIC left private staging files")
			}
		})
	}
}
