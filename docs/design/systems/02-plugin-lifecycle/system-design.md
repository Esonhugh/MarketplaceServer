# Plugin Lifecycle System Design

- **Status:** `approved`
- **Owner:** `backend` Plugin lifecycle domain; `git` owns Git/storage mechanics only
- **Created:** 2026-08-13
- **Last reviewed:** 2026-08-16
- **Design approval:** 2026-08-16
- **Implementation approval:** 2026-08-30，all implementation slices in this design; out-of-scope items remain excluded
- **Related:** [project goals](../../../project-goals.md), [roadmap](../../../roadmap.md), [product invariants](../../../product-invariants.md)

## 1. Outcome and scope

Namespace-authorized users synchronously create a lowercase kebab-case Plugin with optional visibility (default public). Plugin is the aggregate root and sole product identity; its hidden Repository is mandatory, shares the same UUID and is ready with symbolic `HEAD=main` before `201`.

Development can use ordinary `main`/branches without Plugin validation. Canonical `v`-prefixed SemVer tags are strict validation boundaries. Management publish selects the current canonical tag SHA as the logical Version; the first successful publish activates a draft Plugin. Marketplace revisions retain immutable Plugin+tag configuration while derived artifacts can rebuild after an allowed published-tag move.

Out of scope: independent Repository CRUD/permission/identity, physical deletion, Marketplace authoring/publication APIs, private distribution credential design, SSH, teams, audit UI, frontend implementation and cross-system ACID claims.

## 2. Aggregate and lifecycle

```text
Plugin 1 ─── 1 hidden Repository
plugins.id == repositories.id
```

Plugin stores namespace, immutable slug, visibility, product lifecycle, archived origin and default tag. Repository stores only opaque storage identity and `ready|readOnly|error` operational status.

```text
Plugin: draft --first successful Version publish--> active
        draft|active --archive--> archived --restore--> archived_from

Version: canonical candidate --publish--> available
         available --allowed tag delete--> deleted
         deleted --tag recreate + explicit publish--> available
```

Archive is idempotent and keeps metadata, Git history, authorized clone/fetch and existing distributions. It rejects all push, publish, default and visibility mutations. Restore returns to the prior draft/active state. Deleting the final available Version never changes active back to draft.

Repository status combines with Plugin lifecycle by the strictest result. `readOnly` permits read but no push. `error` fails Git and distribution closed and exposes only safe degraded metadata to authorized owner/admin.

## 3. Creation consistency

Creation uses a namespace+slug lock, UUID repository initialization, then one SQL transaction inserting both shared-ID rows. PostgreSQL uses bounded session advisory locking; SQLite is single-process and uses a process-local keyed lock. Duplicate retry returns `409`.

SQL failure synchronously removes only the newly generated UUID repository. Failed compensation records an orphan cleanup job. If the database is unavailable, recovery later scans minimum-age UUID storage entries and quarantines/cleans only entries absent from both aggregate tables. No marker inside the bare repository is required.

## 4. Management contract

The implemented wire is [management-v1.yaml](../../../../api/openapi/management-v1.yaml); rationale is [management/api-contract.md](management/api-contract.md). The earlier proposal remains in [management-v1-design.yaml](../../../../api/openapi/management-v1-design.yaml) as design history; runtime conformance promoted the Plugin routes to the deployed contract.

Commands:

```text
POST /api/v1/namespaces/{namespace}/plugins
GET  /api/v1/namespaces/{namespace}/plugins
GET  /api/v1/namespaces/{namespace}/plugins/{plugin}
POST /api/v1/namespaces/{namespace}/plugins/{plugin}:archive
POST /api/v1/namespaces/{namespace}/plugins/{plugin}:restore
POST /api/v1/namespaces/{namespace}/plugins/{plugin}:set-visibility
GET  /api/v1/namespaces/{namespace}/plugins/{plugin}/versions
GET  /api/v1/namespaces/{namespace}/plugins/{plugin}/versions/{tag}
POST /api/v1/namespaces/{namespace}/plugins/{plugin}/versions:publish
POST /api/v1/namespaces/{namespace}/plugins/{plugin}/versions/{tag}:set-default
DELETE /api/v1/namespaces/{namespace}/plugins/{plugin}/default-version
```

Plugin DTO exposes namespace/name/status/visibility/repositoryStatus/relative cloneUrl/defaultVersion/timestamps only. Version DTO exposes tag/status/current SHA/timestamps only. Tag is Version identity; no duplicate semantic version or publication generation is exposed.

## 5. Visibility, default and Marketplace behavior

Anonymous exact reads are allowed only for public active/archived Plugin and available Version. Draft is never anonymous. Private, unknown and undisclosed resources use generic `404`.

Changing public to private changes current `plugin.read` policy but does not rewrite historical Marketplace index configuration. Index visibility is a locator, not a content credential. Private content still requires current Git/subscription PAT authorization; management JWT does not authenticate distribution.

Any current `plugin.write` principal receives development Git source in dynamic user Marketplace. Read-only principals receive only the owner-selected default Version and the Plugin is omitted when no valid default exists. There is no automatic latest/older fallback.

Publish may set default with `makeDefault`. Owners may explicitly set or clear it. Deleting a default published tag clears it; recreating the same tag does not restore default.

## 6. Git and protected receive

`main`, ordinary branches and noncanonical tags are ordinary development refs: no Plugin validator and no lifecycle effect.

Canonical tag create/move validates with native MarketplaceServer Plugin Profile v1 (specified in [Git contract](git/api-contract.md#marketplaceserver-plugin-profile-v1--normative-source-validation)), exact case-sensitive manifest name matching, and annotated-tag peeling to a full commit. Candidate tags do not publish. Management publish repeats strict validation and CAS-binds the current full SHA.

Available tag moves prebuild every Marketplace revision selecting Plugin+tag. Available tag deletion is blocked by any active Marketplace revision reference. Historical-only references allow deletion: Version becomes deleted, default clears, historical Plugin distribution returns `410`, index remains readable and bytes enter async GC. Candidate deletion is directly allowed and creates no tombstone.

Any protected-ref failure rejects the complete multi-ref push.

## 7. Receive consistency and recovery

Detailed protocol is [git/api-contract.md](git/api-contract.md). Effectful receives use durable intent, quarantine, immutable staged artifacts, expected-state CAS and a Plugin-scoped lock.

```text
durable prepare
→ whole-ref-set admission
→ Git ref acceptance
→ re-read actual ref
→ SQL/pointer CAS finalize
```

The accepted-ref/unfinished-finalize crash window is explicit. Affected stable distribution reads fail closed. Reconciliation finalizes when actual ref equals proposed, aborts when it equals expected old, and marks unexpected/corrupt states `manual_required`. It never force-moves Git refs. Audited system-admin retry only reruns deterministic reconciliation and cannot bypass validation or CAS.

## 8. Authorization

Actions are fixed:

```text
plugin.create
plugin.list
plugin.read
plugin.write
plugin.archive
plugin.publish
```

- create/list evaluate a resolved namespace;
- all other actions evaluate tenant-qualified Plugin;
- management uses Bearer JWT;
- Git uses its dedicated PAT plane and preset intersection;
- private HTTP distribution uses subscription PAT plane;
- invalid supplied credential never becomes anonymous;
- every plane performs final server-side `Principal + Action + Resource + Context` authorization.

## 9. Persistence and module boundaries

Target records and constraints are in [persistence-design.md](persistence-design.md). Current independent-ID foundation must not be hidden behind compatibility shims; implementation needs an explicitly approved migration/rebuild strategy. PostgreSQL is production; SQLite is single-process dev/test; MySQL is unsupported.

Backend owns SQL policy/state/orchestration. Git owns filesystem, refs, quarantine, subprocess and artifact mechanics. Distribution receives read-only capability only. Cross-module contracts expose opaque IDs and immutable value facts, never GORM records, handlers or paths.

## 10. TDD and implementation slices

| Slice | First required tests |
|---|---|
| shared-ID persistence/provisioning | constraints, duplicate lock, Git init/HEAD, SQL compensation, orphan recovery, PostgreSQL and SQLite single-process |
| lifecycle/management | create/list/get, visibility privacy, archive/restore, DTO secrecy, default commands, `410` tombstone behavior |
| Git authorization/admission | real clone/fetch/push; PAT preset denies; archive/readOnly/error denies before subprocess; tag strict validation; whole-push rejection |
| Version/publication | first publish activation, CAS SHA, default changes, delete/restore same logical tag, active-reference delete deny |
| intent/projection recovery | crash points, staged digest, accepted/unaccepted/ambiguous ref reconciliation, fail-closed reads, GC idempotency, race tests |
| integration/docs | DI boundary, deployed OpenAPI conformance, current-state and operations evidence |

Agents may read shared files but implementation owners must have exclusive write scope. Shared DI/OpenAPI/current-state changes are serialized by the integration owner. The 2026-08-30 approval covers every slice in this design. Each slice still requires TDD, focused and full applicable checks, and a cohesive new commit only when explicitly requested.

Required final evidence includes `git diff --check`, focused tests, `go test ./...`, `go test -race ./...`, `go vet ./...`, `go build ./...`, real Git client coverage and PostgreSQL tests. Any skipped PostgreSQL, validator, Git, frontend or SSH coverage must be reported.

## 11. Approval record

The design decisions were reviewed interactively and the user instructed that they be fixed in documentation and committed on 2026-08-16. That approval covered the design only.

On 2026-08-30 the user explicitly requested complete implementation of the approved design. This authorizes the shared-ID persistence/provisioning, lifecycle/management, Git authorization/admission, Version/publication, intent/projection recovery, integration and documentation slices. The out-of-scope items in section 1 remain excluded, including frontend implementation and production migration of valuable legacy data. Runtime implementation is claimed only after its code and required conformance evidence exist.
