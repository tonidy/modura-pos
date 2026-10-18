// Package testdb gives each test its own Postgres schema with the real migrations
// and demo seed applied, so tests run in parallel against the real database.
package testdb

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"modura/apps/sales-go/internal/dbmigrate"
)

const localURL = "postgres://modura:modura@localhost:55433/modura?sslmode=disable"

// New returns a pool whose search_path is a fresh schema, dropped when the test ends.
// Without a reachable database the test is skipped, unless MODURA_REQUIRE_DB is set
// (as in CI), in which case it fails.
func New(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	url := os.Getenv("DATABASE_URL")
	if url == "" {
		url = localURL
	}
	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		if os.Getenv("MODURA_REQUIRE_DB") != "" {
			t.Fatalf("connect to Postgres: %v", err)
		}
		t.Skipf("Postgres not reachable (%v). Run `pnpm db:up` or set DATABASE_URL.", err)
	}

	suffix := make([]byte, 6)
	_, _ = rand.Read(suffix)
	schema := "test_" + hex.EncodeToString(suffix)
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		_ = admin.Close(context.Background())
	})

	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("parse DATABASE_URL: %v", err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	// Lets WaitForLockWaiters find this test's sessions among parallel tests.
	cfg.ConnConfig.RuntimeParams["application_name"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)

	// The same runner the deployed API uses, so tests also cover the migrations.
	dir := DBDir(t)
	if _, err := dbmigrate.Apply(ctx, pool, dir); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := dbmigrate.Seed(ctx, pool, dir); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return pool
}

// DBDir is the workspace's db/ directory.
func DBDir(t *testing.T) string {
	return filepath.Join(Root(t), "db")
}

// WaitForLockWaiters blocks until at least n of this test's sessions are waiting on
// a lock, for example a checkout queued behind another holding the same key.
func WaitForLockWaiters(t *testing.T, pool *pgxpool.Pool, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var waiting int
		err := pool.QueryRow(context.Background(), `
			SELECT count(*) FROM pg_stat_activity
			WHERE application_name = current_setting('application_name')
			  AND wait_event_type = 'Lock'`).Scan(&waiting)
		if err != nil {
			t.Fatalf("query pg_stat_activity: %v", err)
		}
		if waiting >= n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d session(s) to block on a lock", n)
}

// Root is the workspace root: the nearest directory holding pnpm-workspace.yaml.
func Root(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "pnpm-workspace.yaml")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("pnpm-workspace.yaml not found above the test directory")
		}
		dir = parent
	}
}
