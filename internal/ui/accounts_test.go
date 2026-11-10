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
	users map[string]store.User
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

func uiHandler(t *testing.T, accounts Accounts, setupToken string) (http.Handler, *auth.Sessions) {
	t.Helper()
	sessions := auth.NewWithSecret("", nil, "test-session-secret")
	h := New(fstest.MapFS{"index.html": {Data: []byte("<html>app</html>")}},
		slog.New(slog.NewTextHandler(io.Discard, nil)), sessions, WithAccounts(accounts, setupToken))
	return h, sessions
}

func formRequest(method, path string, values url.Values) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

func TestFirstRunSetupAndPasswordLogin(t *testing.T) {
	accounts := newFakeAccounts()
	h, sessions := uiHandler(t, accounts, "setup-token")

	rec := get(t, h, "/setup")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Set up the admin account") {
		t.Fatalf("setup page = %d: %s", rec.Code, rec.Body)
	}
	form := url.Values{"token": {"setup-token"}, "username": {"ega"}, "password": {"correct horse battery"}, "confirm": {"correct horse battery"}}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, formRequest(http.MethodPost, "/setup", form))
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/ui/" {
		t.Fatalf("setup = %d %q: %s", rec.Code, rec.Header().Get("Location"), rec.Body)
	}
	if len(accounts.users) != 1 || !auth.VerifyPassword(accounts.users["ega"].PasswordHash, "correct horse battery") {
		t.Errorf("admin tidak dibuat dengan hash yang benar: %+v", accounts.users)
	}
	var sessionCookies []*http.Cookie
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == auth.CookieName {
			sessionCookies = append(sessionCookies, cookie)
		}
	}
	if len(sessionCookies) == 0 {
		t.Fatal("setup harus membuat session")
	}

	// Setup is closed once an admin exists.
	if rec := get(t, h, "/setup"); rec.Code != http.StatusFound || rec.Header().Get("Location") != "/ui/login" {
		t.Errorf("setup setelah admin = %d %q", rec.Code, rec.Header().Get("Location"))
	}

	// Password login uses the account, not REDLINE_API_KEY.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/login", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Username") {
		t.Fatalf("login password page = %d: %s", rec.Code, rec.Body)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, formRequest(http.MethodPost, "/login",
		url.Values{"username": {"ega"}, "password": {"wrong"}, "next": {"/ui/"}}))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("password salah = %d, want 401", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, formRequest(http.MethodPost, "/login",
		url.Values{"username": {"ega"}, "password": {"correct horse battery"}, "next": {"/ui/"}}))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("login = %d: %s", rec.Code, rec.Body)
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
