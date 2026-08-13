# Management API Contract

- **Status:** Approved
- **Last reviewed:** 2026-08-11
- **Approval record:** 用户在交互评审中确认 OpenAPI 只作 API 文档、deployed/proposed 分离、page/size pagination、handwritten client 与 JWT frontend auth
- **Scope:** `/api/v1` 当前与目标 wire conventions
- **Implementation conformance:** identity login/health/PAT slice 已符合 deployed contract；broader Plugin/Marketplace/team target 仍未实现

## 1. 两份 OpenAPI 文档

- [`api/openapi/management-v1.yaml`](../../api/openapi/management-v1.yaml)：**deployed**，只描述当前 runtime 已注册且测试证明的行为。
- [`api/openapi/management-v1-design.yaml`](../../api/openapi/management-v1-design.yaml)：**proposed**，描述已评审的目标 API，不代表 route、handler、migration 或 frontend 已实现。

OpenAPI 3.1 是完整公开 wire contract 权威，定义精确 path、method、parameter、schema、status、header 和 example；它不生成 Go DTO、TypeScript types 或 frontend client。Proposed OpenAPI 定义目标，deployed OpenAPI 与源码/routes/tests 共同证明当前 behavior；[`docs/current-state.md`](../current-state.md) 仍是 capability inventory。

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
  "details": "field is invalid"
}
```

`code` 是稳定机器值，`message` 只供人阅读。`details` 是可选安全字符串，客户端不能依据它分支。

## 3. Request decoding 与 request ID

目标 handler behavior：

- JSON body 有大小上限；
- unknown JSON fields 被忽略，以允许客户端/服务端渐进演进；
- malformed JSON、缺失 required body 和 trailing second JSON value 被拒绝；
- URL、slug、UUID、tag 和时间分别按自己的规则校验；
- error response 返回框架生成的 ULID `X-Request-Id`；
- error body 的 `requestId` 与 header 相同；success/204 只有 operation 明确定义时才返回 request ID；
- caller-supplied request ID 不被信任或复用。

OpenAPI request schema 不使用 `additionalProperties: false` 来宣称 runtime 拒绝未知字段。Response schema可以描述已知输出，但 server 不输出未定义的 persistence/internal fields。

## 4. Authentication

### Frontend

`POST /api/v1/auth/login` 接收 username/password，成功签发固定 30 天的 stateless HS256 JWT。JWT 只包含 canonical `username`、`iat` 和 `exp`，不包含 `sub`、`jti`、`authVersion`、role、namespace、scope 或 permission snapshot。JWT 验签不查询用户表；资源 endpoint 仍按当前 resource policy 默认拒绝。已签发 JWT 不支持 server-side revoke，普通 logout 只清除浏览器状态，无 refresh token。

JWT key 从 YAML `backend.jwtSecret` 加载，`MARKETPLACE_JWT_SECRET` 优先覆盖；两者缺失/空值，或环境变量显式设置为空时，认证初始化失败。Secret rotation 仍需单独设计。

### Management

除 login、health 等显式 public allowlist 外，management API 只接受 Bearer JWT。Account Basic 与 PAT Basic 都不能调用 `/api/v1`。

### Git 与 subscription

PAT 只表达 cumulative `sub-read`、`git-clone` 或 `git-write` capability，并始终与当前资源 policy 取交集。Git Smart HTTP 和 subscription distribution 只接受合适 preset 的 PAT；account password 和 JWT 不是这些平面的 credential。

## 5. Persistence/API DTO boundary

- Go/Jin request/response DTO handwritten；
- frontend API client handwritten；
- handler 不序列化 GORM record；
- DB/API IDs 使用 string，内部 service/contract 可以使用 `uuid.UUID`；
- timestamp 使用 RFC 3339；
- plaintext credential 只出现在 owning system 明确定义的 create/reveal DTO；repeatably revealable persistence 必须引用 ADR-0006；
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

## 7. 当前实现边界

Identity slice 已部署并由 `management-v1.yaml` 描述：health/login 公开；PAT list/create/revoke/reveal 只接受 Bearer JWT；success 使用 `data` envelope，list 使用 page/size/exact total；create/reveal 返回 plaintext 且禁止缓存；error 使用框架 ULID header/body。旧 Basic/cursor/scope identity components 已无 deployed reference。

`management-v1-design.yaml` 中 Plugin、Plugin version、Marketplace、revision 与 distribution credential path 仍是 proposed，未进入 deployed OpenAPI，也不能由 identity 完成状态推导为已实现。

Identity persistence 只保留获批目标模型，不增加 legacy 双读/backfill。Migration 发现旧 scope table 或缺少目标 PAT columns 时拒绝启动；开发环境由 operator 确认可丢弃后重建，非开发环境先备份并设计显式 migration/credential rotation。
