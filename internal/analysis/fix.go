package analysis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"redline/internal/llm"
	"redline/internal/store"
	"redline/internal/triage"
)

// SourceFile adalah satu file kode test yang dikirim CLI eksperimen (spec + file lokal yang di-import).
type SourceFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// Attempt adalah percobaan patch sebelumnya yang belum berhasil, supaya AI tidak mengulang dugaan yang sama.
type Attempt struct {
	Hypothesis string `json:"hypothesis"`
	Patch      string `json:"patch"`
	Result     string `json:"result"` // error setelah patch, atau alasan patch ditolak
}

// Edit mengganti potongan teks yang muncul tepat sekali di sebuah file.
type Edit struct {
	Path    string `json:"path"`
	Find    string `json:"find"`
	Replace string `json:"replace"`
}

// Fix adalah hipotesis AI beserta patch untuk mengujinya. Edits kosong kalau menurut AI
// penyebabnya bukan di kode test: hipotesis seperti itu tidak bisa dibuktikan dengan patch.
type Fix struct {
	Category   string  `json:"category"`
	Confidence string  `json:"confidence"`
	Hypothesis string  `json:"hypothesis"`
	Edits      []Edit  `json:"edits"`
	Model      string  `json:"model"`
	CostUSD    float64 `json:"cost_usd"`
}

const fixToolName = "propose_fix"

// Batas ukuran supaya prompt tetap murah.
const (
	maxFixFiles     = 8
	maxFixFileBytes = 30_000
	maxFixAttempts  = 3
)

const fixSystemPrompt = `Kamu SDET senior. Sebuah test otomatis gagal. Tugasmu membuat SATU hipotesis penyebab
yang bisa DIBUKTIKAN dengan menjalankan test lagi.

Kalau menurutmu kesalahan ada di kode test (await yang hilang, matcher yang salah, schema Zod yang salah,
data test yang salah, path atau method endpoint yang salah di test), usulkan perubahan SEKECIL MUNGKIN
supaya test bekerja sesuai MAKSUD ASLINYA. Test akan dijalankan dengan patch itu: kalau lulus, hipotesismu terbukti.

DILARANG, dan patch seperti ini ditolak otomatis:
- menghapus atau melemahkan assertion (expect, parse, toBe, toMatchObject, dan sejenisnya)
- menambah skip, fixme, only, atau fail
- membungkus kode dengan try/catch untuk menelan error
- menambah retry atau timeout untuk menutupi masalah
- mengubah nilai yang diharapkan supaya cocok dengan response API yang salah. Kalau API yang berubah
  (lihat matriks perubahan), itu backend_bug, bukan salah test.

Kalau penyebabnya di API/backend, environment, flaky, atau tidak jelas: isi category yang sesuai dan biarkan
edits KOSONG. Jangan memaksakan patch.

Format edit: "find" adalah potongan teks PERSIS dari file (termasuk spasi) yang muncul tepat sekali di file itu,
"replace" adalah penggantinya. Ambil find secukupnya (satu sampai beberapa baris) supaya unik.
Kalau ada PERCOBAAN SEBELUMNYA, pelajari hasilnya dan jangan ulangi patch yang sama.
KASUS MIRIP adalah penyebab yang pernah terbukti di project ini; pakai sebagai petunjuk, bukan untuk disalin.
Peta kode: kalau test lain dengan endpoint/file yang sama lulus, bandingkan cara mereka menulis test.
Tulis hypothesis dalam bahasa Indonesia, maksimal 2 kalimat, sebutkan file dan bagian yang salah.

Jawab dengan memanggil tool propose_fix.`

var fixTool = json.RawMessage(`{
  "type": "object",
  "properties": {
    "category":   {"type": "string", "enum": ["backend_bug", "test_bug", "environment", "flaky", "unknown"]},
    "confidence": {"type": "string", "enum": ["low", "medium", "high"]},
    "hypothesis": {"type": "string", "description": "Dugaan penyebab, maksimal 2 kalimat."},
    "edits": {
      "type": "array", "maxItems": 6,
      "items": {
        "type": "object",
        "properties": {
          "path":    {"type": "string", "description": "Path file persis seperti di bagian FILE."},
          "find":    {"type": "string"},
          "replace": {"type": "string"}
        },
        "required": ["path", "find", "replace"]
      }
    }
  },
  "required": ["category", "confidence", "hypothesis", "edits"]
}`)

// ErrBadFixInput: file yang dikirim kosong atau terlalu besar.
var ErrBadFixInput = errors.New("input eksperimen tidak valid")

// ProposeFix meminta AI membuat hipotesis + patch untuk satu kelompok kegagalan.
// Isi file disamarkan dulu (token, password) sebelum dikirim ke AI.
func (a *Analyzer) ProposeFix(ctx context.Context, fingerprint string, files []SourceFile, attempts []Attempt) (*Fix, error) {
	if a.LLM == nil {
		return nil, ErrAIDisabled
	}
	if len(files) == 0 || len(files) > maxFixFiles {
		return nil, fmt.Errorf("%w: kirim 1 sampai %d file", ErrBadFixInput, maxFixFiles)
	}
	for _, f := range files {
		if f.Path == "" || len(f.Content) > maxFixFileBytes {
			return nil, fmt.Errorf("%w: file %q kosong namanya atau lebih dari %d byte", ErrBadFixInput, f.Path, maxFixFileBytes)
		}
	}
	facts, err := a.facts(ctx, fingerprint)
	if err != nil {
		return nil, err
	}

	resp, err := a.LLM.CreateMessage(ctx, llm.Request{
		Model:     a.Model,
		MaxTokens: 4096,
		System:    fixSystemPrompt,
		Messages:  []llm.Message{{Role: "user", Content: fixPrompt(facts, files, attempts)}},
		Tools: []llm.Tool{{
			Name:        fixToolName,
			Description: "Laporkan hipotesis penyebab dan patch untuk mengujinya.",
			InputSchema: fixTool,
		}},
		ToolChoice: &llm.ToolChoice{Type: "tool", Name: fixToolName},
	})
	if err != nil {
		return nil, err
	}
	raw, ok := resp.ToolInput(fixToolName)
	if !ok {
		return nil, fmt.Errorf("AI tidak memanggil %s (stop_reason=%s)", fixToolName, resp.StopReason)
	}
	var fix Fix
	if err := json.Unmarshal(raw, &fix); err != nil {
		return nil, fmt.Errorf("jawaban AI tidak valid: %w", err)
	}
	if !store.ValidCategory(fix.Category) {
		fix.Category = "unknown"
	}
	if fix.Category != "test_bug" {
		fix.Edits = nil // hanya salah test yang bisa dibuktikan dengan patch test
	}
	known := map[string]bool{}
	for _, f := range files {
		known[f.Path] = true
	}
	edits := []Edit{}
	for _, e := range fix.Edits {
		if known[e.Path] && e.Find != "" && e.Find != e.Replace {
			edits = append(edits, e)
		}
	}
	fix.Edits = edits
	fix.Hypothesis = strings.TrimSpace(fix.Hypothesis)
	fix.Model = resp.Model
	if fix.Model == "" {
		fix.Model = a.Model
	}
	fix.CostUSD = a.Pricing.Cost(resp.Usage)
	a.logger().Info("usulan patch AI", "fingerprint", fingerprint, "category", fix.Category,
		"edits", len(fix.Edits), "cost_usd", fix.CostUSD)
	return &fix, nil
}

func fixPrompt(f *store.Facts, files []SourceFile, attempts []Attempt) string {
	var b strings.Builder
	b.WriteString("FAKTA:\n\n")
	b.WriteString(BuildFacts(f))
	b.WriteString(References(f))
	b.WriteString("\n\nFILE (kode test saat ini):\n")
	for _, file := range files {
		fmt.Fprintf(&b, "\n=== %s ===\n%s\n", file.Path, triage.Redact(file.Content))
	}
	if len(attempts) > maxFixAttempts {
		attempts = attempts[len(attempts)-maxFixAttempts:]
	}
	if len(attempts) > 0 {
		b.WriteString("\nPERCOBAAN SEBELUMNYA (tidak berhasil):\n")
		for i, at := range attempts {
			fmt.Fprintf(&b, "\n%d. Hipotesis: %s\nPatch:\n%s\nHasil: %s\n", i+1, at.Hypothesis,
				clip(triage.Redact(at.Patch), 2000), clip(triage.Clean(at.Result), 1500))
		}
	}
	return b.String()
}
