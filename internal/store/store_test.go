package store

import (
	"context"
	"encoding/base64"
	"io/fs"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"

	"redline/internal/report"
)

func TestStatements(t *testing.T) {
	got := statements("// komentar\nCREATE CONSTRAINT a IF NOT EXISTS\n  FOR (n:A) REQUIRE n.id IS UNIQUE;\n\nCREATE INDEX b IF NOT EXISTS FOR (n:B) ON (n.x);\n")
	if len(got) != 2 || !strings.HasPrefix(got[0], "CREATE CONSTRAINT a") || strings.HasSuffix(got[0], ";") ||
		!strings.Contains(got[0], "REQUIRE n.id") {
		t.Fatalf("statement salah: %q", got)
	}
}

func TestOpen_InvalidName(t *testing.T) {
	for _, name := range []string{"redline_test", "Redline", "1abc", "ab"} {
		if _, err := Open(context.Background(), Config{URI: "bolt://localhost:1", Database: name}); err == nil ||
			!strings.Contains(err.Error(), "tidak valid") {
			t.Errorf("%q harus ditolak, dapat %v", name, err)
		}
	}
}

func TestMigrate(t *testing.T) {
	ctx := context.Background()
	s := TestStore(ctx, t, "redlinetest-store")

	// Migrasi aman dijalankan ulang, dan tercatat sekali.
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	files, _ := fs.Glob(migrationFS, "migrations/*.cypher")
	res, err := s.query(ctx, `MATCH (m:SchemaMigration) RETURN m.version AS v ORDER BY v`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Records) != len(files) {
		t.Fatalf("setiap file migrasi harus tercatat tepat sekali: %d file, %d tercatat", len(files), len(res.Records))
	}
	if v, _ := res.Records[0].Get("v"); v != "001_constraints" {
		t.Fatalf("migrasi pertama salah: %v", v)
	}
	res, err = s.query(ctx, `SHOW CONSTRAINTS YIELD name RETURN collect(name) AS names`, nil)
	if err != nil {
		t.Fatal(err)
	}
	names, _ := res.Records[0].Get("names")
	for _, want := range []string{"run_id", "test_key", "failure_id", "counter_name"} {
		found := false
		for _, n := range names.([]any) {
			found = found || n == want
		}
		if !found {
			t.Errorf("constraint %s tidak ada: %v", want, names)
		}
	}

	// Constraint benar-benar ditegakkan.
	if _, err := s.query(ctx, `CREATE (:Run {id: 1})`, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.query(ctx, `CREATE (:Run {id: 1})`, nil); err == nil {
		t.Errorf("Run dengan id yang sama harus ditolak")
	}
}

func TestNextID_Concurrent(t *testing.T) {
	ctx := context.Background()
	s := TestStore(ctx, t, "redlinetest-store-ids")

	const n = 20
	ids := make(chan int64, n)
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := s.write(ctx, func(tx neo4j.ManagedTransaction) (any, error) { return nextID(ctx, tx, "run") })
			if err != nil {
				t.Error(err)
				return
			}
			ids <- id.(int64)
		}()
	}
	wg.Wait()
	close(ids)
	seen := map[int64]bool{}
	for id := range ids {
		if seen[id] || id < 1 || id > n {
			t.Errorf("id %d dobel atau di luar 1..%d", id, n)
		}
		seen[id] = true
	}
	if len(seen) != n {
		t.Errorf("mau %d id unik, dapat %d", n, len(seen))
	}
}

// Waktu mulai dengan offset zona laptop dibaca Go sebagai zona "Local", yang ditolak Neo4j.
func TestIngest_LocalStartTime(t *testing.T) {
	ctx := context.Background()
	s := TestStore(ctx, t, "redlinetest-graph")
	start := time.Date(2026, 10, 13, 9, 0, 0, 0, time.Local)
	rep := &report.Report{}
	rep.Stats.StartTime = start
	if _, err := s.IngestReport(ctx, rep, RunMeta{}); err != nil {
		t.Fatal(err)
	}
	runs, err := s.ListRuns(ctx, 1)
	if err != nil || len(runs) != 1 || runs[0].StartedAt == nil || !runs[0].StartedAt.Equal(start) {
		t.Fatalf("waktu mulai harus tersimpan apa adanya: %+v %v", runs, err)
	}
}

// Bentuk response disimpan sekali per isi: 10 run x 3 test dengan 2 bentuk berbeda = 2 node :Shape.
func TestIngest_DedupesResponseShapes(t *testing.T) {
	ctx := context.Background()
	s := TestStore(ctx, t, "redlinetest-store")
	spec := func(title, calls string) report.Spec {
		r := report.Result{Status: "passed"}
		if calls != "" {
			r.Attachments = []report.Attachment{{Name: "redline-http", ContentType: "application/json",
				Body: base64.StdEncoding.EncodeToString([]byte(calls))}}
		}
		return report.Spec{Title: title, File: "shop.spec.ts", Line: len(title),
			Tests: []report.Test{{ProjectName: "api", Status: "expected", Results: []report.Result{r}}}}
	}
	ingest := func(specs ...report.Spec) {
		t.Helper()
		rep := &report.Report{Suites: []report.Suite{{Title: "shop.spec.ts", File: "shop.spec.ts", Specs: specs}}}
		if _, err := s.IngestReport(ctx, rep, RunMeta{}); err != nil {
			t.Fatal(err)
		}
	}
	shapes := func() int64 {
		t.Helper()
		res, err := s.query(ctx, `MATCH (x:Shape) RETURN count(x) AS n`, nil)
		if err != nil {
			t.Fatal(err)
		}
		n, _ := res.Records[0].Get("n")
		return n.(int64)
	}
	brands := `[{"method":"GET","path":"/brands","status":200,"shape":[{"id":"number","name":"string"}]}]`
	login := `[{"method":"POST","path":"/users/login","status":200,"shape":{"token":"string"}}]`

	for range 10 {
		ingest(spec("brands", brands), spec("brands lagi", brands), spec("login", login))
	}
	if n := shapes(); n != 2 {
		t.Errorf("mau 2 bentuk unik, dapat %d", n)
	}
	ingest(spec("tanpa fixture", "")) // test tanpa fixture Redline tidak membuat bentuk kosong
	if n := shapes(); n != 2 {
		t.Errorf("calls kosong tidak boleh disimpan, dapat %d bentuk", n)
	}
}
