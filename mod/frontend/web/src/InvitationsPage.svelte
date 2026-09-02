<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import type { ApiClient } from './api.js';
  import { asApiError, isAbortError, type ApiErrorLike, type Invitation } from './types.js';
  export let apiClient: ApiClient;
  let state: 'loading' | 'ready' | 'error' = 'loading', error: ApiErrorLike | null = null, items: Invitation[] = [], pending = '', controller: AbortController | undefined;
  onMount(load); onDestroy(() => controller?.abort());
  async function load() { controller?.abort(); controller=new AbortController(); state='loading'; error=null; try { const r=await apiClient.listMyInvitations(1,100,{signal:controller.signal}); items=r.items; state='ready'; } catch(cause) { if (!isAbortError(cause)) { error=asApiError(cause); state='error'; } } }
  async function respond(item: Invitation, accept: boolean) { pending=item.id; error=null; try { if (accept) await apiClient.acceptInvitation(item.id); else await apiClient.rejectInvitation(item.id); await load(); } catch(cause) { error=asApiError(cause); } finally { pending=''; } }
</script>
<svelte:head><title>Invitations · MarketplaceServer</title></svelte:head>
<div class="page"><header class="page-header"><div><p class="eyebrow">Inbox</p><h1>Team invitations</h1></div></header>
{#if error && state!=='error'}<p class="error panel" role="alert">{error.status===409?'This invitation is no longer pending.':error.message}</p>{/if}
{#if state==='loading'}<p class="panel" role="status">Loading invitations…</p>{:else if state==='error'}<div class="panel" role="alert"><p class="error">{error?.message}</p><button class="secondary" onclick={load}>Retry</button></div>{:else if !items.length}<p class="panel muted">No pending invitations.</p>{:else}<ul class="card-list">{#each items as invitation (invitation.id)}<li class="panel row"><div><h2>{invitation.teamDisplayName || invitation.teamSlug}</h2><p class="muted">Role: {invitation.role} · invited by {invitation.inviterUsername}</p></div><div class="actions"><button class="primary" disabled={Boolean(pending)} onclick={()=>respond(invitation,true)}>Accept</button><button class="secondary" disabled={Boolean(pending)} onclick={()=>respond(invitation,false)}>Reject</button></div></li>{/each}</ul>{/if}</div>
