package ui

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"

	"redline/internal/auth"
)

func testHandler(t *testing.T, files fstest.MapFS) http.Handler {
	t.Helper()
	return New(files, slog.New(slog.NewTextHandler(io.Discard, nil)), auth.New("", nil))
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

func securedHandler(t *testing.T) http.Handler {
	t.Helper()
	return New(
		fstest.MapFS{
			"index.html":    {Data: []byte("<html>app</html>")},
			"assets/app.js": {Data: []byte("console.log(1)")},
		},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		auth.New("secret", nil),
	)
}

func TestLoginRequiredForPages(t *testing.T) {
	h := securedHandler(t)

	rec := get(t, h, "/failures/abc")
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d", rec.Code)
	}
	want := "/ui/login?next=%2Ffailures%2Fabc"
	if got := rec.Header().Get("Location"); got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
}

func TestAssetsStayPublic(t *testing.T) {
	if rec := get(t, securedHandler(t), "/assets/app.js"); rec.Code != http.StatusOK {
		t.Errorf("asset status = %d", rec.Code)
	}
}

func TestLoginFlow(t *testing.T) {
	h := securedHandler(t)

	if rec := get(t, h, "/login"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Sign in") {
		t.Fatalf("login page: %d %q", rec.Code, rec.Body)
	}

	post := func(key string) *httptest.ResponseRecorder {
		form := url.Values{"key": {key}, "next": {"/ui/failures/abc"}}
		req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	if rec := post("wrong"); rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong key status = %d", rec.Code)
	}

	rec := post("secret")
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/ui/failures/abc" {
		t.Fatalf("login: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	cookies := rec.Result().Cookies()
	var session *http.Cookie
	hasCSRF := false
	for _, cookie := range cookies {
		if cookie.Name == auth.CookieName {
			session = cookie
		}
		if cookie.Name == auth.CSRFCookieName && !cookie.HttpOnly && cookie.Value != "" {
			hasCSRF = true
		}
	}
	if session == nil || !session.HttpOnly || !hasCSRF {
		t.Fatalf("cookies = %+v", cookies)
	}

	req := httptest.NewRequest(http.MethodGet, "/ui/failures/abc", nil)
	req.AddCookie(session)
	page := httptest.NewRecorder()
	h.ServeHTTP(page, req)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "app") {
		t.Errorf("authenticated page: %d %q", page.Code, page.Body)
	}
}

func TestLogoutClearsCookie(t *testing.T) {
	h := securedHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/logout", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/ui/login" {
		t.Fatalf("logout: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	cookies := rec.Result().Cookies()
	cleared := map[string]bool{}
	for _, cookie := range cookies {
		if cookie.MaxAge < 0 {
			cleared[cookie.Name] = true
		}
	}
	if !cleared[auth.CookieName] || !cleared[auth.CSRFCookieName] {
		t.Errorf("logout cookies = %+v", cookies)
	}
}
