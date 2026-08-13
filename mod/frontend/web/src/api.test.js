import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError, createApiClient } from './api.js';
import { clearSession, loadSession, saveSession } from './session.js';

describe('identity API client', () => {
  afterEach(() => {
    localStorage.clear();
    vi.restoreAllMocks();
  });

  it('sends same-origin JSON requests with the JWT and AbortSignal', async () => {
    saveSession({ username: 'alice', token: 'jwt-token' });
    const signal = new AbortController().signal;
    const fetchImpl = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ data: { items: [], page: 2, size: 10, total: 0 } }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    );

    const api = createApiClient({ fetchImpl, getSession: loadSession, clearSession });
    await expect(api.listTokens(2, 10, { signal })).resolves.toEqual({
      items: [],
      page: 2,
      size: 10,
      total: 0,
    });
    expect(fetchImpl).toHaveBeenCalledWith('/api/v1/me/tokens?page=2&size=10', {
      method: 'GET',
      headers: { Accept: 'application/json', Authorization: 'Bearer jwt-token' },
      signal,
    });
  });

  it('normalizes direct API errors, extracts request IDs, and clears a rejected session', async () => {
    saveSession({ username: 'alice', token: 'expired-jwt' });
    const fetchImpl = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({ code: 'unauthenticated', message: 'authentication is required', requestId: 'body-id' }),
        { status: 401, headers: { 'X-Request-Id': 'header-id', 'Content-Type': 'application/json' } },
      ),
    );
    const api = createApiClient({ fetchImpl, getSession: loadSession, clearSession });

    await expect(api.listTokens(1, 20)).rejects.toEqual(
      expect.objectContaining({
        name: 'ApiError',
        status: 401,
        code: 'unauthenticated',
        message: 'authentication is required',
        requestId: 'header-id',
      }),
    );
    expect(loadSession()).toBeNull();
  });

  it('does not clear a replacement session when an old request receives a delayed 401', async () => {
    saveSession({ username: 'alice', token: 'old-jwt' });
    let resolveRequest;
    const fetchImpl = vi.fn().mockReturnValue(
      new Promise((resolve) => {
        resolveRequest = resolve;
      }),
    );
    const api = createApiClient({ fetchImpl, getSession: loadSession, clearSession });

    const oldRequest = api.listTokens(1, 20);
    saveSession({ username: 'alice', token: 'new-jwt' });
    resolveRequest(
      new Response(JSON.stringify({ code: 'unauthenticated', message: 'expired' }), {
        status: 401,
        headers: { 'Content-Type': 'application/json' },
      }),
    );

    await expect(oldRequest).rejects.toEqual(expect.objectContaining({ status: 401 }));
    expect(loadSession()).toEqual({ username: 'alice', token: 'new-jwt' });
  });

  it('keeps the authenticated session when reveal rejects the account password', async () => {
    saveSession({ username: 'alice', token: 'jwt-token' });
    const fetchImpl = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ code: 'invalid_credentials', message: 'invalid username or password' }), {
        status: 401,
        headers: { 'Content-Type': 'application/json' },
      }),
    );
    const api = createApiClient({ fetchImpl, getSession: loadSession, clearSession });

    await expect(api.revealToken('token-id', 'wrong-password')).rejects.toEqual(
      expect.objectContaining({ status: 401, code: 'invalid_credentials' }),
    );
    expect(fetchImpl.mock.calls[0][1].headers.Authorization).toBe('Bearer jwt-token');
    expect(loadSession()).toEqual({ username: 'alice', token: 'jwt-token' });
  });

  it('normalizes non-JSON failures and accepts bodyless revoke responses', async () => {
    saveSession({ username: 'alice', token: 'jwt-token' });
    const fetchImpl = vi
      .fn()
      .mockResolvedValueOnce(new Response('gateway unavailable', { status: 502, headers: { 'X-Request-Id': 'proxy-id' } }))
      .mockResolvedValueOnce(new Response(null, { status: 204 }));
    const api = createApiClient({ fetchImpl, getSession: loadSession, clearSession });

    await expect(api.listTokens(1, 20)).rejects.toEqual(
      expect.objectContaining({ status: 502, code: 'http_error', requestId: 'proxy-id' }),
    );
    await expect(api.revokeToken('token-id')).resolves.toBeUndefined();
  });

  it('fails closed on malformed success envelopes and removes plaintext fields from token metadata', async () => {
    saveSession({ username: 'alice', token: 'jwt-token' });
    const item = {
      id: 'token-id',
      name: 'CI',
      preset: 'git-clone',
      status: 'active',
      expiresAt: null,
      lastUsedAt: null,
      revokedAt: null,
      createdAt: '2026-08-10T00:00:00Z',
      token: 'mpsk_must_not_escape',
    };
    const fetchImpl = vi
      .fn()
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ data: { items: [item], page: 1, size: 20, total: 1 } }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        }),
      )
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ data: { items: 'not-an-array', page: 1, size: 20, total: 0 } }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        }),
      )
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ result: { username: 'alice', token: 'jwt-token' } }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        }),
      );
    const api = createApiClient({ fetchImpl, getSession: loadSession, clearSession });

    const list = await api.listTokens(1, 20);
    expect(list.items[0]).toEqual(expect.objectContaining({ id: 'token-id', name: 'CI', status: 'active' }));
    expect(Object.hasOwn(list.items[0], 'token')).toBe(false);
    await expect(api.listTokens(1, 20)).rejects.toEqual(
      expect.objectContaining({ name: 'ApiError', code: 'invalid_response' }),
    );
    await expect(api.login('alice', 'password')).rejects.toEqual(
      expect.objectContaining({ name: 'ApiError', code: 'invalid_response' }),
    );
  });

  it('uses the exact login, create, and reveal contracts', async () => {
    saveSession({ username: 'alice', token: 'jwt-token' });
    const fetchImpl = vi.fn().mockImplementation(async (url, options) => {
      const data = url.endsWith('/login')
        ? { username: 'alice', token: 'jwt-token' }
        : { name: 'CI', token: 'mpsk_plaintext' };
      return new Response(JSON.stringify({ data }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      });
    });
    const api = createApiClient({ fetchImpl, getSession: loadSession, clearSession });

    await api.login('alice', 'password');
    await api.createToken({ name: 'CI', preset: 'git-clone', expiresAt: null });
    await api.revealToken('token-id', 'password');

    expect(fetchImpl.mock.calls.map(([url, options]) => [url, options.method, JSON.parse(options.body)])).toEqual([
      ['/api/v1/auth/login', 'POST', { username: 'alice', password: 'password' }],
      ['/api/v1/me/tokens', 'POST', { name: 'CI', preset: 'git-clone', expiresAt: null }],
      ['/api/v1/me/tokens/token-id/reveal', 'POST', { password: 'password' }],
    ]);
  });

  it('exposes a stable typed error for callers', () => {
    expect(new ApiError({ status: 422, code: 'validation_failed', message: 'invalid' })).toBeInstanceOf(Error);
  });
});

describe('session storage', () => {
  afterEach(() => localStorage.clear());

  it('persists only username and JWT and clears both together', () => {
    saveSession({ username: 'alice', token: 'jwt-token', password: 'never', expiresAt: 'never' });

    expect(localStorage.length).toBe(2);
    expect([localStorage.key(0), localStorage.key(1)].sort()).toEqual(['marketplace.jwt', 'marketplace.username']);
    expect([localStorage.getItem('marketplace.username'), localStorage.getItem('marketplace.jwt')]).toEqual([
      'alice',
      'jwt-token',
    ]);
    expect(loadSession()).toEqual({ username: 'alice', token: 'jwt-token' });

    clearSession();
    expect(localStorage.length).toBe(0);
  });
});
