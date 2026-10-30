// Package auth provides the UI session cookie and the shared API key check.
//
// The cookie is signed with a key derived from REDLINE_API_KEY, so restarting
// the server keeps sessions valid while rotating the key invalidates them. An
// empty REDLINE_API_KEY means the server is open (loopback only, by config).
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// CookieName is the HttpOnly session cookie for the UI.
const CookieName = "redline_session"

// CSRFCookieName is readable by JavaScript and paired with the X-CSRF-Token
// header for cookie-authenticated writes (double submit).
const CSRFCookieName = "redline_csrf"

const sessionTTL = 12 * time.Hour

type Sessions struct {
	apiKey string
	users  map[string]string // name -> key
	key    []byte
	now    func() time.Time
}

// New builds the session signer. users maps a display name to its key; the
// legacy single REDLINE_API_KEY is accepted as user "admin". An empty key and
// no users means the server is open (loopback only, by config).
func New(apiKey string, users map[string]string) *Sessions {
	s := &Sessions{apiKey: apiKey, users: users, now: time.Now}
	material := apiKey
	names := make([]string, 0, len(users))
	for name := range users {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		material += "|" + name + "=" + users[name]
	}
	if material != "" {
		sum := sha256.Sum256([]byte("redline-ui-session|" + material))
		s.key = sum[:]
	}
	return s
}

// Enabled reports whether a key is configured. When it is not, the UI and API
// are open (the server refuses to bind a non-loopback host without a key).
func (s *Sessions) Enabled() bool { return len(s.key) > 0 }

// CheckKey returns the user for a submitted key. Comparisons are constant time.
func (s *Sessions) CheckKey(key string) (string, bool) {
	if !s.Enabled() {
		return "local", true
	}
	for name, value := range s.users {
		if subtle.ConstantTimeCompare([]byte(key), []byte(value)) == 1 {
			return name, true
		}
	}
	if s.apiKey != "" && subtle.ConstantTimeCompare([]byte(key), []byte(s.apiKey)) == 1 {
		return "admin", true
	}
	return "", false
}

// ValidKey reports whether a Bearer token is accepted.
func (s *Sessions) ValidKey(key string) bool {
	_, ok := s.CheckKey(key)
	return ok
}

// Valid reports whether the request carries a valid, unexpired session cookie.
func (s *Sessions) Valid(r *http.Request) bool {
	_, ok := s.parse(r)
	return ok
}

// User returns the session's user, or "" when the cookie is missing/invalid.
func (s *Sessions) User(r *http.Request) string {
	user, _ := s.parse(r)
	return user
}

// parse validates the cookie and returns the embedded user.
func (s *Sessions) parse(r *http.Request) (string, bool) {
	if !s.Enabled() {
		return "local", true
	}
	cookie, err := r.Cookie(CookieName)
	if err != nil {
		return "", false
	}
	payload, signature, ok := strings.Cut(cookie.Value, ".")
	if !ok {
		return "", false
	}
	if subtle.ConstantTimeCompare([]byte(signature), []byte(s.sign(payload))) != 1 {
		return "", false
	}
	parts := strings.Split(payload, "|")
	if len(parts) != 3 || parts[0] != "v1" {
		return "", false
	}
	expires, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || s.now().Unix() >= expires {
		return "", false
	}
	return parts[2], true
}

// SetCookie issues a 12-hour session. Path is / so the API can accept the same
// cookie for GET requests; it is HttpOnly and SameSite=Lax.
func (s *Sessions) SetCookie(w http.ResponseWriter, r *http.Request, user string) {
	if !s.Enabled() {
		return
	}
	expires := s.now().Add(sessionTTL)
	payload := fmt.Sprintf("v1|%d|%s", expires.Unix(), user)
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
	s.setCSRFCookie(w, r, expires, newCSRFToken())
}

// EnsureCSRF issues the readable CSRF cookie for a valid session that predates
// the CSRF upgrade (or lost the cookie).
func (s *Sessions) EnsureCSRF(w http.ResponseWriter, r *http.Request) {
	if !s.Enabled() {
		return
	}
	if cookie, err := r.Cookie(CSRFCookieName); err == nil && cookie.Value != "" {
		return
	}
	s.setCSRFCookie(w, r, s.now().Add(sessionTTL), newCSRFToken())
}

func (s *Sessions) setCSRFCookie(w http.ResponseWriter, r *http.Request, expires time.Time, value string) {
	http.SetCookie(w, &http.Cookie{
		Name:     CSRFCookieName,
		Value:    value,
		Path:     "/",
		HttpOnly: false,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteLaxMode,
		Expires:  expires,
		MaxAge:   int(sessionTTL.Seconds()),
	})
}

// CSRFValid checks the double-submit token for a cookie-authenticated write.
func (s *Sessions) CSRFValid(r *http.Request) bool {
	if !s.Enabled() {
		return true
	}
	cookie, err := r.Cookie(CSRFCookieName)
	if err != nil || cookie.Value == "" {
		return false
	}
	header := r.Header.Get("X-CSRF-Token")
	return subtle.ConstantTimeCompare([]byte(header), []byte(cookie.Value)) == 1
}

func (s *Sessions) ClearCookie(w http.ResponseWriter, r *http.Request) {
	for _, name := range []string{CookieName, CSRFCookieName} {
		http.SetCookie(w, &http.Cookie{
			Name:     name,
			Value:    "",
			Path:     "/",
			HttpOnly: name == CookieName,
			Secure:   isHTTPS(r),
			SameSite: http.SameSiteLaxMode,
			Expires:  time.Unix(1, 0),
			MaxAge:   -1,
		})
	}
}

func (s *Sessions) sign(payload string) string {
	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func newCSRFToken() string {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(buf)
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
