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

export interface AuthStatus {
  bootstrap_required: boolean;
  providers: string[];
}

export interface User {
  id: string;
  email: string;
  display_name?: string;
  role: 'admin' | 'member';
  provider: 'local' | 'oidc';
  created_at: string;
  last_login_at?: string;
}

export interface Session {
  user: User;
  /** Empty on GET /auth/session: only a hash is stored server-side. */
  csrf_token: string;
  expires_at: string;
}

/**
 * A machine on the network.
 *
 * Note what is absent: there is no private key field, because a device
 * generates its own key pair and sends only the public half.
 */
export interface Device {
  id: string;
  user_id: string;
  name: string;
  public_key: string;
  os?: string;
  hostname?: string;
  /** Cleared on revocation, when the address returns to the pool. */
  ipv4?: string;
  ipv6?: string;
  enrolled_with?: string;
  created_at: string;
  last_seen_at?: string;
  revoked_at?: string;
}

export interface DeviceList {
  devices: Device[];
}

export interface RegisterDeviceRequest {
  name: string;
  public_key: string;
  os?: string;
  hostname?: string;
}

/** The name of the cookie carrying the CSRF token. */
export const CSRF_COOKIE = 'headnet_csrf';

/** The header the CSRF token is echoed in. */
export const CSRF_HEADER = 'X-CSRF-Token';

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

  /** Whether this means the caller is not (or is no longer) signed in. */
  get unauthenticated(): boolean {
    return this.code === 'unauthorized';
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
  /**
   * Reads the CSRF token. Defaults to reading the cookie, and is injectable
   * because tests have no document.
   */
  readCsrfToken?: () => string | undefined;
}

const DEFAULT_TIMEOUT_MS = 10_000;

/** Reads a cookie by name, or undefined when it is absent. */
export function readCookie(name: string): string | undefined {
  if (typeof document === 'undefined') return undefined;
  for (const part of document.cookie.split(';')) {
    const [key, ...rest] = part.trim().split('=');
    if (key === name) return decodeURIComponent(rest.join('='));
  }
  return undefined;
}

interface RequestOptions {
  method?: string;
  body?: unknown;
  /** Statuses to treat as a successful answer rather than a failure. */
  acceptStatuses?: number[];
  /** True when the response carries no body. */
  empty?: boolean;
}

export class HeadnetClient {
  readonly #baseUrl: string;
  readonly #fetch: FetchLike;
  readonly #timeoutMs: number;
  readonly #readCsrfToken: () => string | undefined;

  constructor(options: ClientOptions = {}) {
    this.#baseUrl = (options.baseUrl ?? '').replace(/\/+$/, '');
    this.#fetch = options.fetch ?? ((input, init) => globalThis.fetch(input, init));
    this.#timeoutMs = options.timeoutMs ?? DEFAULT_TIMEOUT_MS;
    this.#readCsrfToken = options.readCsrfToken ?? (() => readCookie(CSRF_COOKIE));
  }

  // --- operations ----------------------------------------------------------

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

  // --- authentication ------------------------------------------------------

  /** Whether the installation still needs its first administrator. */
  async authStatus(): Promise<AuthStatus> {
    return this.#request<AuthStatus>('/api/v1/auth/status');
  }

  /** Creates the first administrator on a fresh installation. */
  async bootstrap(email: string, password: string, displayName?: string): Promise<Session> {
    return this.#request<Session>('/api/v1/auth/bootstrap', {
      method: 'POST',
      body: { email, password, ...(displayName ? { display_name: displayName } : {}) },
    });
  }

  async login(email: string, password: string): Promise<Session> {
    return this.#request<Session>('/api/v1/auth/login', {
      method: 'POST',
      body: { email, password },
    });
  }

  async logout(): Promise<void> {
    await this.#request<void>('/api/v1/auth/logout', { method: 'POST', empty: true });
  }

  /** The caller's current session, or an `unauthorized` ApiError. */
  async session(): Promise<Session> {
    return this.#request<Session>('/api/v1/auth/session');
  }

  // --- devices -------------------------------------------------------------

  async devices(): Promise<Device[]> {
    const list = await this.#request<DeviceList>('/api/v1/devices');
    return list.devices ?? [];
  }

  async registerDevice(device: RegisterDeviceRequest): Promise<Device> {
    return this.#request<Device>('/api/v1/devices', { method: 'POST', body: device });
  }

  async revokeDevice(id: string): Promise<void> {
    await this.#request<void>(`/api/v1/devices/${encodeURIComponent(id)}`, {
      method: 'DELETE',
      empty: true,
    });
  }

  // --- transport -----------------------------------------------------------

  async #request<T>(path: string, options: RequestOptions = {}): Promise<T> {
    const method = options.method ?? 'GET';
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), this.#timeoutMs);

    const headers: Record<string, string> = { Accept: 'application/json' };
    if (options.body !== undefined) {
      headers['Content-Type'] = 'application/json';
    }
    // Safe methods do not need a CSRF token, and sending one on every read
    // would be noise. Anything that changes state must carry it.
    if (!['GET', 'HEAD', 'OPTIONS'].includes(method)) {
      const token = this.#readCsrfToken();
      if (token) headers[CSRF_HEADER] = token;
    }

    let response: Response;
    try {
      response = await this.#fetch(`${this.#baseUrl}${path}`, {
        method,
        headers,
        signal: controller.signal,
        // The API is same-origin and cookie-authenticated, so credentials must
        // ride along.
        credentials: 'same-origin',
        ...(options.body !== undefined ? { body: JSON.stringify(options.body) } : {}),
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
    const accepted = options.acceptStatuses ?? [200, 201, 204];

    if (!response.ok && !accepted.includes(response.status)) {
      throw await toApiError(response, requestId);
    }

    if (options.empty || response.status === 204) {
      return undefined as T;
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
