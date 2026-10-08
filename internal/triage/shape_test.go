package triage

import (
	"encoding/json"
	"strings"
	"testing"

	"redline/internal/report"
)

func call(method, path string, status int, shape string) report.HTTPCall {
	return report.HTTPCall{Method: method, Path: path, Status: status, Shape: json.RawMessage(shape)}
}

func TestDiffCalls(t *testing.T) {
	brands := `[{"id":"string","name":"string","slug":"string"}]`
	cases := []struct {
		name    string
		before  []report.HTTPCall
		after   []report.HTTPCall
		verdict string
		want    []string
	}{
		{"field hilang", []report.HTTPCall{call("GET", "/brands", 200, brands)},
			[]report.HTTPCall{call("GET", "/brands", 200, `[{"id":"string","name":"string"}]`)},
			ResponseChanged, []string{"GET /brands: field hilang: [].slug (string)"}},
		{"field baru saja", []report.HTTPCall{call("GET", "/brands", 200, brands)},
			[]report.HTTPCall{call("GET", "/brands", 200, `[{"id":"string","name":"string","slug":"string","logo":"null|string"}]`)},
			ResponseAddedOnly, []string{"GET /brands: field baru: [].logo (null|string)"}},
		{"tipe berubah", []report.HTTPCall{call("GET", "/orders", 200, `{"total":"number","items":[]}`)},
			[]report.HTTPCall{call("GET", "/orders", 200, `{"total":"string","items":[{"sku":"string"}]}`)},
			ResponseChanged, []string{"GET /orders: tipe berubah di total: number -> string"}},
		{"status berubah", []report.HTTPCall{call("POST", "/users/register", 201, `{"id":"string"}`)},
			[]report.HTTPCall{call("POST", "/users/register", 500, `{"message":"string"}`)},
			ResponseChanged, []string{"POST /users/register: status 201 -> 500"}},
		{"sama", []report.HTTPCall{call("GET", "/orders", 200, `{"status":"string"}`)},
			[]report.HTTPCall{call("GET", "/orders", 200, `{"status":"string"}`)},
			ResponseSame, nil},
		{"objek jadi null", []report.HTTPCall{call("GET", "/me", 200, `{"profile":{"name":"string"}}`)},
			[]report.HTTPCall{call("GET", "/me", 200, `{"profile":"null"}`)},
			ResponseChanged, []string{"GET /me: tipe berubah di profile: object -> null"}},
		{"tanpa pembanding", nil, []report.HTTPCall{call("GET", "/brands", 200, brands)}, ResponseUnknown, nil},
		{"endpoint lain", []report.HTTPCall{call("GET", "/brands", 200, brands)},
			[]report.HTTPCall{call("GET", "/categories", 200, `[]`)},
			ResponseUnknown, []string{"GET /categories: endpoint ini tidak dipanggil saat terakhir lulus"}},
	}
	for _, c := range cases {
		got := DiffCalls(c.before, c.after)
		if got.Verdict != c.verdict {
			t.Errorf("%s: verdict %q, mau %q (%v)", c.name, got.Verdict, c.verdict, got.Lines)
		}
		if strings.Join(got.Lines, "\n") != strings.Join(c.want, "\n") {
			t.Errorf("%s: lines\n got  %q\n want %q", c.name, got.Lines, c.want)
		}
	}
}

func TestCompareHash(t *testing.T) {
	if CompareHash("a", "a") != "tidak" || CompareHash("a", "b") != "ya" || CompareHash("", "b") != "tidak diketahui" {
		t.Error("CompareHash salah")
	}
}
