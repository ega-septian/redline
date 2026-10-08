package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"redline/internal/report"
	"redline/internal/triage"
)

// Categories adalah kategori penyebab kegagalan yang dipakai aturan, AI, dan label manual.
var Categories = []string{"backend_bug", "test_bug", "environment", "flaky", "unknown"}

func ValidCategory(c string) bool {
	for _, x := range Categories {
		if x == c {
			return true
		}
	}
	return false
}

// RunRef adalah ringkasan satu run untuk keperluan bukti.
type RunRef struct {
	RunID      int64     `json:"run_id"`
	At         time.Time `json:"at"`
	Source     string    `json:"source"`
	CommitSHA  string    `json:"commit"`
	AppVersion string    `json:"app_version"`
}

// Facts adalah semua bukti tentang satu kelompok kegagalan. Hanya fakta dari database,
// tanpa tafsiran; inilah yang dikirim ke aturan dan ke AI.
type Facts struct {
	Group         Group  `json:"group"`
	ErrorMessage  string `json:"error_message"` // kemunculan terakhir, sudah diredaksi
	ErrorSnippet  string `json:"error_snippet"`
	ErrorLocation string `json:"error_location"`

	// LastPass: run terakhir test ini lulus sebelum kegagalan sekarang (nil = belum pernah lulus).
	LastPass *RunRef `json:"last_pass"`
	// FirstFail: run pertama gagal dengan error ini setelah LastPass.
	FirstFail RunRef `json:"first_fail"`

	RecentStatuses []string `json:"recent_statuses"` // status test ini di 10 run terakhir, lama -> baru
	FlakyRecent    int      `json:"flaky_recent"`    // berapa kali flaky di 20 run terakhir

	// Sinyal perubahan dibanding LastPass (tanpa git):
	CurrentCalls []report.HTTPCall   `json:"current_calls"` // bentuk response saat gagal
	TestChanged  string              `json:"test_changed"`  // "ya" / "tidak" / "tidak diketahui"
	Response     triage.ResponseDiff `json:"response"`
}

// Facts mengumpulkan bukti untuk satu kelompok kegagalan.
func (s *Store) Facts(ctx context.Context, fingerprint string) (*Facts, error) {
	g, err := scanGroup(s.pool.QueryRow(ctx, `SELECT `+groupColumns+` FROM failure_groups WHERE fingerprint = $1`, fingerprint))
	if err == pgx.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	f := &Facts{Group: g, RecentStatuses: []string{}}

	var curHash string
	var curCalls []byte
	if err := s.pool.QueryRow(ctx, `
		SELECT error_message, error_snippet, error_location, source_hash, http_calls FROM test_results
		WHERE fingerprint = $1 AND status = 'failed' ORDER BY run_id DESC LIMIT 1`, fingerprint,
	).Scan(&f.ErrorMessage, &f.ErrorSnippet, &f.ErrorLocation, &curHash, &curCalls); err != nil && err != pgx.ErrNoRows {
		return nil, fmt.Errorf("kemunculan terakhir: %w", err)
	}
	f.CurrentCalls = decodeCalls(curCalls)

	const runRef = `r.id, r.created_at, r.source, r.commit_sha, r.app_version`
	var lp RunRef
	var passHash string
	var passCalls []byte
	err = s.pool.QueryRow(ctx, `
		SELECT `+runRef+`, t.source_hash, t.http_calls FROM test_results t JOIN runs r ON r.id = t.run_id
		WHERE t.test_key = $1 AND t.status = 'passed' AND r.id < $2
		ORDER BY r.id DESC LIMIT 1`, g.TestKey, g.LastSeenRun,
	).Scan(&lp.RunID, &lp.At, &lp.Source, &lp.CommitSHA, &lp.AppVersion, &passHash, &passCalls)
	switch {
	case err == nil:
		f.LastPass = &lp
	case err != pgx.ErrNoRows:
		return nil, fmt.Errorf("lulus terakhir: %w", err)
	}
	f.TestChanged = triage.CompareHash(passHash, curHash)
	f.Response = triage.DiffCalls(decodeCalls(passCalls), f.CurrentCalls)

	after := int64(0)
	if f.LastPass != nil {
		after = f.LastPass.RunID
	}
	if err := s.pool.QueryRow(ctx, `
		SELECT `+runRef+` FROM test_results t JOIN runs r ON r.id = t.run_id
		WHERE t.fingerprint = $1 AND t.status = 'failed' AND r.id > $2
		ORDER BY r.id ASC LIMIT 1`, fingerprint, after,
	).Scan(&f.FirstFail.RunID, &f.FirstFail.At, &f.FirstFail.Source, &f.FirstFail.CommitSHA, &f.FirstFail.AppVersion); err != nil {
		return nil, fmt.Errorf("gagal pertama: %w", err)
	}

	rows, err := s.pool.Query(ctx, `
		SELECT status FROM (
			SELECT run_id, status FROM test_results WHERE test_key = $1 ORDER BY run_id DESC LIMIT 10
		) x ORDER BY run_id ASC`, g.TestKey)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var st string
		if err := rows.Scan(&st); err != nil {
			rows.Close()
			return nil, err
		}
		f.RecentStatuses = append(f.RecentStatuses, st)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if err := s.pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE status = 'flaky') FROM (
			SELECT status FROM test_results WHERE test_key = $1 ORDER BY run_id DESC LIMIT 20
		) x`, g.TestKey).Scan(&f.FlakyRecent); err != nil {
		return nil, err
	}
	return f, nil
}

func decodeCalls(raw []byte) []report.HTTPCall {
	var calls []report.HTTPCall
	if len(raw) == 0 || json.Unmarshal(raw, &calls) != nil {
		return []report.HTTPCall{}
	}
	return calls
}

// Analysis adalah hasil analisis satu kelompok kegagalan.
type Analysis struct {
	Fingerprint   string    `json:"fingerprint"`
	PromptVersion string    `json:"prompt_version"`
	Source        string    `json:"source"` // rule, ai, atau human (label manual, tidak disimpan di tabel ini)
	Model         string    `json:"model,omitempty"`
	Category      string    `json:"category"`
	Confidence    string    `json:"confidence"`
	Summary       string    `json:"summary"`
	Evidence      []string  `json:"evidence"`
	NextStep      string    `json:"next_step"`
	InputTokens   int       `json:"input_tokens"`
	OutputTokens  int       `json:"output_tokens"`
	CostUSD       float64   `json:"cost_usd"`
	CreatedAt     time.Time `json:"created_at"`
}

func (s *Store) SaveAnalysis(ctx context.Context, a Analysis) error {
	if a.Evidence == nil {
		a.Evidence = []string{}
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO analyses (fingerprint, prompt_version, source, model, category, confidence, summary,
			evidence, next_step, input_tokens, output_tokens, cost_usd, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, now())
		ON CONFLICT (fingerprint, prompt_version) DO UPDATE SET
			source = EXCLUDED.source, model = EXCLUDED.model, category = EXCLUDED.category,
			confidence = EXCLUDED.confidence, summary = EXCLUDED.summary, evidence = EXCLUDED.evidence,
			next_step = EXCLUDED.next_step, input_tokens = EXCLUDED.input_tokens,
			output_tokens = EXCLUDED.output_tokens, cost_usd = EXCLUDED.cost_usd, created_at = now()`,
		a.Fingerprint, a.PromptVersion, a.Source, a.Model, a.Category, a.Confidence, a.Summary,
		a.Evidence, a.NextStep, a.InputTokens, a.OutputTokens, a.CostUSD)
	return err
}

func (s *Store) GetAnalysis(ctx context.Context, fingerprint, promptVersion string) (Analysis, error) {
	var a Analysis
	err := s.pool.QueryRow(ctx, `
		SELECT fingerprint, prompt_version, source, model, category, confidence, summary, evidence,
			next_step, input_tokens, output_tokens, cost_usd::float8, created_at
		FROM analyses WHERE fingerprint = $1 AND prompt_version = $2`, fingerprint, promptVersion,
	).Scan(&a.Fingerprint, &a.PromptVersion, &a.Source, &a.Model, &a.Category, &a.Confidence, &a.Summary,
		&a.Evidence, &a.NextStep, &a.InputTokens, &a.OutputTokens, &a.CostUSD, &a.CreatedAt)
	if err == pgx.ErrNoRows {
		return Analysis{}, ErrNotFound
	}
	return a, err
}

// SetLabel menyimpan label manual. label kosong menghapus label.
func (s *Store) SetLabel(ctx context.Context, fingerprint, label, note string) error {
	var labelArg any
	if label != "" {
		labelArg = label
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE failure_groups SET human_label = $2, human_note = $3,
			labeled_at = CASE WHEN $2::text IS NULL THEN NULL ELSE now() END
		WHERE fingerprint = $1`, fingerprint, labelArg, note)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
