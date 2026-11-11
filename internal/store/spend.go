package store

import (
	"context"
	"fmt"
	"time"
)

// RecordSpend mencatat biaya satu panggilan AI. Node-nya terpisah dari Analysis supaya analisis ulang
// (yang menimpa Analysis dengan id yang sama) tidak menghapus riwayat biaya.
func (s *Store) RecordSpend(ctx context.Context, project string, costUSD float64) error {
	if costUSD <= 0 {
		return nil
	}
	if _, err := s.query(ctx, `CREATE (:AiSpend {at: datetime(), costUSD: $cost, project: $project})`,
		map[string]any{"cost": costUSD, "project": project}); err != nil {
		return fmt.Errorf("record AI cost: %w", err)
	}
	return nil
}

// SpendSince menjumlahkan biaya AI sejak `since`. Dipakai batas biaya harian.
func (s *Store) SpendSince(ctx context.Context, since time.Time) (float64, error) {
	res, err := s.query(ctx,
		`MATCH (s:AiSpend) WHERE s.at >= datetime($since) RETURN coalesce(sum(s.costUSD), 0.0) AS usd`,
		map[string]any{"since": since.Format(time.RFC3339)})
	if err != nil {
		return 0, fmt.Errorf("calculate AI cost: %w", err)
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

// SpendSinceProject sums AI cost for one project since `since`. Old rows without a project are
// intentionally excluded; they only contribute to the global budget.
func (s *Store) SpendSinceProject(ctx context.Context, since time.Time, project string) (float64, error) {
	res, err := s.query(ctx,
		`MATCH (s:AiSpend) WHERE s.at >= datetime($since) AND s.project = $project
		 RETURN coalesce(sum(s.costUSD), 0.0) AS usd`,
		map[string]any{"since": since.Format(time.RFC3339), "project": project})
	if err != nil {
		return 0, fmt.Errorf("calculate project AI cost: %w", err)
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
