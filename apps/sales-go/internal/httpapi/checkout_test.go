package httpapi

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"modura/apps/sales-go/internal/store"
	"modura/apps/sales-go/internal/testdb"
)

// Checkout tests for sales.createSale (plan M2, ADR-0004). Every request goes through
// the real HTTP stack, and every response is validated against the contract by call.

type line struct {
	productID string
	quantity  int
}

func saleOf(lines ...line) map[string]any {
	items := make([]any, len(lines))
	for i, l := range lines {
		items[i] = map[string]any{"productId": l.productID, "quantity": l.quantity}
	}
	return map[string]any{"lines": items}
}

func withKey(key string) map[string]string {
	return map[string]string{"Idempotency-Key": key}
}

func (h *harness) checkout(key string, lines ...line) response {
	h.t.Helper()
	return h.call(cashier, "sales.createSale", saleOf(lines...), withKey(key))
}

func (h *harness) stock(productID string) int64 {
	h.t.Helper()
	var available int64
	if err := h.db.QueryRow(context.Background(),
		`SELECT available FROM inventory WHERE product_id = $1`, productID).Scan(&available); err != nil {
		h.t.Fatal(err)
	}
	return available
}

func (h *harness) count(query string) int {
	h.t.Helper()
	var n int
	if err := h.db.QueryRow(context.Background(), query).Scan(&n); err != nil {
		h.t.Fatal(err)
	}
	return n
}

func replayed(res response) string {
	return res.header.Get("Idempotent-Replayed")
}

func TestCheckoutDeductsStockOnceAndSnapshotsPrices(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	key := uuid.NewString()

	first := h.checkout(key, line{"prd_flat_white", 1}, line{"prd_croissant", 2}, line{"prd_flat_white", 1})

	expect(t, first, http.StatusOK, "")
	if replayed(first) != "false" {
		t.Fatalf("Idempotent-Replayed = %q, want false", replayed(first))
	}
	lines := first.body["lines"].([]any)
	if len(lines) != 2 || lines[0].(map[string]any)["productId"] != "prd_croissant" ||
		lines[1].(map[string]any)["quantity"] != float64(2) {
		t.Fatalf("lines = %v, want merged and sorted: croissant x2, flat white x2", lines)
	}
	// 2 x 375 + 2 x 450, priced by the server from the catalog.
	if total := first.body["total"].(map[string]any)["amountMinor"]; total != float64(1650) {
		t.Fatalf("total = %v, want 1650", total)
	}
	if h.stock("prd_croissant") != 1 || h.stock("prd_flat_white") != 148 {
		t.Fatalf("stock = %d croissant, %d flat white; want 1 and 148", h.stock("prd_croissant"), h.stock("prd_flat_white"))
	}
	if n := h.count(`SELECT count(*) FROM stock_movements WHERE reason = 'sale'`); n != 2 {
		t.Fatalf("stock movements = %d, want 2", n)
	}

	// A later price change must not touch the past sale or its replay.
	if _, err := h.db.Exec(context.Background(), `UPDATE products SET price_minor = 999 WHERE id = 'prd_croissant'`); err != nil {
		t.Fatal(err)
	}
	replay := h.checkout(key, line{"prd_croissant", 2}, line{"prd_flat_white", 2})
	expect(t, replay, http.StatusOK, "")
	if replayed(replay) != "true" || !reflect.DeepEqual(replay.body, first.body) {
		t.Fatalf("replay = %v (replayed %q), want the original body", replay.body, replayed(replay))
	}
	if h.stock("prd_croissant") != 1 {
		t.Fatal("replay deducted stock again")
	}

	fresh := h.checkout(uuid.NewString(), line{"prd_croissant", 1})
	expect(t, fresh, http.StatusOK, "")
	unitPrice := fresh.body["lines"].([]any)[0].(map[string]any)["unitPrice"].(map[string]any)["amountMinor"]
	if unitPrice != float64(999) {
		t.Fatalf("new sale unit price = %v, want the current catalog price 999", unitPrice)
	}
}

func TestCheckoutRejectsAKeyReusedForADifferentSale(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	key := uuid.NewString()

	expect(t, h.checkout(key, line{"prd_espresso", 1}), http.StatusOK, "")
	expect(t, h.checkout(key, line{"prd_espresso", 2}), http.StatusConflict, codeIdempotencyKeyReused)

	if h.stock("prd_espresso") != 199 {
		t.Fatalf("espresso stock = %d, want 199", h.stock("prd_espresso"))
	}
}

func TestRejectedCheckoutsWriteNothingAndLeaveTheKeyFree(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	cases := []struct {
		name  string
		lines []line
		code  string
	}{
		{"unknown product", []line{{"prd_espresso", 1}, {"prd_unicorn", 1}}, codeUnknownProduct},
		{"inactive product", []line{{"prd_espresso", 1}, {"prd_seasonal_latte", 1}}, codeProductNotSellable},
		{"insufficient stock", []line{{"prd_espresso", 1}, {"prd_croissant", 4}}, codeInsufficientStock},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			expect(t, h.checkout(uuid.NewString(), tc.lines...), http.StatusUnprocessableEntity, tc.code)
		})
	}

	if h.stock("prd_espresso") != 200 {
		t.Fatalf("espresso stock = %d, want 200: a rejected sale changed stock", h.stock("prd_espresso"))
	}
	for _, table := range []string{"sales", "sale_lines", "stock_movements", "idempotency_keys"} {
		if n := h.count("SELECT count(*) FROM " + table); n != 0 {
			t.Fatalf("%s has %d rows after rejected checkouts, want 0", table, n)
		}
	}

	// A rejection does not burn the key: the same attempt can succeed once fixed.
	key := uuid.NewString()
	short := h.checkout(key, line{"prd_croissant", 4})
	expect(t, short, http.StatusUnprocessableEntity, codeInsufficientStock)
	detail := short.body["details"].(map[string]any)["lines"].([]any)[0].(map[string]any)
	if detail["available"] != float64(3) || detail["requested"] != float64(4) {
		t.Fatalf("details = %v, want available 3, requested 4", detail)
	}
	expect(t, h.checkout(key, line{"prd_croissant", 3}), http.StatusOK, "")
}

func TestMergedLinesStillRespectTheQuantityLimit(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	res := h.checkout(uuid.NewString(), line{"prd_espresso", 600}, line{"prd_espresso", 400})

	expect(t, res, http.StatusBadRequest, codeValidationFailed)
}

func TestConcurrentCheckoutsCannotOversell(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const attempts = 20 // prd_croissant has 3 in stock

	statuses := make([]int, attempts)
	var wg sync.WaitGroup
	for i := range attempts {
		wg.Go(func() {
			statuses[i] = h.checkout(uuid.NewString(), line{"prd_croissant", 1}).status
		})
	}
	wg.Wait()

	sold, rejected := 0, 0
	for _, status := range statuses {
		switch status {
		case http.StatusOK:
			sold++
		case http.StatusUnprocessableEntity:
			rejected++
		default:
			t.Errorf("unexpected status %d", status)
		}
	}
	if sold != 3 || rejected != attempts-3 {
		t.Fatalf("sold %d, rejected %d; want exactly 3 sold", sold, rejected)
	}
	if h.stock("prd_croissant") != 0 || h.count(`SELECT count(*) FROM sales`) != 3 {
		t.Fatalf("stock %d, sales %d; want 0 and 3", h.stock("prd_croissant"), h.count(`SELECT count(*) FROM sales`))
	}
	if moved := h.count(`SELECT -sum(delta) FROM stock_movements WHERE product_id = 'prd_croissant'`); moved != 3 {
		t.Fatalf("stock movements add up to %d, want 3", moved)
	}
}

func TestConcurrentRetriesWithOneKeyCreateOneSale(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	key := uuid.NewString()
	const retries = 10

	results := make([]response, retries)
	var wg sync.WaitGroup
	for i := range retries {
		wg.Go(func() { results[i] = h.checkout(key, line{"prd_espresso", 1}) })
	}
	wg.Wait()

	originals := 0
	for _, res := range results {
		expect(t, res, http.StatusOK, "")
		if res.body["id"] != results[0].body["id"] {
			t.Fatalf("got sale ids %v and %v for one key", res.body["id"], results[0].body["id"])
		}
		if replayed(res) == "false" {
			originals++
		}
	}
	if originals != 1 {
		t.Fatalf("%d responses claim to be the original, want 1", originals)
	}
	if h.stock("prd_espresso") != 199 || h.count(`SELECT count(*) FROM sales`) != 1 {
		t.Fatal("one key produced more than one deduction")
	}
}

func TestConcurrentMultiItemCheckoutsDoNotDeadlock(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const attempts = 20

	statuses := make([]int, attempts)
	var wg sync.WaitGroup
	for i := range attempts {
		wg.Go(func() {
			lines := []line{{"prd_espresso", 1}, {"prd_flat_white", 1}, {"prd_cappuccino", 1}}
			if i%2 == 1 {
				lines = []line{{"prd_cappuccino", 1}, {"prd_flat_white", 1}, {"prd_espresso", 1}}
			}
			statuses[i] = h.checkout(uuid.NewString(), lines...).status
		})
	}
	wg.Wait()

	for _, status := range statuses {
		if status != http.StatusOK {
			t.Fatalf("status %d; opposite line orders must not deadlock or fail", status)
		}
	}
	if h.stock("prd_espresso") != 180 || h.stock("prd_flat_white") != 130 || h.stock("prd_cappuccino") != 130 {
		t.Fatal("stock does not add up after concurrent multi-item checkouts")
	}
}

// holdFirstCommit returns store options whose first checkout pauses just before
// commit. entered fires once it is paused; closing release lets it finish with err.
func holdFirstCommit(err error) (opts store.Options, entered <-chan struct{}, release chan<- struct{}) {
	in := make(chan struct{})
	out := make(chan struct{})
	var first atomic.Bool
	opts.BeforeCommit = func(context.Context) error {
		if first.CompareAndSwap(false, true) {
			close(in)
			<-out
			return err
		}
		return nil
	}
	return opts, in, out
}

func TestInFlightRetryWaitsAndReplaysEvenAfterTheLastUnitIsSold(t *testing.T) {
	t.Parallel()
	opts, entered, release := holdFirstCommit(nil)
	h := newHarnessWith(t, opts)
	key := uuid.NewString()

	var original, retry response
	var wg sync.WaitGroup
	wg.Go(func() { original = h.checkout(key, line{"prd_croissant", 3}) })
	<-entered // the original has taken all 3 croissants and is about to commit

	wg.Go(func() { retry = h.checkout(key, line{"prd_croissant", 3}) })
	testdb.WaitForLockWaiters(t, h.db, 1) // the retry is queued behind the key
	close(release)
	wg.Wait()

	expect(t, original, http.StatusOK, "")
	expect(t, retry, http.StatusOK, "") // not INSUFFICIENT_STOCK: the key was claimed first
	if replayed(retry) != "true" || retry.body["id"] != original.body["id"] {
		t.Fatalf("retry = %v (replayed %q), want a replay of %v", retry.body["id"], replayed(retry), original.body["id"])
	}
	if h.stock("prd_croissant") != 0 || h.count(`SELECT count(*) FROM sales`) != 1 {
		t.Fatal("the retry sold again")
	}
}

func TestInFlightRetryTakesOverWhenTheOriginalRollsBack(t *testing.T) {
	t.Parallel()
	opts, entered, release := holdFirstCommit(errors.New("simulated crash before commit"))
	h := newHarnessWith(t, opts)
	key := uuid.NewString()

	var original, retry response
	var wg sync.WaitGroup
	wg.Go(func() { original = h.checkout(key, line{"prd_croissant", 3}) })
	<-entered
	wg.Go(func() { retry = h.checkout(key, line{"prd_croissant", 3}) })
	testdb.WaitForLockWaiters(t, h.db, 1)
	close(release)
	wg.Wait()

	expect(t, original, http.StatusInternalServerError, codeInternal)
	expect(t, retry, http.StatusOK, "")
	if replayed(retry) != "false" {
		t.Fatal("the retry should own the key after the original rolled back")
	}
	if h.stock("prd_croissant") != 0 || h.count(`SELECT count(*) FROM sales`) != 1 {
		t.Fatal("expected exactly one sale, made by the retry")
	}
}

func TestInFlightRetryGivesUpAfterTheClaimTimeout(t *testing.T) {
	t.Parallel()
	opts, entered, release := holdFirstCommit(nil)
	opts.ClaimTimeout = 200 * time.Millisecond
	h := newHarnessWith(t, opts)
	key := uuid.NewString()

	var original response
	var wg sync.WaitGroup
	wg.Go(func() { original = h.checkout(key, line{"prd_espresso", 1}) })
	<-entered

	busy := h.checkout(key, line{"prd_espresso", 1})
	expect(t, busy, http.StatusConflict, codeIdempotencyInProgress)
	if busy.body["retryable"] != true {
		t.Fatal("IDEMPOTENCY_IN_PROGRESS must be retryable")
	}

	close(release)
	wg.Wait()
	expect(t, original, http.StatusOK, "")

	again := h.checkout(key, line{"prd_espresso", 1})
	expect(t, again, http.StatusOK, "")
	if replayed(again) != "true" || h.stock("prd_espresso") != 199 {
		t.Fatal("retrying after the timeout should replay the original sale")
	}
}
