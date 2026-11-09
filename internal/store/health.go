package store

import (
	"context"
	"sort"
	"time"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// TestHealthSummary is the suite-level view for one project and time window.
type TestHealthSummary struct {
	Days             int     `json:"days"`
	Project          string  `json:"project,omitempty"`
	Runs             int     `json:"runs"`
	Results          int     `json:"results"`
	Passed           int     `json:"passed"`
	Failed           int     `json:"failed"`
	Flaky            int     `json:"flaky"`
	Skipped          int     `json:"skipped"`
	PassRate         float64 `json:"pass_rate"`
	FlakeRate        float64 `json:"flake_rate"`
	OpenFailures     int     `json:"open_failures"`
	NewFailures      int     `json:"new_failures"`
	ResolvedFailures int     `json:"resolved_failures"`
	MTTRHours        float64 `json:"mttr_hours"`
}

// TestHealthRow is one test across runs, keyed by @TC- ID when it has one.
type TestHealthRow struct {
	Key        string  `json:"key"`
	TestKey    string  `json:"test"`
	TestCaseID string  `json:"test_case_id,omitempty"`
	Project    string  `json:"project,omitempty"`
	Total      int     `json:"total"`
	Passed     int     `json:"passed"`
	Failed     int     `json:"failed"`
	Flaky      int     `json:"flaky"`
	Skipped    int     `json:"skipped"`
	PassRate   float64 `json:"pass_rate"`
	FlakeRate  float64 `json:"flake_rate"`
	// Flips counts pass↔fail transitions in the last 10 executed results (newest first). A test that
	// alternates without Playwright retries still has a high flip count.
	Flips              int        `json:"flips"`
	AvgDurationMs      int        `json:"avg_duration_ms"`
	LastDurationMs     int        `json:"last_duration_ms,omitempty"`
	PreviousDurationMs int        `json:"previous_duration_ms,omitempty"`
	DurationDeltaPct   float64    `json:"duration_delta_pct,omitempty"`
	LastStatus         string     `json:"last_status"`
	LastRunAt          *time.Time `json:"last_run_at,omitempty"`
	LastFailureAt      *time.Time `json:"last_failure_at,omitempty"`
	TriageOwner        string     `json:"triage_owner,omitempty"`
	TriageStatus       string     `json:"triage_status,omitempty"`
	Quarantined        bool       `json:"quarantined"`
	QuarantineOwner    string     `json:"quarantine_owner,omitempty"`
	QuarantineReason   string     `json:"quarantine_reason,omitempty"`
	QuarantineUntil    *time.Time `json:"quarantine_until,omitempty"`
}

// TestHealthPoint is one run in the suite-level health trend.
type TestHealthPoint struct {
	RunID     int64     `json:"run_id"`
	At        time.Time `json:"at"`
	Branch    string    `json:"branch,omitempty"`
	Commit    string    `json:"commit,omitempty"`
	Passed    int       `json:"passed"`
	Failed    int       `json:"failed"`
	Flaky     int       `json:"flaky"`
	Skipped   int       `json:"skipped"`
	PassRate  float64   `json:"pass_rate"`
	FlakeRate float64   `json:"flake_rate"`
}

// TestRunPoint is one result in a test's timeline.
type TestRunPoint struct {
	RunID       int64     `json:"run_id"`
	At          time.Time `json:"at"`
	Branch      string    `json:"branch,omitempty"`
	Commit      string    `json:"commit,omitempty"`
	Status      string    `json:"status"`
	DurationMs  int       `json:"duration_ms"`
	Retries     int       `json:"retries"`
	Error       string    `json:"error,omitempty"`
	Fingerprint string    `json:"fingerprint,omitempty"`
}

type healthRow struct {
	Key          string
	Project      string
	TestKey      string
	CaseID       string
	Status       string
	DurationMs   int
	Retries      int
	Error        string
	GroupID      string
	Owner        string
	TriageStatus string
	RunID        int64
	At           time.Time
	Branch       string
	Commit       string
}

type testAgg struct {
	row              TestHealthRow
	executed         int
	durationSum      int
	lastDuration     int
	previousDuration int
	lastExecuted     string
	recentExecuted   int
	flips            int
}

// TestHealth returns the suite summary and one row per test for the last days.
func (s *Store) TestHealth(ctx context.Context, project string, days int) (TestHealthSummary, []TestHealthRow, error) {
	days = clampDays(days)
	since := time.Now().AddDate(0, 0, -days)
	rows, err := s.healthRows(ctx, project, since, "")
	if err != nil {
		return TestHealthSummary{}, nil, err
	}

	summary := TestHealthSummary{Days: days, Project: project}
	runs := map[int64]struct{}{}
	for _, row := range rows {
		runs[row.RunID] = struct{}{}
		switch row.Status {
		case "passed":
			summary.Passed++
		case "failed":
			summary.Failed++
		case "flaky":
			summary.Flaky++
		case "skipped":
			summary.Skipped++
		}
	}
	summary.Runs = len(runs)
	summary.Results = len(rows)
	summary.PassRate = rate(summary.Passed, summary.Passed+summary.Failed+summary.Flaky)
	summary.FlakeRate = rate(summary.Flaky, summary.Passed+summary.Failed+summary.Flaky)

	open, newFailures, resolved, mttr, err := s.failureHealth(ctx, project, since)
	if err != nil {
		return TestHealthSummary{}, nil, err
	}
	summary.OpenFailures = open
	summary.NewFailures = newFailures
	summary.ResolvedFailures = resolved
	summary.MTTRHours = mttr

	tests := aggregateTests(rows)
	active, err := s.ActiveQuarantines(ctx)
	if err != nil {
		return TestHealthSummary{}, nil, err
	}
	applyQuarantine(tests, active)
	return summary, tests, nil
}

// TestHealthTrend returns one point per run in the window, oldest first. It is the same data the
// Health page uses for the suite summary, grouped by run instead of by test.
func (s *Store) TestHealthTrend(ctx context.Context, project string, days, limit int) ([]TestHealthPoint, error) {
	days = clampDays(days)
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	since := time.Now().AddDate(0, 0, -days)
	rows, err := s.healthRows(ctx, project, since, "")
	if err != nil {
		return nil, err
	}
	byRun := map[int64]*TestHealthPoint{}
	for _, row := range rows {
		point := byRun[row.RunID]
		if point == nil {
			point = &TestHealthPoint{
				RunID: row.RunID, At: row.At, Branch: row.Branch, Commit: row.Commit,
			}
			byRun[row.RunID] = point
		}
		switch row.Status {
		case "passed":
			point.Passed++
		case "failed":
			point.Failed++
		case "flaky":
			point.Flaky++
		case "skipped":
			point.Skipped++
		}
	}
	points := make([]TestHealthPoint, 0, len(byRun))
	for _, point := range byRun {
		point.PassRate = rate(point.Passed, point.Passed+point.Failed+point.Flaky)
		point.FlakeRate = rate(point.Flaky, point.Passed+point.Failed+point.Flaky)
		points = append(points, *point)
	}
	sort.Slice(points, func(i, j int) bool {
		if !points[i].At.Equal(points[j].At) {
			return points[i].At.Before(points[j].At)
		}
		return points[i].RunID < points[j].RunID
	})
	if len(points) > limit {
		points = points[len(points)-limit:]
	}
	return points, nil
}

// TestHistory returns one test's aggregate and its timeline, newest first.
func (s *Store) TestHistory(ctx context.Context, test string, days, limit int) (TestHealthRow, []TestRunPoint, error) {
	days = clampDays(days)
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	since := time.Now().AddDate(0, 0, -days)
	rows, err := s.healthRows(ctx, "", since, test)
	if err != nil {
		return TestHealthRow{}, nil, err
	}
	if len(rows) == 0 {
		return TestHealthRow{}, nil, ErrNotFound
	}

	tests := aggregateTests(rows)
	active, err := s.ActiveQuarantines(ctx)
	if err != nil {
		return TestHealthRow{}, nil, err
	}
	applyQuarantine(tests, active)
	row := tests[0]
	for _, candidate := range tests {
		if candidate.Key == test || candidate.TestKey == test {
			row = candidate
			break
		}
	}

	points := make([]TestRunPoint, 0, min(limit, len(rows)))
	for _, item := range rows {
		if item.Key != row.Key {
			continue
		}
		points = append(points, TestRunPoint{
			RunID: item.RunID, At: item.At, Branch: item.Branch, Commit: item.Commit,
			Status: item.Status, DurationMs: item.DurationMs, Retries: item.Retries,
			Error: item.Error, Fingerprint: item.GroupID,
		})
		if len(points) == limit {
			break
		}
	}
	return row, points, nil
}

func (s *Store) healthRows(ctx context.Context, project string, since time.Time, test string) ([]healthRow, error) {
	res, err := s.query(ctx, `
		MATCH (run:Run)-[:HAS]->(r:Result)-[:OF]->(t:Test)
		WHERE run.createdAt >= $since
		  AND ($project = '' OR coalesce(t.project, '') = $project)
		  AND ($test = '' OR r.caseId = $test OR r.testKey = $test)
		OPTIONAL MATCH (f:Failure {id: r.groupId})
		RETURN CASE WHEN r.caseId IS NULL OR r.caseId = '' THEN r.testKey ELSE r.caseId END AS key,
		       coalesce(t.project, '') AS project,
		       r.testKey AS testKey,
		       coalesce(r.caseId, '') AS caseId,
		       r.status AS status,
		       coalesce(r.durationMs, 0) AS durationMs,
		       coalesce(r.retries, 0) AS retries,
		       coalesce(r.errorMessage, '') AS error,
		       coalesce(r.groupId, '') AS groupId,
		       coalesce(f.triageOwner, '') AS owner,
		       coalesce(f.triageStatus, '') AS triageStatus,
		       run.id AS runId,
		       run.createdAt AS at,
		       coalesce(run.branch, '') AS branch,
		       coalesce(run.commit, '') AS commit
		ORDER BY at DESC, runId DESC`,
		map[string]any{"since": since.UTC(), "project": project, "test": test})
	if err != nil {
		return nil, err
	}

	rows := make([]healthRow, 0, len(res.Records))
	for _, record := range res.Records {
		rows = append(rows, healthRow{
			Key:          recString(record, "key"),
			Project:      recString(record, "project"),
			TestKey:      recString(record, "testKey"),
			CaseID:       recString(record, "caseId"),
			Status:       recString(record, "status"),
			DurationMs:   recInt(record, "durationMs"),
			Retries:      recInt(record, "retries"),
			Error:        recString(record, "error"),
			GroupID:      recString(record, "groupId"),
			Owner:        recString(record, "owner"),
			TriageStatus: recString(record, "triageStatus"),
			RunID:        recInt64(record, "runId"),
			At:           local(recValue(record, "at")),
			Branch:       recString(record, "branch"),
			Commit:       recString(record, "commit"),
		})
	}
	return rows, nil
}

func (s *Store) failureHealth(ctx context.Context, project string, since time.Time) (int, int, int, float64, error) {
	res, err := s.query(ctx, `
		MATCH (f:Failure)
		WHERE ($project = '' OR coalesce(f.project, '') = $project)
		OPTIONAL MATCH (first:Run {id: f.firstSeenRun})
		OPTIONAL MATCH (resolved:Run {id: f.resolvedRun})
		RETURN
		  count(CASE WHEN f.status IN ['open', 'regressed'] THEN 1 END) AS open,
		  count(CASE WHEN first.createdAt >= $since THEN 1 END) AS new,
		  count(CASE WHEN resolved.createdAt >= $since THEN 1 END) AS resolved,
		  avg(CASE WHEN resolved.createdAt >= $since AND first.createdAt IS NOT NULL
		      THEN duration.inSeconds(first.createdAt, resolved.createdAt).seconds END) AS mttrSeconds`,
		map[string]any{"since": since.UTC(), "project": project})
	if err != nil {
		return 0, 0, 0, 0, err
	}
	if len(res.Records) == 0 {
		return 0, 0, 0, 0, nil
	}
	record := res.Records[0]
	mttr, _ := recValue(record, "mttrSeconds").(float64)
	return recInt(record, "open"), recInt(record, "new"), recInt(record, "resolved"), mttr / 3600, nil
}

func aggregateTests(rows []healthRow) []TestHealthRow {
	byKey := map[string]*testAgg{}
	order := make([]string, 0, len(rows))
	for _, item := range rows {
		agg := byKey[item.Key]
		if agg == nil {
			at := item.At
			agg = &testAgg{row: TestHealthRow{
				Key: item.Key, TestKey: item.TestKey, TestCaseID: item.CaseID, Project: item.Project,
				LastStatus: item.Status, LastRunAt: &at,
			}}
			byKey[item.Key] = agg
			order = append(order, item.Key)
		}
		// healthRows is newest first, so the first row for a key is the latest.
		if agg.row.TestKey == "" {
			agg.row.TestKey = item.TestKey
		}
		if agg.row.TestCaseID == "" && item.CaseID != "" {
			agg.row.TestCaseID = item.CaseID
		}
		if agg.row.Project == "" {
			agg.row.Project = item.Project
		}
		if agg.row.TriageOwner == "" && item.Owner != "" {
			agg.row.TriageOwner = item.Owner
			agg.row.TriageStatus = item.TriageStatus
		}
		agg.row.Total++
		switch item.Status {
		case "passed":
			agg.row.Passed++
		case "failed":
			agg.row.Failed++
		case "flaky":
			agg.row.Flaky++
		case "skipped":
			agg.row.Skipped++
		}
		if item.Status != "skipped" {
			if agg.executed == 0 {
				agg.lastDuration = item.DurationMs
			}
			if agg.executed == 1 {
				agg.previousDuration = item.DurationMs
			}
			agg.executed++
			agg.durationSum += item.DurationMs
			// healthRows is newest first; compare each result with the previous executed one.
			if agg.recentExecuted < 10 {
				if agg.lastExecuted != "" && isPassed(agg.lastExecuted) != isPassed(item.Status) {
					agg.flips++
				}
				agg.lastExecuted = item.Status
				agg.recentExecuted++
			}
		}
		if agg.row.LastFailureAt == nil && (item.Status == "failed" || item.Status == "flaky") {
			at := item.At
			agg.row.LastFailureAt = &at
		}
	}

	tests := make([]TestHealthRow, 0, len(byKey))
	for _, key := range order {
		agg := byKey[key]
		agg.row.PassRate = rate(agg.row.Passed, agg.executed)
		agg.row.FlakeRate = rate(agg.row.Flaky, agg.executed)
		agg.row.Flips = agg.flips
		if agg.executed > 0 {
			agg.row.AvgDurationMs = agg.durationSum / agg.executed
		}
		agg.row.LastDurationMs = agg.lastDuration
		agg.row.PreviousDurationMs = agg.previousDuration
		if agg.lastDuration > 0 && agg.previousDuration > 0 {
			agg.row.DurationDeltaPct = float64(agg.lastDuration-agg.previousDuration) / float64(agg.previousDuration) * 100
		}
		tests = append(tests, agg.row)
	}
	sort.SliceStable(tests, func(i, j int) bool {
		left, right := tests[i], tests[j]
		if left.Failed+left.Flaky != right.Failed+right.Flaky {
			return left.Failed+left.Flaky > right.Failed+right.Flaky
		}
		if left.LastRunAt != nil && right.LastRunAt != nil && !left.LastRunAt.Equal(*right.LastRunAt) {
			return left.LastRunAt.After(*right.LastRunAt)
		}
		return left.Key < right.Key
	})
	return tests
}

func isPassed(status string) bool {
	return status == "passed"
}

func clampDays(days int) int {
	if days <= 0 {
		return 30
	}
	if days > 365 {
		return 365
	}
	return days
}

func rate(part, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(part) / float64(total)
}

func recValue(record *neo4j.Record, key string) any {
	value, _ := record.Get(key)
	return value
}

func recString(record *neo4j.Record, key string) string {
	value, _ := recValue(record, key).(string)
	return value
}

func recInt(record *neo4j.Record, key string) int {
	value, _ := recValue(record, key).(int64)
	return int(value)
}

func recInt64(record *neo4j.Record, key string) int64 {
	value, _ := recValue(record, key).(int64)
	return value
}
