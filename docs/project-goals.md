# MarketplaceServer Project Goals

## Mission

MarketplaceServer provides a self-hosted control and distribution plane for Claude Code Plugins and Marketplace indexes. It is intended for individuals, teams, enterprises, and task-specific environments that need reproducible Plugin delivery with explicit ownership and permissions.

## Product goals

1. Host one authoritative Git repository per Plugin with HTTPS clone, fetch, and push.
2. Publish immutable Plugin versions pinned to validated tags and full commit identities.
3. Compose multiple independent Marketplace templates without coupling their Plugin selections.
4. Expose stable HTTP JSON and read-only Git distribution endpoints backed by immutable projections.
5. Support users, personal and team namespaces, scoped API keys, and server-side authorization shared by REST, Git, and distribution paths.
6. Generate a private user Marketplace containing the latest installable versions of all Plugins the user can currently read.
7. Support enterprise isolation so different users, teams, agents, and CTF tasks can receive distinct Marketplace compositions and credentials.
8. Deliver the management frontend and backend as one Go binary, with a Svelte and Tailwind static application embedded through `embed.FS`.
9. Preserve operational integrity through audit events, reconciliation, durable jobs, backups, and explicit security revocation.

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
- Published projections are immutable and content-addressed; stable routes only switch validated pointers.
- Secrets, Authorization headers, private keys, and password-bearing URLs are never logged or embedded in the frontend.
- Tenant-scoped database queries and database constraints prevent cross-namespace ownership ambiguity.

## Current delivery baseline

The current implementation includes:

- deterministic module startup and dependency-safe shutdown;
- the five-module runtime foundation;
- embedded frontend serving boundaries;
- secure authentication primitives;
- users, personal namespaces, fixed `admin` and dynamic `default` group semantics;
- transactional bootstrap of the initial administrator;
- scoped personal access token create, list, and revoke APIs;
- unified authorization for current user, group, namespace, repository, Plugin, and Marketplace state;
- authenticated development Git Smart HTTP using passwords or API keys;
- immutable public Plugin and Marketplace distribution foundations;
- readable immutable Marketplace public keys;
- dynamically generated private user Marketplace JSON.

PostgreSQL-backed constraint and production-wiring tests require `MARKETPLACE_TEST_POSTGRES_DSN`; they skip explicitly when it is unavailable.

## Success criteria

MarketplaceServer reaches its intended product state when administrators can safely create users and teams, host and publish Plugins, compose independently versioned Marketplaces, distribute them through authenticated or public channels, manage the system through the embedded frontend, and recover the database and Git storage without violating published identities or tenant permissions.
