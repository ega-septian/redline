// Package analysis menentukan penyebab kegagalan: aturan deterministik dulu,
// AI hanya kalau aturan tidak cukup. Hasilnya di-cache per (fingerprint, versi prompt).
package analysis

import (
	"fmt"
	"regexp"

	"redline/internal/store"
	"redline/internal/triage"
)

var (
	connErr  = regexp.MustCompile(`(?i)\b(ECONNREFUSED|ENOTFOUND|EAI_AGAIN|ECONNRESET|ETIMEDOUT|getaddrinfo|socket hang up|net::ERR_[A-Z_]+)\b[^\n]*`)
	expected = regexp.MustCompile(`Expected: (2\d\d)\b`)
	received = regexp.MustCompile(`Received: (5\d\d)\b`)
)

// ApplyRules mengembalikan hasil kalau penyebabnya jelas tanpa AI, atau nil.
// Aturan sengaja sedikit dan ketat: lebih baik diserahkan ke AI daripada salah yakin.
func ApplyRules(f *store.Facts) *store.Analysis {
	msg := f.ErrorMessage

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

	// Matriks perubahan: hanya kalau ada pembanding (run lulus terakhir) dan kedua sinyal diketahui.
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

func first(lines []string, n int) []string {
	if len(lines) < n {
		return lines
	}
	return lines[:n]
}
