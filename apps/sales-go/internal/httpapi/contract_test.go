package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/jackc/pgx/v5/pgxpool"

	"modura/apps/sales-go/internal/api"
	"modura/apps/sales-go/internal/store"
	"modura/apps/sales-go/internal/testdb"
)

const (
	cashier = "cashier-token"
	viewer  = "viewer-token"
)

type harness struct {
	t      *testing.T
	server *httptest.Server
	spec   *openapi3.T
	db     *pgxpool.Pool
}

func newHarness(t *testing.T) *harness {
	return newHarnessWith(t, store.Options{})
}

func newHarnessWith(t *testing.T, opts store.Options) *harness {
	return newHarnessConfig(t, opts, nil)
}

// newHarnessConfig lets a test adjust the handler Config, for example to turn on
// rate limiting.
func newHarnessConfig(t *testing.T, opts store.Options, adjust func(*Config)) *harness {
	t.Helper()
	db := testdb.New(t)
	cfg := Config{
		Store:      store.New(db, opts),
		Identities: DemoIdentities(cashier, viewer),
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if adjust != nil {
		adjust(&cfg)
	}
	handler, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := api.GetSwagger()
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return &harness{t: t, server: server, spec: spec, db: db}
}

type response struct {
	status int
	header http.Header
	body   map[string]any
}

// call sends a real HTTP request and checks the real response against the generated
// OpenAPI document: the status must be declared, the body must match its schema, and
// an error code must be one the contract lists for that status.
//
// It is safe to use from several goroutines: failures are reported with Errorf.
func (h *harness) call(token, operation string, body any, headers map[string]string) response {
	h.t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		h.t.Errorf("marshal request: %v", err)
		return response{}
	}
	path := "/v1/" + operation
	req, err := http.NewRequest(http.MethodPost, h.server.URL+path, bytes.NewReader(payload))
	if err != nil {
		h.t.Errorf("build request: %v", err)
		return response{}
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Errorf("send request: %v", err)
		return response{}
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		h.t.Errorf("read response: %v", err)
		return response{}
	}

	item := h.spec.Paths.Value(path)
	if item == nil || item.Post == nil {
		h.t.Errorf("%s is not in the contract", path)
		return response{}
	}
	options := &openapi3filter.Options{
		IncludeResponseStatus: true,
		AuthenticationFunc:    openapi3filter.NoopAuthenticationFunc,
	}
	err = openapi3filter.ValidateResponse(context.Background(), &openapi3filter.ResponseValidationInput{
		RequestValidationInput: &openapi3filter.RequestValidationInput{
			Request: req,
			Route:   &routers.Route{Spec: h.spec, Path: path, PathItem: item, Method: http.MethodPost, Operation: item.Post},
			Options: options,
		},
		Status:  res.StatusCode,
		Header:  res.Header,
		Body:    io.NopCloser(bytes.NewReader(raw)),
		Options: options,
	})
	if err != nil {
		h.t.Errorf("%s %d response violates the contract: %v\nbody: %s", operation, res.StatusCode, err, raw)
	}

	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		h.t.Errorf("response is not JSON: %s", raw)
	}
	if declared := item.Post.Responses.Status(res.StatusCode); res.StatusCode >= 400 && declared != nil {
		codes, _ := stringList(declared.Value.Extensions["x-modura-error-codes"])
		if code, _ := decoded["code"].(string); !slices.Contains(codes, code) {
			h.t.Errorf("%s returned %q with status %d, but the contract lists %v", operation, code, res.StatusCode, codes)
		}
	}
	return response{status: res.StatusCode, header: res.Header, body: decoded}
}

func expect(t *testing.T, got response, status int, code string) {
	t.Helper()
	if got.status != status {
		t.Fatalf("status = %d, want %d (body %v)", got.status, status, got.body)
	}
	if code != "" && got.body["code"] != code {
		t.Fatalf("code = %v, want %s (body %v)", got.body["code"], code, got.body)
	}
}

func TestListProductsReturnsActiveProductsOnly(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	res := h.call(viewer, "catalog.listProducts", map[string]any{}, nil)

	expect(t, res, http.StatusOK, "")
	products := res.body["products"].([]any)
	if len(products) != 7 {
		t.Fatalf("got %d products, want the 7 active ones", len(products))
	}
	for _, p := range products {
		if p.(map[string]any)["id"] == "prd_seasonal_latte" {
			t.Fatal("inactive product was listed")
		}
	}
	first := products[0].(map[string]any)
	if first["id"] != "prd_banana_bread" {
		t.Fatalf("first product = %v, want category then name order", first["id"])
	}
}

func TestGetStock(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	cases := []struct {
		name      string
		input     map[string]any
		status    int
		code      string
		available float64
	}{
		{"known product", map[string]any{"productId": "prd_croissant"}, 200, "", 3},
		{"known product with no stock", map[string]any{"productId": "prd_seasonal_latte"}, 200, "", 0},
		{"unknown product", map[string]any{"productId": "prd_nope"}, 404, codeProductNotFound, 0},
		{"malformed id", map[string]any{"productId": "espresso"}, 400, codeValidationFailed, 0},
		{"unknown field", map[string]any{"productId": "prd_espresso", "extra": true}, 400, codeValidationFailed, 0},
		{"missing field", map[string]any{}, 400, codeValidationFailed, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := h.call(viewer, "inventory.getStock", tc.input, nil)
			expect(t, res, tc.status, tc.code)
			if tc.status == 200 && res.body["available"] != tc.available {
				t.Fatalf("available = %v, want %v", res.body["available"], tc.available)
			}
		})
	}
}

func TestAuthorizationComesFromTheContract(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	sale := map[string]any{"lines": []any{map[string]any{"productId": "prd_espresso", "quantity": 1}}}
	key := map[string]string{"Idempotency-Key": "4b0ff1b6-6a3c-4d3e-9a51-2a8e8f3f1c10"}

	expect(t, h.call("", "inventory.getStock", map[string]any{"productId": "prd_espresso"}, nil), 401, codeUnauthenticated)
	expect(t, h.call("wrong", "inventory.getStock", map[string]any{"productId": "prd_espresso"}, nil), 401, codeUnauthenticated)

	forbidden := h.call(viewer, "sales.createSale", sale, key)
	expect(t, forbidden, 403, codeForbidden)
	if details, _ := forbidden.body["details"].(map[string]any); details["permission"] != "sales.write" {
		t.Fatalf("details = %v, want the missing permission", forbidden.body["details"])
	}

	expect(t, h.call(cashier, "sales.createSale", sale, key), 200, "")
}

func TestIdempotencyKeyIsRequiredAndValidated(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	sale := map[string]any{"lines": []any{map[string]any{"productId": "prd_espresso", "quantity": 1}}}

	missing := h.call(cashier, "sales.createSale", sale, nil)
	expect(t, missing, 400, codeValidationFailed)
	if details, _ := missing.body["details"].(map[string]any); details["parameter"] != "Idempotency-Key" {
		t.Fatalf("details = %v, want the missing header", missing.body["details"])
	}

	expect(t, h.call(cashier, "sales.createSale", sale, map[string]string{"Idempotency-Key": "not-a-uuid"}), 400, codeValidationFailed)
}

// TestErrorCatalogMatchesIR keeps the Go error table in sync with the contract.
func TestErrorCatalogMatchesIR(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(testdb.Root(t), "generated", "ir", "modura.ir.json"))
	if err != nil {
		t.Fatal(err)
	}
	type definition struct {
		Category  string `json:"category"`
		Retryable bool   `json:"retryable"`
	}
	var ir struct {
		PlatformErrors map[string]definition `json:"platformErrors"`
		Modules        map[string]struct {
			Errors map[string]definition `json:"errors"`
		} `json:"modules"`
	}
	if err := json.Unmarshal(raw, &ir); err != nil {
		t.Fatal(err)
	}
	contract := map[string]definition{}
	for code, def := range ir.PlatformErrors {
		contract[code] = def
		if _, ok := errorCatalog[code]; !ok {
			t.Errorf("platform error %s is missing from the Go catalog", code)
		}
	}
	for module, m := range ir.Modules {
		for code, def := range m.Errors {
			contract[module+"."+code] = def
		}
	}
	for code, spec := range errorCatalog {
		def, ok := contract[code]
		if !ok {
			t.Errorf("Go catalog has %s, which the contract does not define", code)
			continue
		}
		if def.Category != spec.category || def.Retryable != spec.retryable {
			t.Errorf("%s: Go has (%s, retryable=%v), contract has (%s, retryable=%v)",
				code, spec.category, spec.retryable, def.Category, def.Retryable)
		}
		if _, ok := categoryStatus[spec.category]; !ok {
			t.Errorf("%s: category %s has no HTTP status", code, spec.category)
		}
	}
}
