# Plugin Lifecycle Git Contract

- **Status:** `approved`
- **Owner:** `git` transport/storage with `backend` Plugin policy ownership
- **Design approval:** 2026-08-16
- **Implementation approval:** none

## Resource and credential boundary

Development URL is `/git/{namespace}/{plugin}.git`. Backend resolves namespace+Plugin slug and gives `git` an opaque shared aggregate identity; no GORM record, DAO, concrete service, storage key or path crosses the module contract.

| Git operation | Credential | Final action |
|---|---|---|
| advertise/upload-pack, clone, fetch | anonymous only for public active/archived, otherwise Git Basic username + allowed PAT | `plugin.read` |
| receive-pack, push | Git Basic username + `git-write` PAT | `plugin.write` |

JWT, account password, distribution credential and request-header hints cannot authenticate Git. Invalid supplied credentials never downgrade to anonymous. Resolution, authorization and lifecycle checks occur before starting a Git subprocess.

Draft clone/fetch is available to current authorized `plugin.read` principals but never anonymously. Archived permits authorized read and rejects all push. Repository `readOnly` permits read and rejects push; `error` rejects clone/fetch/push and distribution.

## Ref admission

Git objects remain in quarantine while the complete proposed ref command set is inspected.

| Ref operation | Admission/effect |
|---|---|
| `main`, ordinary branches, noncanonical tags | no Plugin validator and no lifecycle effect |
| candidate canonical tag create/move | strict `claude plugin validate`; warnings and errors reject; exact case-sensitive manifest name; annotated tag peels to commit |
| candidate canonical tag delete | directly allowed under `plugin.write`; no Version tombstone/default/Marketplace work |
| available tag move | strict validation/name check and prebuild every referencing Marketplace revision |
| available tag delete | no validator; reject if any active Marketplace revision references Plugin+tag; otherwise prepare Version deletion/default clear/pointer isolation |

Any protected-ref authorization, validation, reference, prebuild or CAS failure rejects the entire multi-ref push, including unrelated ordinary ref updates. Canonical parsing is strict `v`-prefixed SemVer and does not reuse a broad slug/ref regexp.

Candidate creation does not publish. Management publish re-runs strict validation/name matching and CAS-binds the current full SHA. The first successful publish activates a draft Plugin.

## Durable receive protocol

Only receives that mutate an available Version, default tag or projection pointer create durable receive intents. Candidate-only operations do not create inert business state.

### 1. Preflight

```text
resolve aggregate
→ authenticate Git PAT
→ authorize plugin.write
→ reject archived/readOnly/error
→ acquire Plugin effect lock when needed
→ inspect complete ref set in quarantine
```

PostgreSQL uses a session-level Plugin advisory lock without a long SQL transaction. SQLite uses a process-local keyed lock and is single-process only. Expected ref/SHA and SQL pointer generations remain mandatory CAS facts.

### 2. Prepare

For an available tag move:

1. peel and validate proposed commit in quarantine;
2. require exact manifest Plugin name;
3. find every Marketplace revision selecting Plugin+tag;
4. build and digest immutable staged artifacts;
5. persist intent, artifact facts, pointer transitions and safe audit/outbox;
6. mark affected serving transitions pending/fail-closed;
7. return pre-receive success only when every requirement is ready.

For an available tag delete:

1. verify no active Marketplace revision reference;
2. prepare Version deleted/default clear/history pointer isolation/GC facts;
3. durably persist intent before allowing ref deletion.

Intent safe facts include intent/Plugin IDs, operation, canonical tag, expected old SHA, proposed new SHA or deletion, expected Version state, affected revision/artifact IDs, state, attempts, safe error and timestamps. It excludes credentials, pack body, path and raw validator output.

### 3. Accept and finalize

```text
durable prepare
→ pre-receive succeeds
→ Git atomically updates this push's refs
→ re-read actual ref
→ SQL CAS finalize
```

- actual ref equals proposed SHA/deletion: finalize Version/default/pointers;
- actual ref equals expected old SHA: mark `aborted`, restore previous stable SQL/pointer state and clean staged artifacts;
- actual ref is neither: mark `manual_required`, retain fail-closed state.

Successful tag move updates the same available logical Version and switches verified staged artifacts to ready pointers; old artifacts enter asynchronous GC. Successful tag deletion changes the same Version to deleted, clears current content facts and any matching default, isolates historical Plugin distribution pointers and schedules GC.

## Crash and response semantics

The unavoidable window is Git refs accepted while SQL finalize is unfinished. The server does not claim cross-system atomicity or silently move the ref back.

A finalize failure may cause the Git client to receive failure even though the ref was accepted. The intent and expected state make retry idempotent. Affected distribution reads fail closed; development clone/fetch follows actual Git refs. New pushes touching the unresolved canonical tag are rejected. Unrelated ordinary branch operations may proceed only when they do not share the unresolved protected transition.

## Reconciliation

The automatic reconciler obtains the same Plugin lock, reads the actual ref, verifies staged artifact digest, and then:

| Observed fact | Action |
|---|---|
| ref equals proposed | idempotent finalize |
| ref equals expected old | abort and restore old stable SQL/pointers |
| same intent already finalized | mark completed |
| unexpected ref, missing/corrupt artifact, unknown CAS change | `manual_required`, alert and remain fail closed |

The reconciler never force-updates, deletes or rolls back a Git ref. Only system-admin may trigger an audited retry by intent ID; retry only repeats the deterministic finalize/abort check and cannot ignore CAS, select an arbitrary SHA or bypass validation. No owner recovery endpoint is part of proposed Management API.

## Marketplace and deletion semantics

Active Marketplace revision references prohibit published tag deletion and reject the entire push. Historical non-active references do not block deletion: immutable Marketplace configuration and index/projection remain readable, but the affected Plugin distribution returns `410`. Recreating the tag yields a strict-valid candidate; explicit management publish restores the same logical Version and rebuilds historical artifacts. Default does not automatically restore.

Distribution handlers remain absolutely read-only and may not initialize repositories, invoke receive-pack, validate, build, update refs or switch pointers.

## Process and secrecy rules

Git subprocesses use `exec.CommandContext`, fixed arguments, minimal environment, timeout and storage-root containment. Protocol/log/audit/error output must omit Authorization, PAT, pack body, absolute repository/quarantine path, sensitive command output and raw validator output. Real-client tests are mandatory for allow/deny and whole-push rejection behavior.
