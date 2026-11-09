package analysis

import (
	"context"
	"errors"
	"strings"
	"testing"

	"redline/internal/llm"
)

type loadErrorLLM struct {
	text string
	req  llm.Request
}

func (f *loadErrorLLM) CreateMessage(_ context.Context, req llm.Request) (*llm.Response, error) {
	f.req = req
	return &llm.Response{
		Model:   "claude-haiku-5-5",
		Content: []llm.ContentBlock{{Type: "text", Text: f.text}},
		Usage:   llm.Usage{InputTokens: 100, OutputTokens: 30},
	}, nil
}

func TestAnalyzeLoadError(t *testing.T) {
	fake := &loadErrorLLM{text: "```json\n{\"category\":\"environment\",\"confidence\":\"high\",\"summary\":\"A package is missing.\",\"next_step\":\"Run npm ci.\"}\n```"}
	a := &Analyzer{LLM: fake, Model: "claude-haiku-5-5"}
	got, err := a.AnalyzeLoadError(context.Background(), "Cannot find package 'zod'")
	if err != nil {
		t.Fatal(err)
	}
	if got.Category != "environment" || got.Confidence != "high" || got.Source != "ai" ||
		got.Summary == "" || got.NextStep != "Run npm ci." {
		t.Errorf("analysis salah: %+v", got)
	}
	if !strings.Contains(fake.req.System, "test_bug") || !strings.Contains(fake.req.Messages[0].Content, "zod") {
		t.Errorf("prompt salah: %+v", fake.req)
	}

	off := &Analyzer{}
	if _, err := off.AnalyzeLoadError(context.Background(), "x"); !errors.Is(err, ErrAIDisabled) {
		t.Errorf("AI mati: mau ErrAIDisabled, dapat %v", err)
	}

	bad := &Analyzer{LLM: &loadErrorLLM{text: "bukan json"}, Model: "m"}
	if _, err := bad.AnalyzeLoadError(context.Background(), "x"); err == nil {
		t.Error("jawaban bukan JSON harus error")
	}
}

func TestAnalyzeLoadErrorCategories(t *testing.T) {
	for _, category := range []string{"test_bug", "environment", "backend_bug", "flaky", "unclear"} {
		fake := &loadErrorLLM{text: `{"category":"` + category + `","confidence":"low","summary":"s","next_step":"n"}`}
		got, err := (&Analyzer{LLM: fake, Model: "m"}).AnalyzeLoadError(context.Background(), "x")
		if err != nil || got.Category != category || got.Source != "ai" {
			t.Errorf("kategori %s salah: %+v %v", category, got, err)
		}
	}
	// Kategori yang tidak dikenal jatuh ke unclear, bukan diteruskan ke UI.
	fake := &loadErrorLLM{text: `{"category":"bogus","confidence":"high","summary":"s","next_step":"n"}`}
	got, err := (&Analyzer{LLM: fake, Model: "m"}).AnalyzeLoadError(context.Background(), "x")
	if err != nil || got.Category != "unclear" {
		t.Errorf("kategori asing harus unclear: %+v %v", got, err)
	}
}
