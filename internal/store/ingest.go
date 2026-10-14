package store

import (
	"context"
	"fmt"
	"time"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"

	"redline/internal/report"
	"redline/internal/triage"
)

// IngestReport menyimpan satu laporan Playwright dan memperbarui status kelompok kegagalan, dengan aturan
// yang sama persis seperti versi Postgres di v0.1.0. Semuanya dalam satu transaksi.
//
// Graph yang dibuat:
//
//	(:Run)-[:HAS]->(:Result)-[:OF]->(:Test)   (:Result)-[:RETURNED]->(:Shape)
//	(:Failure)-[:OF]->(:Test)                 (:SourceFile) disimpan sekali per isi
//	(:Result)-[:CALLED]->(:Endpoint)          (:Result)-[:USED]->(:File)   (:Result)-[:DEPENDS_ON]->(:File)
//	(:File)-[:IMPORTS]->(:File)               (:Run)-[:HAD]->(:FileVersion)<-[:VERSION]-(:File)
func (s *Store) IngestReport(ctx context.Context, rep *report.Report, meta RunMeta) (*IngestResult, error) {
	if meta.Source == "" {
		meta.Source = "local"
	}
	outcomes := rep.Outcomes()
	res := &IngestResult{
		New: []GroupChange{}, Recurring: []GroupChange{}, Regressed: []GroupChange{},
		Resolved: []GroupChange{}, FlakyTests: []string{}, Incidents: []Incident{},
	}
	incidents := NewIncidentSet()
	shared := s.SharesStatus(meta.Source)
	res.SharedStatus = shared

	_, err := s.write(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		runID, err := nextID(ctx, tx, "run")
		if err != nil {
			return nil, fmt.Errorf("nomor run: %w", err)
		}
		res.RunID = runID
		var started any
		if !rep.Stats.StartTime.IsZero() {
			started = rep.Stats.StartTime.UTC() // Neo4j tidak mengenal zona waktu "Local" milik Go
		}
		reportErrors := []string{}
		for _, e := range rep.Errors {
			reportErrors = append(reportErrors, triage.Clean(e.Message))
		}
		if _, err := tx.Run(ctx, `
			CREATE (:Run {id: $id, createdAt: datetime(), startedAt: $startedAt, durationMs: $durationMs,
				source: $source, triggeredBy: $triggeredBy, ciURL: $ciURL, branch: $branch, commit: $commit,
				appVersion: $appVersion, playwrightVersion: $playwrightVersion, reportErrors: $reportErrors,
				total: 0, passed: 0, failed: 0, flaky: 0, skipped: 0})`,
			map[string]any{
				"id": runID, "startedAt": started, "durationMs": int64(rep.Stats.Duration), "source": meta.Source,
				"triggeredBy": meta.TriggeredBy, "ciURL": meta.CIURL, "branch": meta.Branch, "commit": meta.CommitSHA,
				"appVersion": meta.AppVersion, "playwrightVersion": rep.Config.Version, "reportErrors": reportErrors,
			}); err != nil {
			return nil, fmt.Errorf("simpan run: %w", err)
		}

		sourceIDs, err := saveSources(ctx, tx, rep.Sources)
		if err != nil {
			return nil, err
		}
		if err := saveFiles(ctx, tx, runID, rep.Files); err != nil {
			return nil, err
		}
		aliases, err := followRenames(ctx, tx, outcomes)
		if err != nil {
			return nil, err
		}

		passedKeys := []string{}
		for _, o := range outcomes {
			res.Total++
			fingerprint, cleanMsg, incidentKey := "", "", ""
			switch o.Status {
			case report.StatusPassed:
				res.Passed++
				passedKeys = append(passedKeys, o.Key())
			case report.StatusSkipped:
				res.Skipped++
			case report.StatusFlaky:
				res.Flaky++
				res.FlakyTests = append(res.FlakyTests, o.Key())
			case report.StatusFailed:
				res.Failed++
			}
			if o.ErrorMessage != "" {
				cleanMsg = triage.Clean(o.ErrorMessage)
				normalized := triage.Normalize(cleanMsg)
				fingerprint = triage.Fingerprint(o.Key(), normalized)
				if previous := aliases[o.Key()]; len(previous) > 0 && o.Status == report.StatusFailed {
					if fingerprint, err = aliasFingerprint(ctx, tx, o.Key(), previous, normalized); err != nil {
						return nil, err
					}
				}
				var kind, label string
				incidentKey, kind, label = triage.IncidentKey(normalized, o.Calls)
				if o.Status == report.StatusFailed {
					incidents.Add(incidentKey, kind, triage.Clean(label))
				}
			}
			// Hanya kegagalan murni yang membuka/memperbarui kelompok; flaky hanya dicatat di hasil test.
			if o.Status == report.StatusFailed && fingerprint != "" {
				change, kind, err := upsertFailure(ctx, tx, runID, o, fingerprint, cleanMsg, incidentKey,
					incidents.Label(incidentKey), shared)
				if err != nil {
					return nil, err
				}
				incidents.Member(incidentKey, fingerprint, kind)
				switch kind {
				case "new":
					res.New = append(res.New, change)
				case "regressed":
					res.Regressed = append(res.Regressed, change)
				default:
					res.Recurring = append(res.Recurring, change)
				}
			}
			if err := saveResult(ctx, tx, runID, o, fingerprint, cleanMsg, incidentKey, sourceIDs,
				rep.Dependencies(o.CodeFiles)); err != nil {
				return nil, err
			}
		}

		resolved, err := resolvePassed(ctx, tx, runID, passedKeys, shared)
		if err != nil {
			return nil, err
		}
		res.Resolved = resolved

		_, err = tx.Run(ctx, `MATCH (r:Run {id: $id})
			SET r.total = $total, r.passed = $passed, r.failed = $failed, r.flaky = $flaky, r.skipped = $skipped`,
			map[string]any{"id": runID, "total": res.Total, "passed": res.Passed, "failed": res.Failed,
				"flaky": res.Flaky, "skipped": res.Skipped})
		return nil, err
	})
	if err != nil {
		return nil, err
	}
	res.Incidents = incidents.List()
	return res, nil
}

// saveSources menyimpan isi file kode test (sudah disamarkan) sekali per isi, lalu mengembalikan path -> id.
func saveSources(ctx context.Context, tx neo4j.ManagedTransaction, sources map[string]string) (map[string]string, error) {
	ids := map[string]string{}
	rows := []map[string]any{}
	for p, content := range sources {
		if p == "" || len(content) > MaxSourceBytes {
			continue
		}
		id, clean := SourceID(p, content)
		ids[p] = id
		rows = append(rows, map[string]any{"id": id, "path": p, "content": clean})
	}
	if len(rows) == 0 {
		return ids, nil
	}
	if _, err := tx.Run(ctx, `UNWIND $rows AS row
		MERGE (f:SourceFile {id: row.id}) ON CREATE SET f.path = row.path, f.content = row.content`,
		map[string]any{"rows": rows}); err != nil {
		return nil, fmt.Errorf("simpan kode test: %w", err)
	}
	return ids, nil
}

// saveFiles menyimpan graph import run ini: isi setiap file (versi) dan import langsungnya. Import setiap
// file diganti dengan yang terbaru, jadi (:File)-[:IMPORTS]->(:File) adalah graph import yang sekarang.
func saveFiles(ctx context.Context, tx neo4j.ManagedTransaction, runID int64, files map[string]report.FileNode) error {
	if len(files) == 0 {
		return nil
	}
	rows := make([]map[string]any, 0, len(files))
	for path, f := range files {
		imports := f.Imports
		if imports == nil {
			imports = []string{}
		}
		rows = append(rows, map[string]any{"path": path, "hash": f.Hash, "imports": imports})
	}
	_, err := tx.Run(ctx, `
		MATCH (run:Run {id: $runId})
		UNWIND $rows AS row
		MERGE (f:File {path: row.path})
		SET f.hash = row.hash, f.lastRunId = $runId
		MERGE (v:FileVersion {id: row.path + '@' + row.hash}) ON CREATE SET v.path = row.path, v.hash = row.hash
		MERGE (f)-[:VERSION]->(v)
		MERGE (run)-[:HAD]->(v)
		WITH f, row
		OPTIONAL MATCH (f)-[old:IMPORTS]->()
		DELETE old
		WITH DISTINCT f, row
		FOREACH (p IN row.imports | MERGE (g:File {path: p}) MERGE (f)-[:IMPORTS]->(g))`,
		map[string]any{"runId": runID, "rows": rows})
	if err != nil {
		return fmt.Errorf("simpan graph import: %w", err)
	}
	return nil
}

// saveResult menyimpan hasil satu test di run ini, beserta test-nya dan bentuk response-nya.
func saveResult(ctx context.Context, tx neo4j.ManagedTransaction, runID int64, o report.Outcome,
	fingerprint, cleanMsg, incidentKey string, sourceIDs map[string]string, deps []string) error {
	id, err := nextID(ctx, tx, "result")
	if err != nil {
		return err
	}
	codeFiles := o.CodeFiles
	if codeFiles == nil {
		codeFiles = []string{}
	}
	// Isi kode hanya untuk test yang punya error (gagal atau flaky): itulah yang dianalisis.
	sources := []string{}
	if o.ErrorMessage != "" {
		for _, p := range codeFiles {
			if sid, ok := sourceIDs[p]; ok {
				sources = append(sources, sid)
			}
		}
	}
	screenshots := o.Screenshots
	if screenshots == nil {
		screenshots = []string{}
	}
	calls := CallsJSON(o.Calls)
	shapeID := ""
	if calls != "[]" {
		shapeID = ShapeID(calls)
	}
	if _, err := tx.Run(ctx, `
		MATCH (run:Run {id: $runId})
		MERGE (t:Test {key: $key}) ON CREATE SET t.project = $project, t.file = $file, t.title = $title
		SET t.caseId = $caseId
		CREATE (run)-[:HAS]->(r:Result {id: $id, runId: $runId, testKey: $key, status: $status, retries: $retries,
			durationMs: $durationMs, errorMessage: $errorMessage, errorSnippet: $errorSnippet,
			errorLocation: $errorLocation, groupId: $groupId, causeId: $causeId, testCodeHash: $testCodeHash,
			shapeId: $shapeId, tracePath: $tracePath, screenshots: $screenshots, codeFiles: $codeFiles,
			sourceIds: $sourceIds, caseId: $caseId})-[:OF]->(t)
		WITH r
		OPTIONAL MATCH (f:Failure {id: $groupId})
		FOREACH (_ IN CASE WHEN f IS NULL THEN [] ELSE [1] END | CREATE (r)-[:IN_GROUP]->(f))`,
		map[string]any{
			"runId": runID, "id": id, "key": o.Key(), "project": o.Project, "file": o.File, "title": o.Title,
			"caseId": o.TestCaseID, "status": o.Status, "retries": o.Retries, "durationMs": o.DurationMs,
			"errorMessage": cleanMsg, "errorSnippet": triage.Clean(o.ErrorSnippet), "errorLocation": o.ErrorLocation,
			"groupId": fingerprint, "causeId": incidentKey, "testCodeHash": o.SourceHash, "shapeId": shapeID,
			"tracePath": o.TracePath, "screenshots": screenshots, "codeFiles": codeFiles, "sourceIds": sources,
		}); err != nil {
		return fmt.Errorf("simpan hasil %q: %w", o.Key(), err)
	}
	// Peta kode: endpoint yang dipanggil dan file yang dipakai test ini, sebagai hubungan graph.
	endpoints := []string{}
	for _, c := range DecodeCalls([]byte(calls)) {
		endpoints = append(endpoints, c.Method+" "+c.Path)
	}
	// DEPENDS_ON: semua file yang dipakai, termasuk import bertingkat (kosong kalau reporter lama).
	if deps == nil {
		deps = []string{}
	}
	if len(endpoints)+len(codeFiles)+len(deps) > 0 {
		if _, err := tx.Run(ctx, `
			MATCH (r:Result {id: $id})
			FOREACH (key IN $endpoints | MERGE (e:Endpoint {key: key}) MERGE (r)-[:CALLED]->(e))
			FOREACH (path IN $files | MERGE (f:File {path: path}) MERGE (r)-[:USED]->(f))
			FOREACH (path IN $deps | MERGE (f:File {path: path}) MERGE (r)-[:DEPENDS_ON]->(f))`,
			map[string]any{"id": id, "endpoints": endpoints, "files": codeFiles, "deps": deps}); err != nil {
			return fmt.Errorf("simpan peta kode: %w", err)
		}
	}
	if shapeID != "" {
		if _, err := tx.Run(ctx, `
			MERGE (s:Shape {id: $shapeId}) ON CREATE SET s.shape = $calls
			WITH s MATCH (r:Result {id: $id}) CREATE (r)-[:RETURNED]->(s)`,
			map[string]any{"shapeId": shapeID, "calls": calls, "id": id}); err != nil {
			return fmt.Errorf("simpan bentuk response: %w", err)
		}
	}
	return nil
}

// upsertFailure membuat kelompok baru atau memperbarui yang sudah ada. kind: new, recurring, atau regressed.
// shared false: kelompok baru dibuat sebagai localOnly dan kelompok yang ada tidak diubah.
func upsertFailure(ctx context.Context, tx neo4j.ManagedTransaction, runID int64, o report.Outcome,
	fingerprint, cleanMsg, incidentKey, incidentLabel string, shared bool) (GroupChange, string, error) {
	change := GroupChange{Fingerprint: fingerprint, TestKey: o.Key(), TestCaseID: o.TestCaseID,
		Error: Summarize(cleanMsg), Incident: incidentKey}

	// SET ke nilai yang sama mengunci node (pengganti SELECT ... FOR UPDATE), supaya dua run yang masuk
	// bersamaan tidak sama-sama mengira kelompok ini belum ada atau masih resolved.
	cur, err := tx.Run(ctx, `MATCH (f:Failure {id: $id}) SET f.status = f.status
		RETURN f.status AS status, f.localOnly AS localOnly, f.occurrences AS occurrences`,
		map[string]any{"id": fingerprint})
	if err != nil {
		return change, "", err
	}
	if !cur.Next(ctx) {
		if err := cur.Err(); err != nil {
			return change, "", err
		}
		if _, err := tx.Run(ctx, `
			MERGE (t:Test {key: $key}) ON CREATE SET t.project = $project, t.file = $file, t.title = $title
			CREATE (f:Failure {id: $id, project: $project, file: $file, title: $title, lastError: $lastError,
				status: 'open', occurrences: 1, regressions: 0, firstSeenRun: $runId, lastSeenRun: $runId,
				causeId: $causeId, cause: $cause, manualNote: '', labeledBy: '', localOnly: $localOnly,
				caseId: $caseId})-[:OF]->(t)`,
			map[string]any{"key": o.Key(), "project": o.Project, "file": o.File, "title": o.Title, "id": fingerprint,
				"lastError": cleanMsg, "runId": runID, "causeId": incidentKey, "cause": incidentLabel,
				"localOnly": !shared, "caseId": o.TestCaseID}); err != nil {
			return change, "", fmt.Errorf("buat kelompok %s: %w", fingerprint, err)
		}
		change.Occurrences = 1
		return change, "new", nil
	}
	rec := cur.Record()
	prevStatus, _ := rec.Get("status")
	localOnly, _ := rec.Get("localOnly")
	occ, _ := rec.Get("occurrences")
	change.Occurrences = int(occ.(int64))

	kind := "recurring"
	if prevStatus == "resolved" {
		kind = "regressed"
	}
	if !shared {
		return change, kind, nil
	}
	params := map[string]any{"id": fingerprint, "runId": runID, "lastError": cleanMsg, "causeId": incidentKey,
		"cause": incidentLabel, "caseId": o.TestCaseID}
	if localOnly == true {
		// Pertama kali terlihat di run yang mengubah status: mulai sebagai kelompok bersama yang baru.
		if _, err := tx.Run(ctx, `MATCH (f:Failure {id: $id})
			SET f.localOnly = false, f.status = 'open', f.occurrences = 1, f.regressions = 0,
				f.firstSeenRun = $runId, f.lastSeenRun = $runId, f.lastError = $lastError, f.resolvedRun = null,
				f.causeId = $causeId, f.cause = $cause, f.caseId = $caseId`, params); err != nil {
			return change, "", fmt.Errorf("jadikan kelompok bersama %s: %w", fingerprint, err)
		}
		change.Occurrences = 1
		return change, "new", nil
	}
	upd, err := tx.Run(ctx, `MATCH (f:Failure {id: $id})
		WITH f, f.status = 'resolved' AS wasResolved
		SET f.occurrences = f.occurrences + 1, f.lastSeenRun = $runId, f.lastError = $lastError,
			f.status = CASE WHEN wasResolved THEN 'regressed' ELSE f.status END,
			f.regressions = f.regressions + CASE WHEN wasResolved THEN 1 ELSE 0 END,
			f.resolvedRun = null, f.causeId = $causeId, f.cause = $cause, f.caseId = $caseId
		RETURN f.occurrences AS occurrences`, params)
	if err != nil {
		return change, "", fmt.Errorf("perbarui kelompok %s: %w", fingerprint, err)
	}
	r, err := upd.Single(ctx)
	if err != nil {
		return change, "", fmt.Errorf("perbarui kelompok %s: %w", fingerprint, err)
	}
	n, _ := r.Get("occurrences")
	change.Occurrences = int(n.(int64))
	return change, kind, nil
}

// resolvePassed: test yang lulus bersih menutup kelompok kegagalannya yang masih terbuka.
// Run yang tidak mengubah status bersama hanya menampilkan pratinjaunya.
func resolvePassed(ctx context.Context, tx neo4j.ManagedTransaction, runID int64, keys []string, shared bool) ([]GroupChange, error) {
	out := []GroupChange{}
	if len(keys) == 0 {
		return out, nil
	}
	set := ""
	if shared {
		set = "SET f.status = 'resolved', f.resolvedRun = $runId"
	}
	cur, err := tx.Run(ctx, `UNWIND $keys AS key
		MATCH (f:Failure)-[:OF]->(:Test {key: key})
		WHERE f.status IN ['open', 'regressed'] AND f.lastSeenRun <> $runId
		`+set+`
		RETURN f.id AS id, key, f.lastError AS lastError, f.occurrences AS occurrences`,
		map[string]any{"keys": keys, "runId": runID})
	if err != nil {
		return nil, fmt.Errorf("tutup kelompok yang sudah lulus: %w", err)
	}
	for cur.Next(ctx) {
		rec := cur.Record()
		id, _ := rec.Get("id")
		key, _ := rec.Get("key")
		lastError, _ := rec.Get("lastError")
		occ, _ := rec.Get("occurrences")
		out = append(out, GroupChange{Fingerprint: id.(string), TestKey: key.(string),
			Error: Summarize(lastError.(string)), Occurrences: int(occ.(int64))})
	}
	return out, cur.Err()
}

// local mengubah waktu dari Neo4j ke zona waktu server (TIMEZONE).
func local(v any) time.Time {
	if t, ok := v.(time.Time); ok {
		return t.In(time.Local)
	}
	return time.Time{}
}
