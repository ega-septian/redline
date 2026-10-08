package store

import (
	"context"
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
	Source     string `json:"source"` // local / ci
	Branch     string `json:"branch"`
	CommitSHA  string `json:"commit"`
	AppVersion string `json:"app_version"`
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
			INSERT INTO runs (started_at, duration_ms, source, branch, commit_sha, app_version, playwright_version, report_errors)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`,
			started, int(rep.Stats.Duration), meta.Source, meta.Branch, meta.CommitSHA, meta.AppVersion,
			rep.Config.Version, reportErrors,
		).Scan(&res.RunID); err != nil {
			return fmt.Errorf("simpan run: %w", err)
		}

		var passedKeys []string
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
				var kind, label string
				incidentKey, kind, label = triage.IncidentKey(normalized, o.Calls)
				if o.Status == report.StatusFailed {
					incidents.add(incidentKey, kind, triage.Clean(label))
				}
			}
			// Hanya kegagalan murni yang membuka/memperbarui kelompok.
			// Flaky dicatat di test_results saja (fingerprint tetap disimpan untuk analisis nanti).
			if o.Status == report.StatusFailed && fingerprint != "" {
				change, kind, err := upsertGroup(ctx, tx, res.RunID, o, fingerprint, cleanMsg, incidentKey, incidents.label(incidentKey))
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
			if _, err := tx.Exec(ctx, `
				INSERT INTO test_results (run_id, test_key, project, file, title, line, status, retries, duration_ms,
					error_message, error_snippet, error_location, fingerprint, trace_path, screenshots,
					source_hash, calls_hash, incident_key)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16,
					CASE WHEN $17::jsonb = '[]'::jsonb THEN '' ELSE md5($17::jsonb::text) END, $18)`,
				res.RunID, o.Key(), o.Project, o.File, o.Title, o.Line, o.Status, o.Retries, o.DurationMs,
				cleanMsg, triage.Clean(o.ErrorSnippet), o.ErrorLocation, fingerprint, o.TracePath, screenshots,
				o.SourceHash, calls, incidentKey,
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
				INSERT INTO response_shapes (hash, calls)
				SELECT md5(c::jsonb::text), c::jsonb FROM unnest($1::text[]) AS c
				ON CONFLICT (hash) DO NOTHING`, list); err != nil {
				return fmt.Errorf("simpan bentuk response: %w", err)
			}
		}

		// Test yang lulus bersih menutup semua kelompok kegagalannya yang masih terbuka.
		if len(passedKeys) > 0 {
			rows, err := tx.Query(ctx, `
				UPDATE failure_groups
				SET status = 'resolved', resolved_run = $1, resolved_at = now()
				WHERE test_key = ANY($2) AND status IN ('open', 'regressed') AND last_seen_run <> $1
				RETURNING fingerprint, test_key, sample_error, occurrences`, res.RunID, passedKeys)
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

		_, err := tx.Exec(ctx, `UPDATE runs SET total=$2, passed=$3, failed=$4, flaky=$5, skipped=$6 WHERE id=$1`,
			res.RunID, res.Total, res.Passed, res.Failed, res.Flaky, res.Skipped)
		return err
	})
	if err != nil {
		return nil, err
	}
	res.Incidents = incidents.list()
	return res, nil
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
func upsertGroup(ctx context.Context, tx pgx.Tx, runID int64, o report.Outcome, fingerprint, cleanMsg, incidentKey, incidentLabel string) (GroupChange, string, error) {
	change := GroupChange{Fingerprint: fingerprint, TestKey: o.Key(), Error: summarize(cleanMsg), Incident: incidentKey}

	var prevStatus string
	err := tx.QueryRow(ctx, `SELECT status FROM failure_groups WHERE fingerprint = $1 FOR UPDATE`, fingerprint).Scan(&prevStatus)
	if err == pgx.ErrNoRows {
		_, err = tx.Exec(ctx, `
			INSERT INTO failure_groups (fingerprint, test_key, project, file, title, normalized_error, sample_error,
				status, first_seen_run, last_seen_run, incident_key, incident_label)
			VALUES ($1, $2, $3, $4, $5, $6, $7, 'open', $8, $8, $9, $10)
			ON CONFLICT (fingerprint) DO NOTHING`,
			fingerprint, o.Key(), o.Project, o.File, o.Title, triage.Normalize(cleanMsg), cleanMsg, runID,
			incidentKey, incidentLabel)
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
	err = tx.QueryRow(ctx, `
		UPDATE failure_groups SET
			occurrences   = occurrences + 1,
			last_seen_run = $2,
			last_seen_at  = now(),
			sample_error  = $3,
			status        = CASE WHEN status = 'resolved' THEN 'regressed' ELSE status END,
			regressions   = regressions + CASE WHEN status = 'resolved' THEN 1 ELSE 0 END,
			resolved_run  = NULL,
			resolved_at   = NULL,
			incident_key   = $4,
			incident_label = $5
		WHERE fingerprint = $1
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
