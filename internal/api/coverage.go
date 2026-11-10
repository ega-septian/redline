package api

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"redline/internal/contract"
	"redline/internal/store"
)

type coverageEndpoint struct {
	Method              string     `json:"method"`
	Path                string     `json:"path"`
	Summary             string     `json:"summary,omitempty"`
	StatusCodes         []string   `json:"status_codes"`
	ObservedStatusCodes []string   `json:"observed_status_codes"`
	Covered             bool       `json:"covered"`
	Tests               int        `json:"tests"`
	TestKeys            []string   `json:"test_keys"`
	LastTestedAt        *time.Time `json:"last_tested_at,omitempty"`
}

type coverageSummary struct {
	Documented            int `json:"documented"`
	Covered               int `json:"covered"`
	Uncovered             int `json:"uncovered"`
	StatusCodesDocumented int `json:"status_codes_documented"`
	StatusCodesSeen       int `json:"status_codes_seen"`
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

	coveredTests := map[string]map[string]bool{}
	observed := map[string]map[string]bool{}
	lastTested := map[string]time.Time{}
	for _, call := range calls {
		template, ok := latest.Match(call.Method, call.Path)
		if !ok {
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
		row := coverageEndpoint{
			Method: endpoint.Method, Path: endpoint.Path, Summary: endpoint.Summary,
			StatusCodes: endpoint.StatusCodes, ObservedStatusCodes: statuses,
			Tests: len(coveredTests[key]), TestKeys: testKeys,
			Covered: len(coveredTests[key]) > 0,
		}
		if at := lastTested[key]; !at.IsZero() {
			row.LastTestedAt = &at
		}
		response.Summary.Documented++
		response.Summary.StatusCodesDocumented += len(endpoint.StatusCodes)
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
