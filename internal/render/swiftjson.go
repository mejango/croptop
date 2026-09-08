package render

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// SwiftJSON serializes v the way Swift's JSONEncoder does with
// [.prettyPrinted, .sortedKeys, .withoutEscapingSlashes]: two-space indent,
// `"key" : value`, empty containers as "[\n\n  ]", no trailing newline.
// nft.json.cid.txt is the CID of these exact bytes, so the format matters.
func SwiftJSON(v any) ([]byte, error) {
	var generic any
	raw, err := marshalNoHTML(v)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&generic); err != nil {
		return nil, err
	}
	var b strings.Builder
	writeSwift(&b, generic, 0)
	return []byte(b.String()), nil
}

func marshalNoHTML(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

func writeSwift(b *strings.Builder, v any, depth int) {
	pad := strings.Repeat("  ", depth)
	inner := strings.Repeat("  ", depth+1)
	switch x := v.(type) {
	case map[string]any:
		if len(x) == 0 {
			b.WriteString("{\n\n" + pad + "}")
			return
		}
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteString("{\n")
		for i, k := range keys {
			b.WriteString(inner)
			b.WriteString(quote(k))
			b.WriteString(" : ")
			writeSwift(b, x[k], depth+1)
			if i < len(keys)-1 {
				b.WriteString(",")
			}
			b.WriteString("\n")
		}
		b.WriteString(pad + "}")
	case []any:
		if len(x) == 0 {
			b.WriteString("[\n\n" + pad + "]")
			return
		}
		b.WriteString("[\n")
		for i, e := range x {
			b.WriteString(inner)
			writeSwift(b, e, depth+1)
			if i < len(x)-1 {
				b.WriteString(",")
			}
			b.WriteString("\n")
		}
		b.WriteString(pad + "]")
	case string:
		b.WriteString(quote(x))
	case json.Number:
		b.WriteString(x.String())
	case bool:
		if x {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case nil:
		b.WriteString("null")
	default:
		b.WriteString(fmt.Sprint(x))
	}
}

func quote(s string) string {
	out, _ := marshalNoHTML(s)
	return string(out)
}
