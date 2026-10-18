// Package store reads and writes the Postgres schema in db/migrations.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound means the requested row does not exist.
var ErrNotFound = errors.New("not found")

type Store struct {
	db   *pgxpool.Pool
	opts Options
}

type Options struct {
	// ClaimTimeout bounds how long checkout waits for another request holding the
	// same idempotency key. Default 2s. Exceeding it is ErrIdempotencyInProgress.
	ClaimTimeout time.Duration
	// LockTimeout bounds how long checkout waits for inventory row locks.
	// Default 5s. Exceeding it is ErrStockBusy.
	LockTimeout time.Duration
	// BeforeCommit, if set, runs inside the checkout transaction just before commit.
	// It is a test seam for holding a transaction open; returning an error rolls back.
	BeforeCommit func(context.Context) error
}

func New(db *pgxpool.Pool, opts Options) *Store {
	if opts.ClaimTimeout <= 0 {
		opts.ClaimTimeout = 2 * time.Second
	}
	if opts.LockTimeout <= 0 {
		opts.LockTimeout = 5 * time.Second
	}
	return &Store{db: db, opts: opts}
}

type Product struct {
	ID         string
	SKU        string
	Name       string
	Category   string
	PriceMinor int64
	Currency   string
}

type StockLevel struct {
	ProductID string
	Available int64
	UpdatedAt time.Time
}

func (s *Store) Ping(ctx context.Context) error {
	return s.db.Ping(ctx)
}

// ListActiveProducts returns sellable products in a stable display order.
func (s *Store) ListActiveProducts(ctx context.Context) ([]Product, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, sku, name, category, price_minor, currency
		FROM products
		WHERE active
		ORDER BY category, name, id`)
	if err != nil {
		return nil, fmt.Errorf("list products: %w", err)
	}
	products, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Product, error) {
		var p Product
		err := row.Scan(&p.ID, &p.SKU, &p.Name, &p.Category, &p.PriceMinor, &p.Currency)
		return p, err
	})
	if err != nil {
		return nil, fmt.Errorf("list products: %w", err)
	}
	return products, nil
}

// GetStock returns the stock level of a known product. A product without an
// inventory row has zero stock; only an unknown product is ErrNotFound.
func (s *Store) GetStock(ctx context.Context, productID string) (StockLevel, error) {
	var level StockLevel
	err := s.db.QueryRow(ctx, `
		SELECT p.id, COALESCE(i.available, 0), COALESCE(i.updated_at, p.created_at)
		FROM products p
		LEFT JOIN inventory i ON i.product_id = p.id
		WHERE p.id = $1`, productID).Scan(&level.ProductID, &level.Available, &level.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return StockLevel{}, ErrNotFound
	}
	if err != nil {
		return StockLevel{}, fmt.Errorf("get stock %s: %w", productID, err)
	}
	return level, nil
}

// IsUnavailable reports whether err is transient (the database is unreachable or
// busy) as opposed to a bug. It decides between UNAVAILABLE (retryable) and INTERNAL.
func IsUnavailable(err error) bool {
	var connectErr *pgconn.ConnectError
	if errors.As(err, &connectErr) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrStockBusy) {
		return true
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "40001", "40P01", "57P01": // serialization failure, deadlock, admin shutdown
			return true
		}
	}
	return false
}
