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
	Port        string
	DatabaseURL string
	// MaxReportMB membatasi ukuran results.json yang boleh diupload.
	MaxReportMB int64

	// Analisis AI. Kosongkan ANTHROPIC_API_KEY untuk mematikan AI (aturan tetap jalan).
	AnthropicAPIKey  string
	AnthropicModel   string
	AnthropicBaseURL string
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
	return Config{
		Port:        getenv("PORT", "8787"),
		DatabaseURL: getenv("DATABASE_URL", "postgres://redline:redline@localhost:5433/redline?sslmode=disable"),
		MaxReportMB: maxMB,

		AnthropicAPIKey:  os.Getenv("ANTHROPIC_API_KEY"),
		AnthropicModel:   getenv("ANTHROPIC_MODEL", "claude-haiku-5-5"),
		AnthropicBaseURL: getenv("ANTHROPIC_BASE_URL", "https://api.anthropic.com"),
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
