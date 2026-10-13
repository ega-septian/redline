package analysis

import (
	"strings"
	"testing"

	"redline/internal/store"
	"redline/internal/triage"
)

// Fakta seperti skenario benchmark kag: brands.spec.ts dan products.spec.ts memakai helper -> schema,
// direct.spec.ts memanggil GET /brands/:id tanpa helper.
const (
	getKey    = "bench › brands.spec.ts › get brand via helper"
	listKey   = "bench › brands.spec.ts › list brands via helper"
	matchKey  = "bench › products.spec.ts › product brand matches brand endpoint"
	detailKey = "bench › products.spec.ts › product detail"
	directKey = "bench › direct.spec.ts › brand by id without helper"
)

func split(passed, failed []string) store.TestSplit {
	if passed == nil {
		passed = []string{}
	}
	if failed == nil {
		failed = []string{}
	}
	return store.TestSplit{Passed: passed, Failed: failed}
}

func graphFacts(testKey, response string, testChanged string, k *store.Knowledge) *store.Facts {
	k.PassRunID, k.Compared = 1, true
	return &store.Facts{
		Group:        store.Group{TestKey: testKey, Title: testKey[strings.LastIndex(testKey, "› ")+4:]},
		ErrorMessage: "expect(received).toBe(expected)",
		LastPass:     &store.RunRef{RunID: 1},
		FirstFail:    store.RunRef{RunID: 2},
		TestChanged:  testChanged,
		Response:     triage.ResponseDiff{Verdict: response, Lines: []string{"GET /brands/:id: field hilang: name (string)"}},
		Knowledge:    k,
	}
}

func schemaChanged(others store.TestSplit) []store.FileFact {
	return []store.FileFact{
		{Path: "kag-specs/brands.spec.ts", Direct: true, Others: split(nil, nil)},
		{Path: "kag-specs/helpers/brands.ts", Direct: true, Others: split(nil, nil)},
		{Path: "kag-specs/schemas/brand.ts", Changed: true, Others: others},
	}
}

func TestGraphRules(t *testing.T) {
	cases := []struct {
		name       string
		facts      *store.Facts
		category   string // "" = aturan tidak memutuskan
		confidence string
		mention    string
	}{
		{
			name: "K01 schema dua import di bawah berubah, test langsung lulus",
			facts: graphFacts(getKey, triage.ResponseSame, "tidak", &store.Knowledge{
				Files: schemaChanged(split([]string{detailKey}, []string{listKey, matchKey})),
				Endpoints: []store.EndpointFact{{Key: "GET /brands/:id", SeenPassing: true,
					WithChanged: split(nil, []string{matchKey}), Without: split([]string{directKey}, nil)}},
			}),
			category: "test_bug", confidence: "high", mention: "schemas/brand.ts berubah (lewat import lain)",
		},
		{
			name: "K01 tapi tidak ada pemakai lain yang ikut gagal: kurang yakin",
			facts: graphFacts(getKey, triage.ResponseSame, "tidak", &store.Knowledge{
				Files: schemaChanged(split([]string{detailKey}, nil)),
				Endpoints: []store.EndpointFact{{Key: "GET /brands/:id", SeenPassing: true,
					WithChanged: split(nil, nil), Without: split([]string{directKey}, nil)}},
			}),
			category: "test_bug", confidence: "medium",
		},
		{
			name: "K02 helper salah ketik path: endpoint baru hanya lewat kode yang berubah",
			facts: graphFacts(getKey, triage.ResponseUnknown, "ya", &store.Knowledge{
				Files: []store.FileFact{{Path: "kag-specs/helpers/brands.ts", Direct: true, Changed: true,
					Others: split([]string{listKey, detailKey}, []string{matchKey})}},
				Endpoints: []store.EndpointFact{{Key: "GET /brand/:id", SeenPassing: false,
					WithChanged: split(nil, []string{matchKey}), Without: split(nil, nil)}},
			}),
			category: "test_bug", confidence: "high", mention: "GET /brand/:id",
		},
		{
			name: "K04 helper berubah, tapi test tanpa helper di spec lain juga gagal",
			facts: graphFacts(getKey, triage.ResponseChanged, "ya", &store.Knowledge{
				Files: []store.FileFact{{Path: "kag-specs/helpers/brands.ts", Direct: true, Changed: true,
					Others: split([]string{listKey, detailKey}, []string{matchKey})}},
				Endpoints: []store.EndpointFact{{Key: "GET /brands/:id", SeenPassing: true,
					WithChanged: split(nil, []string{matchKey}), Without: split(nil, []string{directKey})}},
			}),
			category: "backend_bug", confidence: "high", mention: "GET /brands/:id",
		},
		{
			name: "K05 kode tidak berubah, endpoint gagal di beberapa spec",
			facts: graphFacts(matchKey, triage.ResponseSame, "tidak", &store.Knowledge{
				Files: []store.FileFact{{Path: "kag-specs/products.spec.ts", Direct: true, Others: split(nil, nil)}},
				Endpoints: []store.EndpointFact{
					{Key: "GET /brands/:id", SeenPassing: true, WithChanged: split(nil, nil), Without: split(nil, []string{getKey, directKey})},
					{Key: "GET /products/:id", SeenPassing: true, WithChanged: split(nil, nil), Without: split([]string{detailKey}, nil)},
				},
			}),
			category: "backend_bug", confidence: "high",
		},
		{
			name: "K06 schema berubah (tidak berhubungan) dan response berubah: matriks response yang memutuskan",
			facts: graphFacts(getKey, triage.ResponseChanged, "tidak", &store.Knowledge{
				Files: schemaChanged(split([]string{detailKey}, []string{listKey, matchKey})),
				Endpoints: []store.EndpointFact{{Key: "GET /brands/:id", SeenPassing: true,
					WithChanged: split(nil, []string{matchKey}), Without: split([]string{directKey}, nil)}},
			}),
			category: "backend_bug", confidence: "high", mention: "bentuk response API berubah",
		},
		{
			name: "endpoint campuran (ada yang lulus, ada yang gagal tanpa file berubah): tidak memutuskan",
			facts: graphFacts(getKey, triage.ResponseSame, "tidak", &store.Knowledge{
				Files: schemaChanged(split(nil, []string{matchKey})),
				Endpoints: []store.EndpointFact{{Key: "GET /brands/:id", SeenPassing: true,
					WithChanged: split(nil, nil), Without: split([]string{detailKey}, []string{directKey})}},
			}),
		},
		{
			name: "yang gagal hanya di spec yang sama: belum tentu endpoint",
			facts: graphFacts(getKey, triage.ResponseSame, "tidak", &store.Knowledge{
				Files: []store.FileFact{{Path: "kag-specs/brands.spec.ts", Direct: true, Others: split(nil, nil)}},
				Endpoints: []store.EndpointFact{{Key: "GET /brands/:id", SeenPassing: true,
					WithChanged: split(nil, nil), Without: split(nil, []string{listKey})}},
			}),
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := ApplyRules(c.facts)
			if c.category == "" {
				if res != nil {
					t.Fatalf("aturan tidak boleh memutuskan: %+v", res)
				}
				return
			}
			if res == nil || res.Category != c.category || res.Confidence != c.confidence {
				t.Fatalf("mau %s/%s, dapat %+v", c.category, c.confidence, res)
			}
			if c.mention != "" && !strings.Contains(res.Summary+strings.Join(res.Evidence, "|"), c.mention) {
				t.Errorf("penjelasan harus menyebut %q: %s %v", c.mention, res.Summary, res.Evidence)
			}
		})
	}

	// Tanpa pembanding yang punya graph, "file berubah" tidak bisa dipercaya: aturan graph diam.
	f := cases[0].facts
	f.Knowledge.Compared = false
	if res := graphRules(f); res != nil {
		t.Errorf("tanpa pembanding graph: %+v", res)
	}
	f.Knowledge.Compared, f.LastPass = true, nil
	if res := graphRules(f); res != nil {
		t.Errorf("test yang belum pernah lulus: %+v", res)
	}
}

func TestBuildFacts_GraphSection(t *testing.T) {
	f := graphFacts(getKey, triage.ResponseSame, "tidak", &store.Knowledge{
		Files: schemaChanged(split([]string{detailKey}, []string{listKey, matchKey})),
		Endpoints: []store.EndpointFact{{Key: "GET /brands/:id", SeenPassing: true,
			WithChanged: split(nil, []string{matchKey}), Without: split([]string{directKey}, nil)}},
	})
	text := BuildFacts(f)
	for _, want := range []string{
		"Pengetahuan graph",
		"- File kag-specs/schemas/brand.ts (lewat import lain, BERUBAH sejak lulus terakhir)",
		"test lain yang memakainya: lulus 1 (products.spec.ts › product detail); gagal 2",
		"- Endpoint GET /brands/:id (pernah lulus sebelumnya)",
		"test lain tanpa file yang berubah: lulus 1 (direct.spec.ts › brand by id without helper); gagal 0",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("fakta harus memuat %q:\n%s", want, text)
		}
	}
	f.Knowledge = nil
	if strings.Contains(BuildFacts(f), "Pengetahuan graph") {
		t.Error("tanpa graph, bagian ini tidak ditulis")
	}
}
