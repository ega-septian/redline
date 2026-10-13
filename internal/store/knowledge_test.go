package store_test

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"redline/internal/report"
	"redline/internal/store"
	"redline/internal/triage"
)

// Dunia yang sama dengan skenario benchmark kag:
//
//	brands.spec.ts   ─┐
//	products.spec.ts ─┴→ helpers/brands.ts → schemas/brand.ts
//	direct.spec.ts   → GET /brands/1 tanpa helper
type kagTest struct {
	file, title string
	line        int
	helper      bool
	calls       []string
}

var kagTests = []kagTest{
	{"brands.spec.ts", "list", 6, true, []string{"GET /brands"}},
	{"brands.spec.ts", "get", 11, true, []string{"GET /brands/:id"}},
	{"products.spec.ts", "match", 12, true, []string{"GET /products/:id", "GET /brands/:id"}},
	{"products.spec.ts", "detail", 20, true, []string{"GET /products/:id"}},
	{"direct.spec.ts", "direct", 5, false, []string{"GET /brands/:id"}},
}

func kagKey(title string) string {
	for _, k := range kagTests {
		if k.title == title {
			return "api › " + k.file + " › " + title
		}
	}
	panic(title)
}

// kagRun meng-ingest satu run. failed: judul -> pesan error. calls: judul -> endpoint pengganti (misalnya helper
// salah ketik path).
func kagRun(t *testing.T, s *store.Store, schemaHash, helperHash string, failed map[string]string,
	calls map[string][]string) *store.IngestResult {
	t.Helper()
	rep := &report.Report{TestFiles: map[string][]string{}}
	suites := map[string]*report.Suite{}
	for _, k := range kagTests {
		endpoints := k.calls
		if c, ok := calls[k.title]; ok {
			endpoints = c
		}
		var js []string
		for _, e := range endpoints {
			method, path, _ := strings.Cut(e, " ")
			js = append(js, fmt.Sprintf(`{"method":%q,"path":%q,"status":200,"shape":{}}`, method, path))
		}
		res := report.Result{Status: "passed", Attachments: []report.Attachment{{Name: "redline-http",
			ContentType: "application/json", Body: base64.StdEncoding.EncodeToString([]byte("[" + strings.Join(js, ",") + "]"))}}}
		status := "expected"
		if msg, ok := failed[k.title]; ok {
			res.Status, res.Error, status = "failed", &report.ErrorEntry{Message: msg}, "unexpected"
		}
		if suites[k.file] == nil {
			suites[k.file] = &report.Suite{Title: k.file, File: k.file}
		}
		suites[k.file].Specs = append(suites[k.file].Specs, report.Spec{Title: k.title, File: k.file, Line: k.line,
			Tests: []report.Test{{ProjectName: "api", Status: status, Results: []report.Result{res}}}})
		files := []string{k.file}
		if k.helper {
			files = append(files, "helpers/brands.ts")
		}
		rep.TestFiles[fmt.Sprintf("%s:%d", k.file, k.line)] = files
	}
	for _, f := range []string{"brands.spec.ts", "products.spec.ts", "direct.spec.ts"} {
		rep.Suites = append(rep.Suites, *suites[f])
	}
	rep.Files = map[string]report.FileNode{
		"brands.spec.ts":    {Hash: "b1", Imports: []string{"helpers/brands.ts"}},
		"products.spec.ts":  {Hash: "p1", Imports: []string{"helpers/brands.ts"}},
		"direct.spec.ts":    {Hash: "d1"},
		"helpers/brands.ts": {Hash: helperHash, Imports: []string{"schemas/brand.ts"}},
		"schemas/brand.ts":  {Hash: schemaHash},
	}
	return ingest(t, s, rep, store.RunMeta{})
}

func kagFingerprint(title, msg string) string {
	return triage.Fingerprint(kagKey(title), triage.Normalize(triage.Clean(msg)))
}

func split(passed, failed []string) store.TestSplit {
	keys := func(titles []string) []string {
		out := []string{}
		for _, t := range titles {
			out = append(out, kagKey(t))
		}
		return out
	}
	return store.TestSplit{Passed: keys(passed), Failed: keys(failed)}
}

func TestKnowledge(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	none := []string{}
	base := kagRun(t, s, "schema1", "helper1", nil, nil) // semua lulus: riwayat

	t.Run("SchemaTwoImportsDeepChanged", func(t *testing.T) {
		msg := "ZodError: id expected string, received number"
		fail := kagRun(t, s, "schema2", "helper1", map[string]string{"list": msg, "get": msg, "match": msg}, nil)
		k, err := s.Knowledge(ctx, kagFingerprint("get", msg))
		if err != nil {
			t.Fatal(err)
		}
		if k.RunID != fail.RunID || k.PassRunID != base.RunID {
			t.Errorf("run gagal dan run lulus terakhir: %+v", k)
		}
		want := []store.FileFact{
			{Path: "brands.spec.ts", Direct: true, Others: split(none, none)},
			{Path: "helpers/brands.ts", Direct: true, Others: split(none, none)},
			{Path: "schemas/brand.ts", Direct: false, Changed: true, Others: split([]string{"detail"}, []string{"list", "match"})},
		}
		if !reflect.DeepEqual(k.Files, want) {
			t.Errorf("file yang dipakai:\n got %+v\nwant %+v", k.Files, want)
		}
		want2 := []store.EndpointFact{{Key: "GET /brands/:id", SeenPassing: true,
			WithChanged: split(none, []string{"match"}), Without: split([]string{"direct"}, none)}}
		if !reflect.DeepEqual(k.Endpoints, want2) {
			t.Errorf("endpoint:\n got %+v\nwant %+v", k.Endpoints, want2)
		}
	})

	t.Run("EndpointBrokenCodeUnchanged", func(t *testing.T) {
		msg := "Expected: \"new-brand\"\nReceived: \"brand-name-2\""
		kagRun(t, s, "schema1", "helper1", map[string]string{"get": msg, "match": msg, "direct": msg}, nil)
		k, err := s.Knowledge(ctx, kagFingerprint("match", msg))
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range k.Files {
			if f.Changed {
				t.Errorf("kode tidak berubah sejak lulus terakhir: %+v", f)
			}
		}
		want := []store.EndpointFact{
			{Key: "GET /brands/:id", SeenPassing: true, WithChanged: split(none, none), Without: split(none, []string{"get", "direct"})},
			{Key: "GET /products/:id", SeenPassing: true, WithChanged: split(none, none), Without: split([]string{"detail"}, none)},
		}
		if !reflect.DeepEqual(k.Endpoints, want) {
			t.Errorf("endpoint:\n got %+v\nwant %+v", k.Endpoints, want)
		}
	})

	t.Run("HelperPathTypo", func(t *testing.T) {
		msg := "Expected: 200\nReceived: 404"
		typo := map[string][]string{"get": {"GET /brand/:id"}, "match": {"GET /products/:id", "GET /brand/:id"}}
		kagRun(t, s, "schema1", "helper2", map[string]string{"get": msg, "match": msg}, typo)
		k, err := s.Knowledge(ctx, kagFingerprint("get", msg))
		if err != nil {
			t.Fatal(err)
		}
		changed := []string{}
		for _, f := range k.Files {
			if f.Changed {
				changed = append(changed, f.Path)
			}
		}
		if strings.Join(changed, ",") != "helpers/brands.ts" {
			t.Errorf("hanya helper yang berubah: %v", changed)
		}
		want := []store.EndpointFact{{Key: "GET /brand/:id", SeenPassing: false,
			WithChanged: split(none, []string{"match"}), Without: split(none, none)}}
		if !reflect.DeepEqual(k.Endpoints, want) {
			t.Errorf("endpoint baru yang hanya dipanggil lewat helper yang berubah:\n got %+v\nwant %+v", k.Endpoints, want)
		}
	})

	t.Run("Impact", func(t *testing.T) {
		keys := func(tests []store.ImpactedTest) []string {
			out := []string{}
			for _, it := range tests {
				out = append(out, strings.TrimPrefix(it.TestKey, "api › "))
			}
			return out
		}
		for _, c := range []struct {
			q    string
			file bool
			want string
		}{
			// products.spec.ts › detail ikut: spec-nya meng-import helper, jadi bergantung pada schema.
			{"schemas/brand.ts", true, "brands.spec.ts › get,brands.spec.ts › list,products.spec.ts › detail,products.spec.ts › match"},
			{"direct.spec.ts", true, "direct.spec.ts › direct"},
			// Disimpan sebagai /brands/:id. Run terakhir get dan match memanggil /brand/:id (salah ketik);
			// impact memakai run lulus terakhir.
			{"GET /brands/1", false, "brands.spec.ts › get,direct.spec.ts › direct,products.spec.ts › match"},
			{"/products/1", false, "products.spec.ts › detail,products.spec.ts › match"},
		} {
			var got []store.ImpactedTest
			var err error
			if c.file {
				got, err = s.ImpactOfFile(ctx, c.q)
			} else {
				got, err = s.ImpactOfEndpoint(ctx, c.q)
			}
			if err != nil || strings.Join(keys(got), ",") != c.want {
				t.Errorf("impact %s:\n got %v %v\nwant %s", c.q, keys(got), err, c.want)
			}
		}
		if got, _ := s.ImpactOfFile(ctx, "tidak-ada.ts"); len(got) != 0 {
			t.Errorf("file yang tidak dikenal: %+v", got)
		}
	})

	if _, err := s.Knowledge(ctx, "tidakada"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("mau ErrNotFound, dapat %v", err)
	}
}
