package mobile

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"golang.org/x/image/webp"
)

// These limits belong to the normalizer and are also served by the capability
// endpoint. Callers must bound concurrency: a 16-bit, 40MP source can occupy
// 320 MB while decoded. Encoding uses a transformed view, not a second bitmap.
const (
	MaxImageBytes           = 20 << 20
	MaxImagePixels          = 40_000_000
	ImageTimeout            = 30 * time.Second
	MaxNormalizedImageBytes = 160 << 20
	maxConvertedImageBytes  = 320 << 20
	converterMemoryKiB      = 1024 * 1024
)

var (
	ErrImageTooLarge    = errors.New("image exceeds the 20 MiB input limit")
	ErrImagePixels      = errors.New("image exceeds the 40 megapixel limit")
	ErrUnsupportedImage = errors.New("choose a PNG, JPEG, WebP, HEIC, or HEIF still image")
	ErrInvalidImage     = errors.New("image is damaged or cannot be decoded")
	ErrHEIFUnavailable  = errors.New("HEIC/HEIF conversion is unavailable; choose a PNG or JPEG image")
)

type Media struct {
	Path   string `json:"path"`
	Type   string `json:"type"`
	SHA256 string `json:"sha256"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// SupportedFormats reports installed capabilities. CROPTOP_HEIF_CONVERTER may
// select a heif-convert/heif-dec/sips executable, or "disabled" to omit HEIF.
// A supported executable can still reject an unsupported codec or corrupt file.
func SupportedFormats() []string {
	formats := []string{"image/png", "image/jpeg", "image/webp"}
	if _, err := findHEIFConverter(); err == nil {
		formats = append(formats, "image/heic", "image/heif")
	}
	return formats
}

// NormalizeImage decodes a single still image into a metadata-free 8-bit PNG,
// preserving screenshot detail without a second lossy compression pass. The
// returned path is private to outputDir until the caller publishes it.
// Input names and MIME claims are never trusted. Originals are left untouched.
func NormalizeImage(ctx context.Context, inputPath, outputDir string) (Media, error) {
	ctx, cancel := context.WithTimeout(ctx, ImageTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return Media{}, err
	}
	f, err := os.Open(inputPath)
	if err != nil {
		return Media{}, fmt.Errorf("open image: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return Media{}, err
	}
	if !info.Mode().IsRegular() {
		return Media{}, ErrInvalidImage
	}
	if info.Size() > MaxImageBytes {
		return Media{}, ErrImageTooLarge
	}
	data, err := io.ReadAll(io.LimitReader(contextReader{ctx, f}, MaxImageBytes+1))
	if err != nil {
		return Media{}, err
	}
	if len(data) > MaxImageBytes {
		return Media{}, ErrImageTooLarge
	}
	typ := imageType(data)
	if typ == "" {
		return Media{}, ErrUnsupportedImage
	}
	if animatedRaster(data, typ) {
		return Media{}, errors.New("choose one still image; animated images are not supported")
	}
	if err := os.MkdirAll(outputDir, 0700); err != nil {
		return Media{}, err
	}
	if typ == "heif" {
		if err := checkHEIFDimensions(data); err != nil {
			return Media{}, err
		}
		converted, cleanup, err := convertHEIF(ctx, data, outputDir)
		if err != nil {
			return Media{}, err
		}
		defer cleanup()
		f, err := os.Open(converted)
		if err != nil {
			return Media{}, ErrInvalidImage
		}
		defer f.Close()
		// Converted PNG is bounded independently of the compressed input size.
		orientation := convertedPNGOrientation(f)
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return Media{}, err
		}
		return normalizeRaster(ctx, f, "png", orientation, outputDir)
	}
	return normalizeRaster(ctx, bytes.NewReader(data), typ, exifOrientation(data, typ), outputDir)
}

func imageType(data []byte) string {
	switch {
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		return "png"
	case bytes.HasPrefix(data, []byte{0xff, 0xd8, 0xff}):
		return "jpeg"
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return "webp"
	case len(data) >= 16 && string(data[4:8]) == "ftyp":
		n := int(binary.BigEndian.Uint32(data[:4]))
		if n < 16 || n > len(data) || n > 4096 {
			return ""
		}
		accepted := false
		for i := 8; i+4 <= n; i += 4 {
			if i == 12 {
				continue
			} // minor version is not a brand
			switch string(data[i : i+4]) {
			case "avif", "avis", "msf1":
				return "" // AVIF and sequences are outside this release.
			case "heic", "heix", "mif1":
				accepted = true
			}
		}
		if accepted {
			return "heif"
		}
	}
	return ""
}

func checkImageDimensions(w, h int) error {
	if w <= 0 || h <= 0 {
		return ErrInvalidImage
	}
	if uint64(w)*uint64(h) > MaxImagePixels {
		return ErrImagePixels
	}
	return nil
}

func normalizeRaster(ctx context.Context, r io.ReadSeeker, typ string, orientation int, outputDir string) (Media, error) {
	var config image.Config
	var err error
	cr := contextReader{ctx, r}
	switch typ {
	case "png":
		config, err = png.DecodeConfig(cr)
	case "jpeg":
		config, err = jpeg.DecodeConfig(cr)
	case "webp":
		config, err = webp.DecodeConfig(cr)
	default:
		return Media{}, ErrUnsupportedImage
	}
	if err != nil {
		return Media{}, imageDecodeError(ctx, err)
	}
	if err = checkImageDimensions(config.Width, config.Height); err != nil {
		return Media{}, err
	}
	if _, err = r.Seek(0, io.SeekStart); err != nil {
		return Media{}, err
	}
	var decoded image.Image
	switch typ {
	case "png":
		decoded, err = png.Decode(cr)
	case "jpeg":
		decoded, err = jpeg.Decode(cr)
	case "webp":
		decoded, err = webp.Decode(cr)
	}
	if err != nil {
		return Media{}, imageDecodeError(ctx, err)
	}
	if err := ctx.Err(); err != nil {
		return Media{}, err
	}
	if err := checkImageDimensions(decoded.Bounds().Dx(), decoded.Bounds().Dy()); err != nil {
		return Media{}, err
	}
	view := orientedImage{decoded, orientation}
	out, err := os.CreateTemp(outputDir, ".normalizing-*.png")
	if err != nil {
		return Media{}, err
	}
	defer os.Remove(out.Name())
	hash := sha256.New()
	w := &boundedImageWriter{ctx: ctx, w: io.MultiWriter(out, hash), remaining: MaxNormalizedImageBytes}
	encoder := png.Encoder{CompressionLevel: png.BestSpeed}
	err = encoder.Encode(w, view)
	if err == nil {
		err = out.Sync()
	}
	closeErr := out.Close()
	if err != nil {
		return Media{}, err
	}
	if closeErr != nil {
		return Media{}, closeErr
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	dest := filepath.Join(outputDir, digest+".png")
	if err := os.Rename(out.Name(), dest); err != nil {
		return Media{}, err
	}
	return Media{Path: dest, Type: "image/png", SHA256: digest, Width: view.Bounds().Dx(), Height: view.Bounds().Dy()}, nil
}

func imageDecodeError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return fmt.Errorf("%w: %v", ErrInvalidImage, err)
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

type boundedImageWriter struct {
	ctx       context.Context
	w         io.Writer
	remaining int64
}

func (w *boundedImageWriter) Write(p []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	if int64(len(p)) > w.remaining {
		return 0, errors.New("normalized image exceeds output limit")
	}
	n, err := w.w.Write(p)
	w.remaining -= int64(n)
	return n, err
}

// A view applies EXIF without allocating another full-sized image. The model
// requests 8-bit PNG output, including when the source used 16-bit channels.
type orientedImage struct {
	image.Image
	orientation int
}

func (im orientedImage) ColorModel() color.Model { return color.NRGBAModel }
func (im orientedImage) Bounds() image.Rectangle {
	w, h := im.Image.Bounds().Dx(), im.Image.Bounds().Dy()
	if im.orientation >= 5 && im.orientation <= 8 {
		w, h = h, w
	}
	return image.Rect(0, 0, w, h)
}
func (im orientedImage) At(x, y int) color.Color {
	b := im.Image.Bounds()
	w, h := b.Dx(), b.Dy()
	switch im.orientation {
	case 2:
		x = w - 1 - x
	case 3:
		x, y = w-1-x, h-1-y
	case 4:
		y = h - 1 - y
	case 5:
		x, y = y, x
	case 6:
		x, y = y, h-1-x
	case 7:
		x, y = w-1-y, h-1-x
	case 8:
		x, y = w-1-y, x
	}
	return im.Image.At(x+b.Min.X, y+b.Min.Y)
}

// Read only the bounded EXIF orientation field; all source metadata is discarded
// by PNG re-encoding. Malformed optional metadata does not hide a valid image.
func exifOrientation(data []byte, typ string) int {
	switch typ {
	case "jpeg":
		for p := 2; p+4 <= len(data); {
			if data[p] != 0xff {
				break
			}
			for p+1 < len(data) && data[p+1] == 0xff {
				p++
			}
			if p+4 > len(data) {
				break
			}
			marker := data[p+1]
			if marker == 0xda || marker == 0xd9 {
				break
			}
			n := int(binary.BigEndian.Uint16(data[p+2 : p+4]))
			if n < 2 || n > len(data)-p-2 {
				break
			}
			if marker == 0xe1 && bytes.HasPrefix(data[p+4:p+2+n], []byte("Exif\x00\x00")) {
				return tiffOrientation(data[p+10 : p+2+n])
			}
			p += n + 2
		}
	case "png", "webp":
		orientation := 1
		walkRasterChunks(data, typ, func(tag string, payload []byte) bool {
			if tag == "eXIf" || tag == "EXIF" {
				orientation = tiffOrientation(bytes.TrimPrefix(payload, []byte("Exif\x00\x00")))
				return false
			}
			return true
		})
		return orientation
	}
	return 1
}

func animatedRaster(data []byte, typ string) bool {
	animated := false
	walkRasterChunks(data, typ, func(tag string, payload []byte) bool {
		animated = tag == "acTL" || tag == "ANIM" || tag == "ANMF" ||
			(tag == "VP8X" && len(payload) > 0 && payload[0]&2 != 0)
		return !animated
	})
	return animated
}

// The container's length fields bound all metadata inspection in one place.
// Actual decoder validation remains responsible for CRCs and image structure.
func walkRasterChunks(data []byte, typ string, visit func(string, []byte) bool) {
	p, overhead := 8, 12
	if typ == "webp" {
		p, overhead = 12, 8
	} else if typ != "png" {
		return
	}
	for p+overhead <= len(data) {
		var n uint64
		var tag string
		if typ == "png" {
			n = uint64(binary.BigEndian.Uint32(data[p : p+4]))
			tag = string(data[p+4 : p+8])
		} else {
			n = uint64(binary.LittleEndian.Uint32(data[p+4 : p+8]))
			tag = string(data[p : p+4])
		}
		if n > uint64(len(data)-p-overhead) {
			return
		}
		if !visit(tag, data[p+8:p+8+int(n)]) {
			return
		}
		p += overhead + int(n)
		if typ == "webp" {
			p += int(n) & 1
		}
	}
}

func tiffOrientation(data []byte) int {
	if len(data) < 8 {
		return 1
	}
	var order binary.ByteOrder
	switch string(data[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 1
	}
	if order.Uint16(data[2:4]) != 42 {
		return 1
	}
	offset := uint64(order.Uint32(data[4:8]))
	if offset > uint64(len(data)-2) {
		return 1
	}
	n := int(order.Uint16(data[offset : offset+2]))
	for p, i := int(offset)+2, 0; i < n && p+12 <= len(data); p, i = p+12, i+1 {
		if order.Uint16(data[p:p+2]) == 0x112 && order.Uint16(data[p+2:p+4]) == 3 && order.Uint32(data[p+4:p+8]) == 1 {
			v := int(order.Uint16(data[p+8 : p+10]))
			if v >= 1 && v <= 8 {
				return v
			}
		}
	}
	return 1
}

// ImageIO can preserve a HEIF rotation as PNG eXIf instead of rotating pixels.
// Inspect chunks without loading the converted bitmap file into memory.
func convertedPNGOrientation(f *os.File) int {
	if _, err := f.Seek(8, io.SeekStart); err != nil {
		return 1
	}
	var header [8]byte
	for {
		if _, err := io.ReadFull(f, header[:]); err != nil {
			return 1
		}
		n := int64(binary.BigEndian.Uint32(header[:4]))
		if n > maxConvertedImageBytes {
			return 1
		}
		if string(header[4:8]) == "eXIf" {
			if n > 1<<20 {
				return 1
			}
			data := make([]byte, n)
			if _, err := io.ReadFull(f, data); err != nil {
				return 1
			}
			return tiffOrientation(data)
		}
		if string(header[4:8]) == "IEND" {
			return 1
		}
		if _, err := f.Seek(n+4, io.SeekCurrent); err != nil {
			return 1
		}
	}
}

// Inspect HEIF spatial extents before handing the untrusted container to a
// decoder. The decoder remains isolated and resource limited because container
// dimensions alone cannot establish the size a codec bitstream will allocate.
func checkHEIFDimensions(data []byte) error {
	count := 0
	var walk func([]byte, int) error
	walk = func(boxes []byte, depth int) error {
		if depth > 4 {
			return ErrInvalidImage
		}
		for len(boxes) > 0 {
			if len(boxes) < 8 {
				return ErrInvalidImage
			}
			n, header := uint64(binary.BigEndian.Uint32(boxes[:4])), uint64(8)
			if n == 1 {
				if len(boxes) < 16 {
					return ErrInvalidImage
				}
				n, header = binary.BigEndian.Uint64(boxes[8:16]), 16
			} else if n == 0 {
				n = uint64(len(boxes))
			}
			if n < header || n > uint64(len(boxes)) {
				return ErrInvalidImage
			}
			body := boxes[header:n]
			switch string(boxes[4:8]) {
			case "meta":
				if len(body) < 4 {
					return ErrInvalidImage
				}
				if err := walk(body[4:], depth+1); err != nil {
					return err
				}
			case "iprp", "ipco":
				if err := walk(body, depth+1); err != nil {
					return err
				}
			case "ispe":
				if len(body) != 12 {
					return ErrInvalidImage
				}
				w, h := binary.BigEndian.Uint32(body[4:8]), binary.BigEndian.Uint32(body[8:12])
				if err := checkImageDimensions(int(w), int(h)); err != nil {
					return err
				}
				count++
			}
			boxes = boxes[n:]
		}
		return nil
	}
	if err := walk(data, 0); err != nil {
		return err
	}
	if count == 0 {
		return ErrInvalidImage
	}
	return nil
}

func findHEIFConverter() (string, error) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		return "", ErrHEIFUnavailable
	}
	if configured := os.Getenv("CROPTOP_HEIF_CONVERTER"); configured != "" {
		switch filepath.Base(configured) {
		case "heif-convert", "heif-dec", "sips":
			if p, err := exec.LookPath(configured); err == nil {
				return p, nil
			}
		}
		return "", ErrHEIFUnavailable
	}
	if runtime.GOOS == "darwin" {
		if p, err := exec.LookPath("sips"); err == nil {
			return p, nil
		}
	}
	for _, name := range []string{"heif-dec", "heif-convert"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	return "", ErrHEIFUnavailable
}

func convertHEIF(ctx context.Context, data []byte, outputDir string) (string, func(), error) {
	converter, err := findHEIFConverter()
	if err != nil {
		return "", nil, err
	}
	dir, err := os.MkdirTemp(outputDir, ".heif-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	success := false
	defer func() {
		if !success {
			cleanup()
		}
	}()
	in, out := filepath.Join(dir, "input.heic"), filepath.Join(dir, "converted.png")
	if err := os.WriteFile(in, data, 0600); err != nil {
		return "", nil, err
	}
	args := []string{in, out}
	if filepath.Base(converter) == "sips" {
		args = []string{"--setProperty", "format", "png", "--optimizeColorForSharing", in, "--out", out}
	} else if filepath.Base(converter) == "heif-dec" {
		// The pinned production decoder supports explicit bounded parallelism.
		args = []string{"--codec-threads", "1", "--tile-threads", "1", in, out}
	}
	childCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(childCtx, converter, args...)
	if runtime.GOOS == "linux" {
		// A constant shell program sets child-only kernel limits. All paths are
		// positional arguments; none is interpreted as shell source.
		limits := "ulimit -v " + strconv.Itoa(converterMemoryKiB) + " && ulimit -f " + strconv.Itoa(maxConvertedImageBytes/512) + " && ulimit -t 30 && exec \"$@\""
		cmd = exec.CommandContext(childCtx, "/bin/sh", append([]string{"-c", limits, "croptop-image", converter}, args...)...)
	}
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "TMPDIR="+dir)
	configureMediaProcess(cmd)
	cmd.WaitDelay = time.Second
	// Some containers include private filenames/metadata in diagnostic output.
	// Discard it, avoiding both leakage and unbounded stderr accumulation.
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err := cmd.Start(); err != nil {
		return "", nil, ErrHEIFUnavailable
	}
	// Clean up descendants even if the parent exited before cancellation.
	defer func() { _ = cmd.Cancel() }()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	var runErr error
	running := true
	for running {
		select {
		case runErr = <-done:
			running = false
		case <-ticker.C:
			if err := checkConversionFiles(dir); err != nil {
				cancel()
				<-done
				return "", nil, err
			}
		case <-ctx.Done():
			cancel()
			<-done
			return "", nil, ctx.Err()
		}
	}
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
	if runErr != nil {
		return "", nil, ErrInvalidImage
	}
	if err := checkConversionFiles(dir); err != nil {
		return "", nil, err
	}
	info, err := os.Lstat(out)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return "", nil, ErrInvalidImage
	}
	// libheif writes suffixed filenames for multi-image inputs. Requiring this
	// exact output rejects those inputs instead of silently choosing one image.
	success = true
	return out, cleanup, nil
}

func checkConversionFiles(dir string) error {
	files, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var total int64
	for _, f := range files {
		if f.Name() == "input.heic" {
			continue
		}
		if f.Type()&os.ModeSymlink != 0 || f.IsDir() {
			return ErrInvalidImage
		}
		if strings.HasSuffix(f.Name(), ".png") && f.Name() != "converted.png" {
			return ErrUnsupportedImage
		}
		info, err := f.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		if total > maxConvertedImageBytes {
			return errors.New("converted image exceeds output limit")
		}
	}
	return nil
}
