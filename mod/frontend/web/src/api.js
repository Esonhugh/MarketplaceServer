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
  if (typeof data.username !== 'string' || !data.username || typeof data.token !== 'string' || !data.token) throw invalidResponse();
  return data;
}

function normalizeTokenSecret(value) {
  const data = requireObject(value);
  if (typeof data.name !== 'string' || !data.name || typeof data.token !== 'string' || !data.token) throw invalidResponse();
  return data;
}

function normalizePage(value) {
  const data = requireObject(value);
  if (!Array.isArray(data.items) || !Number.isInteger(data.page) || data.page < 1 || !Number.isInteger(data.size) || data.size < 1 || !Number.isInteger(data.total) || data.total < 0) throw invalidResponse();
  return data;
}

function normalizeTokenList(value) {
  const data = normalizePage(value);
  return {
    ...data,
    items: data.items.map((value) => {
      const item = requireObject(value);
      if (typeof item.id !== 'string' || !item.id || typeof item.name !== 'string' || !item.name || typeof item.preset !== 'string' || !item.preset || typeof item.status !== 'string' || !item.status) throw invalidResponse();
      const { token: _plaintext, ...metadata } = item;
      return metadata;
    }),
  };
}

function query(page, size, extra = {}) {
  const params = new URLSearchParams({ page: String(page), size: String(size) });
  for (const [key, value] of Object.entries(extra)) if (value !== '' && value != null) params.set(key, String(value));
  return params.toString();
}

export function createApiClient({ fetchImpl = fetch, getSession, clearSession }) {
  async function request(path, { method = 'GET', body, signal, authenticated = true, clearAuthSession = true, normalize = requireObject } = {}) {
    const headers = { Accept: 'application/json' };
    if (body !== undefined) headers['Content-Type'] = 'application/json';
    let requestToken = '';
    if (authenticated) {
      requestToken = getSession()?.token || '';
      if (requestToken) headers.Authorization = `Bearer ${requestToken}`;
    }
    let response;
    try {
      response = await fetchImpl(path, { method, headers, ...(body === undefined ? {} : { body: JSON.stringify(body) }), ...(signal ? { signal } : {}) });
    } catch (error) {
      if (error?.name === 'AbortError') throw error;
      throw new ApiError({ message: 'Unable to reach the server' });
    }
    if (response.status === 401 && authenticated && clearAuthSession && requestToken && getSession()?.token === requestToken) clearSession();
    if (!response.ok) {
      let payload;
      try { payload = await response.json(); } catch { payload = null; }
      throw new ApiError({ status: response.status, code: payload?.code || 'http_error', message: payload?.message || `Request failed with status ${response.status}`, requestId: response.headers.get('X-Request-Id') || payload?.requestId || '', details: payload?.details });
    }
    if (response.status === 204) return undefined;
    let payload;
    try { payload = await response.json(); } catch { throw invalidResponse(); }
    if (!payload || typeof payload !== 'object' || Array.isArray(payload) || !Object.hasOwn(payload, 'data')) throw invalidResponse();
    return normalize(payload.data);
  }

  const teamPath = (slug) => `/api/v1/teams/${encodeURIComponent(slug)}`;
  const userPath = (id) => `/api/v1/admin/users/${encodeURIComponent(id)}`;
  return {
    capabilities: (options = {}) => request('/api/v1/auth/capabilities', { authenticated: false, signal: options.signal }),
    login: (username, password, options = {}) => request('/api/v1/auth/login', { method: 'POST', body: { username, password }, signal: options.signal, authenticated: false, normalize: normalizeSession }),
    register: (input, options = {}) => request('/api/v1/auth/register', { method: 'POST', body: input, signal: options.signal, authenticated: false, normalize: normalizeSession }),
    me: (options = {}) => request('/api/v1/me', { signal: options.signal }),
    listTokens: (page, size, options = {}) => request(`/api/v1/me/tokens?${query(page, size)}`, { signal: options.signal, normalize: normalizeTokenList }),
    createToken: (input, options = {}) => request('/api/v1/me/tokens', { method: 'POST', body: input, signal: options.signal, normalize: normalizeTokenSecret }),
    revealToken: (id, password, options = {}) => request(`/api/v1/me/tokens/${encodeURIComponent(id)}/reveal`, { method: 'POST', body: { password }, signal: options.signal, clearAuthSession: false, normalize: normalizeTokenSecret }),
    revokeToken: (id, options = {}) => request(`/api/v1/me/tokens/${encodeURIComponent(id)}`, { method: 'DELETE', signal: options.signal }),
    listTeams: (page, size, scope = '', options = {}) => request(`/api/v1/teams?${query(page, size, { scope })}`, { signal: options.signal, normalize: normalizePage }),
    createTeam: (input, options = {}) => request('/api/v1/teams', { method: 'POST', body: input, signal: options.signal }),
    getTeam: (slug, options = {}) => request(teamPath(slug), { signal: options.signal }),
    updateTeam: (slug, input, options = {}) => request(teamPath(slug), { method: 'PATCH', body: input, signal: options.signal }),
    listMembers: (slug, page, size, options = {}) => request(`${teamPath(slug)}/members?${query(page, size)}`, { signal: options.signal, normalize: normalizePage }),
    putMember: (slug, userId, role, options = {}) => request(`${teamPath(slug)}/members/${encodeURIComponent(userId)}`, { method: 'PUT', body: { role }, signal: options.signal }),
    removeMember: (slug, userId, options = {}) => request(`${teamPath(slug)}/members/${encodeURIComponent(userId)}`, { method: 'DELETE', signal: options.signal }),
    listTeamInvitations: (slug, page, size, options = {}) => request(`${teamPath(slug)}/invitations?${query(page, size)}`, { signal: options.signal, normalize: normalizePage }),
    invite: (slug, input, options = {}) => request(`${teamPath(slug)}/invitations`, { method: 'POST', body: input, signal: options.signal }),
    revokeInvitation: (slug, id, options = {}) => request(`${teamPath(slug)}/invitations/${encodeURIComponent(id)}`, { method: 'DELETE', signal: options.signal }),
    reissueInvitation: (slug, id, options = {}) => request(`${teamPath(slug)}/invitations/${encodeURIComponent(id)}:reissue`, { method: 'POST', signal: options.signal }),
    listMyInvitations: (page, size, options = {}) => request(`/api/v1/me/team-invitations?${query(page, size)}`, { signal: options.signal, normalize: normalizePage }),
    acceptInvitation: (id, options = {}) => request(`/api/v1/me/team-invitations/${encodeURIComponent(id)}:accept`, { method: 'POST', signal: options.signal }),
    rejectInvitation: (id, options = {}) => request(`/api/v1/me/team-invitations/${encodeURIComponent(id)}:reject`, { method: 'POST', signal: options.signal }),
    listPlugins: (namespace, page, size, options = {}) => request(`/api/v1/namespaces/${encodeURIComponent(namespace)}/plugins?${query(page, size)}`, { signal: options.signal, normalize: normalizePage }),
    createPlugin: (namespace, input, options = {}) => request(`/api/v1/namespaces/${encodeURIComponent(namespace)}/plugins`, { method: 'POST', body: input, signal: options.signal }),
    getPlugin: (namespace, plugin, options = {}) => request(`/api/v1/namespaces/${encodeURIComponent(namespace)}/plugins/${encodeURIComponent(plugin)}`, { signal: options.signal }),
    archivePlugin: (namespace, plugin, options = {}) => request(`/api/v1/namespaces/${encodeURIComponent(namespace)}/plugins/${encodeURIComponent(plugin)}:archive`, { method: 'POST', signal: options.signal }),
    restorePlugin: (namespace, plugin, options = {}) => request(`/api/v1/namespaces/${encodeURIComponent(namespace)}/plugins/${encodeURIComponent(plugin)}:restore`, { method: 'POST', signal: options.signal }),
    setPluginVisibility: (namespace, plugin, visibility, options = {}) => request(`/api/v1/namespaces/${encodeURIComponent(namespace)}/plugins/${encodeURIComponent(plugin)}:set-visibility`, { method: 'POST', body: { visibility }, signal: options.signal }),
    listPluginVersions: (namespace, plugin, page, size, options = {}) => request(`/api/v1/namespaces/${encodeURIComponent(namespace)}/plugins/${encodeURIComponent(plugin)}/versions?${query(page, size)}`, { signal: options.signal, normalize: normalizePage }),
    publishPluginVersion: (namespace, plugin, input, options = {}) => request(`/api/v1/namespaces/${encodeURIComponent(namespace)}/plugins/${encodeURIComponent(plugin)}/versions:publish`, { method: 'POST', body: input, signal: options.signal }),
    setDefaultPluginVersion: (namespace, plugin, tag, options = {}) => request(`/api/v1/namespaces/${encodeURIComponent(namespace)}/plugins/${encodeURIComponent(plugin)}/versions/${encodeURIComponent(tag)}:set-default`, { method: 'POST', signal: options.signal }),
    clearDefaultPluginVersion: (namespace, plugin, options = {}) => request(`/api/v1/namespaces/${encodeURIComponent(namespace)}/plugins/${encodeURIComponent(plugin)}/default-version`, { method: 'DELETE', signal: options.signal }),
    listRepositoryRefs: (namespace, plugin, options = {}) => request(`/api/v1/namespaces/${encodeURIComponent(namespace)}/plugins/${encodeURIComponent(plugin)}/repository/refs`, { signal: options.signal }),
    getRepositoryTree: (namespace, plugin, ref, path = '', options = {}) => request(`/api/v1/namespaces/${encodeURIComponent(namespace)}/plugins/${encodeURIComponent(plugin)}/repository/tree?${new URLSearchParams({ ref, ...(path ? { path } : {}) })}`, { signal: options.signal }),
    getRepositoryBlob: (namespace, plugin, ref, path, options = {}) => request(`/api/v1/namespaces/${encodeURIComponent(namespace)}/plugins/${encodeURIComponent(plugin)}/repository/blob?${new URLSearchParams({ ref, path })}`, { signal: options.signal }),
    listRepositoryCommits: (namespace, plugin, ref, path = '', page = 1, size = 30, options = {}) => request(`/api/v1/namespaces/${encodeURIComponent(namespace)}/plugins/${encodeURIComponent(plugin)}/repository/commits?${query(page, size, { ref, path })}`, { signal: options.signal, normalize: normalizePage }),
    listUsers: (page, size, status = '', options = {}) => request(`/api/v1/admin/users?${query(page, size, { status })}`, { signal: options.signal, normalize: normalizePage }),
    createUser: (input, options = {}) => request('/api/v1/admin/users', { method: 'POST', body: input, signal: options.signal }),
    getUser: (id, options = {}) => request(userPath(id), { signal: options.signal }),
    updateUser: (id, input, options = {}) => request(userPath(id), { method: 'PATCH', body: input, signal: options.signal }),
    disableUser: (id, options = {}) => request(`${userPath(id)}:disable`, { method: 'POST', signal: options.signal }),
    enableUser: (id, options = {}) => request(`${userPath(id)}:enable`, { method: 'POST', signal: options.signal }),
    grantSystemAdmin: (id, options = {}) => request(`${userPath(id)}/system-admin`, { method: 'PUT', signal: options.signal }),
    revokeSystemAdmin: (id, options = {}) => request(`${userPath(id)}/system-admin`, { method: 'DELETE', signal: options.signal }),
  };
}
