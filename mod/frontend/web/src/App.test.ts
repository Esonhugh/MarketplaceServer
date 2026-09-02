import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';
import App from './App.svelte';
import { saveSession } from './session.js';
import type { ApiClient } from './api.js';
import type { Session } from './types.js';

function token(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    id: '11111111-1111-1111-1111-111111111111',
    name: 'CI clone',
    preset: 'git-clone',
    status: 'active',
    expiresAt: null,
    lastUsedAt: null,
    revokedAt: null,
    createdAt: '2026-08-10T00:00:00Z',
    ...overrides,
  };
}

function api(overrides: Partial<ApiClient> = {}): ApiClient {
  return {
    login: vi.fn(),
    listTokens: vi.fn().mockResolvedValue({ items: [], page: 1, size: 20, total: 0 }),
    createToken: vi.fn(),
    revealToken: vi.fn(),
    revokeToken: vi.fn(),
    ...overrides,
  } as ApiClient;
}

afterEach(() => {
  cleanup();
  localStorage.clear();
  sessionStorage.clear();
  vi.restoreAllMocks();
});

describe('identity console', () => {
  it('loads the registered user profile immediately after signup', async () => {
    window.history.replaceState({}, '', '/register');
    const user = userEvent.setup();
    const client = api({
      capabilities: vi.fn().mockResolvedValue({ registrationEnabled: true }),
      register: vi.fn().mockResolvedValue({ username: 'alice', token: 'jwt-token' }),
      me: vi.fn().mockResolvedValue({ username: 'alice', systemAdmin: false }),
    });
    render(App, { props: { apiClient: client } });

    await user.type(await screen.findByLabelText('Username'), 'alice');
    await user.type(screen.getByLabelText('Display name'), 'Alice');
    await user.type(screen.getByLabelText('Password'), 'correct horse battery staple');
    await user.click(screen.getByRole('button', { name: 'Create account' }));

    expect(await screen.findByText('Credentials owned by alice.')).toBeInTheDocument();
    await waitFor(() => expect(client.me).toHaveBeenCalledTimes(1));
    expect(screen.queryByRole('link', { name: 'Users' })).not.toBeInTheDocument();
  });

  it('logs in without persisting the password and logs out', async () => {
    const user = userEvent.setup();
    const client = api({
      login: vi.fn().mockResolvedValue({ username: 'alice', token: 'jwt-token', expiresAt: '2026-09-11T12:00:00Z' }),
      me: vi.fn().mockResolvedValue({ username: 'alice', systemAdmin: true }),
    });
    render(App, { props: { apiClient: client } });

    await user.type(screen.getByLabelText('Username'), 'alice');
    await user.type(screen.getByLabelText('Password'), 'correct horse');
    await user.click(screen.getByRole('button', { name: 'Sign in' }));

    expect(await screen.findByText('Personal access tokens')).toBeInTheDocument();
    expect(localStorage.getItem('marketplace.username')).toBe('alice');
    expect(localStorage.getItem('marketplace.jwt')).toBe('jwt-token');
    expect([...Array(localStorage.length)].map((_, index) => localStorage.key(index)).sort()).toEqual([
      'marketplace.jwt',
      'marketplace.username',
    ]);
    expect(document.body.textContent).not.toContain('correct horse');
    expect(await screen.findByRole('link', { name: 'Users' })).toBeInTheDocument();

    await user.click(screen.getAllByRole('button', { name: 'Log out' })[0]);
    expect(screen.getByRole('button', { name: 'Sign in' })).toBeInTheDocument();
    expect(localStorage.length).toBe(0);
  });

  it('never saves a delayed login after logout, unmount, or a replacement login', async () => {
    const user = userEvent.setup();
    let resolveFirst: (session: Session) => void = () => {};
    let resolveUnmounted: (session: Session) => void = () => {};
    const firstLogin = new Promise<Session>((resolve) => {
      resolveFirst = resolve;
    });
    const unmountedLogin = new Promise<Session>((resolve) => {
      resolveUnmounted = resolve;
    });
    const client = api({
      login: vi
        .fn()
        .mockReturnValueOnce(firstLogin)
        .mockResolvedValueOnce({ username: 'alice', token: 'new-jwt' })
        .mockReturnValueOnce(unmountedLogin),
    });
    const view = render(App, { props: { apiClient: client } });

    await user.type(screen.getByLabelText('Username'), 'alice');
    await user.type(screen.getByLabelText('Password'), 'first-password');
    await user.click(screen.getByRole('button', { name: 'Sign in' }));
    await user.clear(screen.getByLabelText('Password'));
    await user.type(screen.getByLabelText('Password'), 'second-password');
    await fireEvent.submit(screen.getByLabelText('Password').closest('form')!);
    expect(await screen.findByText('Personal access tokens')).toBeInTheDocument();
    expect(localStorage.getItem('marketplace.jwt')).toBe('new-jwt');

    resolveFirst({ username: 'alice', token: 'old-jwt' });
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(localStorage.getItem('marketplace.jwt')).toBe('new-jwt');

    await user.click(screen.getByRole('button', { name: 'Log out' }));
    expect(localStorage.length).toBe(0);
    expect(sessionStorage.length).toBe(0);

    await user.type(screen.getByLabelText('Username'), 'alice');
    await user.type(screen.getByLabelText('Password'), 'third-password');
    await user.click(screen.getByRole('button', { name: 'Sign in' }));
    view.unmount();
    resolveUnmounted({ username: 'alice', token: 'unmounted-jwt' });
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(localStorage.length).toBe(0);
    expect(sessionStorage.length).toBe(0);
  });

  it('does not save an older pending login after logout', async () => {
    const user = userEvent.setup();
    let resolveOldLogin: (session: Session) => void = () => {};
    const oldLogin = new Promise<Session>((resolve) => {
      resolveOldLogin = resolve;
    });
    const client = api({
      login: vi
        .fn()
        .mockReturnValueOnce(oldLogin)
        .mockResolvedValueOnce({ username: 'alice', token: 'current-jwt' }),
    });
    render(App, { props: { apiClient: client } });

    await user.type(screen.getByLabelText('Username'), 'alice');
    await user.type(screen.getByLabelText('Password'), 'old-password');
    await user.click(screen.getByRole('button', { name: 'Sign in' }));
    await user.clear(screen.getByLabelText('Password'));
    await user.type(screen.getByLabelText('Password'), 'current-password');
    await fireEvent.submit(screen.getByLabelText('Password').closest('form')!);
    expect(await screen.findByText('Personal access tokens')).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'Log out' }));
    resolveOldLogin({ username: 'alice', token: 'old-jwt' });
    await new Promise((resolve) => setTimeout(resolve, 0));

    expect(screen.getByRole('button', { name: 'Sign in' })).toBeInTheDocument();
    expect(localStorage.length).toBe(0);
    expect(sessionStorage.length).toBe(0);
  });

  it('shows token loading, empty, error, and retry states', async () => {
    saveSession({ username: 'alice', token: 'jwt-token' });
    let rejectFirst: (cause?: unknown) => void = () => {};
    const firstRequest = new Promise<never>((_, reject) => {
      rejectFirst = reject;
    });
    const client = api({
      listTokens: vi
        .fn()
        .mockReturnValueOnce(firstRequest)
        .mockResolvedValueOnce({ items: [], page: 1, size: 20, total: 0 }),
    });
    const user = userEvent.setup();
    render(App, { props: { apiClient: client } });

    expect(screen.getByRole('status')).toHaveTextContent('Loading tokens');
    rejectFirst({ message: 'Service unavailable', requestId: 'request-123' });
    expect(await screen.findByRole('alert')).toHaveTextContent('Service unavailable');
    expect(screen.getByRole('alert')).toHaveTextContent('request-123');

    await user.click(screen.getByRole('button', { name: 'Retry' }));
    expect(await screen.findByText('No tokens yet.')).toBeInTheDocument();
    expect(client.listTokens).toHaveBeenLastCalledWith(1, 20, expect.objectContaining({ signal: expect.any(AbortSignal) }));
  });

  it('creates, copies, replaces, and clears plaintext while refetching token metadata', async () => {
    saveSession({ username: 'alice', token: 'jwt-token' });
    const writeText = vi.fn().mockResolvedValue(undefined);
    const client = api({
      listTokens: vi
        .fn()
        .mockResolvedValueOnce({ items: [], page: 1, size: 20, total: 0 })
        .mockResolvedValue({ items: [token()], page: 1, size: 20, total: 1 }),
      createToken: vi.fn().mockResolvedValue({ ...token(), token: 'mpsk_created' }),
      revealToken: vi.fn().mockResolvedValue({ ...token(), token: 'mpsk_revealed' }),
    });
    const user = userEvent.setup();
    render(App, { props: { apiClient: client } });
    await screen.findByText('No tokens yet.');
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } });

    await user.type(screen.getByLabelText('Token name'), 'CI clone');
    await user.selectOptions(screen.getByLabelText('Preset'), 'git-clone');
    await user.click(screen.getByRole('button', { name: 'Create token' }));

    expect(await screen.findByText('mpsk_created')).toBeInTheDocument();
    expect(sessionStorage.length).toBe(0);
    expect(client.createToken).toHaveBeenCalledWith(
      { name: 'CI clone', preset: 'git-clone', expiresAt: null },
      expect.objectContaining({ signal: expect.any(AbortSignal) }),
    );
    expect(client.listTokens).toHaveBeenCalledTimes(2);
    await user.click(screen.getByRole('button', { name: 'Copy token' }));
    expect(writeText).toHaveBeenCalledWith('mpsk_created');

    await user.click(screen.getByRole('button', { name: 'Reveal CI clone' }));
    expect(screen.queryByText('mpsk_created')).not.toBeInTheDocument();
    await user.type(screen.getByLabelText('Current password'), 'correct');
    await user.click(screen.getByRole('button', { name: 'Reveal token' }));
    expect(await screen.findByText('mpsk_revealed')).toBeInTheDocument();
    expect(sessionStorage.length).toBe(0);
    expect(client.revealToken).toHaveBeenCalledWith(
      token().id,
      'correct',
      expect.objectContaining({ signal: expect.any(AbortSignal) }),
    );

    await user.click(screen.getByRole('button', { name: 'Log out' }));
    expect(screen.queryByText('mpsk_revealed')).not.toBeInTheDocument();
    expect(sessionStorage.length).toBe(0);
  });

  it('paginates by page/size and refetches after revoke', async () => {
    saveSession({ username: 'alice', token: 'jwt-token' });
    const client = api({
      listTokens: vi.fn().mockResolvedValue({ items: [token()], page: 1, size: 20, total: 41 }),
      revokeToken: vi.fn().mockResolvedValue(undefined),
    });
    const user = userEvent.setup();
    render(App, { props: { apiClient: client } });
    await screen.findByText('CI clone');

    expect(screen.getByText('Page 1 of 3 · 41 total')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Next page' }));
    expect(client.listTokens).toHaveBeenLastCalledWith(2, 20, expect.any(Object));

    await user.selectOptions(screen.getByLabelText('Page size'), '10');
    expect(client.listTokens).toHaveBeenLastCalledWith(1, 10, expect.any(Object));

    await user.click(screen.getByRole('button', { name: 'Revoke CI clone' }));
    await waitFor(() => expect(client.revokeToken).toHaveBeenCalledWith(token().id, expect.any(Object)));
    await waitFor(() => expect(client.listTokens).toHaveBeenCalledTimes(4));
  });

  it('clamps and refetches after revoking the final item on the last page', async () => {
    saveSession({ username: 'alice', token: 'jwt-token' });
    const lastToken = token({ name: 'Last token' });
    const client = api({
      listTokens: vi
        .fn()
        .mockResolvedValueOnce({ items: [token()], page: 1, size: 20, total: 21 })
        .mockResolvedValueOnce({ items: [lastToken], page: 2, size: 20, total: 21 })
        .mockResolvedValueOnce({ items: [], page: 2, size: 20, total: 20 })
        .mockResolvedValueOnce({ items: [token({ name: 'First page token' })], page: 1, size: 20, total: 20 }),
      revokeToken: vi.fn().mockResolvedValue(undefined),
    });
    const user = userEvent.setup();
    render(App, { props: { apiClient: client } });
    await screen.findByText('CI clone');

    await user.click(screen.getByRole('button', { name: 'Next page' }));
    expect(await screen.findByText('Last token')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Revoke Last token' }));

    expect(await screen.findByText('First page token')).toBeInTheDocument();
    expect(client.listTokens).toHaveBeenLastCalledWith(1, 20, expect.any(Object));
    expect(screen.getByText('Page 1 of 1 · 20 total')).toBeInTheDocument();
  });

  it('ignores stale list responses after pagination changes', async () => {
    saveSession({ username: 'alice', token: 'jwt-token' });
    let resolveOldPage: (page: { items: ReturnType<typeof token>[]; page: number; size: number; total: number }) => void = () => {};
    const oldPage = new Promise<{ items: ReturnType<typeof token>[]; page: number; size: number; total: number }>((resolve) => {
      resolveOldPage = resolve;
    });
    const client = api({
      listTokens: vi
        .fn()
        .mockResolvedValueOnce({ items: [token()], page: 1, size: 20, total: 41 })
        .mockReturnValueOnce(oldPage)
        .mockResolvedValueOnce({ items: [token({ name: 'Newest page' })], page: 1, size: 10, total: 1 }),
    });
    const user = userEvent.setup();
    render(App, { props: { apiClient: client } });
    await screen.findByText('CI clone');

    await user.click(screen.getByRole('button', { name: 'Next page' }));
    await user.selectOptions(screen.getByLabelText('Page size'), '10');
    expect(await screen.findByText('Newest page')).toBeInTheDocument();

    resolveOldPage({ items: [token({ name: 'Stale page' })], page: 2, size: 20, total: 41 });
    await Promise.resolve();
    expect(screen.queryByText('Stale page')).not.toBeInTheDocument();
    expect(screen.getByText('Newest page')).toBeInTheDocument();
  });

  it('never displays a reveal that finishes after logout', async () => {
    saveSession({ username: 'alice', token: 'jwt-token' });
    let resolveReveal: (secret: ReturnType<typeof token>) => void = () => {};
    const reveal = new Promise<ReturnType<typeof token>>((resolve) => {
      resolveReveal = resolve;
    });
    const client = api({
      listTokens: vi.fn().mockResolvedValue({ items: [token()], page: 1, size: 20, total: 1 }),
      revealToken: vi.fn().mockReturnValue(reveal),
    });
    const user = userEvent.setup();
    render(App, { props: { apiClient: client } });
    await screen.findByText('CI clone');

    await user.click(screen.getByRole('button', { name: 'Reveal CI clone' }));
    await user.type(screen.getByLabelText('Current password'), 'correct');
    await user.click(screen.getByRole('button', { name: 'Reveal token' }));
    await user.click(screen.getByRole('button', { name: 'Log out' }));
    resolveReveal({ ...token(), token: 'mpsk_too_late' });

    await Promise.resolve();
    expect(screen.getByRole('button', { name: 'Sign in' })).toBeInTheDocument();
    expect(screen.queryByText('mpsk_too_late')).not.toBeInTheDocument();
  });

  it('does not restore create or reveal plaintext after unmount and never uses sessionStorage', async () => {
    saveSession({ username: 'alice', token: 'jwt-token' });
    let resolveCreate: (secret: ReturnType<typeof token>) => void = () => {};
    let resolveReveal: (secret: ReturnType<typeof token>) => void = () => {};
    const create = new Promise<ReturnType<typeof token>>((resolve) => {
      resolveCreate = resolve;
    });
    const reveal = new Promise<ReturnType<typeof token>>((resolve) => {
      resolveReveal = resolve;
    });
    const client = api({
      listTokens: vi.fn().mockResolvedValue({ items: [token()], page: 1, size: 20, total: 1 }),
      createToken: vi.fn().mockReturnValue(create),
      revealToken: vi.fn().mockReturnValue(reveal),
    });
    const user = userEvent.setup();
    const createView = render(App, { props: { apiClient: client } });
    await screen.findByText('CI clone');
    expect(sessionStorage.length).toBe(0);

    await user.type(screen.getByLabelText('Token name'), 'New token');
    await user.click(screen.getByRole('button', { name: 'Create token' }));
    createView.unmount();
    resolveCreate({ ...token({ name: 'New token' }), token: 'mpsk_create_too_late' });
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(document.body).not.toHaveTextContent('mpsk_create_too_late');
    expect(sessionStorage.length).toBe(0);

    const revealView = render(App, { props: { apiClient: client } });
    await screen.findByText('CI clone');
    await user.click(screen.getByRole('button', { name: 'Reveal CI clone' }));
    await user.type(screen.getByLabelText('Current password'), 'correct');
    await user.click(screen.getByRole('button', { name: 'Reveal token' }));
    expect(sessionStorage.length).toBe(0);
    revealView.unmount();
    resolveReveal({ ...token(), token: 'mpsk_reveal_too_late' });
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(document.body).not.toHaveTextContent('mpsk_reveal_too_late');
    expect(sessionStorage.length).toBe(0);
  });

  it('renders active, expired, and revoked token states', async () => {
    saveSession({ username: 'alice', token: 'jwt-token' });
    const client = api({
      listTokens: vi.fn().mockResolvedValue({
        items: [token(), token({ id: '2', name: 'Old token', status: 'expired' }), token({ id: '3', name: 'Stopped token', status: 'revoked' })],
        page: 1,
        size: 20,
        total: 3,
      }),
    });
    render(App, { props: { apiClient: client } });

    expect(await screen.findByText('active')).toBeInTheDocument();
    expect(screen.getByText('expired')).toBeInTheDocument();
    expect(screen.getByText('revoked')).toBeInTheDocument();
  });
});
