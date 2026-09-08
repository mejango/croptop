// Package templates embeds the Croptop site template (git submodule of
// Planetable/SiteTemplateCroptop) into the binary.
package templates

import (
	"embed"
	"io/fs"
)

//go:embed croptop/template.json croptop/templates all:croptop/assets
var embedded embed.FS

// FS is rooted at the template directory (template.json, templates/, assets/).
var FS fs.FS = mustSub(embedded, "croptop")

func mustSub(f embed.FS, dir string) fs.FS {
	s, err := fs.Sub(f, dir)
	if err != nil {
		panic(err)
	}
	return s
}
