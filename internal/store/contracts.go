package store

import (
	"context"
	"crypto/md5"
	"encoding/hex"

	"github.com/jackc/pgx/v5"
)

// SaveRunContract menyimpan potret kontrak (sekali per isi) dan mencatatnya sebagai kontrak project itu di run ini.
func (s *Store) SaveRunContract(ctx context.Context, runID int64, project, sourceURL, document string) (string, error) {
	sum := md5.Sum([]byte(document))
	id := hex.EncodeToString(sum[:])
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO contracts (id, project, source_url, document) VALUES ($1, $2, $3, $4)
			ON CONFLICT (id) DO NOTHING`, id, project, sourceURL, document); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO run_contracts (run_id, project, contract_id) VALUES ($1, $2, $3)
			ON CONFLICT (run_id, project) DO UPDATE SET contract_id = EXCLUDED.contract_id`, runID, project, id)
		return err
	})
	return id, err
}

// RunContract mengembalikan id dan isi kontrak project pada run tertentu, atau ErrNotFound.
func (s *Store) RunContract(ctx context.Context, runID int64, project string) (id, document string, err error) {
	err = s.pool.QueryRow(ctx, `
		SELECT c.id, c.document FROM run_contracts r JOIN contracts c ON c.id = r.contract_id
		WHERE r.run_id = $1 AND r.project = $2`, runID, project).Scan(&id, &document)
	if err == pgx.ErrNoRows {
		return "", "", ErrNotFound
	}
	return id, document, err
}
