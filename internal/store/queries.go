package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

type Run struct {
	ID                int64      `json:"id"`
	CreatedAt         time.Time  `json:"created_at"`
	StartedAt         *time.Time `json:"started_at"`
	DurationMs        int        `json:"duration_ms"`
	Source            string     `json:"source"`
	Branch            string     `json:"branch"`
	CommitSHA         string     `json:"commit"`
	AppVersion        string     `json:"app_version"`
	PlaywrightVersion string     `json:"playwright_version"`
	Total             int        `json:"total"`
	Passed            int        `json:"passed"`
	Failed            int        `json:"failed"`
	Flaky             int        `json:"flaky"`
	Skipped           int        `json:"skipped"`
	ReportErrors      []string   `json:"report_errors"`
}

type Group struct {
	Fingerprint     string     `json:"fingerprint"`
	TestKey         string     `json:"test"`
	Project         string     `json:"project"`
	File            string     `json:"file"`
	Title           string     `json:"title"`
	Status          string     `json:"status"`
	Summary         string     `json:"summary"`
	SampleError     string     `json:"sample_error,omitempty"`
	NormalizedError string     `json:"normalized_error,omitempty"`
	Occurrences     int        `json:"occurrences"`
	Regressions     int        `json:"regressions"`
	FirstSeenRun    int64      `json:"first_seen_run"`
	LastSeenRun     int64      `json:"last_seen_run"`
	ResolvedRun     *int64     `json:"resolved_run"`
	FirstSeenAt     time.Time  `json:"first_seen_at"`
	LastSeenAt      time.Time  `json:"last_seen_at"`
	ResolvedAt      *time.Time `json:"resolved_at"`
	HumanLabel      string     `json:"human_label,omitempty"`
	HumanNote       string     `json:"human_note,omitempty"`
}

// Occurrence adalah satu kemunculan kegagalan di satu run.
type Occurrence struct {
	RunID         int64     `json:"run_id"`
	RunCreatedAt  time.Time `json:"run_created_at"`
	Source        string    `json:"source"`
	Branch        string    `json:"branch"`
	CommitSHA     string    `json:"commit"`
	AppVersion    string    `json:"app_version"`
	Status        string    `json:"status"`
	Retries       int       `json:"retries"`
	ErrorLocation string    `json:"error_location"`
	ErrorSnippet  string    `json:"error_snippet"`
	TracePath     string    `json:"trace_path"`
	Screenshots   []string  `json:"screenshots"`
}

const runColumns = `id, created_at, started_at, duration_ms, source, branch, commit_sha, app_version,
	playwright_version, total, passed, failed, flaky, skipped, report_errors`

func (s *Store) ListRuns(ctx context.Context, limit int) ([]Run, error) {
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	rows, err := s.pool.Query(ctx, `SELECT `+runColumns+` FROM runs ORDER BY id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	runs := []Run{}
	for rows.Next() {
		var r Run
		if err := rows.Scan(&r.ID, &r.CreatedAt, &r.StartedAt, &r.DurationMs, &r.Source, &r.Branch, &r.CommitSHA,
			&r.AppVersion, &r.PlaywrightVersion, &r.Total, &r.Passed, &r.Failed, &r.Flaky, &r.Skipped,
			&r.ReportErrors); err != nil {
			return nil, err
		}
		runs = append(runs, r)
	}
	return runs, rows.Err()
}

const groupColumns = `fingerprint, test_key, project, file, title, status, sample_error, normalized_error,
	occurrences, regressions, first_seen_run, last_seen_run, resolved_run, first_seen_at, last_seen_at, resolved_at,
	coalesce(human_label, ''), human_note`

func scanGroup(row pgx.Row) (Group, error) {
	var g Group
	err := row.Scan(&g.Fingerprint, &g.TestKey, &g.Project, &g.File, &g.Title, &g.Status, &g.SampleError,
		&g.NormalizedError, &g.Occurrences, &g.Regressions, &g.FirstSeenRun, &g.LastSeenRun, &g.ResolvedRun,
		&g.FirstSeenAt, &g.LastSeenAt, &g.ResolvedAt, &g.HumanLabel, &g.HumanNote)
	g.Summary = summarize(g.SampleError)
	return g, err
}

// ListGroups mengembalikan kelompok kegagalan. statuses kosong = open + regressed (yang perlu dikerjakan).
func (s *Store) ListGroups(ctx context.Context, statuses []string, limit int) ([]Group, error) {
	if len(statuses) == 0 {
		statuses = []string{"open", "regressed"}
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `SELECT `+groupColumns+` FROM failure_groups
		WHERE status = ANY($1) ORDER BY last_seen_at DESC LIMIT $2`, statuses, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := []Group{}
	for rows.Next() {
		g, err := scanGroup(rows)
		if err != nil {
			return nil, err
		}
		g.SampleError, g.NormalizedError = "", "" // daftar cukup ringkasan
		groups = append(groups, g)
	}
	return groups, rows.Err()
}

// GetGroup mengembalikan satu kelompok beserta kemunculan terakhirnya.
func (s *Store) GetGroup(ctx context.Context, fingerprint string) (Group, []Occurrence, error) {
	g, err := scanGroup(s.pool.QueryRow(ctx, `SELECT `+groupColumns+` FROM failure_groups WHERE fingerprint = $1`, fingerprint))
	if err == pgx.ErrNoRows {
		return Group{}, nil, ErrNotFound
	}
	if err != nil {
		return Group{}, nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT r.id, r.created_at, r.source, r.branch, r.commit_sha, r.app_version,
		       t.status, t.retries, t.error_location, t.error_snippet, t.trace_path, t.screenshots
		FROM test_results t JOIN runs r ON r.id = t.run_id
		WHERE t.fingerprint = $1
		ORDER BY r.id DESC LIMIT 20`, fingerprint)
	if err != nil {
		return Group{}, nil, err
	}
	defer rows.Close()
	occ := []Occurrence{}
	for rows.Next() {
		var o Occurrence
		if err := rows.Scan(&o.RunID, &o.RunCreatedAt, &o.Source, &o.Branch, &o.CommitSHA, &o.AppVersion,
			&o.Status, &o.Retries, &o.ErrorLocation, &o.ErrorSnippet, &o.TracePath, &o.Screenshots); err != nil {
			return Group{}, nil, err
		}
		occ = append(occ, o)
	}
	return g, occ, rows.Err()
}
