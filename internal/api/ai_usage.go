package api

import (
	"math"
	"net/http"
	"time"
)

// handleAIUsage: GET /api/ai/usage — mode, model, daily budget and spend for the Config card.
func (s *Server) handleAIUsage(w http.ResponseWriter, r *http.Request) {
	settings := s.AI
	if settings == nil {
		settings = &AISettings{Mode: "fallback"}
	}
	now := time.Now()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	spent, err := s.Store.SpendSince(r.Context(), start)
	if err != nil {
		s.serverError(w, err)
		return
	}
	project := r.URL.Query().Get("project")
	projectSpent := spent
	if project != "" {
		projectSpent, err = s.Store.SpendSinceProject(r.Context(), start, project)
		if err != nil {
			s.serverError(w, err)
			return
		}
	}

	var remaining *float64
	exhausted := false
	if settings.DailyBudgetUSD > 0 {
		left := math.Max(0, settings.DailyBudgetUSD-spent)
		remaining = &left
		exhausted = spent >= settings.DailyBudgetUSD
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":                 settings.Enabled,
		"mode":                    settings.Mode,
		"model":                   settings.Model,
		"daily_budget_usd":        settings.DailyBudgetUSD,
		"spent_today_usd":         spent,
		"project":                 project,
		"project_spent_today_usd": projectSpent,
		"remaining_usd":           remaining,
		"max_per_run":             settings.MaxPerRun,
		"budget_exhausted":        exhausted,
	})
}

// handleAutoAcceptStats: GET /api/auto-accept/stats — how often automatic acceptance was
// confirmed or overridden by a human.
func (s *Server) handleAutoAcceptStats(w http.ResponseWriter, r *http.Request) {
	stats, err := s.Store.AutoAcceptStats(r.Context(), r.URL.Query().Get("project"))
	if err != nil {
		s.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, stats)
}
