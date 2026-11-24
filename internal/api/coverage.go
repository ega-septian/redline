package api

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"redline/internal/contract"
	"redline/internal/coverage"
	"redline/internal/store"
)

type coverageStatusHint struct {
	Status     string               `json:"status"`
	Hint       string               `json:"hint"` // likely_unreachable / reachable / unknown
	Reason     string               `json:"reason"`
	Suggestion *coverage.Suggestion `json:"suggestion,omitempty"`
}

type coverageEndpoint struct {
	Method              string               `json:"method"`
	Path                string               `json:"path"`
	Summary             string               `json:"summary,omitempty"`
	StatusCodes         []string             `json:"status_codes"`
	ObservedStatusCodes []string             `json:"observed_status_codes"`
	IgnoredStatusCodes  []string             `json:"ignored_status_codes"`
	StatusHints         []coverageStatusHint `json:"status_hints"`
	Covered             bool                 `json:"covered"`
	Tests               int                  `json:"tests"`
	TestKeys            []string             `json:"test_keys"`
	LastTestedAt        *time.Time           `json:"last_tested_at,omitempty"`
}

type coverageSummary struct {
	Documented            int `json:"documented"`
	Covered               int `json:"covered"`
	Uncovered             int `json:"uncovered"`
	StatusCodesDocumented int `json:"status_codes_documented"`
	StatusCodesSeen       int `json:"status_codes_seen"`
	StatusCodesIgnored    int `json:"status_codes_ignored"`
	Changes               int `json:"changes"`
}

type coveragePoint struct {
	RunID                 int64     `json:"run_id"`
	At                    time.Time `json:"at"`
	Documented            int       `json:"documented"`
	Covered               int       `json:"covered"`
	StatusCodesDocumented int       `json:"status_codes_documented"`
	StatusCodesSeen       int       `json:"status_codes_seen"`
	Changes               int       `json:"changes"`
}

type coverageChange struct {
	contract.ContractChange
	Tests    int      `json:"tests"`
	TestKeys []string `json:"test_keys"`
}

type coverageResponse struct {
	Available bool                    `json:"available"`
	Project   string                  `json:"project"`
	Message   string                  `json:"message,omitempty"`
	Snapshot  *store.ContractSnapshot `json:"snapshot,omitempty"`
	Previous  *store.ContractSnapshot `json:"previous,omitempty"`
	Summary   coverageSummary         `json:"summary"`
	Endpoints []coverageEndpoint      `json:"endpoints"`
	Changes   []coverageChange        `json:"changes"`
	History   []coveragePoint         `json:"history"`
}

// coverageHint applies deterministic rules for documented but unobserved status codes. It is a
// suggestion for the operator, never an automatic ignore.
func coverageHint(method, path, status string, observed bool) coverageStatusHint {
	hint := coverageStatusHint{Status: status, Hint: "unknown"}
	if observed {
		return hint
	}
	if status == "405" {
		hint.Hint = "reachable"
		hint.Reason = "405 is path-level; call this path with another HTTP method"
		return hint
	}
	if status == "401" {
		hint.Hint = "reachable"
		hint.Reason = "call without credentials"
		return hint
	}
	if status == "403" {
		hint.Hint = "reachable"
		hint.Reason = "call with a valid token that has the wrong role"
		return hint
	}
	if method != "GET" {
		return hint
	}
	if status == "404" {
		if strings.Contains(path, "{") {
			hint.Hint = "reachable"
			hint.Reason = "use an unknown path parameter"
		} else {
			hint.Hint = "likely_unreachable"
			hint.Reason = "no path parameter; 404 belongs to a detail endpoint"
		}
	}
	return hint
}

// observePathStatus attributes a path-level response such as 405 to every documented operation on
// the same path that lists it. The call method may be undocumented (for example PUT /brands), but
// the response still proves that the path returns 405 for unsupported methods.
func observePathStatus(doc *contract.Document, observed map[string]map[string]bool, path, status string) {
	tmpl, ok := doc.MatchPath(path)
	if !ok {
		return
	}
	for _, endpoint := range doc.Endpoints() {
		if endpoint.Path != tmpl || !hasStatus(endpoint.StatusCodes, status) {
			continue
		}
		key := endpoint.Method + " " + tmpl
		if observed[key] == nil {
			observed[key] = map[string]bool{}
		}
		observed[key][status] = true
	}
}

func hasStatus(statuses []string, status string) bool {
	for _, s := range statuses {
		if s == status {
			return true
		}
	}
	return false
}

// handleCoverage: GET /api/coverage?project=toolshop
func (s *Server) handleCoverage(w http.ResponseWriter, r *http.Request) {
	project := r.URL.Query().Get("project")
	if project == "" {
		writeError(w, http.StatusBadRequest, "project is required")
		return
	}
	snapshots, err := s.Store.ContractSnapshots(r.Context(), project, 20)
	if err != nil {
		s.serverError(w, err)
		return
	}
	if len(snapshots) == 0 {
		message := "No contract snapshot for this project yet. Add a contract source in Config, then send a run."
		if s.hasContractSource(r.Context(), project) {
			message = "Contract source is configured. Send a run so Redline can store the first snapshot."
		}
		writeJSON(w, http.StatusOK, coverageResponse{
			Available: false, Project: project,
			Message:   message,
			Endpoints: []coverageEndpoint{}, Changes: []coverageChange{},
			History: []coveragePoint{},
		})
		return
	}
	docs := make([]*contract.Document, len(snapshots))
	for i, snapshot := range snapshots {
		doc, err := contract.ParseCached(snapshot.ContractID, []byte(snapshot.Document))
		if err != nil {
			s.serverError(w, err)
			return
		}
		docs[i] = doc
	}
	latest := docs[0]
	var previous *contract.Document
	if len(docs) > 1 {
		previous = docs[1]
	}
	calls, err := s.Store.EndpointCalls(r.Context(), project)
	if err != nil {
		s.serverError(w, err)
		return
	}
	ignores, err := s.Store.CoverageIgnores(r.Context(), project)
	if err != nil {
		s.serverError(w, err)
		return
	}
	ignored := map[string]map[string]bool{}
	for _, ignore := range ignores {
		key := ignore.Method + " " + ignore.Path
		if ignored[key] == nil {
			ignored[key] = map[string]bool{}
		}
		ignored[key][ignore.Status] = true
	}

	coveredTests := map[string]map[string]bool{}
	observed := map[string]map[string]bool{}
	lastTested := map[string]time.Time{}
	for _, call := range calls {
		template, ok := latest.Match(call.Method, call.Path)
		if !ok {
			if call.Status == 405 {
				observePathStatus(latest, observed, call.Path, "405")
			}
			continue
		}
		key := call.Method + " " + template
		if coveredTests[key] == nil {
			coveredTests[key] = map[string]bool{}
			observed[key] = map[string]bool{}
		}
		coveredTests[key][call.TestKey] = true
		observed[key][strconv.Itoa(call.Status)] = true
		if call.At.After(lastTested[key]) {
			lastTested[key] = call.At
		}
	}

	// Impact: which tests called each endpoint, matching against the latest contract and the previous
	// one so a removed endpoint still reports the tests that still call it.
	impact := map[string]map[string]bool{}
	for _, call := range calls {
		method := strings.ToUpper(call.Method)
		template := ""
		if matched, ok := latest.Match(method, call.Path); ok {
			template = matched
		} else if previous != nil {
			if matched, ok := previous.Match(method, call.Path); ok {
				template = matched
			}
		}
		if template == "" || call.TestKey == "" {
			continue
		}
		key := method + " " + template
		if impact[key] == nil {
			impact[key] = map[string]bool{}
		}
		impact[key][call.TestKey] = true
	}

	endpoints := latest.Endpoints()
	pathMethods := map[string]map[string]bool{}
	for _, endpoint := range endpoints {
		if pathMethods[endpoint.Path] == nil {
			pathMethods[endpoint.Path] = map[string]bool{}
		}
		pathMethods[endpoint.Path][endpoint.Method] = true
	}
	response := coverageResponse{
		Available: true, Project: project, Snapshot: &snapshots[0],
		Endpoints: make([]coverageEndpoint, 0, len(endpoints)),
		Changes:   []coverageChange{},
		History:   coverageHistory(snapshots, docs, calls),
	}
	if previous != nil {
		response.Previous = &snapshots[1]
		diffs := latest.Diff(previous)
		response.Changes = make([]coverageChange, 0, len(diffs))
		for _, change := range diffs {
			testKeys := sortedSet(impact[change.Method+" "+change.Path])
			tests := len(testKeys)
			if len(testKeys) > 20 {
				testKeys = testKeys[:20]
			}
			response.Changes = append(response.Changes, coverageChange{
				ContractChange: change, Tests: tests, TestKeys: testKeys,
			})
		}
	}
	response.Summary.Changes = len(response.Changes)
	for _, endpoint := range endpoints {
		key := endpoint.Method + " " + endpoint.Path
		statuses := sortedSet(observed[key])
		testKeys := sortedSet(coveredTests[key])
		if len(testKeys) > 20 {
			testKeys = testKeys[:20]
		}
		ignoredStatuses := []string{}
		for _, code := range endpoint.StatusCodes {
			if ignored[key][code] && !observed[key][code] {
				ignoredStatuses = append(ignoredStatuses, code)
			}
		}
		hints := make([]coverageStatusHint, 0, len(endpoint.StatusCodes))
		for _, code := range endpoint.StatusCodes {
			hint := coverageHint(endpoint.Method, endpoint.Path, code, observed[key][code])
			hint.Suggestion = coverage.Suggest(project, endpoint, pathMethods[endpoint.Path], code, observed[key][code])
			if hint.Suggestion != nil && (code == "405" || code == "401") {
				if fields, ok := latest.ResponseFields(endpoint.Method, endpoint.Path, code); ok {
					hint.Suggestion.BodyFields = fields
				}
			}
			hints = append(hints, hint)
		}
		row := coverageEndpoint{
			Method: endpoint.Method, Path: endpoint.Path, Summary: endpoint.Summary,
			StatusCodes: endpoint.StatusCodes, ObservedStatusCodes: statuses,
			IgnoredStatusCodes: ignoredStatuses, StatusHints: hints,
			Tests: len(coveredTests[key]), TestKeys: testKeys,
			Covered: len(coveredTests[key]) > 0,
		}
		if at := lastTested[key]; !at.IsZero() {
			row.LastTestedAt = &at
		}
		response.Summary.Documented++
		response.Summary.StatusCodesDocumented += len(endpoint.StatusCodes)
		response.Summary.StatusCodesIgnored += len(ignoredStatuses)
		for _, code := range endpoint.StatusCodes {
			if observed[key][code] {
				response.Summary.StatusCodesSeen++
			}
		}
		if row.Covered {
			response.Summary.Covered++
		}
		response.Endpoints = append(response.Endpoints, row)
	}
	response.Summary.Uncovered = response.Summary.Documented - response.Summary.Covered
	writeJSON(w, http.StatusOK, response)
}

// handleSetCoverageIgnore: POST /api/coverage/ignores
// {"project":"toolshop","method":"GET","path":"/brands","status":"404","reason":"...","by":"ega"}
func (s *Server) handleSetCoverageIgnore(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Project string `json:"project"`
		Method  string `json:"method"`
		Path    string `json:"path"`
		Status  string `json:"status"`
		Reason  string `json:"reason"`
		By      string `json:"by"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "request body must be JSON")
		return
	}
	if s.User != nil {
		if user := s.User(r); user != "" {
			body.By = user
		}
	}
	body.Project = strings.TrimSpace(body.Project)
	body.Method = strings.ToUpper(strings.TrimSpace(body.Method))
	body.Path = strings.TrimSpace(body.Path)
	body.Status = strings.TrimSpace(body.Status)
	body.Reason = strings.TrimSpace(body.Reason)
	body.By = strings.TrimSpace(body.By)
	if body.Project == "" || body.Method == "" || body.Path == "" || body.Status == "" {
		writeError(w, http.StatusBadRequest, "project, method, path and status are required")
		return
	}
	if body.Reason == "" {
		writeError(w, http.StatusBadRequest, "reason is required")
		return
	}
	if body.By == "" {
		writeError(w, http.StatusBadRequest, "set \"by\" to the name of the person ignoring the status")
		return
	}
	ignore, err := s.Store.SetCoverageIgnore(r.Context(), store.CoverageIgnore{
		Project: body.Project, Method: body.Method, Path: body.Path, Status: body.Status,
		Reason: body.Reason, By: body.By,
	})
	if err != nil {
		s.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ignore)
}

// handleDeleteCoverageIgnore: DELETE /api/coverage/ignores?project=...&method=...&path=...&status=...
func (s *Server) handleDeleteCoverageIgnore(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	project := strings.TrimSpace(q.Get("project"))
	method := strings.ToUpper(strings.TrimSpace(q.Get("method")))
	path := strings.TrimSpace(q.Get("path"))
	status := strings.TrimSpace(q.Get("status"))
	if project == "" || method == "" || path == "" || status == "" {
		writeError(w, http.StatusBadRequest, "project, method, path and status are required")
		return
	}
	if err := s.Store.DeleteCoverageIgnore(r.Context(), project, method, path, status); err != nil {
		s.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"removed": true})
}

// coverageHistory builds one point per stored snapshot, oldest first, using only the calls recorded
// for that run. It compares each contract with the next older snapshot to count contract changes.
func coverageHistory(snapshots []store.ContractSnapshot, docs []*contract.Document, calls []store.EndpointCall) []coveragePoint {
	callsByRun := map[int64][]store.EndpointCall{}
	for _, call := range calls {
		callsByRun[call.RunID] = append(callsByRun[call.RunID], call)
	}
	points := make([]coveragePoint, 0, len(snapshots))
	for i, snapshot := range snapshots {
		doc := docs[i]
		point := coveragePoint{RunID: snapshot.RunID, At: snapshot.At}
		covered := map[string]bool{}
		observed := map[string]map[string]bool{}
		for _, call := range callsByRun[snapshot.RunID] {
			template, ok := doc.Match(call.Method, call.Path)
			if !ok {
				if call.Status == 405 {
					observePathStatus(doc, observed, call.Path, "405")
				}
				continue
			}
			key := call.Method + " " + template
			covered[key] = true
			if observed[key] == nil {
				observed[key] = map[string]bool{}
			}
			observed[key][strconv.Itoa(call.Status)] = true
		}
		for _, endpoint := range doc.Endpoints() {
			key := endpoint.Method + " " + endpoint.Path
			point.Documented++
			point.StatusCodesDocumented += len(endpoint.StatusCodes)
			for _, code := range endpoint.StatusCodes {
				if observed[key][code] {
					point.StatusCodesSeen++
				}
			}
			if covered[key] {
				point.Covered++
			}
		}
		if i+1 < len(docs) {
			point.Changes = len(doc.Diff(docs[i+1]))
		}
		points = append(points, point)
	}
	for i, j := 0, len(points)-1; i < j; i, j = i+1, j-1 {
		points[i], points[j] = points[j], points[i]
	}
	return points
}

func (s *Server) hasContractSource(ctx context.Context, project string) bool {
	sources, err := s.Store.ContractSources(ctx)
	if err != nil {
		return false
	}
	for _, source := range sources {
		if source.Project == project && source.Enabled && source.URL != "" {
			return true
		}
	}
	return false
}

func sortedSet(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}
