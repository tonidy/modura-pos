package checkout

import (
	"bytes"
	"errors"
	"reflect"
	"testing"
)

func TestNormalizeMergesAndSorts(t *testing.T) {
	got, err := Normalize([]Line{
		{"prd_flat_white", 1},
		{"prd_croissant", 2},
		{"prd_flat_white", 3},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []Line{{"prd_croissant", 2}, {"prd_flat_white", 4}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestNormalizeRejectsMergedQuantityOverTheLimit(t *testing.T) {
	_, err := Normalize([]Line{{"prd_espresso", 600}, {"prd_espresso", 400}})

	var quantityErr *QuantityError
	if !errors.As(err, &quantityErr) || quantityErr.Quantity != 1000 {
		t.Fatalf("err = %v, want a QuantityError for 1000", err)
	}
}

func TestFingerprintIgnoresLineOrderAfterNormalization(t *testing.T) {
	a, _ := Normalize([]Line{{"prd_a", 1}, {"prd_b", 2}})
	b, _ := Normalize([]Line{{"prd_b", 1}, {"prd_a", 1}, {"prd_b", 1}})
	c, _ := Normalize([]Line{{"prd_a", 1}, {"prd_b", 3}})

	if !bytes.Equal(Fingerprint("sales.createSale", a), Fingerprint("sales.createSale", b)) {
		t.Fatal("equivalent requests have different fingerprints")
	}
	if bytes.Equal(Fingerprint("sales.createSale", a), Fingerprint("sales.createSale", c)) {
		t.Fatal("different quantities share a fingerprint")
	}
	if bytes.Equal(Fingerprint("sales.createSale", a), Fingerprint("other.op", a)) {
		t.Fatal("different operations share a fingerprint")
	}
}
