# MarketplaceServer Project Goals

## Mission

MarketplaceServer provides a self-hosted control and distribution plane for Claude Code Plugins and Marketplace indexes. It is intended for individuals, teams, enterprises, and task-specific environments that need reproducible Plugin delivery with explicit ownership and permissions.

## Product goals

1. Make Plugin the user-facing resource and host its hidden one-to-one Git repository with HTTPS clone, fetch, and push.
2. Publish Plugin versions by selecting validated canonical `v`-prefixed SemVer tags; the tag's current full commit identity is authoritative for that version.
3. Compose multiple independent Marketplace templates without coupling their Plugin+tag selections.
4. Expose stable HTTP JSON and read-only Git distribution endpoints backed by validated projection artifacts.
5. Support users, personal and team namespaces, scoped API keys, and server-side authorization shared by REST, Git, and distribution paths.
6. Generate a private user Marketplace containing the latest installable versions of all Plugins the user can currently read.
7. Allow authorized users and teams to subscribe to team-shared Marketplaces, with membership and authorization re-evaluated so disabled users, departed members, and revoked access lose availability on the next request.
8. Support permission-isolated Marketplace profiles that combine enterprise-public Plugins with user-, team-, automation-, or CTF-task-scoped Plugins without expanding the visibility or credential scope of any source Plugin. Team-shared does not mean globally public.
9. Deliver the management frontend and backend as one Go binary, with a Svelte and Tailwind static application embedded through `embed.FS`.
10. Preserve operational integrity through audit events, reconciliation, durable jobs, backups, and explicit security revocation.

## Architectural boundaries

MarketplaceServer has exactly five runtime modules:

- `jin`: shared HTTP server and routing engine.
- `sql`: GORM database lifecycle and connection management.
- `git`: repository filesystem, Git subprocesses, Smart HTTP, and immutable projection storage.
- `backend`: identity, authorization, Plugin, version, Marketplace, token, and management APIs.
- `frontend`: embedded static management application and SPA fallback.

The public request planes remain separate:

- `/api/v1`: management and control APIs.
- `/git`: authenticated development repositories.
- `/distribution`: Claude Code installation and immutable distribution resources.

Credentials identify a principal but never replace action-specific authorization. Git protocol handlers and distribution handlers must not share control-plane handlers.

## Security and integrity goals

- Default deny for every non-public action.
- Passwords use bounded Argon2id hashes; API keys are stored only as peppered HMAC indexes.
- API-key scopes intersect with current user, group, namespace, and resource policy.
- Disabling a user or revoking a key takes effect on the next request.
- Public keys and distribution UUIDs are locators, not credentials.
- Distribution requests never mutate development repositories, projections, refs, or database state.
- Each built projection artifact is immutable and validated; a Marketplace revision retains its Plugin+tag configuration, and its projection pointer may switch to a rebuilt artifact when a selected tag moves.
- Secrets, Authorization headers, private keys, and password-bearing URLs are never logged or embedded in the frontend.
- Tenant-scoped database queries and database constraints prevent cross-namespace ownership ambiguity.

## Current delivery status

Current capabilities and known gaps are maintained in [Current implementation status](current-state.md). This goals document does not serve as a release-status inventory.

## Success criteria

MarketplaceServer reaches its intended product state when administrators can safely create users and teams, host and publish Plugins, compose independently versioned Marketplaces, distribute them through authenticated or public channels, manage the system through the embedded frontend, and recover the database and Git storage without violating published identities or tenant permissions.
