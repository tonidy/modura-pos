// Command sales-api serves the Modura POS API and, optionally, the POS UI.
//
// Usage:
//
//	sales-api [serve]     apply pending migrations, seed an empty catalog, serve
//	sales-api migrate     apply pending migrations and seed an empty catalog, then exit
//	sales-api reset-demo  wipe all data and reload the demo seed, then exit
//
// Environment (see docs/deploy.md):
//
//	DATABASE_URL             Postgres connection string (required)
//	PORT                     listen port, default 8080
//	MODURA_DB_DIR            directory with migrations/ and seed/, default ../../db
//	MODURA_WEB_DIR           built POS UI to serve at /, default none
//	MODURA_CSP_CONNECT_SRC   other origins the UI may call, space-separated (e.g. the reports service)
//	MODURA_CASHIER_TOKEN     demo cashier bearer token, default "demo-cashier"
//	MODURA_VIEWER_TOKEN      demo viewer bearer token, default "demo-viewer"
//	MODURA_CLIENT_IP_HEADER  trusted proxy header with the client IP, e.g. Fly-Client-IP
//	MODURA_CHECKOUTS_PER_MIN per-IP command rate limit, default 30 (0 disables)
//	MODURA_DEMO_MODE         "true" enables stock replenishment and the nightly reset
//	MODURA_DEMO_RESET_AT     UTC time of the nightly reset, default 03:00
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/time/rate"

	"modura/apps/sales-go/internal/dbmigrate"
	"modura/apps/sales-go/internal/demo"
	"modura/apps/sales-go/internal/httpapi"
	"modura/apps/sales-go/internal/store"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	command := "serve"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}
	if err := run(command, logger); err != nil {
		logger.Error("sales-api failed", "command", command, "error", err)
		os.Exit(1)
	}
}

func run(command string, logger *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return errors.New("DATABASE_URL is required")
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	dbDir := env("MODURA_DB_DIR", "../../db")

	switch command {
	case "migrate":
		return migrate(ctx, pool, dbDir, logger)
	case "reset-demo":
		if err := migrate(ctx, pool, dbDir, logger); err != nil {
			return err
		}
		if err := dbmigrate.ResetDemo(ctx, pool, dbDir); err != nil {
			return err
		}
		logger.Info("demo data reset")
		return nil
	case "serve":
		if err := migrate(ctx, pool, dbDir, logger); err != nil {
			return err
		}
		return serve(ctx, pool, dbDir, logger)
	default:
		return fmt.Errorf("unknown command %q (want serve, migrate or reset-demo)", command)
	}
}

func migrate(ctx context.Context, pool *pgxpool.Pool, dbDir string, logger *slog.Logger) error {
	applied, err := dbmigrate.Apply(ctx, pool, dbDir)
	if err != nil {
		return err
	}
	seeded, err := dbmigrate.Seed(ctx, pool, dbDir)
	if err != nil {
		return err
	}
	logger.Info("database ready", "applied", applied, "seeded", seeded)
	return nil
}

func serve(ctx context.Context, pool *pgxpool.Pool, dbDir string, logger *slog.Logger) error {
	perMinute, err := strconv.Atoi(env("MODURA_CHECKOUTS_PER_MIN", "30"))
	if err != nil {
		return fmt.Errorf("MODURA_CHECKOUTS_PER_MIN: %w", err)
	}
	st := store.New(pool, store.Options{})

	cfg := httpapi.Config{
		Store: st,
		Identities: httpapi.DemoIdentities(
			env("MODURA_CASHIER_TOKEN", "demo-cashier"),
			env("MODURA_VIEWER_TOKEN", "demo-viewer"),
		),
		Logger:           logger,
		ClientIPHeader:   os.Getenv("MODURA_CLIENT_IP_HEADER"),
		CommandRateLimit: rate.Limit(float64(perMinute) / 60),
		CommandBurst:     max(5, perMinute/3),
		WebDir:           os.Getenv("MODURA_WEB_DIR"),
		ConnectSrc:       strings.Fields(os.Getenv("MODURA_CSP_CONNECT_SRC")),
	}

	if os.Getenv("MODURA_DEMO_MODE") == "true" {
		jobs := demo.New(demo.Config{
			DB:             pool,
			Store:          st,
			DBDir:          dbDir,
			ReplenishEvery: 30 * time.Second,
			ResetAt:        env("MODURA_DEMO_RESET_AT", "03:00"),
			Logger:         logger,
		})
		cfg.DemoStatus = func() any { return jobs.Status() }
		go func() {
			if err := jobs.Run(ctx); err != nil {
				logger.Error("demo jobs stopped", "error", err)
			}
		}()
	}

	handler, err := httpapi.New(cfg)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Addr:              ":" + env("PORT", "8080"),
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	errs := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", srv.Addr, "web", cfg.WebDir != "", "demoMode", cfg.DemoStatus != nil)
		errs <- srv.ListenAndServe()
	}()

	select {
	case err := <-errs:
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
