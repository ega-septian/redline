package contract

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"redline/internal/report"
)

// Violations membandingkan response yang tercatat (status dan bentuk) dengan kontrak, tanpa AI.
// Hanya endpoint yang terdokumentasi yang dinilai. Field tambahan di response bukan pelanggaran;
// field yang didokumentasikan tapi tidak ada, atau tipenya berbeda, adalah pelanggaran.
func (d *Document) Violations(calls []report.HTTPCall) []string {
	var out []string
	for _, c := range uniqueCalls(calls) {
		tmpl := d.match(c.Path)
		if tmpl == "" {
			continue
		}
		op, ok := d.paths[tmpl][strings.ToLower(c.Method)].(map[string]any)
		if !ok {
			continue
		}
		prefix := strings.ToUpper(c.Method) + " " + c.Path
		responses, _ := op["responses"].(map[string]any)
		resp, documented := responses[strconv.Itoa(c.Status)]
		if !documented {
			if _, hasDefault := responses["default"]; !hasDefault {
				out = append(out, fmt.Sprintf("%s: status %d tidak ada di kontrak (yang terdokumentasi: %s)",
					prefix, c.Status, strings.Join(statusCodes(responses), ", ")))
			}
			continue
		}
		r, _ := d.resolve(resp, 0).(map[string]any)
		schema := d.jsonSchema(r)
		if schema == nil || len(c.Shape) == 0 || string(c.Shape) == "null" {
			continue
		}
		var shape any
		if json.Unmarshal(c.Shape, &shape) != nil {
			continue
		}
		var found []string
		d.compare(schema, shape, "", 0, &found)
		for _, v := range found {
			if len(out) >= 6 {
				break
			}
			out = append(out, prefix+": "+v)
		}
	}
	return out
}

func statusCodes(responses map[string]any) []string {
	codes := make([]string, 0, len(responses))
	for code := range responses {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	return codes
}

func (d *Document) jsonSchema(obj map[string]any) any {
	content, _ := obj["content"].(map[string]any)
	media, ok := content["application/json"].(map[string]any)
	if !ok {
		return nil
	}
	return d.resolve(media["schema"], 0)
}

// compare mencocokkan bentuk (dari fixture: "number", "string|null", [elemen], {field: bentuk}) dengan schema.
func (d *Document) compare(schemaV, shape any, path string, depth int, out *[]string) {
	schema, ok := d.resolve(schemaV, 0).(map[string]any)
	if !ok || depth > 6 {
		return
	}
	if list, ok := schema["allOf"].([]any); ok {
		for _, part := range list {
			d.compare(part, shape, path, depth+1, out)
		}
		return
	}
	if _, ok := schema["oneOf"]; ok {
		return // alternatif: terlalu longgar untuk dinilai tanpa AI
	}
	if _, ok := schema["anyOf"]; ok {
		return
	}
	at := path
	if at == "" {
		at = "body"
	}
	want := expectedKind(schema)
	got := shapeKind(shape)
	if want == "" || got == "" || strings.Contains(got, "...") || got == "object" && want == "object" && !isMap(shape) {
		return
	}
	if !kindMatches(want, got, schema) {
		*out = append(*out, fmt.Sprintf("%s bertipe %s, kontrak: %s", at, got, describeKind(want, schema)))
		return
	}
	switch want {
	case "object":
		obj, ok := shape.(map[string]any)
		props, _ := schema["properties"].(map[string]any)
		if !ok || len(props) == 0 {
			return
		}
		names := make([]string, 0, len(props))
		for name := range props {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			child := join(path, name)
			v, present := obj[name]
			if !present {
				*out = append(*out, fmt.Sprintf("field %s ada di kontrak tapi tidak ada di response", child))
				continue
			}
			d.compare(props[name], v, child, depth+1, out)
		}
	case "array":
		items, ok := shape.([]any)
		if ok && len(items) > 0 {
			d.compare(schema["items"], items[0], path+"[]", depth+1, out)
		}
	}
}

func join(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}

func isMap(v any) bool { _, ok := v.(map[string]any); return ok }

// shapeKind: jenis bentuk dari fixture (string, number, boolean, null, array, object, atau gabungan "a|b").
func shapeKind(shape any) string {
	switch v := shape.(type) {
	case string:
		return v
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return ""
}

func expectedKind(schema map[string]any) string {
	if t, ok := schema["type"].(string); ok {
		return t
	}
	if _, ok := schema["properties"]; ok {
		return "object"
	}
	if _, ok := schema["items"]; ok {
		return "array"
	}
	return ""
}

// kindMatches: setiap jenis yang tercatat harus diizinkan kontrak. integer dan number sama-sama "number" di JavaScript.
func kindMatches(want, got string, schema map[string]any) bool {
	nullable, _ := schema["nullable"].(bool)
	for _, k := range strings.Split(got, "|") {
		switch {
		case k == "null" && nullable:
		case k == "number" && (want == "number" || want == "integer"):
		case k == want:
		default:
			return false
		}
	}
	return true
}

func describeKind(want string, schema map[string]any) string {
	if nullable, _ := schema["nullable"].(bool); nullable {
		return want + " atau null"
	}
	return want
}
