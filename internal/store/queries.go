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
		p := node.(neo4j.Node).Props
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
		runs = append(runs, r)
	}
	return runs, nil
}

// failureReturn: kolom kelompok kegagalan beserta waktu dari run terkait. Dipakai setelah MATCH (f:Failure).
const failureReturn = `
	OPTIONAL MATCH (fr:Run {id: f.firstSeenRun})
	OPTIONAL MATCH (lr:Run {id: f.lastSeenRun})
	OPTIONAL MATCH (rr:Run {id: f.resolvedRun})
	RETURN f, fr.createdAt AS firstAt, lr.createdAt AS lastAt, rr.createdAt AS resolvedAt`

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
