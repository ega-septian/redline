package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPasswordHashing(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword(hash, "correct horse battery staple") {
		t.Error("password benar harus cocok")
	}
	if VerifyPassword(hash, "wrong password") {
		t.Error("password salah tidak boleh cocok")
	}
	if _, err := HashPassword("short"); err == nil {
		t.Error("password pendek harus ditolak")
	}
	if _, err := HashPassword(string(make([]byte, 73))); err == nil {
		t.Error("password >72 byte harus ditolak")
	}
}

func TestUsernameAndToken(t *testing.T) {
	if !ValidUsername("ega") || !ValidUsername("ega.septian-1") || ValidUsername("bad name") {
		t.Error("validasi username salah")
	}
	token, err := NewToken(16)
	if err != nil || token == "" {
		t.Fatalf("token = %q %v", token, err)
	}
	if !TokenEqual(token, token) || TokenEqual(token, "other") {
		t.Error("perbandingan token salah")
	}
}

func TestSessionSecret(t *testing.T) {
	s := NewWithSecret("", nil, "stable-server-secret")
	if !s.Enabled() {
		t.Fatal("session dengan secret harus enabled")
	}
	rec := httptest.NewRecorder()
	s.SetCookie(rec, httptest.NewRequest(http.MethodPost, "/ui/login", nil), "ega")
	req := httptest.NewRequest(http.MethodGet, "/ui/", nil)
	for _, cookie := range rec.Result().Cookies() {
		req.AddCookie(cookie)
	}
	if !s.Valid(req) || s.User(req) != "ega" {
		t.Errorf("session secret harus valid: user=%q", s.User(req))
	}
	other := NewWithSecret("", nil, "different-secret")
	if other.Valid(req) {
		t.Error("secret berbeda tidak boleh memvalidasi cookie")
	}
}

func TestHashToken(t *testing.T) {
	first := HashToken("rl_example")
	if len(first) != 64 || first != HashToken("rl_example") {
		t.Errorf("hash token harus SHA-256 hex yang stabil: %q", first)
	}
	if first == HashToken("rl_other") {
		t.Error("token berbeda harus menghasilkan hash berbeda")
	}
	if first == "rl_example" {
		t.Error("hash tidak boleh sama dengan token")
	}
}
