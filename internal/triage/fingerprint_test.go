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
		`{"password": "rahasia123", "email": "a@b.co"}`:      `{"password": "[REDACTED]", "email": "a@b.co"}`,
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
