<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import type { ApiClient } from './api.js';
  import { asApiError, isAbortError, type ApiErrorLike, type Plugin, type Team, type UserProfile } from './types.js';
  export let apiClient: ApiClient;
  export let profile: UserProfile | null = null;
  export let creating = false;
  let namespace='', teams: Team[]=[], items: Plugin[]=[], state: 'loading'|'ready'|'error'='loading', error: ApiErrorLike | null = null, pending=false, name='', visibility='public', controller: AbortController | undefined;
  $: choices = profile ? [{ slug: profile.username, displayName: profile.displayName || profile.username }, ...teams] : teams;
  onMount(initialize); onDestroy(()=>controller?.abort());
  async function initialize(){controller?.abort();controller=new AbortController();state='loading';error=null;try{const result=await apiClient.listTeams(1,100,'',{signal:controller.signal});teams=result.items;if(!namespace)namespace=profile?.username||teams[0]?.slug||'';if(creating)state='ready';else await load();}catch(e){if(!isAbortError(e)){error=asApiError(e);state='error';}}}
  async function load(){if(!namespace){state='ready';return;}controller?.abort();controller=new AbortController();state='loading';error=null;try{const result=await apiClient.listPlugins(namespace,1,100,{signal:controller.signal});items=result.items;state='ready';}catch(e){if(!isAbortError(e)){error=asApiError(e);state='error';}}}
  async function create(event: SubmitEvent){event.preventDefault();pending=true;error=null;try{const created=await apiClient.createPlugin(namespace,{name,visibility});window.history.pushState({},'',`/plugins/${encodeURIComponent(created.namespace)}/${encodeURIComponent(created.name)}/code`);window.dispatchEvent(new PopStateEvent('popstate'));}catch(e){error=asApiError(e);}finally{pending=false;}}
</script>
<svelte:head><title>{creating?'Create a new Plugin':'Plugins'} · MarketplaceServer</title></svelte:head>
<section class="page plugin-list" class:create-page={creating}>
  {#if creating}
    <header class="page-header"><div><a data-route href="/plugins">Plugins</a><h1>Create a new Plugin</h1><p class="muted">A Plugin contains your source files and version history.</p></div></header>
    <p class="muted">Required fields are marked with an asterisk (*).</p>
    <form class="create-form" onsubmit={create}>
      <div class="owner-name"><div><label for="plugin-namespace">Owner *</label><select class="field" id="plugin-namespace" bind:value={namespace} disabled={pending}>{#each choices as choice}<option value={choice.slug}>{choice.slug}</option>{/each}</select></div><span aria-hidden="true">/</span><div><label for="plugin-name">Plugin name</label><input class="field" id="plugin-name" required pattern="[a-z0-9]+(?:-[a-z0-9]+)*" bind:value={name} disabled={pending}/></div></div>
      <p class="muted">Use lowercase letters, numbers, and hyphens. The name must match your Plugin manifest.</p>
      <div class="visibility-choice"><label for="plugin-visibility">Visibility</label><select class="field" id="plugin-visibility" bind:value={visibility} disabled={pending}><option value="public">Public</option><option value="private">Private</option></select><p class="muted">Public Plugins can be read by anyone. Private Plugins require access.</p></div>
      {#if error}<p class="error" role="alert">{error.status===409?'That Plugin name already exists in this namespace.':error.message}</p>{/if}
      {#if state==='error'}<button type="button" class="secondary" onclick={initialize}>Retry</button>{/if}
      <div class="create-footer"><button class="primary" disabled={pending||!namespace||state!=='ready'}>{pending?'Creating…':'Create Plugin'}</button></div>
    </form>
  {:else}
    <header class="page-header"><div><h1>Plugins</h1><p class="muted">Your Plugin repositories.</p></div><a class="primary" data-route href="/plugins/new">New Plugin</a></header>
    <div class="list-toolbar"><label for="plugin-namespace">Owner <select class="field compact" id="plugin-namespace" bind:value={namespace} onchange={load}>{#each choices as choice}<option value={choice.slug}>{choice.displayName} ({choice.slug})</option>{/each}</select></label><span class="muted">{items.length} Plugins</span></div>
    {#if state==='loading'}<p role="status">Loading Plugins…</p>{:else if state==='error'}<div role="alert"><p class="error">{error?.message}</p><button class="secondary" onclick={initialize}>Retry</button></div>{:else if !items.length}<div class="empty-repository"><h2>No Plugins in {namespace || 'this workspace'}</h2><p class="muted">Create a Plugin to start hosting your code.</p><a class="primary" data-route href="/plugins/new">New Plugin</a></div>{:else}<ul class="plugin-repositories">{#each items as item (item.name)}<li><h2><a data-route href={`/plugins/${encodeURIComponent(item.namespace)}/${encodeURIComponent(item.name)}`}>{item.namespace} / {item.name}</a><span class="status">{item.visibility}</span></h2><p class="muted">{item.status} · repository {item.repositoryStatus}{item.defaultVersion?` · default ${item.defaultVersion}`:''}</p></li>{/each}</ul>{/if}
  {/if}
</section>
