package render

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/flosch/pongo2/v6"
)

// Planet renders Stencil templates. Stencil is Django syntax, as is pongo2,
// but the Croptop template leans on a few Swift-isms. These rewrites are
// applied to template source before parsing:
//
//	x.count > 0            ->  x|length > 0
//	x != nil / x == true   ->  x
//	dict['key']            ->  dict.key
//	'single quoted' names  ->  "double quoted" in include/extends
var shims = []struct {
	re   *regexp.Regexp
	repl string
}{
	{regexp.MustCompile(`([\w.]+)\.count\s*>\s*0`), `$1|length > 0`},
	{regexp.MustCompile(`\s*!=\s*nil`), ``},
	{regexp.MustCompile(`\s*==\s*true`), ``},
	{regexp.MustCompile(`(\w+)\['([\w-]+)'\]`), `$1.$2`},
	{regexp.MustCompile(`({%\s*(?:include|extends)\s+)'([^']*)'`), `$1"$2"`},
}

func applyShims(src string) string {
	for _, s := range shims {
		src = s.re.ReplaceAllString(src, s.repl)
	}
	return src
}

// loader serves templates/*.html from the template FS with shims applied.
// Names resolve from the templates root, like Stencil's FileSystemLoader.
type loader struct{ fsys fs.FS }

func (l loader) Abs(base, name string) string {
	name = strings.TrimPrefix(name, "./")
	return path.Clean(name)
}

func (l loader) Get(p string) (io.Reader, error) {
	b, err := fs.ReadFile(l.fsys, path.Join("templates", p))
	if err != nil {
		return nil, err
	}
	return bytes.NewReader([]byte(applyShims(string(b)))), nil
}

func init() {
	pongo2.SetAutoescape(false) // Stencil does not autoescape; the template uses |escape explicitly
	pongo2.RegisterFilter("mdyydot", dateFilter("1.2.06"))
	pongo2.RegisterFilter("formatDateC", dateFilter("2006-01-02T15:04:05-07:00"))
}

// Dates reach templates as {"timeIntervalSince1970": unix, "apple": seconds}
// so that `article.created.timeIntervalSince1970` keeps working.
func dateValue(unix int64) map[string]any {
	return map[string]any{"timeIntervalSince1970": unix, "apple": float64(unix) - 978307200}
}

func dateFilter(layout string) pongo2.FilterFunction {
	return func(in *pongo2.Value, _ *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
		var t time.Time
		switch x := in.Interface().(type) {
		case map[string]any:
			if u, ok := x["timeIntervalSince1970"].(int64); ok {
				t = time.Unix(u, 0)
			}
		case time.Time:
			t = x
		case float64:
			t = time.Unix(int64(x)+978307200, 0)
		default:
			return in, nil
		}
		return pongo2.AsValue(t.Local().Format(layout)), nil
	}
}

// Meta is template.json.
type Meta struct {
	Name                string                   `json:"name"`
	GenerateNFTMetadata bool                     `json:"generateNFTMetadata"`
	GenerateTagPages    bool                     `json:"generateTagPages"`
	Settings            map[string]MetaSetting `json:"settings"`
}

type MetaSetting struct {
	Name         string `json:"name"`
	Type         string `json:"type"`
	DefaultValue any    `json:"defaultValue"`
	Description  string `json:"description"`
	Advanced     bool   `json:"advanced"`
}

func LoadMeta(fsys fs.FS) (*Meta, error) {
	b, err := fs.ReadFile(fsys, "template.json")
	if err != nil {
		return nil, fmt.Errorf("template.json: %w", err)
	}
	var m Meta
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("template.json: %w", err)
	}
	return &m, nil
}

// SettingsWithDefaults fills missing keys from template.json defaults.
func (m *Meta) SettingsWithDefaults(stored map[string]any) map[string]any {
	out := map[string]any{}
	for k, s := range m.Settings {
		if s.DefaultValue != nil {
			out[k] = fmt.Sprint(s.DefaultValue)
		} else {
			out[k] = ""
		}
	}
	for k, v := range stored {
		out[k] = v
	}
	return out
}

type engine struct {
	set  *pongo2.TemplateSet
	fsys fs.FS
}

func newEngine(fsys fs.FS) *engine {
	return &engine{set: pongo2.NewSet("croptop", loader{fsys}), fsys: fsys}
}

func (e *engine) render(name string, ctx map[string]any) (string, error) {
	t, err := e.set.FromCache(name)
	if err != nil {
		return "", fmt.Errorf("template %s: %w", name, err)
	}
	out, err := t.Execute(pongo2.Context(ctx))
	if err != nil {
		return "", fmt.Errorf("render %s: %w", name, err)
	}
	return out, nil
}

func (e *engine) has(name string) bool {
	_, err := fs.Stat(e.fsys, path.Join("templates", name))
	return err == nil
}
