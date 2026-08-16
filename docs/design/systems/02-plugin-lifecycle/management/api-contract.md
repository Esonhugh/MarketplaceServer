# Plugin Lifecycle Management API Contract

- **Status:** `approved`
- **Owner:** `backend` Plugin lifecycle domain and management handler
- **Design approval:** 2026-08-16
- **Implementation approval:** none

## 请求平面与 authority

本文件记录 management-plane rationale、authorization 与 lifecycle 边界。精确 proposed HTTP wire 以 [management-v1-design.yaml](../../../../../api/openapi/management-v1-design.yaml) 为权威；当前 deployed [management-v1.yaml](../../../../../api/openapi/management-v1.yaml) 尚无这些 Plugin routes。

所有 management mutation 使用 Bearer JWT。PAT、account password、Git Basic credential 和 distribution credential 均不能认证 `/api/v1`。DTO 与 GORM record 隔离；API 不暴露 Plugin/Repository UUID、storage key、path 或 internal recovery detail。

JSON、pagination 与 error 行为沿用 management v1：bounded single JSON、unknown fields ignored、malformed/missing/null/trailing body 为 `400`、semantic validation 为 `422`、page/size/total envelope、framework-generated ULID request ID。

## Operations

| Operation | Auth/action | Semantics | Success | Stable failure |
|---|---|---|---|---|
| `POST /api/v1/namespaces/{namespace}/plugins` | JWT + `plugin.create` | lowercase kebab-case `name`，可选 `visibility` 默认 public；同步创建 shared-ID hidden repo 和 `HEAD=main` | `201` only after aggregate ready | `400`, `401`, `403`, `409`, `422`, safe `500` |
| `GET /api/v1/namespaces/{namespace}/plugins` | JWT + `plugin.list` | namespace-scoped stable page | `200` | `401`, `403`, `422`, `500` |
| `GET /api/v1/namespaces/{namespace}/plugins/{plugin}` | `plugin.read` | exact metadata read | `200` | anonymous/private/draft/unknown nondisclosure uses `404` |
| `POST .../{plugin}:archive` | JWT + `plugin.archive` | idempotent archive；记录恢复前状态 | bodyless `204` | `401`, `403`, `404`, `409`, `500` |
| `POST .../{plugin}:restore` | JWT + `plugin.archive` | archived 恢复为 `archived_from` 的 draft/active | bodyless `204` | `401`, `403`, `404`, `409`, `500` |
| `POST .../{plugin}:set-visibility` | JWT + `plugin.archive` | idempotent set public/private；不重写已发布 Marketplace index | bodyless `204` | `400`, `401`, `403`, `404`, `409`, `422`, `500` |
| `GET .../{plugin}/versions` | JWT + `plugin.read` | list available logical versions only | `200` | `401`, `403`, `404`, `422`, `500` |
| `GET .../{plugin}/versions/{tag}` | `plugin.read` | exact available Version read；visible deleted tombstone returns error only | `200` or `410` | private/draft/unknown nondisclosure uses `404` |
| `POST .../{plugin}/versions:publish` | JWT + `plugin.publish` | strict validate current canonical tag，CAS full SHA；first success activates draft | `201` | `400`, `401`, `403`, `404`, `409`, `422`, `500` |
| `POST .../{plugin}/versions/{tag}:set-default` | JWT + `plugin.publish` | set an available canonical tag as owner-selected default | bodyless `204` | `401`, `403`, `404`, `409`, `500` |
| `DELETE .../{plugin}/default-version` | JWT + `plugin.publish` | idempotently clear default | bodyless `204` | `401`, `403`, `404`, `409`, `500` |

Archive/restore and visibility are explicit commands; `DELETE /plugins/{plugin}` is not defined. Physical deletion is outside this design.

## DTOs

Plugin response fields are exactly:

```text
namespace
name
status              draft | active | archived
visibility          public | private
repositoryStatus    ready | readOnly | error
cloneUrl            same-origin relative /git/{namespace}/{plugin}.git
defaultVersion      canonical tag or null
createdAt
updatedAt
```

Version response fields are exactly:

```text
tag
status              available
commitSha
publishedAt
updatedAt
```

Tag is the complete Version identity. There is no duplicate `version`, publication generation, Plugin UUID, Repository UUID, storage key or path. Deleted Version does not render as a normal DTO; an authorized exact query receives `410 Gone` with the normal error envelope and no tombstone metadata.

Publish accepts:

```json
{
  "tag": "v1.2.0",
  "makeDefault": true
}
```

`makeDefault` defaults false. Default can also be changed through its explicit command. Deleting the published default tag clears it; no older/latest Version is selected automatically, and restoring the same tag does not restore its default status.

## Visibility and authorization

Actions are exactly `plugin.create`, `plugin.list`, `plugin.read`, `plugin.write`, `plugin.archive`, and `plugin.publish`.

- anonymous may read only exact known public active/archived Plugin and available Version metadata;
- draft is never anonymous, even when public;
- private/unknown/unauthorized exact resources use generic `404` where nondisclosure applies;
- archived remains readable but denies write/publish/default/visibility mutation until restore;
- Repository `readOnly` allows read and denies write; `error` fails Git and distribution closed;
- management always performs final `Principal + Action + Resource + Context` authorization.

Changing public to private changes current content authorization only. Existing Marketplace index configuration is not rebuilt or erased, and historical index visibility is not a content credential.

## Frontend boundary

No frontend implementation is authorized by this slice. A future static frontend may consume only these management DTOs and must not infer authorization from button visibility or expose internal intent/recovery state.
