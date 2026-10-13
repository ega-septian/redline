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
	// Nilai yang diperiksa ternyata objek response Playwright: lupa membaca body (.json() / .text()).
	apiResponse = regexp.MustCompile(`(?i)[^\n]*received:?\s+APIResponse\b[^\n]*`)
)

// ApplyRules mengembalikan hasil kalau penyebabnya jelas tanpa AI, atau nil. Hasil berkeyakinan di bawah
// high (matriks "kode test berubah") hanya dipakai kalau AI mati atau gagal; lihat Analyzer.decide.
// Aturan sengaja sedikit dan ketat: lebih baik diserahkan ke AI daripada salah yakin.
// Urutan: aturan bawaan yang spesifik, matriks "test tetap + API berubah" (bukti kuat bug backend), kontrak,
// pengetahuan graph (KAG), aturan yang dipelajari, lalu sisa matriks perubahan. Aturan yang dipelajari hanya melihat teks error,
// jadi tidak boleh mengalahkan bukti bahwa API-lah yang berubah.
func ApplyRules(f *store.Facts, learned ...store.LearnedRule) *store.Analysis {
	if res := builtinRules(f.ErrorMessage); res != nil {
		return res
	}
	matrix := matrixRules(f)
	if matrix != nil && matrix.Confidence == "high" {
		return matrix
	}
	if res := contractRule(f); res != nil {
		return res
	}
	if res := graphRules(f); res != nil {
		return res
	}
	if res := learnedRules(f.ErrorMessage, learned); res != nil {
		return res
	}
	return matrix
}

// contractRule: response yang melanggar kontrak saat test gagal (status tidak terdokumentasi, field hilang,
// tipe berbeda) adalah penyimpangan API, selama kode test tidak sedang berubah.
func contractRule(f *store.Facts) *store.Analysis {
	if len(f.ContractViolations) == 0 || f.TestChanged == "ya" {
		return nil
	}
	return &store.Analysis{
		Source:     "rule",
		Category:   "backend_bug",
		Confidence: "high",
		Summary: fmt.Sprintf("Response API tidak sesuai kontrak (OpenAPI) yang berlaku saat test gagal: %s.",
			f.ContractViolations[0]),
		Evidence: first(f.ContractViolations, 3),
		NextStep: "Laporkan ke tim backend beserta bagian kontraknya. Kalau ternyata kontraknya yang usang, minta dokumentasi diperbarui.",
	}
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

	if m := apiResponse.FindString(msg); m != "" {
		return &store.Analysis{
			Source:     "rule",
			Category:   "test_bug",
			Confidence: "high",
			Summary: "Test memeriksa objek response Playwright (APIResponse), bukan isi body-nya. " +
				"Kemungkinan besar lupa membaca body dengan `.json()` atau `.text()`.",
			Evidence: []string{strings.TrimSpace(m)},
			NextStep: "Baca body dulu sebelum divalidasi, misalnya `Schema.parse(await response.json())`.",
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
	case f.TestChanged == "tidak" && f.Response.Verdict == triage.ResponseChanged && f.ContractChanged == "ya" &&
		len(f.ContractViolations) == 0:
		// Dimensi ketiga: kontrak ikut berubah dan response mengikuti kontrak baru, jadi perubahannya disengaja.
		return &store.Analysis{
			Source:     "rule",
			Category:   "test_bug",
			Confidence: "high",
			Summary: fmt.Sprintf("Kontrak API untuk endpoint ini berubah sejak run lulus terakhir (#%d), dan response sekarang "+
				"sesuai kontrak baru: %s. Perubahannya disengaja; test belum mengikuti kontrak baru.",
				f.LastPass.RunID, f.Response.Lines[0]),
			Evidence: append([]string{"Kode test berubah: tidak", "Kontrak API berubah: ya"}, first(f.Response.Lines, 2)...),
			NextStep: "Sesuaikan test dan schema dengan kontrak baru. Kalau perubahan kontrak ini tidak disengaja, laporkan ke tim backend.",
		}
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

// graphRules: aturan dari pengetahuan graph (KAG). Dijalankan setelah matriks yang yakin dan kontrak, jadi
// tidak pernah mengalahkan bukti bahwa response atau kontrak API berubah. Hanya dipakai kalau ada pembanding
// (run lulus terakhir yang juga punya graph import), supaya "file berubah" bisa dipercaya.
func graphRules(f *store.Facts) *store.Analysis {
	k := f.Knowledge
	if k == nil || f.LastPass == nil || !k.Compared {
		return nil
	}
	ownSpec := specOf(f.Group.TestKey)

	// 1. Endpoint yang sama gagal untuk test di spec lain yang TIDAK memakai kode yang berubah, dan tidak ada
	// yang lulus: masalahnya di endpoint, bukan di kode test.
	for _, e := range k.Endpoints {
		others := otherSpecs(e.Without.Failed, ownSpec)
		if len(others) == 0 || len(e.Without.Passed) > 0 {
			continue
		}
		return &store.Analysis{
			Source:     "rule",
			Category:   "backend_bug",
			Confidence: "high",
			Summary: fmt.Sprintf("%s juga gagal untuk %d test lain di spec berbeda yang tidak memakai kode yang berubah, "+
				"dan tidak ada test yang lulus di endpoint itu. Masalahnya di endpoint, bukan di kode test ini.",
				e.Key, len(e.Without.Failed)),
			Evidence: append([]string{e.Key + ": test lain tanpa kode yang berubah gagal"}, first(titles(others), 3)...),
			NextStep: "Periksa " + e.Key + " (response dan data) bersama tim backend; semua test yang memanggilnya ikut gagal.",
		}
	}

	changed := []store.FileFact{}
	for _, file := range k.Files {
		if file.Changed {
			changed = append(changed, file)
		}
	}
	if len(changed) == 0 {
		return nil
	}
	paths := []string{}
	for _, c := range changed {
		paths = append(paths, describeFile(c))
	}

	// 2. Endpoint yang belum pernah lulus, dan hanya dipanggil lewat kode yang berubah: kode test yang baru
	// memanggil endpoint itu (misalnya salah ketik path).
	for _, e := range k.Endpoints {
		if e.SeenPassing || len(e.Without.Passed)+len(e.Without.Failed) > 0 {
			continue
		}
		return &store.Analysis{
			Source:     "rule",
			Category:   "test_bug",
			Confidence: "high",
			Summary: fmt.Sprintf("Test memanggil %s, yang belum pernah dipanggil test yang lulus, dan hanya dipanggil lewat "+
				"kode yang berubah sejak run lulus terakhir (#%d): %s. Kemungkinan path atau method di kode test salah.",
				e.Key, f.LastPass.RunID, strings.Join(paths, ", ")),
			Evidence: append([]string{e.Key + ": belum pernah dipanggil test yang lulus"}, paths...),
			NextStep: "Bandingkan path di " + changed[0].Path + " dengan kontrak API atau path sebelum diubah.",
		}
	}

	// 3. Kode bersama berubah, bentuk response tetap, dan test lain yang memanggil endpoint yang sama tanpa
	// kode itu lulus: endpoint-nya sehat, yang berubah kode test.
	if f.Response.Verdict != triage.ResponseSame && f.Response.Verdict != triage.ResponseAddedOnly {
		return nil
	}
	var healthy *store.EndpointFact
	for i, e := range k.Endpoints {
		if len(e.Without.Failed) > 0 {
			return nil
		}
		if len(e.Without.Passed) > 0 && healthy == nil {
			healthy = &k.Endpoints[i]
		}
	}
	if healthy == nil {
		return nil
	}
	// Lebih yakin kalau test lain yang memakai file yang berubah ikut gagal.
	confidence := "medium"
	for _, c := range changed {
		if len(c.Others.Failed) > 0 {
			confidence = "high"
		}
	}
	return &store.Analysis{
		Source:     "rule",
		Category:   "test_bug",
		Confidence: confidence,
		Summary: fmt.Sprintf("Kode yang dipakai test ini berubah sejak run lulus terakhir (#%d): %s. Bentuk response API "+
			"tetap, dan %s lulus untuk test lain yang tidak memakai kode itu. Penyebabnya di kode yang berubah.",
			f.LastPass.RunID, strings.Join(paths, ", "), healthy.Key),
		Evidence: append(append([]string{}, paths...),
			fmt.Sprintf("%s: lulus untuk %s", healthy.Key, strings.Join(first(titles(healthy.Without.Passed), 2), ", ")),
			"Response API: "+f.Response.Verdict),
		NextStep: "Periksa perubahan di " + changed[0].Path + " (git diff); file itu dipakai juga oleh test lain.",
	}
}

// describeFile: "schemas/brand.ts (lewat import lain)".
func describeFile(f store.FileFact) string {
	if f.Direct {
		return f.Path + " berubah"
	}
	return f.Path + " berubah (lewat import lain)"
}

// specOf mengambil file spec dari kunci test "project › file › judul".
func specOf(testKey string) string {
	parts := strings.SplitN(testKey, " › ", 3)
	if len(parts) < 2 {
		return ""
	}
	return parts[1]
}

func otherSpecs(tests []string, spec string) []string {
	out := []string{}
	for _, t := range tests {
		if specOf(t) != spec {
			out = append(out, t)
		}
	}
	return out
}

// titles: kunci test -> "file › judul" (tanpa project), supaya singkat.
func titles(tests []string) []string {
	out := make([]string, len(tests))
	for i, t := range tests {
		if _, rest, ok := strings.Cut(t, " › "); ok {
			t = rest
		}
		out[i] = t
	}
	return out
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
