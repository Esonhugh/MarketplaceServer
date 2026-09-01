# MarketplaceServer Roadmap

This roadmap is the authoritative future delivery order. Items listed here are planned unless [current-state.md](current-state.md) explicitly classifies them as implemented or foundation capability; source code and tests remain authoritative.

## Near-term: production hardening

Plugin management、shared-ID hidden Repository、Git-first provisioning/补偿、protected canonical-tag receive、Version lifecycle、durable receive coordination 与 projection pointer/artifact coordination 已实现，见 [current-state.md](current-state.md)。后续只保留运维强化：

- Add bounded repository size, request size, push concurrency, and operation quotas beyond the current HTTP limits.
- Schedule the existing receive, orphan-cleanup, and projection-GC reconciliation entry points with durable worker leases, bounded retries, and operational visibility.
- Add audited physical deletion and retention only after the archive/tombstone lifecycle has proven stable.

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

## Next: Marketplace publication

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

## Then: audit and identity extensions

Team namespaces, fixed owner/admin/maintainer/developer/viewer roles, memberships, existing-user invitations, system-administrator user lifecycle, optional registration, and immediate policy reevaluation on departure/disable are implemented; see [current-state.md](current-state.md). Remaining work:

- Keep system administration distinct from team ownership and add durable cross-tenant administration audit evidence.
- Add service accounts and separately scoped automation credentials.
- Add append-only audit storage and tenant-scoped audit query APIs.
- Add transactional outbox events for high-risk control-plane mutations.

## Then: SSH Git and transport parity

- Add a dedicated SSH listener with persisted host keys.
- Authenticate users and service accounts by active SSH public-key fingerprints.
- Accept only strict `git-upload-pack` and `git-receive-pack` exec requests.
- Reject shell, PTY, forwarding, arbitrary environment, and unknown commands.
- Reuse the same Plugin-backed repository resolver and read/write authorization as HTTPS Smart HTTP.
- Add SSH Marketplace source variants and installation guidance.
- Verify HTTPS and SSH authorization parity with real Git clients.

## Then: complete the Svelte management frontend

Login、registration、当前用户 PAT、管理员用户、Team/invitation 与 Plugin management 页面已实现。Plugin 项目页已提供 HTTPS clone URL、版本/default/visibility/archive 操作，以及 branch/tag tree、受限 text blob 和 commit history 浏览；hidden Repository 仍不暴露独立产品 identity。first-run setup 和其余管理流程仍未交付。

- Implement first-run setup flow.
- Add generic namespace switching beyond the implemented personal/Team Plugin selector.
- Add Marketplace, credential, and audit pages.
- Extend repository browsing with commit detail/diff and syntax-aware rendering without exposing a separate Repository CRUD model.
- Display future SSH clone/install instructions without exposing secrets.
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
