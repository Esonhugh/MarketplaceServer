# User and Team Lifecycle Management API Rationale

- **Status:** `implemented`
- **Owner:** `backend/domain/identity`, `backend/domain/authorization`, and identity management handlers
- **Created:** 2026-08-31
- **Approval record:** user approved design and implementation on 2026-09-01; backend lifecycle is implemented

Exact JSON Schema, parameters, examples, and operation IDs for this implemented lifecycle are authoritative in `api/openapi/management-v1.yaml`; `management-v1-design.yaml` retains the matching lifecycle contract alongside future proposed slices. Routes were promoted after implementation; OpenAPI conformance assertions must track the deployed route inventory.

## Capability and current-user operations

| Method/path | Authentication | Result | Stable failures |
|---|---|---|---|
| `GET /api/v1/auth/capabilities` | public | `{registrationEnabled}` | `500` |
| `POST /api/v1/auth/register` | public; route exists only when enabled | creates active account and returns same `LoginResult` as login | `400`, `409`, `422`, `500`; disabled is `404` |
| `GET /api/v1/me` | Bearer JWT | safe current user profile, `systemAdmin`, and navigation capabilities | `401`, `403`, `500` |

Registration request:

```json
{
  "username": "alice",
  "displayName": "Alice",
  "email": "alice@example.test",
  "password": "a sufficiently long password"
}
```

`email` is optional and nullable; all other fields are required. Secret-bearing registration/JWT responses use `Cache-Control: private, no-store` and `Pragma: no-cache`. Password and hash never appear in any response.

## System-administrator user operations

All operations require Bearer JWT plus current system-admin policy.

| Method/path | Action/resource | Request | Success |
|---|---|---|---|
| `GET /api/v1/admin/users` | `user.list` / `user_collection` | `page`, `size`, optional exact `status` | `200` exact page |
| `POST /api/v1/admin/users` | `user.create` / `user_collection` | registration fields plus optional `status` | `201` safe User DTO |
| `GET /api/v1/admin/users/{userId}` | `user.read` / exact user | none | `200` safe User DTO |
| `PATCH /api/v1/admin/users/{userId}` | `user.update` / exact user | `{displayName}` | `200` safe User DTO |
| `POST /api/v1/admin/users/{userId}:disable` | `user.disable` / exact user | bodyless | `204` idempotent |
| `POST /api/v1/admin/users/{userId}:enable` | `user.enable` / exact user | bodyless | `204` idempotent |
| `PUT /api/v1/admin/users/{userId}/system-admin` | `user.admin.grant` / exact user | bodyless | `204` idempotent |
| `DELETE /api/v1/admin/users/{userId}/system-admin` | `user.admin.revoke` / exact user | none | `204` idempotent |

Safe User DTO contains `id`, `username`, nullable `email`, `displayName`, `status`, `createdAt`, `updatedAt`; never password hash or PAT data.

Malformed/noncanonical IDs are `404` to match exact-resource nondisclosure. Invalid page/filter/body is `422` after syntactically valid JSON; malformed/trailing/oversized body is `400`. Duplicate username/email/namespace slug is `409`. Non-admin is `403`. Self-disable, self admin-membership change, disabling the final active global administrator, and revoking the final active global administrator are `409`. Safe User DTO additionally includes `systemAdmin` for this administrator-only view.

## Team operations

| Method/path | Action/resource | Request | Success |
|---|---|---|---|
| `GET /api/v1/teams` | `team.list` | `page`, `size`; optional `scope=all` only for system admin | `200` exact Team page |
| `POST /api/v1/teams` | `team.create` | `{slug,displayName}` | `201` Team DTO |
| `GET /api/v1/teams/{team}` | `team.read` / Team namespace | none | `200` Team detail |
| `PATCH /api/v1/teams/{team}` | `team.settings.write` / Team namespace | `{displayName}` | `200` Team detail |
| `GET /api/v1/teams/{team}/members` | `team.members.read` / Team namespace | `page`, `size` | `200` exact member page |
| `PUT /api/v1/teams/{team}/members/{userId}` | `team.members.manage` / exact membership | `{role}` | `200` created or updated member DTO |
| `DELETE /api/v1/teams/{team}/members/{userId}` | `team.members.manage` / exact membership | none | `204` |
| `GET /api/v1/teams/{team}/invitations` | `team.invitations.read` / Team namespace | `page`, `size` | `200` exact invitation page |
| `POST /api/v1/teams/{team}/invitations` | `team.invitations.manage` / Team namespace | `{username,role}` | `201` pending invitation |
| `DELETE /api/v1/teams/{team}/invitations/{invitationId}` | `team.invitations.manage` / Team namespace | none | `204` revoke idempotently |
| `POST /api/v1/teams/{team}/invitations/{invitationId}:reissue` | `team.invitations.manage` / Team namespace | bodyless | `201` replacement invitation |
| `GET /api/v1/me/team-invitations` | `team.invitations.read` / current user | `page`, `size` | `200` current pending invitation page |
| `POST /api/v1/me/team-invitations/{invitationId}:accept` | `team.invitation.respond` / exact invitation | bodyless | `200` Team DTO |
| `POST /api/v1/me/team-invitations/{invitationId}:reject` | `team.invitation.respond` / exact invitation | bodyless | `204` |

Team DTO:

```json
{
  "id": "uuid",
  "slug": "security",
  "displayName": "Security",
  "currentUserRole": "owner",
  "permissions": {
    "readMembers": true,
    "manageMembers": true,
    "updateSettings": true,
    "manageOwners": true
  },
  "createdAt": "...",
  "updatedAt": "..."
}
```

For system administrators without direct membership, `currentUserRole` is null and permissions reflect explicit system-admin authority. The client consumes permission booleans for controls; it does not reconstruct policy from the role.

Member DTO contains safe user identity/profile, Team role, and membership timestamps. Add/update uses `PUT` because `(Team,user)` has one desired role and repeated identical requests are idempotent. It returns `200` in both create/update cases to avoid response branching on prior membership existence.

Invitation DTO contains opaque invitation ID, Team safe identity, invited user's safe username/display name where the Team manager is authorized, fixed role, inviter safe identity, derived `pending|accepted|rejected|revoked|expired` status, expiry, and lifecycle timestamps. It contains no secret or email. Inbox queries only return invitations whose `user_id` matches the current JWT principal.

Unknown/non-disclosed Team or invitation is `404`; authenticated member with insufficient role receives `403`. Unknown target user is `404`. Duplicate Team slug, live duplicate invitation, already-member target, stale invitation response, and final-owner removal/demotion are `409`. Invalid role/body is `422`.

## Request and response conventions

- Existing success `{data: ...}` envelope and error `{code,message,requestId,details?}` remain unchanged.
- Unknown JSON fields remain ignored for consistency with deployed management handlers; malformed, top-level null, trailing, unreadable, or oversized JSON is rejected.
- List pagination remains one-based `page`, `size`, exact `total`, max size 100.
- JWT is the only accepted credential for protected management routes; PAT/password Basic never fall back.
- Team slug path resolution is canonical lowercase kebab-case and always resolves a `kind='team'` namespace.
- Mutation responses are `private, no-store` when they contain account email; public capability response may be `no-cache`.
