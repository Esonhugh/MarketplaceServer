# Identity Authentication System Design

- **Status:** `approved`
- **Owner:** `backend/domain/identity`，由 `backend` module 装配
- **Created:** 2026-08-12
- **Last reviewed:** 2026-08-12
- **Approval record:** 用户通过逐项 AskUser 评审直接批准
- **Related roadmap:** [Authentication hardening](../../../roadmap.md#authentication-hardening)

> 本设计已由当前 identity slice 实现；精确 deployed wire、当前 capability 与运行验证分别以 deployed OpenAPI、`current-state.md` 和实际 test report 为准。

## 1. 用户结果、scope 与当前基线

### 用户结果

- 用户用 canonical username 和账号密码登录 management frontend，获得固定 30 天 HS256 JWT。
- Frontend 使用 JWT 调用受保护的 `/api/v1` management operation；公开 allowlist 仅包含 login、health 等显式 route。
- 用户管理自己的 PAT；PAT 可用于 subscription read、development Git clone/fetch 或 push，但不能调用 management API。
- PAT owner 可以再次 reveal 自己的 PAT，并可管理 revoked/expired 历史记录。

### Scope

第一版只设计五个 management operations：

```text
POST   /api/v1/auth/login
GET    /api/v1/me/tokens
POST   /api/v1/me/tokens
DELETE /api/v1/me/tokens/{tokenId}
POST   /api/v1/me/tokens/{tokenId}/reveal
```

### 非目标

- 不增加 `auth_version`、JWT session、refresh token、revoked JTI 或 logout-all storage。
- 不做 login throttling、Argon2 concurrency guard、email login、OIDC 或 signing-key ring。
- 不设计 team/service-account credential、private Marketplace credential 或 subscription 数据模型。
- 不允许 PAT 访问 `/api/v1`。

### 当前实现

当前 runtime 已实现用户与 bootstrap、password login、固定 30 天 HS256 JWT、management Bearer-only、PAT create/page-list/reveal/revoke、preset 与 plane-specific Git/subscription authenticator。Identity 在 `model`、`dao`、`service` package 内分离持久化 record、GORM/migration 与业务认证；management handler 不序列化 GORM record。当前事实与精确 route 见 [`current-state.md`](../../../current-state.md) 和 deployed OpenAPI。

## 2. Authentication 与 credential lifecycle

### Stateless frontend JWT

- Login 只接受 username/password。
- JWT 只包含 canonical `username`、`iat` 和 `exp`；不包含 `sub`、`jti`、`authVersion`、role、namespace、scope 或 permission snapshot。
- Header 固定要求 `alg=HS256`，不使用 `kid`。
- 有效期固定 30 天，`iat`/`exp` 验证允许 60 秒 clock skew；无 refresh token。
- JWT 验签不查询用户表。账号被 disabled 后，已签发 JWT 仍可认证至到期；disabled 会阻止后续 password login 和 PAT authentication。
- 资源 endpoint 仍按目标资源查询当前 policy/membership。JWT username 找不到授权事实时按该资源 policy 默认拒绝，不降级为 anonymous。
- Principal credential type 明确为 `CredentialJWT`。

JWT key 配置保持简单：YAML `backend.jwtSecret`，环境变量 `MARKETPLACE_JWT_SECRET` 优先；任意非空 raw string 可用。缺失/空值使认证初始化失败；环境变量显式存在但为空时不回退 YAML。示例配置只列空值并要求生产使用受保护环境/secret manager。

### PAT preset

PAT 使用三个稳定、逐级包含的 preset：

| Preset | Capability |
|---|---|
| `sub-read` | 读取当前用户获准读取的 subscription Marketplace 输出 |
| `git-clone` | `sub-read` + development Git clone/fetch |
| `git-write` | `git-clone` + development Git push，等同 full access |

数据库只保存 preset 名，不保存 action scope rows。Preset 只是 credential capability，不能扩大用户权限：subscription、clone 和 push 仍与当前 Marketplace/Plugin policy 取交集。

自动 `all` Marketplace 是每个用户保留且不可删除的 subscription identity，内容为该用户当前可读的全部 Plugin；用户也可像 playlist 一样组织多个自定义 Marketplace。具体 persistence、authoring 和 distribution locator 由后续 subscription/Marketplace 系统设计，PAT 不绑定或修改 subscription 内容。

### PAT lifecycle

```text
active --expiresAt reached--> expired
active --DELETE/revoke------> revoked
```

- 创建请求包含 `name`、`preset` 和可选未来 RFC 3339 `expiresAt`。
- PAT 格式保持 `mpsk_` 加 43 位 Base64URL secret。
- List 返回 active、expired、revoked 全部 owner records；统一计算 `status`，`revoked` 优先于 `expired`。
- DELETE 只 revoke，不物理删除；重复 revoke 幂等返回 204。
- Revoked/expired PAT 不能认证，但 owner 仍可 reveal 以排障。
- Reveal body 只含当前账号 password；先验证 CredentialJWT 和密码，再按 token owner policy 授权。第一版只有 PAT owner 可 reveal，system admin 只能在未来显式管理设计中获得其他能力。

## 3. Authorization 与请求平面

| Plane/operation | Accepted credential | Explicit deny |
|---|---|---|
| `/api/v1/auth/login` | account username/password body | PAT、JWT 不替代 password login |
| `/api/v1/health` | anonymous | 无需 credential |
| 其他 `/api/v1` management | Bearer JWT | account Basic、PAT Basic |
| `/git` upload-pack | Basic username + `git-clone`/`git-write` PAT，另与 Plugin read policy 取交集 | password、JWT、`sub-read` PAT |
| `/git` receive-pack | Basic username + `git-write` PAT，另与 Plugin write policy 取交集 | password、JWT、其他 preset |
| subscription distribution | Basic username + 任一含 `sub-read` 的 PAT，另执行当前 subscription policy | password、JWT |

所有服务端授权默认拒绝。Management、Git 与 distribution 使用独立 handler/authenticator；不能用一个“非 PAT 就尝试账号密码”的通用 Basic fallback。

## 4. Consistency、secret 与 frontend

- Login/JWT 不引入新的数据库写入或跨系统 transaction。
- PAT create/revoke 是单数据库 transaction；last-used 更新不能改变请求侧 credential secret。
- 经 [ADR-0006](../../../decisions/0006-repeatable-credential-plaintext-storage.md) 明确批准，repeatably revealable PAT 保存 `secret_plaintext` 与 lookup `secret_hmac`。数据库和备份属于 credential trust boundary。
- Persistence record 绝不能直接序列化；`secret_plaintext` 不进入 list、日志、audit、error 或 debug output，只进入 create/reveal DTO。
- Reveal 安全结构化日志只记录 actor username、credential ID、result 和可用 request ID，不记录 password、PAT、Authorization 或 body。
- Frontend localStorage 仍只保存 username/JWT。Reveal 后 PAT 可保留在当前 frontend 内存会话，不能写 localStorage/sessionStorage。

## 5. TDD acceptance matrix

| Requirement/risk | First failing test | Layer | Allow | Deny/failure |
|---|---|---|---|---|
| JWT issue/verify | login handler/service tests | unit/HTTP | correct username/password gets 30-day HS256 JWT | malformed protocol 400；unknown/wrong/disabled 401；wrong alg/signature/time denied |
| Stateless claims | JWT claims test | unit | username/iat/exp only | sub/jti/authVersion/role/scope absent |
| Plane isolation | authenticator contract tests | unit/HTTP/Git | JWT management；PAT Git/subscription | password outside login；PAT management；JWT Git/distribution denied |
| PAT persistence | clean migration + constraint tests | DB | preset/plaintext/HMAC/metadata persisted | unknown preset、duplicate HMAC、invalid expiry denied |
| PAT pagination | repository/HTTP tests | DB/HTTP | page/size/exact total, stable `created_at DESC,id DESC` | invalid bounds denied；cross-owner rows absent |
| PAT reveal | service/HTTP tests | unit/HTTP | owner JWT + password reveals active/inactive PAT metadata and secret | wrong password 401；non-owner/not-found hidden |
| PAT revoke | service/DB tests | unit/DB | active becomes revoked；repeat revoke 204 | revoked PAT authentication denied |
| Secret non-leakage | DTO/log tests | unit/HTTP | create/reveal explicitly contain token | list/error/log never contains password or PAT |
| Request ID | error response tests | HTTP | framework ULID in error header/body | success/204 need not include request ID |

涉及 credential 的实现必须运行 `go test -race ./...`。PostgreSQL suite 未配置 `MARKETPLACE_TEST_POSTGRES_DSN` 时必须报告 skip。

## 6. Agent ownership 与 commit slices

| Slice | Exclusive concern | Tests before commit |
|---|---|---|
| Identity persistence | PAT target record/preset/repository queries and migration | identity unit + PostgreSQL conditional suite |
| JWT/auth contracts | config, login issuer/verifier, plane-specific authenticators | auth/identity/Git deny tests + race |
| Management handlers | five routes, DTOs, page envelope, reveal/revoke | focused HTTP tests + OpenAPI conformance |
| Frontend login/PAT UI | handwritten client, login state, PAT management/reveal | npm check/test/build + Go embed tests |
| Integration owner | DI wiring, proposed→deployed OpenAPI and authority docs after implementation | full operations gates |

共享 wiring/OpenAPI 文件由 integration owner 串行修改。每个 implementation commit 必须单独批准、可构建、可测试、不可 amend。

## 7. Approval 与 implementation record

- **Design approval:** 2026-08-12 AskUser 逐项确认，用户选择直接 `approved`
- **Implementation status:** `implemented`
- **Evidence:** `domain/identity/{model,dao,service}`、`handler/identity`、backend route wiring、identity allow/deny tests 与 deployed OpenAPI conformance tests
- **Compatibility boundary:** legacy scope/HMAC-only PAT schema 启动拒绝；不自动 backfill、drop 或双读
- **Remaining hardening:** audit events、operator secret rotation、non-development legacy migration plan；broader Plugin/Marketplace/team management API 仍不属于本 slice
- **Verification boundary:** PostgreSQL/SQLite/race/frontend 是否通过必须引用实际执行报告，不由 `implemented` 状态推定；MarketplaceServer 运行时不支持 MySQL
