import {
  createHttpTransport,
  createModuraClient,
  type ModuraClient,
  type ModuraError,
  type Transport,
} from '@modura/pos-sdk';

export type Role = 'cashier' | 'viewer';

// Public demo tokens. They show authorization boundaries, not authentication (plan section 5).
const tokens: Record<Role, string> = {
  cashier: import.meta.env.VITE_CASHIER_TOKEN ?? 'demo-cashier',
  viewer: import.meta.env.VITE_VIEWER_TOKEN ?? 'demo-viewer',
};

// Each service is called directly, so neither forwards the other's requests (ADR-0011).
// The Go service serves this UI, so it is the page's own origin; reports-py allows
// this origin through CORS.
const serviceUrls = {
  'reports-py': import.meta.env.VITE_REPORTS_URL ?? 'http://localhost:8090',
};

/** One command as the internals panel shows it. */
export interface CallRecord {
  readonly operation: string;
  readonly idempotencyKey?: string;
  readonly input: unknown;
  readonly at: Date;
  readonly outcome:
    | { readonly ok: true; readonly value: unknown; readonly replayed: boolean }
    | { readonly ok: false; readonly error: ModuraError };
}

/**
 * The generated client on top of the SDK's own transport. The wrapper only records
 * commands for the internals panel; it never touches the network itself.
 */
export function createClient(role: Role, onCommand: (record: CallRecord) => void): ModuraClient {
  const http = createHttpTransport({ baseUrl: window.location.origin, serviceUrls, token: tokens[role] });
  const recording: Transport = {
    async call(operation, input, options) {
      const result = await http.call(operation, input, options);
      if (options?.idempotencyKey !== undefined) {
        onCommand({
          operation,
          idempotencyKey: options.idempotencyKey,
          input,
          at: new Date(),
          outcome: result.ok
            ? { ok: true, value: result.value, replayed: result.replayed }
            : { ok: false, error: result.error },
        });
      }
      return result as never;
    },
  };
  return createModuraClient(recording);
}
