import { describe, expect, it } from 'vitest';
import { routeFor } from './router.js';

describe('frontend routes', () => {
  it.each([
    ['/login', { name: 'login' }],
    ['/register', { name: 'register' }],
    ['/tokens', { name: 'tokens' }],
    ['/plugins', { name: 'plugins' }],
    ['/plugins/acme%20inc/my-plugin', { name: 'plugin', namespace: 'acme inc', plugin: 'my-plugin', tab: 'code', path: '' }],
    ['/plugins/acme/my-plugin/code/src%2Fmain.go', { name: 'plugin', namespace: 'acme', plugin: 'my-plugin', tab: 'code', path: 'src/main.go' }],
    ['/plugins/acme/my-plugin/versions', { name: 'plugin', namespace: 'acme', plugin: 'my-plugin', tab: 'versions', path: '' }],
    ['/teams', { name: 'teams' }],
    ['/invitations', { name: 'invitations' }],
    ['/admin/users', { name: 'admin-users' }],
    ['/teams/red%20team', { name: 'team', slug: 'red team' }],
  ])('matches %s', (path, expected) => expect(routeFor(path)).toEqual(expected));

  it('does not accept nested, malformed, or unknown paths', () => {
    expect(routeFor('/teams/security/members')).toEqual({ name: 'not-found' });
    expect(routeFor('/plugins/%E0%A4%A/my-plugin')).toEqual({ name: 'not-found' });
    expect(routeFor('/unknown')).toEqual({ name: 'not-found' });
  });
});
