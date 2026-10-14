package store

import (
	"context"
	"fmt"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"

	"redline/internal/report"
	"redline/internal/triage"
)

// followRenames menyambung riwayat test yang di-rename (judul, describe, atau file berubah) lewat ID test case
// dari tag @TC-. Identitas test tetap kuncinya (project › file › judul); ID hanya dipakai untuk mengenali bahwa
// kunci baru adalah test lama yang sama. Test lama dipindahkan ke kunci baru beserta semua hasilnya, jadi
// pembanding "lulus terakhir", riwayat status, dan impact analysis tetap utuh.
//
// Rename hanya diikuti kalau aman:
//   - ID itu dipakai tepat satu test di run ini (tag dobel tidak diikuti),
//   - kunci baru belum pernah ada,
//   - ada tepat satu test lama di project yang sama dengan ID itu, dan kunci lamanya tidak ada di run ini
//     (kalau masih ada, tag-nya dipindah ke test lain, bukan rename).
//
// Mengembalikan kunci-kunci lama per kunci baru, untuk menyambung kelompok kegagalan lama (lihat aliasFingerprint).
func followRenames(ctx context.Context, tx neo4j.ManagedTransaction, outcomes []report.Outcome) (map[string][]string, error) {
	inRun := map[string]bool{}
	perID := map[string]int{}
	for _, o := range outcomes {
		inRun[o.Key()] = true
		if o.TestCaseID != "" {
			perID[o.Project+"\x00"+o.TestCaseID]++
		}
	}
	keys := make([]string, 0, len(inRun))
	for k := range inRun {
		keys = append(keys, k)
	}
	rows := []map[string]any{}
	byKey := map[string]report.Outcome{}
	for _, o := range outcomes {
		if o.TestCaseID != "" && perID[o.Project+"\x00"+o.TestCaseID] == 1 {
			rows = append(rows, map[string]any{"key": o.Key(), "project": o.Project, "caseId": o.TestCaseID})
			byKey[o.Key()] = o
		}
	}

	if len(rows) > 0 {
		res, err := tx.Run(ctx, `
			UNWIND $rows AS row
			WITH row WHERE NOT EXISTS { MATCH (:Test {key: row.key}) }
			MATCH (old:Test {project: row.project, caseId: row.caseId})
			WHERE old.key <> row.key AND NOT old.key IN $keys
			WITH row, collect(old.key) AS olds WHERE size(olds) = 1
			RETURN row.key AS new, olds[0] AS old`, map[string]any{"rows": rows, "keys": keys})
		if err != nil {
			return nil, fmt.Errorf("cari test yang di-rename: %w", err)
		}
		renames, err := res.Collect(ctx)
		if err != nil {
			return nil, err
		}
		for _, rec := range renames {
			p := rec.AsMap()
			o := byKey[str(p, "new")]
			if _, err := tx.Run(ctx, `
				MATCH (t:Test {key: $old})
				SET t.key = $new, t.project = $project, t.file = $file, t.title = $title,
				    t.previousKeys = coalesce(t.previousKeys, []) + $old
				WITH t
				OPTIONAL MATCH (t)<-[:OF]-(r:Result)
				SET r.testKey = $new
				WITH DISTINCT t
				OPTIONAL MATCH (f:Failure)-[:OF]->(t)
				SET f.project = $project, f.file = $file, f.title = $title`,
				map[string]any{"old": str(p, "old"), "new": o.Key(), "project": o.Project, "file": o.File,
					"title": o.Title}); err != nil {
				return nil, fmt.Errorf("pindahkan riwayat %q ke %q: %w", str(p, "old"), o.Key(), err)
			}
		}
	}

	// Kunci lama dari rename kali ini dan sebelumnya.
	res, err := tx.Run(ctx, `
		UNWIND $keys AS k
		MATCH (t:Test {key: k}) WHERE size(coalesce(t.previousKeys, [])) > 0
		RETURN k AS key, t.previousKeys AS previous`, map[string]any{"keys": keys})
	if err != nil {
		return nil, fmt.Errorf("kunci lama test: %w", err)
	}
	recs, err := res.Collect(ctx)
	if err != nil {
		return nil, err
	}
	aliases := map[string][]string{}
	for _, rec := range recs {
		p := rec.AsMap()
		aliases[str(p, "key")] = strs(p, "previous")
	}
	return aliases, nil
}

// aliasFingerprint: sidik jari kelompok untuk test yang pernah di-rename. Kalau kelompok dengan kunci sekarang
// belum ada, tetapi kelompok yang sama dengan kunci lama sudah ada, kelompok lama itu yang dipakai, supaya
// error yang sama setelah rename tidak membuka kelompok baru. Kunci lama yang terbaru didahulukan.
func aliasFingerprint(ctx context.Context, tx neo4j.ManagedTransaction, key string, previous []string, normalized string) (string, error) {
	current := triage.Fingerprint(key, normalized)
	candidates := []string{current}
	for i := len(previous) - 1; i >= 0; i-- {
		candidates = append(candidates, triage.Fingerprint(previous[i], normalized))
	}
	res, err := tx.Run(ctx, `
		UNWIND range(0, size($ids) - 1) AS i
		MATCH (f:Failure {id: $ids[i]})
		RETURN f.id AS id ORDER BY i LIMIT 1`, map[string]any{"ids": candidates})
	if err != nil {
		return "", fmt.Errorf("kelompok lama test yang di-rename: %w", err)
	}
	rec, err := res.Single(ctx)
	if err != nil {
		return current, nil // belum ada kelompok sama sekali: kelompok baru dengan kunci sekarang
	}
	id, _ := rec.Get("id")
	return id.(string), nil
}
