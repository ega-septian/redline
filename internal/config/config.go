// Package config membaca konfigurasi dari environment variable (dan file .env kalau ada).
package config

import (
	"bufio"
	"fmt"
	"math"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"
)

type Config struct {
	// Host: interface tempat server mendengarkan. Default 127.0.0.1 (hanya laptop sendiri).
	// Host non-loopback mensyaratkan REDLINE_API_KEY, supaya server tidak terbuka tanpa autentikasi.
	Host string
	Port string
	// MaxReportMB membatasi ukuran results.json yang boleh diupload.
	MaxReportMB int64
	// RetentionDays: detail hasil test lebih tua dari ini dihapus (yang dibutuhkan analisis tetap). 0 = mati.
	RetentionDays int
	// StatusSources: source run yang boleh mengubah status kelompok bersama. Kosong = semua.
	StatusSources []string
	// Timezone untuk semua waktu yang dikeluarkan server (JSON dan log).
	Timezone string
	// APIKey: kalau diisi, setiap request /api/... wajib membawa "Authorization: Bearer <key>". Kosong = terbuka
	// (cocok untuk server di laptop sendiri). Wajib diisi kalau server bisa dijangkau orang lain.
	APIKey string
	// Users: nama -> key untuk login UI dan audit label (REDLINE_USERS=ega:key,budi:key).
	// Key lama (REDLINE_API_KEY) tetap diterima sebagai user "admin".
	Users map[string]string

	// Analisis AI. Kosongkan ANTHROPIC_API_KEY untuk mematikan AI (aturan tetap jalan).
	AnthropicAPIKey  string
	AnthropicModel   string
	AnthropicBaseURL string
	// AnthropicInputPrice/OutputPrice: harga USD per 1 juta token, untuk model yang tidak ada di
	// daftar bawaan. Keduanya harus diisi bersama; 0 = pakai harga bawaan.
	AnthropicInputPrice  float64
	AnthropicOutputPrice float64
	// AIDailyBudgetUSD: batas biaya analisis AI per hari (0 = tanpa batas). Kalau tercapai, aturan
	// tetap dipakai tetapi AI tidak dipanggil.
	AIDailyBudgetUSD float64

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

// userName adalah bentuk nama yang aman untuk audit log dan payload cookie.
var userName = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

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
	host := getenv("HOST", "127.0.0.1")
	apiKey := os.Getenv("REDLINE_API_KEY")
	users, err := parseUsers(os.Getenv("REDLINE_USERS"))
	if err != nil {
		return Config{}, err
	}
	if !loopbackHost(host) && apiKey == "" && len(users) == 0 {
		return Config{}, fmt.Errorf("HOST %q bisa dijangkau dari jaringan, tetapi tidak ada key: isi REDLINE_API_KEY atau REDLINE_USERS, atau pakai HOST=127.0.0.1", host)
	}
	inputPrice, err := priceEnv("ANTHROPIC_INPUT_PRICE")
	if err != nil {
		return Config{}, err
	}
	outputPrice, err := priceEnv("ANTHROPIC_OUTPUT_PRICE")
	if err != nil {
		return Config{}, err
	}
	if (inputPrice == 0) != (outputPrice == 0) {
		return Config{}, fmt.Errorf("ANTHROPIC_INPUT_PRICE dan ANTHROPIC_OUTPUT_PRICE harus diisi berdua (USD per 1 juta token)")
	}
	budget, err := priceEnv("AI_DAILY_BUDGET_USD")
	if err != nil {
		return Config{}, err
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
		Host:          host,
		Port:          getenv("PORT", "8787"),
		RetentionDays: retention,
		StatusSources: sources,
		Timezone:      getenv("TIMEZONE", "Asia/Jakarta"),
		MaxReportMB:   maxMB,
		APIKey:        apiKey,
		Users:         users,

		AnthropicAPIKey:  os.Getenv("ANTHROPIC_API_KEY"),
		AnthropicModel:   getenv("ANTHROPIC_MODEL", "claude-haiku-5-5"),
		AnthropicBaseURL: getenv("ANTHROPIC_BASE_URL", "https://api.anthropic.com"),

		AnthropicInputPrice:  inputPrice,
		AnthropicOutputPrice: outputPrice,
		AIDailyBudgetUSD:     budget,

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

// loopbackHost: true untuk localhost dan alamat loopback. Nama host atau alamat lain dianggap bisa
// dijangkau dari jaringan, jadi server mensyaratkan API key.
func loopbackHost(host string) bool {
	if host == "" {
		return true
	}
	h := strings.ToLower(strings.Trim(host, "[]"))
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// parseUsers membaca REDLINE_USERS dengan format "nama:key,nama2:key2".
func parseUsers(raw string) (map[string]string, error) {
	users := map[string]string{}
	if strings.TrimSpace(raw) == "" {
		return users, nil
	}
	for _, pair := range strings.Split(raw, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		name, key, ok := strings.Cut(pair, ":")
		if !ok {
			return nil, fmt.Errorf("REDLINE_USERS: %q harus format nama:key", pair)
		}
		name, key = strings.TrimSpace(name), strings.TrimSpace(key)
		if !userName.MatchString(name) {
			return nil, fmt.Errorf("REDLINE_USERS: nama %q tidak valid (huruf, angka, titik, garis bawah, tanda hubung; maksimal 64)", name)
		}
		if key == "" {
			return nil, fmt.Errorf("REDLINE_USERS: key untuk %q kosong", name)
		}
		if _, exists := users[name]; exists {
			return nil, fmt.Errorf("REDLINE_USERS: nama %q dipakai dua kali", name)
		}
		users[name] = key
	}
	return users, nil
}

// priceEnv membaca angka non-negatif (harga USD atau batas biaya). Kosong = 0.
func priceEnv(key string) (float64, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return 0, nil
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, fmt.Errorf("%s harus angka >= 0, dapat %q", key, raw)
	}
	return v, nil
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
