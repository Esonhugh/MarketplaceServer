# User and Team Lifecycle Persistence Design

- **Status:** `implemented`
- **Owner:** `backend/domain/identity`
- **Created:** 2026-08-31
- **Approval record:** user approved design and implementation on 2026-09-01; persistence lifecycle is implemented

## Existing records reused

`users` remains the account authority. `namespaces` remains the tenant identity for both personal and Team resources. Existing immutable guards continue to protect:

- `users.username`, `users.email`;
- `namespaces.kind`, `namespaces.slug`, `namespaces.owner_user_id`.

No duplicate `teams` table is added. A Team is a `namespaces` row with `kind='team'` and `owner_user_id IS NULL`.

## New record: `team_memberships`

| Field | Representation | Mutable | Constraint/index | Meaning |
|---|---|---:|---|---|
| `namespace_id` | `string`, `char(36)` | no | composite PK; FK `namespaces(id)` RESTRICT | Team namespace |
| `user_id` | `string`, `char(36)` | no | composite PK; FK `users(id)` RESTRICT; user-list index | Direct member |
| `role` | string | yes | check in `owner|admin|maintainer|developer|viewer`; Team role index | Current fixed role |
| `created_at` | UTC `time.Time` | no | non-null | Membership creation |
| `updated_at` | UTC `time.Time` | yes | non-null | Last role change |

Proposed GORM identity:

```go
type TeamMembership struct {
    NamespaceID string    `gorm:"type:char(36);primaryKey"`
    UserID      string    `gorm:"type:char(36);primaryKey;index:idx_team_memberships_user"`
    Role        string    `gorm:"size:32;not null;index:idx_team_memberships_namespace_role;check:chk_team_memberships_role,role IN ('owner','admin','maintainer','developer','viewer')"`
    CreatedAt   time.Time `gorm:"not null"`
    UpdatedAt   time.Time `gorm:"not null"`
}
```

Database guards additionally reject a membership whose namespace is not `kind='team'`. Because a cross-table condition cannot be represented by a portable SQL `CHECK`, migration installs:

- PostgreSQL insert/update trigger reading the referenced namespace kind;
- SQLite insert/update trigger with `NOT EXISTS` Team namespace predicate.

Membership key fields are immutable through PostgreSQL/SQLite update triggers. Role remains mutable.

## New record: `team_invitations`

Invitations target an existing user ID and carry no bearer secret.

| Field | Representation | Mutable | Constraint/index | Meaning |
|---|---|---:|---|---|
| `id` | canonical UUID string | no | PK | Opaque invitation locator |
| `namespace_id` | `string`, `char(36)` | no | FK Team namespace RESTRICT; Team-page index | Inviting Team |
| `user_id` | `string`, `char(36)` | no | FK user RESTRICT; inbox index | Exact invited existing user |
| `role` | fixed Team role | no | same role check as membership | Role granted on acceptance |
| `invited_by_user_id` | `string`, `char(36)` | no | FK user RESTRICT | Actor who issued this invitation |
| `expires_at` | UTC `time.Time` | no | non-null; inbox index | Exactly seven days after creation |
| `accepted_at` | nullable UTC time | one-way | terminal exclusivity check | Accepted terminal state |
| `rejected_at` | nullable UTC time | one-way | terminal exclusivity check | Rejected terminal state |
| `revoked_at` | nullable UTC time | one-way | terminal exclusivity check | Revoked/reissued terminal state |
| `created_at`, `updated_at` | UTC `time.Time` | bounded | non-null | Lifecycle facts |

A row is pending when every terminal timestamp is null and `expires_at > now`; expired is derived and does not require a scheduler write. A DB check permits at most one terminal timestamp. Partial unique indexes enforce at most one pending row per `(namespace_id,user_id)` in PostgreSQL and SQLite. Reissue transaction terminalizes the prior pending row before inserting the replacement.

Accept transaction locks the invitation, verifies target/current active user, pending/unexpired state, Team existence, nonmembership, and role; then inserts membership and sets `accepted_at`. Exactly one concurrent accept succeeds. Reject and revoke similarly use expected-pending predicates. Historical terminal invitation rows are retained.

## User creation transaction

Input is normalized and validated before hashing:

- username: trimmed lowercase canonical slug, 1–64 bytes;
- display name: trimmed, 1–255 UTF-8 bytes;
- email: omitted/null or trimmed lowercase, simple syntactic email validation, max 320 bytes;
- password: 12–1024 UTF-8 bytes, must not use reserved `mpsk_` prefix.

The service hashes with current default Argon2id parameters before opening the transaction. The transaction creates:

1. `users{id,username,email,display_name,status,password_hash}`;
2. `namespaces{id,kind='user',slug=username,display_name,owner_user_id=user.id}`.

Unique violations on username, email, or namespace slug map to `conflict` without revealing which existing account owns an email. Registration and administrator creation use the same repository command. There is no check-then-insert race.

Public registration always creates `active`. Administrator creation may choose `active|disabled` and defaults to `active`.

## User queries and mutations

- List uses exact total and stable `created_at DESC, id DESC`, page 1/size 20/max 100.
- Optional `status` filter is exact. First slice omits free-text search to avoid underspecified collation/index behavior.
- Get/mutation predicates use canonical user ID, not mutable display fields.
- Profile update permits only `display_name`.
- Status update uses `WHERE id=? AND status=?` CAS semantics; idempotent requests return `204` after confirming existence.
- The service rejects disabling the acting user. It also rejects disabling the final active user holding the fixed admin-group membership. PostgreSQL serializes this check with a transaction advisory lock; SQLite uses its supported write transaction boundary.
- System-admin grant inserts the fixed admin-group membership idempotently; revoke deletes another user's membership after the same serialized final-active-admin check. Self grant/revoke is rejected. No generic system-group CRUD is exposed.

## Team creation and queries

Creation transaction inserts:

1. `namespaces{id,kind='team',slug,display_name,owner_user_id=NULL}`;
2. `team_memberships{namespace_id,user_id=actor.id,role='owner'}`.

Team slug is globally unique because namespace routing is global and personal/Team namespaces share paths. A username may therefore reserve a future Team slug and vice versa.

Team list joins `team_memberships` to `namespaces`, scopes by `user_id`, and orders `namespaces.created_at DESC, namespaces.id DESC`. System-admin all-Team list is a separate query mode and never emulated by loading all rows then filtering.

Member list is scoped by `namespace_id` in SQL, joins safe user metadata, returns exact total, and orders `team_memberships.created_at ASC, user_id ASC`.

## Last-owner transaction

Role change/removal for an owner executes in one transaction:

1. verify Team namespace and acting authorization facts;
2. lock all owner memberships for the Team (`FOR UPDATE` on PostgreSQL);
3. count current owners;
4. reject if the target is owner, owner count is one, and command would remove owner role;
5. update/delete with `(namespace_id,user_id,current_role)` predicate.

Concurrent owner changes are serialized per Team. SQLite is only a single-process development/test target and uses its write transaction locking. No process-local lock is treated as PostgreSQL correctness.

## Migration and existing data

Ordered migration adds `TeamMembership` and `TeamInvitation` after `User` and `Namespace`, before records that consume authorization. Existing Team namespace fixture/data rows remain valid and gain no synthetic owner. This is intentional: migration cannot safely infer ownership.

Operational rule for pre-existing Team namespaces:

- migration succeeds;
- a Team with no membership is inaccessible to ordinary users but remains visible/manageable to a system administrator;
- system administrator may add an initial owner through the member endpoint;
- frontend labels zero-owner Teams as requiring administrator repair.

This avoids destructive migration or fabricated ownership. A clean database and all newly created Teams always have at least one owner; runtime commands preserve that invariant.

Migration tests cover fresh SQLite/PostgreSQL, upgrade with existing Team namespaces, membership/invitation role and FK constraints, one-live-invitation uniqueness, terminal exclusivity, non-Team guards, and immutable membership/invitation identity.
