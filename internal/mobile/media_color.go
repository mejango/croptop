package mobile

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
)

// Color metadata is intentionally bounded independently of compressed pixels.
// Only the profile goes to lcms; EXIF, XMP, names and other source metadata never
// reach the normalized PNG or leave the private staging directory.
const maxICCProfileBytes = 1 << 20

func imageColorProfile(data []byte, typ string) ([]byte, error) {
	if typ == "heif" {
		return heifColorProfile(data)
	}
	var profile []byte
	var colorErr error
	setProfile := func(p []byte) {
		if profile != nil || !validRGBICC(p) {
			colorErr = ErrImageColor
			return
		}
		profile = p
	}
	if typ == "jpeg" {
		var pieces [256][]byte
		total, found := 0, 0
		var exifNonSRGB bool
		for p := 2; p+4 <= len(data); {
			if data[p] != 0xff {
				break
			}
			for p+1 < len(data) && data[p+1] == 0xff {
				p++
			}
			if p+4 > len(data) || data[p+1] == 0xda || data[p+1] == 0xd9 {
				break
			}
			n := int(binary.BigEndian.Uint16(data[p+2 : p+4]))
			if n < 2 || n > len(data)-p-2 {
				break
			}
			part := data[p+4 : p+2+n]
			if data[p+1] == 0xe1 && bytes.HasPrefix(part, []byte("Exif\x00\x00")) {
				exifNonSRGB = exifNonSRGB || tiffNonSRGB(part[6:])
			}
			if data[p+1] == 0xe2 && bytes.HasPrefix(part, []byte("ICC_PROFILE\x00")) {
				if len(part) < 14 || part[12] == 0 || part[13] == 0 || part[12] > part[13] || (total != 0 && total != int(part[13])) || pieces[part[12]] != nil {
					return nil, ErrImageColor
				}
				total, pieces[part[12]] = int(part[13]), part[14:]
				found++
			}
			// Gain-map/multi-picture JPEG needs an HDR policy, not an accidental
			// extraction of its SDR base image. EXIF itself is still discarded.
			if (data[p+1] == 0xe2 && bytes.HasPrefix(part, []byte("MPF\x00"))) ||
				(data[p+1] == 0xe1 && (bytes.Contains(part, []byte("hdr-gain-map")) || bytes.Contains(part, []byte("hdrgm:")))) {
				return nil, ErrImageColor
			}
			p += n + 2
		}
		if total != found {
			return nil, ErrImageColor
		}
		if total != 0 {
			var assembled []byte
			for i := 1; i <= total; i++ {
				if pieces[i] == nil || len(assembled)+len(pieces[i]) > maxICCProfileBytes {
					return nil, ErrImageColor
				}
				assembled = append(assembled, pieces[i]...)
			}
			setProfile(assembled)
		}
		if exifNonSRGB && len(profile) == 0 {
			return nil, ErrImageColor
		}
		return profile, colorErr
	}
	if typ == "png" && len(data) > 24 && data[24] == 16 {
		return nil, ErrImageColor // no implicit HDR/high-bit-depth truncation
	}
	var nonSRGB, cicp, webpICC bool
	walkRasterChunks(data, typ, func(tag string, payload []byte) bool {
		switch tag {
		case "iCCP":
			zero := bytes.IndexByte(payload, 0)
			if zero < 1 || zero > 79 || zero+2 > len(payload) || payload[zero+1] != 0 {
				colorErr = ErrImageColor
				return false
			}
			reader, err := zlib.NewReader(bytes.NewReader(payload[zero+2:]))
			if err != nil {
				colorErr = ErrImageColor
				return false
			}
			p, err := io.ReadAll(io.LimitReader(reader, maxICCProfileBytes+1))
			closeErr := reader.Close()
			if err != nil || closeErr != nil {
				colorErr = ErrImageColor
				return false
			}
			setProfile(p)
		case "ICCP":
			setProfile(payload)
		case "VP8X":
			if len(payload) > 0 && payload[0]&0x20 != 0 {
				webpICC = true
			}
		case "cICP":
			// H.273: BT.709 primaries, sRGB transfer, RGB identity matrix,
			// full-range. PQ/HLG and any other declaration fail closed.
			if cicp || !bytes.Equal(payload, []byte{1, 13, 0, 1}) {
				colorErr = ErrImageColor
			}
			cicp = true
		case "gAMA":
			if len(payload) != 4 || binary.BigEndian.Uint32(payload) != 45455 {
				nonSRGB = true
			}
		case "cHRM":
			want := [...]uint32{31270, 32900, 64000, 33000, 30000, 60000, 15000, 6000}
			if len(payload) != 32 {
				nonSRGB = true
				break
			}
			for i, n := range want {
				if binary.BigEndian.Uint32(payload[i*4:]) != n {
					nonSRGB = true
				}
			}
		case "cLLI", "cLLi":
			// Content-light metadata is not a transfer function: SDR ImageIO
			// screenshots also carry it. Validate then discard under our
			// supported/assumed-SDR policy; PQ/HLG are rejected separately.
			if len(payload) != 8 {
				colorErr = ErrImageColor
				break
			}
			peak, average := binary.BigEndian.Uint32(payload), binary.BigEndian.Uint32(payload[4:])
			if peak != 0 && average != 0 && average > peak {
				colorErr = ErrImageColor
			}
		case "mDCV", "mDCv":
			// Mastering metadata can also describe SDR, but this pilot has no
			// policy for reproducing its viewing conditions.
			colorErr = ErrImageColor
		}
		return colorErr == nil
	})
	if nonSRGB && len(profile) == 0 {
		colorErr = ErrImageColor
	}
	// PNG cICP has precedence over ICC. This pilot rejects conflicting/mixed
	// declarations instead of applying an ICC transform to differently encoded
	// pixels. A WebP with its ICC flag but no profile is similarly ambiguous.
	if (cicp && len(profile) != 0) || (webpICC && len(profile) == 0) {
		colorErr = ErrImageColor
	}
	return profile, colorErr
}

func tiffNonSRGB(data []byte) bool {
	if len(data) < 8 {
		return false
	}
	var order binary.ByteOrder
	switch string(data[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return false
	}
	if order.Uint16(data[2:4]) != 42 {
		return false
	}
	var inspect func(uint64, int) bool
	inspect = func(offset uint64, depth int) bool {
		if offset > uint64(len(data)-2) {
			return false
		}
		count := int(order.Uint16(data[offset:]))
		for p, i := int(offset)+2, 0; i < count && p+12 <= len(data); p, i = p+12, i+1 {
			tag, typ, n := order.Uint16(data[p:]), order.Uint16(data[p+2:]), order.Uint32(data[p+4:])
			if depth == 0 && tag == 0x8769 && typ == 4 && n == 1 {
				if inspect(uint64(order.Uint32(data[p+8:])), 1) {
					return true
				}
			}
			if depth == 1 && tag == 0xa005 && typ == 4 && n == 1 {
				if inspect(uint64(order.Uint32(data[p+8:])), 2) {
					return true
				}
			}
			if depth == 2 && tag == 1 && typ == 2 && n == 4 && string(data[p+8:p+12]) == "R03\x00" {
				return true // Adobe RGB interoperability declaration
			}
			if depth == 1 && tag == 0xa001 && typ == 3 && n == 1 {
				if order.Uint16(data[p+8:]) != 1 {
					return true
				}
			}
		}
		return false
	}
	return inspect(uint64(order.Uint32(data[4:])), 0)
}

// Structural validation is only a first bound: lcms independently opens the
// profile and must create a real RGB->sRGB transform. It has no fallback profile.
func validRGBICC(p []byte) bool {
	if len(p) < 132 || len(p) > maxICCProfileBytes || int(binary.BigEndian.Uint32(p)) != len(p) || string(p[36:40]) != "acsp" || string(p[16:20]) != "RGB " {
		return false
	}
	if string(p[20:24]) != "XYZ " && string(p[20:24]) != "Lab " {
		return false
	}
	if string(p[12:16]) != "mntr" && string(p[12:16]) != "scnr" {
		return false
	}
	count := uint64(binary.BigEndian.Uint32(p[128:132]))
	if count == 0 || count > uint64((len(p)-132)/12) {
		return false
	}
	for i := uint64(0); i < count; i++ {
		tag := p[132+i*12 : 144+i*12]
		offset, size := uint64(binary.BigEndian.Uint32(tag[4:8])), uint64(binary.BigEndian.Uint32(tag[8:12]))
		if offset < 132+count*12 || size < 8 || offset+size > uint64(len(p)) {
			return false
		}
		// ICC v4's optional CICP tag can identify HDR even with 8-bit channels.
		if string(tag[:4]) == "cicp" {
			body := p[offset : offset+size]
			if len(body) < 12 || body[9] == 16 || body[9] == 18 {
				return false
			}
		}
	}
	return true
}

func heifColorProfile(data []byte) ([]byte, error) {
	var profile []byte
	var nonSRGB bool
	err := walkHEIFBoxes(data, func(tag string, body []byte) error {
		switch tag {
		case "pixi":
			if len(body) < 5 || len(body) != 5+int(body[4]) {
				return ErrImageColor
			}
			for _, depth := range body[5:] {
				if depth != 8 {
					return ErrImageColor
				}
			}
		case "hvcC":
			// HEVCDecoderConfigurationRecord carries bit depths separately
			// from pixi; inspect both rather than trusting a possibly missing pixi.
			if len(body) < 23 || body[17]&7 != 0 || body[18]&7 != 0 {
				return ErrImageColor
			}
		case "auxC":
			if bytes.Contains(body, []byte("hdrgainmap")) || bytes.Contains(body, []byte("21496")) {
				return ErrImageColor
			}
		case "colr":
			if len(body) < 4 {
				return ErrImageColor
			}
			switch string(body[:4]) {
			case "prof", "rICC":
				if profile != nil || !validRGBICC(body[4:]) {
					return ErrImageColor
				}
				profile = body[4:]
			case "nclx":
				if len(body) != 11 {
					return ErrImageColor
				}
				primaries, transfer := binary.BigEndian.Uint16(body[4:6]), binary.BigEndian.Uint16(body[6:8])
				if transfer == 16 || transfer == 18 {
					return ErrImageColor
				}
				// Unspecified SDR color is treated like an unprofiled raster.
				if (primaries != 1 && primaries != 2) || (transfer != 13 && transfer != 2) {
					nonSRGB = true
				}
			default:
				return ErrImageColor
			}
		}
		return nil
	})
	if err == nil && nonSRGB && len(profile) == 0 {
		err = ErrImageColor
	}
	return profile, err
}

// Converted PNGs can be much larger than uploads. Only color-related chunks
// are read into memory; bitmap data and arbitrary ancillary fields are skipped.
func convertedPNGColorProfile(f *os.File) ([]byte, error) {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	var signature [8]byte
	if _, err := io.ReadFull(f, signature[:]); err != nil || string(signature[:]) != "\x89PNG\r\n\x1a\n" {
		return nil, ErrInvalidImage
	}
	data := append([]byte{}, signature[:]...)
	for {
		var header [8]byte
		if _, err := io.ReadFull(f, header[:]); err != nil {
			return nil, ErrInvalidImage
		}
		n := int64(binary.BigEndian.Uint32(header[:4]))
		if n > maxConvertedImageBytes {
			return nil, ErrInvalidImage
		}
		tag := string(header[4:8])
		switch tag {
		case "IHDR", "iCCP", "cICP", "gAMA", "cHRM", "mDCV", "cLLI", "mDCv", "cLLi":
			if n > maxICCProfileBytes || len(data)+int(n)+12 > maxICCProfileBytes+4096 {
				return nil, ErrImageColor
			}
			data = append(data, header[:]...)
			start := len(data)
			data = append(data, make([]byte, n+4)...)
			if _, err := io.ReadFull(f, data[start:]); err != nil {
				return nil, ErrInvalidImage
			}
		default:
			if _, err := f.Seek(n+4, io.SeekCurrent); err != nil {
				return nil, err
			}
		}
		if tag == "IEND" {
			break
		}
	}
	return imageColorProfile(data, "png")
}

func findColorConverter() (string, error) {
	name := os.Getenv("CROPTOP_COLOR_CONVERTER")
	if name == "" {
		name = "croptop-color"
	}
	if name == "disabled" {
		return "", ErrColorUnavailable
	}
	p, err := exec.LookPath(name)
	if err != nil {
		return "", ErrColorUnavailable
	}
	return p, nil
}

func normalizeColorRaster(ctx context.Context, r io.ReadSeeker, typ string, orientation int, profile []byte, outputDir string) (Media, error) {
	if len(profile) == 0 {
		return normalizeRaster(ctx, r, typ, orientation, outputDir)
	}
	converter, err := findColorConverter()
	if err != nil {
		return Media{}, err
	}
	dir, err := os.MkdirTemp(outputDir, ".color-*")
	if err != nil {
		return Media{}, err
	}
	defer os.RemoveAll(dir)
	media, err := normalizeRaster(ctx, r, typ, orientation, dir)
	if err != nil {
		return Media{}, err
	}
	in, out, icc := filepath.Join(dir, "pixels.raster"), filepath.Join(dir, "converted.png"), filepath.Join(dir, "input.icc")
	if err := os.Rename(media.Path, in); err != nil {
		return Media{}, err
	}
	if err := os.WriteFile(icc, profile, 0600); err != nil {
		return Media{}, err
	}
	args := []string{in, icc, out, strconv.Itoa(MaxImagePixels), strconv.Itoa(maxICCProfileBytes)}
	if err := runMediaConverter(ctx, converter, args, dir); err != nil {
		if ctx.Err() != nil {
			return Media{}, ctx.Err()
		}
		if errors.Is(err, ErrHEIFUnavailable) {
			return Media{}, ErrColorUnavailable
		}
		return Media{}, ErrImageColor
	}
	info, err := os.Lstat(out)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return Media{}, ErrImageColor
	}
	f, err := os.Open(out)
	if err != nil {
		return Media{}, err
	}
	defer f.Close()
	return normalizeRaster(ctx, f, "png", 1, outputDir)
}
