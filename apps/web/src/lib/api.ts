/**
 * Typed client for the Headnet control-plane API.
 *
 * The types here mirror `packages/api` in the Go module, which is the single
 * source of truth. They are hand-written rather than generated so that the
 * frontend has no build-time dependency on the Go toolchain; the OpenAPI
 * document in `docs/api/openapi.yaml` describes the same contract, and
 * generating this file from it is a task for when the surface grows large
 * enough to be worth the machinery.
 */

/** Stable, machine-readable failure classifications. */
export type ErrorCode =
  | 'bad_request'
  | 'unauthorized'
  | 'forbidden'
  | 'not_found'
  | 'conflict'
  | 'payload_too_large'
  | 'rate_limited'
  | 'unsupported_protocol_version'
  | 'not_implemented'
  | 'unavailable'
  | 'internal';

/** The body of every non-2xx response. */
export interface ApiErrorBody {
  code: ErrorCode;
  message: string;
  details?: Record<string, string>;
  request_id?: string;
}

export type HealthStatus = 'ok' | 'degraded' | 'down';

export interface HealthCheck {
  name: string;
  status: HealthStatus;
  detail?: string;
  duration_ms: number;
}

export interface HealthResponse {
  status: HealthStatus;
  version: string;
  uptime_seconds: number;
  checks: HealthCheck[];
}

export interface VersionResponse {
  version: string;
  commit: string;
  build_date: string;
  protocol_version: number;
  min_protocol_version: number;
  capabilities: string[];
}

/**
 * An error carrying the server's structured envelope.
 *
 * `requestId` is surfaced in the UI so a user reporting a problem can quote it
 * and an operator can find the matching server-side log line.
 */
export class ApiError extends Error {
  readonly code: ErrorCode | 'network' | 'malformed_response';
  readonly status: number;
  readonly requestId: string | undefined;
  readonly details: Record<string, string> | undefined;

  constructor(
    message: string,
    options: {
      code: ErrorCode | 'network' | 'malformed_response';
      status: number;
      requestId?: string | undefined;
      details?: Record<string, string> | undefined;
    },
  ) {
    super(message);
    this.name = 'ApiError';
    this.code = options.code;
    this.status = options.status;
    this.requestId = options.requestId;
    this.details = options.details;
  }

  /** Whether retrying the same request later could plausibly succeed. */
  get retryable(): boolean {
    return (
      this.code === 'network' ||
      this.code === 'rate_limited' ||
      this.code === 'unavailable' ||
      this.code === 'internal'
    );
  }
}

/** The subset of `fetch` this client needs, so tests can supply their own. */
export type FetchLike = (input: string, init?: RequestInit) => Promise<Response>;

export interface ClientOptions {
  /** Base URL of the control plane. Empty means same-origin. */
  baseUrl?: string;
  /** Injected for testing. */
  fetch?: FetchLike;
  /** Abandon a request after this many milliseconds. */
  timeoutMs?: number;
}

const DEFAULT_TIMEOUT_MS = 10_000;

export class HeadnetClient {
  readonly #baseUrl: string;
  readonly #fetch: FetchLike;
  readonly #timeoutMs: number;

  constructor(options: ClientOptions = {}) {
    this.#baseUrl = (options.baseUrl ?? '').replace(/\/+$/, '');
    this.#fetch = options.fetch ?? ((input, init) => globalThis.fetch(input, init));
    this.#timeoutMs = options.timeoutMs ?? DEFAULT_TIMEOUT_MS;
  }

  /**
   * Reads the aggregate health of the control plane.
   *
   * A 503 is a real answer here, not a failure: it means the server is running
   * and telling us a dependency is broken. Throwing it away would lose exactly
   * the information the dashboard exists to show.
   */
  async health(): Promise<HealthResponse> {
    return this.#request<HealthResponse>('/health', { acceptStatuses: [200, 503] });
  }

  /** Reads the server build and the protocol range it speaks. */
  async version(): Promise<VersionResponse> {
    return this.#request<VersionResponse>('/api/v1/version');
  }

  async #request<T>(path: string, options: { acceptStatuses?: number[] } = {}): Promise<T> {
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), this.#timeoutMs);

    let response: Response;
    try {
      response = await this.#fetch(`${this.#baseUrl}${path}`, {
        headers: { Accept: 'application/json' },
        signal: controller.signal,
        // The API is same-origin and cookie-authenticated from Phase 1
        // onwards, so credentials must ride along.
        credentials: 'same-origin',
      });
    } catch (cause) {
      throw new ApiError(
        cause instanceof Error && cause.name === 'AbortError'
          ? 'The control server did not respond in time.'
          : 'The control server could not be reached.',
        { code: 'network', status: 0 },
      );
    } finally {
      clearTimeout(timer);
    }

    const requestId = response.headers.get('X-Request-Id') ?? undefined;
    const accepted = options.acceptStatuses ?? [200];

    if (!response.ok && !accepted.includes(response.status)) {
      throw await toApiError(response, requestId);
    }

    try {
      return (await response.json()) as T;
    } catch {
      throw new ApiError('The control server returned a response that could not be read.', {
        code: 'malformed_response',
        status: response.status,
        requestId,
      });
    }
  }
}

/** Converts an error response into an ApiError, tolerating a non-JSON body. */
async function toApiError(response: Response, requestId: string | undefined): Promise<ApiError> {
  let body: { error?: ApiErrorBody } | undefined;
  try {
    body = (await response.json()) as { error?: ApiErrorBody };
  } catch {
    // A proxy or load balancer in front of the server may return HTML.
    body = undefined;
  }

  const envelope = body?.error;
  if (!envelope) {
    return new ApiError(`The control server returned HTTP ${response.status}.`, {
      code: 'internal',
      status: response.status,
      requestId,
    });
  }

  return new ApiError(envelope.message, {
    code: envelope.code,
    status: response.status,
    requestId: envelope.request_id ?? requestId,
    details: envelope.details,
  });
}
