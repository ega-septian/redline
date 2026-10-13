package store

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Knowledge mengumpulkan pengetahuan graph (KAG) untuk satu kelompok kegagalan, dari kemunculan gagal
// terakhirnya. Menjawab: file apa yang dipakai test ini (termasuk import bertingkat) dan mana yang berubah
// sejak terakhir lulus, siapa lagi yang memakai file itu, dan siapa lagi yang memanggil endpoint yang sama.
func (s *Store) Knowledge(ctx context.Context, fingerprint string) (*Knowledge, error) {
	res, err := s.query(ctx, `
		MATCH (r:Result {groupId: $id, status: 'failed'})
		WITH r ORDER BY r.runId DESC LIMIT 1
		OPTIONAL MATCH (p:Result {testKey: r.testKey, status: 'passed'}) WHERE p.runId < r.runId
		RETURN r.runId AS run, r.testKey AS key, max(p.runId) AS pass`, map[string]any{"id": fingerprint})
	if err != nil {
		return nil, err
	}
	if len(res.Records) == 0 {
		return nil, ErrNotFound
	}
	p := res.Records[0].AsMap()
	k := &Knowledge{RunID: i64(p, "run"), PassRunID: i64(p, "pass"), Files: []FileFact{}, Endpoints: []EndpointFact{}}
	key := str(p, "key")
	params := map[string]any{"run": k.RunID, "pass": k.PassRunID, "key": key}

	// 1. File yang dipakai test, dan apakah isinya berbeda dari run lulus terakhir. Perubahan hanya bisa
	// dinilai kalau run lulus itu juga punya graph import (reporter lama tidak mengirimnya).
	res, err = s.query(ctx, `
		OPTIONAL MATCH (:Run {id: $pass})-[:HAD]->(anyVersion:FileVersion)
		WITH count(anyVersion) > 0 AS passHasGraph
		MATCH (:Run {id: $run})-[:HAS]->(r:Result {testKey: $key})-[:DEPENDS_ON]->(f:File)
		OPTIONAL MATCH (:Run {id: $run})-[:HAD]->(now:FileVersion {path: f.path})
		OPTIONAL MATCH (:Run {id: $pass})-[:HAD]->(before:FileVersion {path: f.path})
		RETURN f.path AS path, EXISTS { (r)-[:USED]->(f) } AS direct,
		       passHasGraph AND now IS NOT NULL AND coalesce(before.hash, '') <> now.hash AS changed
		ORDER BY path`, params)
	if err != nil {
		return nil, fmt.Errorf("file yang dipakai: %w", err)
	}
	changed := []string{}
	for _, rec := range res.Records {
		p := rec.AsMap()
		f := FileFact{Path: str(p, "path"), Direct: p["direct"] == true, Changed: p["changed"] == true, Others: newSplit()}
		if f.Changed {
			changed = append(changed, f.Path)
		}
		k.Files = append(k.Files, f)
	}

	// 2. Test lain di run ini yang bergantung pada file yang berubah.
	if len(changed) > 0 {
		res, err = s.query(ctx, `
			MATCH (:Run {id: $run})-[:HAS]->(o:Result)-[:DEPENDS_ON]->(f:File)
			WHERE f.path IN $changed AND o.testKey <> $key AND o.status <> 'skipped'
			RETURN f.path AS path, o.testKey AS test, o.status AS status`,
			map[string]any{"run": k.RunID, "key": key, "changed": changed})
		if err != nil {
			return nil, fmt.Errorf("pemakai file yang berubah: %w", err)
		}
		byPath := map[string]*TestSplit{}
		for i := range k.Files {
			byPath[k.Files[i].Path] = &k.Files[i].Others
		}
		for _, rec := range res.Records {
			p := rec.AsMap()
			byPath[str(p, "path")].add(str(p, "test"), str(p, "status"))
		}
	}

	// 3. Endpoint yang dipanggil test ini: pernahkah dipanggil test yang lulus sebelumnya, dan siapa lagi di
	// run ini yang memanggilnya, dipisah menurut memakai file yang berubah atau tidak.
	res, err = s.query(ctx, `
		MATCH (:Run {id: $run})-[:HAS]->(r:Result {testKey: $key})-[:CALLED]->(e:Endpoint)
		RETURN e.key AS endpoint,
		       EXISTS { MATCH (e)<-[:CALLED]-(p:Result) WHERE p.status IN ['passed', 'flaky'] AND p.runId < $run } AS seen
		ORDER BY endpoint`, params)
	if err != nil {
		return nil, fmt.Errorf("endpoint yang dipanggil: %w", err)
	}
	byKey := map[string]*EndpointFact{}
	for _, rec := range res.Records {
		p := rec.AsMap()
		k.Endpoints = append(k.Endpoints, EndpointFact{Key: str(p, "endpoint"), SeenPassing: p["seen"] == true,
			WithChanged: newSplit(), Without: newSplit()})
	}
	for i := range k.Endpoints {
		byKey[k.Endpoints[i].Key] = &k.Endpoints[i]
	}
	res, err = s.query(ctx, `
		MATCH (:Run {id: $run})-[:HAS]->(r:Result {testKey: $key})-[:CALLED]->(e:Endpoint)
		MATCH (:Run {id: $run})-[:HAS]->(o:Result)-[:CALLED]->(e)
		WHERE o.testKey <> $key AND o.status <> 'skipped'
		RETURN e.key AS endpoint, o.testKey AS test, o.status AS status,
		       EXISTS { MATCH (o)-[:DEPENDS_ON]->(f:File) WHERE f.path IN $changed } AS usesChanged`,
		map[string]any{"run": k.RunID, "key": key, "changed": changed})
	if err != nil {
		return nil, fmt.Errorf("pemanggil endpoint yang sama: %w", err)
	}
	for _, rec := range res.Records {
		p := rec.AsMap()
		e := byKey[str(p, "endpoint")]
		if p["usesChanged"] == true {
			e.WithChanged.add(str(p, "test"), str(p, "status"))
		} else {
			e.Without.add(str(p, "test"), str(p, "status"))
		}
	}
	for i := range k.Files {
		k.Files[i].Others.sort()
	}
	for i := range k.Endpoints {
		k.Endpoints[i].WithChanged.sort()
		k.Endpoints[i].Without.sort()
	}
	return k, nil
}

func newSplit() TestSplit { return TestSplit{Passed: []string{}, Failed: []string{}} }

func (t *TestSplit) add(test, status string) {
	switch status {
	case "passed", "flaky":
		t.Passed = append(t.Passed, test)
	case "failed":
		t.Failed = append(t.Failed, test)
	}
}

func (t *TestSplit) sort() {
	sort.Strings(t.Passed)
	sort.Strings(t.Failed)
}

// ImpactOfFile mengembalikan test yang bergantung pada file ini (langsung atau lewat import lain).
//
// Dasarnya run lulus terakhir setiap test (atau run terakhirnya kalau belum pernah lulus): run yang sedang
// rusak bisa memanggil endpoint lain, misalnya karena helper salah ketik path. path boleh lengkap ("kag-specs/schemas/brand.ts") atau akhirannya ("schemas/brand.ts").
func (s *Store) ImpactOfFile(ctx context.Context, path string) ([]ImpactedTest, error) {
	return s.impact(ctx, `(r)-[:DEPENDS_ON]->(x:File) WHERE x.path = $q OR x.path ENDS WITH '/' + $q`, path)
}

// ImpactOfEndpoint mengembalikan test yang memanggil endpoint ini (dasarnya sama dengan ImpactOfFile). endpoint boleh dengan
// method ("GET /brands/1") atau tanpa ("/brands/1", semua method).
func (s *Store) ImpactOfEndpoint(ctx context.Context, endpoint string) ([]ImpactedTest, error) {
	return s.impact(ctx, `(r)-[:CALLED]->(x:Endpoint) WHERE x.key = $q OR x.key ENDS WITH ' ' + $q`,
		endpointPattern(endpoint))
}

// endpointPattern menyamakan endpoint dengan bentuk yang disimpan fixture Redline: ID di path menjadi :id
// ("GET /brands/1" -> "GET /brands/:id"). Aturannya sama dengan normalizePath di fixtures/redline.ts.
func endpointPattern(endpoint string) string {
	endpoint = strings.TrimSpace(endpoint)
	method, path, ok := strings.Cut(endpoint, " ")
	if !ok {
		method, path = "", endpoint
	}
	segs := strings.Split(path, "/")
	for i, seg := range segs {
		if idSegment.MatchString(seg) || strings.Contains(seg, "@") {
			segs[i] = ":id"
		}
	}
	path = strings.Join(segs, "/")
	if method == "" {
		return path
	}
	return strings.ToUpper(method) + " " + path
}

// Angka, UUID, ULID, atau hex panjang.
var idSegment = regexp.MustCompile(`^(\d+|(?i:[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})|[0-9A-HJKMNP-TV-Z]{26}|(?i:[0-9a-f]{16,}))$`)

func (s *Store) impact(ctx context.Context, match, q string) ([]ImpactedTest, error) {
	res, err := s.query(ctx, `
		MATCH (t:Test)<-[:OF]-(any:Result)
		WITH t, max(any.runId) AS last,
		     max(CASE WHEN any.status IN ['passed', 'flaky'] THEN any.runId END) AS lastPass
		WITH t, coalesce(lastPass, last) AS run
		MATCH (t)<-[:OF]-(r:Result {runId: run})
		WHERE EXISTS { MATCH `+match+` }
		RETURN t.key AS test, t.caseId AS caseId, run ORDER BY test`, map[string]any{"q": q})
	if err != nil {
		return nil, err
	}
	out := []ImpactedTest{}
	for _, rec := range res.Records {
		p := rec.AsMap()
		out = append(out, ImpactedTest{TestKey: str(p, "test"), CaseID: str(p, "caseId"), RunID: i64(p, "run")})
	}
	return out, nil
}
