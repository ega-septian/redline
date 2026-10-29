package store

import (
	"context"
	"fmt"
	"time"
)

// RecordSpend mencatat biaya satu panggilan AI. Node-nya terpisah dari Analysis supaya analisis ulang
// (yang menimpa Analysis dengan id yang sama) tidak menghapus riwayat biaya.
func (s *Store) RecordSpend(ctx context.Context, costUSD float64) error {
	if costUSD <= 0 {
		return nil
	}
	if _, err := s.query(ctx, `CREATE (:AiSpend {at: datetime(), costUSD: $cost})`,
		map[string]any{"cost": costUSD}); err != nil {
		return fmt.Errorf("catat biaya AI: %w", err)
	}
	return nil
}

// SpendSince menjumlahkan biaya AI sejak `since`. Dipakai batas biaya harian.
func (s *Store) SpendSince(ctx context.Context, since time.Time) (float64, error) {
	res, err := s.query(ctx,
		`MATCH (s:AiSpend) WHERE s.at >= datetime($since) RETURN coalesce(sum(s.costUSD), 0.0) AS usd`,
		map[string]any{"since": since.Format(time.RFC3339)})
	if err != nil {
		return 0, fmt.Errorf("hitung biaya AI: %w", err)
	}
	if len(res.Records) == 0 {
		return 0, nil
	}
	usd, _ := res.Records[0].Get("usd")
	switch v := usd.(type) {
	case float64:
		return v, nil
	case int64:
		return float64(v), nil
	}
	return 0, nil
}
