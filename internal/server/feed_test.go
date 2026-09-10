package server

import (
	"strings"
	"testing"

	"github.com/mejango/croptop/internal/render"
)

func TestPlainText(t *testing.T) {
	in := "# Title\n\nSome **bold** text with a [link](https://x.y) and <b>html</b>.<script type=\"module\">alert(1)</script>\n\n`code`"
	got := plainText(in, 240)
	want := "Title Some bold text with a link and html. code"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	long := plainText(strings.Repeat("word ", 100), 40)
	if len(long) > 45 || !strings.HasSuffix(long, "…") {
		t.Fatalf("truncation: %q", long)
	}
}

func TestHasPreview(t *testing.T) {
	if !hasPreview(render.PublicPost{Attachments: []string{"preview.js"}}) {
		t.Fatal("attachment")
	}
	if !hasPreview(render.PublicPost{Content: `<script type="croptop/preview">export default () => {}</script>`}) {
		t.Fatal("inline")
	}
	if hasPreview(render.PublicPost{Content: "<script type=\"module\"></script>"}) {
		t.Fatal("module is not a preview")
	}
}
