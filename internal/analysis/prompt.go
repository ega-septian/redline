package analysis

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"redline/internal/store"
)

// PromptVersion dinaikkan setiap kali prompt atau format fakta berubah,
// supaya cache lama tidak dipakai untuk prompt baru.
const PromptVersion = "v8"

const toolName = "report_verdict"

const systemPrompt = `Kamu membantu SDET menentukan penyebab test otomatis yang gagal.

Pilih satu kategori:
- backend_bug: perilaku API/aplikasi salah atau berubah (field hilang, nilai salah, status code salah, kontrak berubah).
- test_bug: kode test, data test, atau assertion yang salah atau sudah usang.
- environment: service mati, jaringan, konfigurasi, atau data awal (seed) yang tidak sesuai.
- flaky: hasil tidak konsisten karena timing, urutan, atau ketergantungan antar test.
- unknown: fakta tidak cukup untuk memutuskan.

Aturan:
1. Gunakan HANYA fakta di bagian FAKTA. Jangan mengarang endpoint, field, atau isi response yang tidak tertulis.
2. Setiap item "evidence" harus kutipan PERSIS (salin apa adanya) dari bagian FAKTA, maksimal 4 item.
3. Perhatikan matriks perubahan dibanding run terakhir yang lulus. "Kode test berubah" dihitung dari isi kode test,
   "Response API" dari bentuk response (field, tipe, status). Kode test tetap + response berubah condong ke backend_bug;
   kode test berubah + response tetap condong ke test_bug. Bentuk response yang sama tidak berarti nilainya sama:
   nilai yang berubah (misalnya status "PAID" jadi "PENDING") terlihat dari pesan error, bukan dari bentuk.
   Kalau sinyal "tidak diketahui", jangan berasumsi.
4. Field tambahan di response bukan breaking change. Field yang hilang atau tipe yang berubah adalah perubahan kontrak.
5. Kalau ragu, pilih kategori yang PALING MUNGKIN dengan confidence low, lalu sebutkan keraguannya di summary.
   Pilih unknown hanya kalau fakta benar-benar tidak menunjuk ke kategori mana pun.
   Salah yakin (confidence high padahal salah) lebih buruk daripada confidence low.
6. Tulis summary dan next_step dalam bahasa Indonesia, singkat (summary maksimal 3 kalimat).
7. Peta kode: kalau test lain yang memakai endpoint atau file yang sama LULUS, penyebab condong ke test ini sendiri.
   Kalau semuanya ikut GAGAL, condong ke bagian bersama: schema/helper bersama, atau backend.
8. Baca KODE TEST sebelum menyalahkan API. Bandingkan apa yang dikirim dan diharapkan test dengan response yang
   tercatat: path, method, payload, nama field, tipe di schema, status dan pesan yang diharapkan. Kalau test
   mengirim data yang tidak lengkap, memanggil path yang salah, atau mengharapkan sesuatu yang bertentangan dengan
   response wajar API, itu test_bug. Kalau kode test masuk akal dan response-lah yang menyimpang (field hilang,
   tipe salah, status tidak sesuai), itu backend_bug.
9. KASUS MIRIP hanya referensi dari masa lalu, bukan bukti. Jangan dikutip sebagai evidence. Pakai hanya kalau
   fakta kasus ini benar-benar sesuai; error yang mirip bisa saja penyebabnya berbeda.
10. Kalau ada KONTRAK API, itulah acuan utama untuk memutuskan siapa yang menyimpang. Bandingkan yang diharapkan
   test (status, field, tipe di schema, payload yang dikirim) dan response yang tercatat dengan kontrak:
   - test mengharapkan atau mengirim sesuatu yang berbeda dari kontrak -> deviates "test", category test_bug
   - response berbeda dari kontrak (status, field hilang, tipe salah) -> deviates "api", category backend_bug
   - kontrak tidak mengatur hal yang diperdebatkan (misalnya isi teks pesan) -> putuskan dari fakta lain
   Tanpa kontrak dan tanpa riwayat lulus, "test mengharapkan X, API memberi Y" sulit diputuskan: pakai confidence low.
   Kalau kontrak berubah dibanding run lulus terakhir dan response mengikuti kontrak baru, perubahannya disengaja:
   test yang usang (test_bug). Kalau kontrak tetap tapi response berubah, itu regresi API (backend_bug).
11. Urutan menjawab: tulis summary dan evidence dulu, lalu deviates (pihak yang menyimpang menurut summary itu),
   lalu category yang sejalan: test -> test_bug, api -> backend_bug, environment -> environment.

Jawab dengan memanggil tool report_verdict.`

var verdictTool = json.RawMessage(`{
  "type": "object",
  "properties": {
    "summary":    {"type": "string", "description": "Tulis lebih dulu: penjelasan penyebab, maksimal 3 kalimat."},
    "evidence":   {"type": "array", "items": {"type": "string"}, "maxItems": 4,
                   "description": "Kutipan persis dari bagian FAKTA yang mendukung kesimpulan."},
    "deviates":   {"type": "string", "enum": ["test", "api", "environment", "unclear"],
                   "description": "Pihak yang menyimpang dari perilaku yang benar, sesuai summary (lihat kontrak API kalau ada)."},
    "category":   {"type": "string", "enum": ["backend_bug", "test_bug", "environment", "flaky", "unknown"],
                   "description": "Sejalan dengan deviates: test -> test_bug, api -> backend_bug, environment -> environment."},
    "confidence": {"type": "string", "enum": ["low", "medium", "high"]},
    "next_step":  {"type": "string", "description": "Satu langkah konkret berikutnya untuk SDET."}
  },
  "required": ["summary", "evidence", "deviates", "category", "confidence", "next_step"]
}`)

type verdict struct {
	Deviates   string   `json:"deviates"`
	Category   string   `json:"category"`
	Confidence string   `json:"confidence"`
	Summary    string   `json:"summary"`
	Evidence   []string `json:"evidence"`
	NextStep   string   `json:"next_step"`
}

// BuildFacts menyusun bagian FAKTA untuk prompt. Semua isi sudah diredaksi saat ingest.
func BuildFacts(f *store.Facts) string {
	g := f.Group
	var b strings.Builder
	fmt.Fprintf(&b, "Test: %s\nProject: %s\nFile: %s\n", g.Title, g.Project, g.File)
	if f.ErrorLocation != "" {
		fmt.Fprintf(&b, "Lokasi error: %s\n", f.ErrorLocation)
	}
	fmt.Fprintf(&b, "\nPesan error:\n%s\n", clip(f.ErrorMessage, 3000))
	if len(f.Sources) > 0 {
		b.WriteString(codeSection(f.Sources))
	} else if f.ErrorSnippet != "" {
		fmt.Fprintf(&b, "\nPotongan kode test di sekitar error:\n%s\n", clip(f.ErrorSnippet, 1500))
	}

	b.WriteString("\nRiwayat:\n")
	fmt.Fprintf(&b, "- Status kelompok: %s, muncul %d kali, regresi %d kali.\n", g.Status, g.Occurrences, g.Regressions)
	if len(f.RecentStatuses) > 0 {
		fmt.Fprintf(&b, "- Status test ini di run terakhir (lama ke baru): %s\n", strings.Join(f.RecentStatuses, ", "))
	}
	fmt.Fprintf(&b, "- Jumlah flaky dalam 20 run terakhir: %d\n", f.FlakyRecent)

	if len(f.CurrentCalls) > 0 {
		b.WriteString("\nResponse API saat gagal (bentuk saja, tanpa isi; tipe JavaScript: number mencakup integer dan desimal):\n")
		for _, c := range f.CurrentCalls {
			fmt.Fprintf(&b, "- %s %s -> %d %s\n", c.Method, c.Path, c.Status, clip(compactJSON(c.Shape), 400))
		}
	}

	if len(f.CodeFiles) > 0 || len(f.CurrentCalls) > 0 {
		b.WriteString("\nPeta kode (test lain di run yang sama):\n")
		if len(f.CodeFiles) > 0 {
			fmt.Fprintf(&b, "- File yang dipakai test ini: %s\n", strings.Join(f.CodeFiles, ", "))
		}
		if len(f.CodeMap) == 0 {
			b.WriteString("- Tidak ada test lain di run ini yang memakai endpoint atau file yang sama.\n")
		}
		for _, l := range f.CodeMap {
			fmt.Fprintf(&b, "- %s %s: %d test lain lulus, %d gagal\n", l.Kind, l.Name, l.Passed, l.Failed)
		}
	}

	if f.Contract != "" {
		fmt.Fprintf(&b, "\nKontrak API (OpenAPI) saat test gagal, untuk endpoint yang dipanggil, * = wajib:\n%s", clip(f.Contract, 4000))
		b.WriteString("Kesesuaian response dengan kontrak (dicek otomatis):\n")
		if len(f.ContractViolations) == 0 {
			b.WriteString("- sesuai kontrak\n")
		}
		for _, v := range f.ContractViolations {
			fmt.Fprintf(&b, "- tidak sesuai: %s\n", v)
		}
	}

	b.WriteString("\nMatriks perubahan:\n")
	if f.LastPass == nil {
		b.WriteString("- Test ini belum pernah lulus sejak dicatat Redline, jadi tidak ada pembanding.\n")
	} else {
		fmt.Fprintf(&b, "- Pembanding: run lulus terakhir #%d, gagal pertama sesudahnya #%d\n", f.LastPass.RunID, f.FirstFail.RunID)
		fmt.Fprintf(&b, "- Kode test berubah: %s\n", f.TestChanged)
		fmt.Fprintf(&b, "- Response API: %s\n", f.Response.Verdict)
		for _, l := range f.Response.Lines {
			fmt.Fprintf(&b, "  - %s\n", l)
		}
		if f.ContractChanged != "" {
			fmt.Fprintf(&b, "- Kontrak API berubah: %s\n", f.ContractChanged)
			if f.ContractBefore != "" {
				fmt.Fprintf(&b, "  Kontrak saat run lulus terakhir:\n%s", indent(clip(f.ContractBefore, 2000), "    "))
			}
		}
		if info := versionInfo(f); info != "" {
			b.WriteString(info)
		}
	}
	return b.String()
}

// Batas kode test di prompt: cukup untuk spec + schema/helper, tetap murah.
const (
	maxCodePerFile = 6000
	maxCodeTotal   = 16000
)

// codeSection menulis kode test dengan nomor baris, supaya cocok dengan lokasi error (file:baris).
func codeSection(files []store.SourceFile) string {
	var b strings.Builder
	b.WriteString("\nKode test (file spec lalu file yang di-import, sudah disamarkan):\n")
	budget := maxCodeTotal
	for _, f := range files {
		if budget <= 0 {
			fmt.Fprintf(&b, "\n=== %s ===\n(dilewati: batas panjang kode)\n", f.Path)
			continue
		}
		content := clip(f.Content, min(maxCodePerFile, budget))
		budget -= len(content)
		fmt.Fprintf(&b, "\n=== %s ===\n", f.Path)
		for i, line := range strings.Split(strings.TrimRight(content, "\n"), "\n") {
			fmt.Fprintf(&b, "%4d | %s\n", i+1, line)
		}
	}
	return b.String()
}

// References menyusun bagian KASUS MIRIP. Sengaja di luar FAKTA: bukti AI hanya boleh dikutip dari FAKTA.
func References(f *store.Facts) string {
	if len(f.Similar) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\nKASUS MIRIP YANG SUDAH TERBUKTI (referensi, bukan bukti; penyebabnya bisa berbeda):\n")
	for i, c := range f.Similar {
		by := "label manual"
		if c.Source == "experiment" {
			by = "eksperimen"
		}
		fmt.Fprintf(&b, "\n%d. %s, terbukti lewat %s, kemiripan makna error %.2f\n   Test: %s\n   Error: %s\n",
			i+1, c.Category, by, c.Similarity, c.Test, strings.ReplaceAll(clip(c.Error, 400), "\n", "\n   "))
		if c.Reason != "" {
			fmt.Fprintf(&b, "   Penyebab: %s\n", clip(c.Reason, 300))
		}
	}
	return b.String()
}

// versionInfo: commit dan versi app hanya info tambahan, ditulis kalau diisi.
func versionInfo(f *store.Facts) string {
	var b strings.Builder
	if f.LastPass.CommitSHA != "" || f.FirstFail.CommitSHA != "" {
		fmt.Fprintf(&b, "- Commit test: %s -> %s\n", orUnknown(f.LastPass.CommitSHA), orUnknown(f.FirstFail.CommitSHA))
	}
	if f.LastPass.AppVersion != "" || f.FirstFail.AppVersion != "" {
		fmt.Fprintf(&b, "- Versi aplikasi: %s -> %s (berubah: %s)\n", orUnknown(f.LastPass.AppVersion),
			orUnknown(f.FirstFail.AppVersion), changed(f.LastPass.AppVersion, f.FirstFail.AppVersion))
	}
	return b.String()
}

func indent(s, pad string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = pad + l
	}
	return strings.Join(lines, "\n") + "\n"
}

func changed(before, after string) string {
	switch {
	case before == "" || after == "":
		return "tidak diketahui"
	case before == after:
		return "tidak"
	default:
		return "ya"
	}
}

func orUnknown(s string) string {
	if s == "" {
		return "(tidak dicatat)"
	}
	return s
}

func compactJSON(raw json.RawMessage) string {
	var buf bytes.Buffer
	if json.Compact(&buf, raw) != nil {
		return string(raw)
	}
	return buf.String()
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "\n…(dipotong)"
	}
	return s
}
