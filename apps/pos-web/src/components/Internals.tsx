import type { CallRecord } from '../lib/api';

interface Props {
  readonly pendingKey: string | null;
  readonly last: CallRecord | undefined;
  readonly retryNote: string | null;
  readonly canRetry: boolean;
  readonly retrying: boolean;
  readonly onSimulateRetry: () => void;
}

/**
 * Shows what checkout sends and receives, and lets a reviewer replay the last
 * checkout with the same idempotency key, as a client would after a timeout.
 */
export function Internals({ pendingKey, last, retryNote, canRetry, retrying, onSimulateRetry }: Props) {
  return (
    <section className="internals" aria-labelledby="internals-heading">
      <div className="internals-header">
        <h2 id="internals-heading">Internals</h2>
        <button type="button" onClick={onSimulateRetry} disabled={!canRetry || retrying}>
          {retrying ? 'Retrying…' : 'Simulate retry'}
        </button>
      </div>
      <p className="internals-help">
        <strong>Simulate retry</strong> resends the last checkout with the same{' '}
        <code>Idempotency-Key</code>, like a client retrying after a dropped connection. The server
        replays the stored sale instead of selling again, so stock does not move.
      </p>
      {retryNote && <p className="retry-note" role="status">{retryNote}</p>}

      <dl className="facts">
        <dt>Key for the next charge</dt>
        <dd>
          <code>{pendingKey ?? 'a new UUID is created when you press Charge'}</code>
        </dd>
      </dl>

      {last ? (
        <div className="exchange">
          <div>
            <h3>Request</h3>
            <pre>
              {`POST /v1/${last.operation}\nIdempotency-Key: ${last.idempotencyKey}\n\n${JSON.stringify(last.input, null, 2)}`}
            </pre>
          </div>
          <div>
            <h3>Response</h3>
            <pre>
              {last.outcome.ok
                ? `Idempotent-Replayed: ${last.outcome.replayed}\n\n${JSON.stringify(last.outcome.value, null, 2)}`
                : JSON.stringify(last.outcome.error, null, 2)}
            </pre>
          </div>
        </div>
      ) : (
        <p className="empty">Charge an order to see the request and response here.</p>
      )}
    </section>
  );
}
