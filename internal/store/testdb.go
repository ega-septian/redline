package store

import (
	"context"
	"os"
	"testing"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// TestStore membuka database Neo4j khusus test bernama name, yang DIKOSONGKAN dulu (CREATE OR REPLACE),
// lalu menjalankan migrasi. Tiap package test memakai nama sendiri, jadi `go test ./...` yang paralel
// tidak saling menghapus data. Dilewati kalau REDLINE_TEST_NEO4J_URI tidak diset.
func TestStore(ctx context.Context, t testing.TB, name string) *Store {
	t.Helper()
	uri := os.Getenv("REDLINE_TEST_NEO4J_URI")
	if uri == "" {
		t.Skip("REDLINE_TEST_NEO4J_URI tidak diset; test Neo4j dilewati")
	}
	password := os.Getenv("REDLINE_TEST_NEO4J_PASSWORD")
	if password == "" {
		password = "redline-local"
	}
	driver, err := neo4j.NewDriverWithContext(uri, neo4j.BasicAuth("neo4j", password, ""))
	if err != nil {
		t.Fatal(err)
	}
	if err := createDatabase(ctx, driver, name, true); err != nil {
		t.Fatal(err)
	}
	driver.Close(ctx)

	s, err := Open(ctx, Config{URI: uri, Username: "neo4j", Password: password, Database: name})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close(context.Background()) })
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return s
}
