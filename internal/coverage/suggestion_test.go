package coverage

import (
	"strings"
	"testing"

	"redline/internal/contract"
)

func TestSuggest405UsesUndocumentedMethod(t *testing.T) {
	endpoint := contract.EndpointSpec{Method: "GET", Path: "/brands", StatusCodes: []string{"200", "405"}}
	methods := map[string]bool{"GET": true, "POST": true}
	got := Suggest("toolshop", endpoint, methods, "405", false)
	if got == nil || got.Kind != KindRecipe || got.Confidence != "high" {
		t.Fatalf("want high-confidence recipe, got %+v", got)
	}
	if got.Request == nil || got.Request.Method != "PUT" || got.Request.Path != "/brands" {
		t.Fatalf("want PUT /brands request, got %+v", got.Request)
	}
	if got.ExpectedStatus != 405 || !strings.Contains(got.Command, "coverage probe") {
		t.Fatalf("want 405 and probe command, got %+v", got)
	}
}

func TestSuggestSkipsObservedStatus(t *testing.T) {
	endpoint := contract.EndpointSpec{Method: "GET", Path: "/brands"}
	if got := Suggest("toolshop", endpoint, map[string]bool{"GET": true}, "405", true); got != nil {
		t.Fatalf("observed status must not get a suggestion: %+v", got)
	}
}

func TestSuggest404CollectionIsUnreachable(t *testing.T) {
	endpoint := contract.EndpointSpec{Method: "GET", Path: "/brands"}
	got := Suggest("toolshop", endpoint, map[string]bool{"GET": true}, "404", false)
	if got == nil || got.Kind != KindUnreachable {
		t.Fatalf("want unreachable 404, got %+v", got)
	}
}

func TestSuggest422IsRecipe(t *testing.T) {
	endpoint := contract.EndpointSpec{Method: "POST", Path: "/brands"}
	got := Suggest("toolshop", endpoint, map[string]bool{"POST": true}, "422", false)
	if got == nil || got.Kind != KindRecipe || !strings.Contains(got.Reason, "schema") {
		t.Fatalf("want 422 recipe, got %+v", got)
	}
}
