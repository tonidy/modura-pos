// Package dbmigrate applies the SQL files in db/migrations and loads the demo seed.
// It is the only migration runner: local resets, tests and the deployed API all use it.
package dbmigrate

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Apply runs every migration in dir/migrations that is not yet recorded in
// schema_migrations, in filename order, each in its own transaction. An advisory
// lock keyed on the current schema keeps concurrent starts from racing.
func Apply(ctx context.Context, db *pgxpool.Pool, dir string) ([]string, error) {
	files, err := filepath.Glob(filepath.Join(dir, "migrations", "*.sql"))
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no migrations found in %s", filepath.Join(dir, "migrations"))
	}
	sort.Strings(files)

	conn, err := db.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(hashtext(current_schema()))`); err != nil {
		return nil, fmt.Errorf("lock migrations: %w", err)
	}
	defer conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock(hashtext(current_schema()))`)

	if _, err := conn.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    text PRIMARY KEY,
			applied_at timestamptz NOT NULL DEFAULT now()
		)`); err != nil {
		return nil, fmt.Errorf("create schema_migrations: %w", err)
	}

	var applied []string
	for _, file := range files {
		version := strings.TrimSuffix(filepath.Base(file), ".sql")
		var done bool
		if err := conn.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)`, version).Scan(&done); err != nil {
			return applied, err
		}
		if done {
			continue
		}
		sql, err := os.ReadFile(file)
		if err != nil {
			return applied, err
		}
		err = pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, string(sql)); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, version)
			return err
		})
		if err != nil {
			return applied, fmt.Errorf("apply %s: %w", version, err)
		}
		applied = append(applied, version)
	}
	return applied, nil
}

// Seed loads dir/seed/demo.sql if the catalog is empty, so restarts never duplicate it.
func Seed(ctx context.Context, db *pgxpool.Pool, dir string) (bool, error) {
	var empty bool
	if err := db.QueryRow(ctx, `SELECT NOT EXISTS (SELECT 1 FROM products)`).Scan(&empty); err != nil {
		return false, err
	}
	if !empty {
		return false, nil
	}
	sql, err := os.ReadFile(filepath.Join(dir, "seed", "demo.sql"))
	if err != nil {
		return false, err
	}
	return true, pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, string(sql))
		return err
	})
}

// ResetDemo empties every table and reloads the seed in one transaction. TRUNCATE
// waits for in-flight checkouts to finish, so no sale is ever half-wiped.
func ResetDemo(ctx context.Context, db *pgxpool.Pool, dir string) error {
	sql, err := os.ReadFile(filepath.Join(dir, "seed", "demo.sql"))
	if err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			TRUNCATE idempotency_keys, stock_movements, sale_lines, sales, inventory, products
			RESTART IDENTITY`); err != nil {
			return fmt.Errorf("truncate: %w", err)
		}
		if _, err := tx.Exec(ctx, string(sql)); err != nil {
			return fmt.Errorf("seed: %w", err)
		}
		return nil
	})
}
