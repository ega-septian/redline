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

func TestLoad_AnthropicPriceOverrides(t *testing.T) {
	t.Setenv("ANTHROPIC_INPUT_PRICE", "1.5")
	t.Setenv("ANTHROPIC_OUTPUT_PRICE", "2.5")
	t.Setenv("AI_DAILY_BUDGET_USD", "3")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.AnthropicInputPrice != 1.5 || cfg.AnthropicOutputPrice != 2.5 || cfg.AIDailyBudgetUSD != 3 {
		t.Errorf("harga/budget = %v/%v/%v", cfg.AnthropicInputPrice, cfg.AnthropicOutputPrice, cfg.AIDailyBudgetUSD)
	}
}

func TestLoad_AnthropicPriceNeedsBoth(t *testing.T) {
	t.Setenv("ANTHROPIC_INPUT_PRICE", "1.5")
	t.Setenv("ANTHROPIC_OUTPUT_PRICE", "")

	if _, err := Load(); err == nil {
		t.Fatal("hanya satu harga diisi harus ditolak")
	}
}

func TestLoad_RejectsInvalidPrice(t *testing.T) {
	for _, raw := range []string{"-1", "abc", "NaN", "+Inf"} {
		t.Run(raw, func(t *testing.T) {
			t.Setenv("ANTHROPIC_INPUT_PRICE", raw)
			t.Setenv("ANTHROPIC_OUTPUT_PRICE", "1")

			if _, err := Load(); err == nil {
				t.Fatalf("harga %q harus ditolak", raw)
			}
		})
	}
}

func TestLoad_Users(t *testing.T) {
	t.Setenv("REDLINE_USERS", "ega:key-ega, budi:key-budi")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if len(cfg.Users) != 2 || cfg.Users["ega"] != "key-ega" || cfg.Users["budi"] != "key-budi" {
		t.Errorf("users = %v", cfg.Users)
	}
}

func TestLoad_UsersAllowPublicBind(t *testing.T) {
	t.Setenv("HOST", "0.0.0.0")
	t.Setenv("REDLINE_API_KEY", "")
	t.Setenv("REDLINE_USERS", "ega:key-ega")

	if _, err := Load(); err != nil {
		t.Fatalf("users should satisfy the public-bind rule: %v", err)
	}
}

func TestLoad_RejectsInvalidUsers(t *testing.T) {
	cases := []string{
		"ega",             // no key
		"ega:",            // empty key
		"ega:key,ega:key", // duplicate
		"bad name:key",    // invalid name
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			t.Setenv("REDLINE_USERS", raw)
			if _, err := Load(); err == nil {
				t.Fatalf("REDLINE_USERS %q must be rejected", raw)
			}
		})
	}
}

func TestLoad_AIMode(t *testing.T) {
	t.Setenv("AI_MODE", "always")
	t.Setenv("AI_MAX_PER_RUN", "7")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.AIMode != "always" || cfg.AIMaxPerRun != 7 {
		t.Errorf("AI mode/limit = %q/%d", cfg.AIMode, cfg.AIMaxPerRun)
	}
}

func TestLoad_AIModeDefaults(t *testing.T) {
	t.Setenv("AI_MODE", "")
	t.Setenv("AI_MAX_PER_RUN", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.AIMode != "fallback" || cfg.AIMaxPerRun != 20 {
		t.Errorf("AI defaults = %q/%d", cfg.AIMode, cfg.AIMaxPerRun)
	}
}

func TestLoad_RejectsInvalidAIMode(t *testing.T) {
	t.Setenv("AI_MODE", "sometimes")
	if _, err := Load(); err == nil {
		t.Fatal("invalid AI_MODE must be rejected")
	}
}

func TestLoad_RejectsInvalidAIMaxPerRun(t *testing.T) {
	for _, raw := range []string{"-1", "many"} {
		t.Run(raw, func(t *testing.T) {
			t.Setenv("AI_MAX_PER_RUN", raw)
			if _, err := Load(); err == nil {
				t.Fatalf("AI_MAX_PER_RUN=%q must be rejected", raw)
			}
		})
	}
}
