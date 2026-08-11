# Management API Contract

- **Status:** Approved
- **Last reviewed:** 2026-08-11
- **Approval record:** 用户在交互评审中确认 OpenAPI 只作 API 文档、deployed/proposed 分离、page/size pagination、handwritten client 与 JWT frontend auth
- **Scope:** `/api/v1` 当前与目标 wire conventions
- **Implementation conformance:** 当前 handler 仅部分符合目标；差异不会因本文档而自动改变

## 1. 两份 OpenAPI 文档

- [`api/openapi/management-v1.yaml`](../../api/openapi/management-v1.yaml)：**deployed**，只描述当前 runtime 已注册且测试证明的行为。
- [`api/openapi/management-v1-design.yaml`](../../api/openapi/management-v1-design.yaml)：**proposed**，描述已评审的目标 API，不代表 route、handler、migration 或 frontend 已实现。

OpenAPI 3.1 是公开 wire contract 的文档权威，但只用于 review、lint、example validation 和实现对照，不生成 Go DTO、TypeScript types 或 frontend client。源码、routes 和 tests 仍决定 deployed behavior；[`docs/current-state.md`](../current-state.md) 仍是当前 capability inventory。

Git Smart HTTP、distribution endpoints 和外部 `marketplace.json` 不属于 management OpenAPI。

## 2. 目标 response convention

所有非 `204` success：

```json
{
  "data": {}
}
```

List success：

```json
{
  "data": {
    "items": [],
    "page": 1,
    "size": 20,
    "total": 0
  }
}
```

规则：

- `page` 从 1 开始，默认 1；
- `size` 默认 20，最大 100；
- `total` 是应用 authorization/filter 后的 exact total；
- server 使用固定稳定顺序和唯一 tie-breaker；
- frontend 可以对当前页临时排序，但不能把它描述成全结果排序；
- bodyless success 使用 `204`，不返回 `{ "data": null }`。

Error 不包 `data`：

```json
{
  "code": "validation_failed",
  "message": "request is invalid",
  "requestId": "...",
  "details": []
}
```

`code` 是稳定机器值，`message` 只供人阅读。`details` 仅在 operation 定义了安全结构时返回。

## 3. Request decoding 与 request ID

目标 handler behavior：

- JSON body 有大小上限；
- unknown JSON fields 被忽略，以允许客户端/服务端渐进演进；
- malformed JSON、缺失 required body 和 trailing second JSON value 被拒绝；
- URL、slug、UUID、tag 和时间分别按自己的规则校验；
- 所有 response 都返回 `X-Request-Id`；
- error body 的 `requestId` 与 header 相同；
- caller-supplied request ID 是 bounded opaque value，不能作为信任凭据。

OpenAPI request schema 不使用 `additionalProperties: false` 来宣称 runtime 拒绝未知字段。Response schema可以描述已知输出，但 server 不输出未定义的 persistence/internal fields。

## 4. Authentication

### Frontend

`POST /api/v1/auth/login` 接收 username/password，成功签发固定 30 天的 HS256 JWT。JWT claims 至少包含：

- `sub`：user identity；
- `iat`、`exp`；
- `jti`；
- `authVersion`。

JWT 不缓存 role 或 resource permission。每次 Bearer request 重新检查 user active state、当前 `auth_version` 和当前 resource authorization。disable user、password change 或 logout-all 增加 `auth_version`，旧 JWT 下一请求失效。普通 logout 只清除浏览器状态。无 refresh token；过期后重新 password login。

HS256 secret 来自受保护环境变量，至少包含 32 random bytes。secret rotation 仍需单独设计。

### CLI / automation

Management API 同时接受 HTTP Basic `username + PAT`。PAT scope 与用户当前权限取交集，不能扩大权限。

### Git

Git Smart HTTP 只接受 PAT；account password 和 JWT 不是 Git credential。Management、Git 与 distribution credential 不能跨 request plane 使用。

## 5. Persistence/API DTO boundary

- Go/Jin request/response DTO handwritten；
- frontend API client handwritten；
- handler 不序列化 GORM record；
- DB/API IDs 使用 string，内部 service/contract 可以使用 `uuid.UUID`；
- timestamp 使用 RFC 3339；
- secret plaintext 只出现在明确的一次性 creation response，并使用 `private, no-store`；
- storage key、filesystem path、hash/HMAC、Authorization 和 private runtime config 不进入 DTO、error 或 log。

## 6. Frontend client

Svelte component 只通过一个 application-owned `apiClient` 调用 `/api/v1`。client 负责：

- Bearer header；
- base URL、cancellation 和 JSON decode；
- direct error normalization；
- 401 时清除已保存登录信息；
- request ID 保留用于错误诊断。

初期不引入全局 query/cache framework。页面使用局部 state，mutation 成功后重新获取当前 list/detail。UI 显式处理 loading、empty、401、403、404、conflict 和 retry。

`localStorage` 只保存 username 和 JWT，不保存 password、PAT plaintext、distribution credential 或其他 secret。

## 7. 当前兼容性差异

Deployed OpenAPI 必须继续描述当前行为，直到单独 implementation slice 修改 handler/tests：

| Current operation | Deployed behavior | Approved target |
|---|---|---|
| `GET /api/v1/health` | direct health payload | `{ "data": Health }` |
| `GET /api/v1/me/tokens` | direct cursor page | enveloped page/size/total list |
| `POST /api/v1/me/tokens` | enveloped create, Basic auth, rejects unknown fields | envelope retained；Bearer JWT + Basic PAT；unknown fields ignored |
| token success / health | 不保证 `X-Request-Id` | all responses include header |
| frontend login | route 不存在 | `POST /api/v1/auth/login` |

这些差异必须通过独立 identity/API TDD slice 迁移，不能只修改 deployed OpenAPI 后声称实现。
