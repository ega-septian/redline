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
	TriggeredBy       string     `json:"triggered_by"`
	CIURL             string     `json:"ci_url"`
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
	Fingerprint  string     `json:"fingerprint"`
	TestKey      string     `json:"test"`
	Project      string     `json:"project"`
	File         string     `json:"file"`
	Title        string     `json:"title"`
	Status       string     `json:"status"`
	Summary      string     `json:"summary"`
	SampleError  string     `json:"sample_error,omitempty"` // pesan error terakhir
	Cause        string     `json:"cause,omitempty"`        // penyebab yang bisa dibaca
	Occurrences  int        `json:"occurrences"`
	Regressions  int        `json:"regressions"`
	FirstSeenRun int64      `json:"first_seen_run"`
	LastSeenRun  int64      `json:"last_seen_run"`
	ResolvedRun  *int64     `json:"resolved_run"`
	FirstSeenAt  time.Time  `json:"first_seen_at"`
	LastSeenAt   time.Time  `json:"last_seen_at"`
	ResolvedAt   *time.Time `json:"resolved_at"`
	HumanLabel   string     `json:"human_label,omitempty"` // kategori dari label manual
	HumanNote    string     `json:"human_note,omitempty"`
	LabeledBy    string     `json:"labeled_by,omitempty"`
	LocalOnly    bool       `json:"local_only"` // hanya terlihat di run lokal; tidak masuk daftar tim
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

const runColumns = `id, created_at, started_at, duration_ms, source, triggered_by, ci_url, branch, commit_sha,
	app_version, playwright_version, total, passed, failed, flaky, skipped, report_errors`

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
		if err := rows.Scan(&r.ID, &r.CreatedAt, &r.StartedAt, &r.DurationMs, &r.Source, &r.TriggeredBy, &r.CIURL,
			&r.Branch, &r.CommitSHA,
			&r.AppVersion, &r.PlaywrightVersion, &r.Total, &r.Passed, &r.Failed, &r.Flaky, &r.Skipped,
			&r.ReportErrors); err != nil {
			return nil, err
		}
		runs = append(runs, r)
	}
	return runs, rows.Err()
}

// groupSelect: kolom kelompok kegagalan, dengan waktu diambil dari run terkait.
// Tambahkan WHERE/ORDER BY dengan alias g.
const groupSelect = `SELECT g.id, ` + testKeyG + `, g.project, g.file, g.title, g.status, g.last_error, g.cause,
	g.occurrences, g.regressions, g.first_seen_run, g.last_seen_run, g.resolved_run,
	fr.created_at, lr.created_at, rr.created_at,
	coalesce(g.manual_category, ''), g.manual_note, g.labeled_by, g.local_only
	FROM failure_groups g
	JOIN runs fr ON fr.id = g.first_seen_run
	JOIN runs lr ON lr.id = g.last_seen_run
	LEFT JOIN runs rr ON rr.id = g.resolved_run`

const testKeyG = `g.project || ' › ' || g.file || ' › ' || g.title`

func scanGroup(row pgx.Row) (Group, error) {
	var g Group
	err := row.Scan(&g.Fingerprint, &g.TestKey, &g.Project, &g.File, &g.Title, &g.Status, &g.SampleError, &g.Cause,
		&g.Occurrences, &g.Regressions, &g.FirstSeenRun, &g.LastSeenRun, &g.ResolvedRun,
		&g.FirstSeenAt, &g.LastSeenAt, &g.ResolvedAt, &g.HumanLabel, &g.HumanNote, &g.LabeledBy, &g.LocalOnly)
	g.Summary = summarize(g.SampleError)
	return g, err
}

// ListGroups mengembalikan kelompok kegagalan bersama (tanpa local_only).
// statuses kosong = open + regressed (yang perlu dikerjakan).
func (s *Store) ListGroups(ctx context.Context, statuses []string, limit int) ([]Group, error) {
	if len(statuses) == 0 {
		statuses = []string{"open", "regressed"}
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, groupSelect+`
		WHERE g.status = ANY($1) AND NOT g.local_only ORDER BY g.last_seen_run DESC LIMIT $2`, statuses, limit)
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
		g.SampleError = "" // daftar cukup ringkasan
		groups = append(groups, g)
	}
	return groups, rows.Err()
}

// GetGroup mengembalikan satu kelompok beserta kemunculan terakhirnya.
func (s *Store) GetGroup(ctx context.Context, fingerprint string) (Group, []Occurrence, error) {
	g, err := scanGroup(s.pool.QueryRow(ctx, groupSelect+` WHERE g.id = $1`, fingerprint))
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
		WHERE t.group_id = $1
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
