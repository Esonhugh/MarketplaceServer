# MarketplaceServer Roadmap

This roadmap is the authoritative future delivery order. Items listed here are planned unless [current-state.md](current-state.md) explicitly classifies them as implemented or foundation capability; source code and tests remain authoritative.

## Near-term: complete the single-user Plugin workflow

### Plugin management and hidden Git repository

- Add tenant-scoped Plugin create, read, and delete lifecycle APIs; do not expose independent repository CRUD.
- Creation accepts only a lowercase kebab-case Plugin name, defaults to public, and atomically creates the hidden one-to-one repository.
- Use Plugin name as the immutable slug/Git path; IDs, storage placement and clone URL remain server-managed.
- Make Plugin read authorization also govern clone/fetch; keep push under separate Plugin write authorization.
- Add repository provisioning states and recoverable filesystem jobs as Plugin implementation details.
- Before protected default-branch/tag updates, export proposed commits in isolation, run `claude plugin validate`, and require exact manifest/Plugin name equality; tag validation is strict.
- Add bounded repository size, request size, push concurrency, operation quotas, durable push events, and ref reconciliation.

### Authentication hardening remaining work

Login、固定 30 天 HS256 JWT、management Bearer-only、PAT 三档 preset、可选 expiry、owner reveal、page/size/total、credential plane separation 与当前 resource-policy intersection 已实现，见 [current-state.md](current-state.md)。后续只保留未交付项：

- Add authentication and token lifecycle audit events without logging password, PAT, Authorization, or request bodies.
- Add operator-supported secret rotation procedures for PAT peppers and bootstrap credentials; JWT key-ring rotation remains deferred.
- Add an explicit non-development migration/rotation plan for legacy PAT schemas; current startup guard refuses automatic backfill or destructive conversion.

### Production verification

- Run all PostgreSQL suites in CI using an isolated database.
- Add production-wired distribution tests for advertisement, upload-pack, revocation, private parents, unknown services, and write-route rejection.
- Verify real Git clients against repository default-branch initialization and clone checkout behavior.
- Add versioned migration handling for any legacy repository status values.

## Next: tag-driven Plugin versions and Marketplace publication

### Plugin versions

- Publish by selecting an existing unpublished canonical `v`-prefixed SemVer tag; ignore manifest `version` as release identity.
- Treat each published tag as one logical version and track the tag's current full commit SHA.
- Reject protected tag updates unless strict Claude Plugin validation and exact manifest-name checks pass.
- On tag movement, update the same version and prebuild every Marketplace revision that references the Plugin+tag.
- Prefer the highest stable SemVer for latest; use the highest prerelease only when no stable version exists.

### Marketplace authoring

- Add Marketplace template and mutable Plugin+tag draft management APIs.
- Publish a retained revision configuration containing an ordered Plugin+tag selection.
- Generate deterministic `marketplace.json` and independent HTTPS/later SSH projection artifacts when outputs differ.
- Rebuild every referencing revision projection when a selected tag moves; reject the tag push when any validation or build fails.
- Switch stable directly to an existing revision whose current projection is ready.
- Resolve the Git-ref/database-projection-pointer failure boundary before implementing this workflow in production; do not claim cross-system atomicity.
- Reject private publication until dedicated private distribution credentials and policy are complete.

### Distribution credentials

- Add per-user, per-Marketplace private distribution credentials.
- Decide in that system contract whether distribution credentials are repeatably revealable; if so, apply ADR-0006, otherwise store only a peppered HMAC and return plaintext once.
- Support multiple named credentials, optional expiry, and independent revocation.
- Recheck active account and current Marketplace authorization on every request.
- Support the same credential in HTTP JSON Authorization headers and Git Credential Helper flows without embedding passwords in URLs.

## Then: teams, RBAC, and audit

- Add team namespaces, team lifecycle, invitations, and membership management.
- Implement owner, admin, maintainer, developer, and viewer action matrices.
- Keep system administration distinct from team ownership and audit cross-tenant administration explicitly.
- Add service accounts and separately scoped automation credentials.
- Add append-only audit storage and tenant-scoped audit query APIs.
- Add transactional outbox events for high-risk control-plane mutations.
- Verify immediate permission loss when a user leaves a team or is disabled.

## Then: SSH Git and transport parity

- Add a dedicated SSH listener with persisted host keys.
- Authenticate users and service accounts by active SSH public-key fingerprints.
- Accept only strict `git-upload-pack` and `git-receive-pack` exec requests.
- Reject shell, PTY, forwarding, arbitrary environment, and unknown commands.
- Reuse the same Plugin-backed repository resolver and read/write authorization as HTTPS Smart HTTP.
- Add SSH Marketplace source variants and installation guidance.
- Verify HTTPS and SSH authorization parity with real Git clients.

## Then: complete the Svelte management frontend

Login 与当前用户 PAT management 页面已实现；first-run setup 和其余管理流程仍未交付。

- Implement first-run setup flow.
- Add namespace switching and user/team management.
- Add Plugin, version, Marketplace, credential, and audit pages; repository details remain hidden behind the Plugin product model.
- Display HTTPS and SSH clone/install instructions without exposing secrets.
- Use one handwritten API client, local page state, and explicit list/detail refetch after mutations; do not add a global query/cache framework initially.
- Handle loading, empty, forbidden, not-found, conflict, and retry states.
- Add keyboard navigation, focus management, semantic labels, and accessible dialogs.
- Add frontend checks, tests, static builds, and embedded-production integration tests to CI.

## Enterprise and operations

- Add durable worker leases, bounded retries, dead-letter handling, and idempotent consumers.
- Add storage integrity scans, reconciliation dashboards, and security revocation workflows.
- Add readiness, liveness, metrics, traces, and capacity alerts.
- Add repository placement or shared POSIX storage design before horizontal scaling.
- Add database PITR, Git storage snapshots, host-key backup, and restore drills.
- Add quota policy, rate limiting, retention, trash collection, and audited physical deletion.
- Add optional OIDC/SSO after local identity and recovery paths are stable.
- Support permission-isolated Marketplace compositions for enterprise teams, automation agents, and task-specific CTF environments.

## Delivery rule for each slice

Every roadmap slice follows the approval, Agent ownership, TDD, and commit process in [AI / Agent development workflow](ai-development.md), plus the applicable executable gates in [Operations, testing, and delivery](operations.md). This roadmap defines sequencing and scope; it does not redefine implementation workflow or test commands.
