package contract

import (
	"encoding/json"
	"sort"
	"strings"
)

// EndpointSpec is one operation documented in an OpenAPI contract.
type EndpointSpec struct {
	Method      string   `json:"method"`
	Path        string   `json:"path"`
	Summary     string   `json:"summary,omitempty"`
	StatusCodes []string `json:"status_codes"`

	// Fingerprints are internal: they let Diff notice schema/parameter changes without exposing the contract.
	RequestFingerprint   string `json:"-"`
	ResponseFingerprint  string `json:"-"`
	ParameterFingerprint string `json:"-"`
}

// ContractChange is one difference between two contract snapshots.
type ContractChange struct {
	Kind   string `json:"kind"` // added / changed / removed
	Method string `json:"method"`
	Path   string `json:"path"`
	Detail string `json:"detail"`
}

var httpMethods = []string{"get", "post", "put", "patch", "delete", "head", "options"}

// Endpoints returns every documented operation, sorted by path and method.
func (d *Document) Endpoints() []EndpointSpec {
	out := []EndpointSpec{}
	for path, operations := range d.paths {
		for _, method := range httpMethods {
			op, ok := operations[method].(map[string]any)
			if !ok {
				continue
			}
			summary, _ := op["summary"].(string)
			out = append(out, EndpointSpec{
				Method: strings.ToUpper(method), Path: path, Summary: summary,
				StatusCodes:          operationStatusCodes(op),
				RequestFingerprint:   partFingerprint(op["requestBody"]),
				ResponseFingerprint:  partFingerprint(stripDescriptions(op["responses"])),
				ParameterFingerprint: partFingerprint(op["parameters"]),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Method < out[j].Method
	})
	return out
}

// Match maps a concrete method+path to the documented template and reports whether the method exists.
func (d *Document) Match(method, path string) (string, bool) {
	tmpl := d.match(path)
	if tmpl == "" {
		return "", false
	}
	if _, ok := d.paths[tmpl][strings.ToLower(method)]; !ok {
		return "", false
	}
	return tmpl, true
}

// Diff compares this contract with a previous snapshot. It reports added/removed endpoints, status-code
// changes and schema/parameter changes for endpoints that still exist. Descriptions are ignored.
func (d *Document) Diff(previous *Document) []ContractChange {
	if previous == nil {
		return []ContractChange{}
	}
	current := endpointMap(d)
	old := endpointMap(previous)
	out := []ContractChange{}
	for key, endpoint := range current {
		prev, ok := old[key]
		if !ok {
			out = append(out, ContractChange{Kind: "added", Method: endpoint.Method, Path: endpoint.Path,
				Detail: "new endpoint in the contract"})
			continue
		}
		out = append(out, diffEndpoint(endpoint, prev)...)
	}
	for key, endpoint := range old {
		if _, ok := current[key]; !ok {
			out = append(out, ContractChange{Kind: "removed", Method: endpoint.Method, Path: endpoint.Path,
				Detail: "removed from the contract"})
		}
	}
	order := map[string]int{"added": 0, "changed": 1, "removed": 2}
	sort.Slice(out, func(i, j int) bool {
		left, right := out[i], out[j]
		if order[left.Kind] != order[right.Kind] {
			return order[left.Kind] < order[right.Kind]
		}
		if left.Path != right.Path {
			return left.Path < right.Path
		}
		if left.Method != right.Method {
			return left.Method < right.Method
		}
		return left.Detail < right.Detail
	})
	return out
}

func endpointMap(d *Document) map[string]EndpointSpec {
	out := map[string]EndpointSpec{}
	for _, endpoint := range d.Endpoints() {
		out[endpoint.Method+" "+endpoint.Path] = endpoint
	}
	return out
}

func diffEndpoint(current, previous EndpointSpec) []ContractChange {
	out := []ContractChange{}
	prevCodes := map[string]bool{}
	for _, code := range previous.StatusCodes {
		prevCodes[code] = true
	}
	currentCodes := map[string]bool{}
	for _, code := range current.StatusCodes {
		currentCodes[code] = true
		if !prevCodes[code] {
			out = append(out, ContractChange{Kind: "changed", Method: current.Method, Path: current.Path,
				Detail: "status " + code + " added"})
		}
	}
	for _, code := range previous.StatusCodes {
		if !currentCodes[code] {
			out = append(out, ContractChange{Kind: "changed", Method: current.Method, Path: current.Path,
				Detail: "status " + code + " removed"})
		}
	}
	if current.RequestFingerprint != previous.RequestFingerprint {
		out = append(out, ContractChange{Kind: "changed", Method: current.Method, Path: current.Path,
			Detail: "request schema changed"})
	}
	if current.ResponseFingerprint != previous.ResponseFingerprint {
		out = append(out, ContractChange{Kind: "changed", Method: current.Method, Path: current.Path,
			Detail: "response schema changed"})
	}
	if current.ParameterFingerprint != previous.ParameterFingerprint {
		out = append(out, ContractChange{Kind: "changed", Method: current.Method, Path: current.Path,
			Detail: "parameters changed"})
	}
	return out
}

func operationStatusCodes(op map[string]any) []string {
	responses, _ := op["responses"].(map[string]any)
	out := make([]string, 0, len(responses))
	for code := range responses {
		if code != "default" {
			out = append(out, code)
		}
	}
	sort.Strings(out)
	return out
}

// partFingerprint returns a stable representation without descriptions, used to notice schema changes.
func partFingerprint(value any) string {
	data, _ := json.Marshal(stripDescriptions(value))
	return string(data)
}

func stripDescriptions(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := map[string]any{}
		for key, child := range typed {
			if key == "description" {
				continue
			}
			out[key] = stripDescriptions(child)
		}
		return out
	case []any:
		out := make([]any, 0, len(typed))
		for _, child := range typed {
			out = append(out, stripDescriptions(child))
		}
		return out
	default:
		return value
	}
}
