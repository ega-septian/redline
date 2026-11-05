package api

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"redline/internal/store"
)

const suggestionDays = 7

// quarantineSuggestion is one evidence-based proposal. It never changes state: the operator copies
// the JSON into redline-quarantine.json or approves it explicitly.
type quarantineSuggestion struct {
	Key            string     `json:"key"`
	Test           string     `json:"test"`
	TestCaseID     string     `json:"test_case_id,omitempty"`
	Project        string     `json:"project,omitempty"`
	Score          string     `json:"score"`
	Reason         string     `json:"reason"`
	Owner          string     `json:"owner,omitempty"`
	SuggestedUntil string     `json:"suggested_until"`
	Days           int        `json:"days"`
	Executed       int        `json:"executed"`
	Flaky          int        `json:"flaky"`
	FlakeRate      float64    `json:"flake_rate"`
	Flips          int        `json:"flips"`
	LastFailureAt  *time.Time `json:"last_failure_at,omitempty"`
}

// handleQuarantineSuggestions: GET /api/tests/quarantine/suggestions?days=30&project=...
func (s *Server) handleQuarantineSuggestions(w http.ResponseWriter, r *http.Request) {
	days, err := queryInt(r, "days", 30, 1, 365)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	project := strings.TrimSpace(r.URL.Query().Get("project"))
	_, tests, err := s.Store.TestHealth(r.Context(), project, days)
	if err != nil {
		s.serverError(w, err)
		return
	}
	dismissals, err := s.Store.SuggestionDismissals(r.Context())
	if err != nil {
		s.serverError(w, err)
		return
	}

	now := time.Now()
	suggestions := make([]quarantineSuggestion, 0, 4)
	for _, row := range tests {
		if suggestion, ok := suggestionFor(row, dismissals, now); ok {
			suggestions = append(suggestions, suggestion)
		}
	}
	sort.Slice(suggestions, func(i, j int) bool {
		left, right := suggestions[i], suggestions[j]
		if left.Score != right.Score {
			return left.Score == "high"
		}
		if left.Flips != right.Flips {
			return left.Flips > right.Flips
		}
		if left.FlakeRate != right.FlakeRate {
			return left.FlakeRate > right.FlakeRate
		}
		return left.Key < right.Key
	})
	if len(suggestions) > 10 {
		suggestions = suggestions[:10]
	}
	writeJSON(w, http.StatusOK, suggestions)
}

// suggestionFor applies the deterministic heuristic. Human approval is still required; this only
// decides what is worth showing.
func suggestionFor(row store.TestHealthRow, dismissals map[string]time.Time, now time.Time) (quarantineSuggestion, bool) {
	executed := row.Passed + row.Failed + row.Flaky
	if row.Quarantined || executed < 5 {
		return quarantineSuggestion{}, false
	}
	if row.PassRate <= 0 || row.PassRate >= 1 {
		return quarantineSuggestion{}, false
	}
	if row.LastFailureAt == nil || now.Sub(*row.LastFailureAt) > 14*24*time.Hour {
		return quarantineSuggestion{}, false
	}
	if until, ok := dismissals[row.Key]; ok && until.After(now) {
		return quarantineSuggestion{}, false
	}
	if row.Flips < 2 && !(row.Flaky >= 2 && row.FlakeRate >= 0.2) {
		return quarantineSuggestion{}, false
	}

	score := "medium"
	if row.Flips >= 3 || row.FlakeRate >= 0.3 {
		score = "high"
	}
	window := min(10, executed)
	detail := fmt.Sprintf("%d flips in the last %d runs", row.Flips, window)
	if row.Flips == 0 {
		detail = fmt.Sprintf("%d flaky retries in %d runs", row.Flaky, executed)
	} else if row.Flaky > 0 {
		detail += fmt.Sprintf(" (%d%% flake)", int(row.FlakeRate*100+0.5))
	}
	lastFailure := row.LastFailureAt.Format("2006-01-02")

	return quarantineSuggestion{
		Key: row.Key, Test: row.TestKey, TestCaseID: row.TestCaseID, Project: row.Project,
		Score: score, Owner: row.TriageOwner,
		Reason:         fmt.Sprintf("Flaky: %s, last failure %s", detail, lastFailure),
		SuggestedUntil: now.AddDate(0, 0, suggestionDays).Format("2006-01-02"),
		Days:           suggestionDays, Executed: executed, Flaky: row.Flaky,
		FlakeRate: row.FlakeRate, Flips: row.Flips, LastFailureAt: row.LastFailureAt,
	}, true
}

// handleDismissQuarantineSuggestion: POST /api/tests/quarantine/suggestions/dismiss
func (s *Server) handleDismissQuarantineSuggestion(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Test string `json:"test"`
		Days int    `json:"days"`
		By   string `json:"by"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "body harus JSON: {\"test\": \"TC-...\", \"days\": 7}")
		return
	}
	key := strings.TrimSpace(body.Test)
	if key == "" {
		writeError(w, http.StatusBadRequest, "test wajib diisi")
		return
	}
	if len(key) > 300 {
		writeError(w, http.StatusBadRequest, "test terlalu panjang (maks 300 karakter)")
		return
	}
	if body.Days == 0 {
		body.Days = suggestionDays
	}
	if body.Days < 1 || body.Days > 90 {
		writeError(w, http.StatusBadRequest, "days harus antara 1 dan 90")
		return
	}
	until := time.Now().AddDate(0, 0, body.Days)
	if err := s.Store.DismissQuarantineSuggestion(r.Context(), key, until, s.actor(r, body.By)); err != nil {
		s.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"dismissed": key, "until": until})
}
