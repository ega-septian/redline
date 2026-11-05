package store

import (
	"context"
	"time"
)

// DismissQuarantineSuggestion snoozes the suggestion for one test until the given time. It is an
// upsert: dismissing again extends or shortens the same record.
func (s *Store) DismissQuarantineSuggestion(ctx context.Context, key string, until time.Time, by string) error {
	_, err := s.query(ctx, `
		MERGE (d:QuarantineSuggestionDismissal {key: $key})
		ON CREATE SET d.createdAt = datetime()
		SET d.until = datetime($until), d.by = $by, d.updatedAt = datetime()`,
		map[string]any{"key": key, "until": until.UTC(), "by": by})
	return err
}

// SuggestionDismissals returns the active dismissals (only records whose until is in the future).
func (s *Store) SuggestionDismissals(ctx context.Context) (map[string]time.Time, error) {
	res, err := s.query(ctx, `
		MATCH (d:QuarantineSuggestionDismissal)
		WHERE d.until > datetime()
		RETURN d.key AS key, d.until AS until`, nil)
	if err != nil {
		return nil, err
	}
	out := make(map[string]time.Time, len(res.Records))
	for _, record := range res.Records {
		out[recString(record, "key")] = local(recValue(record, "until"))
	}
	return out, nil
}
