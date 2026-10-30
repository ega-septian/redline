package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func requestWithCookie(cookie *http.Cookie) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/ui/", nil)
	if cookie != nil {
		r.AddCookie(cookie)
	}
	return r
}

func issue(t *testing.T, s *Sessions) *http.Cookie {
	t.Helper()
	rec := httptest.NewRecorder()
	s.SetCookie(rec, httptest.NewRequest(http.MethodPost, "/ui/login", nil), "tester")
	cookies := rec.Result().Cookies()
	for _, cookie := range cookies {
		if cookie.Name == CookieName {
			return cookie
		}
	}
	t.Fatalf("session cookie missing: %+v", cookies)
	return nil
}

func csrfCookie(t *testing.T, s *Sessions) *http.Cookie {
	t.Helper()
	rec := httptest.NewRecorder()
	s.SetCookie(rec, httptest.NewRequest(http.MethodPost, "/ui/login", nil), "tester")
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == CSRFCookieName {
			return cookie
		}
	}
	t.Fatal("csrf cookie missing")
	return nil
}

func TestDisabledWithoutKey(t *testing.T) {
	s := New("", nil)
	if s.Enabled() {
		t.Fatal("empty key must disable auth")
	}
	if !s.Valid(requestWithCookie(nil)) {
		t.Fatal("open server must accept requests")
	}
	rec := httptest.NewRecorder()
	s.SetCookie(rec, httptest.NewRequest(http.MethodPost, "/ui/login", nil), "tester")
	if got := rec.Header().Get("Set-Cookie"); got != "" {
		t.Errorf("open server must not set a cookie: %q", got)
	}
	if _, ok := s.CheckKey("anything"); !ok {
		t.Error("open server must accept any key")
	}
}

func TestSessionRoundTrip(t *testing.T) {
	s := New("secret", nil)
	cookie := issue(t, s)
	if !s.Valid(requestWithCookie(cookie)) {
		t.Fatal("fresh cookie should be valid")
	}
	if cookie.HttpOnly != true || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/" {
		t.Errorf("cookie flags: %+v", cookie)
	}
}

func TestSessionTampered(t *testing.T) {
	s := New("secret", nil)
	cookie := issue(t, s)
	cookie.Value = cookie.Value[:len(cookie.Value)-1] + "x"
	if s.Valid(requestWithCookie(cookie)) {
		t.Fatal("tampered signature must be rejected")
	}
}

func TestSessionExpires(t *testing.T) {
	s := New("secret", nil)
	cookie := issue(t, s)
	s.now = func() time.Time { return time.Now().Add(13 * time.Hour) }
	if s.Valid(requestWithCookie(cookie)) {
		t.Fatal("expired cookie must be rejected")
	}
}

func TestRotationInvalidatesSession(t *testing.T) {
	cookie := issue(t, New("old", nil))
	if New("new", nil).Valid(requestWithCookie(cookie)) {
		t.Fatal("rotating the API key must invalidate old sessions")
	}
}

func TestCheckKey(t *testing.T) {
	s := New("secret", nil)
	if _, ok := s.CheckKey("wrong"); ok {
		t.Error("wrong key must fail")
	}
	if user, ok := s.CheckKey("secret"); !ok || user != "admin" {
		t.Errorf("secret key = %q, %v", user, ok)
	}
}

func TestUserKeySession(t *testing.T) {
	s := New("", map[string]string{"ega": "key-ega", "budi": "key-budi"})
	if user, ok := s.CheckKey("key-budi"); !ok || user != "budi" {
		t.Fatalf("user key = %q, %v", user, ok)
	}
	if _, ok := s.CheckKey("key-ega2"); ok {
		t.Error("unknown key must fail")
	}

	rec := httptest.NewRecorder()
	s.SetCookie(rec, httptest.NewRequest(http.MethodPost, "/ui/login", nil), "budi")
	var cookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == CookieName {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("session cookie missing")
	}
	req := requestWithCookie(cookie)
	if !s.Valid(req) || s.User(req) != "budi" {
		t.Errorf("session user = %q, valid=%v", s.User(req), s.Valid(req))
	}
}

func TestCSRFDoubleSubmit(t *testing.T) {
	s := New("secret", nil)
	cookie := csrfCookie(t, s)
	if cookie.HttpOnly {
		t.Error("csrf cookie must be readable by the UI")
	}

	req := httptest.NewRequest(http.MethodPut, "/api/groups/x/label", nil)
	req.AddCookie(cookie)
	if s.CSRFValid(req) {
		t.Error("missing header must fail")
	}
	req.Header.Set("X-CSRF-Token", cookie.Value)
	if !s.CSRFValid(req) {
		t.Error("matching header must pass")
	}
	req.Header.Set("X-CSRF-Token", cookie.Value+"x")
	if s.CSRFValid(req) {
		t.Error("wrong header must fail")
	}
	if !New("", nil).CSRFValid(req) {
		t.Error("open server must not require CSRF")
	}
}

func TestEnsureCSRFForExistingSession(t *testing.T) {
	s := New("secret", nil)
	req := httptest.NewRequest(http.MethodGet, "/ui/", nil)
	rec := httptest.NewRecorder()
	s.EnsureCSRF(rec, req)
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != CSRFCookieName || cookies[0].Value == "" {
		t.Fatalf("cookies = %+v", cookies)
	}

	req.AddCookie(cookies[0])
	rec = httptest.NewRecorder()
	s.EnsureCSRF(rec, req)
	if got := rec.Result().Cookies(); len(got) != 0 {
		t.Errorf("existing CSRF cookie must not be replaced: %+v", got)
	}
}

func TestNextPath(t *testing.T) {
	cases := map[string]string{
		"":                 "/ui/",
		"/ui/failures/abc": "/ui/failures/abc",
		"/api/runs":        "/ui/",
		"//evil.example":   "/ui/",
		"https://evil":     "/ui/",
	}
	for input, want := range cases {
		if got := NextPath(input); got != want {
			t.Errorf("NextPath(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestSecureCookieBehindProxy(t *testing.T) {
	s := New("secret", nil)
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/ui/login", strings.NewReader(""))
	r.Header.Set("X-Forwarded-Proto", "https")
	s.SetCookie(rec, r, "tester")
	var session *http.Cookie
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == CookieName {
			session = cookie
		}
	}
	if session == nil || !session.Secure {
		t.Error("session cookie must be Secure behind an HTTPS proxy")
	}
}
