export class ApiError extends Error {
  constructor({ status = 0, code = 'network_error', message = 'Request failed', requestId = '', details } = {}) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.code = code;
    this.requestId = requestId;
    this.details = details;
  }
}

function invalidResponse() {
  return new ApiError({ code: 'invalid_response', message: 'Server returned an invalid response' });
}

function requireObject(value) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw invalidResponse();
  return value;
}

function normalizeSession(value) {
  const data = requireObject(value);
  if (typeof data.username !== 'string' || !data.username || typeof data.token !== 'string' || !data.token) {
    throw invalidResponse();
  }
  return data;
}

function normalizeTokenSecret(value) {
  const data = requireObject(value);
  if (typeof data.name !== 'string' || !data.name || typeof data.token !== 'string' || !data.token) {
    throw invalidResponse();
  }
  return data;
}

function normalizeTokenList(value) {
  const data = requireObject(value);
  if (
    !Array.isArray(data.items) ||
    !Number.isInteger(data.page) ||
    data.page < 1 ||
    !Number.isInteger(data.size) ||
    data.size < 1 ||
    !Number.isInteger(data.total) ||
    data.total < 0
  ) {
    throw invalidResponse();
  }
  const items = data.items.map((value) => {
    const item = requireObject(value);
    if (
      typeof item.id !== 'string' ||
      !item.id ||
      typeof item.name !== 'string' ||
      !item.name ||
      typeof item.preset !== 'string' ||
      !item.preset ||
      typeof item.status !== 'string' ||
      !item.status
    ) {
      throw invalidResponse();
    }
    const { token: _plaintext, ...metadata } = item;
    return metadata;
  });
  return { ...data, items };
}

export function createApiClient({ fetchImpl = fetch, getSession, clearSession }) {
  async function request(
    path,
    { method = 'GET', body, signal, authenticated = true, clearAuthSession = true, normalize = requireObject } = {},
  ) {
    const headers = { Accept: 'application/json' };
    if (body !== undefined) headers['Content-Type'] = 'application/json';
    let requestToken = '';
    if (authenticated) {
      const session = getSession();
      requestToken = session?.token || '';
      if (requestToken) headers.Authorization = `Bearer ${requestToken}`;
    }

    let response;
    try {
      response = await fetchImpl(path, {
        method,
        headers,
        ...(body === undefined ? {} : { body: JSON.stringify(body) }),
        ...(signal ? { signal } : {}),
      });
    } catch (error) {
      if (error?.name === 'AbortError') throw error;
      throw new ApiError({ message: 'Unable to reach the server' });
    }

    if (response.status === 401 && authenticated && clearAuthSession && requestToken && getSession()?.token === requestToken) {
      clearSession();
    }
    if (!response.ok) {
      let payload;
      try {
        payload = await response.json();
      } catch {
        payload = null;
      }
      throw new ApiError({
        status: response.status,
        code: payload?.code || 'http_error',
        message: payload?.message || `Request failed with status ${response.status}`,
        requestId: response.headers.get('X-Request-Id') || payload?.requestId || '',
        details: payload?.details,
      });
    }
    if (response.status === 204) return undefined;
    let payload;
    try {
      payload = await response.json();
    } catch {
      throw invalidResponse();
    }
    if (!payload || typeof payload !== 'object' || Array.isArray(payload) || !Object.hasOwn(payload, 'data')) {
      throw invalidResponse();
    }
    return normalize(payload.data);
  }

  return {
    login: (username, password, options = {}) =>
      request('/api/v1/auth/login', {
        method: 'POST',
        body: { username, password },
        signal: options.signal,
        authenticated: false,
        normalize: normalizeSession,
      }),
    listTokens: (page, size, options = {}) =>
      request(`/api/v1/me/tokens?page=${encodeURIComponent(page)}&size=${encodeURIComponent(size)}`, {
        signal: options.signal,
        normalize: normalizeTokenList,
      }),
    createToken: (input, options = {}) =>
      request('/api/v1/me/tokens', {
        method: 'POST',
        body: input,
        signal: options.signal,
        normalize: normalizeTokenSecret,
      }),
    revealToken: (tokenId, password, options = {}) =>
      request(`/api/v1/me/tokens/${encodeURIComponent(tokenId)}/reveal`, {
        method: 'POST',
        body: { password },
        signal: options.signal,
        clearAuthSession: false,
        normalize: normalizeTokenSecret,
      }),
    revokeToken: (tokenId, options = {}) =>
      request(`/api/v1/me/tokens/${encodeURIComponent(tokenId)}`, {
        method: 'DELETE',
        signal: options.signal,
      }),
  };
}
