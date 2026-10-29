package config

import "testing"

func TestLoad_BindsLoopbackByDefault(t *testing.T) {
	t.Setenv("HOST", "")
	t.Setenv("REDLINE_API_KEY", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Host != "127.0.0.1" {
		t.Errorf("Host default = %q, want 127.0.0.1", cfg.Host)
	}
}

func TestLoad_RefusesPublicBindWithoutAPIKey(t *testing.T) {
	for _, host := range []string{"0.0.0.0", "::", "192.168.1.10", "redline.example.com"} {
		t.Run(host, func(t *testing.T) {
			t.Setenv("HOST", host)
			t.Setenv("REDLINE_API_KEY", "")

			if _, err := Load(); err == nil {
				t.Fatalf("HOST %q tanpa API key harus ditolak", host)
			}
		})
	}
}

func TestLoad_AllowsPublicBindWithAPIKey(t *testing.T) {
	t.Setenv("HOST", "0.0.0.0")
	t.Setenv("REDLINE_API_KEY", "rahasia")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Host != "0.0.0.0" || cfg.APIKey != "rahasia" {
		t.Errorf("Host/APIKey = %q/%q", cfg.Host, cfg.APIKey)
	}
}

func TestLoad_LoopbackAliases(t *testing.T) {
	for _, host := range []string{"localhost", "127.0.0.1", "::1", "[::1]"} {
		t.Run(host, func(t *testing.T) {
			t.Setenv("HOST", host)
			t.Setenv("REDLINE_API_KEY", "")

			if _, err := Load(); err != nil {
				t.Fatalf("HOST %q adalah loopback, tidak boleh butuh API key: %v", host, err)
			}
		})
	}
}
