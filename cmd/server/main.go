// Command server menjalankan API Redline: menerima results.json dari Playwright,
// mengelompokkan kegagalan, dan melacak statusnya (open, resolved, regressed).
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"slices"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // data zona waktu ikut di binary, untuk container yang tidak punya /usr/share/zoneinfo

	"redline/internal/analysis"
	"redline/internal/api"
	"redline/internal/auth"
	"redline/internal/config"
	"redline/internal/contract"
	"redline/internal/embed"
	"redline/internal/llm"
	"redline/internal/notify"
	"redline/internal/reportportal"
	"redline/internal/store"
	"redline/internal/ui"
	"redline/web"
)

// pricing mengembalikan harga per 1 juta token. Model yang tidak dikenal dihitung 0
// (token tetap dicatat, hanya biayanya yang tidak).
func sourcesProjects(sources []store.ContractSource) []string {
	out := make([]string, 0, len(sources))
	for _, source := range sources {
		out = append(out, source.Project)
	}
	slices.Sort(out)
	return out
}

func pricing(model string) llm.Pricing {
	switch {
	case strings.HasPrefix(model, "claude-haiku-5-5"):
		return llm.Pricing{InputPerMTok: 0.10, OutputPerMTok: 0.50}
	default:
		return llm.Pricing{}
	}
}

// versionOverride lets release builds inject a tag with -ldflags.
var versionOverride string

// buildVersion: versi modul dari build info, atau commit pendek saat build lokal (`go run`).
func buildVersion() string {
	if versionOverride != "" {
		return versionOverride
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	if v := info.Main.Version; v != "" && v != "(devel)" {
		return v
	}
	rev, dirty := "", false
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if rev == "" {
		return "dev"
	}
	if len(rev) > 7 {
		rev = rev[:7]
	}
	if dirty {
		rev += "-dirty"
	}
	return rev
}

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)
	if err := run(log); err != nil {
		log.Error("server stopped", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		return fmt.Errorf("unknown TIMEZONE %q: %w", cfg.Timezone, err)
	}
	// Semua time.Time dari database dan log memakai zona ini, termasuk di server yang jam sistemnya UTC.
	time.Local = loc
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, closeDB, err := openStore(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer closeDB()
	if len(cfg.StatusSources) == 0 {
		log.Info("all runs may change group status (STATUS_FROM=all)")
	} else {
		log.Info("only this run may change group status; other runs are previews", "source", cfg.StatusSources)
	}

	if cfg.RetentionDays > 0 {
		go pruneLoop(ctx, db, cfg.RetentionDays, log)
	} else {
		log.Info("retention disabled (RETENTION_DAYS=0); all test details are kept")
	}

	analyzer := &analysis.Analyzer{Store: db, Model: cfg.AnthropicModel, Log: log,
		AIMode: cfg.AIMode, MaxAIPerRun: cfg.AIMaxPerRun}
	price, knownPrice := llm.PricingFor(cfg.AnthropicModel)
	if cfg.AnthropicInputPrice > 0 || cfg.AnthropicOutputPrice > 0 {
		price = llm.Pricing{InputPerMTok: cfg.AnthropicInputPrice, OutputPerMTok: cfg.AnthropicOutputPrice}
		knownPrice = true
	}
	analyzer.Pricing = price
	if cfg.AIDailyBudgetUSD > 0 {
		analyzer.DailyBudgetUSD = cfg.AIDailyBudgetUSD
		log.Info("daily AI budget enabled", "usd", cfg.AIDailyBudgetUSD)
	}
	if cfg.AnthropicAPIKey != "" && !knownPrice {
		log.Warn("unknown model price; AI cost is recorded as 0; set ANTHROPIC_INPUT_PRICE and ANTHROPIC_OUTPUT_PRICE",
			"model", cfg.AnthropicModel)
	}
	if cfg.AnthropicAPIKey != "" {
		analyzer.LLM = llm.NewClient(cfg.AnthropicAPIKey, cfg.AnthropicBaseURL)
		log.Info("AI analysis enabled", "model", cfg.AnthropicModel, "mode", cfg.AIMode, "max_per_run", cfg.AIMaxPerRun)
	} else {
		log.Info("AI analysis disabled (ANTHROPIC_API_KEY is empty); only deterministic rules run")
	}
	// Contract sources live in the database and are managed from the Config page; no environment override.
	registry := contract.NewRegistry(nil)
	analyzer.Contracts = registry
	if sources, err := db.ContractSources(ctx); err != nil {
		log.Warn("failed to read contract sources; contract analysis and Coverage are disabled until the database is readable", "err", err)
	} else if len(sources) == 0 {
		log.Warn("no contract source yet; contract analysis and Coverage are disabled until a source is added in Config")
	} else {
		for _, source := range sources {
			if source.Enabled && source.URL != "" {
				registry.Set(source.Project, source.URL)
			} else {
				registry.Delete(source.Project)
			}
		}
		log.Info("contract sources from the database enabled", "project", strings.Join(sourcesProjects(sources), ","))
	}
	if os.Getenv("OPENAPI_SPECS") != "" {
		log.Warn("OPENAPI_SPECS is ignored; configure the contract source in Config")
	}
	if cfg.VoyageAPIKey != "" {
		analyzer.Embed = embed.NewVoyage(cfg.VoyageAPIKey, cfg.VoyageModel, cfg.VoyageBaseURL)
		analyzer.EmbedModel = cfg.VoyageModel
		log.Info("similar-case search enabled", "model", cfg.VoyageModel)
	} else {
		log.Info("similar-case search disabled (VOYAGE_API_KEY is empty)")
	}

	srv := &api.Server{Store: db, Analyzer: analyzer, Log: log, MaxBodySize: cfg.MaxReportMB << 20, APIKey: cfg.APIKey,
		ArtifactDir: cfg.ArtifactDir, MaxArtifactSize: cfg.MaxArtifactMB << 20, Version: buildVersion(),
		Contracts: registry, Notifier: notify.NewDiscord()}
	srv.AI = &api.AISettings{
		Enabled: analyzer.LLM != nil, Mode: cfg.AIMode, Model: cfg.AnthropicModel,
		DailyBudgetUSD: cfg.AIDailyBudgetUSD, MaxPerRun: cfg.AIMaxPerRun,
	}
	authSecret, err := db.AuthSecret(ctx)
	if err != nil {
		return fmt.Errorf("session secret: %w", err)
	}
	sessions := auth.NewWithSecret(cfg.APIKey, cfg.Users, authSecret)
	// The UI always requires a login (first run redirects to registration); /api stays open on a
	// laptop with no keys, matching the documented behaviour.
	srv.AuthEnabled = cfg.APIKey != "" || len(cfg.Users) > 0
	srv.ResolveKey = func(ctx context.Context, token string) (string, bool) {
		if name, ok := sessions.CheckKey(token); ok {
			return name, true
		}
		if token == "" {
			return "", false
		}
		name, err := db.APIKeyName(ctx, auth.HashToken(token))
		return name, err == nil
	}
	srv.User = func(r *http.Request) string {
		if user := sessions.User(r); user != "" {
			return user
		}
		return api.KeyName(r)
	}
	srv.Session = sessions.Valid
	srv.CSRF = sessions.CSRFValid
	// /readyz memeriksa database; /healthz tetap liveness saja.
	srv.Ready = db.Ping
	apiKeyCount, err := db.APIKeyCount(ctx)
	if err != nil {
		return fmt.Errorf("baca API key: %w", err)
	}
	if apiKeyCount > 0 {
		srv.EnableKeyAuth()
	}
	switch {
	case cfg.APIKey != "" || len(cfg.Users) > 0:
		log.Info("API key from the environment enabled: /api/... requests must send a Bearer key")
	case apiKeyCount > 0:
		log.Info("API keys from Config enabled: /api/... requests must send a Bearer key", "keys", apiKeyCount)
	default:
		log.Info("API is open without a key; create an API key in Config to enable Bearer auth")
	}
	adminCount, err := db.UserCount(ctx)
	if err != nil {
		return fmt.Errorf("read UI admin: %w", err)
	}
	if adminCount == 0 {
		log.Warn("no UI admin yet; open /ui/register to create the first admin account")
	} else {
		log.Info("UI admin exists", "users", adminCount)
	}
	if cfg.ReportPortalURL != "" {
		srv.ReportPortal = &reportportal.Syncer{
			Client:   &reportportal.Client{BaseURL: cfg.ReportPortalURL, Project: cfg.ReportPortalProject, APIKey: cfg.ReportPortalAPIKey},
			Store:    db,
			Analyzer: analyzer,
			Log:      log,
		}
		log.Info("ReportPortal integration enabled", "url", cfg.ReportPortalURL, "project", cfg.ReportPortalProject)
	}
	root := http.NewServeMux()
	root.Handle("/ui", http.RedirectHandler("/ui/", http.StatusMovedPermanently))
	root.Handle("/ui/", http.StripPrefix("/ui", ui.New(web.Dist(), log, sessions, ui.WithAccounts(db))))
	root.Handle("/", srv.Routes())

	httpSrv := &http.Server{
		Addr:              net.JoinHostPort(cfg.Host, cfg.Port),
		Handler:           root,
		ReadHeaderTimeout: 10 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		log.Info("Redline is running", "url", "http://"+httpSrv.Addr)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return httpSrv.Shutdown(shutdownCtx)
}

// openStore membuka database Neo4j, menjalankan migrasi, dan mengembalikan fungsi penutupnya.
func openStore(ctx context.Context, cfg config.Config, log *slog.Logger) (*store.Store, func(), error) {
	db, err := store.Open(ctx, store.Config{URI: cfg.Neo4jURI, Username: cfg.Neo4jUsername,
		Password: cfg.Neo4jPassword, Database: cfg.Neo4jDatabase, Reset: cfg.Neo4jDatabaseReset})
	if err != nil {
		return nil, nil, err
	}
	closeDB := func() { db.Close(context.Background()) }
	if err := db.Migrate(ctx); err != nil {
		closeDB()
		return nil, nil, err
	}
	db.StatusSources = cfg.StatusSources
	log.Info("database Neo4j", "uri", cfg.Neo4jURI, "database", cfg.Neo4jDatabase, "reset", cfg.Neo4jDatabaseReset)
	return db, closeDB, nil
}

// pruneLoop menjalankan retensi saat start lalu setiap 24 jam.
func pruneLoop(ctx context.Context, db *store.Store, days int, log *slog.Logger) {
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for {
		before := time.Now().AddDate(0, 0, -days)
		if res, err := db.Prune(ctx, before); err != nil {
			log.Error("retention failed", "err", err)
		} else if res.TestResults+res.Shapes+res.Sources+res.Artifacts > 0 {
			for _, path := range res.ArtifactPaths {
				if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
					log.Warn("failed to delete artifact file", "path", path, "err", err)
				}
			}
			log.Info("retention finished", "days", days, "test_results", res.TestResults, "response_shapes", res.Shapes,
				"source_files", res.Sources, "artifacts", res.Artifacts)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
