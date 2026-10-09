package store

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// CodeLink: test lain di run yang sama yang memakai endpoint atau file yang sama dengan test yang gagal.
// Kalau test lain itu lulus, masalahnya condong ke test ini; kalau ikut gagal, condong ke bagian bersama.
type CodeLink struct {
	Kind   string `json:"kind"` // endpoint / file
	Name   string `json:"name"` // "GET /brands" atau "tests/api/toolshop/schemas/brand.schema.ts"
	Passed int    `json:"passed"`
	Failed int    `json:"failed"`
}

// SimilarCase adalah kasus terbukti lain yang pesan error-nya mirip maknanya (embedding).
type SimilarCase struct {
	Case
	Test       string  `json:"test"`
	Similarity float64 `json:"similarity"` // cosine similarity, 0 sampai 1
}

// confirmedCasesSQL: kasus yang penyebabnya sudah terbukti. Label manual menang atas eksperimen.
const confirmedCasesSQL = `
	SELECT g.id, ` + testKeyG + ` AS test, g.last_error, g.manual_category AS category, 'human' AS source,
	       g.manual_note AS reason, g.last_seen_run
	FROM failure_groups g WHERE g.manual_category IS NOT NULL
	UNION ALL
	SELECT * FROM (
		SELECT DISTINCT ON (e.group_id) g.id, ` + testKeyG + `, g.last_error, e.category, 'experiment', e.hypothesis, g.last_seen_run
		FROM experiments e JOIN failure_groups g ON g.id = e.group_id
		WHERE e.kind = 'patch' AND e.outcome = 'passed' AND g.manual_category IS NULL
		ORDER BY e.group_id, e.id DESC
	) proven`

// PendingEmbeddings mengembalikan kelompok yang belum punya embedding dari model ini.
// Kasus terbukti didahulukan karena merekalah yang dicari; sisanya yang terbaru.
func (s *Store) PendingEmbeddings(ctx context.Context, model string, limit int) ([]GroupError, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT g.id, g.last_error FROM failure_groups g
		WHERE g.embedding_model <> $1
		ORDER BY (g.id IN (SELECT id FROM (`+confirmedCasesSQL+`) c)) DESC, g.last_seen_run DESC
		LIMIT $2`, model, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []GroupError{}
	for rows.Next() {
		var g GroupError
		if err := rows.Scan(&g.GroupID, &g.Error); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// SaveEmbeddings menyimpan embedding beberapa kelompok sekaligus. vectors sejajar dengan ids.
func (s *Store) SaveEmbeddings(ctx context.Context, model string, ids []string, vectors [][]float32) error {
	if len(ids) != len(vectors) {
		return fmt.Errorf("jumlah id (%d) dan embedding (%d) berbeda", len(ids), len(vectors))
	}
	literals := make([]string, len(vectors))
	for i, v := range vectors {
		literals[i] = vectorLiteral(v)
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE failure_groups g SET embedding = v.e::vector, embedding_model = $1
		FROM unnest($2::text[], $3::text[]) AS v(id, e) WHERE g.id = v.id`, model, ids, literals)
	return err
}

// vectorLiteral menulis embedding dalam format teks pgvector: [0.1,0.2,...].
func vectorLiteral(v []float32) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, x := range v {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(x), 'g', -1, 32))
	}
	b.WriteByte(']')
	return b.String()
}

// SimilarCases mengembalikan sampai limit kasus terbukti (selain kelompok ini) yang embedding-nya paling
// dekat, dengan cosine similarity minimal minSimilarity. Hanya embedding dari model yang sama yang dibandingkan.
func (s *Store) SimilarCases(ctx context.Context, groupID, model string, minSimilarity float64, limit int) ([]SimilarCase, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT c.id, c.test, c.last_error, c.category, c.source, c.reason,
		       (1 - (cg.embedding <=> g.embedding))::float8 AS sim
		FROM (`+confirmedCasesSQL+`) c
		JOIN failure_groups cg ON cg.id = c.id
		JOIN failure_groups g ON g.id = $1
		WHERE c.id <> $1 AND g.embedding_model = $2 AND cg.embedding_model = $2
		  AND 1 - (cg.embedding <=> g.embedding) >= $3
		ORDER BY sim DESC LIMIT $4`, groupID, model, minSimilarity, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SimilarCase{}
	for rows.Next() {
		var c SimilarCase
		if err := rows.Scan(&c.GroupID, &c.Test, &c.Error, &c.Category, &c.Source, &c.Reason, &c.Similarity); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// codeMap: untuk setiap endpoint yang dipanggil dan file yang dipakai test ini, hitung test LAIN di run
// yang sama yang ikut memakainya, lulus atau gagal.
func (s *Store) codeMap(ctx context.Context, runID int64, testKey string, endpoints, files []string) ([]CodeLink, error) {
	out := []CodeLink{}
	if len(endpoints) > 0 {
		rows, err := s.pool.Query(ctx, `
			SELECT e.name,
			       count(DISTINCT t.test_key) FILTER (WHERE t.status IN ('passed', 'flaky')),
			       count(DISTINCT t.test_key) FILTER (WHERE t.status = 'failed')
			FROM test_results t
			JOIN response_shapes s ON s.id = t.response_shape_id
			CROSS JOIN LATERAL (
				SELECT DISTINCT (c->>'method') || ' ' || (c->>'path') AS name FROM jsonb_array_elements(s.shape) c
			) e
			WHERE t.run_id = $1 AND t.test_key <> $2 AND e.name = ANY($3)
			GROUP BY e.name ORDER BY e.name`, runID, testKey, endpoints)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			l := CodeLink{Kind: "endpoint"}
			if err := rows.Scan(&l.Name, &l.Passed, &l.Failed); err != nil {
				rows.Close()
				return nil, err
			}
			out = append(out, l)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	if len(files) > 0 {
		rows, err := s.pool.Query(ctx, `
			SELECT f,
			       count(*) FILTER (WHERE t.status IN ('passed', 'flaky')),
			       count(*) FILTER (WHERE t.status = 'failed')
			FROM test_results t CROSS JOIN LATERAL unnest(t.code_files) f
			WHERE t.run_id = $1 AND t.test_key <> $2 AND f = ANY($3)
			GROUP BY f ORDER BY f`, runID, testKey, files)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			l := CodeLink{Kind: "file"}
			if err := rows.Scan(&l.Name, &l.Passed, &l.Failed); err != nil {
				return nil, err
			}
			out = append(out, l)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// SavePrediction mencatat tebakan aturan atau AI untuk rapor. Tebakan yang sama berturut-turut
// (kategori sama dengan tebakan terakhir dari sumber yang sama) tidak dicatat ulang.
func (s *Store) SavePrediction(ctx context.Context, a Analysis) error {
	if a.Source != "rule" && a.Source != "ai" {
		return nil
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO predictions (group_id, source, category, confidence, prompt_version, model)
		SELECT $1, $2, $3, $4, $5, $6
		WHERE NOT EXISTS (
			SELECT 1 FROM (SELECT category FROM predictions WHERE group_id = $1 AND source = $2 ORDER BY id DESC LIMIT 1) last
			WHERE last.category = $3)`,
		a.Fingerprint, a.Source, a.Category, a.Confidence, a.PromptVersion, a.Model)
	return err
}

// ScoreEntry: satu tebakan yang sudah bisa dinilai karena jawabannya sudah terbukti.
type ScoreEntry struct {
	GroupID    string     `json:"group_id"`
	Test       string     `json:"test"`
	Source     string     `json:"source"` // rule / ai
	Predicted  string     `json:"predicted"`
	Confidence string     `json:"confidence"`
	Truth      string     `json:"truth"`
	TruthBy    string     `json:"truth_by"` // label / eksperimen
	Correct    bool       `json:"correct"`
	ProvenAt   *time.Time `json:"proven_at"`
}

// Score adalah ringkasan akurasi satu sumber. Tebakan "unknown" dihitung terpisah (tidak menebak).
type Score struct {
	Source   string  `json:"source"`
	Graded   int     `json:"graded"` // tebakan selain unknown yang sudah ada jawabannya
	Correct  int     `json:"correct"`
	Unknown  int     `json:"unknown"`
	Accuracy float64 `json:"accuracy"` // correct / graded, 0 kalau graded 0
}

type Scoreboard struct {
	Sources []Score      `json:"sources"`
	Entries []ScoreEntry `json:"entries"` // terbaru dulu
}

// Scores menilai tebakan terakhir tiap sumber yang dibuat SEBELUM jawabannya terbukti.
// groupID kosong = semua kelompok.
func (s *Store) Scores(ctx context.Context, groupID string, limit int) (*Scoreboard, error) {
	rows, err := s.pool.Query(ctx, `
		WITH truth AS (
			SELECT g.id, g.manual_category AS category, 'label' AS by, g.labeled_at AS at
			FROM failure_groups g WHERE g.manual_category IS NOT NULL
			UNION ALL
			SELECT * FROM (
				SELECT DISTINCT ON (e.group_id) e.group_id,
				       CASE WHEN e.kind = 'patch' THEN e.category ELSE 'flaky' END, 'eksperimen', e.created_at
				FROM experiments e JOIN failure_groups g ON g.id = e.group_id
				WHERE g.manual_category IS NULL AND e.outcome = 'passed' AND (e.kind = 'patch' OR
					e.id = (SELECT max(x.id) FROM experiments x WHERE x.group_id = e.group_id AND x.kind = 'rerun'))
				ORDER BY e.group_id, (e.kind = 'patch') DESC, e.id DESC
			) proven
		),
		guess AS (
			SELECT DISTINCT ON (p.group_id, p.source) p.group_id, p.source, p.category, p.confidence
			FROM predictions p JOIN truth t ON t.id = p.group_id
			WHERE t.at IS NULL OR p.created_at <= t.at
			ORDER BY p.group_id, p.source, p.id DESC
		)
		SELECT gs.group_id, `+testKeyG+`, gs.source, gs.category, gs.confidence, t.category, t.by, t.at
		FROM guess gs JOIN truth t ON t.id = gs.group_id JOIN failure_groups g ON g.id = gs.group_id
		WHERE $1 = '' OR gs.group_id = $1
		ORDER BY t.at DESC NULLS LAST, gs.group_id, gs.source`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	board := &Scoreboard{Sources: []Score{{Source: "rule"}, {Source: "ai"}}, Entries: []ScoreEntry{}}
	bySource := map[string]*Score{"rule": &board.Sources[0], "ai": &board.Sources[1]}
	for rows.Next() {
		var e ScoreEntry
		if err := rows.Scan(&e.GroupID, &e.Test, &e.Source, &e.Predicted, &e.Confidence, &e.Truth, &e.TruthBy, &e.ProvenAt); err != nil {
			return nil, err
		}
		e.Correct = e.Predicted == e.Truth
		sc := bySource[e.Source]
		if e.Predicted == "unknown" {
			sc.Unknown++
		} else {
			sc.Graded++
			if e.Correct {
				sc.Correct++
			}
		}
		if limit <= 0 || len(board.Entries) < limit {
			board.Entries = append(board.Entries, e)
		}
	}
	for i := range board.Sources {
		if sc := &board.Sources[i]; sc.Graded > 0 {
			sc.Accuracy = float64(sc.Correct) / float64(sc.Graded)
		}
	}
	return board, rows.Err()
}
