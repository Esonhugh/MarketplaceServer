# 当前实现状态

本文只汇总已经接入 MarketplaceServer 五模块运行时的能力。设计约束见 [产品不变量](product-invariants.md)，未来工作见 [路线图](roadmap.md)。

## 状态术语

- **已实现**：已经接入运行时，并有代码或测试作为依据。
- **基础能力**：模型、contract、service 或存储原语已经存在，但完整用户流程尚未交付。
- **规划中**：当前运行时没有完整实现，以路线图为准。

代码、测试、migration、路由注册和运行时配置高于本文；发现偏差时应在同一变更中更新本文。

## 运行时基础

**已实现。** `cmd/server/modList/list.go` 只注册以下模块，顺序固定：

1. `jin`
2. `sql`
3. `git`
4. `backend`
5. `frontend`

`core/kernel/kernel.go` 使用有序 `modules` slice 和名称索引 map：

- `Config`、`PreInit`、`Init`、`PostInit`、`Load` 按注册顺序执行；
- 每个模块的 `Start` 在独立 goroutine 中运行；
- shutdown 先停止首个 `jin` 模块，再按逆注册顺序停止其余模块；
- 生命周期、重复注册和停止行为由 `core/kernel/kernel_test.go` 覆盖。

旧的 `jinx`、`myDB`、`pgsql`、`grpcGateway`、`rds` 和可观测性模块仍可能保留源码，但不属于当前 MarketplaceServer 运行时。

## 当前能力矩阵

| 能力 | 状态 | 当前范围 | 主要依据 |
|---|---|---|---|
| HTTP 与 SQL 基础设施 | 已实现 | jin HTTP engine、PostgreSQL/MySQL GORM 生命周期 | `mod/jin/`, `mod/sql/` |
| Identity | 已实现 | 用户、个人 namespace、系统组、成员关系、管理员 bootstrap、password login、固定 30 天 HS256 management JWT 与请求平面专用认证 | `mod/backend/domain/identity/{model,dao,service}/`, `mod/backend/handler/identity/` |
| Personal Access Token | 已实现 | 创建、page/size/total 列表、owner password-confirmed reveal、幂等撤销、三档 preset、状态与可选过期时间 | `mod/backend/domain/identity/service/token_service.go`, `mod/backend/handler/identity/tokens.go` |
| Authorization | 已实现 | 当前用户、组、namespace、repository、Plugin、Marketplace 资源的 action policy | `mod/backend/domain/authorization/` |
| 开发 Git Smart HTTP | 已实现 | repository advertise、upload-pack、receive-pack；按 read/write action 授权 | `mod/git/mod.go`, `mod/git/production_smarthttp_integration_test.go` |
| 不可变 Git 投影 | 已实现 | Marketplace/Plugin projection builder 与只读 reader contract | `pkg/gitservice/`, `mod/git/service.go` |
| Public distribution | 已实现 | Marketplace public key、Plugin distribution UUID、只读 advertise/upload-pack、Marketplace JSON | `mod/git/distribution_http.go`, `mod/backend/handler/distribution/marketplace_json.go` |
| 用户动态 Marketplace JSON | 已实现 | BasicAuth 后按当前可读 Plugin 生成用户私有索引，响应 `private, no-store` | `mod/backend/handler/distribution/user_marketplace_json.go` |
| Plugin/version/Marketplace persistence | 基础能力 | repository、Plugin、version、revision、distribution、projection models 与 publication service | `mod/backend/domain/distribution/` |
| 完整 repository/Plugin CRUD | 规划中 | 尚无完整管理 API 与 provisioning/reconciliation 流程 | `roadmap.md` |
| 完整版本发布与 Marketplace authoring | 规划中 | 尚无完整 draft、validate、publish、rollback 管理 API | `roadmap.md` |
| 团队、角色矩阵、审计 | 规划中 | 当前没有 team lifecycle、完整 RBAC 或 append-only audit API | `roadmap.md` |
| SSH Git | 规划中 | 当前没有 SSH listener、公钥认证或 transport wiring | `roadmap.md` |
| Identity 管理前端 | 已实现 | login/session 与当前用户 PAT list/create/reveal/revoke UI；静态嵌入和 SPA boundary 保持不变 | `mod/frontend/web/src/`, `mod/frontend/` |
| 完整管理前端 | 规划中 | namespace、team、Plugin、Marketplace、audit 等完整管理页面尚未交付 | `roadmap.md` |

## 当前 backend 领域

`mod/backend/mod.go` 当前统一装配三个内部领域：

- `domain/identity`：按 `model`、`dao`、`service` package 隔离持久化 record、GORM 查询/迁移与业务/认证逻辑；HTTP 协议在 `handler/identity`；
- `domain/authorization`
- `domain/distribution`

Identity 的 model/dao/service 是同一 backend 内部领域的分层，不是新增 kernel module，也不通过全局 DI 暴露 DAO 或 GORM model。`teams`、`plugins`、`versions`、`marketplaces` 和 `audit` 可以成为未来的 backend 内部领域，但不是当前已经存在的独立 package，也不是新的 kernel module。

## 当前路由

本节是唯一当前 route inventory，来自现有 `Register` 和模块 `Load` 调用；路径参数的 `.git` 后缀由 Git handler 严格校验。请求平面规则见 [协议](protocols.md)，frontend reserved-path 规则见 [系统架构](architecture.md#frontend-架构)。

### 管理 API

| Method | Path | 认证 |
|---|---|---|
| `POST` | `/api/v1/auth/login` | 无；body 为账号 username/password |
| `GET` | `/api/v1/health` | 无 |
| `GET` | `/api/v1/me/tokens` | Bearer JWT |
| `POST` | `/api/v1/me/tokens` | Bearer JWT |
| `DELETE` | `/api/v1/me/tokens/:tokenId` | Bearer JWT |
| `POST` | `/api/v1/me/tokens/:tokenId/reveal` | Bearer JWT + body 中的当前账号 password |

### 开发 Git Smart HTTP

| Method | Path | 行为 |
|---|---|---|
| `GET` | `/git/:namespace/:repository/info/refs` | allowlist upload-pack/receive-pack，并分别检查 read/write |
| `POST` | `/git/:namespace/:repository/git-upload-pack` | repository read |
| `POST` | `/git/:namespace/:repository/git-receive-pack` | repository write |

### 分发

| Method | Path | 行为 |
|---|---|---|
| `GET` | `/distribution/marketplaces/:distribution/marketplace.json` | public key 定位公开不可变快照 |
| `GET` | `/distribution/users/:username/marketplace.json` | BasicAuth，动态生成用户可读索引 |
| `GET` | `/distribution/marketplaces/:distribution/info/refs` | 只允许 `git-upload-pack` advertisement |
| `POST` | `/distribution/marketplaces/:distribution/git-upload-pack` | 只读 Marketplace projection |
| `GET` | `/distribution/plugins/:distribution/info/refs` | 只允许 `git-upload-pack` advertisement |
| `POST` | `/distribution/plugins/:distribution/git-upload-pack` | 只读 Plugin projection |

当前没有 distribution receive-pack、SSH route 或本文之外的完整管理 API。

## 当前持久化基础

`mod/backend/migrate.go` 只装配 identity 与 distribution migrations。

Identity 当前迁移：

- users
- namespaces
- system groups
- user/group memberships
- personal access tokens、三档 cumulative preset、repeatably revealable plaintext 与 HMAC lookup index

Distribution 当前迁移：

- repositories 与 Plugins
- Plugin versions
- Marketplace templates、revisions 与 revision items
- Marketplace distributions 与 immutable projections
- Plugin distributions

Identity 字段以 `mod/backend/domain/identity/model/model.go` 为准，查询与 migration/legacy guard 以 `mod/backend/domain/identity/dao/` 为准；其他领域仍以各自 model/migrate 源码为准。当前没有 team、invitation、audit、outbox、job、session 或 SSH key migration。

## 当前配置

支持的 operator 配置以 [`config.example.yaml`](../config.example.yaml) 为准，当前模块段为：

- `jin`
- `sql`
- `git`
- `backend`
- `frontend`

示例中的 `backend.jwtSecret` 保持空值；生产使用受保护的 `MARKETPLACE_JWT_SECRET`，其优先级高于 YAML fallback。Identity bootstrap password 和 API-key pepper 也通过受保护的 runtime environment 提供，示例不包含真实 secret。

## 已知缺口

- Identity migration 发现旧 `personal_access_token_scopes` 或缺少 `preset`/`secret_plaintext`/`secret_hmac` 的旧 PAT table 时拒绝启动，不提供自动 backfill 或 destructive migration；开发环境需在确认无需保留数据后由 operator 重建数据库，非开发环境必须先备份并设计显式迁移。
- 没有 SSH Git transport。
- 没有完整 team lifecycle、五角色矩阵和审计查询。
- 没有完整 repository/Plugin CRUD、版本发布、Marketplace draft/publish/rollback API。
- frontend 已交付 login 与当前用户 PAT management，但 namespace/team/Plugin/Marketplace/audit 页面仍未实现，不等于完整管理 UI。
- PostgreSQL-backed 测试需要 `MARKETPLACE_TEST_POSTGRES_DSN`；未设置时会显式跳过。

## 相关文档

- [架构](architecture.md)
- [产品不变量](product-invariants.md)
- [协议](protocols.md)
- [运维与验证](operations.md)
- [路线图](roadmap.md)
