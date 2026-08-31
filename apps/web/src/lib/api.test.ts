import { describe, expect, it, vi, type Mock } from 'vitest';

import { ApiError, CSRF_HEADER, HeadnetClient, type FetchLike } from './api';

/**
 * Asserts that a request fails with an ApiError and returns it, narrowed.
 *
 * Using `.catch(e => e as ApiError)` would leave the value typed as a union
 * with the success type, so a test could silently assert against a
 * successful response.
 */
async function expectApiError(promise: Promise<unknown>): Promise<ApiError> {
  try {
    await promise;
  } catch (cause) {
    if (cause instanceof ApiError) return cause;
    throw cause;
  }
  throw new Error('expected the request to fail, but it succeeded');
}

/** Statuses the Response constructor refuses to give a body. */
const NULL_BODY_STATUSES = new Set([101, 204, 205, 304]);

/**
 * A fetch stub. Typed as a Mock so tests can inspect the calls, and as a
 * FetchLike so it can be handed to the client.
 */
type FetchStub = FetchLike & Mock<(input: string, init?: RequestInit) => Promise<Response>>;

/** Builds a fetch stub returning one canned response. */
function stubFetch(
  body: unknown,
  init: { status?: number; headers?: Record<string, string>; raw?: string } = {},
): FetchStub {
  const status = init.status ?? 200;
  const headers = new Headers({ 'Content-Type': 'application/json', ...init.headers });
  // A 204 cannot carry a body — not even an empty string — and constructing
  // one that does throws, which would surface as a bogus network error rather
  // than as the case under test.
  const payload = NULL_BODY_STATUSES.has(status) ? null : (init.raw ?? JSON.stringify(body));

  return vi.fn(async () => new Response(payload, { status, headers })) as FetchStub;
}

/** A client whose CSRF token is fixed, since tests have no cookies. */
function clientWithCsrf(fetch: FetchLike, token = 'csrf-token') {
  return new HeadnetClient({ fetch, readCsrfToken: () => token });
}

describe('HeadnetClient.health', () => {
  it('returns the parsed health response', async () => {
    const client = new HeadnetClient({
      fetch: stubFetch({
        status: 'ok',
        version: '0.1.0',
        uptime_seconds: 42,
        checks: [{ name: 'database', status: 'ok', duration_ms: 1 }],
      }),
    });

    const health = await client.health();

    expect(health.status).toBe('ok');
    expect(health.version).toBe('0.1.0');
    expect(health.checks[0]?.name).toBe('database');
  });

  it('treats 503 as a real answer rather than a failure', async () => {
    // A 503 from /health means the server is running and telling us a
    // dependency is broken. Throwing it away would discard exactly the
    // information the dashboard exists to show.
    const client = new HeadnetClient({
      fetch: stubFetch(
        {
          status: 'down',
          version: '0.1.0',
          uptime_seconds: 5,
          checks: [
            { name: 'database', status: 'down', detail: 'not reachable', duration_ms: 3000 },
          ],
        },
        { status: 503 },
      ),
    });

    const health = await client.health();

    expect(health.status).toBe('down');
    expect(health.checks[0]?.detail).toBe('not reachable');
  });
});

describe('HeadnetClient.version', () => {
  it('returns the build and protocol range', async () => {
    const client = new HeadnetClient({
      fetch: stubFetch({
        version: '0.1.0',
        commit: 'abc1234',
        build_date: '2026-08-29T00:00:00Z',
        protocol_version: 1,
        min_protocol_version: 1,
        capabilities: ['protocol.negotiation'],
      }),
    });

    const version = await client.version();

    expect(version.protocol_version).toBe(1);
    expect(version.capabilities).toContain('protocol.negotiation');
  });
});

describe('CSRF handling', () => {
  it('attaches the token to state-changing requests', async () => {
    const fetchStub = stubFetch({ devices: [] }, { status: 201 });
    await clientWithCsrf(fetchStub).registerDevice({ name: 'laptop', public_key: 'key' });

    expect(fetchStub).toHaveBeenCalledWith(
      '/api/v1/devices',
      expect.objectContaining({
        method: 'POST',
        headers: expect.objectContaining({ [CSRF_HEADER]: 'csrf-token' }),
      }),
    );
  });

  it('attaches the token to a DELETE', async () => {
    const fetchStub = stubFetch(null, { status: 204, raw: '' });
    await clientWithCsrf(fetchStub).revokeDevice('dev_1');

    expect(fetchStub).toHaveBeenCalledWith(
      '/api/v1/devices/dev_1',
      expect.objectContaining({
        method: 'DELETE',
        headers: expect.objectContaining({ [CSRF_HEADER]: 'csrf-token' }),
      }),
    );
  });

  it('does not attach the token to reads', async () => {
    // Safe methods do not need it, and sending it on every read is noise.
    const fetchStub = stubFetch({ devices: [] });
    await clientWithCsrf(fetchStub).devices();

    const init = fetchStub.mock.calls[0]?.[1] as RequestInit;
    expect((init.headers as Record<string, string>)[CSRF_HEADER]).toBeUndefined();
  });

  it('omits the header entirely when no token is available', async () => {
    const fetchStub = stubFetch(null, { status: 204, raw: '' });
    const client = new HeadnetClient({ fetch: fetchStub, readCsrfToken: () => undefined });
    await client.revokeDevice('dev_1');

    const init = fetchStub.mock.calls[0]?.[1] as RequestInit;
    expect((init.headers as Record<string, string>)[CSRF_HEADER]).toBeUndefined();
  });
});

describe('authentication', () => {
  it('reports whether the installation needs claiming', async () => {
    const client = new HeadnetClient({
      fetch: stubFetch({ bootstrap_required: true, providers: ['local'] }),
    });

    const status = await client.authStatus();
    expect(status.bootstrap_required).toBe(true);
    expect(status.providers).toEqual(['local']);
  });

  it('signs in and returns the session', async () => {
    const fetchStub = stubFetch({
      user: {
        id: 'usr_1',
        email: 'ada@example.com',
        role: 'admin',
        provider: 'local',
        created_at: '',
      },
      csrf_token: 'fresh-token',
      expires_at: '2026-09-29T00:00:00Z',
    });
    const client = new HeadnetClient({ fetch: fetchStub });

    const session = await client.login('ada@example.com', 'correct horse battery staple');

    expect(session.user.role).toBe('admin');
    expect(session.csrf_token).toBe('fresh-token');
    expect(fetchStub).toHaveBeenCalledWith(
      '/api/v1/auth/login',
      expect.objectContaining({ method: 'POST' }),
    );
  });

  it('never puts the password in a URL', async () => {
    // A password in a query string ends up in access logs, proxy logs and
    // browser history.
    const fetchStub = stubFetch({ user: {}, csrf_token: '', expires_at: '' });
    await new HeadnetClient({ fetch: fetchStub }).login('ada@example.com', 'hunter2');

    const [url, init] = fetchStub.mock.calls[0] as [string, RequestInit];
    expect(url).not.toContain('hunter2');
    expect(url).toBe('/api/v1/auth/login');
    expect(init.body).toContain('hunter2');
  });

  it('handles a 204 from logout without trying to parse a body', async () => {
    const client = clientWithCsrf(stubFetch(null, { status: 204, raw: '' }));
    await expect(client.logout()).resolves.toBeUndefined();
  });
});

describe('devices', () => {
  it('returns the device list', async () => {
    const client = new HeadnetClient({
      fetch: stubFetch({
        devices: [
          {
            id: 'dev_1',
            user_id: 'usr_1',
            name: 'laptop',
            public_key: 'k',
            ipv4: '100.100.0.1',
            created_at: '',
          },
        ],
      }),
    });

    const devices = await client.devices();
    expect(devices).toHaveLength(1);
    expect(devices[0]?.ipv4).toBe('100.100.0.1');
  });

  it('tolerates a null device list', async () => {
    // Go marshals an empty slice as null, so the client must not assume an
    // array is present.
    const client = new HeadnetClient({ fetch: stubFetch({ devices: null }) });
    await expect(client.devices()).resolves.toEqual([]);
  });

  it('escapes the identifier in the path', async () => {
    const fetchStub = stubFetch(null, { status: 204, raw: '' });
    await clientWithCsrf(fetchStub).revokeDevice('dev_1/../../admin');

    const [url] = fetchStub.mock.calls[0] as [string];
    expect(url).toBe('/api/v1/devices/dev_1%2F..%2F..%2Fadmin');
  });

  it('surfaces a conflict on a duplicate public key', async () => {
    const client = clientWithCsrf(
      stubFetch(
        {
          error: {
            code: 'conflict',
            message: 'that public key is already registered to a device',
            details: { field: 'public_key' },
          },
        },
        { status: 409 },
      ),
    );

    const error = await expectApiError(client.registerDevice({ name: 'x', public_key: 'k' }));
    expect(error.code).toBe('conflict');
    expect(error.details?.field).toBe('public_key');
  });
});

describe('error handling', () => {
  it('surfaces the structured error envelope', async () => {
    const client = new HeadnetClient({
      fetch: stubFetch(
        {
          error: {
            code: 'not_implemented',
            message: 'device enrollment is not implemented yet',
            details: { roadmap_phase: 'Phase 2' },
            request_id: 'req_ABC',
          },
        },
        { status: 501 },
      ),
    });

    const error = await expectApiError(client.version());
    expect(error.code).toBe('not_implemented');
    expect(error.status).toBe(501);
    expect(error.details?.roadmap_phase).toBe('Phase 2');
  });

  it('carries the request ID so a user can quote it in a bug report', async () => {
    const client = new HeadnetClient({
      fetch: stubFetch(
        { error: { code: 'internal', message: 'internal error', request_id: 'req_XYZ' } },
        { status: 500 },
      ),
    });

    const error = await expectApiError(client.version());
    expect(error.requestId).toBe('req_XYZ');
  });

  it('falls back to the response header when the envelope omits the request ID', async () => {
    const client = new HeadnetClient({
      fetch: stubFetch(
        { error: { code: 'internal', message: 'boom' } },
        { status: 500, headers: { 'X-Request-Id': 'req_FROM_HEADER' } },
      ),
    });

    const error = await expectApiError(client.version());
    expect(error.requestId).toBe('req_FROM_HEADER');
  });

  it('tolerates a non-JSON error body from a proxy', async () => {
    const client = new HeadnetClient({
      fetch: stubFetch(null, {
        status: 502,
        raw: '<html><body>Bad Gateway</body></html>',
        headers: { 'Content-Type': 'text/html' },
      }),
    });

    const error = await expectApiError(client.version());
    expect(error).toBeInstanceOf(ApiError);
    expect(error.status).toBe(502);
    expect(error.message).toContain('502');
  });

  it('reports an unreachable server as a network error', async () => {
    const client = new HeadnetClient({
      fetch: vi.fn(async () => {
        throw new TypeError('Failed to fetch');
      }),
    });

    const error = await expectApiError(client.health());
    expect(error.code).toBe('network');
    expect(error.status).toBe(0);
    expect(error.retryable).toBe(true);
  });

  it('reports a malformed success body distinctly', async () => {
    const client = new HeadnetClient({ fetch: stubFetch(null, { raw: 'not json at all' }) });

    const error = await expectApiError(client.version());
    expect(error.code).toBe('malformed_response');
  });

  it('flags an ended session so the shell can stop claiming to be signed in', async () => {
    const client = new HeadnetClient({
      fetch: stubFetch(
        { error: { code: 'unauthorized', message: 'your session has ended; sign in again' } },
        { status: 401 },
      ),
    });

    const error = await expectApiError(client.session());
    expect(error.unauthenticated).toBe(true);
    expect(error.retryable).toBe(false);
  });

  it('marks the right codes as retryable', async () => {
    const retryable: Array<[string, boolean]> = [
      ['rate_limited', true],
      ['unavailable', true],
      ['internal', true],
      ['forbidden', false],
      ['not_found', false],
      ['not_implemented', false],
      ['unauthorized', false],
    ];

    for (const [code, want] of retryable) {
      const client = new HeadnetClient({
        fetch: stubFetch({ error: { code, message: code } }, { status: 500 }),
      });
      const error = await expectApiError(client.version());
      expect(error.retryable, `${code} should be retryable=${want}`).toBe(want);
    }
  });
});

describe('base URL handling', () => {
  it('defaults to same-origin paths', async () => {
    const fetchStub = stubFetch({ status: 'ok', version: '0', uptime_seconds: 0, checks: [] });
    await new HeadnetClient({ fetch: fetchStub }).health();

    expect(fetchStub).toHaveBeenCalledWith('/health', expect.anything());
  });

  it('strips a trailing slash from a configured base URL', async () => {
    const fetchStub = stubFetch({ status: 'ok', version: '0', uptime_seconds: 0, checks: [] });
    await new HeadnetClient({ baseUrl: 'https://vpn.example.com/', fetch: fetchStub }).health();

    expect(fetchStub).toHaveBeenCalledWith('https://vpn.example.com/health', expect.anything());
  });

  it('sends credentials so cookie authentication will work', async () => {
    const fetchStub = stubFetch({ status: 'ok', version: '0', uptime_seconds: 0, checks: [] });
    await new HeadnetClient({ fetch: fetchStub }).health();

    expect(fetchStub).toHaveBeenCalledWith(
      '/health',
      expect.objectContaining({ credentials: 'same-origin' }),
    );
  });
});
