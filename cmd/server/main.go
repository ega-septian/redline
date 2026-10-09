// Command server menjalankan API Redline: menerima results.json dari Playwright,
// mengelompokkan kegagalan, dan melacak statusnya (open, resolved, regressed).
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // data zona waktu ikut di binary, untuk container yang tidak punya /usr/share/zoneinfo

	"redline/internal/analysis"
	"redline/internal/api"
	"redline/internal/config"
	"redline/internal/contract"
	"redline/internal/embed"
	"redline/internal/llm"
	"redline/internal/store"
)

// pricing mengembalikan harga per 1 juta token. Model yang tidak dikenal dihitung 0
// (token tetap dicatat, hanya biayanya yang tidak).
func pricing(model string) llm.Pricing {
	switch {
	case strings.HasPrefix(model, "claude-haiku-5-5"):
		return llm.Pricing{InputPerMTok: 0.10, OutputPerMTok: 0.50}
	default:
		return llm.Pricing{}
	}
}

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)
	if err := run(log); err != nil {
		log.Error("server berhenti", "err", err)
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
		return fmt.Errorf("TIMEZONE %q tidak dikenal: %w", cfg.Timezone, err)
	}
	// Semua time.Time dari database dan log memakai zona ini, termasuk di server yang jam sistemnya UTC.
	time.Local = loc
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	dbURL, err := store.WithTimezone(cfg.DatabaseURL, cfg.Timezone)
	if err != nil {
		return err
	}
	if cfg.DatabaseSchema != "" {
		if dbURL, err = store.PrepareSchema(ctx, dbURL, cfg.DatabaseSchema, cfg.DatabaseSchemaReset); err != nil {
			return err
		}
		log.Info("memakai schema database terpisah", "schema", cfg.DatabaseSchema, "reset", cfg.DatabaseSchemaReset)
	}
	db, err := store.Open(ctx, dbURL)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		return err
	}
	if err := db.SetDatabaseTimezone(ctx, cfg.Timezone); err != nil {
		return err
	}
	db.StatusSources = cfg.StatusSources
	if len(cfg.StatusSources) == 0 {
		log.Info("semua run mengubah status kelompok (STATUS_FROM=all)")
	} else {
		log.Info("hanya run ini yang mengubah status kelompok; run lain hanya pratinjau", "source", cfg.StatusSources)
	}

	if cfg.RetentionDays > 0 {
		go pruneLoop(ctx, db, cfg.RetentionDays, log)
	} else {
		log.Info("retensi mati (RETENTION_DAYS=0); semua detail test disimpan")
	}

	analyzer := &analysis.Analyzer{Store: db, Model: cfg.AnthropicModel, Pricing: pricing(cfg.AnthropicModel), Log: log}
	if cfg.AnthropicAPIKey != "" {
		analyzer.LLM = llm.NewClient(cfg.AnthropicAPIKey, cfg.AnthropicBaseURL)
		log.Info("analisis AI aktif", "model", cfg.AnthropicModel)
	} else {
		log.Info("analisis AI mati (ANTHROPIC_API_KEY kosong); hanya aturan deterministik yang jalan")
	}
	if cfg.OpenAPISpecs != "" {
		specs, err := contract.ParseSpecs(cfg.OpenAPISpecs)
		if err != nil {
			return err
		}
		analyzer.Contracts = contract.NewRegistry(specs)
		log.Info("kontrak API aktif", "project", strings.Join(slices.Sorted(maps.Keys(specs)), ","))
	}
	if cfg.VoyageAPIKey != "" {
		analyzer.Embed = embed.NewVoyage(cfg.VoyageAPIKey, cfg.VoyageModel, cfg.VoyageBaseURL)
		analyzer.EmbedModel = cfg.VoyageModel
		log.Info("pencarian kasus mirip aktif", "model", cfg.VoyageModel)
	} else {
		log.Info("pencarian kasus mirip mati (VOYAGE_API_KEY kosong)")
	}

	srv := &api.Server{Store: db, Analyzer: analyzer, Log: log, MaxBodySize: cfg.MaxReportMB << 20}
	httpSrv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		log.Info("Redline jalan", "url", "http://localhost:"+cfg.Port)
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

// pruneLoop menjalankan retensi saat start lalu setiap 24 jam.
func pruneLoop(ctx context.Context, db *store.Store, days int, log *slog.Logger) {
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for {
		before := time.Now().AddDate(0, 0, -days)
		if res, err := db.Prune(ctx, before); err != nil {
			log.Error("retensi gagal", "err", err)
		} else if res.TestResults+res.Shapes+res.Sources > 0 {
			log.Info("retensi selesai", "hari", days, "test_results", res.TestResults, "response_shapes", res.Shapes,
				"source_files", res.Sources)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
