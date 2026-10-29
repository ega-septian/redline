package triage

import (
	"strings"
	"testing"
)

func TestRedact(t *testing.T) {
	cases := map[string]string{
		"Authorization: Bearer abc.def-123":                  "Authorization: \"[REDACTED]\"",
		`{"Authorization":"Bearer xyz"}`:                     `{"Authorization":"[REDACTED]"}`,
		`headers: { authorization: 'Bearer 1a2b3c' }`:        `headers: { authorization: "[REDACTED]" }`,
		`{"password": "rahasia123", "email": "a@b.co"}`:      `{"password": "[REDACTED]", "email": "<email>"}`,
		"hubungi +6281234567890 atau 081298765432":           "hubungi +<n> atau <n>",
		"email budi.santoso+qa@example.co.id terdaftar":      "email <email> terdaftar",
		"token=eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.sig_abc": `token="[REDACTED]"`,
		"jwt eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.sig_abc":   "jwt [JWT]",
		"nik 3275012345678901 tidak valid":                   "nik [NIK/16-DIGIT] tidak valid",
		"Expected: 201\nReceived: 500":                       "Expected: 201\nReceived: 500",
	}
	for in, want := range cases {
		if got := Redact(in); got != want {
			t.Errorf("Redact(%q)\n got  %q\n want %q", in, got, want)
		}
	}
}

func TestNormalize_IgnoresVolatileParts(t *testing.T) {
	a := Normalize(Clean("Error: \x1b[31mfailed\x1b[39m order 3f2b8c1e-1d2a-4b5c-9d8e-0a1b2c3d4e5f\n  id: 01JC2X5V7K9QYQ0W8S3T4M6N8P at 2026-10-08T08:54:20.214Z for budi@example.com in 1234ms\nCall log:\n  - step 1"))
	b := Normalize(Clean("Error: failed order 99999999-1d2a-4b5c-9d8e-0a1b2c3d4e5f\n    id: 01JC9ZZZZZZZZZZZZZZZZZZZZZ at 2026-10-09T01:00:00Z for siti@example.org in 87ms\nCall log:\n  - step 9 berbeda"))
	if a != b {
		t.Errorf("error yang sama harus sama setelah normalisasi:\n%s\n---\n%s", a, b)
	}
	if strings.Contains(a, "Call log") || strings.Contains(a, "\x1b") {
		t.Errorf("Call log dan ANSI harus dibuang: %q", a)
	}
}

func TestNormalize_KeepsStatusCodes(t *testing.T) {
	a := Normalize("Expected: 201\nReceived: 500")
	b := Normalize("Expected: 201\nReceived: 404")
	if a == b {
		t.Error("500 dan 404 adalah kegagalan berbeda, jangan disamakan")
	}
}

func TestFingerprint(t *testing.T) {
	f1 := Fingerprint("toolshop › a.spec.ts › x", "Expected: 201\nReceived: 500")
	if len(f1) != 16 || f1 != Fingerprint("toolshop › a.spec.ts › x", "Expected: 201\nReceived: 500") {
		t.Errorf("fingerprint harus 16 hex dan deterministik: %s", f1)
	}
	if f1 == Fingerprint("toolshop › a.spec.ts › y", "Expected: 201\nReceived: 500") {
		t.Error("test berbeda harus beda fingerprint")
	}
}

// Redaksi PII harus menghasilkan teks yang sama dengan pipeline lama (Clean tanpa redaksi PII lalu
// Normalize), supaya test yang sudah punya riwayat tidak memulai grup baru setelah upgrade.
func TestFingerprint_PIIRedactionKeepsHistory(t *testing.T) {
	cases := []string{
		"login gagal untuk budi@example.com",
		"hubungi +6281234567890",
		"hubungi 081298765432",
	}
	for _, msg := range cases {
		old := Fingerprint("t", Normalize(msg))
		now := Fingerprint("t", Normalize(Clean(msg)))
		if old != now {
			t.Errorf("%q: fingerprint berubah %s -> %s", msg, old, now)
		}
	}
}
