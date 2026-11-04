package store

import (
	"context"
	"time"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// Quarantine is an active exception for one test, with an owner and an expiry.
type Quarantine struct {
	Key        string     `json:"key"`
	TestKey    string     `json:"test,omitempty"`
	TestCaseID string     `json:"test_case_id,omitempty"`
	Project    string     `json:"project,omitempty"`
	Owner      string     `json:"owner"`
	Reason     string     `json:"reason"`
	By         string     `json:"by"`
	CreatedAt  time.Time  `json:"created_at"`
	Until      time.Time  `json:"until"`
	ReleasedAt *time.Time `json:"released_at,omitempty"`
	ReleasedBy string     `json:"released_by,omitempty"`
}

// TestIdentity is the stable identity Redline uses for history and quarantine.
type TestIdentity struct {
	Key        string `json:"key"`
	TestKey    string `json:"test"`
	TestCaseID string `json:"test_case_id,omitempty"`
	Project    string `json:"project,omitempty"`
}

// FindTest resolves a @TC- ID or a test key to the stable identity.
func (s *Store) FindTest(ctx context.Context, test string) (TestIdentity, error) {
	res, err := s.query(ctx, `
		MATCH (t:Test)
		WHERE t.key = $test OR t.caseId = $test
		RETURN t.key AS testKey, coalesce(t.caseId, '') AS caseId, coalesce(t.project, '') AS project
		ORDER BY t.key LIMIT 1`, map[string]any{"test": test})
	if err != nil {
		return TestIdentity{}, err
	}
	if len(res.Records) == 0 {
		return TestIdentity{}, ErrNotFound
	}
	record := res.Records[0]
	testKey := recString(record, "testKey")
	caseID := recString(record, "caseId")
	key := caseID
	if key == "" {
		key = testKey
	}
	return TestIdentity{Key: key, TestKey: testKey, TestCaseID: caseID, Project: recString(record, "project")}, nil
}

// SetQuarantine creates or refreshes the active quarantine for one test.
func (s *Store) SetQuarantine(ctx context.Context, q Quarantine) (Quarantine, error) {
	res, err := s.query(ctx, `
		MERGE (q:Quarantine {key: $key})
		ON CREATE SET q.createdAt = datetime()
		SET q.testKey = $testKey, q.caseId = $caseId, q.project = $project,
		    q.owner = $owner, q.reason = $reason, q.by = $by,
		    q.until = datetime($until), q.releasedAt = null, q.releasedBy = ''
		RETURN q`,
		map[string]any{
			"key": q.Key, "testKey": q.TestKey, "caseId": q.TestCaseID, "project": q.Project,
			"owner": q.Owner, "reason": q.Reason, "by": q.By, "until": q.Until.UTC(),
		})
	if err != nil {
		return Quarantine{}, err
	}
	node, _ := res.Records[0].Get("q")
	return toQuarantine(node.(neo4j.Node).Props), nil
}

// ReleaseQuarantine ends the active quarantine for a test.
func (s *Store) ReleaseQuarantine(ctx context.Context, key, by string) (Quarantine, error) {
	res, err := s.query(ctx, `
		MATCH (q:Quarantine {key: $key}) WHERE q.releasedAt IS NULL
		SET q.releasedAt = datetime(), q.releasedBy = $by
		RETURN q`, map[string]any{"key": key, "by": by})
	if err != nil {
		return Quarantine{}, err
	}
	if len(res.Records) == 0 {
		return Quarantine{}, ErrNotFound
	}
	node, _ := res.Records[0].Get("q")
	return toQuarantine(node.(neo4j.Node).Props), nil
}

// ActiveQuarantines returns unreleased quarantines whose expiry is in the future.
func (s *Store) ActiveQuarantines(ctx context.Context) ([]Quarantine, error) {
	res, err := s.query(ctx, `
		MATCH (q:Quarantine) WHERE q.releasedAt IS NULL AND q.until > datetime()
		RETURN q ORDER BY q.until`, nil)
	if err != nil {
		return nil, err
	}
	out := make([]Quarantine, 0, len(res.Records))
	for _, record := range res.Records {
		node, _ := record.Get("q")
		out = append(out, toQuarantine(node.(neo4j.Node).Props))
	}
	return out, nil
}

func toQuarantine(p map[string]any) Quarantine {
	q := Quarantine{
		Key: str(p, "key"), TestKey: str(p, "testKey"), TestCaseID: str(p, "caseId"),
		Project: str(p, "project"), Owner: str(p, "owner"), Reason: str(p, "reason"),
		By: str(p, "by"), CreatedAt: local(p["createdAt"]), Until: local(p["until"]),
		ReleasedBy: str(p, "releasedBy"),
	}
	if released, ok := p["releasedAt"]; ok {
		at := local(released)
		q.ReleasedAt = &at
	}
	return q
}

func applyQuarantine(tests []TestHealthRow, active []Quarantine) {
	byKey := make(map[string]Quarantine, len(active))
	for _, q := range active {
		byKey[q.Key] = q
	}
	for i := range tests {
		q, ok := byKey[tests[i].Key]
		if !ok {
			continue
		}
		until := q.Until
		tests[i].Quarantined = true
		tests[i].QuarantineOwner = q.Owner
		tests[i].QuarantineReason = q.Reason
		tests[i].QuarantineUntil = &until
	}
}
