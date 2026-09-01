import { onMount } from 'svelte';

function decodePathSegment(value) {
  try {
    return decodeURIComponent(value);
  } catch {
    return null;
  }
}

export function routeFor(pathname) {
  if (pathname === '/' || pathname === '/login') return { name: 'login' };
  if (pathname === '/register') return { name: 'register' };
  if (pathname === '/tokens') return { name: 'tokens' };
  if (pathname === '/plugins') return { name: 'plugins' };
  if (pathname === '/teams') return { name: 'teams' };
  if (pathname === '/invitations') return { name: 'invitations' };
  if (pathname === '/admin/users') return { name: 'admin-users' };
  const plugin = pathname.match(/^\/plugins\/([^/]+)\/([^/]+)(?:\/(code|commits|versions|settings))?(?:\/(.*))?$/);
  if (plugin) {
    const namespace = decodePathSegment(plugin[1]);
    const name = decodePathSegment(plugin[2]);
    const path = plugin[4] ? decodePathSegment(plugin[4]) : '';
    if (namespace !== null && name !== null && path !== null) return { name: 'plugin', namespace, plugin: name, tab: plugin[3] || 'code', path };
  }
  const team = pathname.match(/^\/teams\/([^/]+)$/);
  if (team) {
    const slug = decodePathSegment(team[1]);
    if (slug !== null) return { name: 'team', slug };
  }
  return { name: 'not-found' };
}

export function useRouter(onChange) {
  function update() { onChange(routeFor(window.location.pathname)); }
  function navigate(path, { replace = false } = {}) {
    window.history[replace ? 'replaceState' : 'pushState']({}, '', path);
    update();
  }
  function click(event) {
    const anchor = event.target.closest('a[data-route]');
    if (!anchor || event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
    if (anchor.origin !== window.location.origin) return;
    event.preventDefault();
    navigate(anchor.pathname + anchor.search + anchor.hash);
  }
  onMount(() => {
    window.addEventListener('popstate', update);
    document.addEventListener('click', click);
    update();
    return () => {
      window.removeEventListener('popstate', update);
      document.removeEventListener('click', click);
    };
  });
  return { navigate };
}
