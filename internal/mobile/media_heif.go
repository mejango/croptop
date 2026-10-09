package mobile

import (
	"bytes"
	"os"
	"os/exec"
)

const appleSDRGainMap = "urn:com:apple:photo:2020:aux:hdrgainmap"

// Recognition only chooses the safe decoder; it does not authorize an input's
// colors. The primary-only helper owns item selection and validates the actual
// primary and every decoded grid tile through libheif's own item APIs. Existing
// HEIF behavior is unchanged for files without this Apple SDR-base auxiliary.
func hasAppleSDRGainMap(data []byte) (bool, error) {
	var found bool
	err := walkHEIFBoxes(data, func(tag string, body []byte) error {
		if tag != "auxC" || len(body) < 5 || !bytes.Equal(body[:4], []byte{0, 0, 0, 0}) {
			return nil
		}
		end := bytes.IndexByte(body[4:], 0)
		if end >= 0 && string(body[4:4+end]) == appleSDRGainMap {
			found = true
		}
		return nil
	})
	return found, err
}

func findHEIFPrimaryConverter() (string, error) {
	if os.Getenv("CROPTOP_HEIF_CONVERTER") == "disabled" {
		return "", ErrHEIFUnavailable
	}
	name := os.Getenv("CROPTOP_HEIF_PRIMARY_CONVERTER")
	if name == "" {
		name = "croptop-heif"
	}
	if name == "disabled" {
		return "", ErrHEIFUnavailable
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return "", ErrHEIFUnavailable
	}
	return path, nil
}
