package ui

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"

	"redline/internal/auth"
	"redline/internal/store"
)

type fakeAccounts struct {
	users     map[string]store.User
	createErr error
}

func newFakeAccounts() *fakeAccounts { return &fakeAccounts{users: map[string]store.User{}} }

func (f *fakeAccounts) UserCount(context.Context) (int, error) { return len(f.users), nil }
func (f *fakeAccounts) User(_ context.Context, username string) (store.User, error) {
	user, ok := f.users[username]
	if !ok {
		return store.User{}, store.ErrNotFound
	}
	return user, nil
}
func (f *fakeAccounts) CreateAdmin(_ context.Context, username, hash string) (store.User, error) {
	if f.createErr != nil {
		return store.User{}, f.createErr
	}
	user := store.User{Username: username, PasswordHash: hash, Role: "admin"}
	f.users[username] = user
	return user, nil
}
func (f *fakeAccounts) UpdatePassword(_ context.Context, username, hash string) error {
	user, ok := f.users[username]
	if !ok {
		return store.ErrNotFound
	}
	user.PasswordHash = hash
	f.users[username] = user
	return nil
}

func uiHandler(t *testing.T, accounts Accounts) (http.Handler, *auth.Sessions) {
	t.Helper()
	sessions := auth.NewWithSecret("", nil, "test-session-secret")
	h := New(fstest.MapFS{"index.html": {Data: []byte("<html>app</html>")}},
		slog.New(slog.NewTextHandler(io.Discard, nil)), sessions, WithAccounts(accounts))
	return h, sessions
}

func formRequest(method, path string, values url.Values) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

func TestLoginRedirectsToRegisterOnFirstRun(t *testing.T) {
	h, _ := uiHandler(t, newFakeAccounts())

	if rec := get(t, h, "/login"); rec.Code != http.StatusFound || rec.Header().Get("Location") != "/ui/register" {
		t.Fatalf("login tanpa admin = %d %q", rec.Code, rec.Header().Get("Location"))
	}

	rec := get(t, h, "/register")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Create the admin account") {
		t.Fatalf("register page = %d: %s", rec.Code, rec.Body)
	}
	lower := strings.ToLower(rec.Body.String())
	if strings.Contains(lower, "token") || strings.Contains(lower, "api key") {
		t.Errorf("register page must not mention tokens or API keys: %s", rec.Body)
	}
}

func TestRegisterCreatesAdminAndSignsIn(t *testing.T) {
	accounts := newFakeAccounts()
	h, sessions := uiHandler(t, accounts)

	post := func(values url.Values) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, formRequest(http.MethodPost, "/register", values))
		return rec
	}

	if rec := post(url.Values{"username": {"ega"}, "password": {"short"}, "confirm": {"short"}}); rec.Code != http.StatusBadRequest {
		t.Errorf("short password = %d, want 400", rec.Code)
	}
	if rec := post(url.Values{"username": {"ega"}, "password": {"correct horse battery"}, "confirm": {"other horse battery"}}); rec.Code != http.StatusBadRequest {
		t.Errorf("password mismatch = %d, want 400", rec.Code)
	}
	if rec := post(url.Values{"username": {"bad name"}, "password": {"correct horse battery"}, "confirm": {"correct horse battery"}}); rec.Code != http.StatusBadRequest {
		t.Errorf("invalid username = %d, want 400", rec.Code)
	}
	if len(accounts.users) != 0 {
		t.Fatalf("akun tidak boleh dibuat sebelum validasi lolos: %+v", accounts.users)
	}

	rec := post(url.Values{"username": {"ega"}, "password": {"correct horse battery"}, "confirm": {"correct horse battery"}})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/ui/" {
		t.Fatalf("register = %d %q: %s", rec.Code, rec.Header().Get("Location"), rec.Body)
	}
	if len(accounts.users) != 1 || !auth.VerifyPassword(accounts.users["ega"].PasswordHash, "correct horse battery") {
		t.Errorf("admin tidak dibuat dengan hash yang benar: %+v", accounts.users)
	}

	var session *http.Cookie
	hasCSRF := false
	for _, cookie := range rec.Result().Cookies() {
		switch {
		case cookie.Name == auth.CookieName:
			session = cookie
		case cookie.Name == auth.CSRFCookieName && !cookie.HttpOnly && cookie.Value != "":
			hasCSRF = true
		}
	}
	if session == nil || !session.HttpOnly || !hasCSRF {
		t.Fatalf("cookies = %+v", rec.Result().Cookies())
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(session)
	if !sessions.Valid(req) || sessions.User(req) != "ega" {
		t.Errorf("session register tidak valid")
	}

	// Registration closes as soon as the first account exists.
	if rec := get(t, h, "/register"); rec.Code != http.StatusFound || rec.Header().Get("Location") != "/ui/login" {
		t.Errorf("register setelah admin = %d %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestPasswordLoginAndAccount(t *testing.T) {
	accounts := newFakeAccounts()
	h, sessions := uiHandler(t, accounts)
	if rec := get(t, h, "/register"); rec.Code != http.StatusOK {
		t.Fatalf("register page = %d", rec.Code)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, formRequest(http.MethodPost, "/register",
		url.Values{"username": {"ega"}, "password": {"correct horse battery"}, "confirm": {"correct horse battery"}}))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("register = %d: %s", rec.Code, rec.Body)
	}

	rec = get(t, h, "/login")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Username") {
		t.Fatalf("login page = %d: %s", rec.Code, rec.Body)
	}
	lower := strings.ToLower(rec.Body.String())
	if strings.Contains(lower, "token") || strings.Contains(lower, "api key") {
		t.Errorf("login page must not mention tokens or API keys: %s", rec.Body)
	}

	post := func(values url.Values) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, formRequest(http.MethodPost, "/login", values))
		return rec
	}

	// The legacy API-key field is not accepted by the password login.
	if rec := post(url.Values{"key": {"secret"}, "next": {"/ui/"}}); rec.Code != http.StatusUnauthorized {
		t.Errorf("api key login = %d, want 401", rec.Code)
	}
	if rec := post(url.Values{"username": {"ega"}, "password": {"wrong"}, "next": {"/ui/"}}); rec.Code != http.StatusUnauthorized {
		t.Errorf("password salah = %d, want 401", rec.Code)
	} else if !strings.Contains(rec.Body.String(), `value="ega"`) {
		t.Error("failed login should keep the username")
	}

	rec = post(url.Values{"username": {"ega"}, "password": {"correct horse battery"}, "next": {"/ui/failures/abc"}})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/ui/failures/abc" {
		t.Fatalf("login = %d %q: %s", rec.Code, rec.Header().Get("Location"), rec.Body)
	}
	var loginCookies []*http.Cookie
	for _, cookie := range rec.Result().Cookies() {
		loginCookies = append(loginCookies, cookie)
	}
	req := httptest.NewRequest(http.MethodGet, "/account", nil)
	for _, cookie := range loginCookies {
		req.AddCookie(cookie)
	}
	if !sessions.Valid(req) || sessions.User(req) != "ega" {
		t.Errorf("session login tidak valid")
	}

	// A wrong current password leaves the stored hash untouched.
	rec = httptest.NewRecorder()
	req = formRequest(http.MethodPost, "/account",
		url.Values{"current": {"wrong"}, "password": {"new correct horse"}, "confirm": {"new correct horse"}})
	for _, cookie := range loginCookies {
		req.AddCookie(cookie)
	}
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("current password salah = %d, want 401", rec.Code)
	}
	if !auth.VerifyPassword(accounts.users["ega"].PasswordHash, "correct horse battery") {
		t.Error("password lama berubah setelah permintaan ditolak")
	}

	// Change password, then the current session is cleared.
	rec = httptest.NewRecorder()
	req = formRequest(http.MethodPost, "/account",
		url.Values{"current": {"correct horse battery"}, "password": {"new correct horse"}, "confirm": {"new correct horse"}})
	for _, cookie := range loginCookies {
		req.AddCookie(cookie)
	}
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/ui/login?changed=1" {
		t.Fatalf("change password = %d %q: %s", rec.Code, rec.Header().Get("Location"), rec.Body)
	}
	if !auth.VerifyPassword(accounts.users["ega"].PasswordHash, "new correct horse") {
		t.Error("password baru tidak tersimpan")
	}
}

func TestRegisterRedirectsWhenAdminAlreadyExists(t *testing.T) {
	accounts := newFakeAccounts()
	accounts.createErr = store.ErrAdminExists
	h, _ := uiHandler(t, accounts)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, formRequest(http.MethodPost, "/register",
		url.Values{"username": {"ega"}, "password": {"correct horse battery"}, "confirm": {"correct horse battery"}}))
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/ui/login" {
		t.Fatalf("register conflict = %d %q", rec.Code, rec.Header().Get("Location"))
	}
}
