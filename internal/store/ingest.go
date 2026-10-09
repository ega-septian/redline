package store

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"redline/internal/report"
	"redline/internal/triage"
)

// RunMeta adalah informasi tambahan tentang run. Semuanya opsional.
type RunMeta struct {
	Source      string `json:"source"`       // local / ci
	TriggeredBy string `json:"triggered_by"` // siapa yang menjalankan: user, atau ci:<actor>
	CIURL       string `json:"ci_url"`       // halaman run CI (log + artifact)
	Branch      string `json:"branch"`
	CommitSHA   string `json:"commit"`
	AppVersion  string `json:"app_version"`
}

// GroupChange adalah kelompok kegagalan yang statusnya berubah karena run ini.
type GroupChange struct {
	Fingerprint string `json:"fingerprint"`
	TestKey     string `json:"test"`
	Error       string `json:"error,omitempty"` // ringkasan error (sudah diredaksi)
	Occurrences int    `json:"occurrences"`
	Incident    string `json:"incident,omitempty"` // key insiden; kosong untuk resolved
}

// Incident adalah kegagalan beberapa test di run ini yang penyebabnya sama.
type Incident struct {
	Key   string `json:"key"`
	Kind  string `json:"kind"` // connection, http, error
	Label string `json:"label"`
	Tests int    `json:"tests"`
	// Representative: fingerprint yang cukup dianalisis mewakili seluruh insiden
	// (regressed lebih dulu, lalu new, lalu recurring).
	Representative string   `json:"representative"`
	Fingerprints   []string `json:"fingerprints"`
}

// IngestResult adalah ringkasan setelah satu laporan disimpan.
type IngestResult struct {
	RunID   int64 `json:"run_id"`
	Total   int   `json:"total"`
	Passed  int   `json:"passed"`
	Failed  int   `json:"failed"`
	Flaky   int   `json:"flaky"`
	Skipped int   `json:"skipped"`

	New        []GroupChange `json:"new"`       // kegagalan yang belum pernah terlihat
	Recurring  []GroupChange `json:"recurring"` // masih gagal dengan error yang sama
	Regressed  []GroupChange `json:"regressed"` // pernah beres, sekarang gagal lagi
	Resolved   []GroupChange `json:"resolved"`  // dulu gagal, sekarang lulus
	FlakyTests []string      `json:"flaky_tests"`
	Incidents  []Incident    `json:"incidents"` // kegagalan run ini, dikelompokkan per penyebab, terbesar dulu
	// SharedStatus false: run ini (misalnya lokal) tidak mengubah status bersama;
	// New/Recurring/Regressed/Resolved hanya pratinjau.
	SharedStatus bool `json:"shared_status"`
}

// IngestReport menyimpan satu laporan Playwright dan memperbarui status kelompok kegagalan.
// Semuanya dalam satu transaksi: kalau ada yang gagal, tidak ada yang tersimpan.
func (s *Store) IngestReport(ctx context.Context, rep *report.Report, meta RunMeta) (*IngestResult, error) {
	if meta.Source == "" {
		meta.Source = "local"
	}
	outcomes := rep.Outcomes()
	res := &IngestResult{
		New: []GroupChange{}, Recurring: []GroupChange{}, Regressed: []GroupChange{},
		Resolved: []GroupChange{}, FlakyTests: []string{}, Incidents: []Incident{},
	}
	incidents := newIncidentSet()
	shapes := map[string]bool{} // isi calls JSON unik di run ini
	shared := s.SharesStatus(meta.Source)
	res.SharedStatus = shared

	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var started *time.Time
		if !rep.Stats.StartTime.IsZero() {
			started = &rep.Stats.StartTime
		}
		reportErrors := []string{}
		for _, e := range rep.Errors {
			reportErrors = append(reportErrors, triage.Clean(e.Message))
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO runs (started_at, duration_ms, source, triggered_by, ci_url, branch, commit_sha, app_version,
				playwright_version, report_errors)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) RETURNING id`,
			started, int(rep.Stats.Duration), meta.Source, meta.TriggeredBy, meta.CIURL, meta.Branch, meta.CommitSHA,
			meta.AppVersion, rep.Config.Version, reportErrors,
		).Scan(&res.RunID); err != nil {
			return fmt.Errorf("simpan run: %w", err)
		}

		sourceIDs, err := saveSources(ctx, tx, rep.Sources)
		if err != nil {
			return err
		}

		var passed testList
		for _, o := range outcomes {
			res.Total++
			fingerprint, cleanMsg, incidentKey := "", "", ""
			switch o.Status {
			case report.StatusPassed:
				res.Passed++
				passed.add(o)
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
				var kind, label string
				incidentKey, kind, label = triage.IncidentKey(normalized, o.Calls)
				if o.Status == report.StatusFailed {
					incidents.add(incidentKey, kind, triage.Clean(label))
				}
			}
			// Hanya kegagalan murni yang membuka/memperbarui kelompok.
			// Flaky dicatat di test_results saja (group_id tetap disimpan untuk analisis nanti).
			if o.Status == report.StatusFailed && fingerprint != "" {
				change, kind, err := upsertGroup(ctx, tx, res.RunID, o, fingerprint, cleanMsg, incidentKey, incidents.label(incidentKey), shared)
				if err != nil {
					return err
				}
				incidents.member(incidentKey, fingerprint, kind)
				switch kind {
				case "new":
					res.New = append(res.New, change)
				case "regressed":
					res.Regressed = append(res.Regressed, change)
				default:
					res.Recurring = append(res.Recurring, change)
				}
			}
			calls := callsJSON(o.Calls)
			if calls != "[]" {
				shapes[calls] = true
			}
			screenshots := o.Screenshots
			if screenshots == nil {
				screenshots = []string{}
			}
			codeFiles := o.CodeFiles
			if codeFiles == nil {
				codeFiles = []string{}
			}
			// Isi kode hanya untuk test yang punya error (gagal atau flaky): itulah yang dianalisis.
			sources := []string{}
			if o.ErrorMessage != "" {
				for _, p := range codeFiles {
					if id, ok := sourceIDs[p]; ok {
						sources = append(sources, id)
					}
				}
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO test_results (run_id, test_key, status, retries, duration_ms,
					error_message, error_snippet, error_location, group_id, cause_id, test_code_hash,
					response_shape_id, trace_path, screenshots, code_files, source_ids)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11,
					CASE WHEN $12::jsonb = '[]'::jsonb THEN '' ELSE md5($12::jsonb::text) END, $13, $14, $15, $16)`,
				res.RunID, o.Key(), o.Status, o.Retries, o.DurationMs,
				cleanMsg, triage.Clean(o.ErrorSnippet), o.ErrorLocation, fingerprint, incidentKey, o.SourceHash,
				calls, o.TracePath, screenshots, codeFiles, sources,
			); err != nil {
				return fmt.Errorf("simpan hasil %q: %w", o.Key(), err)
			}
		}

		// Satu query untuk semua bentuk unik; yang sudah ada dari run sebelumnya dilewati.
		if len(shapes) > 0 {
			list := make([]string, 0, len(shapes))
			for c := range shapes {
				list = append(list, c)
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO response_shapes (id, shape)
				SELECT md5(c::jsonb::text), c::jsonb FROM unnest($1::text[]) AS c
				ON CONFLICT (id) DO NOTHING`, list); err != nil {
				return fmt.Errorf("simpan bentuk response: %w", err)
			}
		}

		// Test yang lulus bersih menutup semua kelompok kegagalannya yang masih terbuka.
		// Run yang tidak mengubah status bersama hanya menampilkan pratinjaunya.
		if len(passed.project) > 0 {
			const match = `(project, file, title) IN (SELECT * FROM unnest($2::text[], $3::text[], $4::text[]))
				AND status IN ('open', 'regressed') AND last_seen_run <> $1`
			query := `UPDATE failure_groups SET status = 'resolved', resolved_run = $1
				WHERE ` + match + ` RETURNING id, ` + testKeySQL + `, last_error, occurrences`
			if !shared {
				query = `SELECT id, ` + testKeySQL + `, last_error, occurrences FROM failure_groups WHERE ` + match
			}
			rows, err := tx.Query(ctx, query, res.RunID, passed.project, passed.file, passed.title)
			if err != nil {
				return fmt.Errorf("tutup kelompok yang sudah lulus: %w", err)
			}
			for rows.Next() {
				var c GroupChange
				if err := rows.Scan(&c.Fingerprint, &c.TestKey, &c.Error, &c.Occurrences); err != nil {
					rows.Close()
					return err
				}
				c.Error = summarize(c.Error)
				res.Resolved = append(res.Resolved, c)
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return err
			}
		}

		_, err = tx.Exec(ctx, `UPDATE runs SET total=$2, passed=$3, failed=$4, flaky=$5, skipped=$6 WHERE id=$1`,
			res.RunID, res.Total, res.Passed, res.Failed, res.Flaky, res.Skipped)
		return err
	})
	if err != nil {
		return nil, err
	}
	res.Incidents = incidents.list()
	return res, nil
}

// maxSourceBytes: file yang lebih besar dari ini tidak disimpan (bukan file test biasa).
const maxSourceBytes = 50_000

// saveSources menyimpan isi file kode test (sudah disamarkan) sekali per isi, lalu mengembalikan
// path -> id. File yang sama di run berikutnya tidak disimpan ulang.
func saveSources(ctx context.Context, tx pgx.Tx, sources map[string]string) (map[string]string, error) {
	ids := map[string]string{}
	var idList, paths, contents []string
	for p, content := range sources {
		if p == "" || len(content) > maxSourceBytes {
			continue
		}
		clean := triage.Redact(content)
		sum := md5.Sum([]byte(p + "\x00" + clean))
		id := hex.EncodeToString(sum[:])
		ids[p] = id
		idList, paths, contents = append(idList, id), append(paths, p), append(contents, clean)
	}
	if len(idList) == 0 {
		return ids, nil
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO source_files (id, path, content)
		SELECT * FROM unnest($1::text[], $2::text[], $3::text[])
		ON CONFLICT (id) DO NOTHING`, idList, paths, contents); err != nil {
		return nil, fmt.Errorf("simpan kode test: %w", err)
	}
	return ids, nil
}

// callsJSON menyimpan bentuk response. Path diredaksi (jaga-jaga ada email atau token di path).
func callsJSON(calls []report.HTTPCall) string {
	clean := make([]report.HTTPCall, 0, len(calls))
	for _, c := range calls {
		if !json.Valid(c.Shape) {
			c.Shape = json.RawMessage("null")
		}
		c.Path = triage.Clean(c.Path)
		clean = append(clean, c)
	}
	b, err := json.Marshal(clean)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// upsertGroup membuat kelompok baru atau memperbarui yang sudah ada.
// kind: "new", "recurring", atau "regressed".
// shared false: kelompok baru dibuat sebagai local_only dan kelompok yang ada tidak diubah.
func upsertGroup(ctx context.Context, tx pgx.Tx, runID int64, o report.Outcome, fingerprint, cleanMsg, incidentKey, incidentLabel string, shared bool) (GroupChange, string, error) {
	change := GroupChange{Fingerprint: fingerprint, TestKey: o.Key(), Error: summarize(cleanMsg), Incident: incidentKey}

	var prevStatus string
	var localOnly bool
	err := tx.QueryRow(ctx, `SELECT status, local_only, occurrences FROM failure_groups WHERE id = $1 FOR UPDATE`,
		fingerprint).Scan(&prevStatus, &localOnly, &change.Occurrences)
	if err == pgx.ErrNoRows {
		_, err = tx.Exec(ctx, `
			INSERT INTO failure_groups (id, project, file, title, last_error, status, first_seen_run, last_seen_run,
				cause_id, cause, local_only)
			VALUES ($1, $2, $3, $4, $5, 'open', $6, $6, $7, $8, $9)
			ON CONFLICT (id) DO NOTHING`,
			fingerprint, o.Project, o.File, o.Title, cleanMsg, runID, incidentKey, incidentLabel, !shared)
		if err != nil {
			return change, "", fmt.Errorf("buat kelompok %s: %w", fingerprint, err)
		}
		change.Occurrences = 1
		return change, "new", nil
	}
	if err != nil {
		return change, "", err
	}

	kind := "recurring"
	if prevStatus == "resolved" {
		kind = "regressed"
	}
	if !shared {
		return change, kind, nil
	}
	if localOnly {
		// Pertama kali terlihat di run yang mengubah status: mulai sebagai kelompok bersama yang baru.
		_, err = tx.Exec(ctx, `
			UPDATE failure_groups SET
				local_only = false, status = 'open', occurrences = 1, regressions = 0,
				first_seen_run = $2, last_seen_run = $2, last_error = $3, resolved_run = NULL,
				cause_id = $4, cause = $5
			WHERE id = $1`, fingerprint, runID, cleanMsg, incidentKey, incidentLabel)
		if err != nil {
			return change, "", fmt.Errorf("jadikan kelompok bersama %s: %w", fingerprint, err)
		}
		change.Occurrences = 1
		return change, "new", nil
	}
	err = tx.QueryRow(ctx, `
		UPDATE failure_groups SET
			occurrences   = occurrences + 1,
			last_seen_run = $2,
			last_error    = $3,
			status        = CASE WHEN status = 'resolved' THEN 'regressed' ELSE status END,
			regressions   = regressions + CASE WHEN status = 'resolved' THEN 1 ELSE 0 END,
			resolved_run  = NULL,
			cause_id      = $4,
			cause         = $5
		WHERE id = $1
		RETURNING occurrences`, fingerprint, runID, cleanMsg, incidentKey, incidentLabel).Scan(&change.Occurrences)
	if err != nil {
		return change, "", fmt.Errorf("perbarui kelompok %s: %w", fingerprint, err)
	}
	return change, kind, nil
}

// summarize meringkas pesan error menjadi satu baris: baris pertama, ditambah baris yang
// paling informatif ("Expected: 201", "Received: 500", atau baris diff dari toEqual).
func summarize(s string) string {
	var first string
	var picked []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.Join(strings.Fields(line), " ")
		if line == "" {
			continue
		}
		if first == "" {
			first = line
			continue
		}
		if len(picked) < 3 && informative(line) {
			picked = append(picked, line)
		}
	}
	out := strings.Join(append([]string{first}, picked...), " | ")
	if r := []rune(out); len(r) > 240 {
		out = string(r[:240]) + "…"
	}
	return out
}

func informative(line string) bool {
	if strings.HasPrefix(line, "Expected") || strings.HasPrefix(line, "Received") {
		return true
	}
	// ZodError mencetak JSON; baris "message" berisi alasannya.
	if strings.HasPrefix(line, `"message":`) {
		return true
	}
	// Baris diff toEqual: "- \"status\": \"PAID\"," tapi bukan header "- Expected - 1".
	if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "+ ") {
		rest := line[2:]
		return !strings.HasPrefix(rest, "Expected") && !strings.HasPrefix(rest, "Received")
	}
	return false
}

// incidentSet mengumpulkan insiden selama ingest, dengan urutan kemunculan dipertahankan.
type incidentSet struct {
	order []string
	byKey map[string]*Incident
	rank  map[string]int // peringkat representative sekarang: 3 regressed, 2 new, 1 recurring
}

func newIncidentSet() *incidentSet {
	return &incidentSet{byKey: map[string]*Incident{}, rank: map[string]int{}}
}

func (s *incidentSet) add(key, kind, label string) {
	if _, ok := s.byKey[key]; ok {
		return
	}
	s.order = append(s.order, key)
	s.byKey[key] = &Incident{Key: key, Kind: kind, Label: label, Fingerprints: []string{}}
}

func (s *incidentSet) label(key string) string {
	if inc, ok := s.byKey[key]; ok {
		return inc.Label
	}
	return ""
}

// member mencatat satu kelompok kegagalan. kind dari upsertGroup: new, recurring, regressed.
func (s *incidentSet) member(key, fingerprint, kind string) {
	inc, ok := s.byKey[key]
	if !ok {
		return
	}
	inc.Tests++
	inc.Fingerprints = append(inc.Fingerprints, fingerprint)
	r := map[string]int{"regressed": 3, "new": 2}[kind]
	if r == 0 {
		r = 1
	}
	if r > s.rank[key] {
		s.rank[key] = r
		inc.Representative = fingerprint
	}
}

// list: insiden terbesar dulu; yang sama besar mengikuti urutan kemunculan.
func (s *incidentSet) list() []Incident {
	out := make([]Incident, 0, len(s.order))
	for _, k := range s.order {
		if inc := s.byKey[k]; inc.Tests > 0 {
			out = append(out, *inc)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Tests > out[j].Tests })
	return out
}

// testKeySQL menyusun test_key ("project › file › judul") dari kolom failure_groups,
// sama dengan report.Outcome.Key().
const testKeySQL = `project || ' › ' || file || ' › ' || title`

// testList mengumpulkan test sebagai tiga array sejajar, untuk dicocokkan dengan unnest di SQL.
type testList struct{ project, file, title []string }

func (l *testList) add(o report.Outcome) {
	l.project = append(l.project, o.Project)
	l.file = append(l.file, o.File)
	l.title = append(l.title, o.Title)
}
