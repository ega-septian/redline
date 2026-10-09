package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ErrDuplicate dikembalikan kalau aturan dengan pola dan kategori yang sama sudah ada.
var ErrDuplicate = errors.New("duplicate")

// LearnedRule adalah aturan yang dipelajari dari kasus terbukti.
type LearnedRule struct {
	ID          int64      `json:"id"`
	Pattern     string     `json:"pattern"`
	Category    string     `json:"category"`
	Summary     string     `json:"summary"`
	NextStep    string     `json:"next_step"`
	Status      string     `json:"status"` // proposed / active / rejected
	LearnedFrom []string   `json:"learned_from"`
	Hits        int        `json:"hits"`
	AlsoMatches int        `json:"also_matches"`
	CostUSD     float64    `json:"cost_usd"`
	CreatedAt   time.Time  `json:"created_at"`
	DecidedBy   string     `json:"decided_by,omitempty"`
	DecidedAt   *time.Time `json:"decided_at"`
}

const ruleCols = `id, pattern, category, summary, next_step, status, learned_from, hits, also_matches,
	cost_usd::float8, created_at, decided_by, decided_at`

func scanRule(row pgx.Row) (LearnedRule, error) {
	var r LearnedRule
	err := row.Scan(&r.ID, &r.Pattern, &r.Category, &r.Summary, &r.NextStep, &r.Status, &r.LearnedFrom,
		&r.Hits, &r.AlsoMatches, &r.CostUSD, &r.CreatedAt, &r.DecidedBy, &r.DecidedAt)
	return r, err
}

// SaveRule menyimpan usulan aturan baru (status proposed).
func (s *Store) SaveRule(ctx context.Context, r LearnedRule) (int64, error) {
	if r.LearnedFrom == nil {
		r.LearnedFrom = []string{}
	}
	var id int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO learned_rules (pattern, category, summary, next_step, learned_from, hits, also_matches, cost_usd)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`,
		r.Pattern, r.Category, r.Summary, r.NextStep, r.LearnedFrom, r.Hits, r.AlsoMatches, r.CostUSD,
	).Scan(&id)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return 0, ErrDuplicate
	}
	return id, err
}

// ListRules mengembalikan aturan dengan status tertentu (kosong = semua), terbaru dulu.
func (s *Store) ListRules(ctx context.Context, status string) ([]LearnedRule, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+ruleCols+` FROM learned_rules
		WHERE $1 = '' OR status = $1 ORDER BY id DESC LIMIT 200`, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LearnedRule{}
	for rows.Next() {
		r, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ActiveRules mengembalikan aturan yang sudah disetujui, urut dari yang paling lama disetujui.
func (s *Store) ActiveRules(ctx context.Context) ([]LearnedRule, error) {
	rules, err := s.ListRules(ctx, "active")
	if err != nil {
		return nil, err
	}
	// ListRules urut terbaru dulu; aturan lama didahulukan supaya hasil stabil.
	for i, j := 0, len(rules)-1; i < j; i, j = i+1, j-1 {
		rules[i], rules[j] = rules[j], rules[i]
	}
	return rules, nil
}

// DecideRule mengaktifkan atau menolak aturan. status: active / rejected / proposed.
func (s *Store) DecideRule(ctx context.Context, id int64, status, by string) (LearnedRule, error) {
	r, err := scanRule(s.pool.QueryRow(ctx, `
		UPDATE learned_rules SET status = $2, decided_by = $3, decided_at = CASE WHEN $2 = 'proposed' THEN NULL ELSE now() END
		WHERE id = $1 RETURNING `+ruleCols, id, status, by))
	if err == pgx.ErrNoRows {
		return LearnedRule{}, ErrNotFound
	}
	return r, err
}

// Case adalah kegagalan yang penyebabnya sudah terbukti: dilabeli manusia, atau lulus setelah patch.
type Case struct {
	GroupID  string `json:"group_id"`
	Error    string `json:"error"`
	Category string `json:"category"`
	Source   string `json:"source"` // human / experiment
	Reason   string `json:"reason"` // catatan label, atau hipotesis yang terbukti
}

// ConfirmedCases mengembalikan kasus terbukti terbaru (lihat confirmedCasesSQL).
func (s *Store) ConfirmedCases(ctx context.Context, limit int) ([]Case, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, last_error, category, source, reason
		FROM (`+confirmedCasesSQL+`) cases ORDER BY last_seen_run DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Case{}
	for rows.Next() {
		var c Case
		if err := rows.Scan(&c.GroupID, &c.Error, &c.Category, &c.Source, &c.Reason); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GroupError adalah pesan error satu kelompok, untuk menguji aturan baru ke data lama.
type GroupError struct {
	GroupID string
	Error   string
}

// RecentGroupErrors mengembalikan pesan error kelompok terbaru (maksimal limit).
func (s *Store) RecentGroupErrors(ctx context.Context, limit int) ([]GroupError, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, last_error FROM failure_groups ORDER BY last_seen_run DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []GroupError{}
	for rows.Next() {
		var g GroupError
		if err := rows.Scan(&g.GroupID, &g.Error); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}
