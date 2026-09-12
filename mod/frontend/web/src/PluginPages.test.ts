import { cleanup, render, screen, waitFor } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';
import App from './App.svelte';
import PluginPage from './PluginPage.svelte';
import PluginsPage from './PluginsPage.svelte';
import { saveSession } from './session.js';
import type { ApiClient } from './api.js';

const plugin = {
  namespace: 'alice',
  name: 'example-plugin',
  status: 'active',
  visibility: 'public',
  repositoryStatus: 'ready',
  cloneUrl: '/git/alice/example-plugin.git',
  defaultVersion: 'v1.0.0',
};

function client(overrides = {}): Partial<ApiClient> {
  return {
    listTeams: vi.fn().mockResolvedValue({ items: [], page: 1, size: 100, total: 0 }),
    listPlugins: vi.fn().mockResolvedValue({ items: [plugin], page: 1, size: 100, total: 1 }),
    createPlugin: vi.fn().mockResolvedValue(plugin),
    getPlugin: vi.fn().mockResolvedValue(plugin),
    listRepositoryRefs: vi.fn().mockResolvedValue({ branches: [], tags: [], defaultRef: '' }),
    listPluginVersions: vi.fn().mockResolvedValue({
      items: [{ tag: 'v1.0.0', status: 'available', commitSha: '1234567890abcdef', publishedAt: '2026-08-01T00:00:00Z' }],
      page: 1,
      size: 100,
      total: 1,
    }),
    publishPluginVersion: vi.fn().mockResolvedValue({}),
    setDefaultPluginVersion: vi.fn().mockResolvedValue(undefined),
    setPluginVisibility: vi.fn().mockResolvedValue(undefined),
    archivePlugin: vi.fn().mockResolvedValue(undefined),
    restorePlugin: vi.fn().mockResolvedValue(undefined),
    ...overrides,
  };
}

afterEach(() => {
  cleanup();
  localStorage.clear();
  vi.restoreAllMocks();
  window.history.replaceState({}, '', '/');
});

describe('Plugin pages', () => {
  it('lists projects without a permanent creation form', async () => {
    render(PluginsPage, { props: { apiClient: client() as ApiClient, profile: { username: 'alice' } } });
    expect(await screen.findByText('alice / example-plugin')).toBeInTheDocument();
    expect(screen.queryByLabelText('Plugin name')).not.toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'New Plugin' })).toHaveAttribute('href', '/plugins/new');
  });

  it('opens the dedicated creation route without the workspace sidebar', async () => {
    saveSession({ username: 'alice', token: 'jwt-token' });
    window.history.replaceState({}, '', '/plugins/new');
    render(App, { props: { apiClient: client({ me: vi.fn().mockResolvedValue({ username: 'alice' }) }) as ApiClient } });
    expect(await screen.findByLabelText('Plugin name')).toBeInTheDocument();
    expect(screen.queryByRole('navigation', { name: 'Management sections' })).not.toBeInTheDocument();
  });

  it('shows real latest commit, a file table and About', async () => {
    const apiClient = client({
      listRepositoryRefs: vi.fn().mockResolvedValue({ branches: [{ name: 'main' }], tags: [], defaultRef: 'main' }),
      getRepositoryTree: vi.fn().mockResolvedValue({ entries: [{ name: 'README.md', type: 'blob', size: 24 }] }),
      listRepositoryCommits: vi.fn().mockResolvedValue({ items: [{ subject: 'Document installation', authorName: 'Alice', committedAt: '2026-08-01T00:00:00Z', sha: 'abcdef1234567890' }] }),
    });
    render(PluginPage, { props: { apiClient: apiClient as ApiClient, namespace: 'alice', plugin: 'example-plugin' } });
    expect(await screen.findByText('Document installation')).toBeInTheDocument();
    expect(screen.getByRole('table', { name: 'Repository files' })).toBeInTheDocument();
    expect(screen.queryByText('null B')).not.toBeInTheDocument();
    expect(screen.getByRole('complementary', { name: 'About' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'README.md' })).toHaveAttribute('href', '/plugins/alice/example-plugin/code/README.md?ref=main');
    expect(screen.getAllByRole('link', { name: 'example-plugin' })[0]).toHaveAttribute('href', '/plugins/alice/example-plugin/code?ref=main');
  });

  it('renders escaped source with line anchors and parent breadcrumbs', async () => {
    const apiClient = client({
      listRepositoryRefs: vi.fn().mockResolvedValue({ branches: [{ name: 'main' }], tags: [], defaultRef: 'main' }),
      getRepositoryBlob: vi.fn().mockResolvedValue({ path: 'src/index.ts', size: 32, content: '<script>unsafe</script>\nsecond line' }),
      listRepositoryCommits: vi.fn().mockResolvedValue({ items: [] }),
    });
    render(PluginPage, { props: { apiClient: apiClient as ApiClient, namespace: 'alice', plugin: 'example-plugin', path: 'src/index.ts' } });
    expect(await screen.findByText('<script>unsafe</script>')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Line 2' })).toHaveAttribute('href', '#L2');
    expect(screen.getByRole('navigation', { name: 'File path' })).toHaveTextContent('src');
  });

  it('creates a Plugin with the selected visibility on the creation page', async () => {
    const apiClient = client();
    const user = userEvent.setup();
    render(PluginsPage, { props: { apiClient: apiClient as ApiClient, profile: { username: 'alice' }, creating: true } });

    await waitFor(() => expect(screen.getByRole('button', { name: 'Create Plugin' })).toBeEnabled());
    await user.type(screen.getByLabelText('Plugin name'), 'new-plugin');
    await user.selectOptions(screen.getByLabelText('Visibility'), 'private');
    await user.click(screen.getByRole('button', { name: 'Create Plugin' }));

    await waitFor(() => expect(apiClient.createPlugin).toHaveBeenCalledWith(
      'alice',
      { name: 'new-plugin', visibility: 'private' },
    ));
    expect(apiClient.listPlugins).not.toHaveBeenCalled();
    expect(window.location.pathname).toBe('/plugins/alice/example-plugin/code');
  });

  it('keeps a selected ref through file navigation and browser back', async () => {
    saveSession({ username: 'alice', token: 'jwt-token' });
    window.history.replaceState({}, '', '/plugins/alice/example-plugin/code?ref=feature%2Fui');
    const apiClient = client({
      me: vi.fn().mockResolvedValue({ username: 'alice' }),
      listRepositoryRefs: vi.fn().mockResolvedValue({ branches: [{ name: 'main' }, { name: 'feature/ui' }], tags: [], defaultRef: 'main' }),
      getRepositoryTree: vi.fn().mockResolvedValue({ entries: [{ name: 'README.md', type: 'blob' }] }),
      getRepositoryBlob: vi.fn().mockResolvedValue({ path: 'README.md', content: 'Feature source', size: 14 }),
      listRepositoryCommits: vi.fn().mockResolvedValue({ items: [] }),
    });
    const user = userEvent.setup();
    render(App, { props: { apiClient: apiClient as ApiClient } });
    await user.click(await screen.findByRole('link', { name: 'README.md' }));
    expect(await screen.findByText('Feature source')).toBeInTheDocument();
    expect(apiClient.getRepositoryBlob).toHaveBeenCalledWith('alice', 'example-plugin', 'feature/ui', 'README.md', expect.anything());
    await user.selectOptions(screen.getByRole('combobox', { name: 'Branch or tag' }), 'main');
    expect(await screen.findByRole('table', { name: 'Repository files' })).toBeInTheDocument();
    expect(apiClient.getRepositoryTree).toHaveBeenLastCalledWith('alice', 'example-plugin', 'main', '', expect.anything());
    window.history.back();
    expect(await screen.findByText('Feature source')).toBeInTheDocument();
  });

  it('keeps source available when commit history fails', async () => {
    const apiClient = client({
      listRepositoryRefs: vi.fn().mockResolvedValue({ branches: [{ name: 'main' }], tags: [], defaultRef: 'main' }),
      getRepositoryTree: vi.fn().mockResolvedValue({ entries: [{ name: 'README.md', type: 'blob' }] }),
      listRepositoryCommits: vi.fn().mockRejectedValue({ status: 503, message: 'Unavailable' }),
    });
    render(PluginPage, { props: { apiClient: apiClient as ApiClient, namespace: 'alice', plugin: 'example-plugin' } });
    expect(await screen.findByText('Recent commit unavailable.')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'README.md' })).toBeInTheDocument();
  });

  it('retains creation fields and reports a denied request', async () => {
    const apiClient = client({ createPlugin: vi.fn().mockRejectedValue({ status: 403, message: 'Access denied' }) });
    const user = userEvent.setup();
    render(PluginsPage, { props: { apiClient: apiClient as ApiClient, profile: { username: 'alice' }, creating: true } });
    await waitFor(() => expect(screen.getByRole('button', { name: 'Create Plugin' })).toBeEnabled());
    await user.type(screen.getByLabelText('Plugin name'), 'new-plugin');
    await user.click(screen.getByRole('button', { name: 'Create Plugin' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('Access denied');
    expect(screen.getByLabelText('Plugin name')).toHaveValue('new-plugin');
    expect(window.location.pathname).toBe('/');
  });

  it('loads Plugin detail and publishes or selects canonical versions', async () => {
    const apiClient = client();
    const user = userEvent.setup();
    render(PluginPage, { props: { apiClient: apiClient as ApiClient, namespace: 'alice', plugin: 'example-plugin', tab: 'versions' } });

    expect(await screen.findByText('v1.0.0')).toBeInTheDocument();
    await user.type(screen.getByLabelText('Tag'), 'v1.1.0');
    await user.click(screen.getByLabelText('Default version'));
    await user.click(screen.getByRole('button', { name: 'Publish version' }));

    await waitFor(() => expect(apiClient.publishPluginVersion).toHaveBeenCalledWith(
      'alice',
      'example-plugin',
      { tag: 'v1.1.0', makeDefault: true },
    ));
    expect(apiClient.getPlugin).toHaveBeenCalledWith('alice', 'example-plugin', expect.objectContaining({ signal: expect.any(AbortSignal) }));
  });

  it('updates visibility and archives a Plugin from settings', async () => {
    const apiClient = client();
    const user = userEvent.setup();
    render(PluginPage, { props: { apiClient: apiClient as ApiClient, namespace: 'alice', plugin: 'example-plugin', tab: 'settings' } });

    expect(await screen.findByRole('button', { name: 'Save visibility' })).toBeInTheDocument();
    await user.selectOptions(screen.getByLabelText('Plugin visibility'), 'private');
    await user.click(screen.getByRole('button', { name: 'Save visibility' }));
    await waitFor(() => expect(apiClient.setPluginVisibility).toHaveBeenCalledWith('alice', 'example-plugin', 'private'));

    await user.click(screen.getByRole('button', { name: 'Archive Plugin' }));
    await waitFor(() => expect(apiClient.archivePlugin).toHaveBeenCalledWith('alice', 'example-plugin'));
  });

  it('reloads Plugin content when browser history changes tabs', async () => {
    saveSession({ username: 'alice', token: 'jwt-token' });
    window.history.replaceState({}, '', '/plugins/alice/example-plugin/code');
    const apiClient = client({ me: vi.fn().mockResolvedValue({ username: 'alice' }) });
    render(App, { props: { apiClient: apiClient as ApiClient } });

    expect(await screen.findByText('Quick setup')).toBeInTheDocument();
    window.history.pushState({}, '', '/plugins/alice/example-plugin/versions');
    window.dispatchEvent(new PopStateEvent('popstate'));

    expect(await screen.findByText('Publish canonical tag')).toBeInTheDocument();
    expect(apiClient.listPluginVersions).toHaveBeenCalledWith('alice', 'example-plugin', 1, 100, expect.objectContaining({ signal: expect.any(AbortSignal) }));
  });

  it('replaces a protected history entry on logout', async () => {
    saveSession({ username: 'alice', token: 'jwt-token' });
    window.history.replaceState({}, '', '/plugins');
    const apiClient = client({ me: vi.fn().mockResolvedValue({ username: 'alice' }) });
    const user = userEvent.setup();
    render(App, { props: { apiClient: apiClient as ApiClient } });

    expect(await screen.findByRole('button', { name: 'Log out' })).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Log out' }));

    expect(window.location.pathname).toBe('/login');
    expect(screen.getByRole('button', { name: 'Sign in' })).toBeInTheDocument();
  });
});
