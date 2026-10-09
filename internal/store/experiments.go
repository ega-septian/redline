package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"redline/internal/triage"
)

// Experiment adalah satu percobaan menjalankan ulang test untuk membuktikan penyebab kegagalan.
type Experiment struct {
	ID         int64     `json:"id"`
	GroupID    string    `json:"group_id"`
	Kind       string    `json:"kind"`    // rerun / patch
	Outcome    string    `json:"outcome"` // passed / failed / rejected / skipped
	Category   string    `json:"category"`
	Hypothesis string    `json:"hypothesis"`
	Patch      string    `json:"patch"`
	Detail     string    `json:"detail"`
	Runs       int       `json:"runs"`
	Passes     int       `json:"passes"`
	CostUSD    float64   `json:"cost_usd"`
	RunBy      string    `json:"run_by"`
	CreatedAt  time.Time `json:"created_at"`
}

const experimentCols = `id, group_id, kind, outcome, category, hypothesis, patch, detail, runs, passes,
	cost_usd::float8, run_by, created_at`

func scanExperiment(row pgx.Row) (Experiment, error) {
	var e Experiment
	err := row.Scan(&e.ID, &e.GroupID, &e.Kind, &e.Outcome, &e.Category, &e.Hypothesis, &e.Patch, &e.Detail,
		&e.Runs, &e.Passes, &e.CostUSD, &e.RunBy, &e.CreatedAt)
	return e, err
}

// SaveExperiment menyimpan hasil eksperimen. Teks disamarkan dulu seperti pesan error.
func (s *Store) SaveExperiment(ctx context.Context, e Experiment) (int64, error) {
	if e.Category == "" {
		e.Category = "unknown"
	}
	var id int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO experiments (group_id, kind, outcome, category, hypothesis, patch, detail, runs, passes, cost_usd, run_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11) RETURNING id`,
		e.GroupID, e.Kind, e.Outcome, e.Category, triage.Clean(e.Hypothesis), triage.Clean(e.Patch), triage.Clean(e.Detail),
		e.Runs, e.Passes, e.CostUSD, e.RunBy,
	).Scan(&id)
	if isForeignKeyViolation(err) {
		return 0, ErrNotFound
	}
	return id, err
}

// ListExperiments mengembalikan eksperimen satu kelompok, terbaru dulu.
func (s *Store) ListExperiments(ctx context.Context, groupID string) ([]Experiment, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+experimentCols+` FROM experiments WHERE group_id = $1 ORDER BY id DESC LIMIT 20`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Experiment{}
	for rows.Next() {
		e, err := scanExperiment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// LatestProof mengembalikan bukti terkuat untuk satu kelompok, atau ErrNotFound:
// patch terbaru yang membuat test lulus, kalau tidak ada: rerun TERBARU kalau rerun itu lulus.
// Rerun lulus yang sudah disusul rerun gagal tidak dihitung, karena kegagalannya ternyata konsisten.
func (s *Store) LatestProof(ctx context.Context, groupID string) (Experiment, error) {
	e, err := scanExperiment(s.pool.QueryRow(ctx, `SELECT `+experimentCols+`
		FROM experiments e WHERE group_id = $1 AND outcome = 'passed' AND (kind = 'patch' OR
			id = (SELECT max(id) FROM experiments WHERE group_id = $1 AND kind = 'rerun'))
		ORDER BY (kind = 'patch') DESC, id DESC LIMIT 1`, groupID))
	if err == pgx.ErrNoRows {
		return Experiment{}, ErrNotFound
	}
	return e, err
}

// isForeignKeyViolation: kelompok yang dirujuk tidak ada.
func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}
