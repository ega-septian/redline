package contract_test

import (
	"strings"
	"testing"

	"redline/internal/contract"
)

func parse(t *testing.T, raw string) *contract.Document {
	t.Helper()
	doc, err := contract.Parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestEndpointsAndMatch(t *testing.T) {
	doc := parse(t, `{
		"openapi":"3.0.0",
		"paths":{
			"/brands":{
				"get":{"summary":"List brands","responses":{"200":{"description":"ok"},"401":{"description":"no token"}}},
				"post":{"responses":{"201":{"description":"created"},"422":{"description":"invalid"}}}
			},
			"/products/{productId}":{"get":{"responses":{"200":{"description":"ok"},"404":{"description":"missing"}}}}
		}
	}`)
	endpoints := doc.Endpoints()
	if len(endpoints) != 3 {
		t.Fatalf("mau 3 endpoint, dapat %+v", endpoints)
	}
	if endpoints[0].Method != "GET" || endpoints[0].Path != "/brands" ||
		strings.Join(endpoints[0].StatusCodes, ",") != "200,401" {
		t.Errorf("endpoint pertama salah: %+v", endpoints[0])
	}
	if endpoints[0].Summary != "List brands" {
		t.Errorf("summary salah: %+v", endpoints[0])
	}
	if tmpl, ok := doc.Match("GET", "/products/123"); !ok || tmpl != "/products/{productId}" {
		t.Errorf("match GET /products/123 = %q %v", tmpl, ok)
	}
	if _, ok := doc.Match("DELETE", "/brands"); ok {
		t.Error("DELETE /brands tidak boleh match")
	}
	if _, ok := doc.Match("GET", "/unknown"); ok {
		t.Error("endpoint tidak terdokumentasi tidak boleh match")
	}
}

func TestContractDiff(t *testing.T) {
	previous := parse(t, `{
		"openapi":"3.0.0",
		"paths":{
			"/brands":{
				"get":{"responses":{"200":{"description":"ok"}}},
				"post":{"requestBody":{"content":{"application/json":{"schema":{"type":"object","properties":{"name":{"type":"string"}}}}}},
					"responses":{"201":{"description":"created"}}}
			},
			"/old":{"get":{"responses":{"200":{"description":"ok"}}}}
		}
	}`)
	current := parse(t, `{
		"openapi":"3.0.0",
		"paths":{
			"/brands":{
				"get":{"responses":{"200":{"description":"ok"},"401":{"description":"no token"}}},
				"post":{"requestBody":{"content":{"application/json":{"schema":{"type":"object","properties":{"name":{"type":"string"},"slug":{"type":"string"}}}}}},
					"responses":{"201":{"description":"created","content":{"application/json":{"schema":{"type":"object"}}}}}}
			},
			"/products":{"get":{"responses":{"200":{"description":"ok"}}}}
		}
	}`)
	changes := current.Diff(previous)
	byDetail := map[string]contract.ContractChange{}
	for _, change := range changes {
		byDetail[change.Detail] = change
	}
	for _, want := range []string{"status 401 added", "new endpoint in the contract", "removed from the contract", "request schema changed", "response schema changed"} {
		if _, ok := byDetail[want]; !ok {
			t.Errorf("change %q tidak ada: %+v", want, changes)
		}
	}
	if change := byDetail["new endpoint in the contract"]; change.Method != "GET" || change.Path != "/products" || change.Kind != "added" {
		t.Errorf("added endpoint salah: %+v", change)
	}
	if change := byDetail["removed from the contract"]; change.Path != "/old" || change.Kind != "removed" {
		t.Errorf("removed endpoint salah: %+v", change)
	}
}
