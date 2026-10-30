package ui

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func testHandler(t *testing.T, files fstest.MapFS) http.Handler {
	t.Helper()
	return New(files, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestServesIndex(t *testing.T) {
	h := testHandler(t, fstest.MapFS{
		"index.html":    {Data: []byte("<html>app</html>")},
		"assets/app.js": {Data: []byte("console.log(1)")},
	})

	rec := get(t, h, "/")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "app") {
		t.Fatalf("index: status %d body %q", rec.Code, rec.Body)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("index cache = %q", rec.Header().Get("Cache-Control"))
	}
	if rec.Header().Get("Content-Security-Policy") == "" {
		t.Error("CSP header missing")
	}
}

func TestServesHashedAssetsWithLongCache(t *testing.T) {
	h := testHandler(t, fstest.MapFS{
		"index.html":    {Data: []byte("<html>app</html>")},
		"assets/app.js": {Data: []byte("console.log(1)")},
	})

	rec := get(t, h, "/assets/app.js")
	if rec.Code != http.StatusOK || rec.Body.String() != "console.log(1)" {
		t.Fatalf("asset: status %d body %q", rec.Code, rec.Body)
	}
	if got := rec.Header().Get("Cache-Control"); !strings.Contains(got, "immutable") {
		t.Errorf("asset cache = %q", got)
	}
}

func TestClientRouteFallsBackToIndex(t *testing.T) {
	h := testHandler(t, fstest.MapFS{
		"index.html": {Data: []byte("<html>app</html>")},
	})

	rec := get(t, h, "/failures/8f3c1a2b")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "app") {
		t.Fatalf("fallback: status %d body %q", rec.Code, rec.Body)
	}
}

func TestMissingAssetStays404(t *testing.T) {
	h := testHandler(t, fstest.MapFS{
		"index.html": {Data: []byte("<html>app</html>")},
	})

	if rec := get(t, h, "/assets/missing.js"); rec.Code != http.StatusNotFound {
		t.Errorf("missing asset status = %d", rec.Code)
	}
}

func TestNotBuilt(t *testing.T) {
	h := testHandler(t, fstest.MapFS{"dist/.gitkeep": {Data: []byte{}}})

	rec := get(t, h, "/")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "not built") {
		t.Errorf("body should explain the missing build: %q", rec.Body)
	}
}
