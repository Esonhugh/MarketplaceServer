# MarketplaceServer Roadmap

This roadmap describes future delivery order. Items listed here are planned unless explicitly identified as implemented in `project-goals.md` or verified in the current source tree.

## Near-term: complete the single-user Plugin workflow

### Repository and Plugin management

- Add tenant-scoped repository and Plugin create, read, update, and soft-delete APIs.
- Atomically create a Plugin and its one-to-one repository relationship.
- Add repository provisioning states and recoverable filesystem jobs.
- Validate `.claude-plugin/plugin.json` from a server-resolved commit.
- Add bounded repository size, request size, push concurrency, and operation quotas.
- Enforce protected refs before accepting receive-pack updates.
- Write durable push outbox events and reconcile database ref projections from Git.

### Authentication hardening

- Add login throttling and bounded concurrent Argon2 verification.
- Define API-key expiry and delegation policy, including maximum delegated lifetime.
- Add authentication and token lifecycle audit events without request-side secret mutation.
- Add operator-supported secret rotation procedures for API-key peppers and bootstrap credentials.

### Production verification

- Run all PostgreSQL suites in CI using an isolated database.
- Add production-wired distribution tests for advertisement, upload-pack, revocation, private parents, unknown services, and write-route rejection.
- Verify real Git clients against repository default-branch initialization and clone checkout behavior.
- Add versioned migration handling for any legacy repository status values.

## Next: immutable Plugin versions and Marketplace publication

### Plugin versions

- Publish versions from exact SemVer tags and full commit SHAs.
- Validate manifests and persist canonical manifest snapshots and digests.
- Protect published tags from movement or deletion.
- Record version state transitions and support auditable yank operations.
- Detect source-tag drift through reconciliation without rewriting published records.

### Marketplace authoring

- Add Marketplace template and draft item management APIs.
- Resolve draft selectors to exact Plugin versions during validation and publication.
- Build independent HTTPS and later SSH source variants when their output differs.
- Publish immutable revisions and atomically switch stable distribution pointers.
- Verify every rollback invariant before reusing a historical projection.
- Reject private publication until dedicated private distribution credentials and policy are complete.

### Distribution credentials

- Add per-user, per-Marketplace private distribution credentials.
- Store only peppered HMAC indexes and return plaintext once.
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
- Reuse the same repository resolver and authorizer as HTTPS Smart HTTP.
- Add SSH Marketplace source variants and installation guidance.
- Verify HTTPS and SSH authorization parity with real Git clients.

## Then: Svelte management frontend

- Implement login and first-run setup flows.
- Add namespace switching and user/team management.
- Add repository, Plugin, version, Marketplace, credential, and audit pages.
- Display HTTPS and SSH clone/install instructions without exposing secrets.
- Use a centralized API query/cache layer and precise mutation invalidation.
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

## Definition of done for each slice

Each feature slice must include:

1. approved contracts and security boundaries;
2. unit tests for allow and deny behavior;
3. database integration tests for constraints and tenant isolation;
4. real-client tests for Git protocol changes;
5. stable API error and request-ID behavior;
6. configuration and DI documentation updates;
7. secret and mutation-boundary review;
8. `go test ./...`, `go test -race ./...`, `go vet ./...`, `go build ./...`, and `git diff --check`;
9. frontend lint, test, and build checks whenever frontend source changes;
10. an independently reviewable feature commit.
