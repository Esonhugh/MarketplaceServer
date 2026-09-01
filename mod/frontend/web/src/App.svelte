<script>
  import { onDestroy } from 'svelte';
  import { createApiClient } from './api.js';
  import { clearSession, loadSession } from './session.js';
  import { useRouter } from './router.js';
  import IdentityPage from './IdentityPage.svelte';
  import RegisterPage from './RegisterPage.svelte';
  import PluginsPage from './PluginsPage.svelte';
  import PluginPage from './PluginPage.svelte';
  import TeamsPage from './TeamsPage.svelte';
  import TeamPage from './TeamPage.svelte';
  import InvitationsPage from './InvitationsPage.svelte';
  import AdminUsersPage from './AdminUsersPage.svelte';

  export let apiClient = createApiClient({ getSession: loadSession, clearSession });
  let route = { name: 'login' };
  let profile = null;
  let profileController;
  let router = useRouter((next) => {
    if (!['login', 'register', 'not-found'].includes(next.name) && !loadSession()) {
      route = { name: 'login' };
      if (window.location.pathname !== '/login') router.navigate('/login', { replace: true });
      return;
    }
    route = next;
    if (loadSession() && !profile) loadProfile();
  });
  async function loadProfile() {
    profileController?.abort(); profileController = new AbortController();
    try { profile = await apiClient.me({ signal: profileController.signal }); } catch (error) { if (error?.status === 401) router.navigate('/login', { replace: true }); }
  }
  function logout() { clearSession(); profile = null; profileController?.abort(); router.navigate('/login', { replace: true }); }
  onDestroy(() => profileController?.abort());
  $: protectedRoute = !['login', 'register', 'not-found'].includes(route.name);
  $: activeSection = route.name === 'team' ? 'teams' : route.name === 'plugin' ? 'plugins' : route.name;
</script>

<main>
  {#if protectedRoute && loadSession()}
    <header class="site-header">
      <nav class="site-nav" aria-label="Main navigation">
        <a class="brand" data-route href="/plugins"><span class="brand-mark" aria-hidden="true">M</span>MarketplaceServer</a>
        <div class="nav-links"><a data-route href="/plugins">Plugins</a><a data-route href="/teams">Teams</a><a data-route href="/tokens">Tokens</a><span class="header-user">{profile?.username || loadSession()?.username}</span><button class="secondary" onclick={logout}>Log out</button></div>
      </nav>
    </header>
    <div class="app-frame">
      <nav class="side-nav" aria-label="Management sections">
        <p class="side-nav-heading">Workspace</p>
        <a data-route href="/plugins" aria-current={activeSection === 'plugins' ? 'page' : undefined}>Plugins</a>
        <a data-route href="/teams" aria-current={activeSection === 'teams' ? 'page' : undefined}>Teams</a>
        <a data-route href="/invitations" aria-current={activeSection === 'invitations' ? 'page' : undefined}>Invitations</a>
        <a data-route href="/tokens" aria-current={activeSection === 'tokens' ? 'page' : undefined}>Access tokens</a>
        {#if profile?.systemAdmin}<p class="side-nav-heading">Administration</p><a data-route href="/admin/users" aria-current={activeSection === 'admin-users' ? 'page' : undefined}>Users</a>{/if}
      </nav>
      <div class="app-content">
        {#if route.name === 'tokens'}
          <IdentityPage {apiClient} />
        {:else if route.name === 'plugins'}
          <PluginsPage {apiClient} profile={profile} />
        {:else if route.name === 'plugin'}
          {#key `${route.namespace}/${route.plugin}/${route.tab}/${route.path}`}
            <PluginPage {apiClient} namespace={route.namespace} plugin={route.plugin} tab={route.tab} path={route.path} />
          {/key}
        {:else if route.name === 'teams'}
          <TeamsPage {apiClient} />
        {:else if route.name === 'team'}
          <TeamPage {apiClient} slug={route.slug} />
        {:else if route.name === 'invitations'}
          <InvitationsPage {apiClient} />
        {:else if route.name === 'admin-users'}
          {#if profile && !profile.systemAdmin}<section class="page"><div class="panel" role="alert"><h1>Access denied</h1><p>You do not have system administrator permission.</p></div></section>{:else}<AdminUsersPage {apiClient} />{/if}
        {:else}
          <section class="page"><div class="panel"><h1>Page not found</h1><a data-route href="/plugins">Go to Plugins</a></div></section>
        {/if}
      </div>
    </div>
  {:else if route.name === 'login'}
    <IdentityPage {apiClient} />
  {:else if route.name === 'register'}
    <RegisterPage {apiClient} navigate={router.navigate} />
  {:else}
    <section class="auth-page"><div class="panel"><h1>Page not found</h1><a data-route href="/login">Go to sign in</a></div></section>
  {/if}
</main>
