package render

import (
	"bytes"
	"context"
	"github.com/mejango/croptop/internal/store"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreviewCoverUsesTitleInsteadOfEmbeddedMedia(t *testing.T) {
	for _, inline := range []bool{false, true} {
		name := "attachment"
		if inline {
			name = "inline"
		}
		t.Run(name, func(t *testing.T) {
			r, s := fixtureRenderer(t)
			p := &store.Post{ID: "preview-cover", Title: "Audio", Created: store.Now(), Content: "<audio src=\"data:audio/mp4;base64," + strings.Repeat("A", 64000) + "\"></audio>"}
			if inline {
				p.Content += `<script type="croptop/preview">export default () => {}</script>`
			} else {
				p.Attachments = []string{"preview.js"}
				dir := filepath.Join(s.ArticlesDir(fixtureID), p.ID)
				if err := os.MkdirAll(dir, 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "preview.js"), []byte("export default () => {}"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.SavePost(fixtureID, p); err != nil {
				t.Fatal(err)
			}
			if err := r.Render(context.Background(), fixtureID); err != nil {
				t.Fatal(err)
			}
			expected := filepath.Join(t.TempDir(), "expected.png")
			if err := WriteCover(expected, "Audio"); err != nil {
				t.Fatal(err)
			}
			want, err := os.ReadFile(expected)
			if err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(filepath.Join(s.PublicDir(fixtureID), p.ID, "_cover.png"))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Fatal("preview fallback cover contains source instead of the title")
			}
		})
	}
}
