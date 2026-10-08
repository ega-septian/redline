package report

import (
	"os"
	"strings"
	"testing"
)

// Fixture dibuat dari run Playwright sungguhan (lihat README bagian "Fixture test"):
// run1-bug.json saat API bermasalah, run2-fixed.json setelah diperbaiki.
func load(t *testing.T, name string) *Report {
	t.Helper()
	f, err := os.Open("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rep, err := Parse(f)
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func byTitle(outs []Outcome) map[string]Outcome {
	m := map[string]Outcome{}
	for _, o := range outs {
		m[o.Title] = o
	}
	return m
}

func TestOutcomes_BuggyRun(t *testing.T) {
	rep := load(t, "run1-bug.json")
	outs := byTitle(rep.Outcomes())
	if len(outs) != 6 {
		t.Fatalf("mau 6 test, dapat %d", len(outs))
	}

	brand, ok := outs["Brand › brands punya slug"]
	if !ok {
		t.Fatalf("judul describe harus ikut, nama file tidak: %v", outs)
	}
	if brand.Status != StatusFailed || brand.Retries != 1 || brand.Project != "toolshop" || brand.File != "api.spec.ts" {
		t.Errorf("brand salah: %+v", brand)
	}
	if !strings.Contains(brand.ErrorMessage, "slug") || brand.TracePath == "" ||
		!strings.Contains(brand.ErrorLocation, "api.spec.ts:") || brand.ErrorSnippet == "" {
		t.Errorf("detail error brand tidak lengkap: %+v", brand)
	}
	if !strings.HasSuffix(brand.TracePath, "-retry1/trace.zip") {
		t.Errorf("failed harus memakai percobaan terakhir, trace = %s", brand.TracePath)
	}
	if brand.Key() != "toolshop › api.spec.ts › Brand › brands punya slug" {
		t.Errorf("key = %q", brand.Key())
	}

	flaky := outs["kadang lambat (flaky)"]
	if flaky.Status != StatusFlaky || !strings.Contains(flaky.ErrorMessage, "gagal di percobaan pertama") {
		t.Errorf("flaky harus membawa error percobaan pertama: %+v", flaky)
	}
	if outs["fitur wishlist belum ada"].Status != StatusSkipped {
		t.Errorf("test.skip harus skipped")
	}
	if outs["register user"].Status != StatusFailed {
		t.Errorf("register harus failed")
	}

	// Bentuk response dari fixture Redline: dari percobaan gagal, tanpa isi data.
	if len(brand.Calls) != 1 || brand.Calls[0].Method != "GET" || brand.Calls[0].Path != "/brands" || brand.Calls[0].Status != 200 {
		t.Fatalf("calls brand salah: %+v", brand.Calls)
	}
	if got := string(brand.Calls[0].Shape); got != `[{"id":"string","name":"string"}]` {
		t.Errorf("shape brand = %s", got)
	}
	if d := outs["Brand › detail brand"]; d.Status != StatusPassed || len(d.Calls) != 1 || d.Calls[0].Path != "/brands/:id" {
		t.Errorf("test lulus juga harus membawa calls (untuk baseline), path id dinormalisasi: %+v", d.Calls)
	}
	if r := outs["register user"]; len(r.Calls) != 1 || r.Calls[0].Status != 500 {
		t.Errorf("register: %+v", r.Calls)
	}
	if len(outs["kadang lambat (flaky)"].Calls) != 0 {
		t.Errorf("flaky tidak dipakai sebagai pembanding")
	}
}

func TestParseUpload(t *testing.T) {
	raw, err := os.ReadFile("testdata/run1-bug.json")
	if err != nil {
		t.Fatal(err)
	}
	// Format reporter Redline: results.json dibungkus bersama sidik jari kode test.
	wrapped := []byte(`{"playwright":` + string(raw) + `,"test_hashes":{"api.spec.ts:4":"aaaa","api.spec.ts:999":"tidakdipakai"}}`)
	rep, err := ParseUpload(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	outs := byTitle(rep.Outcomes())
	if outs["Brand › brands punya slug"].SourceHash != "aaaa" || outs["register user"].SourceHash != "" {
		t.Errorf("hash harus dicocokkan lewat file:line: %q %q", outs["Brand › brands punya slug"].SourceHash, outs["register user"].SourceHash)
	}

	// Format lama (curl results.json mentah) tetap diterima.
	rep, err = ParseUpload(raw)
	if err != nil || len(rep.Outcomes()) != 6 || rep.TestHashes != nil {
		t.Errorf("results.json mentah harus tetap bisa: %v", err)
	}
}

func TestOutcomes_FixedRun(t *testing.T) {
	outs := byTitle(load(t, "run2-fixed.json").Outcomes())
	for _, title := range []string{"Brand › brands punya slug", "register user", "order dibayar"} {
		o := outs[title]
		if o.Status != StatusPassed || o.ErrorMessage != "" || o.TracePath != "" {
			t.Errorf("%s harus passed tanpa error: %+v", title, o)
		}
	}
}

func TestParse_RejectsNonPlaywrightJSON(t *testing.T) {
	if _, err := Parse(strings.NewReader(`{"hello":"world"}`)); err == nil {
		t.Error("JSON yang bukan laporan Playwright harus ditolak")
	}
	if _, err := Parse(strings.NewReader(`<html>`)); err == nil {
		t.Error("bukan JSON harus ditolak")
	}
}
