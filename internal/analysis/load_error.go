package analysis

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"redline/internal/llm"
	"redline/internal/store"
	"redline/internal/triage"
)

const loadErrorSystemPrompt = `You classify a Playwright suite load error. The suite failed before tests could run.
Choose one category: test_bug, environment, backend_bug, flaky, unclear.
Use test_bug for broken imports, syntax errors, bad module settings or test-helper mistakes.
Use environment for missing packages, globalSetup/globalTeardown/webServer failures, network errors or permissions.
Use backend_bug only when the report clearly points at the application under test.
Use flaky only when the message indicates a nondeterministic startup failure.
If the message is not enough to decide, use unclear.
Return only JSON: {"category":"...","confidence":"high|medium|low","summary":"one sentence","next_step":"one concrete action"}.`

type loadErrorVerdict struct {
	Category   string `json:"category"`
	Confidence string `json:"confidence"`
	Summary    string `json:"summary"`
	NextStep   string `json:"next_step"`
}

// AnalyzeLoadError asks the model only for the groups that deterministic rules could not classify.
func (a *Analyzer) AnalyzeLoadError(ctx context.Context, message string) (triage.LoadErrorAnalysis, error) {
	if a.LLM == nil {
		return triage.LoadErrorAnalysis{}, ErrAIDisabled
	}
	if err := a.checkBudget(ctx); err != nil {
		return triage.LoadErrorAnalysis{}, err
	}
	resp, err := a.LLM.CreateMessage(ctx, llm.Request{
		Model:     a.Model,
		MaxTokens: 512,
		System:    loadErrorSystemPrompt,
		Messages: []llm.Message{{
			Role:    "user",
			Content: "REPORT ERROR:\n\n" + clipLoadErrorMessage(message),
		}},
	})
	if err != nil {
		return triage.LoadErrorAnalysis{}, err
	}
	text := firstText(resp)
	text = strings.TrimSpace(strings.TrimPrefix(strings.TrimSuffix(text, "```"), "```json"))
	var verdict loadErrorVerdict
	if err := json.Unmarshal([]byte(text), &verdict); err != nil {
		return triage.LoadErrorAnalysis{}, fmt.Errorf("invalid AI load-error response: %w", err)
	}
	if verdict.Category == "unknown" || !store.ValidCategory(verdict.Category) {
		verdict.Category = "unclear"
	}
	if verdict.Confidence != "high" && verdict.Confidence != "medium" && verdict.Confidence != "low" {
		verdict.Confidence = "low"
	}
	verdict.Summary = strings.TrimSpace(verdict.Summary)
	verdict.NextStep = strings.TrimSpace(verdict.NextStep)
	if verdict.Summary == "" {
		return triage.LoadErrorAnalysis{}, fmt.Errorf("AI load error did not provide a summary")
	}
	if verdict.NextStep == "" {
		verdict.NextStep = "Inspect the full report error in the run detail."
	}
	cost := a.Pricing.Cost(resp.Usage)
	if cost > 0 {
		if err := a.Store.RecordSpend(ctx, "", cost); err != nil {
			a.logger().Warn("failed to record AI load-error cost", "err", err)
		}
	}
	return triage.LoadErrorAnalysis{
		Category: verdict.Category, Confidence: verdict.Confidence, Source: "ai",
		Summary: verdict.Summary, NextStep: verdict.NextStep,
	}, nil
}

func firstText(resp *llm.Response) string {
	for _, block := range resp.Content {
		if block.Type == "text" {
			return block.Text
		}
	}
	return ""
}

func clipLoadErrorMessage(message string) string {
	const max = 4000
	runes := []rune(strings.TrimSpace(message))
	if len(runes) <= max {
		return string(runes)
	}
	return string(runes[:max-1]) + "…"
}
