package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
)

type contextKey int

const (
	requestIDKey contextKey = iota
	operationKey
	identityKey
)

// Identity is who is calling. v1 uses static demo identities: the point is to show
// authorization boundaries, not to build authentication (see plan section 5).
type Identity struct {
	Name        string
	Permissions map[string]bool
}

// Identities maps bearer tokens to identities.
type Identities map[string]Identity

// DemoIdentities returns the cashier and viewer identities from the v1 plan. Both
// tokens are public on purpose.
func DemoIdentities(cashierToken, viewerToken string) Identities {
	return Identities{
		cashierToken: {Name: "cashier", Permissions: map[string]bool{
			"catalog.read": true, "inventory.read": true, "sales.write": true,
		}},
		viewerToken: {Name: "viewer", Permissions: map[string]bool{
			"catalog.read": true, "inventory.read": true,
		}},
	}
}

type operationRoute struct {
	id       string
	kind     string
	requires []string
	route    *routers.Route
}

var defineFormats sync.Once

const maxRequestBytes = 1 << 20

// contractMiddleware enforces the parts of the contract that hold for every
// operation, using the generated OpenAPI document as the source of truth: rate
// limits on commands (x-modura-kind), authentication, the permissions listed in
// x-modura-requires, and request validation against the schemas. Handlers only
// ever see valid, authorized input. limiter may be nil to disable rate limiting.
func contractMiddleware(spec *openapi3.T, identities Identities, limiter *ipLimiter) (func(http.Handler) http.Handler, error) {
	defineFormats.Do(func() {
		openapi3.DefineStringFormatValidator("uuid",
			openapi3.NewRegexpFormatValidator(openapi3.FormatOfStringForUUIDOfRFC4122))
	})

	routes := map[string]operationRoute{}
	for path, item := range spec.Paths.Map() {
		operation := item.Post
		if operation == nil {
			continue
		}
		id, _ := operation.Extensions["x-modura-operation"].(string)
		kind, _ := operation.Extensions["x-modura-kind"].(string)
		requires, err := stringList(operation.Extensions["x-modura-requires"])
		if id == "" || kind == "" || err != nil || len(requires) == 0 {
			return nil, fmt.Errorf("%s: missing x-modura-operation, x-modura-kind or x-modura-requires", path)
		}
		routes[path] = operationRoute{id: id, kind: kind, requires: requires, route: &routers.Route{
			Spec: spec, Path: path, PathItem: item, Method: http.MethodPost, Operation: operation,
		}}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestID := newRequestID()
			w.Header().Set("X-Request-Id", requestID)
			ctx := context.WithValue(r.Context(), requestIDKey, requestID)

			op, ok := routes[r.URL.Path]
			if !ok || r.Method != http.MethodPost {
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
			ctx = context.WithValue(ctx, operationKey, op.id)
			r = r.WithContext(ctx)
			r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)

			if limiter != nil && op.kind == "command" && !limiter.allow(r) {
				w.Header().Set("Retry-After", fmt.Sprint(limiter.retryAfterSeconds()))
				writeError(w, r, codeRateLimited, "Too many checkouts from this address. Wait a moment and retry.", nil)
				return
			}

			identity, ok := identities[bearerToken(r)]
			if !ok {
				writeError(w, r, codeUnauthenticated, "Send a valid bearer token.", nil)
				return
			}
			for _, permission := range op.requires {
				if !identity.Permissions[permission] {
					writeError(w, r, codeForbidden,
						fmt.Sprintf("%s requires %s, which %s does not have.", op.id, permission, identity.Name),
						map[string]any{"permission": permission})
					return
				}
			}

			err := openapi3filter.ValidateRequest(ctx, &openapi3filter.RequestValidationInput{
				Request: r,
				Route:   op.route,
				Options: &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc},
			})
			if err != nil {
				message, details := describeValidationError(err)
				writeError(w, r, codeValidationFailed, message, details)
				return
			}

			next.ServeHTTP(w, r.WithContext(context.WithValue(ctx, identityKey, identity)))
		})
	}, nil
}

// describeValidationError turns kin-openapi's verbose errors into a short message
// plus the offending field, without leaking schema internals.
func describeValidationError(err error) (string, map[string]any) {
	var schemaErr *openapi3.SchemaError
	if errors.As(err, &schemaErr) {
		field := strings.Join(schemaErr.JSONPointer(), ".")
		if field == "" {
			return "Request body: " + schemaErr.Reason, nil
		}
		return field + ": " + schemaErr.Reason, map[string]any{"field": field}
	}
	var requestErr *openapi3filter.RequestError
	if errors.As(err, &requestErr) && requestErr.Parameter != nil {
		return fmt.Sprintf("%s %s: %s", requestErr.Parameter.In, requestErr.Parameter.Name, requestErr.Reason),
			map[string]any{"parameter": requestErr.Parameter.Name}
	}
	if errors.As(err, &requestErr) {
		return "Request body: " + requestErr.Reason, nil
	}
	return "The request does not match the operation input schema.", nil
}

func bearerToken(r *http.Request) string {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return ""
	}
	return strings.TrimSpace(token)
}

func stringList(value any) ([]string, error) {
	items, ok := value.([]any)
	if !ok {
		return nil, errors.New("not a list")
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		s, ok := item.(string)
		if !ok {
			return nil, errors.New("not a string")
		}
		out = append(out, s)
	}
	return out, nil
}

func newRequestID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "req_" + hex.EncodeToString(b)
}
