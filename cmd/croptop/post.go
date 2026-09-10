package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// post hands media files to the running console, which opens a small form
// that turns them into a post. The app's Dock icon calls this for dropped files.
func (a *app) post(paths []string) error {
	if len(paths) == 0 {
		return fmt.Errorf("usage: croptop post <image, video or audio files>")
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		part, _ := mw.CreateFormFile("files", filepath.Base(p))
		_, err = io.Copy(part, f)
		f.Close()
		if err != nil {
			return err
		}
	}
	mw.Close()
	base := "http://" + strings.Replace(a.cfg.Listen, "0.0.0.0", "127.0.0.1", 1)
	resp, err := http.Post(base+"/v0/croptop/quick", mw.FormDataContentType(), &body)
	if err != nil {
		return fmt.Errorf("the console is not running at %s; start croptop first", base)
	}
	defer resp.Body.Close()
	var out struct{ Open string }
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || out.Open == "" {
		return fmt.Errorf("console refused the files: %s", resp.Status)
	}
	openBrowser(base + out.Open)
	return nil
}
