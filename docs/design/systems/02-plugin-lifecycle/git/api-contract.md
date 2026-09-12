# Plugin Lifecycle Git Contract

- **Status:** `approved`
- **Owner:** `git` transport/storage with `backend` Plugin policy ownership
- **Design approval:** 2026-08-16
- **Implementation approval:** 2026-08-30，development Git authorization, protected receive, durable intent and reconciliation

## Implementation execution decisions

The approved runtime keeps the current direct `git receive-pack --stateless-rpc` transport. Protected receives use server-owned `pre-receive` plus `proc-receive`; all ref commands are committed through one expected-old `git update-ref --stdin` transaction so whole-push rejection does not depend on client `--atomic` support. Runtime startup fails closed when the installed Git lacks the required behavior.

A receive is represented by one durable batch plus its effectful canonical-tag transitions. Every canonical-tag mutation acquires the Plugin effect lock before classification to close the candidate/publish race. Raw tag ref object IDs and peeled commit IDs are stored separately; CAS uses raw ref IDs while validation, Version `commitSha` and projection use peeled commit IDs. Object IDs follow the repository object format rather than a hard-coded SHA-1 length.

Legacy independent-ID data uses the approved rebuild-only boundary: startup fails closed and does not dual-read or destructively migrate valuable data. PostgreSQL is production, SQLite is single-process development/testing, and MySQL is unsupported. Worker retry/lease defaults are operator configuration and may be tuned without changing the receive state machine.

## Resource and credential boundary

Development URL is `/git/{namespace}/{plugin}.git`. Backend resolves namespace+Plugin slug and gives `git` an opaque shared aggregate identity; no GORM record, DAO, concrete service, storage key or path crosses the module contract.

| Git operation | Credential | Final action |
|---|---|---|
| advertise/upload-pack, clone, fetch | anonymous only for public active/archived, otherwise Git Basic username + allowed PAT | `plugin.read` |
| receive-pack, push | Git Basic username + `git-write` PAT | `plugin.write` |

JWT, account password, distribution credential and request-header hints cannot authenticate Git. Invalid supplied credentials never downgrade to anonymous. Resolution, authorization and lifecycle checks occur before starting a Git subprocess.

Draft clone/fetch is available to current authorized `plugin.read` principals but never anonymously. Archived permits authorized read and rejects all push. Repository `readOnly` permits read and rejects push; `error` rejects clone/fetch/push and distribution.

## Ref admission

Inspection uses the current receive's object environment so newly uploaded objects are visible when resolving and materializing proposed commits. Whole-push rejection guarantees unchanged refs, not immediate object removal: Git may already have promoted incoming objects before `proc-receive` rejects the commands, leaving unreachable objects for normal Git maintenance.

| Ref operation | Admission/effect |
|---|---|
| `main`, ordinary branches, noncanonical tags | no Plugin validator and no lifecycle effect |
| candidate canonical tag create/move | native MarketplaceServer Plugin Profile v1 validation; exact case-sensitive manifest name; annotated tag peels to commit |
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

## MarketplaceServer Plugin Profile v1 — normative source validation

Profile v1 is a versioned, native `mod/git` product admission policy, not a copy of Claude Code's internal validator. It never invokes Claude CLI or executes Plugin content. Unknown fields and unsupported components reject; there is no warning-only admission. Canonical receive and management inspection use the same profile. The five runtime modules and `PluginSourceInspector` contract are unchanged. This unreleased product has no legacy-validator compatibility mode or binary setting.

References for the supported subset: [official Plugin reference](https://code.claude.com/docs/en/plugins-reference), [official skills documentation](https://code.claude.com/docs/en/skills), and [Agent Skills format](https://agentskills.io/specification). Upstream additions do not silently expand this fixed profile; extensions require an explicit profile revision and tests.

### Manifest

`.claude-plugin/plugin.json` is required, regular UTF-8 JSON, a single object with exact case-sensitive keys and no duplicate fields. `name` is required, must match the expected Plugin slug exactly, and uses the existing lowercase kebab-case slug policy. Only these fields are admitted:

| Fields | Type / rule |
|---|---|
| `name` | required string, exact Plugin slug |
| `displayName`, `description`, `homepage`, `repository`, `license` | string |
| `version` | strict SemVer string without `v` prefix; metadata only, never Version identity |
| `author` | object with only optional string `name`, `email`, `url` |
| `keywords` | array of strings |
| `metadata` | JSON object, opaque data (not executable component configuration) |
| `defaultEnabled` | boolean |
| `skills` | string or array of strings, component collection paths below |

Official documentation cross-check (2026-09-12): `displayName`, `metadata`, and `defaultEnabled` are documented manifest fields, not marketplace-only extensions. Upstream also accepts `$schema`, additional executable component/configuration fields (`experimental`, `userConfig`, `channels`, `dependencies`, etc.), trailing-slash paths and single-skill directory packaging; these are deliberately outside this fixed profile. Upstream's author description expects `name`; this profile admits an empty author object as descriptive metadata. This table, not the evolving upstream validator, is the admission authority.

Present fields cannot be null. URL/contact/license fields are descriptive strings, not fetched or interpreted by the server. Minimal manifest-only Plugins are allowed; a skill is not mandatory.

### Skills-only component boundary

Default `./skills` is always scanned if present. Manifest `skills` adds collection directories, not replacements for the default. Every configured path must start `./`, contain no empty, dot, parent or backslash segment, remain within Plugin root and exist as a directory. Repeated identical collection paths scan once. Symlinks are not accepted as collection directories, skill directories or SKILL.md files.

Every direct collection entry must be a directory with a regular `SKILL.md`; auxiliary resources may live within that skill directory and are not executed. All discovered effective skill names are unique across default and custom collections. Nested collections are not recursively discovered. A single repository-root or collection-root `SKILL.md` is rejected: standalone single-skill packaging is not this product's mandatory-manifest Plugin shape.

The conventional root entries `commands`, `agents`, `workflows`, `hooks`, `.mcp.json`, `.lsp.json`, `output-styles`, `themes`, `monitors`, `bin`, `settings`, `settings.json` are unsupported even when empty. Corresponding manifest configuration is not in the allowlist and rejects. Skill frontmatter `hooks` and any unknown execution configuration also reject. This is format admission, not a sandbox or a claim that accepted skill instructions/resources are safe to execute on clients.

### SKILL.md frontmatter

The entire file must be UTF-8 without NUL. The first line is exactly `---` (LF or CRLF); a later exact `---` closes frontmatter. Frontmatter is exactly one YAML mapping parsed by `gopkg.in/yaml.v3`. Duplicate/unknown keys, aliases used as field values, merge keys, non-string keys and incorrect scalar tags reject. YAML parse diagnostics and source content never escape the validator.

| Fields | Type / rule |
|---|---|
| `name` | optional string; fallback to directory basename; lowercase kebab-case, 1–64 bytes, globally unique within Plugin |
| `description` | required nonblank string; product requirement for usable discovery, deliberately stricter than optional upstream fallback |
| `argument-hint`, `model`, `agent`, `license`, `compatibility` | string |
| `context` | string `fork` only |
| `disable-model-invocation`, `user-invocable` | YAML boolean |
| `allowed-tools` | string or sequence of strings; not executed or resolved server-side |
| `metadata` | mapping of unique string keys to string values |

Markdown body is preserved and is not interpreted. No shell, tool, hook, model or agent is invoked during validation.

### Limits, diagnostics and persistence

Existing materialization limits remain 10,000 Git blob entries, 16 MiB per file, 256 MiB total source and 1 MiB manifest. All component input is drawn from that bounded materialization. Gitlinks, escaping paths/symlinks and symlink traversal during materialization reject. Auxiliary symlinks must resolve within the completed materialized tree; dangling links, cycles and chained escapes reject. They are not component entry points. Tree listing output is bounded to 16 MiB before parsing; JSON nesting is limited to 64 levels and duplicate keys reject at every depth, including opaque metadata. YAML frontmatter is bounded to 64 KiB (after CRLF normalization); Markdown body remains subject to the file limit. These parser limits prevent bounded file bytes from producing disproportionate parser allocations.

Internal typed diagnostics contain only stable category codes; their public error text and unwrap target are `ErrPluginSourceInvalid` (`plugin source validation failed`). No server path, source text, parser diagnostic or subprocess output is returned or logged. Context cancellation remains cancellation. The manifest snapshot is still the exact original bytes, and its digest remains SHA-256 of those bytes, independent of path/environment/profile metadata; raw tag and peeled commit identities are retained unchanged.
