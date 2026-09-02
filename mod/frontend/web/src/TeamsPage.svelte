<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import type { ApiClient } from './api.js';
  import { asApiError, isAbortError, type ApiErrorLike, type Team } from './types.js';
  export let apiClient: ApiClient;
  let state: 'loading' | 'ready' | 'error' = 'loading', error: ApiErrorLike | null = null, items: Team[] = [], pending = false, slug = '', displayName = '', controller: AbortController | undefined;
  onMount(load); onDestroy(() => controller?.abort());
  async function load() { controller?.abort(); controller = new AbortController(); state='loading'; error=null; try { const r=await apiClient.listTeams(1,100,'',{signal:controller.signal}); items=r.items; state='ready'; } catch(cause) { if (!isAbortError(cause)) { error=asApiError(cause); state='error'; } } }
  async function create(event: SubmitEvent) { event.preventDefault(); pending=true; error=null; try { await apiClient.createTeam({slug,displayName}); slug=''; displayName=''; await load(); } catch(cause) { error=asApiError(cause); } finally { pending=false; } }
</script>
<svelte:head><title>Teams · MarketplaceServer</title></svelte:head>
<div class="page"><header class="page-header"><div><p class="eyebrow">Workspace</p><h1>Teams</h1><p class="muted">Teams and access are managed by server policy.</p></div></header>
<form class="panel form-inline" onsubmit={create}><h2>Create team</h2><label for="team-slug">Slug</label><input class="field" id="team-slug" required pattern="[a-z0-9]+(?:-[a-z0-9]+)*" bind:value={slug}/><label for="team-name">Display name</label><input class="field" id="team-name" required bind:value={displayName}/>{#if error?.status===409}<p class="error" role="alert">That team slug is already in use.</p>{/if}<button class="primary" disabled={pending}>{pending?'Creating…':'Create team'}</button></form>
{#if state==='loading'}<p class="panel" role="status">Loading teams…</p>{:else if state==='error'}<div class="panel" role="alert"><p class="error">{error?.message}</p><button class="secondary" onclick={load}>Retry</button></div>{:else if !items.length}<p class="panel muted">You do not have any teams yet.</p>{:else}<ul class="card-list">{#each items as team (team.slug)}<li class="panel"><h2><a data-route href={`/teams/${encodeURIComponent(team.slug)}`}>{team.displayName}</a></h2><p class="muted">{team.slug}{team.currentUserRole ? ` · ${team.currentUserRole}` : ''}</p></li>{/each}</ul>{/if}</div>
