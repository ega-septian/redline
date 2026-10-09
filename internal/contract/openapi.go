// Package contract membaca kontrak API (OpenAPI 3, JSON) dan meringkas bagian yang relevan untuk
// endpoint yang dipanggil test yang gagal. Untuk test baru (belum pernah lulus), kontrak adalah satu-satunya
// acuan untuk memutuskan siapa yang menyimpang: test atau API.
package contract

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"redline/internal/report"
)

// Registry memetakan project Playwright ke URL dokumen OpenAPI-nya.
// Dokumen diambil setiap run (tidak di-cache) karena kontrak bisa berubah antar versi aplikasi.
type Registry struct {
	Specs map[string]string // project -> URL OpenAPI (JSON)
	HTTP  *http.Client
}

func NewRegistry(specs map[string]string) *Registry {
	return &Registry{Specs: specs, HTTP: &http.Client{Timeout: 10 * time.Second}}
}

// ParseSpecs membaca "toolshop=http://localhost:8091/docs,gorest=https://..." (format env OPENAPI_SPECS).
func ParseSpecs(s string) (map[string]string, error) {
	out := map[string]string{}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		project, url, ok := strings.Cut(part, "=")
		if !ok || strings.TrimSpace(project) == "" || !strings.HasPrefix(strings.TrimSpace(url), "http") {
			return nil, fmt.Errorf("OPENAPI_SPECS: %q harus berbentuk project=http(s)://url", part)
		}
		out[strings.TrimSpace(project)] = strings.TrimSpace(url)
	}
	return out, nil
}

// Fetch mengambil dokumen kontrak project. url kosong (tanpa error) kalau project tidak punya kontrak.
func (r *Registry) Fetch(ctx context.Context, project string) (url string, document []byte, err error) {
	url, ok := r.Specs[project]
	if !ok {
		return "", nil, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return url, nil, err
	}
	resp, err := r.HTTP.Do(req)
	if err != nil {
		return url, nil, fmt.Errorf("ambil kontrak %s: %w", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return url, nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return url, nil, fmt.Errorf("ambil kontrak %s: HTTP %d", url, resp.StatusCode)
	}
	if _, err := Parse(body); err != nil {
		return url, nil, fmt.Errorf("kontrak %s: %w", url, err)
	}
	return url, body, nil
}

var parsed sync.Map // id potret kontrak -> *Document

// ParseCached mem-parse dokumen sekali per id potret (isi dengan id yang sama selalu identik).
func ParseCached(id string, document []byte) (*Document, error) {
	if d, ok := parsed.Load(id); ok {
		return d.(*Document), nil
	}
	doc, err := Parse(document)
	if err != nil {
		return nil, err
	}
	parsed.Store(id, doc)
	return doc, nil
}

// Describe meringkas kontrak untuk endpoint yang dipanggil (unik per method+path, maksimal 4).
func (d *Document) Describe(calls []report.HTTPCall) string {
	var b strings.Builder
	for _, c := range uniqueCalls(calls) {
		if text := d.Operation(c.Method, c.Path); text != "" {
			b.WriteString(text)
		} else {
			fmt.Fprintf(&b, "- %s %s: tidak ada di kontrak (endpoint tidak terdokumentasi)\n", strings.ToUpper(c.Method), c.Path)
		}
	}
	return b.String()
}

func uniqueCalls(calls []report.HTTPCall) []report.HTTPCall {
	seen := map[string]bool{}
	out := []report.HTTPCall{}
	for _, c := range calls {
		key := strings.ToUpper(c.Method) + " " + c.Path
		if seen[key] || len(out) >= 4 {
			continue
		}
		seen[key] = true
		out = append(out, c)
	}
	return out
}

// Document adalah dokumen OpenAPI 3 dalam bentuk mentah (map), cukup untuk dibaca.
type Document struct {
	paths map[string]map[string]any // template path -> method -> operation
	root  map[string]any
	bases []string // path dari servers[].url, misalnya "/api"
}

func Parse(data []byte) (*Document, error) {
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("bukan JSON OpenAPI yang valid: %w", err)
	}
	if _, ok := root["openapi"]; !ok {
		return nil, fmt.Errorf("field \"openapi\" tidak ada (hanya OpenAPI 3 dalam JSON yang didukung)")
	}
	doc := &Document{paths: map[string]map[string]any{}, root: root}
	if paths, ok := root["paths"].(map[string]any); ok {
		for p, v := range paths {
			if ops, ok := v.(map[string]any); ok {
				doc.paths[p] = ops
			}
		}
	}
	if servers, ok := root["servers"].([]any); ok {
		for _, s := range servers {
			if m, ok := s.(map[string]any); ok {
				if u, ok := m["url"].(string); ok {
					if i := strings.Index(strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://"), "/"); i >= 0 {
						host := strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://")
						if base := strings.TrimRight(host[i:], "/"); base != "" {
							doc.bases = append(doc.bases, base)
						}
					}
				}
			}
		}
	}
	return doc, nil
}

// Operation meringkas satu operasi: status yang terdokumentasi beserta schema body request dan response.
func (d *Document) Operation(method, path string) string {
	tmpl := d.match(path)
	if tmpl == "" {
		return ""
	}
	op, ok := d.paths[tmpl][strings.ToLower(method)].(map[string]any)
	if !ok {
		return fmt.Sprintf("- %s %s: method ini tidak ada di kontrak untuk %s\n", strings.ToUpper(method), path, tmpl)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "- %s %s (kontrak: %s)\n", strings.ToUpper(method), path, tmpl)
	if rb, ok := d.resolve(op["requestBody"], 0).(map[string]any); ok {
		if s := d.bodySchema(rb); s != "" {
			fmt.Fprintf(&b, "  request body: %s\n", s)
		}
	}
	responses, _ := op["responses"].(map[string]any)
	codes := make([]string, 0, len(responses))
	for code := range responses {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	for _, code := range codes {
		resp, _ := d.resolve(responses[code], 0).(map[string]any)
		line := "  " + code
		if desc, ok := resp["description"].(string); ok && desc != "" {
			line += " " + clip(strings.Join(strings.Fields(desc), " "), 100)
		}
		if s := d.bodySchema(resp); s != "" {
			line += ": " + s
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

// match mencari template path untuk path yang dicatat fixture ("/brands/:id" cocok dengan "/brands/{id}").
// Base path dari servers[].url ikut dicoba, misalnya "/api/brands" cocok dengan "/brands" kalau server-nya "/api".
func (d *Document) match(path string) string {
	candidates := []string{path}
	for _, base := range d.bases {
		if strings.HasPrefix(path, base+"/") {
			candidates = append(candidates, strings.TrimPrefix(path, base))
		}
	}
	for _, p := range candidates {
		segs := split(p)
		var best string
		bestParams := 1 << 30
		for tmpl := range d.paths {
			tsegs := split(tmpl)
			if len(tsegs) != len(segs) {
				continue
			}
			params, ok := 0, true
			for i := range segs {
				switch {
				case isParam(tsegs[i]):
					params++
				case tsegs[i] != segs[i]:
					ok = false
				}
				if !ok {
					break
				}
			}
			// Template paling spesifik (paling sedikit parameter) menang: /categories/tree, bukan /categories/{id}.
			if ok && params < bestParams {
				best, bestParams = tmpl, params
			}
		}
		if best != "" {
			return best
		}
	}
	return ""
}

func split(p string) []string { return strings.Split(strings.Trim(p, "/"), "/") }

func isParam(seg string) bool {
	return strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}")
}

// bodySchema: schema application/json (atau content type pertama) dari request body / response.
func (d *Document) bodySchema(obj map[string]any) string {
	content, _ := obj["content"].(map[string]any)
	if len(content) == 0 {
		return ""
	}
	media, ok := content["application/json"].(map[string]any)
	if !ok {
		for _, v := range content {
			media, _ = v.(map[string]any)
			break
		}
	}
	schema := d.resolve(media["schema"], 0)
	if schema == nil {
		return ""
	}
	return clip(d.schemaText(schema, 0), 900)
}

// resolve mengikuti $ref lokal ("#/components/...").
func (d *Document) resolve(v any, depth int) any {
	m, ok := v.(map[string]any)
	if !ok || depth > 8 {
		return v
	}
	ref, ok := m["$ref"].(string)
	if !ok || !strings.HasPrefix(ref, "#/") {
		return v
	}
	var cur any = d.root
	for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		cm, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = cm[strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")]
	}
	return d.resolve(cur, depth+1)
}

// schemaText menulis schema secara ringkas, misalnya {id: integer, name: string*, tags: [string]}.
// Tanda * = wajib (required). Kedalaman dibatasi supaya schema rekursif tidak meledak.
func (d *Document) schemaText(v any, depth int) string {
	s, ok := d.resolve(v, 0).(map[string]any)
	if !ok {
		return "?"
	}
	if depth > 4 {
		return "…"
	}
	for _, key := range []string{"allOf", "oneOf", "anyOf"} {
		if list, ok := s[key].([]any); ok {
			parts := make([]string, 0, len(list))
			for _, item := range list {
				parts = append(parts, d.schemaText(item, depth+1))
			}
			sep := map[string]string{"allOf": " & ", "oneOf": " | ", "anyOf": " | "}[key]
			return strings.Join(parts, sep)
		}
	}
	typ, _ := s["type"].(string)
	nullable, _ := s["nullable"].(bool)
	suffix := ""
	if nullable {
		suffix = " | null"
	}
	if enum, ok := s["enum"].([]any); ok {
		vals := make([]string, 0, len(enum))
		for _, e := range enum {
			vals = append(vals, fmt.Sprint(e))
		}
		return "enum(" + strings.Join(vals, ", ") + ")" + suffix
	}
	if props, ok := s["properties"].(map[string]any); ok || typ == "object" {
		required := map[string]bool{}
		if req, ok := s["required"].([]any); ok {
			for _, r := range req {
				if name, ok := r.(string); ok {
					required[name] = true
				}
			}
		}
		if extra, ok := s["additionalProperties"].(map[string]any); ok && len(props) == 0 {
			return "{<field>: " + d.schemaText(extra, depth+1) + "}" + suffix
		}
		names := make([]string, 0, len(props))
		for name := range props {
			names = append(names, name)
		}
		sort.Strings(names)
		fields := make([]string, 0, len(names))
		for _, name := range names {
			mark := ""
			if required[name] {
				mark = "*"
			}
			fields = append(fields, name+mark+": "+d.schemaText(props[name], depth+1))
		}
		return "{" + strings.Join(fields, ", ") + "}" + suffix
	}
	if typ == "array" {
		return "[" + d.schemaText(s["items"], depth+1) + "]" + suffix
	}
	if typ == "" {
		return "any" + suffix
	}
	// Bentuk response yang dicatat fixture memakai tipe JavaScript; integer di sana tercatat sebagai number.
	if typ == "integer" {
		return "number(integer)" + suffix
	}
	if format, ok := s["format"].(string); ok && format != "" {
		return typ + "(" + format + ")" + suffix
	}
	return typ + suffix
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
