<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import { createApiClient } from './api.js';
  import { clearSession, loadSession, saveSession } from './session.js';
  import type { ApiClient } from './api.js';
  import { asApiError, isAbortError, type ApiErrorLike, type Session, type Token, type TokenSecret } from './types.js';

  export let apiClient: ApiClient = createApiClient({ getSession: loadSession, clearSession });
  export let onAuthenticated: () => boolean = () => false;
  export let showLogout = true;

  const presets = [
    { value: 'sub-read', label: 'Subscription read' },
    { value: 'git-clone', label: 'Git clone' },
    { value: 'git-write', label: 'Git write' },
  ];

  let session: Session | null = null;
  type MutationOperation = { controller: AbortController; generation: number; session: number };
  let username = '';
  let password = '';
  let loginPending = false;
  let loginError: ApiErrorLike | null = null;

  let items: Token[] = [];
  let page = 1;
  let size = 20;
  let total = 0;
  let listState: 'idle' | 'loading' | 'ready' | 'error' = 'idle';
  let listError: ApiErrorLike | null = null;

  let tokenName = '';
  let preset = 'sub-read';
  let expiresAt = '';
  let createPending = false;
  let createError: ApiErrorLike | null = null;

  let revealTarget: Token | null = null;
  let revealPassword = '';
  let revealPending = false;
  let revealError: ApiErrorLike | null = null;
  let secret: (TokenSecret & { source: 'created' | 'revealed' }) | null = null;
  let copyState = '';
  let mutationError: ApiErrorLike | null = null;

  let listController: AbortController | undefined;
  let mutationController: AbortController | undefined;
  let loginController: AbortController | undefined;
  let listGeneration = 0;
  let mutationGeneration = 0;
  let loginGeneration = 0;
  let sessionGeneration = 0;
  let mutationKind = '';
  const dateFormatter = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' });

  $: pageCount = Math.max(1, Math.ceil(total / size));
  $: mutationPending = mutationKind !== '';

  onMount(() => {
    session = loadSession();
    if (session) loadTokens();
  });

  onDestroy(() => {
    sessionGeneration += 1;
    listGeneration += 1;
    mutationGeneration += 1;
    loginGeneration += 1;
    secret = null;
    revealPassword = '';
    password = '';
    listController?.abort();
    mutationController?.abort();
    loginController?.abort();
  });

  function errorText(error: ApiErrorLike | null) {
    if (!error) return '';
    return error.requestId ? `${error.message} (request ${error.requestId})` : error.message || 'Request failed';
  }

  function resetSecret() {
    secret = null;
    copyState = '';
  }

  function invalidateRequests() {
    sessionGeneration += 1;
    listGeneration += 1;
    mutationGeneration += 1;
    loginGeneration += 1;
    listController?.abort();
    mutationController?.abort();
    loginController?.abort();
    mutationKind = '';
    loginPending = false;
    createPending = false;
    revealPending = false;
  }

  function showLogin() {
    invalidateRequests();
    session = null;
    items = [];
    total = 0;
    listState = 'idle';
    listError = null;
    createError = null;
    revealError = null;
    mutationError = null;
    revealTarget = null;
    revealPassword = '';
    password = '';
    resetSecret();
  }

  function handleUnauthorized(cause: unknown) {
    const error = asApiError(cause);
    if (error.status === 401 && !loadSession()) {
      showLogin();
      return true;
    }
    return false;
  }

  async function login(event: SubmitEvent) {
    event.preventDefault();
    loginController?.abort();
    loginController = new AbortController();
    const generation = ++loginGeneration;
    loginPending = true;
    loginError = null;
    try {
      const result = await apiClient.login(username, password, { signal: loginController.signal });
      if (generation !== loginGeneration) return;
      invalidateRequests();
      saveSession({ username: result.username, token: result.token });
      if (onAuthenticated()) return;
      session = { username: result.username, token: result.token };
      password = '';
      page = 1;
      await loadTokens();
    } catch (error) {
      if (generation === loginGeneration && !isAbortError(error)) loginError = asApiError(error);
    } finally {
      if (generation === loginGeneration) loginPending = false;
    }
  }

  function logout() {
    clearSession();
    showLogin();
  }

  async function loadTokens() {
    listController?.abort();
    const controller = new AbortController();
    const generation = ++listGeneration;
    const currentSession = sessionGeneration;
    listController = controller;
    listState = 'loading';
    listError = null;
    try {
      const result = await apiClient.listTokens(page, size, { signal: controller.signal });
      if (generation !== listGeneration || currentSession !== sessionGeneration) return;
      const lastPage = Math.max(1, Math.ceil(result.total / result.size));
      if (result.page > lastPage) {
        page = lastPage;
        size = result.size;
        total = result.total;
        await loadTokens();
        return;
      }
      items = result.items;
      page = result.page;
      size = result.size;
      total = result.total;
      listState = 'ready';
    } catch (error) {
      if (isAbortError(error) || generation !== listGeneration || currentSession !== sessionGeneration) return;
      if (handleUnauthorized(error)) return;
      listError = asApiError(error);
      listState = 'error';
    }
  }

  function beginMutation(kind: string): MutationOperation {
    mutationController?.abort();
    const controller = new AbortController();
    mutationController = controller;
    mutationKind = kind;
    return { controller, generation: ++mutationGeneration, session: sessionGeneration };
  }

  function mutationIsCurrent(operation: MutationOperation) {
    return operation.generation === mutationGeneration && operation.session === sessionGeneration;
  }

  function finishMutation(operation: MutationOperation) {
    if (!mutationIsCurrent(operation)) return;
    mutationKind = '';
    mutationController = undefined;
  }

  async function createToken(event: SubmitEvent) {
    event.preventDefault();
    const operation = beginMutation('create');
    resetSecret();
    createPending = true;
    createError = null;
    mutationError = null;
    try {
      const result = await apiClient.createToken(
        {
          name: tokenName,
          preset,
          expiresAt: expiresAt ? new Date(expiresAt).toISOString() : null,
        },
        { signal: operation.controller.signal },
      );
      if (!mutationIsCurrent(operation)) return;
      secret = { token: result.token, name: result.name, source: 'created' };
      tokenName = '';
      preset = 'sub-read';
      expiresAt = '';
      page = 1;
      await loadTokens();
    } catch (error) {
      if (mutationIsCurrent(operation) && !isAbortError(error) && !handleUnauthorized(error)) createError = asApiError(error);
    } finally {
      if (mutationIsCurrent(operation)) createPending = false;
      finishMutation(operation);
    }
  }

  function beginReveal(item: Token) {
    resetSecret();
    revealTarget = item;
    revealPassword = '';
    revealError = null;
    mutationError = null;
  }

  function cancelReveal() {
    if (mutationKind === 'reveal') {
      mutationController?.abort();
      mutationGeneration += 1;
      mutationKind = '';
      revealPending = false;
    }
    revealTarget = null;
    revealPassword = '';
    revealError = null;
  }

  async function revealToken(event: SubmitEvent) {
    event.preventDefault();
    if (!revealTarget) return;
    const target = revealTarget;
    const operation = beginMutation('reveal');
    resetSecret();
    revealPending = true;
    revealError = null;
    try {
      const result = await apiClient.revealToken(target.id, revealPassword, {
        signal: operation.controller.signal,
      });
      if (!mutationIsCurrent(operation)) return;
      revealPassword = '';
      revealTarget = null;
      secret = { token: result.token, name: result.name, source: 'revealed' };
      await loadTokens();
    } catch (error) {
      if (mutationIsCurrent(operation) && !isAbortError(error) && !handleUnauthorized(error)) revealError = asApiError(error);
    } finally {
      if (mutationIsCurrent(operation)) revealPending = false;
      finishMutation(operation);
    }
  }

  async function revokeToken(item: Token) {
    const operation = beginMutation('revoke');
    mutationError = null;
    try {
      await apiClient.revokeToken(item.id, { signal: operation.controller.signal });
      if (!mutationIsCurrent(operation)) return;
      await loadTokens();
    } catch (error) {
      if (mutationIsCurrent(operation) && !isAbortError(error) && !handleUnauthorized(error)) mutationError = asApiError(error);
    } finally {
      finishMutation(operation);
    }
  }

  async function copySecret() {
    try {
      if (!secret) return;
      await navigator.clipboard.writeText(secret.token);
      copyState = 'Copied';
    } catch {
      copyState = 'Copy failed. Select the token and copy it manually.';
    }
  }

  function previousPage() {
    if (page > 1) {
      page -= 1;
      loadTokens();
    }
  }

  function nextPage() {
    if (page < pageCount) {
      page += 1;
      loadTokens();
    }
  }

  function changeSize(event: Event) {
    size = Number((event.currentTarget as HTMLSelectElement).value);
    page = 1;
    loadTokens();
  }

  function formatDate(value: string | null | undefined) {
    return value ? dateFormatter.format(new Date(value)) : 'Never';
  }
</script>

<svelte:head>
  <title>Identity · MarketplaceServer</title>
</svelte:head>

{#if !session}
  <section class="auth-page">
    <header class="auth-header">
      <p class="auth-brand">MarketplaceServer</p>
      <h1>Sign in</h1>
      <p class="muted">Use your MarketplaceServer account.</p>
    </header>
    <form class="panel form-stack auth-form" onsubmit={login}>
      <label class="auth-label" for="username">Username</label>
      <input class="field" id="username" name="username" autocomplete="username" required bind:value={username} />
      <label class="auth-label" for="password">Password</label>
      <input class="field" id="password" name="password" type="password" autocomplete="current-password" required bind:value={password} />
      {#if loginError}<p class="error" role="alert">{errorText(loginError)}</p>{/if}
      <button class="primary" type="submit" disabled={loginPending} aria-busy={loginPending}>{loginPending ? 'Signing in…' : 'Sign in'}</button>
      <footer class="auth-footer"><a data-route href="/register">Create an account</a></footer>
    </form>
  </section>
{:else}
  <section class="page">
    <header class="page-header"><div><p class="eyebrow">Developer settings</p><h1>Personal access tokens</h1><p class="muted">Credentials owned by {session.username}.</p></div>{#if showLogout}<button class="secondary" type="button" onclick={logout}>Log out</button>{/if}</header>
    <form class="panel form-inline" onsubmit={createToken}>
      <h2>Create token</h2>
      <label for="token-name">Token name</label><input class="field" id="token-name" maxlength="128" required bind:value={tokenName} />
      <label for="preset">Preset</label><select class="field" id="preset" bind:value={preset}>{#each presets as option}<option value={option.value}>{option.label}</option>{/each}</select>
      <label for="expires-at">Expires at (optional)</label><input class="field" id="expires-at" type="datetime-local" bind:value={expiresAt} />
      {#if createError}<p class="error" role="alert">{errorText(createError)}</p>{/if}
      <button class="primary" type="submit" disabled={Boolean(mutationPending)} aria-busy={createPending}>{createPending ? 'Creating…' : 'Create token'}</button>
    </form>

    {#if secret}<section class="panel" aria-labelledby="secret-heading"><h2 id="secret-heading">{secret.source === 'created' ? 'Token created' : 'Token revealed'}: {secret.name}</h2><p class="muted">Copy this plaintext before replacing it or logging out.</p><pre class="code-view">{secret.token}</pre><div class="actions"><button class="secondary" type="button" onclick={copySecret}>Copy token</button>{#if copyState}<span aria-live="polite">{copyState}</span>{/if}</div></section>{/if}
    {#if revealTarget}<form class="panel form-inline" onsubmit={revealToken}><h2>Reveal {revealTarget.name}</h2><label for="reveal-password">Current password</label><input class="field" id="reveal-password" type="password" autocomplete="current-password" required bind:value={revealPassword} />{#if revealError}<p class="error" role="alert">{errorText(revealError)}</p>{/if}<div class="actions"><button class="primary" type="submit" disabled={Boolean(mutationPending)} aria-busy={revealPending}>{revealPending ? 'Revealing…' : 'Reveal token'}</button><button class="secondary" type="button" onclick={cancelReveal}>Cancel</button></div></form>{/if}

    <div class="page-header"><div><h2>Your tokens</h2><p class="muted">Revoke credentials that are no longer needed.</p></div><label for="page-size">Page size <select class="field compact" id="page-size" value={size} onchange={changeSize}><option value="10">10</option><option value="20">20</option><option value="50">50</option><option value="100">100</option></select></label></div>
    {#if mutationError}<p class="error" role="alert">{errorText(mutationError)}</p>{/if}
    {#if listState === 'loading'}<p class="panel" role="status">Loading tokens…</p>{:else if listState === 'error'}<div class="panel" role="alert"><p class="error">{errorText(listError)}</p><button class="secondary" type="button" onclick={loadTokens}>Retry</button></div>{:else if listState === 'ready' && items.length === 0}<p class="panel muted">No tokens yet.</p>{:else if listState === 'ready'}
      <ul class="plain-list">{#each items as item (item.id)}<li class="row"><div><h3>{item.name} <span class:status-active={item.status === 'active'} class:status-expired={item.status === 'expired'} class:status-revoked={item.status === 'revoked'} class="status">{item.status}</span></h3><p class="muted">{item.preset} · expires {formatDate(item.expiresAt)} · last used {formatDate(item.lastUsedAt)}</p></div><div class="actions"><button class="secondary" type="button" aria-label={`Reveal ${item.name}`} disabled={Boolean(mutationPending)} onclick={() => beginReveal(item)}>Reveal</button><button class="danger" type="button" aria-label={`Revoke ${item.name}`} disabled={item.status === 'revoked' || mutationPending} onclick={() => revokeToken(item)}>Revoke</button></div></li>{/each}</ul>
    {/if}
    {#if listState === 'ready' && total > 0}<nav class="row" aria-label="Token pages"><p class="muted">Page {page} of {pageCount} · {total} total</p><div class="actions"><button class="secondary" type="button" aria-label="Previous page" disabled={page <= 1} onclick={previousPage}>Previous</button><button class="secondary" type="button" aria-label="Next page" disabled={page >= pageCount} onclick={nextPage}>Next</button></div></nav>{/if}
  </section>
{/if}
