package storetest

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"

	"redline/internal/report"
	"redline/internal/store"
	"redline/internal/triage"
)

// AnalysisBackend menambah bagian yang dipakai analisis: fakta, hasil analisis, dan label manual.
type AnalysisBackend interface {
	Backend
	Facts(ctx context.Context, fingerprint string) (*store.Facts, error)
	SaveAnalysis(ctx context.Context, a store.Analysis) error
	GetAnalysis(ctx context.Context, fingerprint, promptVersion string) (store.Analysis, error)
	SetLabel(ctx context.Context, fingerprint, label, note, by string) error
}

// AnalysisFactory membuat backend kosong (semua source mengubah status).
type AnalysisFactory func(t *testing.T) AnalysisBackend

// RunAnalysis menjalankan test fakta, hasil analisis, dan label terhadap satu implementasi.
func RunAnalysis(t *testing.T, newBackend AnalysisFactory) {
	t.Run("FactsChangeSignals", func(t *testing.T) { testFactsSignals(t, newBackend(t)) })
	t.Run("FactsCodeMapAndSources", func(t *testing.T) { testFactsCodeMap(t, newBackend(t)) })
	t.Run("Analyses", func(t *testing.T) { testAnalyses(t, newBackend(t)) })
	t.Run("Labels", func(t *testing.T) { testLabels(t, newBackend(t)) })
}

// withHashes memberi sidik jari kode test; judul di changed mendapat sidik jari yang berbeda.
func withHashes(rep *report.Report, changed ...string) *report.Report {
	isChanged := map[string]bool{}
	for _, c := range changed {
		isChanged[c] = true
	}
	rep.TestHashes = map[string]string{}
	for _, o := range rep.Outcomes() {
		h := "h-" + o.Title
		if isChanged[o.Title] {
			h += "-diubah"
		}
		rep.TestHashes[fmt.Sprintf("%s:%d", o.File, o.Line)] = h
	}
	return rep
}

// lulus lalu gagal: run2-fixed (semua lulus) lalu run1-bug. Mengembalikan id kelompok per judul test.
func passThenFail(t *testing.T, b Backend, changed ...string) (map[string]string, *store.IngestResult, *store.IngestResult) {
	t.Helper()
	pass := ingest(t, b, withHashes(loadReport(t, "run2-fixed.json")), store.RunMeta{CommitSHA: "c1", AppVersion: "v1"})
	fail := ingest(t, b, withHashes(loadReport(t, "run1-bug.json"), changed...), store.RunMeta{CommitSHA: "c1", AppVersion: "v2"})
	fps := map[string]string{}
	for _, g := range fail.New {
		fps[g.TestKey[strings.LastIndex(g.TestKey, "› ")+len("› "):]] = g.Fingerprint
	}
	return fps, pass, fail
}

func testFactsSignals(t *testing.T, b AnalysisBackend) {
	ctx := context.Background()
	fps, pass, fail := passThenFail(t, b, "order dibayar")

	f, err := b.Facts(ctx, fps["brands punya slug"])
	if err != nil {
		t.Fatal(err)
	}
	if f.LastPass == nil || f.LastPass.RunID != pass.RunID || f.LastPass.CommitSHA != "c1" || f.LastPass.AppVersion != "v1" ||
		f.LastPass.At.IsZero() {
		t.Fatalf("pembanding run lulus terakhir salah: %+v", f.LastPass)
	}
	if f.FirstFail.RunID != fail.RunID || f.FirstFail.AppVersion != "v2" || f.FailRunID != fail.RunID {
		t.Errorf("gagal pertama/terakhir salah: first=%+v fail=%d", f.FirstFail, f.FailRunID)
	}
	if f.TestChanged != "tidak" || f.Response.Verdict != triage.ResponseChanged ||
		!strings.Contains(strings.Join(f.Response.Lines, "\n"), "GET /brands: field hilang: [].slug (string)") {
		t.Errorf("sinyal perubahan salah: test=%s response=%+v", f.TestChanged, f.Response)
	}
	if len(f.CurrentCalls) == 0 || f.CurrentCalls[0].Method != "GET" || f.CurrentCalls[0].Path != "/brands" {
		t.Errorf("response saat gagal salah: %+v", f.CurrentCalls)
	}
	if strings.Join(f.RecentStatuses, ",") != "passed,failed" || f.FlakyRecent != 0 {
		t.Errorf("riwayat salah: %v flaky=%d", f.RecentStatuses, f.FlakyRecent)
	}
	if f.ErrorMessage == "" || f.Group.Fingerprint != fps["brands punya slug"] || f.Group.SampleError == "" {
		t.Errorf("pesan error dan kelompok harus terisi: %+v", f.Group)
	}

	order, err := b.Facts(ctx, fps["order dibayar"])
	if err != nil {
		t.Fatal(err)
	}
	if order.TestChanged != "ya" {
		t.Errorf("kode test order diubah, mau ya, dapat %s", order.TestChanged)
	}
	if _, err := b.Facts(ctx, "tidakada"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("mau ErrNotFound, dapat %v", err)
	}
}

func passedWithCalls(title, calls string) report.Spec {
	spec := passedTest(title)
	spec.Tests[0].Results[0].Attachments = []report.Attachment{{
		Name: "redline-http", ContentType: "application/json",
		Body: base64.StdEncoding.EncodeToString([]byte(calls)),
	}}
	return spec
}

func testFactsCodeMap(t *testing.T, b AnalysisBackend) {
	ctx := context.Background()
	brands := `[{"method":"GET","path":"/brands","status":200,"shape":[{"id":"number"}]}]`
	brandsBroken := `[{"method":"GET","path":"/brands","status":500,"shape":{"message":"string"}}]`
	cats := `[{"method":"GET","path":"/categories","status":200,"shape":[]}]`
	msg := "expect(received).toBe(expected)\nExpected: 200\nReceived: 500"

	rep := shopReport(
		failedTest("brand target", msg, brandsBroken),
		passedWithCalls("brand lain lulus", brands),
		passedWithCalls("brand lain lulus 2", brands),
		failedTest("brand lain gagal", msg, brandsBroken),
		passedWithCalls("kategori", cats),
	)
	for i := range rep.Suites[0].Specs {
		rep.Suites[0].Specs[i].Line = 10 * (i + 1) // TestFiles memakai kunci file:baris, jadi harus beda per test
	}
	rep.TestFiles, rep.Sources = map[string][]string{}, map[string]string{
		"tests/brand.schema.ts": "export const token = \"Bearer abc.def.ghi\";\nexport const Brand = z.object({ id: z.number() });",
		"shop.spec.ts":          "test(\"brand target\", async () => {\n  expect(1).toBe(1);\n});",
	}
	for _, o := range rep.Outcomes() {
		files := []string{o.File}
		if strings.HasPrefix(o.Title, "brand") {
			files = append(files, "tests/brand.schema.ts")
		}
		rep.TestFiles[fmt.Sprintf("%s:%d", o.File, o.Line)] = files
	}
	ingest(t, b, rep, store.RunMeta{})

	f, err := b.Facts(ctx, fingerprint("brand target", msg))
	if err != nil {
		t.Fatal(err)
	}
	links := map[string]store.CodeLink{}
	for _, l := range f.CodeMap {
		links[l.Kind+" "+l.Name] = l
	}
	if l := links["endpoint GET /brands"]; l.Passed != 2 || l.Failed != 1 {
		t.Errorf("peta kode endpoint salah: %+v", f.CodeMap)
	}
	if l := links["file tests/brand.schema.ts"]; l.Passed != 2 || l.Failed != 1 {
		t.Errorf("peta kode file salah: %+v", f.CodeMap)
	}
	if l := links["file shop.spec.ts"]; l.Passed != 3 || l.Failed != 1 {
		t.Errorf("peta kode file spec salah: %+v", f.CodeMap)
	}
	if _, ok := links["endpoint GET /categories"]; ok {
		t.Errorf("endpoint yang tidak dipanggil test ini tidak boleh ikut: %+v", f.CodeMap)
	}
	if strings.Join(f.CodeFiles, ",") != "shop.spec.ts,tests/brand.schema.ts" {
		t.Errorf("file yang dipakai test salah: %v", f.CodeFiles)
	}
	if len(f.Sources) != 2 || f.Sources[0].Path != "shop.spec.ts" || f.Sources[1].Path != "tests/brand.schema.ts" {
		t.Fatalf("kode test harus urut spec dulu, lalu import: %+v", f.Sources)
	}
	if strings.Contains(f.Sources[1].Content, "abc.def") {
		t.Errorf("token di kode test harus disamarkan: %s", f.Sources[1].Content)
	}

	// Test yang lulus tidak menyimpan rujukan kode.
	lulus := shopReport(passedWithCalls("brand target", brands))
	lulus.Sources = rep.Sources
	ingest(t, b, lulus, store.RunMeta{})
	if f2, err := b.Facts(ctx, fingerprint("brand target", msg)); err != nil || len(f2.Sources) != 2 {
		t.Errorf("fakta tetap memakai kemunculan gagal terakhir: %+v %v", f2, err)
	}
}

func testAnalyses(t *testing.T, b AnalysisBackend) {
	ctx := context.Background()
	fps, _, _ := passThenFail(t, b)
	fp := fps["order dibayar"]

	if _, err := b.GetAnalysis(ctx, fp, "v1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("belum ada analisis: mau ErrNotFound, dapat %v", err)
	}
	a := store.Analysis{Fingerprint: fp, PromptVersion: "v1", Source: "ai", Model: "claude-haiku-5-5",
		Category: "backend_bug", Confidence: "high", Summary: "Status berubah.", Evidence: []string{"Expected: \"PAID\""},
		NextStep: "Laporkan.", CostUSD: 0.0012}
	if err := b.SaveAnalysis(ctx, a); err != nil {
		t.Fatal(err)
	}
	got, err := b.GetAnalysis(ctx, fp, "v1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != "ai" || got.Model != a.Model || got.Category != a.Category || got.Confidence != "high" ||
		got.Summary != a.Summary || len(got.Evidence) != 1 || got.NextStep != a.NextStep ||
		got.CostUSD < 0.00119 || got.CostUSD > 0.00121 || got.CreatedAt.IsZero() || got.Fingerprint != fp || got.PromptVersion != "v1" {
		t.Errorf("analisis tersimpan salah: %+v", got)
	}

	// Versi prompt yang sama ditimpa; versi lain terpisah.
	a.Category, a.Evidence = "test_bug", nil
	if err := b.SaveAnalysis(ctx, a); err != nil {
		t.Fatal(err)
	}
	if got, _ = b.GetAnalysis(ctx, fp, "v1"); got.Category != "test_bug" || got.Evidence == nil || len(got.Evidence) != 0 {
		t.Errorf("analisis versi sama harus ditimpa, evidence kosong bukan nil: %+v", got)
	}
	if _, err := b.GetAnalysis(ctx, fp, "v2"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("versi prompt lain belum ada: %v", err)
	}
}

func testLabels(t *testing.T, b AnalysisBackend) {
	ctx := context.Background()
	fps, _, _ := passThenFail(t, b)
	fp := fps["register user"]

	if err := b.SetLabel(ctx, fp, "backend_bug", "500 saat register", "ega"); err != nil {
		t.Fatal(err)
	}
	g, _ := group(t, b, fp)
	if g.HumanLabel != "backend_bug" || g.HumanNote != "500 saat register" || g.LabeledBy != "ega" {
		t.Errorf("label tidak tersimpan: %+v", g)
	}
	if f, _ := b.Facts(ctx, fp); f.Group.HumanLabel != "backend_bug" {
		t.Errorf("fakta harus membawa label manual: %+v", f.Group)
	}

	if err := b.SetLabel(ctx, fp, "", "diabaikan", "diabaikan"); err != nil {
		t.Fatal(err)
	}
	if g, _ = group(t, b, fp); g.HumanLabel != "" || g.HumanNote != "" || g.LabeledBy != "" {
		t.Errorf("label kosong harus menghapus label, catatan, dan pemberinya: %+v", g)
	}
	if err := b.SetLabel(ctx, "tidakada", "test_bug", "", "ega"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("kelompok tidak ada: mau ErrNotFound, dapat %v", err)
	}
}
