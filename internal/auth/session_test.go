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
	s.SetCookie(rec, httptest.NewRequest(http.MethodPost, "/ui/login", nil))
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %d, want 1", len(cookies))
	}
	return cookies[0]
}

func TestDisabledWithoutKey(t *testing.T) {
	s := New("")
	if s.Enabled() {
		t.Fatal("empty key must disable auth")
	}
	if !s.Valid(requestWithCookie(nil)) {
		t.Fatal("open server must accept requests")
	}
	rec := httptest.NewRecorder()
	s.SetCookie(rec, httptest.NewRequest(http.MethodPost, "/ui/login", nil))
	if got := rec.Header().Get("Set-Cookie"); got != "" {
		t.Errorf("open server must not set a cookie: %q", got)
	}
	if !s.CheckKey("anything") {
		t.Error("open server must accept any key")
	}
}

func TestSessionRoundTrip(t *testing.T) {
	s := New("secret")
	cookie := issue(t, s)
	if !s.Valid(requestWithCookie(cookie)) {
		t.Fatal("fresh cookie should be valid")
	}
	if cookie.HttpOnly != true || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/" {
		t.Errorf("cookie flags: %+v", cookie)
	}
}

func TestSessionTampered(t *testing.T) {
	s := New("secret")
	cookie := issue(t, s)
	cookie.Value = cookie.Value[:len(cookie.Value)-1] + "x"
	if s.Valid(requestWithCookie(cookie)) {
		t.Fatal("tampered signature must be rejected")
	}
}

func TestSessionExpires(t *testing.T) {
	s := New("secret")
	cookie := issue(t, s)
	s.now = func() time.Time { return time.Now().Add(13 * time.Hour) }
	if s.Valid(requestWithCookie(cookie)) {
		t.Fatal("expired cookie must be rejected")
	}
}

func TestRotationInvalidatesSession(t *testing.T) {
	cookie := issue(t, New("old"))
	if New("new").Valid(requestWithCookie(cookie)) {
		t.Fatal("rotating the API key must invalidate old sessions")
	}
}

func TestCheckKey(t *testing.T) {
	s := New("secret")
	if s.CheckKey("wrong") || !s.CheckKey("secret") {
		t.Error("CheckKey must compare the configured key")
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
	s := New("secret")
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/ui/login", strings.NewReader(""))
	r.Header.Set("X-Forwarded-Proto", "https")
	s.SetCookie(rec, r)
	cookie := rec.Result().Cookies()[0]
	if !cookie.Secure {
		t.Error("cookie must be Secure behind an HTTPS proxy")
	}
}
