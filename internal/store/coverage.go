package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"

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
	RunID   int64
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
// SetCoverageIgnore upserts one ignored status code and returns the stored record.
func (s *Store) SetCoverageIgnore(ctx context.Context, ignore CoverageIgnore) (CoverageIgnore, error) {
	key := ignore.Project + "\x00" + ignore.Method + "\x00" + ignore.Path + "\x00" + ignore.Status
	res, err := s.query(ctx, `
		MERGE (i:CoverageIgnore {uniqueKey: $key})
		ON CREATE SET i.createdAt = datetime()
		SET i.project = $project, i.method = $method, i.path = $path, i.status = $status,
			i.reason = $reason, i.by = $by
		RETURN i`,
		map[string]any{"key": key, "project": ignore.Project, "method": ignore.Method,
			"path": ignore.Path, "status": ignore.Status, "reason": ignore.Reason, "by": ignore.By})
	if err != nil {
		return CoverageIgnore{}, err
	}
	node, _ := res.Records[0].Get("i")
	return toCoverageIgnore(node.(neo4j.Node).Props), nil
}

// DeleteCoverageIgnore removes one ignored status. Missing rows are not an error (idempotent undo).
func (s *Store) DeleteCoverageIgnore(ctx context.Context, project, method, path, status string) error {
	key := project + "\x00" + method + "\x00" + path + "\x00" + status
	_, err := s.query(ctx, `MATCH (i:CoverageIgnore {uniqueKey: $key}) DELETE i`, map[string]any{"key": key})
	return err
}

// CoverageIgnores returns every ignore for a project, newest first.
func (s *Store) CoverageIgnores(ctx context.Context, project string) ([]CoverageIgnore, error) {
	res, err := s.query(ctx, `
		MATCH (i:CoverageIgnore {project: $project})
		RETURN i ORDER BY i.createdAt DESC, i.method, i.path, i.status`,
		map[string]any{"project": project})
	if err != nil {
		return nil, err
	}
	out := []CoverageIgnore{}
	for _, record := range res.Records {
		node, _ := record.Get("i")
		out = append(out, toCoverageIgnore(node.(neo4j.Node).Props))
	}
	return out, nil
}

func toCoverageIgnore(p map[string]any) CoverageIgnore {
	return CoverageIgnore{
		Project: str(p, "project"), Method: str(p, "method"), Path: str(p, "path"),
		Status: str(p, "status"), Reason: str(p, "reason"), By: str(p, "by"),
		CreatedAt: local(p["createdAt"]),
	}
}

func (s *Store) EndpointCalls(ctx context.Context, project string) ([]EndpointCall, error) {
	res, err := s.query(ctx, `
		MATCH (run:Run)-[:HAS]->(r:Result)-[:RETURNED]->(shape:Shape)
		MATCH (r)-[:OF]->(t:Test)
		WHERE ($project = '' OR coalesce(t.project, '') = $project)
		RETURN shape.shape AS calls, run.id AS runId, run.createdAt AS at, r.testKey AS testKey`,
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
				RunID: recInt64(record, "runId"), Method: call.Method, Path: call.Path,
				Status: call.Status, TestKey: testKey, At: at,
			})
		}
	}
	return out, nil
}
