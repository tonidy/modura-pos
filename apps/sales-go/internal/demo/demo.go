// Package demo runs the two background jobs that keep the public demo usable:
// stock replenishment and the nightly reset. Neither runs unless demo mode is on.
package demo

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"modura/apps/sales-go/internal/dbmigrate"
	"modura/apps/sales-go/internal/store"
)

type Config struct {
	DB    *pgxpool.Pool
	Store *store.Store
	// DBDir holds migrations/ and seed/.
	DBDir string
	// ReplenishEvery is how often low stock is topped up.
	ReplenishEvery time.Duration
	// ResetAt is the UTC time of day ("15:04") for the full reset.
	ResetAt string
	Logger  *slog.Logger
}

// Status is what /healthz reports, so the jobs are observable from outside.
type Status struct {
	LastReplenishAt *time.Time `json:"lastReplenishAt,omitempty"`
	LastResetAt     *time.Time `json:"lastResetAt,omitempty"`
	NextResetAt     *time.Time `json:"nextResetAt,omitempty"`
	LastError       string     `json:"lastError,omitempty"`
}

type Jobs struct {
	cfg    Config
	mu     sync.Mutex
	status Status
}

func New(cfg Config) *Jobs {
	return &Jobs{cfg: cfg}
}

func (j *Jobs) Status() Status {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.status
}

// Run blocks until ctx is done. A failed run is logged and recorded in Status, and
// the next tick tries again: a broken job must never take the API down.
func (j *Jobs) Run(ctx context.Context) error {
	resetAt, err := time.Parse("15:04", j.cfg.ResetAt)
	if err != nil {
		return err
	}
	ticker := time.NewTicker(j.cfg.ReplenishEvery)
	defer ticker.Stop()

	for {
		next := nextAt(time.Now().UTC(), resetAt)
		j.update(func(s *Status) { s.NextResetAt = &next })
		resetTimer := time.NewTimer(time.Until(next))

		select {
		case <-ctx.Done():
			resetTimer.Stop()
			return nil
		case <-ticker.C:
			resetTimer.Stop()
			j.replenish(ctx)
		case <-resetTimer.C:
			j.reset(ctx)
		}
	}
}

func (j *Jobs) replenish(ctx context.Context) {
	restocks, err := j.cfg.Store.ReplenishDemoStock(ctx)
	now := time.Now().UTC()
	if err != nil {
		j.cfg.Logger.Error("demo replenish failed", "error", err)
		j.update(func(s *Status) { s.LastError = "replenish: " + err.Error() })
		return
	}
	for _, r := range restocks {
		j.cfg.Logger.Info("demo restock", "productId", r.ProductID, "added", r.Added)
	}
	j.update(func(s *Status) { s.LastReplenishAt = &now })
}

func (j *Jobs) reset(ctx context.Context) {
	err := dbmigrate.ResetDemo(ctx, j.cfg.DB, j.cfg.DBDir)
	now := time.Now().UTC()
	if err != nil {
		j.cfg.Logger.Error("demo reset failed", "error", err)
		j.update(func(s *Status) { s.LastError = "reset: " + err.Error() })
		return
	}
	j.cfg.Logger.Info("demo reset complete")
	j.update(func(s *Status) { s.LastResetAt = &now })
}

func (j *Jobs) update(fn func(*Status)) {
	j.mu.Lock()
	defer j.mu.Unlock()
	fn(&j.status)
}

// nextAt is the next moment after now whose UTC clock time matches at.
func nextAt(now time.Time, at time.Time) time.Time {
	next := time.Date(now.Year(), now.Month(), now.Day(), at.Hour(), at.Minute(), 0, 0, time.UTC)
	if !next.After(now) {
		next = next.Add(24 * time.Hour)
	}
	return next
}
