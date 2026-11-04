package api

import (
	"net/http"
	"sort"
	"strconv"
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

type coverageResponse struct {
	Available bool                      `json:"available"`
	Project   string                    `json:"project"`
	Message   string                    `json:"message,omitempty"`
	Snapshot  *store.ContractSnapshot   `json:"snapshot,omitempty"`
	Previous  *store.ContractSnapshot   `json:"previous,omitempty"`
	Summary   coverageSummary           `json:"summary"`
	Endpoints []coverageEndpoint        `json:"endpoints"`
	Changes   []contract.ContractChange `json:"changes"`
}

// handleCoverage: GET /api/coverage?project=toolshop
func (s *Server) handleCoverage(w http.ResponseWriter, r *http.Request) {
	project := r.URL.Query().Get("project")
	if project == "" {
		writeError(w, http.StatusBadRequest, "project wajib diisi")
		return
	}
	snapshots, err := s.Store.ContractSnapshots(r.Context(), project, 2)
	if err != nil {
		s.serverError(w, err)
		return
	}
	if len(snapshots) == 0 {
		writeJSON(w, http.StatusOK, coverageResponse{
			Available: false, Project: project,
			Message:   "Belum ada snapshot kontrak untuk project ini. Isi OPENAPI_SPECS dan jalankan ulang.",
			Endpoints: []coverageEndpoint{}, Changes: []contract.ContractChange{},
		})
		return
	}
	latest, err := contract.Parse([]byte(snapshots[0].Document))
	if err != nil {
		s.serverError(w, err)
		return
	}
	var previous *contract.Document
	if len(snapshots) > 1 {
		if parsed, err := contract.Parse([]byte(snapshots[1].Document)); err == nil {
			previous = parsed
		}
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

	endpoints := latest.Endpoints()
	response := coverageResponse{
		Available: true, Project: project, Snapshot: &snapshots[0],
		Endpoints: make([]coverageEndpoint, 0, len(endpoints)),
		Changes:   []contract.ContractChange{},
	}
	if previous != nil {
		response.Previous = &snapshots[1]
		response.Changes = latest.Diff(previous)
	}
	response.Summary.Changes = len(response.Changes)
	for _, endpoint := range endpoints {
		key := endpoint.Method + " " + endpoint.Path
		statuses := sortedSet(observed[key])
		row := coverageEndpoint{
			Method: endpoint.Method, Path: endpoint.Path, Summary: endpoint.Summary,
			StatusCodes: endpoint.StatusCodes, ObservedStatusCodes: statuses,
			Tests: len(coveredTests[key]), Covered: len(coveredTests[key]) > 0,
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

func sortedSet(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}
