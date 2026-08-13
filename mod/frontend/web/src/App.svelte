<script>
  import { onDestroy, onMount } from 'svelte';
  import { createApiClient } from './api.js';
  import { clearSession, loadSession, saveSession } from './session.js';

  export let apiClient = createApiClient({ getSession: loadSession, clearSession });

  const presets = [
    { value: 'sub-read', label: 'Subscription read' },
    { value: 'git-clone', label: 'Git clone' },
    { value: 'git-write', label: 'Git write' },
  ];

  let session = null;
  let username = '';
  let password = '';
  let loginPending = false;
  let loginError = null;

  let items = [];
  let page = 1;
  let size = 20;
  let total = 0;
  let listState = 'idle';
  let listError = null;

  let tokenName = '';
  let preset = 'sub-read';
  let expiresAt = '';
  let createPending = false;
  let createError = null;

  let revealTarget = null;
  let revealPassword = '';
  let revealPending = false;
  let revealError = null;
  let secret = null;
  let copyState = '';
  let mutationError = null;

  let listController;
  let mutationController;
  let loginController;
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

  function errorText(error) {
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

  function handleUnauthorized(error) {
    if (error?.status === 401 && !loadSession()) {
      showLogin();
      return true;
    }
    return false;
  }

  async function login(event) {
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
      session = { username: result.username, token: result.token };
      password = '';
      page = 1;
      await loadTokens();
    } catch (error) {
      if (generation === loginGeneration && error?.name !== 'AbortError') loginError = error;
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
      if (error?.name === 'AbortError' || generation !== listGeneration || currentSession !== sessionGeneration) return;
      if (handleUnauthorized(error)) return;
      listError = error;
      listState = 'error';
    }
  }

  function beginMutation(kind) {
    mutationController?.abort();
    const controller = new AbortController();
    mutationController = controller;
    mutationKind = kind;
    return { controller, generation: ++mutationGeneration, session: sessionGeneration };
  }

  function mutationIsCurrent(operation) {
    return operation.generation === mutationGeneration && operation.session === sessionGeneration;
  }

  function finishMutation(operation) {
    if (!mutationIsCurrent(operation)) return;
    mutationKind = '';
    mutationController = undefined;
  }

  async function createToken(event) {
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
      if (mutationIsCurrent(operation) && error?.name !== 'AbortError' && !handleUnauthorized(error)) createError = error;
    } finally {
      if (mutationIsCurrent(operation)) createPending = false;
      finishMutation(operation);
    }
  }

  function beginReveal(item) {
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

  async function revealToken(event) {
    event.preventDefault();
    const operation = beginMutation('reveal');
    resetSecret();
    revealPending = true;
    revealError = null;
    try {
      const result = await apiClient.revealToken(revealTarget.id, revealPassword, {
        signal: operation.controller.signal,
      });
      if (!mutationIsCurrent(operation)) return;
      revealPassword = '';
      revealTarget = null;
      secret = { token: result.token, name: result.name, source: 'revealed' };
      await loadTokens();
    } catch (error) {
      if (mutationIsCurrent(operation) && error?.name !== 'AbortError' && !handleUnauthorized(error)) revealError = error;
    } finally {
      if (mutationIsCurrent(operation)) revealPending = false;
      finishMutation(operation);
    }
  }

  async function revokeToken(item) {
    const operation = beginMutation('revoke');
    mutationError = null;
    try {
      await apiClient.revokeToken(item.id, { signal: operation.controller.signal });
      if (!mutationIsCurrent(operation)) return;
      await loadTokens();
    } catch (error) {
      if (mutationIsCurrent(operation) && error?.name !== 'AbortError' && !handleUnauthorized(error)) mutationError = error;
    } finally {
      finishMutation(operation);
    }
  }

  async function copySecret() {
    try {
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

  function changeSize(event) {
    size = Number(event.currentTarget.value);
    page = 1;
    loadTokens();
  }

  function formatDate(value) {
    return value ? dateFormatter.format(new Date(value)) : 'Never';
  }
</script>

<svelte:head>
  <title>Identity · MarketplaceServer</title>
</svelte:head>

<main class="min-h-screen bg-slate-950 text-slate-100">
  {#if !session}
    <section class="mx-auto flex min-h-screen max-w-md flex-col justify-center px-6 py-16">
      <p class="text-sm font-semibold uppercase tracking-[0.3em] text-sky-300">MarketplaceServer</p>
      <h1 class="mt-4 text-3xl font-bold tracking-tight">Sign in</h1>
      <p class="mt-3 text-slate-400">Manage your personal access tokens.</p>

      <form class="mt-8 space-y-5 rounded-2xl border border-slate-800 bg-slate-900 p-6" onsubmit={login}>
        <label class="block text-sm font-medium" for="username">Username</label>
        <input class="field" id="username" name="username" autocomplete="username" required bind:value={username} />
        <label class="block text-sm font-medium" for="password">Password</label>
        <input class="field" id="password" name="password" type="password" autocomplete="current-password" required bind:value={password} />
        {#if loginError}<p class="error" role="alert">{errorText(loginError)}</p>{/if}
        <button class="primary w-full" type="submit" disabled={loginPending} aria-busy={loginPending}>{loginPending ? 'Signing in…' : 'Sign in'}</button>
      </form>
    </section>
  {:else}
    <div class="mx-auto max-w-6xl px-5 py-8 sm:px-8">
      <header class="flex flex-wrap items-center justify-between gap-4 border-b border-slate-800 pb-6">
        <div>
          <p class="text-sm font-semibold uppercase tracking-[0.25em] text-sky-300">MarketplaceServer</p>
          <h1 class="mt-2 text-3xl font-bold">Personal access tokens</h1>
          <p class="mt-1 text-sm text-slate-400">Signed in as {session.username}</p>
        </div>
        <button class="secondary" type="button" onclick={logout}>Log out</button>
      </header>

      <div class="mt-8 grid gap-8 lg:grid-cols-[20rem_minmax(0,1fr)]">
        <aside>
          <form class="panel space-y-4" onsubmit={createToken}>
            <div>
              <h2 class="text-lg font-semibold">Create token</h2>
              <p class="mt-1 text-sm text-slate-400">The plaintext is shown in this page only.</p>
            </div>
            <label class="block text-sm font-medium" for="token-name">Token name</label>
            <input class="field" id="token-name" maxlength="128" required bind:value={tokenName} />
            <label class="block text-sm font-medium" for="preset">Preset</label>
            <select class="field" id="preset" bind:value={preset}>
              {#each presets as option}<option value={option.value}>{option.label}</option>{/each}
            </select>
            <label class="block text-sm font-medium" for="expires-at">Expires at <span class="text-slate-500">(optional)</span></label>
            <input class="field" id="expires-at" type="datetime-local" bind:value={expiresAt} />
            {#if createError}<p class="error" role="alert">{errorText(createError)}</p>{/if}
            <button class="primary w-full" type="submit" disabled={mutationPending} aria-busy={createPending}>{createPending ? 'Creating…' : 'Create token'}</button>
          </form>
        </aside>

        <section aria-labelledby="token-list-heading">
          <div class="flex flex-wrap items-end justify-between gap-4">
            <div>
              <h2 class="text-xl font-semibold" id="token-list-heading">Your tokens</h2>
              <p class="mt-1 text-sm text-slate-400">Revoke credentials that are no longer needed.</p>
            </div>
            <label class="text-sm text-slate-300" for="page-size">Page size
              <select class="ml-2 rounded-lg border border-slate-700 bg-slate-900 px-2 py-1" id="page-size" value={size} onchange={changeSize}>
                <option value="10">10</option><option value="20">20</option><option value="50">50</option><option value="100">100</option>
              </select>
            </label>
          </div>

          {#if secret}
            <section class="mt-5 rounded-xl border border-emerald-700 bg-emerald-950/60 p-4" aria-labelledby="secret-heading">
              <h3 class="font-semibold text-emerald-200" id="secret-heading">{secret.source === 'created' ? 'Token created' : 'Token revealed'}: {secret.name}</h3>
              <p class="mt-1 text-sm text-emerald-100/80">Copy this plaintext before replacing it or logging out.</p>
              <code class="mt-3 block overflow-x-auto rounded-lg bg-slate-950 p-3 text-sm text-emerald-200">{secret.token}</code>
              <div class="mt-3 flex items-center gap-3">
                <button class="secondary" type="button" onclick={copySecret}>Copy token</button>
                {#if copyState}<span class="text-sm" aria-live="polite">{copyState}</span>{/if}
              </div>
            </section>
          {/if}

          {#if revealTarget}
            <form class="panel mt-5" onsubmit={revealToken}>
              <h3 class="font-semibold">Reveal {revealTarget.name}</h3>
              <p class="mt-1 text-sm text-slate-400">Confirm with your current account password.</p>
              <label class="mt-4 block text-sm font-medium" for="reveal-password">Current password</label>
              <input class="field mt-2" id="reveal-password" type="password" autocomplete="current-password" required bind:value={revealPassword} />
              {#if revealError}<p class="error mt-3" role="alert">{errorText(revealError)}</p>{/if}
              <div class="mt-4 flex gap-3">
                <button class="primary" type="submit" disabled={mutationPending} aria-busy={revealPending}>{revealPending ? 'Revealing…' : 'Reveal token'}</button>
                <button class="secondary" type="button" onclick={cancelReveal}>Cancel</button>
              </div>
            </form>
          {/if}

          {#if mutationError}<p class="error mt-5" role="alert">{errorText(mutationError)}</p>{/if}
          {#if listState === 'loading'}
            <p class="panel mt-5" role="status">Loading tokens…</p>
          {:else if listState === 'error'}
            <div class="panel mt-5" role="alert">
              <p class="error">{errorText(listError)}</p>
              <button class="secondary mt-3" type="button" onclick={loadTokens}>Retry</button>
            </div>
          {:else if listState === 'ready' && items.length === 0}
            <p class="panel mt-5 text-slate-400">No tokens yet.</p>
          {:else if listState === 'ready'}
            <ul class="mt-5 space-y-3">
              {#each items as item (item.id)}
                <li class="panel flex flex-wrap items-start justify-between gap-4">
                  <div class="min-w-0">
                    <div class="flex flex-wrap items-center gap-2">
                      <h3 class="font-semibold">{item.name}</h3>
                      <span class:status-active={item.status === 'active'} class:status-expired={item.status === 'expired'} class:status-revoked={item.status === 'revoked'} class="status">{item.status}</span>
                    </div>
                    <dl class="mt-2 grid gap-x-6 gap-y-1 text-sm text-slate-400 sm:grid-cols-2">
                      <div><dt class="inline">Preset: </dt><dd class="inline text-slate-300">{item.preset}</dd></div>
                      <div><dt class="inline">Expires: </dt><dd class="inline text-slate-300">{formatDate(item.expiresAt)}</dd></div>
                      <div><dt class="inline">Created: </dt><dd class="inline text-slate-300">{formatDate(item.createdAt)}</dd></div>
                      <div><dt class="inline">Last used: </dt><dd class="inline text-slate-300">{formatDate(item.lastUsedAt)}</dd></div>
                    </dl>
                  </div>
                  <div class="flex gap-2">
                    <button class="secondary" type="button" aria-label={`Reveal ${item.name}`} disabled={mutationPending} onclick={() => beginReveal(item)}>Reveal</button>
                    <button class="danger" type="button" aria-label={`Revoke ${item.name}`} disabled={item.status === 'revoked' || mutationPending} onclick={() => revokeToken(item)}>Revoke</button>
                  </div>
                </li>
              {/each}
            </ul>
          {/if}

          {#if listState === 'ready' && total > 0}
            <nav class="mt-5 flex flex-wrap items-center justify-between gap-3" aria-label="Token pages">
              <p class="text-sm text-slate-400">Page {page} of {pageCount} · {total} total</p>
              <div class="flex gap-2">
                <button class="secondary" type="button" aria-label="Previous page" disabled={page <= 1} onclick={previousPage}>Previous</button>
                <button class="secondary" type="button" aria-label="Next page" disabled={page >= pageCount} onclick={nextPage}>Next</button>
              </div>
            </nav>
          {/if}
        </section>
      </div>
    </div>
  {/if}
</main>
