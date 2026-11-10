package auth

import (
	"crypto/sha256"
	"encoding/hex"
)

// HashToken returns the hex SHA-256 of a bearer token. API keys are high-entropy random values, so
// a fast hash is enough; bcrypt is only needed for low-entropy human passwords.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
