package api

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"redline/internal/analysis"
	"redline/internal/report"
	"redline/internal/store"
)

type fakeStore struct {
	gotMeta  store.RunMeta
	gotTests int
	statuses []string
	label    string
}

func (f *fakeStore) IngestReport(_ context.Context, rep *report.Report, meta store.RunMeta) (*store.IngestResult, error) {
	f.gotMeta = meta
	f.gotTests = len(rep.Outcomes())
	return &store.IngestResult{RunID: 7, Total: f.gotTests}, nil
}
func (f *fakeStore) ListRuns(context.Context, int) ([]store.Run, error) { return []store.Run{}, nil }
func (f *fakeStore) ListGroups(_ context.Context, st []string, _ int) ([]store.Group, error) {
	f.statuses = st
	return []store.Group{}, nil
}
func (f *fakeStore) GetGroup(_ context.Context, fp string) (store.Group, []store.Occurrence, error) {
	return store.Group{}, nil, store.ErrNotFound
}
func (f *fakeStore) GetAnalysis(context.Context, string, string) (store.Analysis, error) {
	return store.Analysis{}, store.ErrNotFound
}
func (f *fakeStore) SetLabel(_ context.Context, fp, label, note string) error {
	if fp != "ada" {
		return store.ErrNotFound
	}
	f.label = label + "|" + note
	return nil
}

type fakeAnalyzer struct {
	force bool
	err   error
}

func (a *fakeAnalyzer) Analyze(_ context.Context, fp string, force bool) (*store.Analysis, bool, error) {
	a.force = force
	if a.err != nil {
		return nil, false, a.err
	}
	return &store.Analysis{Fingerprint: fp, Source: "ai", Category: "backend_bug"}, !force, nil
}

func newServer(fs *fakeStore, max int64) http.Handler {
	return (&Server{Store: fs, Analyzer: &fakeAnalyzer{}, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), MaxBodySize: max}).Routes()
}

func do(h http.Handler, method, url string, body io.Reader) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, url, body))
	return rec
}

func TestIngest(t *testing.T) {
	fs := &fakeStore{}
	f, _ := os.Open("../report/testdata/run1-bug.json")
	defer f.Close()
	rec := do(newServer(fs, 0), "POST", "/api/runs?source=ci&branch=main&commit=abc&app_version=sprint5", f)
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"run_id": 7`) {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if fs.gotMeta != (store.RunMeta{Source: "ci", Branch: "main", CommitSHA: "abc", AppVersion: "sprint5"}) || fs.gotTests != 6 {
		t.Errorf("metadata/test tidak diteruskan: %+v %d", fs.gotMeta, fs.gotTests)
	}
}

func TestIngest_Rejects(t *testing.T) {
	h := newServer(&fakeStore{}, 100)
	cases := []struct {
		url, body string
		code      int
	}{
		{"/api/runs", `bukan json`, http.StatusBadRequest},
		{"/api/runs", `{"foo":1}`, http.StatusBadRequest},
		{"/api/runs?source=prod", `{"suites":[]}`, http.StatusBadRequest},
		{"/api/runs", `{"suites":[` + strings.Repeat(`{"title":"x"},`, 20) + `{}]}`, http.StatusRequestEntityTooLarge},
	}
	for _, c := range cases {
		if rec := do(h, "POST", c.url, strings.NewReader(c.body)); rec.Code != c.code {
			t.Errorf("%s %q: mau %d, dapat %d %s", c.url, c.body[:min(len(c.body), 20)], c.code, rec.Code, rec.Body)
		}
	}
}

func TestListGroups_StatusFilter(t *testing.T) {
	fs := &fakeStore{}
	h := newServer(fs, 0)
	if rec := do(h, "GET", "/api/groups", nil); rec.Code != 200 || fs.statuses != nil {
		t.Errorf("default harus nil (open+regressed): %v", fs.statuses)
	}
	do(h, "GET", "/api/groups?status=all", nil)
	if len(fs.statuses) != 3 {
		t.Errorf("all harus 3 status: %v", fs.statuses)
	}
	if rec := do(h, "GET", "/api/groups?status=lol", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("status tidak dikenal harus 400, dapat %d", rec.Code)
	}
	if rec := do(h, "GET", "/api/groups/abc", nil); rec.Code != http.StatusNotFound {
		t.Errorf("kelompok tidak ada harus 404, dapat %d", rec.Code)
	}
}

func TestAnalyze(t *testing.T) {
	az := &fakeAnalyzer{}
	h := (&Server{Store: &fakeStore{}, Analyzer: az, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}).Routes()

	rec := do(h, "POST", "/api/groups/abc/analyze", nil)
	if rec.Code != 200 || az.force || !strings.Contains(rec.Body.String(), `"cached": true`) {
		t.Errorf("tanpa force harus boleh pakai cache: %d %s", rec.Code, rec.Body)
	}
	do(h, "POST", "/api/groups/abc/analyze?force=1", nil)
	if !az.force {
		t.Error("force=1 harus diteruskan")
	}

	for err, code := range map[error]int{
		store.ErrNotFound:        http.StatusNotFound,
		analysis.ErrAIDisabled:   http.StatusServiceUnavailable,
		errors.New("claude 529"): http.StatusBadGateway,
	} {
		az.err = err
		if rec := do(h, "POST", "/api/groups/abc/analyze", nil); rec.Code != code {
			t.Errorf("%v: mau %d, dapat %d", err, code, rec.Code)
		}
	}
}

func TestLabel(t *testing.T) {
	fs := &fakeStore{}
	h := newServer(fs, 0)
	rec := do(h, "PUT", "/api/groups/ada/label", strings.NewReader(`{"label":"test_bug","note":"assertion usang"}`))
	if rec.Code != 200 || fs.label != "test_bug|assertion usang" {
		t.Errorf("label tidak tersimpan: %d %s %q", rec.Code, rec.Body, fs.label)
	}
	if rec := do(h, "PUT", "/api/groups/ada/label", strings.NewReader(`{"label":"salah"}`)); rec.Code != 400 {
		t.Errorf("label tidak dikenal harus 400, dapat %d", rec.Code)
	}
	if rec := do(h, "PUT", "/api/groups/tidakada/label", strings.NewReader(`{"label":"flaky"}`)); rec.Code != 404 {
		t.Errorf("kelompok tidak ada harus 404, dapat %d", rec.Code)
	}
	if rec := do(h, "PUT", "/api/groups/ada/label", strings.NewReader(`{"label":""}`)); rec.Code != 200 || fs.label != "|" {
		t.Errorf("label kosong harus menghapus label: %q", fs.label)
	}
}
