package api

import (
	"testing"

	"redline/internal/contract"
)

func TestCoverageHint(t *testing.T) {
	cases := []struct {
		name     string
		method   string
		path     string
		status   string
		observed bool
		want     string
	}{
		{"404 collection", "GET", "/brands", "404", false, "likely_unreachable"},
		{"404 detail", "GET", "/brands/{brandId}", "404", false, "reachable"},
		{"405 get", "GET", "/brands", "405", false, "reachable"},
		{"405 post", "POST", "/brands", "405", false, "reachable"},
		{"404 post", "POST", "/brands", "404", false, "unknown"},
		{"observed 404", "GET", "/brands", "404", true, "unknown"},
		{"200", "GET", "/brands", "200", false, "unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := coverageHint(tc.method, tc.path, tc.status, tc.observed)
			if got.Status != tc.status || got.Hint != tc.want {
				t.Errorf("coverageHint(%q, %q, %q, %v) = %+v, want %q",
					tc.method, tc.path, tc.status, tc.observed, got, tc.want)
			}
			if tc.want != "unknown" && got.Reason == "" {
				t.Errorf("hint %q has no reason: %+v", tc.want, got)
			}
		})
	}
}

func TestObservePathStatusAttributes405ToPathOperations(t *testing.T) {
	doc, err := contract.Parse([]byte(`{
		"openapi":"3.0.0",
		"paths":{
			"/brands":{
				"get":{"responses":{"200":{"description":"ok"},"405":{"description":"method not allowed"}}},
				"post":{"responses":{"201":{"description":"created"},"405":{"description":"method not allowed"}}}
			},
			"/products":{"get":{"responses":{"200":{"description":"ok"}}}}
		}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	observed := map[string]map[string]bool{}
	observePathStatus(doc, observed, "/brands", "405")
	if !observed["GET /brands"]["405"] || !observed["POST /brands"]["405"] {
		t.Fatalf("405 should be observed for GET and POST /brands: %+v", observed)
	}
	if observed["GET /brands"]["200"] {
		t.Error("only the requested status should be marked observed")
	}
	if len(observed["GET /products"]) != 0 {
		t.Error("405 must not leak to another path")
	}
}
