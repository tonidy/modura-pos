package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"sync"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/uuid"

	"modura/apps/sales-go/internal/store"
)

// The transactional outbox (plan M7, ADR-0011): an event exists exactly when its
// sale committed.

type outboxRow struct {
	Event   string
	Payload map[string]any
}

func (h *harness) outbox() []outboxRow {
	h.t.Helper()
	rows, err := h.db.Query(context.Background(), `SELECT event, payload FROM outbox ORDER BY id`)
	if err != nil {
		h.t.Fatal(err)
	}
	defer rows.Close()
	var out []outboxRow
	for rows.Next() {
		var row outboxRow
		var raw []byte
		if err := rows.Scan(&row.Event, &raw); err != nil {
			h.t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &row.Payload); err != nil {
			h.t.Fatal(err)
		}
		out = append(out, row)
	}
	return out
}

func TestCheckoutWritesExactlyOneEventMatchingTheContract(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	key := uuid.NewString()

	sale := h.checkout(key, line{"prd_croissant", 2}, line{"prd_espresso", 1})
	expect(t, sale, http.StatusOK, "")
	expect(t, h.checkout(key, line{"prd_croissant", 2}, line{"prd_espresso", 1}), http.StatusOK, "") // replay

	events := h.outbox()
	if len(events) != 1 || events[0].Event != store.SaleCompletedEvent {
		t.Fatalf("outbox = %v, want one sales.saleCompleted", events)
	}
	if !reflect.DeepEqual(events[0].Payload, sale.body) {
		t.Fatalf("payload %v differs from the sale returned to the client %v", events[0].Payload, sale.body)
	}

	// Consumers get typed payloads from the same contract, so the event must match it.
	schema := h.spec.Components.Schemas["SalesSaleCompletedEvent"]
	if schema == nil {
		t.Fatal("the OpenAPI document has no SalesSaleCompletedEvent component")
	}
	var payload any = events[0].Payload
	if err := schema.Value.VisitJSON(payload, openapi3.MultiErrors()); err != nil {
		t.Fatalf("event payload violates the contract: %v", err)
	}
}

func TestRejectedAndRolledBackCheckoutsWriteNoEvent(t *testing.T) {
	t.Parallel()
	opts, entered, release := holdFirstCommit(errors.New("simulated crash before commit"))
	h := newHarnessWith(t, opts)

	expect(t, h.checkout(uuid.NewString(), line{"prd_croissant", 4}), http.StatusUnprocessableEntity, codeInsufficientStock)

	var wg sync.WaitGroup
	var crashed response
	wg.Go(func() { crashed = h.checkout(uuid.NewString(), line{"prd_espresso", 1}) })
	<-entered
	close(release)
	wg.Wait()
	expect(t, crashed, http.StatusInternalServerError, codeInternal)

	if events := h.outbox(); len(events) != 0 {
		t.Fatalf("outbox = %v, want nothing for a rejected and a rolled-back checkout", events)
	}
}

func TestOutboxIsAppendOnly(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	expect(t, h.checkout(uuid.NewString(), line{"prd_espresso", 1}), http.StatusOK, "")

	_, err := h.db.Exec(context.Background(), `UPDATE outbox SET event = 'sales.tampered'`)
	if err == nil {
		t.Fatal("updating the outbox should fail")
	}
	_, err = h.db.Exec(context.Background(), `DELETE FROM outbox`)
	if err == nil {
		t.Fatal("deleting from the outbox should fail")
	}
}
