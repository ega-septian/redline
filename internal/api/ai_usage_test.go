package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

type aiUsageBody struct {
	Enabled         bool     `json:"enabled"`
	Mode            string   `json:"mode"`
	Model           string   `json:"model"`
	DailyBudgetUSD  float64  `json:"daily_budget_usd"`
	SpentTodayUSD   float64  `json:"spent_today_usd"`
	RemainingUSD    *float64 `json:"remaining_usd"`
	MaxPerRun       int      `json:"max_per_run"`
	BudgetExhausted bool     `json:"budget_exhausted"`
}

func getAIUsage(t *testing.T, srv *Server) aiUsageBody {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/ai/usage", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var body aiUsageBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body
}

func TestAIUsage(t *testing.T) {
	body := getAIUsage(t, &Server{
		Store: &fakeStore{spend: 0.25},
		AI: &AISettings{
			Enabled: true, Mode: "always", Model: "claude-haiku-5-5",
			DailyBudgetUSD: 1, MaxPerRun: 20,
		},
	})
	if !body.Enabled || body.Mode != "always" || body.Model != "claude-haiku-5-5" ||
		body.DailyBudgetUSD != 1 || body.SpentTodayUSD != 0.25 || body.MaxPerRun != 20 ||
		body.BudgetExhausted || body.RemainingUSD == nil || *body.RemainingUSD != 0.75 {
		t.Fatalf("usage = %+v", body)
	}
}

func TestAIUsageUnlimitedBudget(t *testing.T) {
	body := getAIUsage(t, &Server{Store: &fakeStore{spend: 0.5}})
	if body.Enabled || body.Mode != "fallback" || body.RemainingUSD != nil || body.BudgetExhausted {
		t.Fatalf("usage = %+v", body)
	}
}

func TestAIUsageBudgetExhausted(t *testing.T) {
	body := getAIUsage(t, &Server{
		Store: &fakeStore{spend: 1.5},
		AI:    &AISettings{Enabled: true, Mode: "always", DailyBudgetUSD: 1},
	})
	if !body.BudgetExhausted || body.RemainingUSD == nil || *body.RemainingUSD != 0 {
		t.Fatalf("usage = %+v", body)
	}
}
