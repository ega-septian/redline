package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"redline/internal/auth"
	"redline/internal/store"
)

func keyServer(fs *fakeStore) (*Server, http.Handler) {
	srv := &Server{
		Store: fs, Analyzer: &fakeAnalyzer{}, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Session: func(r *http.Request) bool { return r.Header.Get("X-Session") == "1" },
		CSRF:    func(r *http.Request) bool { return r.Header.Get("X-CSRF-Token") == "token" },
		User:    func(*http.Request) string { return "ega" },
	}
	return srv, srv.Routes()
}

func keyRequest(method, path, body string, session, csrf bool) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if session {
		req.Header.Set("X-Session", "1")
	}
	if csrf {
		req.Header.Set("X-CSRF-Token", "token")
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}

func TestAPIKeysRequireUISession(t *testing.T) {
	_, h := keyServer(&fakeStore{})

	if rec := do(h, http.MethodGet, "/api/keys", nil); rec.Code != http.StatusForbidden {
		t.Errorf("list tanpa session = %d, want 403", rec.Code)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, keyRequest(http.MethodGet, "/api/keys", "", true, false))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "[]") {
		t.Errorf("list dengan session = %d: %s", rec.Code, rec.Body)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, keyRequest(http.MethodPost, "/api/keys", `{"name":"ci"}`, true, false))
	if rec.Code != http.StatusForbidden {
		t.Errorf("create tanpa CSRF = %d, want 403", rec.Code)
	}
}

func TestAPIKeysCannotManageKeysWithABearerKey(t *testing.T) {
	srv, h := keyServer(&fakeStore{})
	srv.AuthEnabled = true
	srv.ResolveKey = func(_ context.Context, token string) (string, bool) {
		return "machine", token == "machine-key"
	}
	req := httptest.NewRequest(http.MethodGet, "/api/keys", nil)
	req.Header.Set("Authorization", "Bearer machine-key")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("bearer key management = %d, want 403", rec.Code)
	}
}

func TestCreateAndRevokeAPIKey(t *testing.T) {
	fs := &fakeStore{}
	_, h := keyServer(fs)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, keyRequest(http.MethodPost, "/api/keys", `{"name":"coba2-ci"}`, true, true))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", rec.Code, rec.Body)
	}
	var created struct {
		Key    string       `json:"key"`
		APIKey store.APIKey `json:"api_key"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(created.Key, "rl_") || len(created.Key) < 20 {
		t.Errorf("secret key tidak valid: %q", created.Key)
	}
	if created.APIKey.Name != "coba2-ci" || created.APIKey.CreatedBy != "ega" {
		t.Errorf("api_key = %+v", created.APIKey)
	}
	if created.APIKey.Hash != "" {
		t.Errorf("hash tidak boleh dikirim ke client: %+v", created.APIKey)
	}
	if len(fs.apiKeys) != 1 || fs.apiKeys[0].Hash != auth.HashToken(created.Key) ||
		fs.apiKeys[0].Hash == created.Key || fs.apiKeys[0].Prefix != created.Key[:9] {
		t.Errorf("hash/prefix tidak tersimpan benar: %+v", fs.apiKeys)
	}
	if strings.Contains(rec.Body.String(), `"hash"`) {
		t.Errorf("response memuat hash: %s", rec.Body)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, keyRequest(http.MethodPost, "/api/keys", `{"name":"coba2-ci"}`, true, true))
	if rec.Code != http.StatusConflict {
		t.Errorf("nama duplikat = %d, want 409: %s", rec.Code, rec.Body)
	}

	for _, body := range []string{`{"name":""}`, `{"name":"bad name"}`, `{"name":"` + strings.Repeat("a", 65) + `"}`} {
		rec = httptest.NewRecorder()
		h.ServeHTTP(rec, keyRequest(http.MethodPost, "/api/keys", body, true, true))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("nama %q = %d, want 400", body, rec.Code)
		}
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, keyRequest(http.MethodDelete, "/api/keys/"+created.APIKey.ID, "", true, true))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"revoked_at"`) {
		t.Fatalf("revoke = %d: %s", rec.Code, rec.Body)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, keyRequest(http.MethodDelete, "/api/keys/"+created.APIKey.ID, "", true, true))
	if rec.Code != http.StatusOK {
		t.Errorf("revoke kedua harus idempoten = %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, keyRequest(http.MethodDelete, "/api/keys/missing", "", true, true))
	if rec.Code != http.StatusNotFound {
		t.Errorf("revoke tidak ada = %d, want 404", rec.Code)
	}
}

func TestFirstConfigKeyEnablesAPIAuth(t *testing.T) {
	fs := &fakeStore{}
	token := ""
	srv := &Server{
		Store: fs, Analyzer: &fakeAnalyzer{}, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Session: func(r *http.Request) bool { return r.Header.Get("X-Session") == "1" },
		CSRF:    func(r *http.Request) bool { return r.Header.Get("X-CSRF-Token") == "token" },
		User:    func(r *http.Request) string { return KeyName(r) },
		ResolveKey: func(_ context.Context, candidate string) (string, bool) {
			return "coba2-ci", token != "" && candidate == token
		},
	}
	h := srv.Routes()

	if rec := do(h, http.MethodGet, "/api/runs", nil); rec.Code != http.StatusOK {
		t.Fatalf("API harus terbuka sebelum key pertama: %d", rec.Code)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, keyRequest(http.MethodPost, "/api/keys", `{"name":"coba2-ci"}`, true, true))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", rec.Code, rec.Body)
	}
	var created struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	token = created.Key
	if rec := do(h, http.MethodGet, "/api/runs", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("API harus tertutup setelah key pertama: %d", rec.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/runs", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	ok := httptest.NewRecorder()
	h.ServeHTTP(ok, req)
	if ok.Code != http.StatusOK {
		t.Fatalf("bearer key = %d: %s", ok.Code, ok.Body)
	}

	label := httptest.NewRequest(http.MethodPut, "/api/groups/ada/label",
		strings.NewReader(`{"label":"test_bug","note":"n","by":"spoofed"}`))
	label.Header.Set("Authorization", "Bearer "+token)
	labelRec := httptest.NewRecorder()
	h.ServeHTTP(labelRec, label)
	if labelRec.Code != http.StatusOK {
		t.Fatalf("label dengan key = %d: %s", labelRec.Code, labelRec.Body)
	}
	if fs.label != "test_bug|n|coba2-ci" {
		t.Errorf("label harus memakai nama key: %q", fs.label)
	}
}
