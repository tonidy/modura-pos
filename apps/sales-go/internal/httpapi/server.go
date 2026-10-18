// Package httpapi serves the generated Modura HTTP contract.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/time/rate"

	"modura/apps/sales-go/internal/api"
	"modura/apps/sales-go/internal/checkout"
	"modura/apps/sales-go/internal/store"
)

type Config struct {
	Store      *store.Store
	Identities Identities
	Logger     *slog.Logger

	// ClientIPHeader is a header set by a trusted proxy, such as Fly-Client-IP.
	// Empty means the TCP peer address identifies the client.
	ClientIPHeader string
	// CommandRateLimit is how many commands per second one client IP may send,
	// with CommandBurst on top. Zero disables rate limiting.
	CommandRateLimit rate.Limit
	CommandBurst     int

	// WebDir, if set, is the built POS UI, served at / from the same origin.
	WebDir string
	// ConnectSrc lists other origins the UI may call, such as services that own
	// other modules (ADR-0011). They are added to the UI's CSP connect-src.
	ConnectSrc []string
	// DemoStatus, if set, adds the demo jobs' status to /healthz.
	DemoStatus func() any
}

// New returns the complete HTTP handler: generated routes behind the contract
// middleware, an unauthenticated health check, and optionally the POS UI.
func New(cfg Config) (http.Handler, error) {
	spec, err := api.GetSwagger()
	if err != nil {
		return nil, err
	}
	var limiter *ipLimiter
	if cfg.CommandRateLimit > 0 {
		limiter = newIPLimiter(cfg.ClientIPHeader, cfg.CommandRateLimit, max(1, cfg.CommandBurst))
	}
	middleware, err := contractMiddleware(spec, cfg.Identities, limiter)
	if err != nil {
		return nil, err
	}

	s := &server{store: cfg.Store, logger: cfg.Logger, demoStatus: cfg.DemoStatus}
	strict := api.NewStrictHandlerWithOptions(s, nil, api.StrictHTTPServerOptions{
		RequestErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			writeError(w, r, codeValidationFailed, "The request body is not valid JSON.", nil)
		},
		ResponseErrorHandlerFunc: s.handleError,
	})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	if cfg.WebDir != "" {
		csp, err := contentSecurityPolicy(cfg.ConnectSrc)
		if err != nil {
			return nil, err
		}
		mux.Handle("GET /", webHandler(cfg.WebDir, csp))
	}
	api.HandlerWithOptions(strict, api.StdHTTPServerOptions{
		BaseRouter: mux,
		ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			writeError(w, r, codeValidationFailed, err.Error(), nil)
		},
	})
	return middleware(mux), nil
}

// server implements api.StrictServerInterface, which oapi-codegen derives from the
// contract. Adding an operation to a module without implementing it here fails to compile.
type server struct {
	store      *store.Store
	logger     *slog.Logger
	demoStatus func() any
}

var _ api.StrictServerInterface = (*server)(nil)

func (s *server) CatalogListProducts(ctx context.Context, _ api.CatalogListProductsRequestObject) (api.CatalogListProductsResponseObject, error) {
	products, err := s.store.ListActiveProducts(ctx)
	if err != nil {
		return nil, err
	}
	out := api.CatalogListProducts200JSONResponse{Products: make([]api.CatalogProduct, 0, len(products))}
	for _, p := range products {
		out.Products = append(out.Products, api.CatalogProduct{
			Id:       p.ID,
			Sku:      p.SKU,
			Name:     p.Name,
			Category: p.Category,
			Price:    api.Money{AmountMinor: p.PriceMinor, Currency: p.Currency},
		})
	}
	return out, nil
}

func (s *server) InventoryGetStock(ctx context.Context, request api.InventoryGetStockRequestObject) (api.InventoryGetStockResponseObject, error) {
	level, err := s.store.GetStock(ctx, request.Body.ProductId)
	if errors.Is(err, store.ErrNotFound) {
		_, body := newError(ctx, codeProductNotFound, "No product has this identifier.",
			map[string]any{"productId": request.Body.ProductId})
		return api.InventoryGetStock404JSONResponse(body), nil
	}
	if err != nil {
		return nil, err
	}
	return api.InventoryGetStock200JSONResponse{
		ProductId: level.ProductID,
		Available: level.Available,
		UpdatedAt: level.UpdatedAt.UTC(),
	}, nil
}

func (s *server) SalesCreateSale(ctx context.Context, request api.SalesCreateSaleRequestObject) (api.SalesCreateSaleResponseObject, error) {
	requested := make([]checkout.Line, len(request.Body.Lines))
	for i, line := range request.Body.Lines {
		requested[i] = checkout.Line{ProductID: line.ProductId, Quantity: line.Quantity}
	}
	lines, err := checkout.Normalize(requested)
	var quantityErr *checkout.QuantityError
	if errors.As(err, &quantityErr) {
		_, body := newError(ctx, codeValidationFailed,
			fmt.Sprintf("Lines for %s add up to %d units; the limit is %d.", quantityErr.ProductID, quantityErr.Quantity, checkout.MaxLineQuantity),
			map[string]any{"productId": quantityErr.ProductID})
		return api.SalesCreateSale400JSONResponse(body), nil
	}

	// The contract middleware only lets authenticated callers through.
	identity := ctx.Value(identityKey).(Identity)
	outcome, err := s.store.CreateSale(ctx, store.SaleRequest{
		Scope:       identity.Name,
		Key:         request.Params.IdempotencyKey.String(),
		Fingerprint: checkout.Fingerprint("sales.createSale", lines),
		Lines:       lines,
	})

	var rejection *store.Rejection
	switch {
	case errors.As(err, &rejection):
		_, body := newError(ctx, rejectionCodes[rejection.Reason], rejection.Message, rejection.Details)
		return api.SalesCreateSale422JSONResponse(body), nil
	case errors.Is(err, store.ErrIdempotencyKeyReused):
		_, body := newError(ctx, codeIdempotencyKeyReused,
			"This idempotency key was already used for a different sale. Use a new key per checkout.", nil)
		return api.SalesCreateSale409JSONResponse(body), nil
	case errors.Is(err, store.ErrIdempotencyInProgress):
		_, body := newError(ctx, codeIdempotencyInProgress,
			"A request with this idempotency key is still running. Retry shortly with the same key.", nil)
		return api.SalesCreateSale409JSONResponse(body), nil
	case err != nil:
		return nil, err
	}

	return api.SalesCreateSale200JSONResponse{
		Body:    toAPISale(outcome.Sale),
		Headers: api.SalesCreateSale200ResponseHeaders{IdempotentReplayed: &outcome.Replayed},
	}, nil
}

var rejectionCodes = map[store.RejectionReason]string{
	store.UnknownProduct:     codeUnknownProduct,
	store.ProductNotSellable: codeProductNotSellable,
	store.InsufficientStock:  codeInsufficientStock,
}

func toAPISale(sale store.Sale) api.SalesSale {
	out := api.SalesSale{
		Id:        sale.ID,
		Total:     api.Money(sale.Total),
		CreatedAt: sale.CreatedAt,
		Lines:     make([]api.SalesSaleLine, 0, len(sale.Lines)),
	}
	for _, line := range sale.Lines {
		out.Lines = append(out.Lines, api.SalesSaleLine{
			ProductId: line.ProductID,
			Quantity:  line.Quantity,
			UnitPrice: api.Money(line.UnitPrice),
			LineTotal: api.Money(line.LineTotal),
		})
	}
	return out
}

// handleError turns an unexpected handler error into INTERNAL or UNAVAILABLE.
// Details go to the log, keyed by request ID, never to the client.
func (s *server) handleError(w http.ResponseWriter, r *http.Request, err error) {
	code, message := codeInternal, "An unexpected error occurred."
	if store.IsUnavailable(err) {
		code, message = codeUnavailable, "The database is temporarily unavailable."
	}
	requestID, _ := r.Context().Value(requestIDKey).(string)
	s.logger.Error("request failed", "requestId", requestID, "path", r.URL.Path, "error", err)
	writeError(w, r, code, message, nil)
}

func (s *server) health(w http.ResponseWriter, r *http.Request) {
	body := map[string]any{"status": "ok"}
	if s.demoStatus != nil {
		body["demo"] = s.demoStatus()
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := s.store.Ping(r.Context()); err != nil {
		body["status"] = "database unavailable"
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	_ = json.NewEncoder(w).Encode(body)
}

// webHandler serves the built single-page app. Unknown paths get index.html so
// client-side routes work; hashed assets are cached forever, index.html never.
func webHandler(dir, csp string) http.Handler {
	files := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", csp)
		w.Header().Set("Referrer-Policy", "no-referrer")

		path := filepath.Clean("/" + r.URL.Path)
		if info, err := os.Stat(filepath.Join(dir, path)); err != nil || info.IsDir() {
			w.Header().Set("Cache-Control", "no-cache")
			http.ServeFile(w, r, filepath.Join(dir, "index.html"))
			return
		}
		if strings.HasPrefix(path, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		files.ServeHTTP(w, r)
	})
}

// contentSecurityPolicy allows the UI's own origin plus connectSrc for fetches. Each
// entry must be a bare http(s) origin, so configuration cannot smuggle in directives.
func contentSecurityPolicy(connectSrc []string) (string, error) {
	connect := "'self'"
	for _, origin := range connectSrc {
		u, err := url.Parse(origin)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
			u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" ||
			strings.ContainsAny(origin, " ;,'\"") {
			return "", fmt.Errorf("connect-src entry %q is not an http(s) origin", origin)
		}
		connect += " " + u.Scheme + "://" + u.Host
	}
	return "default-src 'self'; connect-src " + connect +
		"; img-src 'self' data:; style-src 'self'; frame-ancestors 'none'", nil
}
