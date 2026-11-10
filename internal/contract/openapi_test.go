package contract

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"redline/internal/report"
)

func load(t *testing.T) *Document {
	t.Helper()
	data, err := os.ReadFile("testdata/toolshop-sprint1.json")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestOperation(t *testing.T) {
	doc := load(t)
	for path, want := range map[string]string{
		"/brands/:id":      "/brands/{id}",
		"/brands":          "/brands",
		"/categories/tree": "/categories/tree", // lebih spesifik dari /categories/{id}
		"/categories/:id":  "/categories/{id}",
		"/products/:id":    "/products/{id}",
		"/brand/:id":       "",
		"/api/v2/whatever": "",
	} {
		if got := doc.match(path); got != want {
			t.Errorf("match(%q) = %q, mau %q", path, got, want)
		}
	}
	post := doc.Operation("POST", "/brands")
	for _, want := range []string{"POST /brands (contract: /brands)", "201", "id: number(integer)", "request body:"} {
		if !strings.Contains(post, want) {
			t.Errorf("kontrak POST /brands harus memuat %q:\n%s", want, post)
		}
	}
	if got := doc.Operation("PATCH", "/brands"); !strings.Contains(got, "this method is not in the contract") {
		t.Errorf("method yang tidak ada harus disebut: %q", got)
	}
}

func TestFetchAndDescribe(t *testing.T) {
	data, _ := os.ReadFile("testdata/toolshop-sprint1.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(data) }))
	defer srv.Close()

	r := NewRegistry(map[string]string{"toolshop": srv.URL, "bench": srv.URL})
	url, doc, err := r.Fetch(context.Background(), "toolshop")
	if err != nil || url != srv.URL || len(doc) == 0 {
		t.Fatalf("Fetch: %q %d %v", url, len(doc), err)
	}
	if url, _, err := r.Fetch(context.Background(), "gorest"); url != "" || err != nil {
		t.Errorf("project tanpa kontrak harus kosong tanpa error: %q %v", url, err)
	}
	d, err := ParseCached("v1", doc)
	if err != nil {
		t.Fatal(err)
	}
	calls := []report.HTTPCall{{Method: "GET", Path: "/brands/:id"}, {Method: "GET", Path: "/brands/:id"}, {Method: "GET", Path: "/brand/:id"}}
	text := d.Describe(calls)
	if strings.Count(text, "GET /brands/:id") != 1 || !strings.Contains(text, "GET /brand/:id: not in the contract") {
		t.Errorf("ringkasan salah:\n%s", text)
	}
}

func TestViolations(t *testing.T) {
	doc := load(t)
	call := func(method, path string, status int, shape string) []string {
		return doc.Violations([]report.HTTPCall{{Method: method, Path: path, Status: status, Shape: json.RawMessage(shape)}})
	}
	if v := call("GET", "/brands/:id", 200, `{"id":"number","name":"string","slug":"string","extra":"string"}`); len(v) != 0 {
		t.Errorf("response sesuai kontrak (field tambahan boleh): %v", v)
	}
	cases := map[string][]string{
		"field slug is in the contract but missing from the response": call("GET", "/brands/:id", 200, `{"id":"number","name":"string"}`),
		"id has type string, contract: integer":                       call("GET", "/brands/:id", 200, `{"id":"string","name":"string","slug":"string"}`),
		"status 200 is not in the contract":                           call("POST", "/brands", 200, `{"id":"number","name":"string","slug":"string"}`),
		"field [].sub_categories is in the contract":                  call("GET", "/categories/tree", 200, `[{"id":"number","name":"string","slug":"string","parent_id":"null"}]`),
		"price has type string, contract: number":                     call("GET", "/products/:id", 200, `{"id":"number","name":"string","price":"string"}`),
	}
	for want, got := range cases {
		if !strings.Contains(strings.Join(got, " | "), want) {
			t.Errorf("mau pelanggaran %q, dapat %v", want, got)
		}
	}
	// parent_id null diizinkan (nullable); endpoint tak terdokumentasi tidak dinilai di sini.
	if v := call("GET", "/categories", 200, `[{"id":"number","name":"string","slug":"string","parent_id":"null|number"}]`); len(v) != 0 {
		t.Errorf("nullable harus diterima: %v", v)
	}
	if v := call("GET", "/brand/:id", 404, `{"message":"string"}`); len(v) != 0 {
		t.Errorf("endpoint tak terdokumentasi tidak dinilai: %v", v)
	}
}
