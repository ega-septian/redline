// Package analysis menentukan penyebab kegagalan: aturan deterministik dulu,
// AI hanya kalau aturan tidak cukup. Hasilnya di-cache per (fingerprint, versi prompt).
package analysis

import (
	"fmt"
	"regexp"
	"strings"
	"sync"

	"redline/internal/store"
	"redline/internal/triage"
)

var (
	connErr  = regexp.MustCompile(`(?i)\b(ECONNREFUSED|ENOTFOUND|EAI_AGAIN|ECONNRESET|ETIMEDOUT|getaddrinfo|socket hang up|net::ERR_[A-Z_]+)\b[^\n]*`)
	expected = regexp.MustCompile(`Expected: (2\d\d)\b`)
	received = regexp.MustCompile(`Received: (5\d\d)\b`)
	// Nilai yang diperiksa ternyata Promise: hampir selalu karena lupa await (misalnya response.json()).
	promise = regexp.MustCompile(`(?i)[^\n]*(received:?\s+Promise\b|\[object Promise\])[^\n]*`)
)

// ApplyRules mengembalikan hasil kalau penyebabnya jelas tanpa AI, atau nil.
// Aturan sengaja sedikit dan ketat: lebih baik diserahkan ke AI daripada salah yakin.
// Urutan: aturan bawaan yang spesifik, matriks "test tetap + API berubah" (bukti kuat bug backend),
// aturan yang dipelajari, lalu sisa matriks perubahan. Aturan yang dipelajari hanya melihat teks error,
// jadi tidak boleh mengalahkan bukti bahwa API-lah yang berubah.
func ApplyRules(f *store.Facts, learned ...store.LearnedRule) *store.Analysis {
	if res := builtinRules(f.ErrorMessage); res != nil {
		return res
	}
	matrix := matrixRules(f)
	if matrix != nil && matrix.Category == "backend_bug" {
		return matrix
	}
	if res := learnedRules(f.ErrorMessage, learned); res != nil {
		return res
	}
	return matrix
}

// builtinRules hanya melihat pesan error.
func builtinRules(msg string) *store.Analysis {

	if m := connErr.FindString(msg); m != "" {
		return &store.Analysis{
			Source:     "rule",
			Category:   "environment",
			Confidence: "high",
			Summary:    "Target tidak bisa dihubungi, jadi test belum sempat menguji perilaku API atau aplikasi.",
			Evidence:   []string{m},
			NextStep:   "Pastikan service target jalan (misalnya `docker ps`) dan baseURL di playwright.config.ts benar, lalu jalankan ulang.",
		}
	}

	if e, r := expected.FindStringSubmatch(msg), received.FindStringSubmatch(msg); e != nil && r != nil {
		return &store.Analysis{
			Source:     "rule",
			Category:   "backend_bug",
			Confidence: "high",
			Summary: fmt.Sprintf("API membalas %s padahal test mengharapkan %s. Kode 5xx berarti server gagal memproses request. "+
				"Walaupun data dari test ikut memicu, server seharusnya membalas 4xx dengan pesan validasi, bukan 5xx.", r[1], e[1]),
			Evidence: []string{e[0], r[0]},
			NextStep: "Cek log backend pada waktu run ini (misalnya `docker logs <container-api>`) untuk melihat exception-nya, lalu laporkan ke tim backend beserta request-nya.",
		}
	}

	if m := promise.FindString(msg); m != "" {
		return &store.Analysis{
			Source:     "rule",
			Category:   "test_bug",
			Confidence: "high",
			Summary: "Test memeriksa sebuah Promise, bukan hasilnya. Hampir pasti ada `await` yang hilang, " +
				"misalnya `response.json()` tanpa `await` atau `test.step` yang tidak di-await.",
			Evidence: []string{strings.TrimSpace(m)},
			NextStep: "Tambahkan `await` di pemanggilan async yang nilainya diperiksa (contoh: `parse(await response.json())`). " +
				"Jalankan `npm run lint`: aturan playwright/missing-playwright-await menangkap `test.step` atau `expect` yang lupa di-await.",
		}
	}

	return nil
}

// learnedRules mencoba aturan yang sudah disetujui, berurutan. Pola yang tidak valid dilewati.
func learnedRules(msg string, rules []store.LearnedRule) *store.Analysis {
	for _, r := range rules {
		re := compileCached(r.Pattern)
		if re == nil {
			continue
		}
		if m := re.FindString(msg); m != "" {
			return &store.Analysis{
				Source:     "rule",
				Category:   r.Category,
				Confidence: "high",
				Summary:    r.Summary,
				Evidence: []string{strings.TrimSpace(firstLineOf(m)),
					fmt.Sprintf("Aturan #%d, dipelajari dari %d kasus terbukti", r.ID, len(r.LearnedFrom))},
				NextStep: r.NextStep,
			}
		}
	}
	return nil
}

// matrixRules: hanya kalau ada pembanding (run lulus terakhir) dan kedua sinyal diketahui.
func matrixRules(f *store.Facts) *store.Analysis {
	if f.LastPass == nil {
		return nil
	}
	switch {
	case f.TestChanged == "tidak" && f.Response.Verdict == triage.ResponseChanged:
		return &store.Analysis{
			Source:     "rule",
			Category:   "backend_bug",
			Confidence: "high",
			Summary: fmt.Sprintf("Kode test sama persis dengan saat terakhir lulus (run #%d), tetapi bentuk response API berubah: %s. "+
				"Perubahan datang dari sisi API.", f.LastPass.RunID, f.Response.Lines[0]),
			Evidence: append([]string{"Kode test berubah: tidak"}, first(f.Response.Lines, 3)...),
			NextStep: "Konfirmasi ke tim backend apakah perubahan kontrak ini disengaja. Kalau disengaja, update test dan schema; kalau tidak, laporkan sebagai bug.",
		}
	case f.TestChanged == "ya" && (f.Response.Verdict == triage.ResponseSame || f.Response.Verdict == triage.ResponseAddedOnly):
		return &store.Analysis{
			Source:     "rule",
			Category:   "test_bug",
			Confidence: "medium",
			Summary: fmt.Sprintf("Bentuk response API sama dengan saat terakhir lulus (run #%d), sedangkan kode test berubah. "+
				"Kemungkinan besar perubahan di test yang membuatnya gagal.", f.LastPass.RunID),
			Evidence: []string{"Kode test berubah: ya", "Response API: " + f.Response.Verdict},
			NextStep: "Bandingkan perubahan test terakhir (git diff). Kalau assertion baru memang sengaja dibuat untuk menangkap bug lama, ganti label kelompok ini menjadi backend_bug.",
		}
	}
	return nil
}

var compiled sync.Map // pola -> *regexp.Regexp (nil kalau tidak valid)

func compileCached(pattern string) *regexp.Regexp {
	if v, ok := compiled.Load(pattern); ok {
		return v.(*regexp.Regexp)
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		re = nil
	}
	compiled.Store(pattern, re)
	return re
}

func firstLineOf(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func first(lines []string, n int) []string {
	if len(lines) < n {
		return lines
	}
	return lines[:n]
}
