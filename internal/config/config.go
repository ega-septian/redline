// Package config membaca konfigurasi dari environment variable (dan file .env kalau ada).
package config

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Port string
	// MaxReportMB membatasi ukuran results.json yang boleh diupload.
	MaxReportMB int64
	// RetentionDays: detail hasil test lebih tua dari ini dihapus (yang dibutuhkan analisis tetap). 0 = mati.
	RetentionDays int
	// StatusSources: source run yang boleh mengubah status kelompok bersama. Kosong = semua.
	StatusSources []string
	// Timezone untuk semua waktu yang dikeluarkan server (JSON dan log).
	Timezone string

	// Analisis AI. Kosongkan ANTHROPIC_API_KEY untuk mematikan AI (aturan tetap jalan).
	AnthropicAPIKey  string
	AnthropicModel   string
	AnthropicBaseURL string

	// Database: Neo4j (DozerDB). Default cocok dengan docker-compose.yml.
	Neo4jURI      string
	Neo4jUsername string
	Neo4jPassword string
	Neo4jDatabase string
	// Neo4jDatabaseReset MENGHAPUS isi database Neo4j itu saat server start (untuk benchmark).
	Neo4jDatabaseReset bool

	// Kontrak API per project Playwright: "toolshop=http://localhost:8091/docs,gorest=https://...". Kosong = tanpa kontrak.
	OpenAPISpecs string

	// ReportPortal: Redline mengisi jenis defect dan komentar untuk test yang masih "To Investigate".
	// Kosongkan REPORTPORTAL_URL untuk mematikannya.
	ReportPortalURL     string
	ReportPortalProject string
	ReportPortalAPIKey  string

	// Embedding untuk mencari kasus mirip. Kosongkan VOYAGE_API_KEY untuk mematikannya.
	VoyageAPIKey  string
	VoyageModel   string
	VoyageBaseURL string
}

// Load membaca file .env (kalau ada) lalu environment variable.
// Environment variable yang sudah diset tidak ditimpa oleh .env.
func Load() (Config, error) {
	if err := LoadDotEnv(".env"); err != nil {
		return Config{}, err
	}
	maxMB, err := strconv.ParseInt(getenv("MAX_REPORT_MB", "50"), 10, 64)
	if err != nil || maxMB <= 0 {
		return Config{}, fmt.Errorf("MAX_REPORT_MB harus angka positif")
	}
	retention, err := strconv.Atoi(getenv("RETENTION_DAYS", "30"))
	if err != nil || retention < 0 {
		return Config{}, fmt.Errorf("RETENTION_DAYS harus angka 0 atau lebih")
	}
	if os.Getenv("REPORTPORTAL_URL") != "" && os.Getenv("REPORTPORTAL_API_KEY") == "" {
		return Config{}, fmt.Errorf("REPORTPORTAL_URL diisi tapi REPORTPORTAL_API_KEY kosong")
	}
	var sources []string
	if from := strings.ToLower(getenv("STATUS_FROM", "ci")); from != "all" {
		for _, s := range strings.Split(from, ",") {
			if s = strings.TrimSpace(s); s != "" {
				sources = append(sources, s)
			}
		}
	}
	return Config{
		Port:          getenv("PORT", "8787"),
		RetentionDays: retention,
		StatusSources: sources,
		Timezone:      getenv("TIMEZONE", "Asia/Jakarta"),
		MaxReportMB:   maxMB,

		AnthropicAPIKey:  os.Getenv("ANTHROPIC_API_KEY"),
		AnthropicModel:   getenv("ANTHROPIC_MODEL", "claude-haiku-5-5"),
		AnthropicBaseURL: getenv("ANTHROPIC_BASE_URL", "https://api.anthropic.com"),

		OpenAPISpecs: os.Getenv("OPENAPI_SPECS"),

		Neo4jURI:      getenv("NEO4J_URI", "bolt://localhost:7688"),
		Neo4jUsername: getenv("NEO4J_USERNAME", "neo4j"),
		Neo4jPassword: getenv("NEO4J_PASSWORD", "redline-local"),
		Neo4jDatabase: getenv("NEO4J_DATABASE", "redline"),

		Neo4jDatabaseReset: os.Getenv("NEO4J_DATABASE_RESET") == "1",

		ReportPortalURL:     os.Getenv("REPORTPORTAL_URL"),
		ReportPortalProject: getenv("REPORTPORTAL_PROJECT", "superadmin_personal"),
		ReportPortalAPIKey:  os.Getenv("REPORTPORTAL_API_KEY"),

		VoyageAPIKey:  os.Getenv("VOYAGE_API_KEY"),
		VoyageModel:   getenv("VOYAGE_MODEL", "voyage-4-lite"),
		VoyageBaseURL: getenv("VOYAGE_BASE_URL", "https://api.voyageai.com"),
	}, nil
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// LoadDotEnv membaca file KEY=VALUE sederhana. File yang tidak ada diabaikan.
func LoadDotEnv(path string) error {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		key, value, ok := parseLine(scanner.Text())
		if !ok {
			continue
		}
		if _, exists := os.LookupEnv(key); !exists {
			os.Setenv(key, value)
		}
	}
	return scanner.Err()
}

func parseLine(line string) (key, value string, ok bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false
	}
	line = strings.TrimPrefix(line, "export ")
	key, value, ok = strings.Cut(line, "=")
	if !ok {
		return "", "", false
	}
	key = strings.TrimSpace(key)
	value = strings.TrimSpace(value)
	quoted := len(value) >= 2 &&
		((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\''))
	if quoted {
		value = value[1 : len(value)-1]
	} else if i := strings.Index(value, " #"); i >= 0 {
		value = strings.TrimSpace(value[:i])
	}
	return key, value, key != ""
}
