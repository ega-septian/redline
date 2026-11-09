package store

import (
	"context"
	"fmt"
	"time"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// LoadErrorStatuses are the triage states for a suite load failure. A load error has no test result,
// so it is resolved by a person, not by a passing run.
var LoadErrorStatuses = []string{"open", "acknowledged", "in_progress", "muted", "wont_fix", "resolved"}

// ValidLoadErrorStatus reports whether status is a known load-error triage state.
func ValidLoadErrorStatus(status string) bool {
	for _, item := range LoadErrorStatuses {
		if status == item {
			return true
		}
	}
	return false
}

// LoadError is a report-level error aggregated across runs. Unlike a Failure, it has no test key:
// the suite (or global setup) failed before any test could produce a result.
type LoadError struct {
	ID              string     `json:"id"`
	Message         string     `json:"message"`
	Occurrences     int        `json:"occurrences"`
	FirstSeenRun    int64      `json:"first_seen_run"`
	LastSeenRun     int64      `json:"last_seen_run"`
	FirstSeenAt     time.Time  `json:"first_seen_at"`
	LastSeenAt      time.Time  `json:"last_seen_at"`
	TriageStatus    string     `json:"triage_status"`
	TriageOwner     string     `json:"triage_owner,omitempty"`
	TriageNote      string     `json:"triage_note,omitempty"`
	TriageUpdatedBy string     `json:"triage_updated_by,omitempty"`
	TriageUpdatedAt *time.Time `json:"triage_updated_at,omitempty"`
}

// LoadErrors returns the newest load-error groups, most recently seen first.
func (s *Store) LoadErrors(ctx context.Context, limit int) ([]LoadError, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	res, err := s.query(ctx, `
		MATCH (e:LoadError) RETURN e ORDER BY e.lastSeenAt DESC LIMIT $limit`,
		map[string]any{"limit": limit})
	if err != nil {
		return nil, err
	}
	out := make([]LoadError, 0, len(res.Records))
	for _, record := range res.Records {
		node, _ := record.Get("e")
		out = append(out, toLoadError(node.(neo4j.Node).Props))
	}
	return out, nil
}

// SetLoadErrorTriage updates the owner/status/note for one load-error group.
func (s *Store) SetLoadErrorTriage(ctx context.Context, id, status, owner, note, by string) error {
	if !ValidLoadErrorStatus(status) {
		return fmt.Errorf("status triage %q tidak valid", status)
	}
	res, err := s.query(ctx, `
		MATCH (e:LoadError {id: $id})
		SET e.triageStatus = $status, e.triageOwner = $owner, e.triageNote = $note,
		    e.triageUpdatedBy = $by, e.triageUpdatedAt = datetime()
		RETURN count(e) AS n`,
		map[string]any{"id": id, "status": status, "owner": owner, "note": note, "by": by})
	if err != nil {
		return err
	}
	if len(res.Records) == 0 || recInt64(res.Records[0], "n") == 0 {
		return ErrNotFound
	}
	return nil
}

func toLoadError(p map[string]any) LoadError {
	item := LoadError{
		ID: str(p, "id"), Message: str(p, "message"), Occurrences: int(i64(p, "occurrences")),
		FirstSeenRun: i64(p, "firstSeenRun"), LastSeenRun: i64(p, "lastSeenRun"),
		TriageStatus: str(p, "triageStatus"), TriageOwner: str(p, "triageOwner"),
		TriageNote: str(p, "triageNote"), TriageUpdatedBy: str(p, "triageUpdatedBy"),
	}
	item.FirstSeenAt = local(p["firstSeenAt"])
	item.LastSeenAt = local(p["lastSeenAt"])
	if item.TriageStatus == "" {
		item.TriageStatus = "open"
	}
	if value, ok := p["triageUpdatedAt"]; ok && value != nil {
		at := local(value)
		item.TriageUpdatedAt = &at
	}
	return item
}
