package analysis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"redline/internal/llm"
	"redline/internal/store"
)

// ErrNothingToLearn: belum ada kasus terbukti yang belum ditangani aturan.
var ErrNothingToLearn = errors.New("belum ada kasus terbukti yang baru: beri label manual atau jalankan eksperimen dulu")

// RejectedRule adalah usulan AI yang gagal diuji ke kasus lama, beserta alasannya.
type RejectedRule struct {
	Pattern  string `json:"pattern"`
	Category string `json:"category"`
	Reason   string `json:"reason"`
}

// LearnResult adalah hasil satu putaran belajar.
type LearnResult struct {
	Cases    int                 `json:"cases"` // kasus terbukti yang belum ditangani aturan
	Proposed []store.LearnedRule `json:"proposed"`
	Rejected []RejectedRule      `json:"rejected"`
	CostUSD  float64             `json:"cost_usd"`
}

const learnToolName = "propose_rules"

const learnSystemPrompt = `Kamu membantu Redline belajar aturan deterministik dari kegagalan test yang penyebabnya
SUDAH TERBUKTI (dilabeli manusia, atau test lulus setelah patch).

Tugasmu: cari POLA di pesan error yang menandai penyebab yang sama, lalu tulis sebagai regex.
Aturan yang bagus:
- Menangkap MEKANISME error (misalnya "received Promise", "Cannot read properties of undefined"),
  bukan nama test, ID, nilai data, URL, atau angka yang berubah-ubah.
- Cukup spesifik supaya tidak cocok dengan kasus berkategori lain.
- Berlaku untuk kasus serupa di masa depan, bukan hanya kasus ini.
- Sintaks regex Go (RE2): tanpa lookahead/lookbehind dan tanpa backreference. Pakai (?i) kalau perlu.

Kalau tidak ada pola yang layak dijadikan aturan, kembalikan daftar kosong. Lebih baik tidak ada aturan
daripada aturan yang salah. Maksimal 3 aturan.
summary harus BENAR UNTUK SEMUA error yang cocok dengan pola, bukan hanya untuk kasus contoh: jangan menyebut
test, endpoint, atau nama variabel tertentu. Kalau pola yang sama bisa berarti penyebab lain (misalnya field hilang
karena API berubah), sebutkan itu, atau buat pola yang lebih sempit. next_step satu langkah konkret.
Keduanya dalam bahasa Indonesia.

Jawab dengan memanggil tool propose_rules.`

var learnTool = json.RawMessage(`{
  "type": "object",
  "properties": {
    "rules": {
      "type": "array", "maxItems": 3,
      "items": {
        "type": "object",
        "properties": {
          "pattern":   {"type": "string", "description": "Regex Go (RE2) terhadap pesan error."},
          "category":  {"type": "string", "enum": ["backend_bug", "test_bug", "environment", "flaky", "unknown"]},
          "summary":   {"type": "string", "description": "Penyebab umum yang benar untuk SEMUA error yang cocok dengan pattern. Jangan sebut nama test, endpoint, atau variabel dari kasus contoh."},
          "next_step": {"type": "string", "description": "Satu langkah konkret, juga berlaku umum."},
          "case_ids":  {"type": "array", "items": {"type": "string"}, "description": "ID kasus yang menjadi dasar aturan."}
        },
        "required": ["pattern", "category", "summary", "next_step", "case_ids"]
      }
    }
  },
  "required": ["rules"]
}`)

type ruleCandidate struct {
	Pattern  string   `json:"pattern"`
	Category string   `json:"category"`
	Summary  string   `json:"summary"`
	NextStep string   `json:"next_step"`
	CaseIDs  []string `json:"case_ids"`
}

// ProposeRules: kumpulkan kasus terbukti yang belum ditangani aturan, minta AI mengusulkan pola,
// uji setiap pola ke kasus lama, lalu simpan yang lolos sebagai usulan (status proposed).
// Aturan baru aktif hanya setelah disetujui manusia.
func (a *Analyzer) ProposeRules(ctx context.Context) (*LearnResult, error) {
	if a.LLM == nil {
		return nil, ErrAIDisabled
	}
	cases, err := a.Store.ConfirmedCases(ctx, 100)
	if err != nil {
		return nil, err
	}
	existing, err := a.Store.ListRules(ctx, "")
	if err != nil {
		return nil, err
	}
	// Kasus yang sudah ditangani aturan aktif atau usulan yang menunggu review tidak dipelajari lagi.
	// Aturan yang ditolak tidak menutupi kasus; polanya dikirim ke AI supaya tidak diusulkan ulang.
	var pending, rejected []store.LearnedRule
	for _, r := range existing {
		if r.Status == "rejected" {
			rejected = append(rejected, r)
		} else {
			pending = append(pending, r)
		}
	}
	uncovered := []store.Case{}
	for _, c := range cases {
		if builtinRules(c.Error) == nil && learnedRules(c.Error, pending) == nil {
			uncovered = append(uncovered, c)
		}
	}
	if len(uncovered) == 0 {
		return nil, ErrNothingToLearn
	}

	resp, err := a.LLM.CreateMessage(ctx, llm.Request{
		Model:     a.Model,
		MaxTokens: 2048,
		System:    learnSystemPrompt,
		Messages:  []llm.Message{{Role: "user", Content: learnPrompt(uncovered, rejected)}},
		Tools: []llm.Tool{{
			Name:        learnToolName,
			Description: "Usulkan aturan deterministik dari kasus terbukti.",
			InputSchema: learnTool,
		}},
		ToolChoice: &llm.ToolChoice{Type: "tool", Name: learnToolName},
	})
	if err != nil {
		return nil, err
	}
	raw, ok := resp.ToolInput(learnToolName)
	if !ok {
		return nil, fmt.Errorf("AI tidak memanggil %s (stop_reason=%s)", learnToolName, resp.StopReason)
	}
	var out struct {
		Rules []ruleCandidate `json:"rules"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("jawaban AI tidak valid: %w", err)
	}

	all, err := a.Store.RecentGroupErrors(ctx, 5000)
	if err != nil {
		return nil, err
	}
	res := &LearnResult{Cases: len(uncovered), Proposed: []store.LearnedRule{}, Rejected: []RejectedRule{},
		CostUSD: a.Pricing.Cost(resp.Usage)}
	share := res.CostUSD
	if len(out.Rules) > 0 {
		share /= float64(len(out.Rules))
	}
	for _, c := range out.Rules {
		rule, reason := Backtest(c, cases, all)
		if reason == "" {
			reason = sameAsRejected(rule, rejected, all)
		}
		if reason != "" {
			res.Rejected = append(res.Rejected, RejectedRule{Pattern: c.Pattern, Category: c.Category, Reason: reason})
			continue
		}
		rule.CostUSD = share
		id, err := a.Store.SaveRule(ctx, rule)
		if errors.Is(err, store.ErrDuplicate) {
			res.Rejected = append(res.Rejected, RejectedRule{Pattern: c.Pattern, Category: c.Category, Reason: "aturan yang sama sudah ada"})
			continue
		}
		if err != nil {
			return nil, err
		}
		rule.ID, rule.Status = id, "proposed"
		res.Proposed = append(res.Proposed, rule)
	}
	a.logger().Info("usulan aturan", "kasus", len(uncovered), "diusulkan", len(res.Proposed),
		"ditolak", len(res.Rejected), "cost_usd", res.CostUSD)
	return res, nil
}

func learnPrompt(cases []store.Case, rejected []store.LearnedRule) string {
	var b strings.Builder
	if len(rejected) > 0 {
		b.WriteString("POLA YANG SUDAH DITOLAK MANUSIA (jangan diusulkan lagi, termasuk variasi kecilnya):\n")
		for _, r := range rejected {
			fmt.Fprintf(&b, "- /%s/ -> %s: %s\n", r.Pattern, r.Category, clip(r.Summary, 200))
		}
		b.WriteString("\n")
	}
	b.WriteString("KASUS TERBUKTI:\n")
	for _, c := range cases {
		fmt.Fprintf(&b, "\n--- id: %s\nkategori: %s (bukti: %s)\n", c.GroupID, c.Category, c.Source)
		if c.Reason != "" {
			fmt.Fprintf(&b, "alasan: %s\n", clip(c.Reason, 300))
		}
		fmt.Fprintf(&b, "pesan error:\n%s\n", clip(c.Error, 800))
	}
	return b.String()
}

// Backtest menguji satu usulan aturan ke kasus lama. Hasilnya aturan siap disimpan, atau alasan penolakan.
//   - pola harus valid dan tidak boleh cocok dengan teks kosong (terlalu umum)
//   - harus cocok dengan minimal satu kasus terbukti berkategori sama
//   - tidak boleh cocok dengan kasus terbukti berkategori lain
//   - tidak boleh cocok dengan lebih dari separuh semua kelompok (terlalu umum)
func Backtest(c ruleCandidate, cases []store.Case, all []store.GroupError) (store.LearnedRule, string) {
	pattern := strings.TrimSpace(c.Pattern)
	if !store.ValidCategory(c.Category) || c.Category == "unknown" {
		return store.LearnedRule{}, "kategori harus jelas, bukan unknown"
	}
	if strings.TrimSpace(c.Summary) == "" {
		return store.LearnedRule{}, "summary kosong"
	}
	if len(pattern) < 6 {
		return store.LearnedRule{}, "pola terlalu pendek"
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return store.LearnedRule{}, "regex tidak valid: " + err.Error()
	}
	if re.MatchString("") {
		return store.LearnedRule{}, "pola cocok dengan teks kosong"
	}

	confirmed := map[string]bool{}
	var from []string
	for _, k := range cases {
		confirmed[k.GroupID] = true
		if !re.MatchString(k.Error) {
			continue
		}
		if k.Category != c.Category {
			return store.LearnedRule{}, fmt.Sprintf("cocok dengan kasus %s yang terbukti %s", k.GroupID, k.Category)
		}
		from = append(from, k.GroupID)
	}
	if len(from) == 0 {
		return store.LearnedRule{}, "tidak cocok dengan satu pun kasus terbukti"
	}
	matched, also := 0, 0
	for _, g := range all {
		if re.MatchString(g.Error) {
			matched++
			if !confirmed[g.GroupID] {
				also++
			}
		}
	}
	if len(all) >= 6 && matched*2 > len(all) {
		return store.LearnedRule{}, fmt.Sprintf("terlalu umum: cocok dengan %d dari %d kelompok", matched, len(all))
	}
	return store.LearnedRule{
		Pattern:     pattern,
		Category:    c.Category,
		Summary:     strings.TrimSpace(c.Summary),
		NextStep:    strings.TrimSpace(c.NextStep),
		LearnedFrom: from,
		Hits:        len(from),
		AlsoMatches: also,
	}, ""
}

// sameAsRejected: usulan yang mencocokkan kelompok yang persis sama dengan aturan yang sudah ditolak
// (dengan kategori sama) dianggap usulan ulang, walaupun teks regex-nya sedikit berbeda.
func sameAsRejected(rule store.LearnedRule, rejected []store.LearnedRule, all []store.GroupError) string {
	re, err := regexp.Compile(rule.Pattern)
	if err != nil {
		return ""
	}
	for _, r := range rejected {
		old := compileCached(r.Pattern)
		if old == nil || r.Category != rule.Category {
			continue
		}
		same := true
		for _, g := range all {
			if re.MatchString(g.Error) != old.MatchString(g.Error) {
				same = false
				break
			}
		}
		if same {
			return fmt.Sprintf("sama dengan aturan #%d yang sudah ditolak", r.ID)
		}
	}
	return ""
}
