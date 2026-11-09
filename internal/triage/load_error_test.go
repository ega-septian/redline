package triage

import "testing"

func TestAnalyzeLoadError(t *testing.T) {
	cases := []struct {
		message string
		want    string
		source  string
	}{
		{"Cannot find module './helpers/brands' imported from tests/brands.spec.ts", "test_bug", "rule"},
		{"Cannot find package 'zod' imported from tests/brands.spec.ts", "environment", "rule"},
		{"Failed to resolve import \"../shared/schema\"", "test_bug", "rule"},
		{"SyntaxError: Unexpected token '}'", "test_bug", "rule"},
		{"Error in globalSetup: connect ECONNREFUSED 127.0.0.1:8091", "environment", "rule"},
		{"Error in globalTeardown: timeout after 30000ms", "environment", "rule"},
		{"EACCES: permission denied, open '/app/storage/logs/laravel.log'", "environment", "rule"},
		{"Something unexpected happened", "unclear", "none"},
	}
	for _, tc := range cases {
		got := AnalyzeLoadError(tc.message)
		if got.Category != tc.want || got.Source != tc.source ||
			got.Summary == "" || got.NextStep == "" {
			t.Errorf("%q -> %+v, want category %s source %s", tc.message, got, tc.want, tc.source)
		}
	}
}
