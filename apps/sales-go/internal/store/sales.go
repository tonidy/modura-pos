package store

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"modura/apps/sales-go/internal/checkout"
)

const createSaleOperation = "sales.createSale"

var (
	// ErrIdempotencyKeyReused means the key was already used for a different request.
	ErrIdempotencyKeyReused = errors.New("idempotency key reused with a different request")
	// ErrIdempotencyInProgress means another request holding the key did not finish
	// within Options.ClaimTimeout.
	ErrIdempotencyInProgress = errors.New("another request with this idempotency key is still running")
	// ErrStockBusy means inventory rows stayed locked longer than Options.LockTimeout.
	ErrStockBusy = errors.New("inventory rows stayed locked past the lock timeout")
)

type RejectionReason int

const (
	UnknownProduct RejectionReason = iota + 1
	ProductNotSellable
	InsufficientStock
)

// Rejection is a business reason a sale cannot happen. The transaction was rolled
// back, so nothing was written and the idempotency key is free to be retried.
type Rejection struct {
	Reason  RejectionReason
	Message string
	Details map[string]any
}

func (r *Rejection) Error() string { return r.Message }

type SaleRequest struct {
	// Scope and Key identify the idempotency record. Scope is the caller's identity.
	Scope string
	Key   string
	// Fingerprint is checkout.Fingerprint of Lines.
	Fingerprint []byte
	// Lines must already be normalized: merged and sorted by product ID.
	Lines []checkout.Line
}

// Money, SaleLine and Sale use the contract's JSON field names, because the stored
// idempotency response is exactly what a replay returns.
type Money struct {
	AmountMinor int64  `json:"amountMinor"`
	Currency    string `json:"currency"`
}

type SaleLine struct {
	ProductID string `json:"productId"`
	Quantity  int64  `json:"quantity"`
	UnitPrice Money  `json:"unitPrice"`
	LineTotal Money  `json:"lineTotal"`
}

type Sale struct {
	ID        string     `json:"id"`
	Lines     []SaleLine `json:"lines"`
	Total     Money      `json:"total"`
	CreatedAt time.Time  `json:"createdAt"`
}

type SaleOutcome struct {
	Sale Sale
	// Replayed is true when Sale comes from an earlier request with the same key.
	Replayed bool
}

type product struct {
	name       string
	active     bool
	priceMinor int64
	currency   string
}

// CreateSale runs checkout in one READ COMMITTED transaction (ADR-0004):
//
//  1. Claim the idempotency key. A concurrent claim of the same key waits on the
//     primary key until the owner commits (then replays) or rolls back (then owns it).
//  2. Lock inventory rows in product ID order.
//  3. Reject unknown, inactive or insufficient lines.
//  4. Write the sale, price snapshots and stock movements, and decrement inventory.
//  5. Store the response on the idempotency row and commit.
//
// The key is claimed before stock is touched. Otherwise a retry waiting on the
// inventory lock would see stock the original already took and answer
// INSUFFICIENT_STOCK instead of replaying the original sale.
func (s *Store) CreateSale(ctx context.Context, req SaleRequest) (SaleOutcome, error) {
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return SaleOutcome{}, fmt.Errorf("begin checkout: %w", err)
	}
	// After a successful Commit this is a no-op.
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	claimed, err := claimKey(ctx, tx, req.Scope, req.Key, createSaleOperation, req.Fingerprint, s.opts.ClaimTimeout)
	if err != nil {
		return SaleOutcome{}, err
	}
	if !claimed {
		body, err := storedResponse(ctx, tx, req.Scope, req.Key, req.Fingerprint)
		if err != nil {
			return SaleOutcome{}, err
		}
		var sale Sale
		if err := json.Unmarshal(body, &sale); err != nil {
			return SaleOutcome{}, fmt.Errorf("decode stored sale: %w", err)
		}
		return SaleOutcome{Sale: sale, Replayed: true}, nil
	}

	if err := setLockTimeout(ctx, tx, s.opts.LockTimeout); err != nil {
		return SaleOutcome{}, err
	}
	productIDs := make([]string, len(req.Lines))
	for i, line := range req.Lines {
		productIDs[i] = line.ProductID
	}
	available, err := lockInventory(ctx, tx, productIDs)
	if isLockTimeout(err) {
		return SaleOutcome{}, ErrStockBusy
	}
	if err != nil {
		return SaleOutcome{}, err
	}
	products, err := loadProducts(ctx, tx, productIDs)
	if err != nil {
		return SaleOutcome{}, err
	}
	if rejection := validate(req.Lines, products, available); rejection != nil {
		return SaleOutcome{}, rejection
	}

	sale, err := writeSale(ctx, tx, req, products)
	if err != nil {
		return SaleOutcome{}, err
	}

	body, err := json.Marshal(sale)
	if err != nil {
		return SaleOutcome{}, fmt.Errorf("encode sale: %w", err)
	}
	if err := completeKey(ctx, tx, req.Scope, req.Key, body); err != nil {
		return SaleOutcome{}, err
	}

	if s.opts.BeforeCommit != nil {
		if err := s.opts.BeforeCommit(ctx); err != nil {
			return SaleOutcome{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return SaleOutcome{}, fmt.Errorf("commit checkout: %w", err)
	}
	return SaleOutcome{Sale: sale}, nil
}

// lockInventory locks the rows in product ID order. With ORDER BY, Postgres locks
// rows as they leave the sort, so every checkout acquires locks in the same order.
func lockInventory(ctx context.Context, tx pgx.Tx, productIDs []string) (map[string]int64, error) {
	rows, err := tx.Query(ctx, `
		SELECT product_id, available FROM inventory
		WHERE product_id = ANY($1)
		ORDER BY product_id
		FOR UPDATE`, productIDs)
	if err != nil {
		return nil, fmt.Errorf("lock inventory: %w", err)
	}
	available := map[string]int64{}
	var productID string
	var units int64
	_, err = pgx.ForEachRow(rows, []any{&productID, &units}, func() error {
		available[productID] = units
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("lock inventory: %w", err)
	}
	return available, nil
}

func loadProducts(ctx context.Context, tx pgx.Tx, productIDs []string) (map[string]product, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, name, active, price_minor, currency FROM products
		WHERE id = ANY($1)`, productIDs)
	if err != nil {
		return nil, fmt.Errorf("load products: %w", err)
	}
	products := map[string]product{}
	var id string
	var p product
	_, err = pgx.ForEachRow(rows, []any{&id, &p.name, &p.active, &p.priceMinor, &p.currency}, func() error {
		products[id] = p
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("load products: %w", err)
	}
	return products, nil
}

// validate reports the first kind of problem found, listing every line affected by it.
func validate(lines []checkout.Line, products map[string]product, available map[string]int64) *Rejection {
	var unknown, inactive []string
	var short []map[string]any
	var shortMessages []string
	for _, line := range lines {
		p, ok := products[line.ProductID]
		switch {
		case !ok:
			unknown = append(unknown, line.ProductID)
		case !p.active:
			inactive = append(inactive, line.ProductID)
		case available[line.ProductID] < line.Quantity:
			short = append(short, map[string]any{
				"productId": line.ProductID,
				"requested": line.Quantity,
				"available": available[line.ProductID],
			})
			shortMessages = append(shortMessages, fmt.Sprintf("%s has %d available, %d requested",
				p.name, available[line.ProductID], line.Quantity))
		}
	}
	switch {
	case len(unknown) > 0:
		return &Rejection{UnknownProduct, "Unknown product: " + strings.Join(unknown, ", ") + ".",
			map[string]any{"productIds": unknown}}
	case len(inactive) > 0:
		return &Rejection{ProductNotSellable, "No longer sold: " + strings.Join(inactive, ", ") + ".",
			map[string]any{"productIds": inactive}}
	case len(short) > 0:
		return &Rejection{InsufficientStock, strings.Join(shortMessages, "; ") + ". Nothing was sold.",
			map[string]any{"lines": short}}
	}
	return nil
}

func writeSale(ctx context.Context, tx pgx.Tx, req SaleRequest, products map[string]product) (Sale, error) {
	sale := Sale{ID: newSaleID(), Lines: make([]SaleLine, 0, len(req.Lines))}
	for _, line := range req.Lines {
		p := products[line.ProductID]
		if sale.Total.Currency == "" {
			sale.Total.Currency = p.currency
		} else if sale.Total.Currency != p.currency {
			// ADR-0006: v1 sells in one currency. Mixed catalogs are a data error.
			return Sale{}, fmt.Errorf("mixed currencies in one sale: %s and %s", sale.Total.Currency, p.currency)
		}
		lineTotal := p.priceMinor * line.Quantity
		sale.Total.AmountMinor += lineTotal
		sale.Lines = append(sale.Lines, SaleLine{
			ProductID: line.ProductID,
			Quantity:  line.Quantity,
			UnitPrice: Money{AmountMinor: p.priceMinor, Currency: p.currency},
			LineTotal: Money{AmountMinor: lineTotal, Currency: p.currency},
		})
	}

	err := tx.QueryRow(ctx, `
		INSERT INTO sales (id, idempotency_scope, idempotency_key, total_minor, currency)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING created_at`,
		sale.ID, req.Scope, req.Key, sale.Total.AmountMinor, sale.Total.Currency).Scan(&sale.CreatedAt)
	if err != nil {
		return Sale{}, fmt.Errorf("insert sale: %w", err)
	}
	sale.CreatedAt = sale.CreatedAt.UTC()

	batch := &pgx.Batch{}
	for i, line := range sale.Lines {
		batch.Queue(`
			INSERT INTO sale_lines (sale_id, line_no, product_id, quantity, unit_price_minor, line_total_minor)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			sale.ID, i+1, line.ProductID, line.Quantity, line.UnitPrice.AmountMinor, line.LineTotal.AmountMinor)
		batch.Queue(`
			UPDATE inventory SET available = available - $2, updated_at = now()
			WHERE product_id = $1`, line.ProductID, line.Quantity)
		batch.Queue(`
			INSERT INTO stock_movements (product_id, delta, reason, sale_id)
			VALUES ($1, $2, 'sale', $3)`, line.ProductID, -line.Quantity, sale.ID)
	}
	if err := tx.SendBatch(ctx, batch).Close(); err != nil {
		return Sale{}, fmt.Errorf("write sale lines: %w", err)
	}

	// The event goes into the outbox in the same transaction (ADR-0011): it exists
	// exactly when the sale committed. A replay returns before reaching this point,
	// so it never writes a second event. The payload is the contract's Sale.
	payload, err := json.Marshal(sale)
	if err != nil {
		return Sale{}, fmt.Errorf("encode sale event: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO outbox (event_id, event, payload) VALUES ($1, $2, $3)`,
		uuid.NewString(), SaleCompletedEvent, payload); err != nil {
		return Sale{}, fmt.Errorf("write sale event: %w", err)
	}
	return sale, nil
}

// SaleCompletedEvent is the qualified name of the event checkout publishes.
const SaleCompletedEvent = "sales.saleCompleted"

func setLockTimeout(ctx context.Context, tx pgx.Tx, timeout time.Duration) error {
	_, err := tx.Exec(ctx, `SELECT set_config('lock_timeout', $1, true)`,
		fmt.Sprintf("%dms", timeout.Milliseconds()))
	if err != nil {
		return fmt.Errorf("set lock_timeout: %w", err)
	}
	return nil
}

func isLockTimeout(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "55P03" // lock_not_available
}

var saleIDEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

func newSaleID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return "sale_" + strings.ToLower(saleIDEncoding.EncodeToString(b))
}
