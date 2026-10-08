package triage

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"

	"redline/internal/report"
)

// Jenis insiden: kegagalan dari banyak test yang penyebabnya sama.
const (
	IncidentConnection = "connection" // target tidak bisa dihubungi
	IncidentHTTP       = "http"       // endpoint yang sama membalas 5xx
	IncidentError      = "error"      // pesan error ternormalisasi sama persis
)

var (
	connCode = regexp.MustCompile(`(?i)\b(?:getaddrinfo\s+)?(ECONNREFUSED|ENOTFOUND|EAI_AGAIN|ECONNRESET|ETIMEDOUT|socket hang up|net::ERR_[A-Z_]+)\b`)
	// Node mencoba IPv6 dan IPv4 bergantian untuk localhost; samakan supaya tetap satu insiden.
	loopback = regexp.MustCompile(`^(?:\[?::1\]?|127\.0\.0\.1)(:|$)`)
)

// IncidentKey mengelompokkan kegagalan lintas test. normalizedErr adalah hasil Normalize.
// Hanya 5xx yang dipakai dari panggilan HTTP: 4xx bisa jadi memang diharapkan oleh test negatif.
func IncidentKey(normalizedErr string, calls []report.HTTPCall) (key, kind, label string) {
	if m := connCode.FindStringSubmatchIndex(normalizedErr); m != nil {
		label = "Tidak bisa terhubung: " + normalizedErr[m[2]:m[3]]
		// Alamat tujuan adalah kata setelah kodenya: "ECONNREFUSED ::1:9", "ENOTFOUND api.example.com".
		if f := strings.Fields(firstLine(normalizedErr[m[1]:])); len(f) > 0 {
			label += " " + loopback.ReplaceAllString(strings.Trim(f[0], ".,;'\"()"), "localhost$1")
		}
		return incidentHash(IncidentConnection, label), IncidentConnection, label
	}
	for _, c := range calls {
		if c.Status >= 500 {
			label = fmt.Sprintf("%s %s → %d", c.Method, c.Path, c.Status)
			return incidentHash(IncidentHTTP, label), IncidentHTTP, label
		}
	}
	label = firstLine(normalizedErr)
	if r := []rune(label); len(r) > 120 {
		label = string(r[:120]) + "…"
	}
	return incidentHash(IncidentError, normalizedErr), IncidentError, label
}

func incidentHash(kind, s string) string {
	sum := sha256.Sum256([]byte(kind + "\x00" + s))
	return hex.EncodeToString(sum[:8])
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
