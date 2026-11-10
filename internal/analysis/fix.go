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
type SourceFile = store.SourceFile

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

const fixSystemPrompt = `You are a senior SDET. An automated test failed. Your job is to make ONE hypothesis
that can be PROVEN by running the test again.

If you believe the fault is in the test code (missing await, wrong matcher, wrong Zod schema,
wrong test data, wrong endpoint path or method in the test), propose the SMALLEST possible change
so the test works as ORIGINALLY INTENDED. The test will run with that patch: if it passes, your hypothesis is proven.

FORBIDDEN, and such patches are rejected automatically:
- removing or weakening assertions (expect, parse, toBe, toMatchObject, and similar)
- adding skip, fixme, only, or fail
- wrapping code in try/catch to swallow errors
- adding retry or timeout to hide the problem
- changing expected values to match a wrong API response. If the API changed (see the change matrix),
  that is backend_bug, not a test bug.

If the cause is in the API/backend, environment, flaky, or unclear: set the matching category and leave
edits EMPTY. Do not force a patch.

Edit format: "find" is the EXACT text from the file (including whitespace) that appears exactly once in that file,
"replace" is the replacement. Keep find short (one to several lines) so it is unique.
If there are PREVIOUS ATTEMPTS, learn from the result and do not repeat the same patch.
SIMILAR CASES are causes proven in this project; use them as hints, not to copy.
Code map: if other tests with the same endpoint/file pass, compare how they write the test.
Write the hypothesis in English, at most 2 sentences, and name the file and the wrong part.

Answer by calling the propose_fix tool.`

var fixTool = json.RawMessage(`{
  "type": "object",
  "properties": {
    "category":   {"type": "string", "enum": ["backend_bug", "test_bug", "environment", "flaky", "unknown"]},
    "confidence": {"type": "string", "enum": ["low", "medium", "high"]},
    "hypothesis": {"type": "string", "description": "Hypothesis for the cause, at most 2 sentences."},
    "edits": {
      "type": "array", "maxItems": 6,
      "items": {
        "type": "object",
        "properties": {
          "path":    {"type": "string", "description": "File path exactly as shown in the FILE section."},
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
var ErrBadFixInput = errors.New("invalid experiment input")

// ProposeFix meminta AI membuat hipotesis + patch untuk satu kelompok kegagalan.
// Isi file disamarkan dulu (token, password) sebelum dikirim ke AI.
func (a *Analyzer) ProposeFix(ctx context.Context, fingerprint string, files []SourceFile, attempts []Attempt) (*Fix, error) {
	if a.LLM == nil {
		return nil, ErrAIDisabled
	}
	if len(files) == 0 || len(files) > maxFixFiles {
		return nil, fmt.Errorf("%w: send 1 to %d files", ErrBadFixInput, maxFixFiles)
	}
	for _, f := range files {
		if f.Path == "" || len(f.Content) > maxFixFileBytes {
			return nil, fmt.Errorf("%w: file %q has no name or is larger than %d bytes", ErrBadFixInput, f.Path, maxFixFileBytes)
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
			Description: "Report the hypothesis and a patch to test it.",
			InputSchema: fixTool,
		}},
		ToolChoice: &llm.ToolChoice{Type: "tool", Name: fixToolName},
	})
	if err != nil {
		return nil, err
	}
	raw, ok := resp.ToolInput(fixToolName)
	if !ok {
		return nil, fmt.Errorf("AI did not call %s (stop_reason=%s)", fixToolName, resp.StopReason)
	}
	var fix Fix
	if err := json.Unmarshal(raw, &fix); err != nil {
		return nil, fmt.Errorf("invalid AI response: %w", err)
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
	a.logger().Info("AI patch proposal", "fingerprint", fingerprint, "category", fix.Category,
		"edits", len(fix.Edits), "cost_usd", fix.CostUSD)
	return &fix, nil
}

func fixPrompt(f *store.Facts, files []SourceFile, attempts []Attempt) string {
	var b strings.Builder
	b.WriteString("FACTS:\n\n")
	// Kode test tidak diambil dari fakta: CLI mengirim versi terbaru di bagian FILE, dan patch harus cocok dengannya.
	withoutCode := *f
	withoutCode.Sources = nil
	b.WriteString(BuildFacts(&withoutCode))
	b.WriteString(References(f))
	b.WriteString("\n\nFILE (current test code):\n")
	for _, file := range files {
		fmt.Fprintf(&b, "\n=== %s ===\n%s\n", file.Path, triage.Redact(file.Content))
	}
	if len(attempts) > maxFixAttempts {
		attempts = attempts[len(attempts)-maxFixAttempts:]
	}
	if len(attempts) > 0 {
		b.WriteString("\nPREVIOUS ATTEMPTS (unsuccessful):\n")
		for i, at := range attempts {
			fmt.Fprintf(&b, "\n%d. Hipotesis: %s\nPatch:\n%s\nHasil: %s\n", i+1, at.Hypothesis,
				clip(triage.Redact(at.Patch), 2000), clip(triage.Clean(at.Result), 1500))
		}
	}
	return b.String()
}
