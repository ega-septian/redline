package reportportal

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"redline/internal/analysis"
	"redline/internal/store"
)

// RunAttribute: atribut launch ReportPortal yang menghubungkan launch dengan run Redline. Nilainya token yang
// sama yang dikirim reporter ke Redline (dibuat sekali per `playwright test`).
const RunAttribute = "redline_run"

// Locator jenis defect bawaan ReportPortal.
const (
	ToInvestigate = "ti001"
	ProductBug    = "pb001"
	AutomationBug = "ab001"
	SystemIssue   = "si001"
)

// DefectFor: kategori Redline → jenis defect ReportPortal. unknown tetap To Investigate (Redline tidak menebak);
// flaky menjadi Automation Bug karena ReportPortal tidak punya kategori flaky bawaan.
func DefectFor(category string) string {
	switch category {
	case "backend_bug":
		return ProductBug
	case "test_bug", "flaky":
		return AutomationBug
	case "environment":
		return SystemIssue
	}
	return ToInvestigate
}

// Analyzer menentukan penyebab satu kelompok kegagalan (lihat analysis.Analyzer).
type Analyzer interface {
	Analyze(ctx context.Context, fingerprint string, force bool) (*store.Analysis, bool, error)
}

// Store memberi daftar kegagalan satu run.
type Store interface {
	RunFailures(ctx context.Context, runID int64) ([]store.RunFailure, error)
}

// Result adalah ringkasan satu sinkronisasi.
type Result struct {
	Launch    int `json:"launch"`    // nomor launch ReportPortal
	Updated   int `json:"updated"`   // jenis defect diisi Redline
	Unclear   int `json:"unclear"`   // tetap To Investigate, hanya diberi komentar
	Labelled  int `json:"labelled"`  // sudah berlabel (manusia atau Auto-Analysis), tidak diubah
	Unmatched int `json:"unmatched"` // tidak ada kegagalan Redline dengan ID test case yang sama
}

// Syncer menunggu launch ReportPortal untuk sebuah run selesai, lalu mengisi item yang masih To Investigate.
type Syncer struct {
	Client   *Client
	Store    Store
	Analyzer Analyzer
	Log      *slog.Logger
	// Menunggu launch muncul dan ditutup: agen Playwright menutupnya setelah semua reporter selesai.
	Wait     time.Duration // default 5 menit
	Interval time.Duration // default 3 detik
}

// ErrLaunchNotFound: launch dengan token run itu tidak muncul atau tidak selesai dalam waktu tunggu.
var ErrLaunchNotFound = errors.New("launch ReportPortal tidak ditemukan atau belum selesai")

// Sync mengisi ReportPortal untuk run Redline runID, yang launch-nya membawa atribut redline_run=token.
func (s *Syncer) Sync(ctx context.Context, runID int64, token string) (Result, error) {
	launch, err := s.waitForLaunch(ctx, token)
	if err != nil {
		return Result{}, err
	}
	res := Result{Launch: launch.Number}
	items, err := s.Client.FailedItems(ctx, launch.ID)
	if err != nil {
		return res, err
	}
	failures, err := s.Store.RunFailures(ctx, runID)
	if err != nil {
		return res, err
	}
	byCase := map[string]store.RunFailure{}
	for _, f := range failures {
		if f.CaseID != "" {
			byCase[f.CaseID] = f
		}
	}

	updates := []IssueUpdate{}
	for _, item := range items {
		if t := item.IssueType(); t != "" && t != ToInvestigate {
			res.Labelled++ // label dari manusia atau Auto-Analysis ReportPortal tidak pernah ditimpa
			continue
		}
		f, ok := byCase[item.TestCaseID]
		if !ok || item.TestCaseID == "" {
			res.Unmatched++
			continue
		}
		a, _, err := s.Analyzer.Analyze(ctx, f.Fingerprint, false)
		if errors.Is(err, analysis.ErrAIDisabled) {
			// Aturan tidak bisa memutuskan dan AI mati: tetap To Investigate, dengan alasannya.
			res.Unclear++
			updates = append(updates, IssueUpdate{ItemID: item.ID, IssueType: ToInvestigate,
				Comment: "**Redline: unclear.** No rule could decide this failure, and AI analysis is off (ANTHROPIC_API_KEY)."})
			continue
		}
		if err != nil {
			s.logger().Warn("analisis untuk ReportPortal gagal", "test", f.TestKey, "err", err)
			res.Unmatched++
			continue
		}
		defect := DefectFor(a.Category)
		if defect == ToInvestigate {
			res.Unclear++
		} else {
			res.Updated++
		}
		updates = append(updates, IssueUpdate{ItemID: item.ID, IssueType: defect, Comment: Comment(a)})
	}
	if err := s.Client.UpdateIssues(ctx, updates); err != nil {
		return res, err
	}
	return res, nil
}

func (s *Syncer) waitForLaunch(ctx context.Context, token string) (*Launch, error) {
	wait, interval := s.Wait, s.Interval
	if wait == 0 {
		wait = 5 * time.Minute
	}
	if interval == 0 {
		interval = 3 * time.Second
	}
	deadline := time.Now().Add(wait)
	for {
		l, err := s.Client.LaunchByAttribute(ctx, RunAttribute, token)
		if err != nil {
			return nil, err
		}
		if l != nil && l.Finished() {
			return l, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("%w (%s=%s)", ErrLaunchNotFound, RunAttribute, token)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(interval):
		}
	}
}

func (s *Syncer) logger() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

var categoryNames = map[string]string{
	"backend_bug": "Product Bug", "test_bug": "Automation Bug", "environment": "System Issue",
	"flaky": "Flaky (Automation Bug)", "unknown": "unclear",
}

var sourceNames = map[string]string{
	"rule": "rules", "ai": "AI", "human": "a human label", "experiment": "an experiment (proven)",
}

// Comment: penjelasan Redline untuk ReportPortal (Markdown).
func Comment(a *store.Analysis) string {
	var b strings.Builder
	name := categoryNames[a.Category]
	if name == "" {
		name = a.Category
	}
	source := sourceNames[a.Source]
	if source == "" {
		source = a.Source
	}
	fmt.Fprintf(&b, "**Redline: %s** (%s confidence, by %s)\n\n%s\n", name, a.Confidence, source, a.Summary)
	if len(a.Evidence) > 0 {
		b.WriteString("\nEvidence:\n")
		for _, e := range a.Evidence {
			fmt.Fprintf(&b, "- `%s`\n", strings.ReplaceAll(e, "`", "'"))
		}
	}
	if a.NextStep != "" {
		fmt.Fprintf(&b, "\nNext step: %s\n", a.NextStep)
	}
	out := []rune(b.String())
	if len(out) > 4000 {
		return string(out[:4000]) + "…" // dipotong per huruf, bukan per byte, supaya UTF-8 tidak rusak
	}
	return string(out)
}
