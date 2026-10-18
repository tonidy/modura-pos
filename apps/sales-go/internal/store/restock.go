package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Restock is one product the replenishment job topped up.
type Restock struct {
	ProductID string
	Added     int64
}

// ReplenishDemoStock tops up every active product that is down to a quarter of its
// restock_to level, recording each top-up as an ordinary stock movement.
//
// Rows are locked in product ID order, like checkout, and SKIP LOCKED leaves rows a
// checkout is holding for the next run. The job never makes a cashier wait, and it
// never touches sales or idempotency records.
func (s *Store) ReplenishDemoStock(ctx context.Context) ([]Restock, error) {
	var restocks []Restock
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT i.product_id, i.restock_to - i.available
			FROM inventory i
			JOIN products p ON p.id = i.product_id
			WHERE p.active
			  AND i.restock_to IS NOT NULL
			  AND i.available * 4 <= i.restock_to
			ORDER BY i.product_id
			FOR UPDATE OF i SKIP LOCKED`)
		if err != nil {
			return err
		}
		restocks, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (Restock, error) {
			var r Restock
			err := row.Scan(&r.ProductID, &r.Added)
			return r, err
		})
		if err != nil {
			return err
		}

		batch := &pgx.Batch{}
		for _, r := range restocks {
			batch.Queue(`
				UPDATE inventory SET available = restock_to, updated_at = now()
				WHERE product_id = $1`, r.ProductID)
			batch.Queue(`
				INSERT INTO stock_movements (product_id, delta, reason)
				VALUES ($1, $2, 'restock')`, r.ProductID, r.Added)
		}
		return tx.SendBatch(ctx, batch).Close()
	})
	if err != nil {
		return nil, fmt.Errorf("replenish demo stock: %w", err)
	}
	return restocks, nil
}
