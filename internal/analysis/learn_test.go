package analysis

import (
	"strings"
	"testing"

	"redline/internal/store"
	"redline/internal/triage"
)

func TestBacktest(t *testing.T) {
	cases := []store.Case{
		{GroupID: "a", Category: "test_bug", Error: "TypeError: Cannot read properties of undefined (reading 'id')"},
		{GroupID: "b", Category: "test_bug", Error: "TypeError: Cannot read properties of undefined (reading 'name')"},
		{GroupID: "c", Category: "backend_bug", Error: "Expected: 201\nReceived: 422"},
	}
	all := []store.GroupError{{GroupID: "a", Error: cases[0].Error}, {GroupID: "b", Error: cases[1].Error},
		{GroupID: "c", Error: cases[2].Error}, {GroupID: "d", Error: "TypeError: Cannot read properties of undefined (reading 'data')"}}

	rule, reason := Backtest(ruleCandidate{Pattern: `Cannot read properties of undefined`, Category: "test_bug", Summary: "x"}, cases, all)
	if reason != "" || rule.Hits != 2 || rule.AlsoMatches != 1 || strings.Join(rule.LearnedFrom, ",") != "a,b" {
		t.Fatalf("mau lolos dengan 2 kasus + 1 kelompok lain, dapat %+v (%s)", rule, reason)
	}

	rejected := map[string]ruleCandidate{
		"cocok dengan kasus c": {Pattern: `(?i)error|received`, Category: "test_bug", Summary: "x"},
		"tidak cocok":          {Pattern: `socket hang up`, Category: "environment", Summary: "x"},
		"teks kosong":          {Pattern: `(TypeError)?`, Category: "test_bug", Summary: "x"},
		"regex tidak valid":    {Pattern: `(?=lookahead)abc`, Category: "test_bug", Summary: "x"},
		"bukan unknown":        {Pattern: `Cannot read properties`, Category: "unknown", Summary: "x"},
		"summary kosong":       {Pattern: `Cannot read properties`, Category: "test_bug"},
		"pola terlalu pendek":  {Pattern: `Type`, Category: "test_bug", Summary: "x"},
	}
	for want, c := range rejected {
		if _, reason := Backtest(c, cases, all); !strings.Contains(reason, want) {
			t.Errorf("%q: alasan %q, mau mengandung %q", c.Pattern, reason, want)
		}
	}
}

func TestBacktest_TooGeneric(t *testing.T) {
	cases := []store.Case{{GroupID: "a", Category: "test_bug", Error: "Error: boom"}}
	all := []store.GroupError{}
	for _, id := range []string{"a", "b", "c", "d", "e", "f"} {
		all = append(all, store.GroupError{GroupID: id, Error: "Error: boom " + id})
	}
	if _, reason := Backtest(ruleCandidate{Pattern: `Error: boom`, Category: "test_bug", Summary: "x"}, cases, all); !strings.Contains(reason, "terlalu umum") {
		t.Fatalf("mau ditolak karena terlalu umum, dapat %q", reason)
	}
}

func TestApplyRules_Learned(t *testing.T) {
	learned := []store.LearnedRule{{ID: 4, Pattern: `Cannot read properties of undefined`, Category: "test_bug",
		Summary: "Objek belum ada saat diakses.", LearnedFrom: []string{"a", "b"}}}
	got := ApplyRules(&store.Facts{ErrorMessage: "TypeError: Cannot read properties of undefined (reading 'id')\n at x"}, learned...)
	if got == nil || got.Category != "test_bug" || got.Confidence != "high" || !strings.Contains(got.Evidence[1], "Aturan #4") {
		t.Fatalf("aturan dipelajari tidak dipakai: %+v", got)
	}
	// Aturan bawaan tetap didahulukan.
	got = ApplyRules(&store.Facts{ErrorMessage: "connect ECONNREFUSED 127.0.0.1:8091 Cannot read properties of undefined"}, learned...)
	if got == nil || got.Category != "environment" {
		t.Fatalf("aturan bawaan harus didahulukan: %+v", got)
	}
	// Pola rusak dilewati, bukan panic.
	if got := ApplyRules(&store.Facts{ErrorMessage: "abc"}, store.LearnedRule{Pattern: `(`}); got != nil {
		t.Fatalf("pola rusak harus dilewati: %+v", got)
	}
}

func TestApplyRules_LearnedDoesNotBeatAPIChange(t *testing.T) {
	learned := []store.LearnedRule{{ID: 1, Pattern: `Cannot read properties of undefined`, Category: "test_bug", Summary: "x"}}
	f := &store.Facts{
		ErrorMessage: "TypeError: Cannot read properties of undefined (reading 'length')",
		LastPass:     &store.RunRef{RunID: 3},
		TestChanged:  "tidak",
		Response:     triage.ResponseDiff{Verdict: triage.ResponseChanged, Lines: []string{"GET /brands: field hilang: data"}},
	}
	if got := ApplyRules(f, learned...); got == nil || got.Category != "backend_bug" {
		t.Fatalf("test tetap + API berubah harus menang atas aturan yang dipelajari: %+v", got)
	}
	f.TestChanged, f.Response.Verdict = "ya", triage.ResponseSame
	if got := ApplyRules(f, learned...); got == nil || got.Confidence != "high" || got.Category != "test_bug" {
		t.Fatalf("aturan yang dipelajari (high) didahulukan dari matriks test_bug (medium): %+v", got)
	}
}

func TestApplyRules_Contract(t *testing.T) {
	changed := triage.ResponseDiff{Verdict: triage.ResponseChanged, Lines: []string{"GET /brands: field hilang: [].slug (string)"}}
	base := func() *store.Facts {
		return &store.Facts{ErrorMessage: "ZodError", LastPass: &store.RunRef{RunID: 3}, TestChanged: "tidak", Response: changed}
	}

	// Kontrak ikut berubah dan response sesuai kontrak baru: perubahan disengaja, test usang.
	f := base()
	f.ContractChanged = "ya"
	if got := ApplyRules(f); got == nil || got.Category != "test_bug" || got.Confidence != "high" {
		t.Errorf("kontrak berubah + response sesuai: mau test_bug (test usang), dapat %+v", got)
	}
	// Kontrak berubah tapi response tidak mengikutinya: tetap salah API.
	f.ContractViolations = []string{"GET /brands: field [].handle ada di kontrak tapi tidak ada di response"}
	if got := ApplyRules(f); got == nil || got.Category != "backend_bug" {
		t.Errorf("kontrak berubah + response melanggar: mau backend_bug, dapat %+v", got)
	}
	// Kontrak tetap, response berubah: regresi.
	f = base()
	f.ContractChanged = "tidak"
	if got := ApplyRules(f); got == nil || got.Category != "backend_bug" || got.Confidence != "high" {
		t.Errorf("kontrak tetap + response berubah: mau backend_bug, dapat %+v", got)
	}

	// Test baru (tanpa riwayat) dan response melanggar kontrak: salah API, tanpa AI.
	cold := &store.Facts{ErrorMessage: "ZodError", TestChanged: "tidak diketahui",
		ContractViolations: []string{"POST /brands: status 200 tidak ada di kontrak (yang terdokumentasi: 201, 422)"}}
	if got := ApplyRules(cold); got == nil || got.Category != "backend_bug" || !strings.Contains(got.Summary, "status 200") {
		t.Errorf("pelanggaran kontrak: mau backend_bug, dapat %+v", got)
	}
	// Kode test sedang berubah: pelanggaran kontrak tidak diputuskan aturan.
	cold.TestChanged = "ya"
	if got := ApplyRules(cold); got != nil && got.Category == "backend_bug" {
		t.Errorf("test yang berubah tidak boleh langsung menyalahkan API: %+v", got)
	}
}
