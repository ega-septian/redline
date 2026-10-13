// Package graphstore adalah lapisan database Redline di Neo4j (DozerDB).
//
// Sedang dibangun di branch feature/neo4j untuk menggantikan internal/store (Postgres). Sampai fiturnya
// setara (diuji dengan benchmark yang sama), server tetap menyimpan data di Postgres.
package graphstore

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

//go:embed migrations/*.cypher
var migrationFS embed.FS

// Config adalah alamat dan kredensial Neo4j. Database dibuat otomatis kalau belum ada (butuh DozerDB atau
// Neo4j Enterprise; di Neo4j Community hanya database "neo4j" yang bisa dipakai).
type Config struct {
	URI      string // misalnya bolt://localhost:7688
	Username string
	Password string
	Database string // misalnya redline
}

type Store struct {
	driver   neo4j.DriverWithContext
	database string
}

// Nama database Neo4j: huruf kecil, angka, titik, dan tanda hubung; diawali huruf; 3-63 karakter.
var databaseName = regexp.MustCompile(`^[a-z][a-z0-9.-]{2,62}$`)

// Open terhubung ke Neo4j dan memastikan database-nya ada dan online.
func Open(ctx context.Context, cfg Config) (*Store, error) {
	if !databaseName.MatchString(cfg.Database) {
		return nil, fmt.Errorf("nama database Neo4j %q tidak valid: huruf kecil, angka, titik, dan tanda hubung, diawali huruf", cfg.Database)
	}
	driver, err := neo4j.NewDriverWithContext(cfg.URI, neo4j.BasicAuth(cfg.Username, cfg.Password, ""))
	if err != nil {
		return nil, fmt.Errorf("driver Neo4j: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := driver.VerifyConnectivity(pingCtx); err != nil {
		driver.Close(ctx)
		return nil, fmt.Errorf("koneksi Neo4j %s (sudah jalankan `docker compose up -d neo4j`?): %w", cfg.URI, err)
	}
	if err := createDatabase(ctx, driver, cfg.Database, false); err != nil {
		driver.Close(ctx)
		return nil, err
	}
	return &Store{driver: driver, database: cfg.Database}, nil
}

func (s *Store) Close(ctx context.Context) error { return s.driver.Close(ctx) }

// createDatabase membuat database (replace = kosongkan dulu kalau sudah ada), lalu menunggu sampai online.
// Database "neo4j" bawaan selalu ada, jadi tidak dibuat.
func createDatabase(ctx context.Context, driver neo4j.DriverWithContext, name string, replace bool) error {
	if name == "neo4j" && !replace {
		return nil
	}
	// DozerDB menolak CREATE OR REPLACE untuk database yang belum ada, jadi dikosongkan dengan DROP + CREATE.
	stmts := []string{"CREATE DATABASE `" + name + "` IF NOT EXISTS"}
	if replace {
		stmts = append([]string{"DROP DATABASE `" + name + "` IF EXISTS"}, stmts...)
	}
	for _, stmt := range stmts {
		if _, err := neo4j.ExecuteQuery(ctx, driver, stmt, nil, neo4j.EagerResultTransformer,
			neo4j.ExecuteQueryWithDatabase("system")); err != nil {
			return fmt.Errorf("buat database Neo4j %s (butuh DozerDB untuk banyak database): %w", name, err)
		}
	}
	deadline := time.Now().Add(60 * time.Second)
	for {
		res, err := neo4j.ExecuteQuery(ctx, driver, "SHOW DATABASE `"+name+"` YIELD currentStatus RETURN currentStatus",
			nil, neo4j.EagerResultTransformer, neo4j.ExecuteQueryWithDatabase("system"))
		if err != nil {
			return fmt.Errorf("status database Neo4j %s: %w", name, err)
		}
		if len(res.Records) > 0 {
			if status, _ := res.Records[0].Get("currentStatus"); status == "online" {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("database Neo4j %s tidak online dalam 60 detik", name)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// query menjalankan satu query di database Redline dan mengembalikan semua baris.
func (s *Store) query(ctx context.Context, cypher string, params map[string]any) (*neo4j.EagerResult, error) {
	return neo4j.ExecuteQuery(ctx, s.driver, cypher, params, neo4j.EagerResultTransformer,
		neo4j.ExecuteQueryWithDatabase(s.database))
}

// Migrate menjalankan file migrations/*.cypher yang belum pernah dijalankan, berurutan, lalu mencatatnya
// sebagai node :SchemaMigration (pengganti tabel schema_migrations di Postgres). Satu statement per baris
// yang diakhiri titik koma; baris yang diawali // adalah komentar.
func (s *Store) Migrate(ctx context.Context) error {
	files, err := fs.Glob(migrationFS, "migrations/*.cypher")
	if err != nil {
		return err
	}
	sort.Strings(files)
	for _, file := range files {
		version := strings.TrimSuffix(strings.TrimPrefix(file, "migrations/"), ".cypher")
		res, err := s.query(ctx, `MATCH (m:SchemaMigration {version: $v}) RETURN count(m) AS n`, map[string]any{"v": version})
		if err != nil {
			return fmt.Errorf("cek migrasi %s: %w", version, err)
		}
		if n, _ := res.Records[0].Get("n"); n.(int64) > 0 {
			continue
		}
		body, err := migrationFS.ReadFile(file)
		if err != nil {
			return err
		}
		// Perintah schema (constraint, index) tidak boleh dicampur dengan penulisan data dalam satu
		// transaksi, jadi setiap statement dijalankan sendiri. Semuanya memakai IF NOT EXISTS, aman diulang.
		for _, stmt := range statements(string(body)) {
			if _, err := s.query(ctx, stmt, nil); err != nil {
				return fmt.Errorf("migrasi %s: %w\n%s", version, err, stmt)
			}
		}
		if _, err := s.query(ctx, `MERGE (m:SchemaMigration {version: $v}) ON CREATE SET m.appliedAt = datetime()`,
			map[string]any{"v": version}); err != nil {
			return fmt.Errorf("catat migrasi %s: %w", version, err)
		}
	}
	return nil
}

// statements memecah isi file migrasi menjadi statement Cypher.
func statements(body string) []string {
	var out []string
	var cur []string
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") {
			continue
		}
		cur = append(cur, line)
		if strings.HasSuffix(trimmed, ";") {
			stmt := strings.TrimSuffix(strings.TrimSpace(strings.Join(cur, "\n")), ";")
			out = append(out, stmt)
			cur = nil
		}
	}
	if len(cur) > 0 {
		out = append(out, strings.TrimSpace(strings.Join(cur, "\n")))
	}
	return out
}

// ErrNotFound dikembalikan kalau data yang diminta tidak ada.
var ErrNotFound = errors.New("not found")

// nextID mengambil nomor berikutnya untuk satu jenis (misalnya "run"), pengganti BIGSERIAL di Postgres.
// Harus dipanggil di dalam transaksi tulis: MERGE + SET mengunci node :Counter, jadi dua run yang masuk
// bersamaan tidak mendapat nomor yang sama.
func nextID(ctx context.Context, tx neo4j.ManagedTransaction, name string) (int64, error) {
	res, err := tx.Run(ctx, `
		MERGE (c:Counter {name: $name}) ON CREATE SET c.value = 0
		SET c.value = c.value + 1
		RETURN c.value AS id`, map[string]any{"name": name})
	if err != nil {
		return 0, err
	}
	rec, err := res.Single(ctx)
	if err != nil {
		return 0, err
	}
	id, _ := rec.Get("id")
	return id.(int64), nil
}

// write menjalankan fungsi di dalam satu transaksi tulis (di-retry otomatis oleh driver kalau sementara gagal).
func (s *Store) write(ctx context.Context, fn func(tx neo4j.ManagedTransaction) (any, error)) (any, error) {
	session := s.driver.NewSession(ctx, neo4j.SessionConfig{DatabaseName: s.database, AccessMode: neo4j.AccessModeWrite})
	defer session.Close(ctx)
	return session.ExecuteWrite(ctx, fn)
}
