package store

import (
	"context"
	"fmt"
	"slices"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"

	"redline/internal/report"
	"redline/internal/triage"
)

// Facts mengumpulkan bukti untuk satu kelompok kegagalan, yaitu:
// kemunculan gagal terakhir, kode test, peta kode, pembanding run lulus terakhir, dan riwayat status.
func (s *Store) Facts(ctx context.Context, fingerprint string) (*Facts, error) {
	res, err := s.query(ctx, `MATCH (f:Failure {id: $id})`+failureReturn, map[string]any{"id": fingerprint})
	if err != nil {
		return nil, err
	}
	if len(res.Records) == 0 {
		return nil, ErrNotFound
	}
	g := toGroup(res.Records[0])
	f := &Facts{Group: g, RecentStatuses: []string{}, CodeFiles: []string{}, Similar: []SimilarCase{}}

	// Kemunculan gagal terakhir: pesan error, kode test, dan bentuk response saat itu.
	res, err = s.query(ctx, `
		MATCH (r:Result {groupId: $id, status: 'failed'})
		WITH r ORDER BY r.runId DESC LIMIT 1
		OPTIONAL MATCH (r)-[:RETURNED]->(shape:Shape)
		RETURN r, shape.shape AS shape`, map[string]any{"id": fingerprint})
	if err != nil {
		return nil, fmt.Errorf("kemunculan terakhir: %w", err)
	}
	var curHash string
	var sourceIDs []string
	if len(res.Records) > 0 {
		node, _ := res.Records[0].Get("r")
		p := node.(neo4j.Node).Props
		f.FailRunID = i64(p, "runId")
		f.ErrorMessage, f.ErrorSnippet, f.ErrorLocation = str(p, "errorMessage"), str(p, "errorSnippet"), str(p, "errorLocation")
		curHash, f.CodeFiles, sourceIDs = str(p, "testCodeHash"), strs(p, "codeFiles"), strs(p, "sourceIds")
		shape, _ := res.Records[0].Get("shape")
		f.CurrentCalls = decode(shape)
	} else {
		f.CurrentCalls = []report.HTTPCall{}
	}
	if f.Sources, err = s.sourceFiles(ctx, sourceIDs); err != nil {
		return nil, fmt.Errorf("kode test: %w", err)
	}

	endpoints := []string{}
	for _, c := range f.CurrentCalls {
		endpoints = append(endpoints, c.Method+" "+c.Path)
	}
	if f.CodeMap, err = s.codeMap(ctx, f.FailRunID, g.TestKey, endpoints, f.CodeFiles); err != nil {
		return nil, fmt.Errorf("peta kode: %w", err)
	}

	// Pembanding: run terakhir test ini lulus sebelum kegagalan sekarang.
	res, err = s.query(ctx, `
		MATCH (r:Result {testKey: $key, status: 'passed'}) WHERE r.runId < $before
		WITH r ORDER BY r.runId DESC LIMIT 1
		MATCH (run:Run {id: r.runId})
		OPTIONAL MATCH (r)-[:RETURNED]->(shape:Shape)
		RETURN run, r.testCodeHash AS hash, shape.shape AS shape`,
		map[string]any{"key": g.TestKey, "before": g.LastSeenRun})
	if err != nil {
		return nil, fmt.Errorf("lulus terakhir: %w", err)
	}
	var passHash string
	passCalls := []report.HTTPCall{}
	if len(res.Records) > 0 {
		ref := runRef(res.Records[0])
		f.LastPass = &ref
		hash, _ := res.Records[0].Get("hash")
		passHash, _ = hash.(string)
		shape, _ := res.Records[0].Get("shape")
		passCalls = decode(shape)
	}
	f.TestChanged = triage.CompareHash(passHash, curHash)
	f.Response = triage.DiffCalls(passCalls, f.CurrentCalls)

	after := int64(0)
	if f.LastPass != nil {
		after = f.LastPass.RunID
	}
	res, err = s.query(ctx, `
		MATCH (r:Result {groupId: $id, status: 'failed'}) WHERE r.runId > $after
		WITH r ORDER BY r.runId ASC LIMIT 1
		MATCH (run:Run {id: r.runId}) RETURN run`, map[string]any{"id": fingerprint, "after": after})
	if err != nil {
		return nil, fmt.Errorf("gagal pertama: %w", err)
	}
	if len(res.Records) == 0 {
		return nil, fmt.Errorf("gagal pertama: tidak ada kemunculan gagal untuk %s", fingerprint)
	}
	f.FirstFail = runRef(res.Records[0])

	res, err = s.query(ctx, `
		MATCH (r:Result {testKey: $key}) WITH r ORDER BY r.runId DESC
		WITH collect(r.status) AS statuses
		RETURN statuses[0..10] AS recent, size([st IN statuses[0..20] WHERE st = 'flaky']) AS flaky`,
		map[string]any{"key": g.TestKey})
	if err != nil {
		return nil, err
	}
	if len(res.Records) > 0 {
		recent, _ := res.Records[0].Get("recent")
		for _, st := range recent.([]any) {
			f.RecentStatuses = append(f.RecentStatuses, st.(string))
		}
		slices.Reverse(f.RecentStatuses) // lama -> baru
		flaky, _ := res.Records[0].Get("flaky")
		f.FlakyRecent = int(flaky.(int64))
	}
	return f, nil
}

func runRef(rec *neo4j.Record) RunRef {
	node, _ := rec.Get("run")
	p := node.(neo4j.Node).Props
	return RunRef{RunID: i64(p, "id"), At: local(p["createdAt"]), Source: str(p, "source"),
		CommitSHA: str(p, "commit"), AppVersion: str(p, "appVersion")}
}

func decode(shape any) []report.HTTPCall {
	if s, ok := shape.(string); ok {
		return DecodeCalls([]byte(s))
	}
	return []report.HTTPCall{}
}

// sourceFiles mengambil isi file kode test berdasarkan id, dengan urutan yang sama (file spec dulu).
func (s *Store) sourceFiles(ctx context.Context, ids []string) ([]SourceFile, error) {
	out := []SourceFile{}
	if len(ids) == 0 {
		return out, nil
	}
	res, err := s.query(ctx, `MATCH (f:SourceFile) WHERE f.id IN $ids RETURN f.id AS id, f.path AS path, f.content AS content`,
		map[string]any{"ids": ids})
	if err != nil {
		return nil, err
	}
	byID := map[string]SourceFile{}
	for _, rec := range res.Records {
		id, _ := rec.Get("id")
		path, _ := rec.Get("path")
		content, _ := rec.Get("content")
		byID[id.(string)] = SourceFile{Path: path.(string), Content: content.(string)}
	}
	for _, id := range ids {
		if f, ok := byID[id]; ok {
			out = append(out, f)
		}
	}
	return out, nil
}

// codeMap: untuk setiap endpoint yang dipanggil dan file yang dipakai test ini, hitung test LAIN di run yang
// sama yang ikut memakainya, lulus atau gagal. Di graph cukup mengikuti hubungan CALLED dan USED.
func (s *Store) codeMap(ctx context.Context, runID int64, testKey string, endpoints, files []string) ([]CodeLink, error) {
	out := []CodeLink{}
	for _, q := range []struct {
		kind, cypher string
		names        []string
	}{
		{"endpoint", `MATCH (:Run {id: $runId})-[:HAS]->(r:Result)-[:CALLED]->(n:Endpoint) WHERE n.key IN $names AND r.testKey <> $key
			RETURN n.key AS name,
			       count(DISTINCT CASE WHEN r.status IN ['passed', 'flaky'] THEN r.testKey END) AS passed,
			       count(DISTINCT CASE WHEN r.status = 'failed' THEN r.testKey END) AS failed
			ORDER BY name`, endpoints},
		{"file", `MATCH (:Run {id: $runId})-[:HAS]->(r:Result)-[:USED]->(n:File) WHERE n.path IN $names AND r.testKey <> $key
			RETURN n.path AS name,
			       count(CASE WHEN r.status IN ['passed', 'flaky'] THEN 1 END) AS passed,
			       count(CASE WHEN r.status = 'failed' THEN 1 END) AS failed
			ORDER BY name`, files},
	} {
		if len(q.names) == 0 {
			continue
		}
		res, err := s.query(ctx, q.cypher, map[string]any{"runId": runID, "key": testKey, "names": q.names})
		if err != nil {
			return nil, err
		}
		for _, rec := range res.Records {
			name, _ := rec.Get("name")
			passed, _ := rec.Get("passed")
			failed, _ := rec.Get("failed")
			out = append(out, CodeLink{Kind: q.kind, Name: name.(string), Passed: int(passed.(int64)), Failed: int(failed.(int64))})
		}
	}
	return out, nil
}

// SaveAnalysis menyimpan hasil analisis per (kelompok, versi prompt); versi yang sama ditimpa.
func (s *Store) SaveAnalysis(ctx context.Context, a Analysis) error {
	if a.Evidence == nil {
		a.Evidence = []string{}
	}
	res, err := s.query(ctx, `
		MATCH (f:Failure {id: $groupId})
		MERGE (a:Analysis {id: $id})
		SET a.groupId = $groupId, a.promptVersion = $promptVersion, a.source = $source, a.model = $model,
			a.category = $category, a.confidence = $confidence, a.summary = $summary, a.evidence = $evidence,
			a.nextStep = $nextStep, a.costUSD = $costUSD, a.createdAt = datetime()
		MERGE (f)-[:ANALYZED]->(a)
		RETURN count(a) AS n`,
		map[string]any{"id": a.Fingerprint + "|" + a.PromptVersion, "groupId": a.Fingerprint,
			"promptVersion": a.PromptVersion, "source": a.Source, "model": a.Model, "category": a.Category,
			"confidence": a.Confidence, "summary": a.Summary, "evidence": a.Evidence, "nextStep": a.NextStep,
			"costUSD": a.CostUSD})
	if err != nil {
		return err
	}
	// count() selalu menghasilkan satu baris; 0 berarti kelompoknya tidak ada.
	if n, _ := res.Records[0].Get("n"); n.(int64) == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) GetAnalysis(ctx context.Context, fingerprint, promptVersion string) (Analysis, error) {
	res, err := s.query(ctx, `MATCH (a:Analysis {id: $id}) RETURN a`,
		map[string]any{"id": fingerprint + "|" + promptVersion})
	if err != nil {
		return Analysis{}, err
	}
	if len(res.Records) == 0 {
		return Analysis{}, ErrNotFound
	}
	node, _ := res.Records[0].Get("a")
	p := node.(neo4j.Node).Props
	cost, _ := p["costUSD"].(float64)
	return Analysis{
		Fingerprint: str(p, "groupId"), PromptVersion: str(p, "promptVersion"), Source: str(p, "source"),
		Model: str(p, "model"), Category: str(p, "category"), Confidence: str(p, "confidence"),
		Summary: str(p, "summary"), Evidence: strs(p, "evidence"), NextStep: str(p, "nextStep"),
		CostUSD: cost, CreatedAt: local(p["createdAt"]),
	}, nil
}

// SetLabel menyimpan label manual beserta siapa yang memberinya. label kosong menghapus label.
func (s *Store) SetLabel(ctx context.Context, fingerprint, label, note, by string) error {
	var labelArg any
	if label != "" {
		labelArg = label
	} else {
		note, by = "", ""
	}
	res, err := s.query(ctx, `
		MATCH (f:Failure {id: $id})
		SET f.manualCategory = $label, f.manualNote = $note, f.labeledBy = $by,
			f.labeledAt = CASE WHEN $label IS NULL THEN null ELSE datetime() END
		RETURN count(f) AS n`, map[string]any{"id": fingerprint, "label": labelArg, "note": note, "by": by})
	if err != nil {
		return err
	}
	// count() selalu menghasilkan satu baris; 0 berarti kelompoknya tidak ada.
	if n, _ := res.Records[0].Get("n"); n.(int64) == 0 {
		return ErrNotFound
	}
	return nil
}
