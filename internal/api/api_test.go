package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"redline/internal/analysis"
	"redline/internal/contract"
	"redline/internal/notify"
	"redline/internal/report"
	"redline/internal/reportportal"
	"redline/internal/store"
	"redline/internal/triage"
)

type fakeStore struct {
	gotMeta  store.RunMeta
	gotTests int
	statuses []string
	label    string

	experiment         store.Experiment
	impact             string
	artifacts          []store.Artifact
	quarantineReleased bool
	sources            []store.ContractSource
	sourceChecks       []store.SourceCheck
	notifications      store.NotificationSettings
	channels           map[string]store.NotificationChannel
	apiKeys            []store.APIKey
	apiKeyErr          error
	spend              float64
	projectSpend       float64
	autoAccept         store.AutoAcceptStats
	shadowStats        []store.ShadowStat
	rules              []store.LearnedRule
	bulkConfirmed      int
	healthRows         []store.TestHealthRow
	testTrend          []store.TestHealthPoint
	runErrors          []store.Run
	loadErrors         []store.LoadError
	dismissals         map[string]time.Time
	dismissed          string
	ingest             *store.IngestResult
	deliveries         []store.NotificationDelivery
	expiring           []store.Quarantine
	expiryMarked       []string
	projects           []store.ProjectSummary
	lastRunProject     string
	lastGroupProject   string
	snapshots          []store.ContractSnapshot
	calls              []store.EndpointCall
	group              *store.Group
	occurrences        []store.Occurrence
	analysis           *store.Analysis
	knowledge          *store.Knowledge
	experiments        []store.Experiment
}

type fakeNotifier struct {
	webhook   string
	message   notify.Message
	err       error
	responses []error
	calls     int
	ch        chan struct{}
}

func (f *fakeNotifier) Send(_ context.Context, webhook string, message notify.Message) error {
	f.calls++
	f.webhook = webhook
	f.message = message
	if f.ch != nil {
		f.ch <- struct{}{}
	}
	if len(f.responses) > 0 {
		if f.calls <= len(f.responses) {
			return f.responses[f.calls-1]
		}
		return nil
	}
	return f.err
}

func (f *fakeStore) IngestReport(_ context.Context, rep *report.Report, meta store.RunMeta) (*store.IngestResult, error) {
	f.gotMeta = meta
	f.gotTests = len(rep.Outcomes())
	if f.ingest != nil {
		return f.ingest, nil
	}
	return &store.IngestResult{RunID: 7, Total: f.gotTests}, nil
}
func (f *fakeStore) ListRuns(context.Context, int) ([]store.Run, error) { return []store.Run{}, nil }
func (f *fakeStore) LoadErrors(_ context.Context, limit int) ([]store.LoadError, error) {
	out := f.loadErrors
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
func (f *fakeStore) LoadError(_ context.Context, id string) (store.LoadError, error) {
	for _, item := range f.loadErrors {
		if item.ID == id {
			return item, nil
		}
	}
	return store.LoadError{}, store.ErrNotFound
}
func (f *fakeStore) SetLoadErrorAnalysis(_ context.Context, id string, analysis triage.LoadErrorAnalysis) error {
	for i := range f.loadErrors {
		if f.loadErrors[i].ID != id {
			continue
		}
		f.loadErrors[i].Category = analysis.Category
		f.loadErrors[i].Confidence = analysis.Confidence
		f.loadErrors[i].AnalysisSource = analysis.Source
		f.loadErrors[i].Summary = analysis.Summary
		f.loadErrors[i].NextStep = analysis.NextStep
		return nil
	}
	return store.ErrNotFound
}
func (f *fakeStore) SetLoadErrorTriage(_ context.Context, id, status, owner, note, by string) error {
	for i := range f.loadErrors {
		if f.loadErrors[i].ID != id {
			continue
		}
		f.loadErrors[i].TriageStatus = status
		f.loadErrors[i].TriageOwner = owner
		f.loadErrors[i].TriageNote = note
		f.loadErrors[i].TriageUpdatedBy = by
		return nil
	}
	return store.ErrNotFound
}
func (f *fakeStore) RunsWithReportErrors(_ context.Context, limit int) ([]store.Run, error) {
	out := f.runErrors
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
func (f *fakeStore) ListRunsForProject(_ context.Context, project string, limit int) ([]store.Run, error) {
	f.lastRunProject = project
	return f.ListRuns(context.Background(), limit)
}
func (f *fakeStore) ProjectSummaries(context.Context) ([]store.ProjectSummary, error) {
	return f.projects, nil
}
func (f *fakeStore) GetRun(_ context.Context, id int64) (store.Run, error) {
	if id != 1 {
		return store.Run{}, store.ErrNotFound
	}
	return store.Run{ID: 1, Total: 2, Failed: 1}, nil
}
func (f *fakeStore) PreviousRun(_ context.Context, id int64, _ string) (store.Run, bool, error) {
	if id != 1 {
		return store.Run{}, false, nil
	}
	return store.Run{ID: 0, Total: 2, Failed: 2}, true, nil
}
func (f *fakeStore) RunResults(_ context.Context, runID int64) ([]store.RunResult, error) {
	if runID != 0 && runID != 1 {
		return []store.RunResult{}, nil
	}
	if runID == 0 {
		return []store.RunResult{
			{TestKey: "toolshop › api.spec.ts › register", TestCaseID: "TC-USR-001", Status: "failed", Fingerprint: "a"},
			{TestKey: "toolshop › api.spec.ts › health", Status: "failed"},
		}, nil
	}
	return []store.RunResult{
		{TestKey: "toolshop › api.spec.ts › register", TestCaseID: "TC-USR-001", Status: "failed", DurationMs: 120, Fingerprint: "a"},
		{TestKey: "toolshop › api.spec.ts › health", Status: "passed", DurationMs: 10},
	}, nil
}
func (f *fakeStore) SaveArtifact(_ context.Context, a store.Artifact) (int64, error) {
	a.ID = int64(len(f.artifacts) + 1)
	f.artifacts = append(f.artifacts, a)
	return a.ID, nil
}
func (f *fakeStore) ListArtifacts(_ context.Context, runID int64) ([]store.Artifact, error) {
	out := []store.Artifact{}
	for _, a := range f.artifacts {
		if a.RunID == runID {
			out = append(out, a)
		}
	}
	return out, nil
}
func (f *fakeStore) GetArtifact(_ context.Context, id int64) (store.Artifact, error) {
	for _, a := range f.artifacts {
		if a.ID == id {
			return a, nil
		}
	}
	return store.Artifact{}, store.ErrNotFound
}
func (f *fakeStore) TestHealthTrend(_ context.Context, _ string, _ int, limit int) ([]store.TestHealthPoint, error) {
	out := f.testTrend
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}
func (f *fakeStore) TestHealth(context.Context, string, int) (store.TestHealthSummary, []store.TestHealthRow, error) {
	if f.healthRows != nil {
		return store.TestHealthSummary{}, f.healthRows, nil
	}
	row := store.TestHealthRow{
		Key: "TC-BRD-001", TestKey: "toolshop › brand.spec.ts › get all", TestCaseID: "TC-BRD-001",
		Total: 2, Passed: 1, Failed: 1, PassRate: 0.5, LastStatus: "failed",
	}
	return store.TestHealthSummary{
		Days: 30, Runs: 1, Results: 2, Passed: 1, Failed: 1, PassRate: 0.5, OpenFailures: 1,
	}, []store.TestHealthRow{row}, nil
}
func (f *fakeStore) DismissQuarantineSuggestion(_ context.Context, key string, until time.Time, _ string) error {
	if f.dismissals == nil {
		f.dismissals = map[string]time.Time{}
	}
	f.dismissals[key] = until
	f.dismissed = key
	return nil
}
func (f *fakeStore) SuggestionDismissals(context.Context) (map[string]time.Time, error) {
	if f.dismissals == nil {
		return map[string]time.Time{}, nil
	}
	return f.dismissals, nil
}
func (f *fakeStore) TestHistory(_ context.Context, test string, _ int, _ int) (store.TestHealthRow, []store.TestRunPoint, error) {
	if test != "TC-BRD-001" {
		return store.TestHealthRow{}, nil, store.ErrNotFound
	}
	row := store.TestHealthRow{
		Key: "TC-BRD-001", TestKey: "toolshop › brand.spec.ts › get all", TestCaseID: "TC-BRD-001",
		Total: 1, Failed: 1, PassRate: 0, LastStatus: "failed",
	}
	return row, []store.TestRunPoint{{RunID: 1, Status: "failed", DurationMs: 12}}, nil
}
func (f *fakeStore) FindTest(_ context.Context, test string) (store.TestIdentity, error) {
	if test != "TC-BRD-001" && test != "toolshop › brand.spec.ts › get all" {
		return store.TestIdentity{}, store.ErrNotFound
	}
	return store.TestIdentity{
		Key: "TC-BRD-001", TestKey: "toolshop › brand.spec.ts › get all",
		TestCaseID: "TC-BRD-001", Project: "toolshop",
	}, nil
}
func (f *fakeStore) SetQuarantine(_ context.Context, q store.Quarantine) (store.Quarantine, error) {
	return q, nil
}
func (f *fakeStore) ReleaseQuarantine(_ context.Context, key, by string) (store.Quarantine, error) {
	if key != "TC-BRD-001" || f.quarantineReleased {
		return store.Quarantine{}, store.ErrNotFound
	}
	f.quarantineReleased = true
	return store.Quarantine{Key: key, ReleasedBy: by}, nil
}
func (f *fakeStore) ActiveQuarantines(context.Context) ([]store.Quarantine, error) {
	return []store.Quarantine{{
		Key: "TC-BRD-001", TestKey: "toolshop › brand.spec.ts › get all",
		TestCaseID: "TC-BRD-001", Project: "toolshop", Owner: "ega", Reason: "flaky",
	}}, nil
}
func (f *fakeStore) ExpiringQuarantines(context.Context, time.Duration) ([]store.Quarantine, error) {
	return f.expiring, nil
}
func (f *fakeStore) MarkQuarantineExpiryNotified(_ context.Context, keys []string) error {
	f.expiryMarked = append(f.expiryMarked, keys...)
	return nil
}
func (f *fakeStore) ContractSnapshots(_ context.Context, project string, _ int) ([]store.ContractSnapshot, error) {
	if f.snapshots != nil {
		return f.snapshots, nil
	}
	if project != "toolshop" {
		return nil, nil
	}
	now := time.Now()
	return []store.ContractSnapshot{
		{RunID: 2, At: now, ContractID: "new", SourceURL: "http://example/docs", Document: `{
			"openapi":"3.0.0","paths":{
				"/brands":{"get":{"responses":{"200":{"description":"ok"},"401":{"description":"auth"}}}},
				"/products":{"get":{"responses":{"200":{"description":"ok"}}}}
			}}`},
		{RunID: 1, At: now.Add(-time.Hour), ContractID: "old", Document: `{
			"openapi":"3.0.0","paths":{
				"/brands":{"get":{"responses":{"200":{"description":"ok"}}}},
				"/old":{"get":{"responses":{"200":{"description":"ok"}}}}
			}}`},
	}, nil
}
func (f *fakeStore) EndpointCalls(_ context.Context, project string) ([]store.EndpointCall, error) {
	if f.calls != nil {
		return f.calls, nil
	}
	if project != "toolshop" {
		return []store.EndpointCall{}, nil
	}
	now := time.Now()
	return []store.EndpointCall{
		{RunID: 2, Method: "GET", Path: "/brands", Status: 200, TestKey: "toolshop › api.spec.ts › get", At: now},
		{RunID: 2, Method: "GET", Path: "/brands", Status: 401, TestKey: "toolshop › api.spec.ts › auth", At: now},
		{RunID: 2, Method: "GET", Path: "/products", Status: 200, TestKey: "toolshop › api.spec.ts › products", At: now},
		{RunID: 1, Method: "GET", Path: "/brands", Status: 200, TestKey: "toolshop › api.spec.ts › old", At: now.Add(-time.Hour)},
	}, nil
}
func (f *fakeStore) ListGroups(_ context.Context, st []string, _ int) ([]store.Group, error) {
	f.statuses = st
	return []store.Group{}, nil
}
func (f *fakeStore) ContractSources(context.Context) ([]store.ContractSource, error) {
	return f.sources, nil
}
func (f *fakeStore) ContractSource(_ context.Context, project string) (store.ContractSource, error) {
	for _, source := range f.sources {
		if source.Project == project {
			return source, nil
		}
	}
	return store.ContractSource{}, store.ErrNotFound
}
func (f *fakeStore) SetContractSource(_ context.Context, src store.ContractSource) (store.ContractSource, error) {
	src.UpdatedAt = time.Now()
	for i := range f.sources {
		if f.sources[i].Project == src.Project {
			src.LastCheckedAt = f.sources[i].LastCheckedAt
			f.sources[i] = src
			return src, nil
		}
	}
	f.sources = append(f.sources, src)
	return src, nil
}
func (f *fakeStore) DeleteContractSource(_ context.Context, project string) error {
	for i := range f.sources {
		if f.sources[i].Project == project {
			f.sources = append(f.sources[:i], f.sources[i+1:]...)
			return nil
		}
	}
	return store.ErrNotFound
}
func (f *fakeStore) RecordSourceCheck(_ context.Context, project string, record store.SourceCheckRecord) error {
	for i := range f.sources {
		if f.sources[i].Project != project {
			continue
		}
		now := time.Now()
		f.sources[i].LastCheckedAt = &now
		f.sources[i].LastStatus = record.Status
		f.sources[i].LastError = record.Error
		f.sources[i].LastVersion = record.Version
		f.sources[i].LastEndpoints = record.Endpoints
		return nil
	}
	return store.ErrNotFound
}
func (f *fakeStore) SourceChecks(_ context.Context, _ string, limit int) ([]store.SourceCheck, error) {
	out := f.sourceChecks
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
func (f *fakeStore) NotificationSettings(context.Context) (store.NotificationSettings, error) {
	return f.notifications, nil
}
func (f *fakeStore) RecordNotificationDelivery(_ context.Context, d store.NotificationDelivery) error {
	f.deliveries = append(f.deliveries, d)
	return nil
}
func (f *fakeStore) LastNotificationDelivery(context.Context) (*store.NotificationDelivery, error) {
	if len(f.deliveries) == 0 {
		return nil, nil
	}
	last := f.deliveries[len(f.deliveries)-1]
	return &last, nil
}
func (f *fakeStore) NotificationDeliveries(_ context.Context, limit int) ([]store.NotificationDelivery, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	out := make([]store.NotificationDelivery, 0, len(f.deliveries))
	for i := len(f.deliveries) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, f.deliveries[i])
	}
	return out, nil
}
func (f *fakeStore) SetNotificationSettings(_ context.Context, settings store.NotificationSettings) (store.NotificationSettings, error) {
	settings.UpdatedAt = time.Now()
	f.notifications = settings
	return settings, nil
}
func (f *fakeStore) NotificationSettingsFor(_ context.Context, project string) (store.NotificationSettings, error) {
	if project != "" {
		if channel, ok := f.channels[project]; ok {
			return store.NotificationSettings{
				Enabled: channel.Enabled, WebhookURL: channel.WebhookURL,
				Events: channel.Events, By: channel.By, UpdatedAt: channel.UpdatedAt,
			}, nil
		}
	}
	return f.NotificationSettings(context.Background())
}
func (f *fakeStore) NotificationChannels(context.Context) ([]store.NotificationChannel, error) {
	out := make([]store.NotificationChannel, 0, len(f.channels))
	for _, channel := range f.channels {
		out = append(out, channel)
	}
	return out, nil
}
func (f *fakeStore) NotificationChannel(_ context.Context, project string) (store.NotificationChannel, error) {
	if channel, ok := f.channels[project]; ok {
		return channel, nil
	}
	return store.NotificationChannel{}, store.ErrNotFound
}
func (f *fakeStore) SetNotificationChannel(_ context.Context, channel store.NotificationChannel) (store.NotificationChannel, error) {
	if f.channels == nil {
		f.channels = map[string]store.NotificationChannel{}
	}
	channel.UpdatedAt = time.Now()
	f.channels[channel.Project] = channel
	return channel, nil
}
func (f *fakeStore) DeleteNotificationChannel(_ context.Context, project string) error {
	if _, ok := f.channels[project]; !ok {
		return store.ErrNotFound
	}
	delete(f.channels, project)
	return nil
}
func (f *fakeStore) CreateAPIKey(_ context.Context, key store.APIKey) (store.APIKey, error) {
	if f.apiKeyErr != nil {
		return store.APIKey{}, f.apiKeyErr
	}
	for _, existing := range f.apiKeys {
		if existing.Name == key.Name {
			return store.APIKey{}, store.ErrAPIKeyExists
		}
	}
	key.ID = "key-" + strconv.Itoa(len(f.apiKeys)+1)
	key.CreatedAt = time.Now()
	f.apiKeys = append([]store.APIKey{key}, f.apiKeys...)
	return key, nil
}
func (f *fakeStore) SpendSince(context.Context, time.Time) (float64, error) {
	return f.spend, nil
}
func (f *fakeStore) ListAPIKeys(context.Context) ([]store.APIKey, error) {
	return f.apiKeys, f.apiKeyErr
}
func (f *fakeStore) RevokeAPIKey(_ context.Context, id string) (store.APIKey, error) {
	for i := range f.apiKeys {
		if f.apiKeys[i].ID != id {
			continue
		}
		if f.apiKeys[i].RevokedAt == nil {
			now := time.Now()
			f.apiKeys[i].RevokedAt = &now
		}
		return f.apiKeys[i], nil
	}
	return store.APIKey{}, store.ErrNotFound
}
func (f *fakeStore) ListGroupsForProject(_ context.Context, statuses []string, limit int, project string) ([]store.Group, error) {
	f.lastGroupProject = project
	return f.ListGroups(context.Background(), statuses, limit)
}
func (f *fakeStore) GetGroup(_ context.Context, _ string) (store.Group, []store.Occurrence, error) {
	if f.group != nil {
		return *f.group, f.occurrences, nil
	}
	return store.Group{}, nil, store.ErrNotFound
}
func (f *fakeStore) SpendSinceProject(context.Context, time.Time, string) (float64, error) {
	return f.projectSpend, nil
}

func (f *fakeStore) AutoAcceptStats(_ context.Context, project string) (store.AutoAcceptStats, error) {
	stats := f.autoAccept
	stats.Project = project
	return stats, nil
}

func (f *fakeStore) ShadowStats(context.Context, string) ([]store.ShadowStat, error) {
	return f.shadowStats, nil
}

func (f *fakeStore) ConfirmAcceptanceBulk(context.Context, []string, string) (int, error) {
	return f.bulkConfirmed, nil
}

func (f *fakeStore) GetAnalysis(context.Context, string, string) (store.Analysis, error) {
	if f.analysis != nil {
		return *f.analysis, nil
	}
	return store.Analysis{}, store.ErrNotFound
}
func (f *fakeStore) SetLabel(_ context.Context, fp, label, note, by string) error {
	if fp != "ada" {
		return store.ErrNotFound
	}
	f.label = label + "|" + note + "|" + by
	return nil
}
func (f *fakeStore) SetTriage(_ context.Context, fp, status, owner, note, by string) error {
	if fp != "ada" {
		return store.ErrNotFound
	}
	f.label = status + "|" + owner + "|" + note + "|" + by
	return nil
}
func (f *fakeStore) SetTriageStatusBulk(_ context.Context, ids []string, status, by string) (int, error) {
	f.label = status + "|" + by
	return len(ids), nil
}
func (f *fakeStore) SetTriageOwnerBulk(_ context.Context, ids []string, owner, by string) (int, error) {
	f.label = owner + "|" + by
	return len(ids), nil
}

func (f *fakeStore) SaveExperiment(_ context.Context, e store.Experiment) (int64, error) {
	f.experiment = e
	return 1, nil
}
func (f *fakeStore) ListExperiments(context.Context, string) ([]store.Experiment, error) {
	if f.experiments != nil {
		return f.experiments, nil
	}
	return []store.Experiment{}, nil
}
func (f *fakeStore) ListRules(context.Context, string) ([]store.LearnedRule, error) {
	return []store.LearnedRule{}, nil
}
func (f *fakeStore) GetRule(_ context.Context, id int64) (store.LearnedRule, error) {
	for _, rule := range f.rules {
		if rule.ID == id {
			return rule, nil
		}
	}
	return store.LearnedRule{}, store.ErrNotFound
}
func (f *fakeStore) ListRuleCandidates(context.Context, string, string) ([]store.RuleCandidate, error) {
	return []store.RuleCandidate{}, nil
}
func (f *fakeStore) DecideRule(_ context.Context, id int64, status, by string) (store.LearnedRule, error) {
	return store.LearnedRule{ID: id, Status: status, DecidedBy: by}, nil
}

func (f *fakeStore) Scores(_ context.Context, groupID string, _ int) (*store.Scoreboard, error) {
	return &store.Scoreboard{Sources: []store.Score{}, Entries: []store.ScoreEntry{
		{GroupID: groupID, Source: "ai", Predicted: "backend_bug", Truth: "test_bug"}}}, nil
}

func (f *fakeStore) Knowledge(_ context.Context, fp string) (*store.Knowledge, error) {
	if f.knowledge != nil {
		return f.knowledge, nil
	}
	if fp != "abc" {
		return nil, store.ErrNotFound
	}
	return &store.Knowledge{RunID: 2, PassRunID: 1, Compared: true, Files: []store.FileFact{}, Endpoints: []store.EndpointFact{}}, nil
}
func (f *fakeStore) ImpactOfFile(_ context.Context, path string) ([]store.ImpactedTest, error) {
	f.impact = "file:" + path
	return []store.ImpactedTest{{TestKey: "api › brands.spec.ts › get", CaseID: "TC-1", RunID: 3}}, nil
}
func (f *fakeStore) ImpactOfEndpoint(_ context.Context, endpoint string) ([]store.ImpactedTest, error) {
	f.impact = "endpoint:" + endpoint
	return []store.ImpactedTest{}, nil
}

type fakeAnalyzer struct {
	force    bool
	err      error
	promoted []string
}

func (a *fakeAnalyzer) Analyze(_ context.Context, fp string, force bool) (*store.Analysis, bool, error) {
	a.force = force
	if a.err != nil {
		return nil, false, a.err
	}
	return &store.Analysis{Fingerprint: fp, Source: "ai", Category: "backend_bug"}, !force, nil
}

func (a *fakeAnalyzer) AnalyzeLoadError(_ context.Context, message string) (triage.LoadErrorAnalysis, error) {
	if a.err != nil {
		return triage.LoadErrorAnalysis{}, a.err
	}
	return triage.LoadErrorAnalysis{
		Category: "test_bug", Confidence: "medium", Source: "ai",
		Summary: "AI classified: " + message, NextStep: "Fix the import.",
	}, nil
}
func (a *fakeAnalyzer) AnalyzeLocal(_ context.Context, fp string, _ bool) (*store.Analysis, error) {
	if a.err != nil {
		return nil, a.err
	}
	return &store.Analysis{Fingerprint: fp, Source: "none", Category: "unknown"}, nil
}

func (a *fakeAnalyzer) PromoteCandidates(_ context.Context, fingerprint string) (*analysis.PromotionResult, error) {
	a.promoted = append(a.promoted, fingerprint)
	return &analysis.PromotionResult{Promoted: []store.LearnedRule{}, Rejected: []analysis.RejectedCandidate{}}, nil
}

func (a *fakeAnalyzer) ProposeFix(context.Context, string, []analysis.SourceFile, []analysis.Attempt) (*analysis.Fix, error) {
	return &analysis.Fix{Category: "test_bug", Edits: []analysis.Edit{}}, nil
}
func (a *fakeAnalyzer) EmbedPending(context.Context) (int, error)          { return 0, nil }
func (a *fakeAnalyzer) SnapshotContracts(context.Context, int64, []string) {}

func (a *fakeAnalyzer) ProposeRules(context.Context) (*analysis.LearnResult, error) {
	return nil, analysis.ErrNothingToLearn
}

func newServer(fs *fakeStore, max int64) http.Handler {
	return (&Server{Store: fs, Analyzer: &fakeAnalyzer{}, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), MaxBodySize: max}).Routes()
}

func do(h http.Handler, method, url string, body io.Reader) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, url, body))
	return rec
}

func TestIngest(t *testing.T) {
	fs := &fakeStore{}
	f, _ := os.Open("../report/testdata/run1-bug.json")
	defer f.Close()
	rec := do(newServer(fs, 0), "POST", "/api/runs?source=ci&branch=main&commit=abc&app_version=sprint5", f)
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"run_id": 7`) {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if fs.gotMeta != (store.RunMeta{Source: "ci", Branch: "main", CommitSHA: "abc", AppVersion: "sprint5"}) || fs.gotTests != 6 {
		t.Errorf("metadata/test tidak diteruskan: %+v %d", fs.gotMeta, fs.gotTests)
	}
}

func TestIngest_Rejects(t *testing.T) {
	h := newServer(&fakeStore{}, 100)
	cases := []struct {
		url, body string
		code      int
	}{
		{"/api/runs", `bukan json`, http.StatusBadRequest},
		{"/api/runs", `{"foo":1}`, http.StatusBadRequest},
		{"/api/runs?source=prod", `{"suites":[]}`, http.StatusBadRequest},
		{"/api/runs", `{"suites":[` + strings.Repeat(`{"title":"x"},`, 20) + `{}]}`, http.StatusRequestEntityTooLarge},
	}
	for _, c := range cases {
		if rec := do(h, "POST", c.url, strings.NewReader(c.body)); rec.Code != c.code {
			t.Errorf("%s %q: mau %d, dapat %d %s", c.url, c.body[:min(len(c.body), 20)], c.code, rec.Code, rec.Body)
		}
	}
}

func TestListGroups_StatusFilter(t *testing.T) {
	fs := &fakeStore{}
	h := newServer(fs, 0)
	if rec := do(h, "GET", "/api/groups", nil); rec.Code != 200 || fs.statuses != nil {
		t.Errorf("default harus nil (open+regressed): %v", fs.statuses)
	}
	do(h, "GET", "/api/groups?status=all", nil)
	if len(fs.statuses) != 3 {
		t.Errorf("all harus 3 status: %v", fs.statuses)
	}
	if rec := do(h, "GET", "/api/groups?status=lol", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("status tidak dikenal harus 400, dapat %d", rec.Code)
	}
	if rec := do(h, "GET", "/api/groups/abc", nil); rec.Code != http.StatusNotFound {
		t.Errorf("kelompok tidak ada harus 404, dapat %d", rec.Code)
	}
}

func TestAnalyze(t *testing.T) {
	az := &fakeAnalyzer{}
	h := (&Server{Store: &fakeStore{}, Analyzer: az, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}).Routes()

	rec := do(h, "POST", "/api/groups/abc/analyze", nil)
	if rec.Code != 200 || az.force || !strings.Contains(rec.Body.String(), `"cached": true`) {
		t.Errorf("tanpa force harus boleh pakai cache: %d %s", rec.Code, rec.Body)
	}
	do(h, "POST", "/api/groups/abc/analyze?force=1", nil)
	if !az.force {
		t.Error("force=1 harus diteruskan")
	}

	for err, code := range map[error]int{
		store.ErrNotFound:        http.StatusNotFound,
		analysis.ErrAIDisabled:   http.StatusServiceUnavailable,
		errors.New("claude 529"): http.StatusBadGateway,
	} {
		az.err = err
		if rec := do(h, "POST", "/api/groups/abc/analyze", nil); rec.Code != code {
			t.Errorf("%v: mau %d, dapat %d", err, code, rec.Code)
		}
	}
}

func TestLabel(t *testing.T) {
	fs := &fakeStore{}
	h := newServer(fs, 0)
	rec := do(h, "PUT", "/api/groups/ada/label", strings.NewReader(`{"label":"test_bug","note":"assertion usang","by":"ega"}`))
	if rec.Code != 200 || fs.label != "test_bug|assertion usang|ega" {
		t.Errorf("label tidak tersimpan: %d %s %q", rec.Code, rec.Body, fs.label)
	}
	if rec := do(h, "PUT", "/api/groups/ada/label", strings.NewReader(`{"label":"salah"}`)); rec.Code != 400 {
		t.Errorf("label tidak dikenal harus 400, dapat %d", rec.Code)
	}
	if rec := do(h, "PUT", "/api/groups/ada/label", strings.NewReader(`{"label":"flaky"}`)); rec.Code != 400 {
		t.Errorf("label tanpa \"by\" harus 400, dapat %d", rec.Code)
	}
	if rec := do(h, "PUT", "/api/groups/tidakada/label", strings.NewReader(`{"label":"flaky","by":"ega"}`)); rec.Code != 404 {
		t.Errorf("kelompok tidak ada harus 404, dapat %d", rec.Code)
	}
	if rec := do(h, "PUT", "/api/groups/ada/label", strings.NewReader(`{"label":""}`)); rec.Code != 200 || fs.label != "||" {
		t.Errorf("label kosong harus menghapus label: %q", fs.label)
	}
}

func TestSaveExperiment(t *testing.T) {
	cases := map[string]int{
		`{"kind":"patch","outcome":"passed","category":"test_bug","runs":1,"passes":1,"patch":"diff"}`: http.StatusCreated,
		`{"kind":"rerun","outcome":"failed","runs":2,"passes":0}`:                                      http.StatusCreated,
		`{"kind":"guess","outcome":"passed","runs":1,"passes":1}`:                                      http.StatusBadRequest,
		`{"kind":"patch","outcome":"passed","runs":1,"passes":0}`:                                      http.StatusBadRequest,
		`{"kind":"patch","outcome":"failed","runs":1,"passes":2}`:                                      http.StatusBadRequest,
		`{"kind":"patch","outcome":"passed","category":"magic","runs":1,"passes":1}`:                   http.StatusBadRequest,
	}
	for body, want := range cases {
		fs := &fakeStore{}
		rec := do(newServer(fs, 0), "POST", "/api/groups/abc/experiments", strings.NewReader(body))
		if rec.Code != want {
			t.Errorf("%s: status %d, mau %d (%s)", body, rec.Code, want, rec.Body)
		}
		if want == http.StatusCreated && fs.experiment.GroupID != "abc" {
			t.Errorf("group id dari path tidak dipakai: %+v", fs.experiment)
		}
		// Eksperimen lulus langsung mengembalikan penilaian tebakan sebelumnya.
		if hasVerdict := strings.Contains(rec.Body.String(), `"truth": "test_bug"`); want == http.StatusCreated &&
			hasVerdict != strings.Contains(body, `"outcome":"passed"`) {
			t.Errorf("%s: verdicts hanya untuk outcome passed: %s", body, rec.Body)
		}
	}
}

func TestDecideRule(t *testing.T) {
	h := newServer(&fakeStore{}, 0)
	if rec := do(h, "PUT", "/api/rules/3", strings.NewReader(`{"status":"active","by":"ega"}`)); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), `"status": "active"`) {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	for _, body := range []string{`{"status":"active"}`, `{"status":"maybe","by":"ega"}`} {
		if rec := do(h, "PUT", "/api/rules/3", strings.NewReader(body)); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: mau 400, dapat %d", body, rec.Code)
		}
	}
	if rec := do(h, "PUT", "/api/rules/abc", strings.NewReader(`{"status":"active","by":"ega"}`)); rec.Code != http.StatusBadRequest {
		t.Errorf("id bukan angka: mau 400, dapat %d", rec.Code)
	}
}

func TestProposeRules_NothingToLearn(t *testing.T) {
	rec := do(newServer(&fakeStore{}, 0), "POST", "/api/rules/propose", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "no new proven cases") {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
}

func TestImpact(t *testing.T) {
	fs := &fakeStore{}
	h := newServer(fs, 0)
	rec := do(h, "GET", "/api/impact?file=schemas/brand.ts", nil)
	if rec.Code != 200 || fs.impact != "file:schemas/brand.ts" || !strings.Contains(rec.Body.String(), `"test_case_id": "TC-1"`) {
		t.Fatalf("impact file: %d %s %s", rec.Code, fs.impact, rec.Body)
	}
	rec = do(h, "GET", "/api/impact?endpoint=GET+%2Fbrands%2F1", nil)
	if rec.Code != 200 || fs.impact != "endpoint:GET /brands/1" || !strings.Contains(rec.Body.String(), `"tests": []`) {
		t.Fatalf("impact endpoint: %d %s %s", rec.Code, fs.impact, rec.Body)
	}
	for _, q := range []string{"", "?file=a.ts&endpoint=GET+/x", "?file=+"} {
		if rec := do(h, "GET", "/api/impact"+q, nil); rec.Code != http.StatusBadRequest {
			t.Errorf("%q harus 400, dapat %d", q, rec.Code)
		}
	}
}

func TestKnowledge(t *testing.T) {
	h := newServer(&fakeStore{}, 0)
	if rec := do(h, "GET", "/api/groups/abc/knowledge", nil); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"compared": true`) {
		t.Errorf("knowledge: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "GET", "/api/groups/tidakada/knowledge", nil); rec.Code != http.StatusNotFound {
		t.Errorf("kelompok tidak ada harus 404, dapat %d", rec.Code)
	}
}

type fakeRP struct {
	run   int64
	token string
	done  chan struct{}
}

func (f *fakeRP) Sync(_ context.Context, runID int64, token string) (reportportal.Result, error) {
	f.run, f.token = runID, token
	close(f.done)
	return reportportal.Result{Launch: 1}, nil
}

func TestReportPortal(t *testing.T) {
	if rec := do(newServer(&fakeStore{}, 0), "POST", "/api/runs/3/reportportal?token=abc", nil); rec.Code != http.StatusNotFound {
		t.Errorf("tanpa REPORTPORTAL_URL harus 404, dapat %d", rec.Code)
	}
	rp := &fakeRP{done: make(chan struct{})}
	h := (&Server{Store: &fakeStore{}, Analyzer: &fakeAnalyzer{}, ReportPortal: rp,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))}).Routes()
	for _, url := range []string{"/api/runs/x/reportportal?token=abc", "/api/runs/3/reportportal",
		"/api/runs/3/reportportal?token=a%20b", "/api/runs/0/reportportal?token=abc"} {
		if rec := do(h, "POST", url, nil); rec.Code != http.StatusBadRequest {
			t.Errorf("%s harus 400, dapat %d", url, rec.Code)
		}
	}
	rec := do(h, "POST", "/api/runs/3/reportportal?token=5f0c-a1", nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("harus 202, dapat %d %s", rec.Code, rec.Body)
	}
	select {
	case <-rp.done:
	case <-time.After(2 * time.Second):
		t.Fatal("sinkronisasi tidak dijalankan di background")
	}
	if rp.run != 3 || rp.token != "5f0c-a1" {
		t.Errorf("run dan token: %d %q", rp.run, rp.token)
	}
}

func TestAPIKey(t *testing.T) {
	h := (&Server{Store: &fakeStore{}, Analyzer: &fakeAnalyzer{}, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), APIKey: "rahasia-123",
		Ready: func(context.Context) error { return nil }}).Routes()
	call := func(path, auth string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", path, nil)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		h.ServeHTTP(rec, req)
		return rec
	}
	cases := []struct {
		name, path, auth string
		want             int
	}{
		{"tanpa key", "/api/runs", "", http.StatusUnauthorized},
		{"key salah", "/api/runs", "Bearer salah", http.StatusUnauthorized},
		{"key tanpa Bearer", "/api/runs", "rahasia-123", http.StatusUnauthorized},
		{"key dengan awalan benar tapi lebih panjang", "/api/runs", "Bearer rahasia-1234", http.StatusUnauthorized},
		{"key benar", "/api/runs", "Bearer rahasia-123", http.StatusOK},
		{"healthz tanpa key", "/healthz", "", http.StatusOK},
		{"readyz tanpa key", "/readyz", "", http.StatusOK},
	}
	for _, c := range cases {
		if rec := call(c.path, c.auth); rec.Code != c.want {
			t.Errorf("%s: status %d, harusnya %d (%s)", c.name, rec.Code, c.want, rec.Body)
		}
	}
	if rec := call("/api/runs", ""); rec.Header().Get("WWW-Authenticate") == "" || strings.Contains(rec.Body.String(), "rahasia") {
		t.Errorf("401 harus memberi WWW-Authenticate dan tidak membocorkan key: %v %s", rec.Header(), rec.Body)
	}
}

func TestAPIKey_EmptyMeansOpen(t *testing.T) {
	if rec := do(newServer(&fakeStore{}, 0), "GET", "/api/runs", nil); rec.Code != http.StatusOK {
		t.Fatalf("tanpa APIKey server harus terbuka, dapat %d", rec.Code)
	}
}

func TestAPIKey_SessionAllowsReadsOnly(t *testing.T) {
	session := func(r *http.Request) bool { return r.Header.Get("X-Session") == "1" }
	csrf := func(r *http.Request) bool { return r.Header.Get("X-CSRF-Token") == "token" }
	h := (&Server{Store: &fakeStore{}, Analyzer: &fakeAnalyzer{},
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), APIKey: "secret",
		Session: session, CSRF: csrf}).Routes()

	call := func(method string, body string, header string) int {
		req := httptest.NewRequest(method, "/api/groups/ada/label", strings.NewReader(body))
		req.Header.Set("X-Session", "1")
		if header != "" {
			req.Header.Set("X-CSRF-Token", header)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	getReq := httptest.NewRequest(http.MethodGet, "/api/runs", nil)
	getReq.Header.Set("X-Session", "1")
	getRec := httptest.NewRecorder()
	h.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Errorf("GET with session = %d, want 200", getRec.Code)
	}
	body := `{"label":"test_bug","by":"ui"}`
	if got := call(http.MethodPut, body, ""); got != http.StatusForbidden {
		t.Errorf("PUT without CSRF = %d, want 403", got)
	}
	if got := call(http.MethodPut, body, "token"); got != http.StatusOK {
		t.Errorf("PUT with CSRF = %d, want 200", got)
	}
}

func TestAPIKey_UserToken(t *testing.T) {
	validate := func(token string) bool { return token == "user-key" }
	h := (&Server{Store: &fakeStore{}, Analyzer: &fakeAnalyzer{},
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), AuthEnabled: true, ValidateKey: validate}).Routes()

	if rec := do(h, http.MethodGet, "/api/runs", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("no token = %d, want 401", rec.Code)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/runs", nil)
	req.Header.Set("Authorization", "Bearer user-key")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("user token = %d, want 200", rec.Code)
	}
}

func TestTriage(t *testing.T) {
	fs := &fakeStore{}
	h := newServer(fs, 0)

	req := httptest.NewRequest(http.MethodPut, "/api/groups/ada/triage",
		strings.NewReader(`{"status":"acknowledged","owner":"ega","note":"check logs","by":"ega"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	if fs.label != "acknowledged|ega|check logs|ega" {
		t.Errorf("triage = %q", fs.label)
	}

	req = httptest.NewRequest(http.MethodPut, "/api/groups/ada/triage",
		strings.NewReader(`{"status":"not-valid","by":"ega"}`))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid status = %d, want 400", rec.Code)
	}

	if rec := do(h, http.MethodPut, "/api/groups/missing/triage",
		strings.NewReader(`{"status":"open","by":"ega"}`)); rec.Code != http.StatusNotFound {
		t.Errorf("missing group = %d, want 404", rec.Code)
	}

	rec = do(h, http.MethodPut, "/api/groups/triage",
		strings.NewReader(`{"fingerprints":["a","b","a"],"status":"acknowledged","by":"ega"}`))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"updated": 2`) {
		t.Errorf("bulk triage = %d: %s", rec.Code, rec.Body)
	}
	rec = do(h, http.MethodPut, "/api/groups/triage/owner",
		strings.NewReader(`{"fingerprints":["a","b"],"owner":"budi","by":"ega"}`))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"updated": 2`) {
		t.Errorf("bulk owner = %d: %s", rec.Code, rec.Body)
	}
	if rec := do(h, http.MethodPut, "/api/groups/triage",
		strings.NewReader(`{"fingerprints":[],"status":"open","by":"ega"}`)); rec.Code != http.StatusBadRequest {
		t.Errorf("bulk tanpa fingerprint = %d, want 400", rec.Code)
	}
	fs.bulkConfirmed = 2
	rec = do(h, http.MethodPost, "/api/groups/accept",
		strings.NewReader(`{"fingerprints":["a","b"],"by":"ega"}`))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"confirmed": 2`) {
		t.Errorf("bulk accept = %d: %s", rec.Code, rec.Body)
	}
	if rec := do(h, http.MethodPost, "/api/groups/accept",
		strings.NewReader(`{"fingerprints":[],"by":"ega"}`)); rec.Code != http.StatusBadRequest {
		t.Errorf("bulk accept tanpa fingerprint = %d, want 400", rec.Code)
	}
}

func TestPromoteShadowRule(t *testing.T) {
	fs := &fakeStore{
		rules: []store.LearnedRule{{ID: 3, Status: "shadow", Category: "test_bug"}},
		shadowStats: []store.ShadowStat{{RuleID: 3, Status: "shadow", Matches: 10,
			Confirmed: 9, Overridden: 1, AgreementRate: 0.9, Ready: true}},
	}
	h := (&Server{Store: fs, Analyzer: &fakeAnalyzer{},
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))}).Routes()
	rec := do(h, http.MethodPost, "/api/rules/3/promote-shadow", strings.NewReader(`{"by":"ega"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("promote shadow = %d: %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `"status": "proposed"`) {
		t.Errorf("status = %s", rec.Body)
	}

	fs.shadowStats[0].Ready = false
	rec = do(h, http.MethodPost, "/api/rules/3/promote-shadow", strings.NewReader(`{"by":"ega"}`))
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "not ready") {
		t.Errorf("not ready = %d: %s", rec.Code, rec.Body)
	}
}

func TestBulkAcceptPromotesCandidates(t *testing.T) {
	an := &fakeAnalyzer{}
	h := (&Server{Store: &fakeStore{bulkConfirmed: 2}, Analyzer: an,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))}).Routes()
	rec := do(h, http.MethodPost, "/api/groups/accept",
		strings.NewReader(`{"fingerprints":["a","b"],"by":"ega"}`))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"confirmed": 2`) {
		t.Fatalf("bulk accept = %d: %s", rec.Code, rec.Body)
	}
	if len(an.promoted) != 2 || an.promoted[0] != "a" || an.promoted[1] != "b" {
		t.Errorf("candidate promotion after bulk accept = %v", an.promoted)
	}
}

func TestLabelUsesSessionUser(t *testing.T) {
	fs := &fakeStore{}
	session := func(*http.Request) bool { return true }
	csrf := func(*http.Request) bool { return true }
	user := func(*http.Request) string { return "ega" }
	h := (&Server{Store: fs, Analyzer: &fakeAnalyzer{},
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), APIKey: "secret",
		Session: session, CSRF: csrf, User: user}).Routes()

	req := httptest.NewRequest(http.MethodPut, "/api/groups/ada/label",
		strings.NewReader(`{"label":"test_bug","note":"n","by":"spoofed"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	if fs.label != "test_bug|n|ega" {
		t.Errorf("label = %q, want the session user", fs.label)
	}
}

func TestRuleDecisionUsesSessionUser(t *testing.T) {
	session := func(*http.Request) bool { return true }
	csrf := func(*http.Request) bool { return true }
	user := func(*http.Request) string { return "ega" }
	h := (&Server{Store: &fakeStore{}, Analyzer: &fakeAnalyzer{},
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), APIKey: "secret",
		Session: session, CSRF: csrf, User: user}).Routes()

	req := httptest.NewRequest(http.MethodPut, "/api/rules/1",
		strings.NewReader(`{"status":"active","by":"spoofed"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `"decided_by": "ega"`) {
		t.Errorf("rule decision did not use the session user: %s", rec.Body)
	}
}

func TestGetRun(t *testing.T) {
	h := newServer(&fakeStore{}, 0)

	rec := do(h, http.MethodGet, "/api/runs/1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "TC-USR-001") || !strings.Contains(rec.Body.String(), `"failed"`) {
		t.Errorf("response should include tests: %s", rec.Body)
	}

	if rec := do(h, http.MethodGet, "/api/runs/2", nil); rec.Code != http.StatusNotFound {
		t.Errorf("unknown run = %d, want 404", rec.Code)
	}
	if rec := do(h, http.MethodGet, "/api/runs/abc", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("invalid id = %d, want 400", rec.Code)
	}
}

func TestCompareRun(t *testing.T) {
	h := newServer(&fakeStore{}, 0)

	rec := do(h, http.MethodGet, "/api/runs/1/compare", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	for _, want := range []string{`"previous":`, `"id": 0`, `"still_failing"`, `"fixed"`} {
		if !strings.Contains(body, want) {
			t.Errorf("compare response harus memuat %s: %s", want, body)
		}
	}
	if rec := do(h, http.MethodGet, "/api/runs/2/compare", nil); rec.Code != http.StatusNotFound {
		t.Errorf("unknown run = %d, want 404", rec.Code)
	}
}

func TestAnalyzeLocal(t *testing.T) {
	h := newServer(&fakeStore{}, 0)

	rec := do(h, http.MethodPost, "/api/groups/abc/analyze?ai=0", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `"needs_ai": true`) || !strings.Contains(rec.Body.String(), `"source": "none"`) {
		t.Errorf("analisis lokal tanpa rule harus needs_ai: %s", rec.Body)
	}
}

func TestRuleCandidates(t *testing.T) {
	h := newServer(&fakeStore{}, 0)

	if rec := do(h, http.MethodGet, "/api/rules/candidates", nil); rec.Code != http.StatusOK {
		t.Fatalf("list candidate = %d: %s", rec.Code, rec.Body)
	}
	if rec := do(h, http.MethodGet, "/api/rules/candidates?status=bad", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("status candidate tidak valid = %d, want 400", rec.Code)
	}
	rec := do(h, http.MethodPost, "/api/rules/candidates/promote", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"candidates"`) {
		t.Errorf("promote candidate = %d: %s", rec.Code, rec.Body)
	}
}

func TestArtifactUpload(t *testing.T) {
	dir := t.TempDir()
	fs := &fakeStore{}
	h := (&Server{Store: fs, Analyzer: &fakeAnalyzer{},
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), ArtifactDir: dir, MaxArtifactSize: 1 << 20}).Routes()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("test", "toolshop › api.spec.ts › register")
	_ = mw.WriteField("kind", "trace")
	fw, _ := mw.CreatePart(textproto.MIMEHeader{
		"Content-Disposition": {`form-data; name="file"; filename="trace.zip"`},
		"Content-Type":        {"application/zip"},
	})
	_, _ = fw.Write([]byte("zip-content"))
	_ = mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/runs/1/artifacts", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), "trace.zip") {
		t.Fatalf("upload artifact = %d: %s", rec.Code, rec.Body)
	}
	detail := do(h, http.MethodGet, "/api/runs/1", nil)
	if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), `"artifacts": [`) ||
		!strings.Contains(detail.Body.String(), `"name": "trace.zip"`) {
		t.Errorf("run detail tanpa artifact: %d %s", detail.Code, detail.Body)
	}
	download := do(h, http.MethodGet, "/api/artifacts/1", nil)
	if download.Code != http.StatusOK || download.Body.String() != "zip-content" ||
		download.Header().Get("Content-Type") != "application/zip" ||
		!strings.Contains(download.Header().Get("Content-Disposition"), "attachment") {
		t.Errorf("download artifact = %d %q %v", download.Code, download.Body, download.Header())
	}
	fs.artifacts[0].Path = "../outside.txt" // server hanya boleh menyajikan file di dalam ARTIFACT_DIR
	if rec := do(h, http.MethodGet, "/api/artifacts/1", nil); rec.Code != http.StatusNotFound {
		t.Errorf("artifact di luar ARTIFACT_DIR = %d, want 404", rec.Code)
	}
	if rec := do(h, http.MethodGet, "/api/artifacts/99", nil); rec.Code != http.StatusNotFound {
		t.Errorf("artifact tidak ada = %d, want 404", rec.Code)
	}
	listed := do(h, http.MethodGet, "/api/runs/1/artifacts", nil)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"trace.zip"`) {
		t.Errorf("daftar artifact = %d %s", listed.Code, listed.Body)
	}
	if rec := do(h, http.MethodGet, "/api/runs/2/artifacts", nil); rec.Code != http.StatusNotFound {
		t.Errorf("run tidak ada = %d, want 404", rec.Code)
	}
	if rec := do(h, http.MethodPost, "/api/runs/1/artifacts", nil); rec.Code == http.StatusCreated {
		t.Errorf("upload tanpa multipart tidak boleh sukses")
	}
}

func TestTestHealth(t *testing.T) {
	fs := &fakeStore{}
	h := newServer(fs, 0)

	if rec := do(h, http.MethodGet, "/api/tests?days=7", nil); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), `"TC-BRD-001"`) ||
		!strings.Contains(rec.Body.String(), `"pass_rate": 0.5`) {
		t.Errorf("test health = %d: %s", rec.Code, rec.Body)
	}
	if rec := do(h, http.MethodGet, "/api/tests?days=nope", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("days tidak valid = %d, want 400", rec.Code)
	}
	if rec := do(h, http.MethodGet, "/api/tests/history?test=TC-BRD-001", nil); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), `"run_id": 1`) {
		t.Errorf("test history = %d: %s", rec.Code, rec.Body)
	}
	if rec := do(h, http.MethodGet, "/api/tests/history", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("history tanpa test = %d, want 400", rec.Code)
	}
	if rec := do(h, http.MethodGet, "/api/tests/history?test=TC-NONE-999", nil); rec.Code != http.StatusNotFound {
		t.Errorf("history test tidak ada = %d, want 404", rec.Code)
	}
}

func TestQuarantine(t *testing.T) {
	fs := &fakeStore{}
	h := newServer(fs, 0)

	if rec := do(h, http.MethodGet, "/api/tests/quarantine", nil); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), `"TC-BRD-001"`) {
		t.Errorf("list quarantine = %d: %s", rec.Code, rec.Body)
	}
	if rec := do(h, http.MethodGet, "/api/tests/quarantine?project=toolshop", nil); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), `"TC-BRD-001"`) {
		t.Errorf("list quarantine per project = %d: %s", rec.Code, rec.Body)
	}
	if rec := do(h, http.MethodGet, "/api/tests/quarantine?project=other", nil); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), "[]") {
		t.Errorf("list quarantine project lain = %d: %s", rec.Code, rec.Body)
	}
	body := `{"test":"TC-BRD-001","owner":"ega","reason":"flaky login","days":7}`
	if rec := do(h, http.MethodPut, "/api/tests/quarantine", strings.NewReader(body)); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), `"owner": "ega"`) {
		t.Errorf("set quarantine = %d: %s", rec.Code, rec.Body)
	}
	if rec := do(h, http.MethodPut, "/api/tests/quarantine", strings.NewReader(`{"test":"TC-BRD-001","days":7}`)); rec.Code != http.StatusBadRequest {
		t.Errorf("quarantine tanpa owner = %d, want 400", rec.Code)
	}
	if rec := do(h, http.MethodPut, "/api/tests/quarantine", strings.NewReader(`{"test":"TC-NONE","owner":"ega","reason":"x"}`)); rec.Code != http.StatusNotFound {
		t.Errorf("quarantine test tidak ada = %d, want 404", rec.Code)
	}
	if rec := do(h, http.MethodDelete, "/api/tests/quarantine?test=TC-BRD-001", nil); rec.Code != http.StatusOK {
		t.Errorf("release quarantine = %d: %s", rec.Code, rec.Body)
	}
	if rec := do(h, http.MethodDelete, "/api/tests/quarantine?test=TC-BRD-001", nil); rec.Code != http.StatusNotFound {
		t.Errorf("release tanpa quarantine aktif = %d, want 404", rec.Code)
	}
}

func TestCoverage(t *testing.T) {
	fs := &fakeStore{}
	h := newServer(fs, 0)

	rec := do(h, http.MethodGet, "/api/coverage?project=toolshop", nil)
	if rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), `"documented": 2`) ||
		!strings.Contains(rec.Body.String(), `"covered": 2`) ||
		!strings.Contains(rec.Body.String(), `"uncovered": 0`) ||
		!strings.Contains(rec.Body.String(), `"status_codes_seen": 3`) ||
		!strings.Contains(rec.Body.String(), `"path": "/products"`) ||
		!strings.Contains(rec.Body.String(), `"test_keys"`) ||
		!strings.Contains(rec.Body.String(), `toolshop › api.spec.ts › auth`) {
		t.Errorf("coverage = %d: %s", rec.Code, rec.Body)
	}
	var history struct {
		History []struct {
			RunID   int64 `json:"run_id"`
			Covered int   `json:"covered"`
			Changes int   `json:"changes"`
		} `json:"history"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &history); err != nil {
		t.Fatalf("history bukan JSON: %v", err)
	}
	if len(history.History) != 2 ||
		history.History[0].RunID != 1 || history.History[0].Covered != 1 || history.History[0].Changes != 0 ||
		history.History[1].RunID != 2 || history.History[1].Covered != 2 || history.History[1].Changes != 4 {
		t.Errorf("history salah: %+v", history.History)
	}

	if rec := do(h, http.MethodGet, "/api/coverage", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("coverage tanpa project = %d, want 400", rec.Code)
	}
	if rec := do(h, http.MethodGet, "/api/coverage?project=other", nil); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), `"available": false`) {
		t.Errorf("coverage project lain = %d: %s", rec.Code, rec.Body)
	}
}

func TestReadyz(t *testing.T) {
	cases := []struct {
		name  string
		ready func(context.Context) error
		want  int
	}{
		{"database siap", func(context.Context) error { return nil }, http.StatusOK},
		{"database mati", func(context.Context) error { return errors.New("koneksi ditolak") }, http.StatusServiceUnavailable},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := (&Server{Store: &fakeStore{}, Analyzer: &fakeAnalyzer{}, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Ready: c.ready}).Routes()
			rec := do(h, "GET", "/readyz", nil)
			if rec.Code != c.want {
				t.Fatalf("status %d, harusnya %d (%s)", rec.Code, c.want, rec.Body)
			}
			if c.want == http.StatusServiceUnavailable && strings.Contains(rec.Body.String(), "koneksi ditolak") {
				t.Errorf("error database tidak boleh bocor ke body: %s", rec.Body)
			}
		})
	}
}

func TestReadyz_NotConfigured(t *testing.T) {
	h := (&Server{Store: &fakeStore{}, Analyzer: &fakeAnalyzer{}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}).Routes()
	if rec := do(h, "GET", "/readyz", nil); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("tanpa Ready, /readyz harus 503, dapat %d", rec.Code)
	}
}

func TestVersionHeaders(t *testing.T) {
	h := (&Server{Store: &fakeStore{}, Analyzer: &fakeAnalyzer{}, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Version: "9.9.9"}).Routes()
	rec := do(h, "GET", "/healthz", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz status %d", rec.Code)
	}
	if got := rec.Header().Get("X-Redline-Version"); got != "9.9.9" {
		t.Errorf("X-Redline-Version = %q", got)
	}
	if got := rec.Header().Get("X-Redline-Api-Version"); got != strconv.Itoa(APIVersion) {
		t.Errorf("X-Redline-Api-Version = %q", got)
	}
	var body struct {
		Status  string `json:"status"`
		Version string `json:"version"`
		API     int    `json:"api"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body healthz bukan JSON: %v", err)
	}
	if body.Status != "ok" || body.Version != "9.9.9" || body.API != APIVersion {
		t.Errorf("body healthz = %+v", body)
	}
}

func TestVersionDefault(t *testing.T) {
	h := (&Server{Store: &fakeStore{}, Analyzer: &fakeAnalyzer{}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}).Routes()
	if got := do(h, "GET", "/healthz", nil).Header().Get("X-Redline-Version"); got != "dev" {
		t.Errorf("versi default = %q, harusnya dev", got)
	}
}

func configServer(fs *fakeStore) (*Server, http.Handler) {
	srv := &Server{
		Store: fs, Analyzer: &fakeAnalyzer{}, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Contracts: contract.NewRegistry(nil),
		SourceCheck: func(_ context.Context, raw string) contract.SourceCheck {
			switch {
			case strings.Contains(raw, "documentation"):
				return contract.SourceCheck{OK: false, URL: raw, Kind: "html",
					Error: "URL mengembalikan halaman HTML (Swagger UI), bukan dokumen JSON"}
			case strings.Contains(raw, "broken"):
				return contract.SourceCheck{OK: false, URL: raw, Kind: "status", Error: "URL membalas HTTP 404"}
			default:
				return contract.SourceCheck{OK: true, URL: raw, Version: "OpenAPI 3.2.0",
					Title: "Toolshop API", Endpoints: 19}
			}
		},
	}
	return srv, srv.Routes()
}

func TestConfigListsSources(t *testing.T) {
	fs := &fakeStore{sources: []store.ContractSource{{
		Project: "toolshop", URL: "https://db.example/docs", Enabled: true,
		LastStatus: "valid", LastVersion: "OpenAPI 3.2.0", LastEndpoints: 19,
	}}}
	_, h := configServer(fs)

	rec := do(h, http.MethodGet, "/api/config", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("config = %d: %s", rec.Code, rec.Body)
	}
	for _, want := range []string{
		`"project": "toolshop"`, `"last_endpoints": 19`, `"delivery": true`, `"provider": "discord"`, `"available_events"`,
	} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("config tidak memuat %s: %s", want, rec.Body)
		}
	}
	if strings.Contains(rec.Body.String(), `"origin"`) {
		t.Errorf("config masih memakai konsep origin: %s", rec.Body)
	}
}

func TestConfigEmptyOnFreshInstall(t *testing.T) {
	_, h := configServer(&fakeStore{})
	rec := do(h, http.MethodGet, "/api/config", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"contracts": []`) {
		t.Fatalf("fresh install = %d: %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `"enabled": false`) ||
		!strings.Contains(rec.Body.String(), `"delivery": true`) {
		t.Errorf("notifikasi default salah: %s", rec.Body)
	}
}

func TestSaveContractSourceRejectsHTMLAndAppliesValid(t *testing.T) {
	fs := &fakeStore{}
	srv, h := configServer(fs)

	rec := do(h, http.MethodPut, "/api/config/contracts",
		strings.NewReader(`{"project":"toolshop","url":"https://api.example.com/api/documentation"}`))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "HTML") {
		t.Fatalf("URL HTML = %d: %s", rec.Code, rec.Body)
	}
	if len(fs.sources) != 0 {
		t.Errorf("source invalid tidak boleh disimpan: %+v", fs.sources)
	}

	rec = do(h, http.MethodPut, "/api/config/contracts",
		strings.NewReader(`{"project":"toolshop","url":"https://api.example.com/docs"}`))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"last_endpoints": 19`) {
		t.Fatalf("save valid = %d: %s", rec.Code, rec.Body)
	}
	if url, _ := srv.Contracts.URL("toolshop"); url != "https://api.example.com/docs" {
		t.Errorf("registry tidak diperbarui: %q", url)
	}
	if len(fs.sources) != 1 || fs.sources[0].LastStatus != "valid" {
		t.Errorf("source tidak tersimpan dengan status valid: %+v", fs.sources)
	}

	rec = do(h, http.MethodPut, "/api/config/contracts",
		strings.NewReader(`{"project":"bad project","url":"https://api.example.com/docs"}`))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("project invalid = %d, want 400", rec.Code)
	}
}

func TestDeleteContractSourceRemovesFromRegistry(t *testing.T) {
	fs := &fakeStore{sources: []store.ContractSource{{Project: "legacy", URL: "https://db.example/docs", Enabled: true}}}
	srv, h := configServer(fs)
	srv.Contracts.Set("legacy", "https://db.example/docs")

	rec := do(h, http.MethodDelete, "/api/config/contracts?project=legacy", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete = %d: %s", rec.Code, rec.Body)
	}
	if url, ok := srv.Contracts.URL("legacy"); ok {
		t.Errorf("source masih aktif setelah delete: %q", url)
	}
	if rec := do(h, http.MethodDelete, "/api/config/contracts?project=legacy", nil); rec.Code != http.StatusNotFound {
		t.Errorf("delete kedua = %d, want 404", rec.Code)
	}
}

func TestContractCheckPersistsResult(t *testing.T) {
	fs := &fakeStore{sources: []store.ContractSource{{Project: "toolshop", URL: "https://api.example.com/docs", Enabled: true}}}
	srv, h := configServer(fs)
	srv.Contracts.Set("toolshop", "https://api.example.com/docs")

	rec := do(h, http.MethodPost, "/api/config/contracts/check", strings.NewReader(`{"project":"toolshop"}`))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ok": true`) {
		t.Fatalf("check = %d: %s", rec.Code, rec.Body)
	}
	if fs.sources[0].LastStatus != "valid" || fs.sources[0].LastEndpoints != 19 {
		t.Errorf("hasil check tidak disimpan: %+v", fs.sources[0])
	}
	if rec := do(h, http.MethodPost, "/api/config/contracts/check", strings.NewReader(`{"project":"missing"}`)); rec.Code != http.StatusNotFound {
		t.Errorf("check project tanpa source = %d, want 404", rec.Code)
	}
}

func TestNotificationsMaskWebhookAndRejectInvalid(t *testing.T) {
	fs := &fakeStore{}
	srv, h := configServer(fs)
	notifier := &fakeNotifier{}
	srv.Notifier = notifier

	rec := do(h, http.MethodPut, "/api/config/notifications", strings.NewReader(
		`{"enabled":true,"webhook_url":"https://hooks.slack.com/services/T000/B000/SECRET","events":["new_failure"]}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("save notifications = %d: %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "SECRET") || !strings.Contains(rec.Body.String(), "••••") {
		t.Errorf("webhook tidak disamarkan: %s", rec.Body)
	}
	if fs.notifications.WebhookURL != "https://hooks.slack.com/services/T000/B000/SECRET" {
		t.Errorf("webhook asli tidak tersimpan: %q", fs.notifications.WebhookURL)
	}

	// The UI sends the masked value back when the operator edits another field; keep the stored secret.
	rec = do(h, http.MethodPut, "/api/config/notifications", strings.NewReader(
		`{"enabled":true,"webhook_url":"https://hooks.slack.com/services/••••/••••","events":["new_failure","regressed"]}`))
	if rec.Code != http.StatusOK || fs.notifications.WebhookURL != "https://hooks.slack.com/services/T000/B000/SECRET" {
		t.Errorf("masked round-trip menghapus secret: %d %q", rec.Code, fs.notifications.WebhookURL)
	}

	if rec := do(h, http.MethodPut, "/api/config/notifications", strings.NewReader(
		`{"enabled":true,"webhook_url":"","events":["new_failure"]}`)); rec.Code != http.StatusBadRequest {
		t.Errorf("enabled tanpa webhook = %d, want 400", rec.Code)
	}
	if rec := do(h, http.MethodPut, "/api/config/notifications", strings.NewReader(
		`{"enabled":false,"webhook_url":"","events":["bogus"]}`)); rec.Code != http.StatusBadRequest {
		t.Errorf("event tidak dikenal = %d, want 400", rec.Code)
	}

	rec = do(h, http.MethodPut, "/api/config/notifications", strings.NewReader(
		`{"enabled":true,"webhook_url":"https://hooks.example.com/hook?token=QUERYSECRET","events":["new_failure"]}`))
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "QUERYSECRET") ||
		!strings.Contains(rec.Body.String(), "?••••") {
		t.Errorf("query webhook tidak disamarkan: %d %s", rec.Code, rec.Body)
	}

	rec = do(h, http.MethodPost, "/api/config/notifications/test", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"sent": true`) ||
		!strings.Contains(rec.Body.String(), `"mock": false`) ||
		notifier.message.Title != "Redline test notification" {
		t.Errorf("test notification = %d: %s", rec.Code, rec.Body)
	}
	if len(fs.deliveries) != 1 || !fs.deliveries[0].OK || fs.deliveries[0].Event != "test" {
		t.Errorf("delivery test tidak tercatat: %+v", fs.deliveries)
	}
	rec = do(h, http.MethodGet, "/api/config", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"last_delivery"`) ||
		!strings.Contains(rec.Body.String(), `"event": "test"`) {
		t.Errorf("config tanpa last_delivery: %s", rec.Body)
	}
}

func TestTestNotificationRetriesRateLimit(t *testing.T) {
	fs := &fakeStore{notifications: store.NotificationSettings{
		Enabled: true, WebhookURL: "https://discord.test/webhook", Events: []string{"new_failure"},
	}}
	notifier := &fakeNotifier{responses: []error{
		&notify.DeliveryError{StatusCode: 429, RetryAfter: 20 * time.Millisecond, Message: "rate limited"},
		nil,
	}}
	srv := &Server{
		Store: fs, Analyzer: &fakeAnalyzer{}, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Notifier: notifier,
	}
	rec := do(srv.Routes(), http.MethodPost, "/api/config/notifications/test", nil)
	if rec.Code != http.StatusOK || notifier.calls != 2 {
		t.Fatalf("retry = %d calls=%d: %s", rec.Code, notifier.calls, rec.Body)
	}
	if len(fs.deliveries) != 1 || !fs.deliveries[0].OK || fs.deliveries[0].Attempts != 2 {
		t.Errorf("delivery retry tidak tercatat: %+v", fs.deliveries)
	}
}

func TestTestNotificationFailure(t *testing.T) {
	fs := &fakeStore{notifications: store.NotificationSettings{
		WebhookURL: "https://discord.test/webhook", Events: []string{"new_failure"},
	}}
	srv := &Server{
		Store: fs, Analyzer: &fakeAnalyzer{}, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Notifier: &fakeNotifier{err: &notify.DeliveryError{StatusCode: http.StatusForbidden, Message: "forbidden"}},
	}
	rec := do(srv.Routes(), http.MethodPost, "/api/config/notifications/test", nil)
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), `"sent": false`) {
		t.Errorf("test notification gagal = %d: %s", rec.Code, rec.Body)
	}
}

func TestNotificationChannels(t *testing.T) {
	fs := &fakeStore{}
	srv, h := configServer(fs)
	notifier := &fakeNotifier{}
	srv.Notifier = notifier

	rec := do(h, http.MethodGet, "/api/config", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"notification_channels": []`) {
		t.Errorf("config tanpa channel = %d: %s", rec.Code, rec.Body)
	}

	rec = do(h, http.MethodPut, "/api/config/notifications/projects/toolshop", strings.NewReader(
		`{"enabled":true,"webhook_url":"https://discord.test/toolshop","events":["new_failure"]}`))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"project": "toolshop"`) ||
		!strings.Contains(rec.Body.String(), `"webhook_set": true`) {
		t.Fatalf("save project channel = %d: %s", rec.Code, rec.Body)
	}
	if fs.channels["toolshop"].WebhookURL != "https://discord.test/toolshop" {
		t.Errorf("channel tidak tersimpan: %+v", fs.channels)
	}
	rec = do(h, http.MethodPut, "/api/config/notifications/projects/toolshop", strings.NewReader(
		`{"enabled":true,"webhook_url":"https://discord.test/••••","events":["new_failure","regressed"]}`))
	if rec.Code != http.StatusOK || fs.channels["toolshop"].WebhookURL != "https://discord.test/toolshop" {
		t.Errorf("masked round-trip menghapus webhook: %d %+v", rec.Code, fs.channels)
	}

	rec = do(h, http.MethodGet, "/api/config", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"project": "toolshop"`) {
		t.Errorf("config tanpa channel project = %d: %s", rec.Code, rec.Body)
	}

	rec = do(h, http.MethodPost, "/api/config/notifications/test?project=toolshop", nil)
	if rec.Code != http.StatusOK || notifier.webhook != "https://discord.test/toolshop" {
		t.Errorf("test channel project = %d webhook=%q: %s", rec.Code, notifier.webhook, rec.Body)
	}
	if len(fs.deliveries) != 1 || fs.deliveries[0].Project != "toolshop" {
		t.Errorf("delivery project tidak tercatat: %+v", fs.deliveries)
	}

	if rec := do(h, http.MethodPut, "/api/config/notifications/projects/bad%20project!", strings.NewReader(
		`{"enabled":true,"webhook_url":"https://discord.test/x","events":["new_failure"]}`)); rec.Code != http.StatusBadRequest {
		t.Errorf("project invalid = %d, want 400", rec.Code)
	}
	if rec := do(h, http.MethodPut, "/api/config/notifications/projects/toolshop", strings.NewReader(
		`{"enabled":true,"webhook_url":"","events":["new_failure"]}`)); rec.Code != http.StatusBadRequest {
		t.Errorf("project channel tanpa webhook = %d, want 400", rec.Code)
	}

	if rec := do(h, http.MethodDelete, "/api/config/notifications/projects/toolshop", nil); rec.Code != http.StatusOK {
		t.Errorf("delete project channel = %d: %s", rec.Code, rec.Body)
	}
	if _, ok := fs.channels["toolshop"]; ok {
		t.Error("project channel masih ada setelah delete")
	}
	if rec := do(h, http.MethodDelete, "/api/config/notifications/projects/toolshop", nil); rec.Code != http.StatusNotFound {
		t.Errorf("delete channel kedua = %d, want 404", rec.Code)
	}
}

func TestIngestUsesProjectNotificationChannel(t *testing.T) {
	fs := &fakeStore{
		ingest: &store.IngestResult{
			RunID: 8, Total: 2, Failed: 2,
			New: []store.GroupChange{
				{Fingerprint: "fp-a", TestKey: "toolshop › a.spec.ts › one", Project: "toolshop", Error: "boom"},
				{Fingerprint: "fp-b", TestKey: "web › b.spec.ts › two", Project: "web", Error: "boom"},
			},
		},
		notifications: store.NotificationSettings{
			Enabled: true, WebhookURL: "https://discord.test/default",
			Events: []string{"new_failure"},
		},
		channels: map[string]store.NotificationChannel{
			"toolshop": {Project: "toolshop", Enabled: true, WebhookURL: "https://discord.test/toolshop",
				Events: []string{"new_failure"}},
			"web": {Project: "web", Enabled: false, WebhookURL: "https://discord.test/web",
				Events: []string{"new_failure"}},
		},
	}
	notifier := &fakeNotifier{ch: make(chan struct{}, 2)}
	srv := &Server{
		Store: fs, Analyzer: &fakeAnalyzer{}, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Notifier: notifier,
	}
	f, _ := os.Open("../report/testdata/run1-bug.json")
	defer f.Close()
	rec := do(srv.Routes(), http.MethodPost, "/api/runs?source=ci&branch=main", f)
	if rec.Code != http.StatusCreated {
		t.Fatalf("ingest = %d: %s", rec.Code, rec.Body)
	}
	select {
	case <-notifier.ch:
	case <-time.After(2 * time.Second):
		t.Fatal("notifier tidak dipanggil")
	}
	if notifier.calls != 1 || notifier.webhook != "https://discord.test/toolshop" {
		t.Errorf("routing channel salah: calls=%d webhook=%q", notifier.calls, notifier.webhook)
	}
	if !strings.Contains(notifier.message.Description, "fp-a") && !strings.Contains(notifier.message.Description, "one") {
		t.Errorf("message project toolshop salah: %+v", notifier.message)
	}
	if len(fs.deliveries) != 1 || fs.deliveries[0].Project != "toolshop" {
		t.Errorf("delivery project salah: %+v", fs.deliveries)
	}
}

func TestIngestSendsDiscordNotification(t *testing.T) {
	fs := &fakeStore{
		ingest: &store.IngestResult{
			RunID: 7, Total: 6, Failed: 1,
			New: []store.GroupChange{{
				Fingerprint: "fp", TestKey: "toolshop › brand.spec.ts › create",
				TestCaseID: "TC-BRD-002", Error: "boom",
			}},
		},
		notifications: store.NotificationSettings{
			Enabled: true, WebhookURL: "https://discord.test/webhook",
			Events: []string{"new_failure", "regressed"},
		},
	}
	notifier := &fakeNotifier{ch: make(chan struct{}, 1)}
	srv := &Server{
		Store: fs, Analyzer: &fakeAnalyzer{}, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Notifier: notifier,
	}
	f, _ := os.Open("../report/testdata/run1-bug.json")
	defer f.Close()
	rec := do(srv.Routes(), http.MethodPost, "/api/runs?source=ci&branch=main&commit=abcdef123", f)
	if rec.Code != http.StatusCreated {
		t.Fatalf("ingest = %d: %s", rec.Code, rec.Body)
	}
	select {
	case <-notifier.ch:
	case <-time.After(2 * time.Second):
		t.Fatal("notifier tidak dipanggil")
	}
	if notifier.webhook != "https://discord.test/webhook" {
		t.Errorf("webhook = %q", notifier.webhook)
	}
	if !strings.Contains(notifier.message.Title, "1 new") ||
		!strings.Contains(notifier.message.Title, "run #7") ||
		!strings.Contains(notifier.message.Description, "TC-BRD-002") {
		t.Errorf("message salah: %+v", notifier.message)
	}
	deadline := time.Now().Add(time.Second)
	for len(fs.deliveries) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if len(fs.deliveries) != 1 || !fs.deliveries[0].OK || fs.deliveries[0].RunID != 7 ||
		fs.deliveries[0].Event != "new_failure" {
		t.Errorf("delivery ingest tidak tercatat: %+v", fs.deliveries)
	}
}

func TestCoverageEmptyMessages(t *testing.T) {
	fs := &fakeStore{}
	h := newServer(fs, 0)

	rec := do(h, http.MethodGet, "/api/coverage?project=other", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Add a contract source in Config") {
		t.Errorf("tanpa source = %d: %s", rec.Code, rec.Body)
	}

	fs.sources = []store.ContractSource{{Project: "other", URL: "https://api.example.com/docs", Enabled: true}}
	rec = do(h, http.MethodGet, "/api/coverage?project=other", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Contract source is configured") {
		t.Errorf("dengan source = %d: %s", rec.Code, rec.Body)
	}
}

func ptrTime(t time.Time) *time.Time { return &t }

func TestQuarantineSuggestions(t *testing.T) {
	now := time.Now()
	recent := now.Add(-2 * 24 * time.Hour)
	fs := &fakeStore{healthRows: []store.TestHealthRow{
		{Key: "TC-A", TestKey: "toolshop › a", TestCaseID: "TC-A", Project: "toolshop",
			Passed: 6, Failed: 2, Flaky: 2, PassRate: 0.7, FlakeRate: 0.2, Flips: 3,
			LastFailureAt: &recent, TriageOwner: "ega"},
		{Key: "TC-B", TestKey: "toolshop › b", TestCaseID: "TC-B", Project: "toolshop",
			Passed: 6, Failed: 2, PassRate: 0.75, Flips: 1, LastFailureAt: &recent},
		{Key: "TC-C", TestKey: "toolshop › c", TestCaseID: "TC-C", Project: "toolshop",
			Passed: 6, Failed: 2, Flaky: 2, PassRate: 0.7, FlakeRate: 0.2, Flips: 4,
			Quarantined: true, LastFailureAt: &recent},
		{Key: "TC-D", TestKey: "toolshop › d", TestCaseID: "TC-D", Project: "toolshop",
			Passed: 2, Failed: 1, PassRate: 0.66, Flips: 3, LastFailureAt: &recent},
		{Key: "TC-E", TestKey: "toolshop › e", TestCaseID: "TC-E", Project: "toolshop",
			Passed: 6, Failed: 2, PassRate: 0.75, Flips: 3, LastFailureAt: ptrTime(now.Add(-30 * 24 * time.Hour))},
		{Key: "TC-F", TestKey: "toolshop › f", TestCaseID: "TC-F", Project: "toolshop",
			Passed: 6, Failed: 2, PassRate: 0.75, Flips: 2, LastFailureAt: &recent},
	}}
	h := newServer(fs, 0)

	rec := do(h, http.MethodGet, "/api/tests/quarantine/suggestions?days=30", nil)
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, `"key": "TC-A"`) ||
		!strings.Contains(body, `"score": "high"`) || !strings.Contains(body, `"owner": "ega"`) ||
		!strings.Contains(body, `"suggested_until"`) {
		t.Fatalf("suggestions = %d: %s", rec.Code, body)
	}
	for _, excluded := range []string{`"TC-B"`, `"TC-C"`, `"TC-D"`, `"TC-E"`} {
		if strings.Contains(body, excluded) {
			t.Errorf("suggestion tidak boleh memuat %s: %s", excluded, body)
		}
	}
	if !strings.Contains(body, `"key": "TC-F"`) || !strings.Contains(body, `"score": "medium"`) {
		t.Errorf("TC-F harus muncul dengan skor medium: %s", body)
	}

	rec = do(h, http.MethodPost, "/api/tests/quarantine/suggestions/dismiss",
		strings.NewReader(`{"test":"TC-A","days":7}`))
	if rec.Code != http.StatusOK || fs.dismissed != "TC-A" {
		t.Fatalf("dismiss = %d: %s", rec.Code, rec.Body)
	}
	rec = do(h, http.MethodGet, "/api/tests/quarantine/suggestions", nil)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), `"TC-A"`) {
		t.Errorf("suggestion yang di-dismiss masih muncul: %s", rec.Body)
	}

	if rec := do(h, http.MethodPost, "/api/tests/quarantine/suggestions/dismiss",
		strings.NewReader(`{"days":7}`)); rec.Code != http.StatusBadRequest {
		t.Errorf("dismiss tanpa test = %d, want 400", rec.Code)
	}
	if rec := do(h, http.MethodPost, "/api/tests/quarantine/suggestions/dismiss",
		strings.NewReader(`{"test":"TC-A","days":91}`)); rec.Code != http.StatusBadRequest {
		t.Errorf("dismiss days invalid = %d, want 400", rec.Code)
	}
}

func TestSaveNotificationsRejectsMetadataHost(t *testing.T) {
	_, h := configServer(&fakeStore{})
	rec := do(h, http.MethodPut, "/api/config/notifications", strings.NewReader(
		`{"enabled":true,"webhook_url":"http://169.254.169.254/latest/meta-data","events":["new_failure"]}`))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("metadata host = %d, want 400: %s", rec.Code, rec.Body)
	}
}

func TestIngestSendsQuarantineExpiryNotification(t *testing.T) {
	until := time.Now().Add(12 * time.Hour)
	fs := &fakeStore{
		ingest: &store.IngestResult{RunID: 7, Total: 1, Passed: 1},
		expiring: []store.Quarantine{{
			Key: "TC-A", TestKey: "toolshop › brand.spec.ts › get all", TestCaseID: "TC-A",
			Owner: "ega", Reason: "flaky login", Until: until,
		}},
		notifications: store.NotificationSettings{
			Enabled: true, WebhookURL: "https://discord.test/webhook",
			Events: []string{"quarantine_expiring"},
		},
	}
	notifier := &fakeNotifier{ch: make(chan struct{}, 1)}
	srv := &Server{
		Store: fs, Analyzer: &fakeAnalyzer{}, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Notifier: notifier,
	}
	f, _ := os.Open("../report/testdata/run2-fixed.json")
	defer f.Close()
	rec := do(srv.Routes(), http.MethodPost, "/api/runs?source=ci&branch=main", f)
	if rec.Code != http.StatusCreated {
		t.Fatalf("ingest = %d: %s", rec.Code, rec.Body)
	}
	select {
	case <-notifier.ch:
	case <-time.After(2 * time.Second):
		t.Fatal("expiry notifier tidak dipanggil")
	}
	if !strings.Contains(notifier.message.Title, "1 quarantine expires") ||
		!strings.Contains(notifier.message.Description, "TC-A") ||
		!strings.Contains(notifier.message.Description, "owner ega") {
		t.Errorf("expiry message salah: %+v", notifier.message)
	}
	deadline := time.Now().Add(time.Second)
	for (len(fs.deliveries) == 0 || len(fs.expiryMarked) == 0) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if len(fs.expiryMarked) != 1 || fs.expiryMarked[0] != "TC-A" {
		t.Errorf("expiry tidak ditandai: %+v", fs.expiryMarked)
	}
	if len(fs.deliveries) != 1 || fs.deliveries[0].Event != "quarantine_expiring" ||
		!fs.deliveries[0].OK {
		t.Errorf("delivery expiry tidak tercatat: %+v", fs.deliveries)
	}
}

func TestExpiryNotificationUsesProjectChannel(t *testing.T) {
	fs := &fakeStore{
		ingest: &store.IngestResult{RunID: 7, Total: 1, Passed: 1},
		expiring: []store.Quarantine{
			{Key: "TC-A", TestCaseID: "TC-A", Project: "toolshop", Owner: "ega", Until: time.Now().Add(12 * time.Hour)},
			{Key: "TC-B", TestCaseID: "TC-B", Project: "web", Owner: "ega", Until: time.Now().Add(18 * time.Hour)},
		},
		notifications: store.NotificationSettings{
			Enabled: true, WebhookURL: "https://discord.test/default",
			Events: []string{"quarantine_expiring"},
		},
		channels: map[string]store.NotificationChannel{
			"toolshop": {Project: "toolshop", Enabled: true, WebhookURL: "https://discord.test/toolshop",
				Events: []string{"quarantine_expiring"}},
			"web": {Project: "web", Enabled: false, Events: []string{"quarantine_expiring"}},
		},
	}
	notifier := &fakeNotifier{ch: make(chan struct{}, 2)}
	srv := &Server{
		Store: fs, Analyzer: &fakeAnalyzer{}, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Notifier: notifier,
	}
	f, _ := os.Open("../report/testdata/run2-fixed.json")
	defer f.Close()
	rec := do(srv.Routes(), http.MethodPost, "/api/runs?source=ci", f)
	if rec.Code != http.StatusCreated {
		t.Fatalf("ingest = %d: %s", rec.Code, rec.Body)
	}
	select {
	case <-notifier.ch:
	case <-time.After(2 * time.Second):
		t.Fatal("expiry notifier tidak dipanggil")
	}
	if notifier.calls != 1 || notifier.webhook != "https://discord.test/toolshop" {
		t.Errorf("routing expiry salah: calls=%d webhook=%q", notifier.calls, notifier.webhook)
	}
	deadline := time.Now().Add(time.Second)
	for (len(fs.deliveries) == 0 || len(fs.expiryMarked) == 0) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if len(fs.deliveries) != 1 || fs.deliveries[0].Project != "toolshop" {
		t.Errorf("delivery expiry project salah: %+v", fs.deliveries)
	}
	if len(fs.expiryMarked) != 1 || fs.expiryMarked[0] != "TC-A" {
		t.Errorf("hanya quarantine toolshop yang boleh ditandai: %+v", fs.expiryMarked)
	}
}

func TestQuarantineExpiryFailureIsNotMarked(t *testing.T) {
	fs := &fakeStore{
		ingest: &store.IngestResult{RunID: 7, Total: 1, Passed: 1},
		expiring: []store.Quarantine{{
			Key: "TC-A", TestCaseID: "TC-A", Owner: "ega", Reason: "flaky",
			Until: time.Now().Add(12 * time.Hour),
		}},
		notifications: store.NotificationSettings{
			Enabled: true, WebhookURL: "https://discord.test/webhook",
			Events: []string{"quarantine_expiring"},
		},
	}
	notifier := &fakeNotifier{
		err: &notify.DeliveryError{StatusCode: http.StatusForbidden, Message: "forbidden"},
		ch:  make(chan struct{}, 1),
	}
	srv := &Server{
		Store: fs, Analyzer: &fakeAnalyzer{}, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Notifier: notifier,
	}
	f, _ := os.Open("../report/testdata/run2-fixed.json")
	defer f.Close()
	do(srv.Routes(), http.MethodPost, "/api/runs?source=ci", f)
	select {
	case <-notifier.ch:
	case <-time.After(2 * time.Second):
		t.Fatal("expiry notifier tidak dipanggil")
	}
	if len(fs.expiryMarked) != 0 {
		t.Errorf("gagal kirim tidak boleh ditandai: %+v", fs.expiryMarked)
	}
	deadline := time.Now().Add(time.Second)
	for len(fs.deliveries) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if len(fs.deliveries) != 1 || fs.deliveries[0].OK || fs.deliveries[0].StatusCode != 403 {
		t.Errorf("delivery gagal salah: %+v", fs.deliveries)
	}
}

func TestProjectsAndFilters(t *testing.T) {
	fs := &fakeStore{projects: []store.ProjectSummary{{
		Project: "toolshop", Tests: 3, Runs: 2, OpenFailures: 1,
	}}}
	h := newServer(fs, 0)

	rec := do(h, http.MethodGet, "/api/projects", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"project": "toolshop"`) {
		t.Fatalf("projects = %d: %s", rec.Code, rec.Body)
	}
	if rec := do(h, http.MethodGet, "/api/runs?project=web", nil); rec.Code != http.StatusOK {
		t.Errorf("runs project = %d: %s", rec.Code, rec.Body)
	}
	if fs.lastRunProject != "web" {
		t.Errorf("runs project tidak diteruskan: %q", fs.lastRunProject)
	}
	if rec := do(h, http.MethodGet, "/api/groups?project=api", nil); rec.Code != http.StatusOK {
		t.Errorf("groups project = %d: %s", rec.Code, rec.Body)
	}
	if fs.lastGroupProject != "api" {
		t.Errorf("groups project tidak diteruskan: %q", fs.lastGroupProject)
	}
}

func TestNotificationDeliveriesEndpoint(t *testing.T) {
	now := time.Now()
	fs := &fakeStore{deliveries: []store.NotificationDelivery{
		{At: now, Event: "test", OK: true, Attempts: 1},
		{At: now.Add(time.Minute), Event: "new_failure", RunID: 7, OK: false,
			StatusCode: 500, Attempts: 2, Error: "boom"},
	}}
	h := newServer(fs, 0)

	rec := do(h, http.MethodGet, "/api/config/notifications/deliveries?limit=1", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"new_failure"`) ||
		strings.Contains(rec.Body.String(), `"event": "test"`) {
		t.Errorf("deliveries limit = %d: %s", rec.Code, rec.Body)
	}
	rec = do(h, http.MethodGet, "/api/config/notifications/deliveries", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"test"`) ||
		!strings.Contains(rec.Body.String(), `"new_failure"`) {
		t.Errorf("deliveries = %d: %s", rec.Code, rec.Body)
	}
	if rec := do(h, http.MethodGet, "/api/config/notifications/deliveries?limit=nope", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("limit invalid = %d, want 400", rec.Code)
	}
}

func TestCoverageChangeImpact(t *testing.T) {
	now := time.Now()
	latest := `{"openapi":"3.0.0","paths":{
		"/brands":{"get":{"responses":{"200":{"description":"ok"}}}},
		"/products":{"get":{"responses":{"200":{"description":"ok"}}}},
		"/many":{"get":{"responses":{"200":{"description":"ok"}}}}}}`
	previous := `{"openapi":"3.0.0","paths":{
		"/brands":{"get":{"responses":{"200":{"description":"ok"}}}},
		"/old":{"get":{"responses":{"200":{"description":"ok"}}}}}}`
	calls := []store.EndpointCall{
		{RunID: 2, Method: "GET", Path: "/brands", Status: 200, TestKey: "suite › brands.spec.ts › list", At: now},
		{RunID: 2, Method: "GET", Path: "/products", Status: 200, TestKey: "suite › products.spec.ts › list", At: now},
		{RunID: 1, Method: "GET", Path: "/old", Status: 200, TestKey: "suite › legacy.spec.ts › old", At: now},
	}
	for i := 0; i < 25; i++ {
		calls = append(calls, store.EndpointCall{
			RunID: 2, Method: "GET", Path: "/many", Status: 200,
			TestKey: "suite › many.spec.ts › test " + strconv.Itoa(i), At: now,
		})
	}
	fs := &fakeStore{
		snapshots: []store.ContractSnapshot{
			{RunID: 2, At: now, ContractID: "impact-new", Document: latest},
			{RunID: 1, At: now.Add(-time.Hour), ContractID: "impact-old", Document: previous},
		},
		calls: calls,
	}
	h := newServer(fs, 0)
	rec := do(h, http.MethodGet, "/api/coverage?project=toolshop", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("coverage = %d: %s", rec.Code, rec.Body)
	}
	var body struct {
		Changes []struct {
			Kind     string   `json:"kind"`
			Method   string   `json:"method"`
			Path     string   `json:"path"`
			Tests    int      `json:"tests"`
			TestKeys []string `json:"test_keys"`
		} `json:"changes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	byPath := map[string]struct {
		tests int
		keys  []string
	}{}
	for _, change := range body.Changes {
		byPath[change.Path] = struct {
			tests int
			keys  []string
		}{change.Tests, change.TestKeys}
	}
	if got := byPath["/products"]; got.tests != 1 || len(got.keys) != 1 || got.keys[0] != "suite › products.spec.ts › list" {
		t.Errorf("impact /products salah: %+v", byPath)
	}
	if got := byPath["/old"]; got.tests != 1 || len(got.keys) != 1 || got.keys[0] != "suite › legacy.spec.ts › old" {
		t.Errorf("impact /old (removed) salah: %+v", byPath)
	}
	if got := byPath["/many"]; got.tests != 25 || len(got.keys) != 20 {
		t.Errorf("impact /many harus 25 test dan 20 key: %+v", got)
	}
}

func TestGroupKnowledgeMarkdown(t *testing.T) {
	now := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	fs := &fakeStore{
		group: &store.Group{
			Fingerprint: "fp-brand", TestKey: "toolshop › brand.spec.ts › get all",
			TestCaseID: "TC-BRD-001", Project: "toolshop", File: "api/toolshop/brand.spec.ts",
			Title: "GET /brands returns a brand with a string id", Status: "recurring",
			Summary: "The API changed id from number to string.", Cause: "GET /brands → id type",
			Occurrences: 2, Regressions: 1, FirstSeenRun: 10, LastSeenRun: 12,
			FirstSeenAt: now.Add(-48 * time.Hour), LastSeenAt: now,
			TriageStatus: "acknowledged", TriageOwner: "ega", TriageNote: "reviewing",
			Category: "test_bug", Confidence: "high", AnalysisSource: "ai",
			AnalysisModel: "claude-haiku-5-5", AnalysisSummary: "The test expects a number.",
			SampleError: "expect(received).toBe(expected)\nExpected: number\nReceived: string",
		},
		occurrences: []store.Occurrence{
			{RunID: 12, RunCreatedAt: now, Status: "failed", Branch: "main", CommitSHA: "abcdef123456", AppVersion: "v1.2.0", Source: "ci"},
			{RunID: 10, RunCreatedAt: now.Add(-48 * time.Hour), Status: "failed", Branch: "main", CommitSHA: "1234567890", AppVersion: "v1.1.0", Source: "ci"},
		},
		analysis: &store.Analysis{
			Source: "ai", Model: "claude-haiku-5-5", Category: "test_bug", Confidence: "high",
			Summary: "The test expects a number.", Evidence: []string{"Expected: number", "Received: string"},
			NextStep: "Update the schema to accept string ids.", CreatedAt: now,
		},
		knowledge: &store.Knowledge{
			RunID: 12, PassRunID: 11, Compared: true,
			Files: []store.FileFact{{
				Path: "schemas/brand.ts", Direct: true, Changed: true,
				Others: store.TestSplit{Failed: []string{"TC-BRD-002"}, Passed: []string{"TC-BRD-003"}},
			}},
			Endpoints: []store.EndpointFact{{
				Key: "GET /brands", SeenPassing: true,
				Without: store.TestSplit{Failed: []string{"TC-BRD-004"}},
			}},
		},
		experiments: []store.Experiment{{
			Kind: "rerun", Outcome: "passed", Hypothesis: "The failure is flaky.", Runs: 3, Passes: 2,
			CreatedAt: now,
		}},
	}
	h := newServer(fs, 0)
	rec := do(h, http.MethodGet, "/api/groups/fp-brand/knowledge.md", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("knowledge markdown = %d: %s", rec.Code, rec.Body)
	}
	if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "text/markdown") {
		t.Errorf("content type = %q", got)
	}
	if got := rec.Header().Get("Content-Disposition"); !strings.Contains(got, "redline-TC-BRD-001.md") {
		t.Errorf("content disposition = %q", got)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"# GET /brands returns a brand with a string id",
		"TC-BRD-001",
		"run #12",
		"## Run history",
		"## Knowledge graph",
		"schemas/brand.ts",
		"GET /brands",
		"## Proof",
		"## Sources",
		"Expected: number",
		"Update the schema to accept string ids.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("markdown harus memuat %q:\n%s", want, body)
		}
	}
}

func TestSourceHistory(t *testing.T) {
	now := time.Now()
	fs := &fakeStore{sourceChecks: []store.SourceCheck{
		{Project: "toolshop", At: now, RunID: 12, Status: "invalid", Error: "HTTP 503", DurationMS: 42},
		{Project: "toolshop", At: now.Add(-time.Hour), RunID: 11, Status: "valid", Version: "OpenAPI 3.0.0", Endpoints: 19, DurationMS: 30},
	}}
	h := newServer(fs, 0)

	rec := do(h, http.MethodGet, "/api/config/contracts/history?project=toolshop&limit=1", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status": "invalid"`) ||
		strings.Contains(rec.Body.String(), `"run_id": 11`) {
		t.Errorf("source history limit = %d: %s", rec.Code, rec.Body)
	}
	rec = do(h, http.MethodGet, "/api/config/contracts/history?project=toolshop", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status": "valid"`) ||
		!strings.Contains(rec.Body.String(), `"run_id": 12`) {
		t.Errorf("source history = %d: %s", rec.Code, rec.Body)
	}
	if rec := do(h, http.MethodGet, "/api/config/contracts/history", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("source history tanpa project = %d, want 400", rec.Code)
	}
	if rec := do(h, http.MethodGet, "/api/config/contracts/history?project=bad%20project", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("source history project invalid = %d, want 400", rec.Code)
	}
	if rec := do(h, http.MethodGet, "/api/config/contracts/history?project=toolshop&limit=0", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("source history limit invalid = %d, want 400", rec.Code)
	}
}

func TestTestTrend(t *testing.T) {
	now := time.Now()
	fs := &fakeStore{testTrend: []store.TestHealthPoint{
		{RunID: 1, At: now.Add(-time.Hour), Passed: 2, Failed: 1, PassRate: 2.0 / 3.0},
		{RunID: 2, At: now, Passed: 3, Flaky: 1, PassRate: 0.75, FlakeRate: 0.25},
	}}
	h := newServer(fs, 0)

	rec := do(h, http.MethodGet, "/api/tests/trend?project=toolshop&limit=1", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"run_id": 2`) ||
		strings.Contains(rec.Body.String(), `"run_id": 1`) {
		t.Errorf("test trend limit = %d: %s", rec.Code, rec.Body)
	}
	rec = do(h, http.MethodGet, "/api/tests/trend?project=toolshop", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"run_id": 1`) ||
		!strings.Contains(rec.Body.String(), `"flake_rate": 0.25`) {
		t.Errorf("test trend = %d: %s", rec.Code, rec.Body)
	}
	if rec := do(h, http.MethodGet, "/api/tests/trend?days=0", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("test trend days invalid = %d, want 400", rec.Code)
	}
	if rec := do(h, http.MethodGet, "/api/tests/trend?limit=51", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("test trend limit invalid = %d, want 400", rec.Code)
	}
}

func TestRunReportErrors(t *testing.T) {
	fs := &fakeStore{runErrors: []store.Run{
		{ID: 12, Total: 0, ReportErrors: []string{"Cannot find module './helpers/brands'"}},
		{ID: 11, Total: 0, ReportErrors: []string{"globalSetup failed"}},
	}}
	h := newServer(fs, 0)

	rec := do(h, http.MethodGet, "/api/runs/errors?limit=1", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"id": 12`) ||
		strings.Contains(rec.Body.String(), `"id": 11`) {
		t.Errorf("run errors limit = %d: %s", rec.Code, rec.Body)
	}
	rec = do(h, http.MethodGet, "/api/runs/errors", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"globalSetup failed"`) {
		t.Errorf("run errors = %d: %s", rec.Code, rec.Body)
	}
	if rec := do(h, http.MethodGet, "/api/runs/errors?limit=101", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("run errors limit invalid = %d, want 400", rec.Code)
	}
}

func TestLoadErrorAPI(t *testing.T) {
	fs := &fakeStore{loadErrors: []store.LoadError{{
		ID: "le-1", Message: "Cannot find module './helpers/brands'", Occurrences: 2,
		FirstSeenRun: 1, LastSeenRun: 2, TriageStatus: "open",
	}}}
	h := newServer(fs, 0)

	rec := do(h, http.MethodGet, "/api/load-errors?limit=10", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"id": "le-1"`) ||
		!strings.Contains(rec.Body.String(), `"occurrences": 2`) {
		t.Errorf("load errors = %d: %s", rec.Code, rec.Body)
	}
	rec = do(h, http.MethodPut, "/api/load-errors/le-1/triage",
		strings.NewReader(`{"status":"acknowledged","owner":"ega","note":"fix import","by":"ega"}`))
	if rec.Code != http.StatusOK || fs.loadErrors[0].TriageStatus != "acknowledged" ||
		fs.loadErrors[0].TriageOwner != "ega" {
		t.Errorf("triage load error = %d: %s -> %+v", rec.Code, rec.Body, fs.loadErrors[0])
	}
	if rec := do(h, http.MethodGet, "/api/load-errors?limit=101", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("load errors limit invalid = %d, want 400", rec.Code)
	}
	if rec := do(h, http.MethodPut, "/api/load-errors/le-1/triage",
		strings.NewReader(`{"status":"bogus","owner":"ega"}`)); rec.Code != http.StatusBadRequest {
		t.Errorf("triage status invalid = %d, want 400", rec.Code)
	}
	if rec := do(h, http.MethodPut, "/api/load-errors/missing/triage",
		strings.NewReader(`{"status":"open","owner":"ega"}`)); rec.Code != http.StatusNotFound {
		t.Errorf("triage missing = %d, want 404", rec.Code)
	}

	fs.loadErrors[0].Category = ""
	rec = do(h, http.MethodPost, "/api/load-errors/le-1/analyze", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"category": "test_bug"`) ||
		fs.loadErrors[0].Summary == "" || fs.loadErrors[0].AnalysisSource != "rule" {
		t.Errorf("analyze load error = %d: %s -> %+v", rec.Code, rec.Body, fs.loadErrors[0])
	}
	if rec := do(h, http.MethodPost, "/api/load-errors/missing/analyze", nil); rec.Code != http.StatusNotFound {
		t.Errorf("analyze missing = %d, want 404", rec.Code)
	}
	fs.loadErrors[0].Category = ""
	rec = do(h, http.MethodPost, "/api/load-errors/le-1/analyze?ai=1", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"source": "ai"`) ||
		fs.loadErrors[0].AnalysisSource != "ai" || fs.loadErrors[0].Summary == "" {
		t.Errorf("AI analyze load error = %d: %s -> %+v", rec.Code, rec.Body, fs.loadErrors[0])
	}
}
