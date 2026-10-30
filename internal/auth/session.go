// Package auth provides the UI session cookie and the shared API key check.
//
// The cookie is signed with a key derived from REDLINE_API_KEY, so restarting
// the server keeps sessions valid while rotating the key invalidates them. An
// empty REDLINE_API_KEY means the server is open (loopback only, by config).
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// CookieName is the HttpOnly session cookie for the UI.
const CookieName = "redline_session"

const sessionTTL = 12 * time.Hour

type Sessions struct {
	apiKey string
	key    []byte
	now    func() time.Time
}

func New(apiKey string) *Sessions {
	s := &Sessions{apiKey: apiKey, now: time.Now}
	if apiKey != "" {
		sum := sha256.Sum256([]byte("redline-ui-session|" + apiKey))
		s.key = sum[:]
	}
	return s
}

// Enabled reports whether a key is configured. When it is not, the UI and API
// are open (the server refuses to bind a non-loopback host without a key).
func (s *Sessions) Enabled() bool { return len(s.key) > 0 }

// CheckKey compares the submitted key in constant time.
func (s *Sessions) CheckKey(key string) bool {
	if !s.Enabled() {
		return true
	}
	return subtle.ConstantTimeCompare([]byte(key), []byte(s.apiKey)) == 1
}

// Valid reports whether the request carries a valid, unexpired session cookie.
func (s *Sessions) Valid(r *http.Request) bool {
	if !s.Enabled() {
		return true
	}
	cookie, err := r.Cookie(CookieName)
	if err != nil {
		return false
	}
	payload, signature, ok := strings.Cut(cookie.Value, ".")
	if !ok {
		return false
	}
	if subtle.ConstantTimeCompare([]byte(signature), []byte(s.sign(payload))) != 1 {
		return false
	}
	expires, err := strconv.ParseInt(strings.TrimPrefix(payload, "v1|"), 10, 64)
	if err != nil {
		return false
	}
	return s.now().Unix() < expires
}

// SetCookie issues a 12-hour session. Path is / so the API can accept the same
// cookie for GET requests; it is HttpOnly and SameSite=Lax.
func (s *Sessions) SetCookie(w http.ResponseWriter, r *http.Request) {
	if !s.Enabled() {
		return
	}
	expires := s.now().Add(sessionTTL)
	payload := fmt.Sprintf("v1|%d", expires.Unix())
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    payload + "." + s.sign(payload),
		Path:     "/",
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteLaxMode,
		Expires:  expires,
		MaxAge:   int(sessionTTL.Seconds()),
	})
}

func (s *Sessions) ClearCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Unix(1, 0),
		MaxAge:   -1,
	})
}

func (s *Sessions) sign(payload string) string {
	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// NextPath keeps a login redirect inside /ui/ and falls back to the dashboard.
func NextPath(raw string) string {
	if raw == "" || !strings.HasPrefix(raw, "/ui/") || strings.HasPrefix(raw, "//") {
		return "/ui/"
	}
	return raw
}
