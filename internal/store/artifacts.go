package store

import (
	"context"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// SaveArtifact menyimpan metadata file trace/screenshot yang sudah ditulis ke disk.
func (s *Store) SaveArtifact(ctx context.Context, a Artifact) (int64, error) {
	id, err := s.write(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		id, err := nextID(ctx, tx, "artifact")
		if err != nil {
			return int64(0), err
		}
		res, err := tx.Run(ctx, `
			MATCH (r:Run {id: $runId})
			CREATE (a:Artifact {id: $id, runId: $runId, testKey: $test, kind: $kind, name: $name,
				path: $path, contentType: $contentType, size: $size, createdAt: datetime()})
			CREATE (r)-[:HAS_ARTIFACT]->(a)
			RETURN a.id AS id`,
			map[string]any{"id": id, "runId": a.RunID, "test": a.TestKey, "kind": a.Kind, "name": a.Name,
				"path": a.Path, "contentType": a.ContentType, "size": a.Size})
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

// ListArtifacts mengembalikan semua artifact satu run, terlama dulu.
func (s *Store) ListArtifacts(ctx context.Context, runID int64) ([]Artifact, error) {
	res, err := s.query(ctx, `
		MATCH (:Run {id: $runId})-[:HAS_ARTIFACT]->(a:Artifact)
		RETURN a ORDER BY a.id`, map[string]any{"runId": runID})
	if err != nil {
		return nil, err
	}
	out := []Artifact{}
	for _, rec := range res.Records {
		node, _ := rec.Get("a")
		out = append(out, toArtifact(node.(neo4j.Node).Props))
	}
	return out, nil
}

// GetArtifact mengembalikan metadata satu artifact, atau ErrNotFound.
func (s *Store) GetArtifact(ctx context.Context, id int64) (Artifact, error) {
	res, err := s.query(ctx, `MATCH (a:Artifact {id: $id}) RETURN a`, map[string]any{"id": id})
	if err != nil {
		return Artifact{}, err
	}
	if len(res.Records) == 0 {
		return Artifact{}, ErrNotFound
	}
	node, _ := res.Records[0].Get("a")
	return toArtifact(node.(neo4j.Node).Props), nil
}

func toArtifact(p map[string]any) Artifact {
	return Artifact{
		ID: i64(p, "id"), RunID: i64(p, "runId"), TestKey: str(p, "testKey"), Kind: str(p, "kind"),
		Name: str(p, "name"), Path: str(p, "path"), ContentType: str(p, "contentType"),
		Size: i64(p, "size"), CreatedAt: local(p["createdAt"]),
	}
}
