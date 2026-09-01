import { cleanup, render, screen, waitFor } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';
import App from './App.svelte';
import PluginPage from './PluginPage.svelte';
import PluginsPage from './PluginsPage.svelte';
import { saveSession } from './session.js';

const plugin = {
  namespace: 'alice',
  name: 'example-plugin',
  status: 'active',
  visibility: 'public',
  repositoryStatus: 'ready',
  cloneUrl: '/git/alice/example-plugin.git',
  defaultVersion: 'v1.0.0',
};

function client(overrides = {}) {
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
  it('lists a personal namespace and creates a Plugin with the selected visibility', async () => {
    const apiClient = client();
    const user = userEvent.setup();
    render(PluginsPage, { props: { apiClient, profile: { username: 'alice' } } });

    expect(await screen.findByText('alice / example-plugin')).toBeInTheDocument();
    await user.type(screen.getByLabelText('Plugin name'), 'new-plugin');
    await user.selectOptions(screen.getByLabelText('Visibility'), 'private');
    await user.click(screen.getByRole('button', { name: 'Create Plugin' }));

    await waitFor(() => expect(apiClient.createPlugin).toHaveBeenCalledWith(
      'alice',
      { name: 'new-plugin', visibility: 'private' },
    ));
    expect(apiClient.listPlugins).toHaveBeenLastCalledWith('alice', 1, 100, expect.objectContaining({ signal: expect.any(AbortSignal) }));
  });

  it('loads Plugin detail and publishes or selects canonical versions', async () => {
    const apiClient = client();
    const user = userEvent.setup();
    render(PluginPage, { props: { apiClient, namespace: 'alice', plugin: 'example-plugin', tab: 'versions' } });

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
    render(PluginPage, { props: { apiClient, namespace: 'alice', plugin: 'example-plugin', tab: 'settings' } });

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
    render(App, { props: { apiClient } });

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
    render(App, { props: { apiClient } });

    expect(await screen.findByRole('button', { name: 'Log out' })).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Log out' }));

    expect(window.location.pathname).toBe('/login');
    expect(screen.getByRole('button', { name: 'Sign in' })).toBeInTheDocument();
  });
});
