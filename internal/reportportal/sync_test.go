package reportportal

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"redline/internal/analysis"
	"redline/internal/store"
)

// fakeRP meniru bagian API ReportPortal yang dipakai: launch berdasarkan atribut, item yang gagal, dan update issue.
type fakeRP struct {
	mu          sync.Mutex
	launchCalls int
	finishAfter int // launch masih IN_PROGRESS untuk sekian panggilan pertama
	items       []map[string]any
	updates     []map[string]any
	auth        string
	filter      string
}

func (f *fakeRP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.auth = r.Header.Get("Authorization")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/proj/launch":
		f.launchCalls++
		f.filter = r.URL.Query().Get("filter.has.compositeAttribute")
		if f.filter != "redline_run:tok-1" {
			_ = json.NewEncoder(w).Encode(map[string]any{"content": []any{}})
			return
		}
		status := "FAILED"
		if f.launchCalls <= f.finishAfter {
			status = "IN_PROGRESS"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"content": []any{
			map[string]any{"id": 77, "uuid": "u-77", "number": 9, "status": status}}})
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/proj/item":
		q := r.URL.Query()
		if q.Get("filter.eq.launchId") != "77" || q.Get("filter.eq.hasStats") != "true" || q.Get("filter.eq.status") != "FAILED" {
			http.Error(w, "filter salah", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"content": f.items, "page": map[string]any{"totalPages": 1}})
	case r.Method == http.MethodPut && r.URL.Path == "/api/v1/proj/item":
		var body struct {
			Issues []map[string]any `json:"issues"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.updates = append(f.updates, body.Issues...)
		_, _ = w.Write([]byte(`[]`))
	default:
		http.NotFound(w, r)
	}
}

func item(id int, caseID, issueType string, aa bool) map[string]any {
	m := map[string]any{"id": id, "name": "t" + caseID, "testCaseId": caseID}
	if issueType != "" {
		m["issue"] = map[string]any{"issueType": issueType, "autoAnalyzed": aa}
	}
	return m
}

type fakeStore struct{ failures []store.RunFailure }

func (f fakeStore) RunFailures(context.Context, int64) ([]store.RunFailure, error) {
	return f.failures, nil
}

type fakeAnalyzer map[string]*store.Analysis

func (f fakeAnalyzer) Analyze(_ context.Context, fp string, _ bool) (*store.Analysis, bool, error) {
	if a, ok := f[fp]; ok {
		return a, false, nil
	}
	if fp == "fp-noai" {
		return nil, false, analysis.ErrAIDisabled
	}
	return nil, false, errors.New("tidak bisa dianalisis")
}

func TestSync(t *testing.T) {
	rp := &fakeRP{finishAfter: 2, items: []map[string]any{
		item(1, "TC-1", "ti001", false), // bug backend → Product Bug
		item(2, "TC-2", "ti001", false), // tidak jelas → tetap To Investigate, diberi komentar
		item(3, "TC-3", "ab001", true),  // sudah dilabeli Auto-Analysis ReportPortal → tidak diubah
		item(4, "TC-4", "ab001", false), // dilabeli manusia → tidak diubah
		item(5, "TC-5", "", false),      // tanpa issue (belum diproses) → diperlakukan seperti To Investigate
		item(6, "TC-9", "ti001", false), // tidak ada kegagalan Redline dengan ID ini
		item(7, "", "ti001", false),     // tanpa ID test case
		item(8, "TC-8", "ti001", false), // aturan tidak memutuskan dan AI mati → tetap To Investigate, dengan alasan
	}}
	srv := httptest.NewServer(rp)
	defer srv.Close()

	s := &Syncer{
		Client: &Client{BaseURL: srv.URL, Project: "proj", APIKey: "key-1"},
		Store: fakeStore{failures: []store.RunFailure{
			{TestKey: "api › a.spec.ts › one", CaseID: "TC-1", Fingerprint: "fp1"},
			{TestKey: "api › a.spec.ts › two", CaseID: "TC-2", Fingerprint: "fp2"},
			{TestKey: "api › a.spec.ts › three", CaseID: "TC-3", Fingerprint: "fp3"},
			{TestKey: "api › a.spec.ts › five", CaseID: "TC-5", Fingerprint: "fp5"},
			{TestKey: "api › a.spec.ts › eight", CaseID: "TC-8", Fingerprint: "fp-noai"},
		}},
		Analyzer: fakeAnalyzer{
			"fp1": {Category: "backend_bug", Confidence: "high", Source: "rule", Summary: "API membalas 500.",
				Evidence: []string{"Received: 500"}, NextStep: "Laporkan ke backend."},
			"fp2": {Category: "unknown", Confidence: "low", Source: "ai", Summary: "Belum jelas."},
			"fp3": {Category: "test_bug", Confidence: "high", Source: "rule", Summary: "x"},
			"fp5": {Category: "flaky", Confidence: "high", Source: "experiment", Summary: "Lulus saat diulang."},
		},
		Wait: 5 * time.Second, Interval: 10 * time.Millisecond,
	}
	res, err := s.Sync(context.Background(), 12, "tok-1")
	if err != nil {
		t.Fatal(err)
	}
	if res != (Result{Launch: 9, Updated: 2, Unclear: 2, Labelled: 2, Unmatched: 2}) {
		t.Errorf("ringkasan salah: %+v", res)
	}
	if rp.launchCalls != 3 {
		t.Errorf("harus menunggu launch selesai (2x IN_PROGRESS), dipanggil %d kali", rp.launchCalls)
	}
	if rp.auth != "Bearer key-1" || rp.filter != "redline_run:tok-1" {
		t.Errorf("otentikasi atau filter salah: %q %q", rp.auth, rp.filter)
	}

	got := map[float64]map[string]any{}
	for _, u := range rp.updates {
		got[u["testItemId"].(float64)] = u["issue"].(map[string]any)
	}
	if len(got) != 4 {
		t.Fatalf("mau 4 item diubah (1, 2, 5, 8), dapat %v", rp.updates)
	}
	if got[8]["issueType"] != "ti001" || !strings.Contains(got[8]["comment"].(string), "AI analysis is off") {
		t.Errorf("item 8: %v", got[8])
	}
	if got[1]["issueType"] != "pb001" || !strings.Contains(got[1]["comment"].(string), "**Redline: Product Bug** (high confidence, by rules)") ||
		!strings.Contains(got[1]["comment"].(string), "- `Received: 500`") || got[1]["autoAnalyzed"] != false {
		t.Errorf("item 1: %v", got[1])
	}
	if got[2]["issueType"] != "ti001" || !strings.Contains(got[2]["comment"].(string), "Redline: unclear") {
		t.Errorf("item 2 harus tetap To Investigate dengan komentar: %v", got[2])
	}
	if got[5]["issueType"] != "ab001" || !strings.Contains(got[5]["comment"].(string), "Flaky") {
		t.Errorf("item 5 (flaky): %v", got[5])
	}
	for _, id := range []float64{3, 4, 6, 7} {
		if _, ok := got[id]; ok {
			t.Errorf("item %v tidak boleh diubah", id)
		}
	}
}

func TestSync_LaunchNeverAppears(t *testing.T) {
	srv := httptest.NewServer(&fakeRP{})
	defer srv.Close()
	s := &Syncer{Client: &Client{BaseURL: srv.URL, Project: "proj", APIKey: "k"}, Store: fakeStore{},
		Analyzer: fakeAnalyzer{}, Wait: 50 * time.Millisecond, Interval: 10 * time.Millisecond}
	if _, err := s.Sync(context.Background(), 1, "tok-lain"); !errors.Is(err, ErrLaunchNotFound) {
		t.Errorf("mau ErrLaunchNotFound, dapat %v", err)
	}
}

func TestClient_ErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"message":"Access denied"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, Project: "proj", APIKey: "salah"}
	if _, err := c.LaunchByAttribute(context.Background(), "k", "v"); err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("status 401 harus jadi error yang jelas: %v", err)
	}
}

func TestComment_Long(t *testing.T) {
	c := Comment(&store.Analysis{Category: "test_bug", Confidence: "low", Source: "ai", Summary: strings.Repeat("é", 5000)})
	if len([]rune(c)) > 4001 || !strings.HasSuffix(c, "…") || !strings.Contains(c, "Automation Bug") {
		t.Errorf("komentar panjang harus dipotong rapi: %d karakter", len([]rune(c)))
	}
}
