package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// PruneResult adalah jumlah baris yang dihapus oleh Prune.
type PruneResult struct {
	TestResults int64 `json:"test_results"`
	Shapes      int64 `json:"response_shapes"`
	Sources     int64 `json:"source_files"`
}

// Prune menghapus detail test_results dari run yang lebih tua dari before, kecuali yang
// masih dibutuhkan analisis: hasil lulus terakhir tiap test (pembanding) serta kemunculan
// pertama dan terakhir tiap kelompok kegagalan. Bentuk response dan kode test yang tidak dipakai lagi ikut dihapus.
// runs dan failure_groups tidak disentuh: kecil, dan dirujuk oleh tabel lain.
func (s *Store) Prune(ctx context.Context, before time.Time) (PruneResult, error) {
	var res PruneResult
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			WITH keep AS (
				(SELECT DISTINCT ON (test_key) id FROM test_results
				 WHERE status = 'passed' ORDER BY test_key, run_id DESC)
				UNION
				(SELECT DISTINCT ON (group_id) id FROM test_results
				 WHERE group_id <> '' ORDER BY group_id, run_id ASC)
				UNION
				(SELECT DISTINCT ON (group_id) id FROM test_results
				 WHERE group_id <> '' ORDER BY group_id, run_id DESC)
			)
			DELETE FROM test_results t USING runs r
			WHERE r.id = t.run_id AND r.created_at < $1 AND t.id NOT IN (SELECT id FROM keep)`, before)
		if err != nil {
			return fmt.Errorf("hapus test_results lama: %w", err)
		}
		res.TestResults = tag.RowsAffected()

		tag, err = tx.Exec(ctx, `
			DELETE FROM response_shapes s
			WHERE NOT EXISTS (SELECT 1 FROM test_results t WHERE t.response_shape_id = s.id)`)
		if err != nil {
			return fmt.Errorf("hapus bentuk response yatim: %w", err)
		}
		res.Shapes = tag.RowsAffected()

		tag, err = tx.Exec(ctx, `
			DELETE FROM source_files f
			WHERE NOT EXISTS (SELECT 1 FROM test_results t WHERE f.id = ANY(t.source_ids))`)
		if err != nil {
			return fmt.Errorf("hapus kode test yatim: %w", err)
		}
		res.Sources = tag.RowsAffected()

		// Potret kontrak yang tidak dipakai run mana pun lagi (run dihapus).
		if _, err := tx.Exec(ctx, `
			DELETE FROM contracts c WHERE NOT EXISTS (SELECT 1 FROM run_contracts r WHERE r.contract_id = c.id)`); err != nil {
			return fmt.Errorf("hapus kontrak yatim: %w", err)
		}
		return nil
	})
	return res, err
}
