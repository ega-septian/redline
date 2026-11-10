package api

import (
	"errors"
	"net/http"
	"regexp"
	"strings"

	"redline/internal/auth"
	"redline/internal/store"
)

var apiKeyName = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// requireKeyAdmin limits key management to a signed-in UI user. Bearer keys cannot create more keys,
// and writes need the same CSRF token as other cookie-authenticated writes.
func (s *Server) requireKeyAdmin(w http.ResponseWriter, r *http.Request) bool {
	if s.Session == nil || !s.Session(r) {
		writeError(w, http.StatusForbidden, "API key management requires a UI login")
		return false
	}
	if r.Method == http.MethodGet {
		return true
	}
	if s.CSRF == nil || !s.CSRF(r) {
		writeError(w, http.StatusForbidden, "CSRF token salah atau tidak ada: kirim header X-CSRF-Token")
		return false
	}
	return true
}

// handleListAPIKeys: GET /api/keys — active and revoked keys, without their secrets.
func (s *Server) handleListAPIKeys(w http.ResponseWriter, r *http.Request) {
	if !s.requireKeyAdmin(w, r) {
		return
	}
	keys, err := s.Store.ListAPIKeys(r.Context())
	if err != nil {
		s.serverError(w, err)
		return
	}
	if keys == nil {
		keys = []store.APIKey{}
	}
	writeJSON(w, http.StatusOK, keys)
}

// handleCreateAPIKey: POST /api/keys — create a key and return the secret once.
func (s *Server) handleCreateAPIKey(w http.ResponseWriter, r *http.Request) {
	if !s.requireKeyAdmin(w, r) {
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "body harus JSON: {\"name\": \"coba2-ci\"}")
		return
	}
	name := strings.TrimSpace(body.Name)
	if !apiKeyName.MatchString(name) {
		writeError(w, http.StatusBadRequest, "name hanya boleh huruf, angka, titik, underscore atau strip (maks 64)")
		return
	}

	secret, err := auth.NewToken(32)
	if err != nil {
		s.serverError(w, err)
		return
	}
	key := "rl_" + secret
	created, err := s.Store.CreateAPIKey(r.Context(), store.APIKey{
		Name: name, Prefix: key[:9], Hash: auth.HashToken(key), CreatedBy: s.actor(r, ""),
	})
	if errors.Is(err, store.ErrAPIKeyExists) {
		writeError(w, http.StatusConflict, "nama API key sudah dipakai (key yang dicabut tetap menyimpan namanya)")
		return
	}
	if err != nil {
		s.serverError(w, err)
		return
	}
	s.EnableKeyAuth()
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, map[string]any{"key": key, "api_key": created})
}

// handleRevokeAPIKey: DELETE /api/keys/{id} — keep the name for audit, reject future requests.
func (s *Server) handleRevokeAPIKey(w http.ResponseWriter, r *http.Request) {
	if !s.requireKeyAdmin(w, r) {
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "id API key wajib diisi")
		return
	}
	key, err := s.Store.RevokeAPIKey(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "API key tidak ditemukan")
		return
	}
	if err != nil {
		s.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, key)
}
