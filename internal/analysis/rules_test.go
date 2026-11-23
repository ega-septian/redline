package analysis

import (
	"strings"
	"testing"

	"redline/internal/store"
)

func TestBackendRuleNextStepDoesNotRequireBackendAccess(t *testing.T) {
	res := ApplyRules(&store.Facts{
		ErrorMessage: "Error: expect(received).toBe(expected)\n\nExpected: 200\nReceived: 500",
	})
	if res == nil || res.Category != "backend_bug" {
		t.Fatalf("want backend_bug, got %+v", res)
	}
	for _, want := range []string{"QA/SDET", "backend owner", "do not change"} {
		if !strings.Contains(res.NextStep, want) {
			t.Errorf("next step missing %q: %s", want, res.NextStep)
		}
	}
}
