package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"modura/apps/sales-go/internal/store"
	"modura/apps/sales-go/internal/testdb"
)

// Tests for the operational pieces added in M3: rate limiting and serving the UI.

func TestCommandsAreRateLimitedPerClientIP(t *testing.T) {
	t.Parallel()
	h := newHarnessConfig(t, store.Options{}, func(cfg *Config) {
		cfg.ClientIPHeader = "Fly-Client-IP"
		cfg.CommandRateLimit = 0.001 // effectively no refill during the test
		cfg.CommandBurst = 2
	})
	from := func(ip string) map[string]string {
		return map[string]string{"Idempotency-Key": uuid.NewString(), "Fly-Client-IP": ip}
	}
	sale := saleOf(line{"prd_espresso", 1})

	expect(t, h.call(cashier, "sales.createSale", sale, from("203.0.113.7")), http.StatusOK, "")
	expect(t, h.call(cashier, "sales.createSale", sale, from("203.0.113.7")), http.StatusOK, "")
	limited := h.call(cashier, "sales.createSale", sale, from("203.0.113.7"))
	expect(t, limited, http.StatusTooManyRequests, codeRateLimited)
	if limited.header.Get("Retry-After") == "" || limited.body["retryable"] != true {
		t.Fatalf("RATE_LIMITED should be retryable with Retry-After, got %v %v", limited.header, limited.body)
	}

	// Another client has its own bucket, and queries are never limited.
	expect(t, h.call(cashier, "sales.createSale", sale, from("198.51.100.4")), http.StatusOK, "")
	for range 5 {
		expect(t, h.call(viewer, "inventory.getStock", map[string]any{"productId": "prd_espresso"},
			map[string]string{"Fly-Client-IP": "203.0.113.7"}), http.StatusOK, "")
	}
}

func TestWebUIIsServedWithSPAFallbackAndStrictHeaders(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html><title>POS</title>"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "assets", "app-1234.js"), []byte("console.log(1)"), 0o644)

	handler, err := New(Config{
		Store:      store.New(testdb.New(t), store.Options{}),
		Identities: DemoIdentities(cashier, viewer),
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		WebDir:     dir,
		ConnectSrc: []string{"http://localhost:8090"},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	get := func(path string) (*http.Response, string) {
		res, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		body, _ := io.ReadAll(res.Body)
		return res, string(body)
	}

	res, body := get("/some/client/route")
	if res.StatusCode != 200 || !strings.Contains(body, "<title>POS</title>") || res.Header.Get("Cache-Control") != "no-cache" {
		t.Fatalf("SPA fallback: %d %q %v", res.StatusCode, body, res.Header)
	}
	if csp := res.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") ||
		!strings.Contains(csp, "connect-src 'self' http://localhost:8090;") {
		t.Fatalf("Content-Security-Policy: %q", csp)
	}
	res, _ = get("/assets/app-1234.js")
	if !strings.Contains(res.Header.Get("Cache-Control"), "immutable") {
		t.Fatalf("hashed assets should be immutable, got %q", res.Header.Get("Cache-Control"))
	}
	res, _ = get("/../../etc/passwd")
	if res.StatusCode != 200 || res.Header.Get("Cache-Control") != "no-cache" {
		t.Fatal("path traversal should fall back to index.html")
	}
	res, _ = get("/healthz")
	if res.Header.Get("Content-Type") != "application/json" {
		t.Fatal("/healthz must still reach the health handler")
	}
}

func TestConnectSrcAcceptsOnlyOrigins(t *testing.T) {
	t.Parallel()
	for _, origin := range []string{"https://reports.example", "http://localhost:8090", "http://localhost:8090/"} {
		if _, err := contentSecurityPolicy([]string{origin}); err != nil {
			t.Errorf("%s: %v", origin, err)
		}
	}
	for _, origin := range []string{
		"*", "localhost:8090", "javascript:alert(1)", "https://a.example/path", "https://a.example?q",
		"https://a.example; script-src *", "https://user@a.example", "'unsafe-inline'",
	} {
		if _, err := contentSecurityPolicy([]string{origin}); err == nil {
			t.Errorf("%q should be refused", origin)
		}
	}
}
