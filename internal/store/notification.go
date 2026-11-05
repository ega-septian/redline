package store

import (
	"context"
	"time"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// NotificationDelivery is one final delivery attempt recorded for debugging and the Config UI. It is
// written after the retry policy has finished, so Attempts is the total number of tries.
type NotificationDelivery struct {
	At         time.Time `json:"at"`
	Event      string    `json:"event"`
	RunID      int64     `json:"run_id,omitempty"`
	OK         bool      `json:"ok"`
	StatusCode int       `json:"status_code,omitempty"`
	Attempts   int       `json:"attempts"`
	Error      string    `json:"error,omitempty"`
}

// RecordNotificationDelivery stores the outcome and keeps only the newest 100 records.
func (s *Store) RecordNotificationDelivery(ctx context.Context, delivery NotificationDelivery) error {
	if delivery.At.IsZero() {
		delivery.At = time.Now()
	}
	_, err := s.query(ctx, `
		CREATE (d:NotificationDelivery {
			at: datetime($at), event: $event, runId: $runId, ok: $ok,
			statusCode: $statusCode, attempts: $attempts, error: $error
		})
		WITH d
		MATCH (old:NotificationDelivery)
		WHERE old <> d
		WITH old ORDER BY old.at DESC SKIP 100
		DETACH DELETE old`,
		map[string]any{
			"at": delivery.At.UTC(), "event": delivery.Event, "runId": delivery.RunID,
			"ok": delivery.OK, "statusCode": delivery.StatusCode,
			"attempts": delivery.Attempts, "error": delivery.Error,
		})
	return err
}

// LastNotificationDelivery returns the newest record, or nil when nothing has been delivered yet.
func (s *Store) LastNotificationDelivery(ctx context.Context) (*NotificationDelivery, error) {
	res, err := s.query(ctx, `MATCH (d:NotificationDelivery) RETURN d ORDER BY d.at DESC LIMIT 1`, nil)
	if err != nil {
		return nil, err
	}
	if len(res.Records) == 0 {
		return nil, nil
	}
	node, _ := res.Records[0].Get("d")
	delivery := toNotificationDelivery(node.(neo4j.Node).Props)
	return &delivery, nil
}

func toNotificationDelivery(p map[string]any) NotificationDelivery {
	return NotificationDelivery{
		At: local(p["at"]), Event: str(p, "event"), RunID: i64(p, "runId"),
		OK: boolValue(p["ok"]), StatusCode: int(i64(p, "statusCode")),
		Attempts: int(i64(p, "attempts")), Error: str(p, "error"),
	}
}
