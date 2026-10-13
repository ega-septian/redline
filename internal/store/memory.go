package store

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// ---------- kontrak per run ----------

// SaveRunContract menyimpan potret kontrak (sekali per isi) dan mencatatnya sebagai kontrak project itu di run
// ini: (:Run)-[:UNDER {project}]->(:Contract). Kontrak lama project itu di run yang sama diganti.
func (s *Store) SaveRunContract(ctx context.Context, runID int64, project, sourceURL, document string) (string, error) {
	sum := md5.Sum([]byte(document))
	id := hex.EncodeToString(sum[:])
	_, err := s.write(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		res, err := tx.Run(ctx, `
			MATCH (run:Run {id: $runId})
			MERGE (c:Contract {id: $id}) ON CREATE SET c.project = $project, c.sourceUrl = $sourceUrl,
				c.document = $document, c.createdAt = datetime()
			WITH run, c
			OPTIONAL MATCH (run)-[old:UNDER {project: $project}]->()
			DELETE old
			WITH DISTINCT run, c
			CREATE (run)-[:UNDER {project: $project}]->(c)
			RETURN count(*) AS n`,
			map[string]any{"runId": runID, "id": id, "project": project, "sourceUrl": sourceURL, "document": document})
		if err != nil {
			return nil, err
		}
		rec, err := res.Single(ctx)
		if err != nil {
			return nil, err
		}
		if n, _ := rec.Get("n"); n.(int64) == 0 {
			return nil, ErrNotFound
		}
		return nil, nil
	})
	return id, err
}

// RunContract mengembalikan id dan isi kontrak project pada run tertentu, atau ErrNotFound.
func (s *Store) RunContract(ctx context.Context, runID int64, project string) (id, document string, err error) {
	res, err := s.query(ctx, `MATCH (:Run {id: $runId})-[:UNDER {project: $project}]->(c:Contract)
		RETURN c.id AS id, c.document AS document`, map[string]any{"runId": runID, "project": project})
	if err != nil {
		return "", "", err
	}
	if len(res.Records) == 0 {
		return "", "", ErrNotFound
	}
	p := res.Records[0].AsMap()
	return str(p, "id"), str(p, "document"), nil
}

// ---------- embedding dan kasus mirip ----------

// PendingEmbeddings mengembalikan kelompok yang belum punya embedding dari model ini.
// Kasus terbukti didahulukan karena merekalah yang dicari; sisanya yang terbaru.
func (s *Store) PendingEmbeddings(ctx context.Context, model string, limit int) ([]GroupError, error) {
	res, err := s.query(ctx, confirmedCasesCypher+`
		WITH collect(id) AS confirmed
		MATCH (f:Failure) WHERE coalesce(f.embeddingModel, '') <> $model
		RETURN f.id AS id, f.lastError AS error
		ORDER BY f.id IN confirmed DESC, f.lastSeenRun DESC LIMIT $limit`,
		map[string]any{"model": model, "limit": limit})
	if err != nil {
		return nil, err
	}
	out := []GroupError{}
	for _, rec := range res.Records {
		p := rec.AsMap()
		out = append(out, GroupError{GroupID: str(p, "id"), Error: str(p, "error")})
	}
	return out, nil
}

// SaveEmbeddings menyimpan embedding pesan error per kelompok, beserta model yang membuatnya.
func (s *Store) SaveEmbeddings(ctx context.Context, model string, ids []string, vectors [][]float32) error {
	if len(ids) != len(vectors) {
		return fmt.Errorf("jumlah id (%d) dan embedding (%d) berbeda", len(ids), len(vectors))
	}
	rows := make([]map[string]any, len(ids))
	for i, v := range vectors {
		vec := make([]float64, len(v))
		for j, x := range v {
			vec[j] = float64(x)
		}
		rows[i] = map[string]any{"id": ids[i], "embedding": vec}
	}
	_, err := s.query(ctx, `
		UNWIND $rows AS row
		MATCH (f:Failure {id: row.id})
		SET f.embedding = row.embedding, f.embeddingModel = $model`, map[string]any{"rows": rows, "model": model})
	return err
}

// SimilarCases mengembalikan sampai limit kasus terbukti (selain kelompok ini) yang embedding-nya paling dekat,
// dengan cosine similarity minimal minSimilarity. Hanya embedding dari model yang sama yang dibandingkan.
//
// vector.similarity.cosine di Neo4j mengembalikan (1 + cosine) / 2, bukan cosine biasa: dua vektor yang tidak
// berhubungan (tegak lurus) dapat 0.5, bukan 0. Di sini dikembalikan ke cosine biasa supaya ambang dan angka
// yang tampil sama dengan cosine biasa (seperti pgvector di v0.1.0).
//
// Perbandingannya eksak ke setiap kasus terbukti, tanpa vector index. Kasus terbukti jumlahnya sedikit, dan
// vector index bersifat perkiraan: ia mengambil k terdekat dari SEMUA kelompok lalu baru disaring, sehingga
// kasus terbukti bisa terlewat.
func (s *Store) SimilarCases(ctx context.Context, groupID, model string, minSimilarity float64, limit int) ([]SimilarCase, error) {
	res, err := s.query(ctx, confirmedCasesCypher+`
		MATCH (g:Failure {id: $id, embeddingModel: $model})
		MATCH (c:Failure {id: id, embeddingModel: $model}) WHERE id <> $id
		WITH id, error, category, source, reason, c,
		     2 * vector.similarity.cosine(c.embedding, g.embedding) - 1 AS sim
		WHERE sim >= $min
		RETURN id, c.project + ' › ' + c.file + ' › ' + c.title AS test, error, category, source, reason, sim
		ORDER BY sim DESC LIMIT $limit`,
		map[string]any{"id": groupID, "model": model, "min": minSimilarity, "limit": limit})
	if err != nil {
		return nil, err
	}
	out := []SimilarCase{}
	for _, rec := range res.Records {
		p := rec.AsMap()
		sim, _ := p["sim"].(float64)
		out = append(out, SimilarCase{Case: toCase(p), Test: str(p, "test"), Similarity: sim})
	}
	return out, nil
}

// ---------- retensi ----------

// Prune menghapus hasil test dari run yang lebih tua dari before, kecuali yang masih dibutuhkan analisis:
// hasil lulus terakhir tiap test (pembanding) serta kemunculan pertama dan terakhir tiap kelompok kegagalan.
// Bentuk response, kode test, kontrak, endpoint, dan file yang tidak dirujuk lagi ikut dihapus.
// Run dan kelompok kegagalan tidak disentuh: kecil, dan dirujuk oleh data lain.
func (s *Store) Prune(ctx context.Context, before time.Time) (PruneResult, error) {
	var out PruneResult
	_, err := s.write(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		count := func(what, cypher string, params map[string]any) (int64, error) {
			res, err := tx.Run(ctx, cypher, params)
			if err != nil {
				return 0, fmt.Errorf("hapus %s: %w", what, err)
			}
			rec, err := res.Single(ctx)
			if err != nil {
				return 0, fmt.Errorf("hapus %s: %w", what, err)
			}
			n, _ := rec.Get("n")
			return n.(int64), nil
		}
		var err error
		// before dikirim dalam UTC: Neo4j tidak mengenal zona waktu "Local" milik Go.
		if out.TestResults, err = count("hasil test lama", `
			CALL {
				MATCH (r:Result {status: 'passed'}) WITH r ORDER BY r.runId DESC
				WITH r.testKey AS k, collect(r)[0] AS keep RETURN keep
				UNION
				MATCH (r:Result) WHERE r.groupId <> '' WITH r ORDER BY r.runId ASC
				WITH r.groupId AS g, collect(r) AS rs RETURN rs[0] AS keep
				UNION
				MATCH (r:Result) WHERE r.groupId <> '' WITH r ORDER BY r.runId DESC
				WITH r.groupId AS g, collect(r) AS rs RETURN rs[0] AS keep
			}
			WITH collect(keep.id) AS keep
			MATCH (run:Run)-[:HAS]->(r:Result) WHERE run.createdAt < $before AND NOT r.id IN keep
			DETACH DELETE r
			RETURN count(*) AS n`, map[string]any{"before": before.UTC()}); err != nil {
			return nil, err
		}
		if out.Shapes, err = count("bentuk response yatim", `
			MATCH (x:Shape) WHERE NOT ()-[:RETURNED]->(x) DETACH DELETE x RETURN count(*) AS n`, nil); err != nil {
			return nil, err
		}
		if out.Sources, err = count("kode test yatim", `
			MATCH (x:SourceFile) WHERE NOT EXISTS { MATCH (r:Result) WHERE x.id IN r.sourceIds }
			DELETE x RETURN count(*) AS n`, nil); err != nil {
			return nil, err
		}
		for what, cypher := range map[string]string{
			"kontrak yatim": `MATCH (x:Contract) WHERE NOT ()-[:UNDER]->(x) DELETE x RETURN count(*) AS n`,
			// File yang masih ada di graph import (IMPORTS, VERSION) tetap disimpan: itu pengetahuan, bukan sisa.
			"endpoint yatim": `MATCH (x:Endpoint) WHERE NOT (x)--() DELETE x RETURN count(*) AS n`,
			"file yatim":     `MATCH (x:File) WHERE NOT (x)--() DELETE x RETURN count(*) AS n`,
		} {
			if _, err := count(what, cypher, nil); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	return out, err
}
