<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import { saveSession } from './session.js';
  import type { ApiClient } from './api.js';
  import { asApiError, isAbortError, type ApiErrorLike } from './types.js';
  export let apiClient: ApiClient;
  export let onAuthenticated: () => void = () => {};
  let capability: 'loading' | 'enabled' | 'disabled' | 'error' = 'loading';
  let error: ApiErrorLike | null = null;
  let pending = false, username = '', displayName = '', email = '', password = '';
  let controller: AbortController | undefined;
  onMount(load); onDestroy(() => controller?.abort());
  async function load() { controller?.abort(); controller = new AbortController(); capability = 'loading'; error = null; try { capability = (await apiClient.capabilities({ signal: controller.signal })).registrationEnabled === true ? 'enabled' : 'disabled'; } catch (cause) { if (!isAbortError(cause)) { error = asApiError(cause); capability = 'error'; } } }
  async function submit(event: SubmitEvent) { event.preventDefault(); controller?.abort(); controller = new AbortController(); pending = true; error = null; try { const result = await apiClient.register({ username, displayName, email: email || null, password }, { signal: controller.signal }); saveSession(result); onAuthenticated(); password = ''; } catch (cause) { if (!isAbortError(cause)) error = asApiError(cause); } finally { pending = false; } }
</script>
<svelte:head><title>Register · MarketplaceServer</title></svelte:head>
<section class="auth-page"><header class="auth-header"><p class="auth-brand">MarketplaceServer</p><h1>Create account</h1><p class="muted">Join your MarketplaceServer workspace.</p></header>
{#if capability === 'loading'}<p class="panel auth-state" role="status">Checking registration…</p>
{:else if capability === 'error'}<div class="panel auth-state" role="alert"><p class="error">{error?.message}</p><button class="secondary" onclick={load}>Retry</button></div>
{:else if capability === 'disabled'}<div class="panel auth-state"><p>Registration is not enabled.</p><a data-route href="/login">Return to sign in</a></div>
{:else}<form class="panel form-stack auth-form" onsubmit={submit}><label class="auth-label" for="register-username">Username</label><input class="field" id="register-username" autocomplete="username" required bind:value={username} /><label class="auth-label" for="display-name">Display name</label><input class="field" id="display-name" required bind:value={displayName} /><label class="auth-label" for="email">Email <span class="muted">(optional)</span></label><input class="field" id="email" type="email" autocomplete="email" bind:value={email} /><label class="auth-label" for="new-password">Password</label><input class="field" id="new-password" type="password" autocomplete="new-password" required bind:value={password} />{#if error}<p class="error" role="alert">{error.message}{error.status === 409 ? ' Choose different account details.' : ''}</p>{/if}<button class="primary" disabled={pending} aria-busy={pending}>{pending ? 'Creating…' : 'Create account'}</button><footer class="auth-footer"><a data-route href="/login">Already have an account?</a></footer></form>{/if}</section>
