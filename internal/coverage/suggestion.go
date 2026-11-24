// Package coverage turns documented but unobserved status codes into deterministic test recipes.
// A suggestion is evidence for the operator, never an automatic ignore or merge.
package coverage

import (
	"fmt"
	"strings"

	"redline/internal/contract"
)

type Kind string

const (
	KindUnreachable Kind = "unreachable"
	KindRecipe      Kind = "recipe"
	KindBusiness    Kind = "business"
)

// Request is the actual call a probe should make. It can differ from the documented operation:
// a GET operation can document 405, but the 405 is observed by calling the path with PUT/PATCH/DELETE.
type Request struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}

// Suggestion is one deterministic recipe for a missing status code.
type Suggestion struct {
	Kind           Kind     `json:"kind"`
	Confidence     string   `json:"confidence"`
	Reason         string   `json:"reason"`
	Method         string   `json:"method"`
	Path           string   `json:"path"`
	Status         string   `json:"status"`
	Request        *Request `json:"request,omitempty"`
	ExpectedStatus int      `json:"expected_status,omitempty"`
	Command        string   `json:"command,omitempty"`
}

// Suggest returns a deterministic recipe when the status has a known way to be observed.
// Nil means there is nothing safe to suggest without more context.
func Suggest(project string, endpoint contract.EndpointSpec, pathMethods map[string]bool, status string, observed bool) *Suggestion {
	if observed {
		return nil
	}
	s := &Suggestion{Method: endpoint.Method, Path: endpoint.Path, Status: status}
	switch status {
	case "405":
		candidate := firstNotDocumented(pathMethods, "PUT", "PATCH", "DELETE", "OPTIONS", "HEAD")
		if candidate == "" {
			return nil
		}
		s.Kind = KindRecipe
		s.Confidence = "high"
		s.Reason = fmt.Sprintf("405 is path-level; %s %s is not documented for this path and should return 405.", candidate, endpoint.Path)
		s.Request = &Request{Method: candidate, Path: endpoint.Path}
		s.ExpectedStatus = 405
		s.Command = fmt.Sprintf("npx redline coverage probe --project=%s --operation %q --status %s",
			project, endpoint.Method+" "+endpoint.Path, status)
		return s
	case "404":
		s.Confidence = "medium"
		if strings.Contains(endpoint.Path, "{") {
			s.Kind = KindRecipe
			s.Reason = "Use an id that existed and was deleted; this needs data setup."
		} else if endpoint.Method == "GET" {
			s.Kind = KindUnreachable
			s.Confidence = "high"
			s.Reason = "No path parameter; 404 belongs to a detail endpoint."
		} else {
			s.Kind = KindRecipe
			s.Reason = "Use a resource that was created and deleted."
		}
	case "409":
		s.Kind = KindRecipe
		s.Confidence = "medium"
		s.Reason = "Send a duplicate unique value; this needs data setup."
	case "422":
		s.Kind = KindRecipe
		s.Confidence = "high"
		s.Reason = "Send a request that violates the documented schema; this needs data setup."
	case "400":
		s.Kind = KindRecipe
		s.Confidence = "medium"
		s.Reason = "Send a malformed body or query; this needs a small fixture."
	case "401":
		s.Kind = KindRecipe
		s.Confidence = "medium"
		s.Reason = "Call without credentials; this needs auth context."
	case "403":
		s.Kind = KindRecipe
		s.Confidence = "medium"
		s.Reason = "Call with a valid token that has the wrong role; this needs a role fixture."
	default:
		s.Kind = KindBusiness
		s.Confidence = "low"
		s.Reason = "No deterministic recipe; this needs business context or AI."
	}
	return s
}

func firstNotDocumented(pathMethods map[string]bool, candidates ...string) string {
	for _, method := range candidates {
		if !pathMethods[method] {
			return method
		}
	}
	return ""
}
