package triage

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"redline/internal/report"
)

// Hasil perbandingan response dengan run terakhir yang lulus.
const (
	ResponseChanged   = "berubah"          // status berubah, field hilang, atau tipe berubah
	ResponseAddedOnly = "hanya field baru" // tidak merusak kontrak
	ResponseSame      = "tidak berubah"    // bentuk sama (nilai bisa berbeda)
	ResponseUnknown   = "tidak diketahui"  // tidak ada data bentuk di salah satu sisi
)

type ResponseDiff struct {
	Verdict string   `json:"verdict"`
	Lines   []string `json:"lines"` // penjelasan per perubahan, sudah siap dibaca manusia/AI
}

// DiffCalls membandingkan bentuk response saat gagal dengan saat terakhir lulus.
// Call dicocokkan berdasarkan method + path, sesuai urutan kemunculan.
func DiffCalls(before, after []report.HTTPCall) ResponseDiff {
	if len(before) == 0 || len(after) == 0 {
		return ResponseDiff{Verdict: ResponseUnknown, Lines: []string{}}
	}
	used := make([]bool, len(before))
	var breaking, added []string
	matched := 0
	for _, a := range after {
		idx := -1
		for i, b := range before {
			if !used[i] && b.Method == a.Method && b.Path == a.Path {
				idx = i
				break
			}
		}
		endpoint := a.Method + " " + a.Path
		if idx < 0 {
			added = append(added, endpoint+": endpoint ini tidak dipanggil saat terakhir lulus")
			continue
		}
		used[idx] = true
		matched++
		b := before[idx]
		if a.Status != b.Status {
			breaking = append(breaking, fmt.Sprintf("%s: status %d -> %d", endpoint, b.Status, a.Status))
			continue // body error biasanya beda total; status sudah cukup menjelaskan
		}
		var bs, as any
		if json.Unmarshal(b.Shape, &bs) != nil || json.Unmarshal(a.Shape, &as) != nil {
			continue
		}
		var br, ad []string
		diffShape("", bs, as, &br, &ad)
		for _, l := range br {
			breaking = append(breaking, endpoint+": "+l)
		}
		for _, l := range ad {
			added = append(added, endpoint+": "+l)
		}
	}

	lines := append(append([]string{}, breaking...), added...)
	if len(lines) > 20 {
		lines = append(lines[:20], fmt.Sprintf("... dan %d perubahan lain", len(lines)-20))
	}
	switch {
	case matched == 0 && len(breaking) == 0:
		return ResponseDiff{Verdict: ResponseUnknown, Lines: lines}
	case len(breaking) > 0:
		return ResponseDiff{Verdict: ResponseChanged, Lines: lines}
	case len(added) > 0:
		return ResponseDiff{Verdict: ResponseAddedOnly, Lines: lines}
	default:
		return ResponseDiff{Verdict: ResponseSame, Lines: lines}
	}
}

// diffShape membandingkan dua bentuk JSON dari fixture Redline:
// string = tipe primitif ("string", "null|string"), []any = array, map = objek.
func diffShape(path string, before, after any, breaking, added *[]string) {
	where := path
	if where == "" {
		where = "(root)"
	}
	switch b := before.(type) {
	case map[string]any:
		a, ok := after.(map[string]any)
		if !ok {
			*breaking = append(*breaking, fmt.Sprintf("tipe berubah di %s: object -> %s", where, kindOf(after)))
			return
		}
		for _, k := range sortedKeys(b) {
			if av, ok := a[k]; ok {
				diffShape(join(path, k), b[k], av, breaking, added)
			} else {
				*breaking = append(*breaking, fmt.Sprintf("field hilang: %s (%s)", join(path, k), kindOf(b[k])))
			}
		}
		for _, k := range sortedKeys(a) {
			if _, ok := b[k]; !ok {
				*added = append(*added, fmt.Sprintf("field baru: %s (%s)", join(path, k), kindOf(a[k])))
			}
		}
	case []any:
		a, ok := after.([]any)
		if !ok {
			*breaking = append(*breaking, fmt.Sprintf("tipe berubah di %s: array -> %s", where, kindOf(after)))
			return
		}
		if len(b) > 0 && len(a) > 0 { // array kosong: bentuk elemen tidak diketahui
			diffShape(path+"[]", b[0], a[0], breaking, added)
		}
	case string:
		if as, ok := after.(string); !ok || as != b {
			*breaking = append(*breaking, fmt.Sprintf("tipe berubah di %s: %s -> %s", where, b, kindOf(after)))
		}
	}
}

func kindOf(v any) string {
	switch x := v.(type) {
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case string:
		return x
	default:
		return "?"
	}
}

func join(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// CompareHash: "ya" / "tidak" / "tidak diketahui".
func CompareHash(before, after string) string {
	switch {
	case before == "" || after == "":
		return "tidak diketahui"
	case strings.EqualFold(before, after):
		return "tidak"
	default:
		return "ya"
	}
}
