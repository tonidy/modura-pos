package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// The claim-first idempotency protocol from ADR-0004, shared by every idempotent
// command. All three steps run inside the command's own transaction.

// claimKey inserts the idempotency row for (scope, key). A concurrent claim of the
// same key waits on the primary key until its owner finishes. It returns false when
// another request already completed the key; the caller then replays storedResponse.
func claimKey(ctx context.Context, tx pgx.Tx, scope, key, operation string, fingerprint []byte, timeout time.Duration) (bool, error) {
	if err := setLockTimeout(ctx, tx, timeout); err != nil {
		return false, err
	}
	claim, err := tx.Exec(ctx, `
		INSERT INTO idempotency_keys (scope, key, operation, fingerprint)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (scope, key) DO NOTHING`,
		scope, key, operation, fingerprint)
	if isLockTimeout(err) {
		return false, ErrIdempotencyInProgress
	}
	if err != nil {
		return false, fmt.Errorf("claim idempotency key: %w", err)
	}
	return claim.RowsAffected() == 1, nil
}

// storedResponse returns the body a completed request stored for (scope, key), or
// ErrIdempotencyKeyReused if this request is a different one.
func storedResponse(ctx context.Context, tx pgx.Tx, scope, key string, fingerprint []byte) ([]byte, error) {
	var stored, body []byte
	err := tx.QueryRow(ctx, `
		SELECT fingerprint, response_body FROM idempotency_keys
		WHERE scope = $1 AND key = $2`, scope, key).Scan(&stored, &body)
	if err != nil {
		return nil, fmt.Errorf("read idempotency key: %w", err)
	}
	if !bytes.Equal(stored, fingerprint) {
		return nil, ErrIdempotencyKeyReused
	}
	if body == nil {
		// Claims and completions share a transaction, so this would be a bug.
		return nil, errors.New("idempotency key is committed without a response")
	}
	return body, nil
}

// completeKey stores the response so retries can replay it.
func completeKey(ctx context.Context, tx pgx.Tx, scope, key string, body []byte) error {
	_, err := tx.Exec(ctx, `
		UPDATE idempotency_keys SET response_status = 200, response_body = $3
		WHERE scope = $1 AND key = $2`, scope, key, body)
	if err != nil {
		return fmt.Errorf("complete idempotency key: %w", err)
	}
	return nil
}
