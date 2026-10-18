package store_test

import (
	"context"
	"testing"

	"modura/apps/sales-go/internal/checkout"
	"modura/apps/sales-go/internal/dbmigrate"
	"modura/apps/sales-go/internal/store"
	"modura/apps/sales-go/internal/testdb"
)

func available(t *testing.T, s *store.Store, productID string) int64 {
	t.Helper()
	level, err := s.GetStock(context.Background(), productID)
	if err != nil {
		t.Fatal(err)
	}
	return level.Available
}

func sell(t *testing.T, s *store.Store, key string, lines ...checkout.Line) store.SaleOutcome {
	t.Helper()
	outcome, err := s.CreateSale(context.Background(), store.SaleRequest{
		Scope: "cashier", Key: key, Fingerprint: checkout.Fingerprint("sales.createSale", lines), Lines: lines,
	})
	if err != nil {
		t.Fatal(err)
	}
	return outcome
}

func TestReplenishTopsUpLowStockAsAStockMovement(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	s := store.New(db, store.Options{})
	ctx := context.Background()

	sale := sell(t, s, "11111111-1111-4111-8111-111111111111", checkout.Line{ProductID: "prd_croissant", Quantity: 3})
	sell(t, s, "22222222-2222-4222-8222-222222222222", checkout.Line{ProductID: "prd_banana_bread", Quantity: 2})

	restocks, err := s.ReplenishDemoStock(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Croissant hit 0 of 3 and is topped up. Banana bread is at 10 of 12, above a quarter.
	if len(restocks) != 1 || restocks[0] != (store.Restock{ProductID: "prd_croissant", Added: 3}) {
		t.Fatalf("restocks = %v, want only the croissant, +3", restocks)
	}
	if available(t, s, "prd_croissant") != 3 || available(t, s, "prd_banana_bread") != 10 {
		t.Fatal("stock after replenishment is wrong")
	}

	var movements int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM stock_movements WHERE reason = 'restock' AND delta = 3`).Scan(&movements); err != nil || movements != 1 {
		t.Fatalf("restock movements = %d (%v), want 1", movements, err)
	}

	// The past sale and its idempotency record are untouched, so a replay still works.
	replay := sell(t, s, "11111111-1111-4111-8111-111111111111", checkout.Line{ProductID: "prd_croissant", Quantity: 3})
	if !replay.Replayed || replay.Sale.ID != sale.Sale.ID || available(t, s, "prd_croissant") != 3 {
		t.Fatal("replenishment must not disturb existing sales or idempotency records")
	}

	// Nothing is low any more, so a second run does nothing.
	if again, err := s.ReplenishDemoStock(ctx); err != nil || len(again) != 0 {
		t.Fatalf("second run restocked %v (%v), want nothing", again, err)
	}
}

func TestMigrationsAreIdempotentAndResetRestoresTheSeed(t *testing.T) {
	t.Parallel()
	db := testdb.New(t) // already migrated and seeded
	s := store.New(db, store.Options{})
	ctx := context.Background()
	dir := testdb.DBDir(t)

	applied, err := dbmigrate.Apply(ctx, db, dir)
	if err != nil || len(applied) != 0 {
		t.Fatalf("second Apply ran %v (%v), want nothing", applied, err)
	}
	if seeded, err := dbmigrate.Seed(ctx, db, dir); err != nil || seeded {
		t.Fatalf("Seed on a non-empty catalog seeded=%v (%v), want false", seeded, err)
	}

	sell(t, s, "33333333-3333-4333-8333-333333333333", checkout.Line{ProductID: "prd_espresso", Quantity: 5})
	if err := dbmigrate.ResetDemo(ctx, db, dir); err != nil {
		t.Fatal(err)
	}
	var sales int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM sales`).Scan(&sales); err != nil || sales != 0 {
		t.Fatalf("sales after reset = %d (%v), want 0", sales, err)
	}
	if available(t, s, "prd_espresso") != 200 {
		t.Fatal("reset did not restore seeded stock")
	}
}
