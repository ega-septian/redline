package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"regexp"

	"golang.org/x/crypto/bcrypt"
)

const passwordCost = 12

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// ValidUsername accepts the same shape as project names: letters, digits, dot, underscore and dash.
func ValidUsername(value string) bool { return usernamePattern.MatchString(value) }

// HashPassword hashes a password with bcrypt. Bcrypt only considers the first 72 bytes, so longer
// passwords are rejected instead of being silently truncated.
func HashPassword(password string) (string, error) {
	if len(password) < 12 {
		return "", errors.New("password must be at least 12 characters")
	}
	if len(password) > 72 {
		return "", errors.New("password must be at most 72 bytes")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), passwordCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

// VerifyPassword compares a password with a stored bcrypt hash in constant time.
func VerifyPassword(hash, password string) bool {
	if hash == "" || password == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// NewToken returns a URL-safe random token.
func NewToken(bytes int) (string, error) {
	if bytes <= 0 {
		bytes = 32
	}
	buf := make([]byte, bytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// TokenEqual compares two secrets without leaking where they differ.
func TokenEqual(left, right string) bool {
	return subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}
