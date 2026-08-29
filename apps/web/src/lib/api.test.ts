import { describe, expect, it, vi } from 'vitest';

import { ApiError, HeadnetClient, type FetchLike } from './api';

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

/** Builds a fetch stub returning one canned response. */
function stubFetch(
  body: unknown,
  init: { status?: number; headers?: Record<string, string>; raw?: string } = {},
): FetchLike {
  const status = init.status ?? 200;
  const headers = new Headers({ 'Content-Type': 'application/json', ...init.headers });
  const payload = init.raw ?? JSON.stringify(body);

  return vi.fn(async () => new Response(payload, { status, headers }));
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

    await expect(client.version()).rejects.toThrowError(ApiError);

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
        {
          status: 500,
          headers: { 'X-Request-Id': 'req_FROM_HEADER' },
        },
      ),
    });

    const error = await expectApiError(client.version());
    expect(error.requestId).toBe('req_FROM_HEADER');
  });

  it('tolerates a non-JSON error body from a proxy', async () => {
    // A load balancer in front of the control plane may return an HTML error
    // page; the UI must still show something useful.
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
    const client = new HeadnetClient({
      fetch: stubFetch(null, { raw: 'not json at all' }),
    });

    const error = await expectApiError(client.version());
    expect(error.code).toBe('malformed_response');
  });

  it('marks the right codes as retryable', async () => {
    const retryable: Array<[string, boolean]> = [
      ['rate_limited', true],
      ['unavailable', true],
      ['internal', true],
      ['forbidden', false],
      ['not_found', false],
      ['not_implemented', false],
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
