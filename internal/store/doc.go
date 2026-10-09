package store

import "encoding/json"

// doc is the persistence trick shared by Site and Post: every key read from
// disk is kept verbatim in Raw, and typed fields are overlaid on save. A file
// written by the Mac app and rewritten by us therefore loses nothing, and
// keys we never model (pinning services, podcast settings, ...) survive.
type doc map[string]json.RawMessage

func (d doc) clone() doc {
	out := make(doc, len(d))
	for k, v := range d {
		out[k] = v
	}
	return out
}

func (d doc) put(key string, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err) // only called with plain values
	}
	d[key] = b
}

func (d doc) putIfNotNil(key string, v any) {
	switch x := v.(type) {
	case *string:
		if x != nil {
			d.put(key, *x)
		}
	case *int:
		if x != nil {
			d.put(key, *x)
		}
	case *bool:
		if x != nil {
			d.put(key, *x)
		}
	case *AppleTime:
		if x != nil {
			d.put(key, *x)
		}
	case []string:
		if x != nil {
			d.put(key, x)
		}
	case map[string]string:
		if x != nil {
			d.put(key, x)
		}
	}
}

func str(s string) *string { return &s }
