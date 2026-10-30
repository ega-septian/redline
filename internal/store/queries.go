package store

import (
	"context"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// ListRuns mengembalikan run terbaru dulu.
func (s *Store) ListRuns(ctx context.Context, limit int) ([]Run, error) {
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	res, err := s.query(ctx, `MATCH (r:Run) RETURN r ORDER BY r.id DESC LIMIT $limit`, map[string]any{"limit": limit})
	if err != nil {
		return nil, err
	}
	runs := []Run{}
	for _, rec := range res.Records {
		node, _ := rec.Get("r")
		runs = append(runs, toRun(node.(neo4j.Node).Props))
	}
	return runs, nil
}

// GetRun mengembalikan satu run, atau ErrNotFound.
func (s *Store) GetRun(ctx context.Context, id int64) (Run, error) {
	res, err := s.query(ctx, `MATCH (r:Run {id: $id}) RETURN r`, map[string]any{"id": id})
	if err != nil {
		return Run{}, err
	}
	if len(res.Records) == 0 {
		return Run{}, ErrNotFound
	}
	node, _ := res.Records[0].Get("r")
	return toRun(node.(neo4j.Node).Props), nil
}

// RunResults mengembalikan semua outcome run, beserta group dan analisis terakhirnya.
func (s *Store) RunResults(ctx context.Context, runID int64) ([]RunResult, error) {
	res, err := s.query(ctx, `
		MATCH (:Run {id: $run})-[:HAS]->(r:Result)
		OPTIONAL MATCH (r)-[:IN_GROUP]->(f:Failure)
		OPTIONAL MATCH (f)-[:ANALYZED]->(a:Analysis)
		WITH r, f, a ORDER BY a.createdAt DESC
		WITH r, f, head(collect(a)) AS a
		RETURN r, a.category AS category, a.confidence AS confidence,
		       coalesce(f.manualCategory, '') AS humanLabel
		ORDER BY r.testKey`, map[string]any{"run": runID})
	if err != nil {
		return nil, err
	}
	out := []RunResult{}
	for _, rec := range res.Records {
		node, _ := rec.Get("r")
		p := node.(neo4j.Node).Props
		m := rec.AsMap()
		out = append(out, RunResult{
			TestKey: str(p, "testKey"), TestCaseID: str(p, "caseId"), Status: str(p, "status"),
			DurationMs: int(i64(p, "durationMs")), Retries: int(i64(p, "retries")),
			ErrorMessage: str(p, "errorMessage"), ErrorSnippet: str(p, "errorSnippet"),
			ErrorLocation: str(p, "errorLocation"), TracePath: str(p, "tracePath"),
			Screenshots: strs(p, "screenshots"), Fingerprint: str(p, "groupId"),
			Category: str(m, "category"), Confidence: str(m, "confidence"), HumanLabel: str(m, "humanLabel"),
		})
	}
	return out, nil
}

func toRun(p map[string]any) Run {
	r := Run{
		ID: i64(p, "id"), CreatedAt: local(p["createdAt"]), DurationMs: int(i64(p, "durationMs")),
		Source: str(p, "source"), TriggeredBy: str(p, "triggeredBy"), CIURL: str(p, "ciURL"),
		Branch: str(p, "branch"), CommitSHA: str(p, "commit"), AppVersion: str(p, "appVersion"),
		PlaywrightVersion: str(p, "playwrightVersion"), Total: int(i64(p, "total")), Passed: int(i64(p, "passed")),
		Failed: int(i64(p, "failed")), Flaky: int(i64(p, "flaky")), Skipped: int(i64(p, "skipped")),
		ReportErrors: strs(p, "reportErrors"),
	}
	if v, ok := p["startedAt"]; ok && v != nil {
		t := local(v)
		r.StartedAt = &t
	}
	return r
}

// failureReturn: kolom kelompok kegagalan beserta waktu dari run terkait. Dipakai setelah MATCH (f:Failure).
const failureReturn = `
	OPTIONAL MATCH (fr:Run {id: f.firstSeenRun})
	OPTIONAL MATCH (lr:Run {id: f.lastSeenRun})
	OPTIONAL MATCH (rr:Run {id: f.resolvedRun})
	OPTIONAL MATCH (f)-[:ANALYZED]->(a:Analysis)
	WITH f, fr, lr, rr, a ORDER BY a.createdAt DESC
	WITH f, fr, lr, rr, head(collect(a)) AS a
	RETURN f, fr.createdAt AS firstAt, lr.createdAt AS lastAt, rr.createdAt AS resolvedAt,
		a.category AS analysisCategory, a.confidence AS analysisConfidence, a.source AS analysisSource,
		a.model AS analysisModel, a.summary AS analysisSummary, a.createdAt AS analyzedAt`

func toGroup(rec *neo4j.Record) Group {
	node, _ := rec.Get("f")
	p := node.(neo4j.Node).Props
	g := Group{
		Fingerprint: str(p, "id"), Project: str(p, "project"), File: str(p, "file"), Title: str(p, "title"),
		TestCaseID: str(p, "caseId"), Status: str(p, "status"), SampleError: str(p, "lastError"),
		Cause: str(p, "cause"), Occurrences: int(i64(p, "occurrences")), Regressions: int(i64(p, "regressions")),
		FirstSeenRun: i64(p, "firstSeenRun"), LastSeenRun: i64(p, "lastSeenRun"),
		HumanLabel: str(p, "manualCategory"), HumanNote: str(p, "manualNote"), LabeledBy: str(p, "labeledBy"),
		LocalOnly: p["localOnly"] == true,
	}
	g.TestKey = g.Project + " › " + g.File + " › " + g.Title
	g.Summary = Summarize(g.SampleError)
	if v, ok := p["resolvedRun"]; ok && v != nil {
		id := v.(int64)
		g.ResolvedRun = &id
	}
	firstAt, _ := rec.Get("firstAt")
	lastAt, _ := rec.Get("lastAt")
	g.FirstSeenAt, g.LastSeenAt = local(firstAt), local(lastAt)
	if resolvedAt, _ := rec.Get("resolvedAt"); resolvedAt != nil {
		t := local(resolvedAt)
		g.ResolvedAt = &t
	}
	m := rec.AsMap()
	g.Category = str(m, "analysisCategory")
	g.Confidence = str(m, "analysisConfidence")
	g.AnalysisSource = str(m, "analysisSource")
	g.AnalysisModel = str(m, "analysisModel")
	g.AnalysisSummary = str(m, "analysisSummary")
	if analyzedAt, _ := m["analyzedAt"]; analyzedAt != nil {
		t := local(analyzedAt)
		g.AnalyzedAt = &t
	}
	return g
}

// ListGroups mengembalikan kelompok kegagalan bersama (tanpa localOnly).
// statuses kosong = open + regressed (yang perlu dikerjakan).
func (s *Store) ListGroups(ctx context.Context, statuses []string, limit int) ([]Group, error) {
	if len(statuses) == 0 {
		statuses = []string{"open", "regressed"}
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	res, err := s.query(ctx, `
		MATCH (f:Failure) WHERE f.status IN $statuses AND NOT f.localOnly
		WITH f ORDER BY f.lastSeenRun DESC LIMIT $limit`+failureReturn+`
		ORDER BY f.lastSeenRun DESC`, map[string]any{"statuses": statuses, "limit": limit})
	if err != nil {
		return nil, err
	}
	groups := []Group{}
	for _, rec := range res.Records {
		g := toGroup(rec)
		g.SampleError = "" // daftar cukup ringkasan
		groups = append(groups, g)
	}
	return groups, nil
}

// GetGroup mengembalikan satu kelompok beserta 20 kemunculan terakhirnya.
func (s *Store) GetGroup(ctx context.Context, fingerprint string) (Group, []Occurrence, error) {
	res, err := s.query(ctx, `MATCH (f:Failure {id: $id})`+failureReturn, map[string]any{"id": fingerprint})
	if err != nil {
		return Group{}, nil, err
	}
	if len(res.Records) == 0 {
		return Group{}, nil, ErrNotFound
	}
	g := toGroup(res.Records[0])

	res, err = s.query(ctx, `
		MATCH (r:Result {groupId: $id}) MATCH (run:Run {id: r.runId})
		RETURN run, r ORDER BY run.id DESC LIMIT 20`, map[string]any{"id": fingerprint})
	if err != nil {
		return Group{}, nil, err
	}
	occ := []Occurrence{}
	for _, rec := range res.Records {
		runNode, _ := rec.Get("run")
		resultNode, _ := rec.Get("r")
		run, r := runNode.(neo4j.Node).Props, resultNode.(neo4j.Node).Props
		occ = append(occ, Occurrence{
			RunID: i64(run, "id"), RunCreatedAt: local(run["createdAt"]), Source: str(run, "source"),
			Branch: str(run, "branch"), CommitSHA: str(run, "commit"), AppVersion: str(run, "appVersion"),
			Status: str(r, "status"), Retries: int(i64(r, "retries")), ErrorLocation: str(r, "errorLocation"),
			ErrorSnippet: str(r, "errorSnippet"), TracePath: str(r, "tracePath"), Screenshots: strs(r, "screenshots"),
		})
	}
	return g, occ, nil
}

func str(p map[string]any, k string) string {
	v, _ := p[k].(string)
	return v
}

func i64(p map[string]any, k string) int64 {
	v, _ := p[k].(int64)
	return v
}

func strs(p map[string]any, k string) []string {
	out := []string{}
	list, _ := p[k].([]any)
	for _, v := range list {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// RunFailure adalah satu test yang gagal di sebuah run, beserta kelompok kegagalannya.
type RunFailure struct {
	TestKey     string `json:"test"`
	CaseID      string `json:"test_case_id,omitempty"`
	Fingerprint string `json:"fingerprint"`
}

// RunFailures mengembalikan test yang gagal di run ini (bukan flaky), urut kunci test.
func (s *Store) RunFailures(ctx context.Context, runID int64) ([]RunFailure, error) {
	res, err := s.query(ctx, `
		MATCH (:Run {id: $run})-[:HAS]->(r:Result {status: 'failed'}) WHERE r.groupId <> ''
		RETURN r.testKey AS test, r.caseId AS caseId, r.groupId AS fp ORDER BY test`, map[string]any{"run": runID})
	if err != nil {
		return nil, err
	}
	out := []RunFailure{}
	for _, rec := range res.Records {
		p := rec.AsMap()
		out = append(out, RunFailure{TestKey: str(p, "test"), CaseID: str(p, "caseId"), Fingerprint: str(p, "fp")})
	}
	return out, nil
}
