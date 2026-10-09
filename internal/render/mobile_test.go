package render

import (
	"encoding/json"
	"testing"
	"testing/fstest"

	"github.com/mejango/croptop/internal/store"
)

func TestMobileTemplateCompatibility(t *testing.T) {
	template := fstest.MapFS{
		"template.json":        {Data: []byte(`{"name":"Croptop"}`)},
		"templates/index.html": {Data: []byte("index")},
		"assets/style.css":     {Data: []byte("styles")},
	}
	digest, err := MobileTemplateDigest(template)
	if err != nil {
		t.Fatal(err)
	}
	template["README.md"] = &fstest.MapFile{Data: []byte("not rendered")}
	unchanged, _ := MobileTemplateDigest(template)
	if digest != unchanged {
		t.Fatal("documentation changed render identity")
	}
	site := &store.Site{}
	if err := json.Unmarshal([]byte(`{"croptopMobile":{"version":1,"templateDigest":"`+digest+`"}}`), site); err != nil {
		t.Fatal(err)
	}
	if err := CheckMobileCompatibility(site, digest); err != nil {
		t.Fatal(err)
	}
	template["assets/style.css"].Data = []byte("other styles")
	changed, _ := MobileTemplateDigest(template)
	if err := CheckMobileCompatibility(site, changed); err == nil {
		t.Fatal("accepted different template assets")
	}
	if err := CheckMobileCompatibility(&store.Site{}, digest); err == nil {
		t.Fatal("accepted unverified legacy template")
	}
	if err := CheckMobileCompatibility(site, ""); err == nil {
		t.Fatal("accepted missing service template")
	}
}
