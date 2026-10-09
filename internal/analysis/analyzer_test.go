package analysis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
	"testing"

	"redline/internal/llm"
	"redline/internal/report"
	"redline/internal/store"
	"redline/internal/triage"
)

// fakeLLM meniru Claude: mengembalikan verdict yang sudah ditentukan dan mencatat request.
type fakeLLM struct {
	calls   int
	lastReq llm.Request
	verdict verdict
}

func (f *fakeLLM) CreateMessage(_ context.Context, req llm.Request) (*llm.Response, error) {
	f.calls++
	f.lastReq = req
	input, _ := json.Marshal(f.verdict)
	return &llm.Response{
		Model:      "claude-haiku-5-5",
		StopReason: "tool_use",
		Content:    []llm.ContentBlock{{Type: "tool_use", Name: toolName, Input: input}},
		Usage:      llm.Usage{InputTokens: 1200, OutputTokens: 150},
	}, nil
}

// setup memasukkan run lulus lalu run gagal. changed berisi judul test yang kodenya diubah
// di antara kedua run (sidik jarinya dibuat berbeda); test lain sidik jarinya sama.
func setup(t *testing.T, changed ...string) (*store.Store, map[string]string) {
	t.Helper()
	url := os.Getenv("REDLINE_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("REDLINE_TEST_DATABASE_URL tidak diset; test database dilewati")
	}
	ctx := context.Background()
	s, err := store.Open(ctx, store.TestSchema(ctx, t, url, "analysis_test"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	isChanged := map[string]bool{}
	for _, c := range changed {
		isChanged[c] = true
	}
	withHashes := func(rep *report.Report, after bool) *report.Report {
		rep.TestHashes = map[string]string{}
		for _, o := range rep.Outcomes() {
			h := "h-" + o.Title
			if after && isChanged[o.Title] {
				h += "-diubah"
			}
			rep.TestHashes[fmt.Sprintf("%s:%d", o.File, o.Line)] = h
		}
		return rep
	}

	if _, err := s.IngestReport(ctx, withHashes(load(t, "run2-fixed.json"), false), store.RunMeta{CommitSHA: "c1", AppVersion: "v1"}); err != nil {
		t.Fatal(err)
	}
	res, err := s.IngestReport(ctx, withHashes(load(t, "run1-bug.json"), true), store.RunMeta{CommitSHA: "c1", AppVersion: "v2"})
	if err != nil {
		t.Fatal(err)
	}
	fps := map[string]string{}
	for _, g := range res.New {
		fps[g.TestKey[strings.LastIndex(g.TestKey, "› ")+len("› "):]] = g.Fingerprint
	}
	return s, fps
}

func load(t *testing.T, name string) *report.Report {
	t.Helper()
	f, err := os.Open("../report/testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rep, err := report.Parse(f)
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func TestFacts_ChangeSignals(t *testing.T) {
	s, fps := setup(t)
	f, err := s.Facts(context.Background(), fps["brands punya slug"])
	if err != nil {
		t.Fatal(err)
	}
	if f.LastPass == nil || f.TestChanged != "tidak" || f.Response.Verdict != triage.ResponseChanged {
		t.Fatalf("sinyal salah: lastPass=%v test=%s response=%+v", f.LastPass, f.TestChanged, f.Response)
	}
	text := BuildFacts(f)
	for _, want := range []string{
		"Kode test berubah: tidak",
		"Response API: berubah",
		"GET /brands: field hilang: [].slug (string)",
		"GET /brands -> 200 [{\"id\":\"string\",\"name\":\"string\"}]",
		"Versi aplikasi: v1 -> v2 (berubah: ya)",
		"Status test ini di run terakhir (lama ke baru): passed, failed",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("fakta harus memuat %q:\n%s", want, text)
		}
	}
}

func TestAnalyze_MatrixRules(t *testing.T) {
	// Kode test "order dibayar" diubah; test lain tetap.
	s, fps := setup(t, "order dibayar")
	ctx := context.Background()
	fake := &fakeLLM{}
	a := &Analyzer{Store: s, LLM: fake}

	brand, _, err := a.Analyze(ctx, fps["brands punya slug"], false)
	if err != nil {
		t.Fatal(err)
	}
	if brand.Source != "rule" || brand.Category != "backend_bug" || brand.Confidence != "high" ||
		!strings.Contains(brand.Summary, "field hilang: [].slug") {
		t.Errorf("test tetap + response berubah harus backend_bug dari aturan: %+v", brand)
	}

	order, _, err := a.Analyze(ctx, fps["order dibayar"], false)
	if err != nil {
		t.Fatal(err)
	}
	if order.Source != "rule" || order.Category != "test_bug" || order.Confidence != "medium" {
		t.Errorf("test berubah + bentuk response sama harus test_bug: %+v", order)
	}
	if fake.calls != 0 {
		t.Errorf("matriks yang jelas tidak boleh memanggil AI, calls=%d", fake.calls)
	}
}

func TestAnalyze_Flow(t *testing.T) {
	s, fps := setup(t)
	ctx := context.Background()
	// "order dibayar": kode test tetap, bentuk response sama (yang berubah nilainya) -> aturan tidak cukup, ke AI.
	fake := &fakeLLM{verdict: verdict{
		Category:   "backend_bug",
		Confidence: "high",
		Summary:    "Nilai status order berubah dari PAID menjadi PENDING padahal kode test tidak berubah.",
		Evidence:   []string{"Kode test berubah: tidak", "kolom status diubah di migration 2024_10_01"}, // yang kedua karangan
		NextStep:   "Laporkan ke backend.",
	}}
	a := &Analyzer{Store: s, LLM: fake, Model: "claude-haiku-5-5", Pricing: llm.Pricing{InputPerMTok: 0.10, OutputPerMTok: 0.50}}

	// 1. Register: 500 vs 201 -> aturan 5xx, tanpa AI.
	res, _, err := a.Analyze(ctx, fps["register user"], false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Source != "rule" || res.Category != "backend_bug" || fake.calls != 0 {
		t.Errorf("register harus diputuskan aturan tanpa AI: %+v calls=%d", res, fake.calls)
	}

	// 2. Order: aturan tidak cocok -> AI. Bukti karangan dibuang.
	res, cached, err := a.Analyze(ctx, fps["order dibayar"], false)
	if err != nil {
		t.Fatal(err)
	}
	if cached || res.Source != "ai" || fake.calls != 1 || res.Category != "backend_bug" {
		t.Fatalf("order harus lewat AI: %+v", res)
	}
	if len(res.Evidence) != 1 || res.Evidence[0] != "Kode test berubah: tidak" {
		t.Errorf("hanya bukti yang ada di fakta yang boleh disimpan: %v", res.Evidence)
	}
	if math.Abs(res.CostUSD-0.000195) > 1e-12 || res.InputTokens != 1200 {
		t.Errorf("biaya salah: %v", res.CostUSD)
	}
	req := fake.lastReq
	if req.ToolChoice == nil || req.ToolChoice.Name != toolName || len(req.Tools) != 1 {
		t.Errorf("model harus dipaksa menjawab lewat tool: %+v", req.ToolChoice)
	}
	for _, want := range []string{"Response API: tidak berubah", "PENDING", "GET /orders -> 200"} {
		if !strings.Contains(req.Messages[0].Content, want) {
			t.Errorf("fakta untuk AI harus memuat %q:\n%s", want, req.Messages[0].Content)
		}
	}

	// 3. Panggilan kedua memakai cache: tidak ada biaya.
	res, cached, err = a.Analyze(ctx, fps["order dibayar"], false)
	if err != nil || !cached || fake.calls != 1 || res.Category != "backend_bug" {
		t.Errorf("harus dari cache: cached=%v calls=%d err=%v", cached, fake.calls, err)
	}

	// 4. force: analisis ulang. Semua bukti karangan -> confidence diturunkan.
	fake.verdict.Evidence = []string{"tabel orders kehilangan kolom paid_at"}
	res, _, err = a.Analyze(ctx, fps["order dibayar"], true)
	if err != nil || fake.calls != 2 || res.Confidence != "low" || len(res.Evidence) != 0 {
		t.Errorf("tanpa bukti valid, confidence harus low: %+v err=%v", res, err)
	}

	// 5. Label manual menang atas semuanya.
	if err := s.SetLabel(ctx, fps["order dibayar"], "test_bug", "status memang sengaja diubah jadi PENDING", "ega"); err != nil {
		t.Fatal(err)
	}
	res, _, err = a.Analyze(ctx, fps["order dibayar"], true)
	if err != nil || res.Source != "human" || res.Category != "test_bug" || fake.calls != 2 {
		t.Errorf("label manual harus dipakai tanpa AI: %+v err=%v calls=%d", res, err, fake.calls)
	}

	// 6. AI mati: aturan tetap jalan, sisanya ErrAIDisabled.
	off := &Analyzer{Store: s}
	if res, _, err := off.Analyze(ctx, fps["brands punya slug"], false); err != nil || res.Source != "rule" {
		t.Errorf("aturan matriks harus jalan tanpa AI: %+v %v", res, err)
	}
	if err := s.SetLabel(ctx, fps["order dibayar"], "", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := off.Analyze(ctx, fps["order dibayar"], true); !errors.Is(err, ErrAIDisabled) {
		t.Errorf("mau ErrAIDisabled, dapat %v", err)
	}
	if _, _, err := off.Analyze(ctx, "tidakada", false); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("mau ErrNotFound, dapat %v", err)
	}
}

func TestApplyRules(t *testing.T) {
	cases := map[string]string{
		"Error: apiRequestContext.get: connect ECONNREFUSED 127.0.0.1:8091\nCall log:": "environment",
		"Error: getaddrinfo ENOTFOUND api.toolshop.local":                              "environment",
		"Expected: 201\nReceived: 500":                                                 "backend_bug",
		"Expected: 200\nReceived: 503":                                                 "backend_bug",
		"Expected: 201\nReceived: 422":                                                 "", // 4xx bisa salah test atau salah API: serahkan ke AI
		"Expected: 500\nReceived: 200":                                                 "",
		"Test timeout of 30000ms exceeded.":                                            "",
		"ZodError: [\n  {\n    \"message\": \"Invalid input: expected array, received Promise\"\n  }\n]": "test_bug",
		"Expected: 200\nReceived: Promise {}": "test_bug",
	}
	for msg, want := range cases {
		got := ApplyRules(&store.Facts{ErrorMessage: msg})
		switch {
		case want == "" && got != nil:
			t.Errorf("%q tidak boleh diputuskan aturan, dapat %s", msg, got.Category)
		case want != "" && (got == nil || got.Category != want):
			t.Errorf("%q: mau %s, dapat %+v", msg, want, got)
		}
	}
}

func TestGrounded(t *testing.T) {
	facts := "Pesan error:\nExpected: 201\n  Received:   500\n"
	got := grounded([]string{"Received: 500", "Expected: 201", "kolom hilang", "", "Received: 500", "Expected: 201"}, facts)
	if len(got) != 4 || got[0] != "Received: 500" {
		t.Errorf("spasi harus diabaikan, karangan dibuang, maksimal 4: %v", got)
	}
}
