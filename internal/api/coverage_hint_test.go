package api

import "testing"

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
		{"405 get", "GET", "/brands", "405", false, "likely_unreachable"},
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
