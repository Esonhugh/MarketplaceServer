# Plugin Lifecycle Persistence Design

- **Status:** `approved`
- **Owner:** `backend` internal Plugin lifecycle domain
- **Design approval:** 2026-08-16
- **Implementation approval:** none

## Database support and migration boundary

Production targets PostgreSQL. SQLite is supported only for single-process local development/testing. MySQL compatibility is out of scope.

Current independent-ID foundation records are not the target schema and must not receive a compatibility/dual-read layer. Implementation must fail closed on incompatible legacy schema and require an explicit development database rebuild or separately approved production migration. GORM records never serialize directly as API DTOs; DB IDs remain strings.

## Aggregate identity

```text
Plugin 1 ─── 1 hidden Repository
plugins.id == repositories.id
```

`Plugin` is aggregate root, external namespace/slug identity and policy authority. `Repository` stores only Git operational facts and cannot exist, authorize or be queried as an independent product resource.

Target constraints:

- `plugins.id`: UUID string primary key;
- `repositories.id`: same UUID, primary key and foreign key to `plugins.id` with `ON DELETE RESTRICT`;
- unique `(plugins.namespace_id, plugins.slug)`;
- immutable Plugin namespace, slug and shared ID;
- unique opaque Repository storage key;
- explicit status check constraints;
- all query/mutation predicates include resolved namespace/resource scope.

## Target records

| Record | Required facts | Rules |
|---|---|---|
| `plugins` | shared ID, namespace ID, slug, visibility, status, `archived_from`, nullable default canonical tag, timestamps | status `draft|active|archived`; visibility `public|private`; `archived_from` null except archived and is `draft|active` |
| `repositories` | shared ID, opaque storage key, operational status, timestamps | status `ready|readOnly|error`; no namespace/slug/visibility/permission columns |
| `plugin_versions` | Plugin ID, canonical tag, status, nullable current SHA/manifest facts, published/updated/deleted timestamps | unique `(plugin_id, tag)`; status `available|deleted`; deleted clears current SHA/manifest/digest |
| `plugin_version_history` | Version identity, operation, old/new SHA, intent/audit correlation, timestamp | append-only safe history |
| `repository_orphan_cleanups` | operation ID, shared UUID, storage key, state, attempts, timestamps | no path, credential or raw command output |
| `receive_intents` | Plugin ID, operation, canonical tag, expected old/proposed new SHA, expected Version state, state, attempts, safe error, timestamps | durable CAS/recovery authority for effectful protected receive |
| `projection_artifacts` | immutable artifact ID, source identity, digest, opaque storage key, build state | bytes/source immutable; staged/ready/failed only |
| `revision_projection_transitions` | intent, revision, expected/current pointer generations, staged artifact, state | unique intent+revision; pointer CAS |
| audit/outbox/GC records | action/correlation/resource facts and idempotent job state | same SQL transaction as protected SQL mutation; no secrets or paths |

## Plugin and Version lifecycle

```text
Plugin: draft --first successful publish--> active
        draft|active --archive--> archived --restore--> archived_from

Version: candidate tag --management publish--> available
         available --allowed tag delete--> deleted
         deleted --tag recreate + explicit publish--> available
```

Candidate tags are Git refs and do not require a Version row. `main`, ordinary branches and noncanonical tags have no Plugin lifecycle effect.

Tag is the complete logical Version identity. There is no hidden publication generation. A deleted tombstone retains only Plugin+tag identity, status, published/deleted timestamps and safe correlation; normal lists omit it and exact authorized reads return `410` without metadata.

`default_version_tag` points only to an available canonical tag. Tag deletion clears it synchronously. No automatic latest/older fallback exists; same-tag restoration does not restore default.

## Synchronous aggregate creation

Creation is Git-first, then one SQL transaction:

```text
acquire namespace+slug lock
→ verify absent
→ generate shared UUID
→ initialize UUID bare repository
→ set symbolic HEAD to refs/heads/main
→ begin SQL transaction
→ insert Plugin
→ insert same-ID Repository(status=ready)
→ commit
→ release lock
→ return 201
```

PostgreSQL uses a session-level advisory lock held through an exclusive connection, with bounded wait. SQLite uses a process-local keyed lock and is explicitly single-process. The database unique constraint remains final duplicate protection. A retry after a successful competing create returns `409`.

SQL failure triggers synchronous deletion of only the newly generated UUID repository. Cleanup failure writes an orphan-cleanup record and returns `500`. If the database is unavailable for both aggregate commit and cleanup recording, bytes are not deleted blindly: after recovery, a reconciler scans UUID storage entries, applies a minimum-age rule, and quarantines/cleans only identities absent from both aggregate tables.

## Receive intent and atomic boundaries

SQL transactions cover SQL only. Git refs and artifact bytes remain separate authorities coordinated through durable intents.

Effectful canonical receives use:

```text
prepared → finalizing → completed
         ↘ aborted
         ↘ manual_required
```

An intent stores expected old/new ref facts and expected SQL pointer generations. It is durable before Git accepts the protected update. Staged projection facts are complete before admission. After Git ref acceptance, finalize re-reads the actual ref and performs SQL CAS:

- actual equals proposed: finalize;
- actual equals expected old: abort and restore old stable pointer state;
- any other ref or artifact digest mismatch: `manual_required` and fail closed.

A reconciler may idempotently finalize or abort defined SQL/pointer work. It never force-updates or deletes a Git ref. System-admin retry only reruns this deterministic decision and cannot bypass CAS or validation.

## Distribution and GC

Marketplace revision Plugin+tag configuration is immutable. Projection artifacts are immutable; only ready pointers change through CAS. Pending/inconsistent pointers fail closed, and `/distribution` never builds or switches them.

Allowed published tag deletion synchronously isolates affected historical Plugin serving pointers and transitions the Version to deleted. Marketplace index/projection remains readable, while the affected Plugin distribution returns `410`. Artifact bytes are removed later by idempotent asynchronous GC. Restoring the tag always rebuilds artifacts; deleted serving state is never reused.

## Secret and audit rules

Credentials, Authorization, PATs, pack bodies, absolute paths, secret-bearing URLs and raw validator output are prohibited from persistence diagnostics, logs, audit and errors. Records use only opaque IDs, refs, full SHAs, safe state/error codes and correlation IDs.
