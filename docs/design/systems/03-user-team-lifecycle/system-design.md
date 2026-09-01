# User and Team Lifecycle System Design

- **Status:** `implemented`
- **Owner:** `backend/domain/identity` and `backend/domain/authorization`; existing static `frontend` module
- **Created:** 2026-08-31
- **Last reviewed:** 2026-09-01
- **Approval record:** user approved design and implementation on 2026-09-01; backend lifecycle is implemented
- **Related goals/roadmap:** [Project goals](../../../project-goals.md), [teams/RBAC roadmap](../../../roadmap.md#then-teams-rbac-and-audit)

The approved backend lifecycle, migrations, routes, authorization integration, and deployed OpenAPI promotion are implemented. This document retains design rationale; runtime routes and wire schemas are authoritative in `api/openapi/management-v1.yaml`.

## 1. User outcome, scope, and baseline

### Outcome

- An operator may enable public account registration. Registration creates an active user and the same-slug personal namespace atomically, then returns a management JWT so the user can enter the application directly.
- A system administrator can list, inspect, create, update profile fields, enable, and disable users.
- An active user can create Teams, list Teams they belong to, inspect a Team, directly add existing users, or send an existing user a seven-day in-product invitation according to the fixed role matrix.
- Existing Plugin authorization recognizes Team membership so Team resources use the same namespace-qualified Plugin/Git paths as personal resources.
- The embedded Svelte frontend gains registration, navigation, user administration, Team list/create/detail, and member management without SSR or a global cache framework.

### Baseline

The runtime has `User`, personal/Team-capable `Namespace`, fixed system groups, Team membership/invitation records, Argon2id password hashing, stateless 30-day JWT, PATs, registration/user-management routes, and Team-aware policy. The frontend delivery status is tracked by `current-state.md`.

### In scope

1. Configurable public registration, default **disabled**.
2. Atomic User + personal Namespace creation, shared by registration and administrator creation.
3. System-administrator user list/get/create/profile-update/status and system-admin grant/revoke commands.
4. Team creation and direct membership with fixed roles `owner`, `admin`, `maintainer`, `developer`, `viewer`.
5. Team list/get/update and member list/add/role-change/remove.
6. Existing-user, username-targeted, in-product Team invitations with accept/reject/revoke/reissue and seven-day expiry.
7. Team-aware Plugin list/read/write/archive/publish and Marketplace read authorization.
8. Frontend pages for these operations.

### Explicitly deferred

- Email or unregistered-user invitations, email ownership/verification, password reset/change, administrator password reset, account deletion, username/email mutation.
- Team deletion/archive, slug rename, ownership transfer, nested groups, custom roles, resource-specific grants.
- Audit persistence/query API and transactional outbox. Per explicit approval, this slice emits structured security logs for cross-tenant user/admin and Team membership/invitation mutations, but does not claim those logs are durable append-only audit. This accepted boundary remains below the target audit invariant until the audit subsystem is implemented.
- Service accounts, SSH keys, OIDC/SSO, Marketplace authoring, and private distribution credentials.

### Key choices and trade-offs

1. **Team identity is the Team namespace.** Do not add a duplicate `teams` table containing the same ID/slug/display name. `TeamMembership(namespace_id,user_id,role)` supplies the missing relationship. This keeps existing resource `namespace_id` and URL identity authoritative.
2. **Registration is operator-controlled and disabled by default.** Unconditional public signup would materially change deployment exposure. A backend config boolean is simple and visible; disabled registration returns `404` so it is absent from the public capability surface.
3. **Administrator creation and public registration share one creation service.** They differ only in caller, initial state, and response. This avoids two implementations of the User + Namespace invariant.
4. **No ownership transfer in the first slice.** A Team may have multiple owners. Removing/demoting an owner is allowed only when another owner remains. This yields recoverable administration without inventing a singular owner pointer or a partially specified transfer protocol.
5. **System-admin grant/revoke is explicit and protected.** Only current active system administrators may change another user's fixed admin-group membership. Self-grant/revoke is rejected, and at least one active system administrator must remain.
6. **Direct membership plus existing-user invitations.** Invitations target a canonical existing username, appear in-product, expire after seven days, and support accept/reject/revoke/reissue. No bearer invitation token or email delivery exists.

## 2. Data, ownership, and lifecycle

Detailed schema is in [persistence-design.md](persistence-design.md).

### User lifecycle

```text
(nonexistent) --register/admin create--> active
(nonexistent) --admin create disabled--> disabled
disabled --admin enable---------------> active
active   --admin disable---------------> disabled
```

- `username`, normalized `email`, personal Namespace kind/slug/owner, Team namespace kind/slug, and membership identity are immutable.
- `displayName` and user status are mutable. Email stays immutable in this slice because current DB guards already make it an identity field.
- Disabling immediately blocks new password login and PAT authentication. An already-issued stateless JWT remains cryptographically valid, but every protected resource policy reads current user state and denies it.
- Disabling does not delete PATs, memberships, namespaces, Plugins, or history. Re-enabling restores policy eligibility; explicitly revoked PATs remain revoked.

### Team lifecycle

```text
(nonexistent) --create--> active Team namespace + creator owner membership
active --update display name--> active
```

There is no Team delete/archive state in this slice. The Team namespace remains a stable tenant identity.

### Membership and invitation commands

| Command | Preconditions | Authorization | Result | Failure |
|---|---|---|---|---|
| Create Team | active user; globally unique canonical slug | `team.create` / proposed namespace identity | Team namespace and creator `owner` membership in one SQL transaction | duplicate `409`; invalid `422` |
| Add member | existing active user; not already a member | `team.members.manage` / Team namespace | direct membership created | hidden user `404`; duplicate `409` |
| Invite member | existing active user; not member; no live same-Team invitation | `team.invitations.manage` / Team namespace | pending invitation expiring after seven days | duplicate/live invite `409` |
| Accept invitation | pending, unexpired invitation belongs to actor | `team.invitation.respond` / exact invitation | membership created and invitation accepted atomically | stale/revoked `409`; hidden `404` |
| Reject invitation | pending, unexpired invitation belongs to actor | `team.invitation.respond` / exact invitation | invitation rejected | stale/revoked `409`; hidden `404` |
| Revoke invitation | pending invitation | `team.invitations.manage` / Team namespace | invitation revoked | already terminal is idempotent `204` |
| Reissue invitation | prior invitation exists; target still active/nonmember | `team.invitations.manage` / Team namespace | old pending revoked and fresh seven-day invitation created | member/conflict `409` |
| Change role | membership exists; valid fixed role | `team.members.manage`; `owner` role changes require owner | role updated | last-owner guard `409` |
| Remove member | membership exists | `team.members.manage`; removing owner requires owner | membership removed | last-owner guard `409` |
| Update Team | nonempty display name | `team.settings.write` / Team namespace | display name updated | wrong tenant/role `403` |

A caller cannot remove or demote the final owner. An owner may leave only if another owner remains. Team admins may manage `admin|maintainer|developer|viewer` memberships and invitations for those roles, but may neither directly add/invite an owner nor demote/remove an owner. System administrators use explicit system-admin authority and remain distinct from Team ownership; they do not receive an implicit Team membership row.

Invitation state is derived from terminal timestamps and expiry:

```text
pending --accept--> accepted
pending --reject--> rejected
pending --revoke/reissue--> revoked
pending --expires_at reached--> expired (derived)
```

There is no bearer invitation secret: the authenticated target user discovers invitations through `/api/v1/me/team-invitations`; exact target `user_id` and current JWT principal must match.

### System-administrator membership commands

Only an active system administrator may grant or revoke another user's membership in the fixed `admin` system group. Self-grant/revoke is rejected. Grant is idempotent. Revoke locks/serializes the active-admin set and rejects removal of the final active administrator. Disabled administrators retain the membership row but cannot authorize; enabling them restores it.

## 3. API, authorization, and frontend

Exact deployed wire schemas and examples are authoritative in [`api/openapi/management-v1.yaml`](../../../../api/openapi/management-v1.yaml). [Management API rationale](management/api-contract.md) records the operation rationale and stable semantics; `management-v1-design.yaml` retains future proposed slices.

### New actions/resources

Actions:

- `user.list`, `user.read`, `user.create`, `user.update`, `user.disable`, `user.enable`, `user.admin.grant`, `user.admin.revoke`
- `team.create`, `team.list`, `team.read`, `team.settings.write`, `team.members.read`, `team.members.manage`, `team.invitations.read`, `team.invitations.manage`, `team.invitation.respond`

Resources:

- existing `user` for exact users;
- `user_collection` for list/create;
- existing `namespace` for Team identity and Team settings;
- `team_membership` for an exact `(namespaceID,userID)` membership locator;
- `team_invitation` for an opaque invitation ID with Team namespace and exact invited user facts.

Registration is an unauthenticated capability, not an authorization action. It is gated by operator config and validates only creation input.

### Fixed Team role matrix

| Action | owner | admin | maintainer | developer | viewer |
|---|---:|---:|---:|---:|---:|
| `team.read`, `team.list`, `team.members.read` | yes | yes | yes | yes | yes |
| `team.settings.write` | yes | yes | no | no | no |
| `team.members.manage` non-owner roles | yes | yes | no | no | no |
| `team.invitations.manage` non-owner roles | yes | yes | no | no | no |
| create/invite/demote/remove owner membership | yes | no | no | no | no |
| `plugin.list`, `plugin.read`, `marketplace.read` | yes | yes | yes | yes | yes |
| `plugin.create`, `plugin.write` | yes | yes | yes | yes | no |
| `plugin.publish`, `plugin.archive` | yes | yes | yes | no | no |

System administrators may perform all listed user and Team control-plane actions, and all Plugin actions, through explicit policy checks. Team role is resolved from current DB membership on every protected request. A disabled or departed user is denied immediately by policy.

`team.create` and `team.list` are user-level operations: any active JWT user may create a Team and list only Teams where they have direct membership; system administrators may list all Teams only through `GET /api/v1/teams?scope=all` to make cross-tenant behavior explicit.

### Frontend structure

The current monolithic `App.svelte` should be split only as needed for this delivered page set:

- `/login` — existing login.
- `/register` — shown only when registration-capability discovery reports enabled; successful registration saves returned JWT.
- `/tokens` — existing PAT page.
- `/teams` — current user's Team list and create form.
- `/invitations` — current user's pending Team invitations with accept/reject controls.
- `/teams/:slug` — Team profile, member list, and invitation management; mutation controls depend on server-provided `permissions` booleans, never inferred from role strings alone.
- `/admin/users` — system administrator user page with list/create/status/profile and grant/revoke system-admin controls.

Use a small browser-history router owned by the frontend, handwritten API methods, local component state, abortable requests, and explicit refetch after mutation. Do not add a global query/cache or state framework. Navigation eligibility comes from a new authenticated `GET /api/v1/me` result containing current user metadata and coarse UI capabilities (`systemAdmin`, `registrationEnabled` is public capability data); backend policy remains authoritative.

Required UI states: loading, empty, validation, unauthenticated, forbidden, not found, conflict, retry, pending mutation, accessible confirmation for disable/remove/demote, focus return, semantic labels, and keyboard navigation.

## 4. Boundaries and consistency

- All data changes are SQL-only. There is no Git/filesystem transaction in account/Team commands.
- User creation transaction: hash before transaction; within one short transaction create User and personal Namespace. On any write failure neither remains.
- Team creation transaction: create Team Namespace and creator owner membership atomically.
- Last-owner validation and mutation occur in one transaction. PostgreSQL locks Team membership rows (`FOR UPDATE`). SQLite relies on its write transaction boundary for supported single-process development/tests.
- Authorization reads Team membership and user status from DB for each request. No role/membership is embedded into JWT or PAT.
- Team membership changes affect existing Plugin/Git authorization without rewriting Plugin records.
- No cross-module model/DAO exposure is introduced. Identity and authorization remain backend-internal; frontend uses HTTP DTOs; git continues consuming only `auth.Authorizer` and repository DTOs.
- Password, password hash, JWT, PAT, Authorization, request body, and email must not enter logs or errors. Per approved scope, structured security logs record timestamp, request ID when available, actor user ID, action, target type/opaque ID, Team namespace ID when relevant, outcome, and safe reason code. They are operational evidence, not a durable audit ledger.

## 5. TDD acceptance matrix

| Risk | First failing test | Layer | Allow | Deny/failure |
|---|---|---|---|---|
| Atomic registration | creation service transaction test | SQLite/PostgreSQL DB | User and same-slug namespace commit | duplicate/namespace trigger failure leaves neither |
| Registration gate | handler/config test | HTTP | enabled creates and returns JWT | disabled `404`; malformed `400`; invalid `422` |
| Password handling | service test | unit | bounded valid password hashes with current Argon2id | empty, API-key-prefixed, overlong denied; never echoed |
| Admin users | service/handler tests | unit/HTTP | admin list/create/update/status | ordinary user `403`; unknown `404`; self-disable or final active-admin disable `409` |
| Admin membership | serialized service/DB tests | DB/HTTP | current admin grants/revokes another user | ordinary/self denied; final active admin retained under concurrency |
| Disabled semantics | auth/policy tests | unit/HTTP/Git | active works; re-enable restores eligible policy | new login/PAT denied; existing JWT resource action denied |
| Team creation | repository/service tests | DB | namespace + owner membership commit | duplicate slug and partial write rollback |
| Membership role guard | transaction tests | DB | owner/admin permitted matrix | admin cannot alter owner; last owner cannot leave/demote |
| Team invitations | repository/service/HTTP tests | DB/HTTP | target accepts; inviter revokes/reissues | wrong target hidden; expired/revoked/member conflict; concurrent accept once |
| Structured security logs | log observer tests | unit/HTTP | approved mutation emits safe IDs/action/outcome | no password, token, Authorization, email, body, or hash |
| Tenant isolation | repository/policy tests | DB/unit | member accesses own Team | nonmember/wrong namespace denied; list excludes other Teams |
| Plugin role matrix | policy and real Git tests | unit/Git | developer push; maintainer publish | viewer push and developer publish denied |
| HTTP contract | handler/OpenAPI conformance | HTTP | envelopes/status/page totals | malformed IDs/body, forbidden/not-found/conflict stable |
| Frontend | Vitest/testing-library | frontend | registration/admin/Team happy paths | 401/403/404/409, stale request cancellation, accessible dialogs |
| Migration | clean + existing-schema migration | SQLite/PostgreSQL | membership table/constraints install | invalid role/orphan/duplicate rejected |

Implementation gates: focused Go tests during TDD, `go test ./...`, `go test -race ./...`, `go vet ./...`, `go build ./...`, frontend `npm run check`, `npm test`, `npm run build`, and embedded frontend Go tests. PostgreSQL is conditional on `MARKETPLACE_TEST_POSTGRES_DSN` and must be reported as skipped if unavailable.

## 6. Commit slices and ownership

| Slice | Exclusive concern | Dependency | Review boundary |
|---|---|---|---|
| User persistence/service | shared create transaction, admin queries/commands, registration config behavior | existing identity | complete backend user lifecycle without routes |
| User HTTP contract | registration, `/me`, admin-user handlers, proposed/deployed OpenAPI when implemented | user service | independently testable management API |
| Team persistence/service | Team Namespace, membership, invitations, transactional guards | user service | complete Team control plane without Plugin policy changes |
| Authorization integration | action/resource contracts, state reader, Team role matrix, Plugin/Git policy | Team service | current membership immediately controls resources |
| Frontend | router/layout, registration, users, Teams, preserve PAT UI | deployed API | static frontend feature slice |
| Integration/docs | backend DI/routes, current-state/protocols/roadmap/OpenAPI authority | all prior slices | full runtime and documentation conformance |

Shared files (`backend/mod.go`, `pkg/auth/contracts.go`, policy, OpenAPI, `App.svelte`) have one serial integration owner. Every functional commit must build and pass its applicable focused tests; no unrelated refactor, amend, skipped hook, or push without explicit request.

## 7. Approval record

- **Design choices confirmed:** public registration config switch default-off; Namespace-as-Team with multiple owners; direct add plus existing-username in-product invitations; seven-day expiry with revoke/reissue; active system administrators may grant/revoke another user's system-admin membership; structured logs without audit persistence.
- **Design approval:** user approved the complete design on 2026-09-01.
- **Implementation approval:** user authorized TDD implementation on 2026-09-01.
- **Current status:** implemented for the backend lifecycle, migrations, routes, authorization integration, and deployed OpenAPI; the authoritative runtime inventory is `docs/current-state.md`.
- **Approved boundary:** exact lifecycle, deferred Team deletion/transfer, fixed role matrix, and accepted non-durable audit gap.
