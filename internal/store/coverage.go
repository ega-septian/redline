package store

import (
	"context"
	"encoding/json"
	"time"

	"redline/internal/report"
)

// ContractSnapshot is one stored OpenAPI document attached to a run.
type ContractSnapshot struct {
	RunID      int64     `json:"run_id"`
	At         time.Time `json:"at"`
	ContractID string    `json:"contract_id"`
	SourceURL  string    `json:"source_url,omitempty"`
	Document   string    `json:"-"`
}

// EndpointCall is one HTTP call observed in a stored result's response shape.
type EndpointCall struct {
	Method  string
	Path    string
	Status  int
	TestKey string
	At      time.Time
}

// ContractSnapshots returns the newest contract snapshots for a project, newest first.
func (s *Store) ContractSnapshots(ctx context.Context, project string, limit int) ([]ContractSnapshot, error) {
	if limit <= 0 || limit > 20 {
		limit = 2
	}
	res, err := s.query(ctx, `
		MATCH (run:Run)-[:UNDER {project: $project}]->(c:Contract)
		RETURN run.id AS runId, run.createdAt AS at, c.id AS contractId,
		       coalesce(c.sourceUrl, '') AS sourceUrl, c.document AS document
		ORDER BY run.id DESC LIMIT $limit`, map[string]any{"project": project, "limit": limit})
	if err != nil {
		return nil, err
	}
	out := make([]ContractSnapshot, 0, len(res.Records))
	for _, record := range res.Records {
		out = append(out, ContractSnapshot{
			RunID: recInt64(record, "runId"), At: local(recValue(record, "at")),
			ContractID: recString(record, "contractId"), SourceURL: recString(record, "sourceUrl"),
			Document: recString(record, "document"),
		})
	}
	return out, nil
}

// EndpointCalls returns every endpoint call recorded in result shapes for a project (or all projects).
func (s *Store) EndpointCalls(ctx context.Context, project string) ([]EndpointCall, error) {
	res, err := s.query(ctx, `
		MATCH (run:Run)-[:HAS]->(r:Result)-[:RETURNED]->(shape:Shape)
		MATCH (r)-[:OF]->(t:Test)
		WHERE ($project = '' OR coalesce(t.project, '') = $project)
		RETURN shape.shape AS calls, run.createdAt AS at, r.testKey AS testKey`,
		map[string]any{"project": project})
	if err != nil {
		return nil, err
	}
	out := []EndpointCall{}
	for _, record := range res.Records {
		var calls []report.HTTPCall
		if err := json.Unmarshal([]byte(recString(record, "calls")), &calls); err != nil {
			continue
		}
		at := local(recValue(record, "at"))
		testKey := recString(record, "testKey")
		for _, call := range calls {
			out = append(out, EndpointCall{
				Method: call.Method, Path: call.Path, Status: call.Status, TestKey: testKey, At: at,
			})
		}
	}
	return out, nil
}
