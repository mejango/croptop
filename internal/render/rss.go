package render

import (
	_ "embed"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/flosch/pongo2/v6"

	"github.com/mejango/croptop/internal/gateway"
	"github.com/mejango/croptop/internal/store"
)

// Planet's RSS template (MIT, Planetable Development Team), rendered with
// the same context Planet gives it so existing subscribers see no change.
//
//go:embed planet/RSS.xml
var rssTemplate string

func init() {
	pongo2.RegisterFilter("md2html", func(in *pongo2.Value, _ *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
		return pongo2.AsSafeValue(Markdown(in.String())), nil
	})
	pongo2.RegisterFilter("rfc822", func(in *pongo2.Value, _ *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
		if m, ok := in.Interface().(map[string]any); ok {
			if u, ok := m["timeIntervalSince1970"].(int64); ok {
				return pongo2.AsValue(time.Unix(u, 0).Local().Format("Mon, 02 Jan 2006 15:04:05 -0700")), nil
			}
		}
		return in, nil
	})
	pongo2.RegisterFilter("hhmmss", func(in *pongo2.Value, _ *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
		s := in.Integer()
		return pongo2.AsValue(pad(s/3600) + ":" + pad(s%3600/60) + ":" + pad(s%60)), nil
	})
}

func pad(n int) string {
	if n < 10 {
		return "0" + itoa(n)
	}
	return itoa(n)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// Stencil's absoluteImageURL takes two arguments; pongo2 filters take one,
// so the value is precomputed per article as content_html_abs.
var rssAbsImage = regexp.MustCompile(`\{\{\s*article\.content\|md2html\|absoluteImageURL:root_prefix,article\.id\s*\}\}`)
var imgSrc = regexp.MustCompile(`(<img\b[^>]*\bsrc=")([^"]+)(")`)

func absoluteImageURLs(html, prefix string) string {
	return imgSrc.ReplaceAllStringFunc(html, func(m string) string {
		parts := imgSrc.FindStringSubmatch(m)
		src := parts[2]
		if strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://") {
			return m
		}
		return parts[1] + prefix + src + parts[3]
	})
}

// RootPrefix is Planet's root_prefix: the site's public URL without the
// trailing slash. A domain that is not .eth/.sol/.bit is used as written.
func RootPrefix(site *store.Site) string {
	if site.Domain != nil {
		d := strings.TrimSpace(*site.Domain)
		if d != "" && !strings.HasSuffix(d, ".eth") && !strings.HasSuffix(d, ".sol") && !strings.HasSuffix(d, ".bit") && !strings.Contains(d, ":") {
			return "https://" + d
		}
	}
	return strings.TrimSuffix(gateway.URL(site), "/")
}

func (r *Renderer) writeRSS(site *store.Site, pubDir string, planet map[string]any, articles []map[string]any) error {
	root := RootPrefix(site)
	items := make([]map[string]any, 0, len(articles))
	for _, a := range articles {
		c := make(map[string]any, len(a)+1)
		for k, v := range a {
			c[k] = v
		}
		content, _ := a["content"].(string)
		id, _ := a["id"].(string)
		c["content_html_abs"] = absoluteImageURLs(Markdown(content), root+"/"+id+"/")
		items = append(items, c)
	}
	p := make(map[string]any, len(planet)+1)
	for k, v := range planet {
		p[k] = v
	}
	p["articles"] = items
	src := rssAbsImage.ReplaceAllString(rssTemplate, "{{ article.content_html_abs }}")
	tpl, err := pongo2.FromString(applyShims(src))
	if err != nil {
		return err
	}
	author := ""
	if v, ok := site.Raw["authorName"]; ok {
		jsonUnmarshal(v, &author)
	}
	hasDomain := site.Domain != nil && strings.TrimSpace(*site.Domain) != "" && !strings.Contains(*site.Domain, ":")
	out, err := tpl.Execute(pongo2.Context{
		"planet": p, "root_prefix": root, "podcast": false, "podcast_author_name": author,
		"has_podcast_cover_art": false, "has_domain": hasDomain, "domain": strings.TrimPrefix(root, "https://"),
	})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(pubDir, "rss.xml"), []byte(out), 0o644)
}
