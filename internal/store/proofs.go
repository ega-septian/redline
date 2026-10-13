package store

import (
	"context"
	"errors"
	"slices"
	"sort"
	"time"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"

	"redline/internal/triage"
)

// ---------- eksperimen ----------

// SaveExperiment menyimpan hasil eksperimen. Teks disamarkan dulu seperti pesan error.
func (s *Store) SaveExperiment(ctx context.Context, e Experiment) (int64, error) {
	if e.Category == "" {
		e.Category = "unknown"
	}
	id, err := s.write(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		if ok, err := exists(ctx, tx, `MATCH (f:Failure {id: $id}) RETURN count(f) AS n`, e.GroupID); err != nil || !ok {
			return int64(0), errors.Join(err, notFoundUnless(err))
		}
		id, err := nextID(ctx, tx, "experiment")
		if err != nil {
			return int64(0), err
		}
		_, err = tx.Run(ctx, `
			MATCH (f:Failure {id: $groupId})
			CREATE (f)-[:TESTED_BY]->(:Experiment {id: $id, groupId: $groupId, kind: $kind, outcome: $outcome,
				category: $category, hypothesis: $hypothesis, patch: $patch, detail: $detail, runs: $runs,
				passes: $passes, costUSD: $costUSD, runBy: $runBy, createdAt: datetime()})`,
			map[string]any{"id": id, "groupId": e.GroupID, "kind": e.Kind, "outcome": e.Outcome, "category": e.Category,
				"hypothesis": triage.Clean(e.Hypothesis), "patch": triage.Clean(e.Patch), "detail": triage.Clean(e.Detail),
				"runs": e.Runs, "passes": e.Passes, "costUSD": e.CostUSD, "runBy": e.RunBy})
		return id, err
	})
	if err != nil {
		return 0, err
	}
	return id.(int64), nil
}

// exists menjalankan query count(...) AS n dengan parameter $id.
func exists(ctx context.Context, tx neo4j.ManagedTransaction, cypher, id string) (bool, error) {
	res, err := tx.Run(ctx, cypher, map[string]any{"id": id})
	if err != nil {
		return false, err
	}
	rec, err := res.Single(ctx)
	if err != nil {
		return false, err
	}
	n, _ := rec.Get("n")
	return n.(int64) > 0, nil
}

// notFoundUnless: ErrNotFound kalau tidak ada error lain.
func notFoundUnless(err error) error {
	if err != nil {
		return nil
	}
	return ErrNotFound
}

func toExperiment(node any) Experiment {
	p := node.(neo4j.Node).Props
	cost, _ := p["costUSD"].(float64)
	return Experiment{
		ID: i64(p, "id"), GroupID: str(p, "groupId"), Kind: str(p, "kind"), Outcome: str(p, "outcome"),
		Category: str(p, "category"), Hypothesis: str(p, "hypothesis"), Patch: str(p, "patch"), Detail: str(p, "detail"),
		Runs: int(i64(p, "runs")), Passes: int(i64(p, "passes")), CostUSD: cost, RunBy: str(p, "runBy"),
		CreatedAt: local(p["createdAt"]),
	}
}

// ListExperiments mengembalikan eksperimen satu kelompok, terbaru dulu.
func (s *Store) ListExperiments(ctx context.Context, groupID string) ([]Experiment, error) {
	res, err := s.query(ctx, `MATCH (e:Experiment {groupId: $id}) RETURN e ORDER BY e.id DESC LIMIT 20`,
		map[string]any{"id": groupID})
	if err != nil {
		return nil, err
	}
	out := []Experiment{}
	for _, rec := range res.Records {
		node, _ := rec.Get("e")
		out = append(out, toExperiment(node))
	}
	return out, nil
}

// provenCypher: bukti terkuat per kelompok yang lolos filter WHERE di depannya: patch yang lulus, atau
// rerun TERBARU kalau rerun itu lulus. Mengembalikan baris (groupId, e) urut dari bukti terkuat.
const provenCypher = `
	MATCH (e:Experiment {groupId: f.id, outcome: 'passed'})
	OPTIONAL MATCH (r:Experiment {groupId: f.id, kind: 'rerun'})
	WITH f, e, max(r.id) AS lastRerun
	WHERE e.kind = 'patch' OR e.id = lastRerun
	RETURN f.id AS groupId, e ORDER BY groupId, (e.kind = 'patch') DESC, e.id DESC`

// LatestProof mengembalikan bukti terkuat untuk satu kelompok, atau ErrNotFound.
func (s *Store) LatestProof(ctx context.Context, groupID string) (Experiment, error) {
	res, err := s.query(ctx, `MATCH (f:Failure {id: $id})`+provenCypher+` LIMIT 1`, map[string]any{"id": groupID})
	if err != nil {
		return Experiment{}, err
	}
	if len(res.Records) == 0 {
		return Experiment{}, ErrNotFound
	}
	node, _ := res.Records[0].Get("e")
	return toExperiment(node), nil
}

// ---------- tebakan dan rapor ----------

// SavePrediction mencatat tebakan aturan atau AI untuk rapor. Tebakan yang sama berturut-turut
// (kategori sama dengan tebakan terakhir dari sumber yang sama) tidak dicatat ulang.
func (s *Store) SavePrediction(ctx context.Context, a Analysis) error {
	if a.Source != "rule" && a.Source != "ai" {
		return nil
	}
	_, err := s.write(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		res, err := tx.Run(ctx, `
			MATCH (f:Failure {id: $groupId})
			OPTIONAL MATCH (p:Prediction {groupId: $groupId, source: $source})
			WITH f, p ORDER BY p.id DESC LIMIT 1
			RETURN count(f) AS n, p.category AS last`, map[string]any{"groupId": a.Fingerprint, "source": a.Source})
		if err != nil {
			return nil, err
		}
		if !res.Next(ctx) {
			return nil, res.Err() // kelompok tidak ada: tidak ada yang dinilai
		}
		if last, _ := res.Record().Get("last"); last == a.Category {
			return nil, nil
		}
		id, err := nextID(ctx, tx, "prediction")
		if err != nil {
			return nil, err
		}
		_, err = tx.Run(ctx, `
			MATCH (f:Failure {id: $groupId})
			CREATE (f)-[:PREDICTED]->(:Prediction {id: $id, groupId: $groupId, source: $source, category: $category,
				confidence: $confidence, promptVersion: $promptVersion, model: $model, createdAt: datetime()})`,
			map[string]any{"id": id, "groupId": a.Fingerprint, "source": a.Source, "category": a.Category,
				"confidence": a.Confidence, "promptVersion": a.PromptVersion, "model": a.Model})
		return nil, err
	})
	return err
}

type truth struct {
	category, by, test string
	at                 *time.Time
}

// Scores menilai tebakan terakhir tiap sumber yang dibuat SEBELUM jawabannya terbukti.
// Jawaban: label manual, kalau tidak ada: bukti eksperimen terkuat. groupID kosong = semua kelompok.
func (s *Store) Scores(ctx context.Context, groupID string, limit int) (*Scoreboard, error) {
	params := map[string]any{"id": groupID}
	truths := map[string]truth{}

	res, err := s.query(ctx, `
		MATCH (f:Failure) WHERE f.manualCategory IS NOT NULL AND ($id = '' OR f.id = $id)
		RETURN f.id AS id, f.manualCategory AS category, f.labeledAt AS at,
		       f.project + ' › ' + f.file + ' › ' + f.title AS test`, params)
	if err != nil {
		return nil, err
	}
	for _, rec := range res.Records {
		id, _ := rec.Get("id")
		category, _ := rec.Get("category")
		test, _ := rec.Get("test")
		t := truth{category: category.(string), by: "label", test: test.(string)}
		if at, _ := rec.Get("at"); at != nil {
			v := local(at)
			t.at = &v
		}
		truths[id.(string)] = t
	}

	res, err = s.query(ctx, `
		MATCH (f:Failure) WHERE f.manualCategory IS NULL AND ($id = '' OR f.id = $id)`+provenCypher, params)
	if err != nil {
		return nil, err
	}
	tests, err := s.testKeys(ctx, groupID)
	if err != nil {
		return nil, err
	}
	for _, rec := range res.Records {
		gid, _ := rec.Get("groupId")
		if _, done := truths[gid.(string)]; done {
			continue // baris pertama per kelompok adalah bukti terkuat
		}
		node, _ := rec.Get("e")
		e := toExperiment(node)
		category := "flaky"
		if e.Kind == "patch" {
			category = e.Category
		}
		at := e.CreatedAt
		truths[gid.(string)] = truth{category: category, by: "eksperimen", test: tests[gid.(string)], at: &at}
	}

	board := &Scoreboard{Sources: []Score{{Source: "rule"}, {Source: "ai"}}, Entries: []ScoreEntry{}}
	if len(truths) == 0 {
		return board, nil
	}
	ids := make([]string, 0, len(truths))
	for id := range truths {
		ids = append(ids, id)
	}
	res, err = s.query(ctx, `MATCH (p:Prediction) WHERE p.groupId IN $ids RETURN p ORDER BY p.id DESC`,
		map[string]any{"ids": ids})
	if err != nil {
		return nil, err
	}
	entries := []ScoreEntry{}
	seen := map[string]bool{}
	for _, rec := range res.Records {
		node, _ := rec.Get("p")
		p := node.(neo4j.Node).Props
		gid, source := str(p, "groupId"), str(p, "source")
		t := truths[gid]
		if seen[gid+" "+source] || (t.at != nil && local(p["createdAt"]).After(*t.at)) {
			continue // hanya tebakan terakhir sebelum jawaban terbukti
		}
		seen[gid+" "+source] = true
		entries = append(entries, ScoreEntry{GroupID: gid, Test: t.test, Source: source,
			Predicted: str(p, "category"), Confidence: str(p, "confidence"), Truth: t.category, TruthBy: t.by,
			Correct: str(p, "category") == t.category, ProvenAt: t.at})
	}
	// Terbaru dulu menurut waktu terbukti (yang tanpa waktu di akhir), lalu kelompok, lalu sumber.
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if (a.ProvenAt == nil) != (b.ProvenAt == nil) {
			return a.ProvenAt != nil
		}
		if a.ProvenAt != nil && !a.ProvenAt.Equal(*b.ProvenAt) {
			return a.ProvenAt.After(*b.ProvenAt)
		}
		if a.GroupID != b.GroupID {
			return a.GroupID < b.GroupID
		}
		return a.Source < b.Source
	})

	bySource := map[string]*Score{"rule": &board.Sources[0], "ai": &board.Sources[1]}
	for _, e := range entries {
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
	return board, nil
}

// testKeys: nama test per kelompok kegagalan.
func (s *Store) testKeys(ctx context.Context, groupID string) (map[string]string, error) {
	res, err := s.query(ctx, `MATCH (f:Failure) WHERE $id = '' OR f.id = $id
		RETURN f.id AS id, f.project + ' › ' + f.file + ' › ' + f.title AS test`, map[string]any{"id": groupID})
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, rec := range res.Records {
		id, _ := rec.Get("id")
		test, _ := rec.Get("test")
		out[id.(string)] = test.(string)
	}
	return out, nil
}

// ---------- aturan yang dipelajari ----------

func toRule(node any) LearnedRule {
	p := node.(neo4j.Node).Props
	cost, _ := p["costUSD"].(float64)
	r := LearnedRule{
		ID: i64(p, "id"), Pattern: str(p, "pattern"), Category: str(p, "category"), Summary: str(p, "summary"),
		NextStep: str(p, "nextStep"), Status: str(p, "status"), LearnedFrom: strs(p, "learnedFrom"),
		Hits: int(i64(p, "hits")), AlsoMatches: int(i64(p, "alsoMatches")), CostUSD: cost,
		CreatedAt: local(p["createdAt"]), DecidedBy: str(p, "decidedBy"),
	}
	if v, ok := p["decidedAt"]; ok && v != nil {
		t := local(v)
		r.DecidedAt = &t
	}
	return r
}

// SaveRule menyimpan usulan aturan baru (status proposed). Pola + kategori yang sama: ErrDuplicate.
func (s *Store) SaveRule(ctx context.Context, r LearnedRule) (int64, error) {
	if r.LearnedFrom == nil {
		r.LearnedFrom = []string{}
	}
	key := r.Category + "\x00" + r.Pattern
	id, err := s.write(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		if dup, err := exists(ctx, tx, `MATCH (r:Rule {uniqueKey: $id}) RETURN count(r) AS n`, key); err != nil || dup {
			return int64(0), errors.Join(err, duplicateUnless(err))
		}
		id, err := nextID(ctx, tx, "rule")
		if err != nil {
			return int64(0), err
		}
		_, err = tx.Run(ctx, `
			CREATE (:Rule {id: $id, uniqueKey: $key, pattern: $pattern, category: $category, summary: $summary,
				nextStep: $nextStep, status: 'proposed', learnedFrom: $learnedFrom, hits: $hits, alsoMatches: $alsoMatches,
				costUSD: $costUSD, createdAt: datetime(), decidedBy: ''})`,
			map[string]any{"id": id, "key": key, "pattern": r.Pattern, "category": r.Category, "summary": r.Summary,
				"nextStep": r.NextStep, "learnedFrom": r.LearnedFrom, "hits": r.Hits, "alsoMatches": r.AlsoMatches,
				"costUSD": r.CostUSD})
		return id, err
	})
	var neoErr *neo4j.Neo4jError
	if errors.As(err, &neoErr) && neoErr.Code == "Neo.ClientError.Schema.ConstraintValidationFailed" {
		return 0, ErrDuplicate // dua usulan yang sama masuk bersamaan
	}
	if err != nil {
		return 0, err
	}
	return id.(int64), nil
}

func duplicateUnless(err error) error {
	if err != nil {
		return nil
	}
	return ErrDuplicate
}

// ListRules mengembalikan aturan dengan status tertentu (kosong = semua), terbaru dulu.
func (s *Store) ListRules(ctx context.Context, status string) ([]LearnedRule, error) {
	res, err := s.query(ctx, `MATCH (r:Rule) WHERE $status = '' OR r.status = $status
		RETURN r ORDER BY r.id DESC LIMIT 200`, map[string]any{"status": status})
	if err != nil {
		return nil, err
	}
	out := []LearnedRule{}
	for _, rec := range res.Records {
		node, _ := rec.Get("r")
		out = append(out, toRule(node))
	}
	return out, nil
}

// ActiveRules mengembalikan aturan yang sudah disetujui, yang lama dulu supaya hasil stabil.
func (s *Store) ActiveRules(ctx context.Context) ([]LearnedRule, error) {
	rules, err := s.ListRules(ctx, "active")
	if err != nil {
		return nil, err
	}
	slices.Reverse(rules)
	return rules, nil
}

// DecideRule mengaktifkan atau menolak aturan. status: active / rejected / proposed.
func (s *Store) DecideRule(ctx context.Context, id int64, status, by string) (LearnedRule, error) {
	res, err := s.query(ctx, `
		MATCH (r:Rule {id: $id})
		SET r.status = $status, r.decidedBy = $by,
			r.decidedAt = CASE WHEN $status = 'proposed' THEN null ELSE datetime() END
		RETURN r`, map[string]any{"id": id, "status": status, "by": by})
	if err != nil {
		return LearnedRule{}, err
	}
	if len(res.Records) == 0 {
		return LearnedRule{}, ErrNotFound
	}
	node, _ := res.Records[0].Get("r")
	return toRule(node), nil
}

// confirmedCasesCypher: kasus yang penyebabnya sudah terbukti, sebagai subquery CALL { ... }.
// Label manual menang atas eksperimen (patch yang lulus, yang terbaru).
const confirmedCasesCypher = `
	CALL {
		MATCH (f:Failure) WHERE f.manualCategory IS NOT NULL
		RETURN f.id AS id, f.lastError AS error, f.manualCategory AS category, 'human' AS source,
		       f.manualNote AS reason, f.lastSeenRun AS lastSeen
		UNION ALL
		MATCH (f:Failure) WHERE f.manualCategory IS NULL
		MATCH (e:Experiment {groupId: f.id, kind: 'patch', outcome: 'passed'})
		WITH f, e ORDER BY e.id DESC
		WITH f, collect(e)[0] AS e
		RETURN f.id AS id, f.lastError AS error, e.category AS category, 'experiment' AS source,
		       e.hypothesis AS reason, f.lastSeenRun AS lastSeen
	}`

// ConfirmedCases mengembalikan kasus terbukti terbaru.
func (s *Store) ConfirmedCases(ctx context.Context, limit int) ([]Case, error) {
	res, err := s.query(ctx, confirmedCasesCypher+`
		RETURN id, error, category, source, reason ORDER BY lastSeen DESC LIMIT $limit`,
		map[string]any{"limit": limit})
	if err != nil {
		return nil, err
	}
	out := []Case{}
	for _, rec := range res.Records {
		out = append(out, toCase(rec.AsMap()))
	}
	return out, nil
}

func toCase(p map[string]any) Case {
	return Case{GroupID: str(p, "id"), Error: str(p, "error"), Category: str(p, "category"),
		Source: str(p, "source"), Reason: str(p, "reason")}
}

// RecentGroupErrors mengembalikan pesan error kelompok terbaru (maksimal limit).
func (s *Store) RecentGroupErrors(ctx context.Context, limit int) ([]GroupError, error) {
	res, err := s.query(ctx, `MATCH (f:Failure) RETURN f.id AS id, f.lastError AS error
		ORDER BY f.lastSeenRun DESC LIMIT $limit`, map[string]any{"limit": limit})
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
