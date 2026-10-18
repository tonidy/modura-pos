// Package checkout holds the pure parts of sales.createSale: request normalization
// and the idempotency fingerprint. Everything here is deterministic and has no I/O.
package checkout

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
)

// MaxLineQuantity matches the contract's quantity maximum, which also applies after
// lines for the same product are merged.
const MaxLineQuantity = 999

type Line struct {
	ProductID string `json:"productId"`
	Quantity  int64  `json:"quantity"`
}

// QuantityError reports a product whose merged quantity exceeds MaxLineQuantity.
type QuantityError struct {
	ProductID string
	Quantity  int64
}

func (e *QuantityError) Error() string {
	return fmt.Sprintf("%s: merged quantity %d exceeds %d", e.ProductID, e.Quantity, MaxLineQuantity)
}

// Normalize merges lines for the same product and sorts them by product ID. The
// sorted order is also the order inventory rows are locked in, which is what keeps
// concurrent multi-item sales from deadlocking each other.
func Normalize(lines []Line) ([]Line, error) {
	merged := map[string]int64{}
	for _, line := range lines {
		merged[line.ProductID] += line.Quantity
	}
	out := make([]Line, 0, len(merged))
	for productID, quantity := range merged {
		if quantity > MaxLineQuantity {
			return nil, &QuantityError{ProductID: productID, Quantity: quantity}
		}
		out = append(out, Line{ProductID: productID, Quantity: quantity})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ProductID < out[j].ProductID })
	return out, nil
}

// Fingerprint identifies a normalized request. Two requests with the same
// idempotency key must have the same fingerprint, or the second is rejected.
func Fingerprint(operation string, lines []Line) []byte {
	payload, err := json.Marshal(struct {
		Operation string `json:"operation"`
		Lines     []Line `json:"lines"`
	}{operation, lines})
	if err != nil {
		panic(err) // unreachable: the struct always marshals
	}
	sum := sha256.Sum256(payload)
	return sum[:]
}
