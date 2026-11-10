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
var ErrNothingToLearn = errors.New("no new proven cases: label one manually or run an experiment first")

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

const learnSystemPrompt = `You help Redline learn deterministic rules from test failures whose cause is
ALREADY PROVEN (labeled by a human, or the test passed after a patch).

Your job: find a PATTERN in the error message that marks the same cause, then write it as a regex.
A good rule:
- Captures the MECHANISM of the error (for example "received Promise", "Cannot read properties of undefined"),
  not the test name, ID, data value, URL, or changing numbers.
- Specific enough not to match cases with another category.
- Applies to similar future cases, not just this one.
- Go (RE2) regex syntax: no lookahead/lookbehind and no backreferences. Use (?i) if needed.

If there is no pattern worth a rule, return an empty list. Better no rule than a wrong rule.
At most 3 rules.
summary must be TRUE FOR EVERY error matching the pattern, not just the example case: do not mention
the test, endpoint, or a specific variable name. If the same pattern can mean another cause (for example a missing field
because the API changed), say so, or make the pattern narrower. next_step is one concrete action.
Both in English.

Answer by calling the propose_rules tool.`

var learnTool = json.RawMessage(`{
  "type": "object",
  "properties": {
    "rules": {
      "type": "array", "maxItems": 3,
      "items": {
        "type": "object",
        "properties": {
          "pattern":   {"type": "string", "description": "Go (RE2) regex against the error message."},
          "category":  {"type": "string", "enum": ["backend_bug", "test_bug", "environment", "flaky", "unknown"]},
          "summary":   {"type": "string", "description": "General cause that is correct for EVERY error matching the pattern. Do not mention the test name, endpoint, or a variable from the example case."},
          "next_step": {"type": "string", "description": "One concrete, general next step."},
          "case_ids":  {"type": "array", "items": {"type": "string"}, "description": "IDs of the cases this rule is based on."}
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
			Description: "Propose deterministic rules from proven cases.",
			InputSchema: learnTool,
		}},
		ToolChoice: &llm.ToolChoice{Type: "tool", Name: learnToolName},
	})
	if err != nil {
		return nil, err
	}
	raw, ok := resp.ToolInput(learnToolName)
	if !ok {
		return nil, fmt.Errorf("AI did not call %s (stop_reason=%s)", learnToolName, resp.StopReason)
	}
	var out struct {
		Rules []ruleCandidate `json:"rules"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("invalid AI response: %w", err)
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
			res.Rejected = append(res.Rejected, RejectedRule{Pattern: c.Pattern, Category: c.Category, Reason: "an identical rule already exists"})
			continue
		}
		if err != nil {
			return nil, err
		}
		rule.ID, rule.Status = id, "proposed"
		res.Proposed = append(res.Proposed, rule)
	}
	a.logger().Info("rule proposal", "cases", len(uncovered), "proposed", len(res.Proposed),
		"rejected", len(res.Rejected), "cost_usd", res.CostUSD)
	return res, nil
}

func learnPrompt(cases []store.Case, rejected []store.LearnedRule) string {
	var b strings.Builder
	if len(rejected) > 0 {
		b.WriteString("PATTERNS ALREADY REJECTED BY A HUMAN (do not propose them again, including small variations):\n")
		for _, r := range rejected {
			fmt.Fprintf(&b, "- /%s/ -> %s: %s\n", r.Pattern, r.Category, clip(r.Summary, 200))
		}
		b.WriteString("\n")
	}
	b.WriteString("PROVEN CASES:\n")
	for _, c := range cases {
		fmt.Fprintf(&b, "\n--- id: %s\ncategory: %s (evidence: %s)\n", c.GroupID, c.Category, c.Source)
		if c.Reason != "" {
			fmt.Fprintf(&b, "reason: %s\n", clip(c.Reason, 300))
		}
		fmt.Fprintf(&b, "error message:\n%s\n", clip(c.Error, 800))
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
		return store.LearnedRule{}, "category must be clear, not unknown"
	}
	if strings.TrimSpace(c.Summary) == "" {
		return store.LearnedRule{}, "summary is empty"
	}
	if len(pattern) < 6 {
		return store.LearnedRule{}, "pattern is too short"
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return store.LearnedRule{}, "invalid regex: " + err.Error()
	}
	if re.MatchString("") {
		return store.LearnedRule{}, "pattern matches empty text"
	}

	confirmed := map[string]bool{}
	var from []string
	for _, k := range cases {
		confirmed[k.GroupID] = true
		if !re.MatchString(k.Error) {
			continue
		}
		if k.Category != c.Category {
			return store.LearnedRule{}, fmt.Sprintf("matches proven case %s with category %s", k.GroupID, k.Category)
		}
		from = append(from, k.GroupID)
	}
	if len(from) == 0 {
		return store.LearnedRule{}, "does not match any proven case"
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
		return store.LearnedRule{}, fmt.Sprintf("too general: matches %d of %d groups", matched, len(all))
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

// BacktestCandidate menguji candidate AI yang sudah punya kebenaran (label/eksperimen).
// Candidate belum pernah dipakai runtime; hasilnya baru menjadi Rule proposed.
func BacktestCandidate(c store.RuleCandidate, cases []store.Case, all []store.GroupError) (store.LearnedRule, string) {
	return Backtest(ruleCandidate{
		Pattern: c.Pattern, Category: c.Category, Summary: c.Summary, NextStep: c.NextStep,
		CaseIDs: []string{c.Fingerprint},
	}, cases, all)
}

// RejectedCandidate adalah candidate yang gagal saat backtest/promosi.
type RejectedCandidate struct {
	ID      int64  `json:"id"`
	Pattern string `json:"pattern"`
	Reason  string `json:"reason"`
}

// PromotionResult adalah hasil satu putaran promosi candidate.
type PromotionResult struct {
	Candidates int                 `json:"candidates"`
	Promoted   []store.LearnedRule `json:"promoted"`
	Rejected   []RejectedCandidate `json:"rejected"`
}

// PromoteCandidates memproses candidate draft yang penyebabnya sudah terbukti. Candidate yang lolos
// backtest menjadi Rule proposed (belum aktif); yang gagal disimpan sebagai rejected beserta alasannya.
func (a *Analyzer) PromoteCandidates(ctx context.Context, fingerprint string) (*PromotionResult, error) {
	candidates, err := a.Store.ListRuleCandidates(ctx, fingerprint, "draft")
	if err != nil {
		return nil, err
	}
	res := &PromotionResult{Candidates: len(candidates), Promoted: []store.LearnedRule{}, Rejected: []RejectedCandidate{}}
	if len(candidates) == 0 {
		return res, nil
	}
	cases, err := a.Store.ConfirmedCases(ctx, 1000)
	if err != nil {
		return nil, err
	}
	confirmed := map[string]bool{}
	for _, k := range cases {
		confirmed[k.GroupID] = true
	}
	all, err := a.Store.RecentGroupErrors(ctx, 5000)
	if err != nil {
		return nil, err
	}
	for _, c := range candidates {
		// Candidate tanpa label/eksperimen tetap draft; jangan ditolak hanya karena belum terbukti.
		if !confirmed[c.Fingerprint] {
			continue
		}
		rule, reason := BacktestCandidate(c, cases, all)
		if reason != "" {
			if err := a.Store.MarkRuleCandidate(ctx, c.ID, "rejected", reason, 0, 0, 0); err != nil {
				return nil, err
			}
			res.Rejected = append(res.Rejected, RejectedCandidate{ID: c.ID, Pattern: c.Pattern, Reason: reason})
			continue
		}
		id, err := a.Store.SaveRule(ctx, rule)
		if errors.Is(err, store.ErrDuplicate) {
			reason = "an identical rule already exists"
			if err := a.Store.MarkRuleCandidate(ctx, c.ID, "rejected", reason, 0, 0, 0); err != nil {
				return nil, err
			}
			res.Rejected = append(res.Rejected, RejectedCandidate{ID: c.ID, Pattern: c.Pattern, Reason: reason})
			continue
		}
		if err != nil {
			return nil, err
		}
		if err := a.Store.MarkRuleCandidate(ctx, c.ID, "promoted", "", id, rule.Hits, rule.AlsoMatches); err != nil {
			return nil, err
		}
		rule.ID, rule.Status = id, "proposed"
		res.Promoted = append(res.Promoted, rule)
		a.logger().Info("candidate promoted", "candidate", c.ID, "rule", id, "hits", rule.Hits)
	}
	return res, nil
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
			return fmt.Sprintf("identical to rejected rule #%d", r.ID)
		}
	}
	return ""
}
