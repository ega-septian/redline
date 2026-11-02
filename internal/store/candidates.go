package store

import (
	"context"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// SaveRuleCandidate menyimpan usulan AI sebagai draft. Pola + kategori + kelompok yang sama
// tidak dibuat dua kali; id yang sudah ada dikembalikan.
func (s *Store) SaveRuleCandidate(ctx context.Context, c RuleCandidate) (int64, error) {
	key := c.Fingerprint + "\x00" + c.Category + "\x00" + c.Pattern
	id, err := s.write(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		res, err := tx.Run(ctx, `MATCH (c:RuleCandidate {uniqueKey: $key}) RETURN c.id AS id LIMIT 1`,
			map[string]any{"key": key})
		if err != nil {
			return int64(0), err
		}
		if res.Next(ctx) {
			existing, _ := res.Record().Get("id")
			return existing.(int64), nil
		}
		if err := res.Err(); err != nil {
			return int64(0), err
		}

		id, err := nextID(ctx, tx, "rule_candidate")
		if err != nil {
			return int64(0), err
		}
		res, err = tx.Run(ctx, `
			MATCH (f:Failure {id: $fingerprint})
			CREATE (c:RuleCandidate {id: $id, uniqueKey: $key, fingerprint: $fingerprint, test: $test,
				pattern: $pattern, category: $category, summary: $summary, nextStep: $nextStep,
				status: 'draft', source: $source, model: $model, promptVersion: $promptVersion,
				confidence: $confidence, support: 0, alsoMatches: 0, backtestReason: '',
				createdAt: datetime()})-[:FROM_FAILURE]->(f)
			RETURN c.id AS id`,
			map[string]any{"id": id, "key": key, "fingerprint": c.Fingerprint, "test": c.Test,
				"pattern": c.Pattern, "category": c.Category, "summary": c.Summary, "nextStep": c.NextStep,
				"source": c.Source, "model": c.Model, "promptVersion": c.PromptVersion, "confidence": c.Confidence})
		if err != nil {
			return int64(0), err
		}
		if !res.Next(ctx) {
			return int64(0), ErrNotFound
		}
		return id, res.Err()
	})
	if err != nil {
		return 0, err
	}
	return id.(int64), nil
}

// ListRuleCandidates mengembalikan candidate terbaru dulu. fingerprint/status kosong = semua.
func (s *Store) ListRuleCandidates(ctx context.Context, fingerprint, status string) ([]RuleCandidate, error) {
	res, err := s.query(ctx, `
		MATCH (c:RuleCandidate)
		WHERE ($fingerprint = '' OR c.fingerprint = $fingerprint)
		  AND ($status = '' OR c.status = $status)
		RETURN c ORDER BY c.id DESC LIMIT 200`,
		map[string]any{"fingerprint": fingerprint, "status": status})
	if err != nil {
		return nil, err
	}
	out := []RuleCandidate{}
	for _, rec := range res.Records {
		node, _ := rec.Get("c")
		out = append(out, toRuleCandidate(node.(neo4j.Node).Props))
	}
	return out, nil
}

// MarkRuleCandidate menyimpan hasil backtest dan keputusan candidate. ruleID 0 = tidak ada rule.
func (s *Store) MarkRuleCandidate(ctx context.Context, id int64, status, reason string, ruleID int64, hits, also int) error {
	var promoted any
	if ruleID > 0 {
		promoted = ruleID
	}
	res, err := s.query(ctx, `
		MATCH (c:RuleCandidate {id: $id})
		SET c.status = $status, c.backtestReason = $reason, c.support = $hits, c.alsoMatches = $also,
			c.promotedRuleId = $promoted,
			c.provenAt = CASE WHEN $status IN ['promoted','rejected'] THEN datetime() ELSE c.provenAt END,
			c.decidedAt = CASE WHEN $status IN ['promoted','rejected'] THEN datetime() ELSE c.decidedAt END
		RETURN count(c) AS n`,
		map[string]any{"id": id, "status": status, "reason": reason, "promoted": promoted, "hits": hits, "also": also})
	if err != nil {
		return err
	}
	if n, _ := res.Records[0].Get("n"); n.(int64) == 0 {
		return ErrNotFound
	}
	return nil
}

func toRuleCandidate(p map[string]any) RuleCandidate {
	c := RuleCandidate{
		ID: i64(p, "id"), Fingerprint: str(p, "fingerprint"), Test: str(p, "test"),
		Pattern: str(p, "pattern"), Category: str(p, "category"), Summary: str(p, "summary"),
		NextStep: str(p, "nextStep"), Status: str(p, "status"), Source: str(p, "source"),
		Model: str(p, "model"), PromptVersion: str(p, "promptVersion"), Confidence: str(p, "confidence"),
		Support: int(i64(p, "support")), AlsoMatches: int(i64(p, "alsoMatches")),
		BacktestReason: str(p, "backtestReason"), CreatedAt: local(p["createdAt"]),
	}
	if v, ok := p["promotedRuleId"]; ok && v != nil {
		id := i64(p, "promotedRuleId")
		c.PromotedRuleID = &id
	}
	if v, ok := p["provenAt"]; ok && v != nil {
		t := local(v)
		c.ProvenAt = &t
	}
	if v, ok := p["decidedAt"]; ok && v != nil {
		t := local(v)
		c.DecidedAt = &t
	}
	return c
}
