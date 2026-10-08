// Package triage berisi aturan deterministik Redline: redaksi data sensitif,
// normalisasi pesan error, dan fingerprint untuk mengelompokkan kegagalan yang sama.
package triage

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

var (
	ansi = regexp.MustCompile(`\x1b\[[0-9;]*m`)

	// Redaksi: dijalankan sebelum apa pun disimpan ke database.
	redactRules = []struct {
		re   *regexp.Regexp
		repl string
	}{
		{regexp.MustCompile(`eyJ[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]*`), "[JWT]"},
		{regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9\-._~+/]+=*`), "$1 [REDACTED]"},
		{regexp.MustCompile(`(?i)("?(?:authorization|x-api-key|api[_-]?key|access[_-]?token|refresh[_-]?token|token|secret|password|passwd)"?\s*[:=]\s*)((?i:bearer|basic)\s+\S+|"[^"]*"|'[^']*'|[^\s,}]+)`), `$1"[REDACTED]"`},
		{regexp.MustCompile(`\b\d{16}\b`), "[NIK/16-DIGIT]"},
	}

	// Normalisasi: mengganti bagian yang berubah-ubah tiap run supaya
	// kegagalan yang sama menghasilkan fingerprint yang sama.
	normalizeRules = []struct {
		re   *regexp.Regexp
		repl string
	}{
		{regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`), "<uuid>"},
		{regexp.MustCompile(`\b[0-9A-HJKMNP-TV-Z]{26}\b`), "<ulid>"},
		{regexp.MustCompile(`\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:?\d{2})?`), "<time>"},
		{regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`), "<email>"},
		{regexp.MustCompile(`(?i)\b[0-9a-f]{16,}\b`), "<hex>"},
		{regexp.MustCompile(`\b\d{5,}\b`), "<n>"}, // id/timestamp panjang; kode status 3 digit tetap
		{regexp.MustCompile(`\b\d+(\.\d+)?ms\b`), "<n>ms"},
	}
	spaces = regexp.MustCompile(`[ \t]+`)
)

// StripANSI membuang kode warna terminal dari pesan Playwright.
func StripANSI(s string) string { return ansi.ReplaceAllString(s, "") }

// Redact menyamarkan token, password, JWT, dan angka 16 digit (NIK/kartu).
func Redact(s string) string {
	for _, r := range redactRules {
		s = r.re.ReplaceAllString(s, r.repl)
	}
	return s
}

// Clean = StripANSI + Redact. Inilah versi pesan yang boleh disimpan.
func Clean(s string) string { return Redact(StripANSI(s)) }

// Normalize mengubah pesan error (yang sudah Clean) menjadi bentuk stabil untuk fingerprint.
func Normalize(msg string) string {
	// "Call log:" berisi detail langkah yang berubah-ubah; bukan inti error.
	if i := strings.Index(msg, "\nCall log:"); i >= 0 {
		msg = msg[:i]
	}
	for _, r := range normalizeRules {
		msg = r.re.ReplaceAllString(msg, r.repl)
	}
	var lines []string
	for _, line := range strings.Split(msg, "\n") {
		line = strings.TrimSpace(spaces.ReplaceAllString(line, " "))
		if line != "" {
			lines = append(lines, line)
		}
	}
	out := strings.Join(lines, "\n")
	if len(out) > 2000 {
		out = out[:2000]
	}
	return out
}

// Fingerprint menggabungkan identitas test dan error yang sudah dinormalisasi.
// Test yang sama dengan error yang sama selalu menghasilkan fingerprint yang sama.
// Nomor baris tidak ikut, supaya menambah baris di file test tidak membuat grup baru.
func Fingerprint(testKey, normalizedError string) string {
	sum := sha256.Sum256([]byte(testKey + "\x00" + normalizedError))
	return hex.EncodeToString(sum[:8])
}
