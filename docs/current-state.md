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
| HTTP 与 SQL 基础设施 | 已实现 | jin HTTP engine；PostgreSQL 生产与 SQLite 单进程开发/测试 GORM 生命周期；其他 driver 拒绝启动 | `mod/jin/`, `mod/sql/` |
| Identity | 已实现 | 用户、个人/Team namespace、系统组、Team membership/invitation、可选 public registration、系统管理员 user lifecycle、password login、固定 30 天 HS256 management JWT 与请求平面专用认证 | `mod/backend/domain/identity/{model,dao,service}/`, `mod/backend/handler/identity/` |
| Personal Access Token | 已实现 | 创建、page/size/total 列表、owner password-confirmed reveal、幂等撤销、三档 preset、状态与可选过期时间 | `mod/backend/domain/identity/service/token_service.go`, `mod/backend/handler/identity/tokens.go` |
| Authorization | 已实现 | 当前用户、组、namespace、repository、Plugin、Marketplace 资源的 action policy | `mod/backend/domain/authorization/` |
| 开发 Git Smart HTTP | 已实现 | Plugin-backed advertise/upload-pack；receive-pack 使用受管 hook、quarantine 内严格 source validation、expected-old ref transaction 与 durable coordinator，任一保护检查失败拒绝整次 push | `mod/git/{mod,service,receive}.go`, `mod/git/receive_test.go` |
| 不可变 Git 投影 | 已实现 | Marketplace/Plugin projection builder、artifact/pointer authority 与只读 reader contract | `pkg/gitservice/`, `mod/git/service.go`, `mod/backend/domain/plugin/receive/` |
| Public distribution | 已实现 | Marketplace public key、Plugin distribution UUID、只读 advertise/upload-pack、Marketplace JSON；解析只信任 ready projection pointer/artifact chain | `mod/git/distribution_http.go`, `mod/backend/domain/distribution/gorm_repository.go` |
| 用户动态 Marketplace JSON | 已实现 | BasicAuth 后按当前可读 Plugin 生成用户私有索引，响应 `private, no-store` | `mod/backend/handler/distribution/user_marketplace_json.go` |
| Plugin lifecycle | 已实现 | shared-ID Plugin/hidden Repository、Git-first provisioning/补偿、tenant-scoped create/list/get、archive/restore/visibility | `mod/backend/domain/plugin/`, `mod/backend/handler/plugin/` |
| Plugin Version lifecycle | 已实现 | canonical tag publish/list/get、default version、deleted tombstone、manifest snapshot/digest 与 tag move projection 协调 | `mod/backend/domain/plugin/service.go`, `mod/backend/domain/plugin/receive/` |
| Marketplace authoring | 规划中 | 尚无完整 draft、publish、rollback 管理 API；现有 publication service 不是完整 authoring API | `roadmap.md` |
| Team lifecycle 与角色矩阵 | 已实现 | Team namespace、固定五角色、member/invitation lifecycle 及对 Plugin/Git policy 的即时授权；没有 append-only audit query API | `mod/backend/domain/identity/service/team_service.go`, `mod/backend/domain/authorization/` |
| SSH Git | 规划中 | 当前没有 SSH listener、公钥认证或 transport wiring | `roadmap.md` |
| Identity 管理前端 | 已实现 | login/session、registration、当前用户/PAT、管理员用户以及 Team/invitation management UI；静态嵌入和 SPA boundary 保持不变 | `mod/frontend/web/src/`, `mod/frontend/` |
| Plugin 管理前端 | 已实现 | namespace-scoped list 与独立 `/plugins/new` 创建页；仓库式 owner/Plugin 标题、Code/Versions/Settings tabs、Code 文件表格与 About、真实最近提交、Clone 菜单；branch/tag 选择通过 `ref` query 保留，UTF-8 text blob 含行号与路径面包屑；保留版本/default、visibility、archive/restore 和 path commit history 浏览 | `mod/frontend/web/src/{PluginsPage,PluginPage}.svelte` |
| 完整管理前端 | 规划中 | first-run setup、通用 namespace、Marketplace、credential、audit 等管理页面尚未交付 | `roadmap.md` |

## 当前 backend 领域

`mod/backend/mod.go` 当前统一装配四个内部领域：

- `domain/identity`：按 `model`、`dao`、`service` package 隔离用户、Team namespace/membership/invitation、持久化 record、GORM 查询/迁移与业务/认证逻辑；HTTP 协议在 `handler/identity`；
- `domain/authorization`：实现 management、Git 与 distribution 共用的 action policy；
- `domain/plugin`：实现 shared-ID Plugin/hidden Repository 聚合、Version、受保护 receive 协调与可调用 recovery；HTTP 协议在 `handler/plugin`；
- `domain/distribution`：只解析 ready projection pointer/artifact chain 和用户动态索引。

这些领域都是同一 backend 内部的分层，不是新增 kernel module，也不通过全局 DI 暴露 DAO 或 GORM model。Team lifecycle 归属 identity；append-only audit 仍是未来 backend 内部领域。Marketplace persistence/publication 基础已存在于 distribution/plugin 领域，完整 authoring API 尚未交付。

## 当前路由

本节是唯一当前 route inventory，来自现有 `Register` 和模块 `Load` 调用；路径参数的 `.git` 后缀由 Git handler 严格校验。请求平面规则见 [协议](protocols.md)，frontend reserved-path 规则见 [系统架构](architecture.md#frontend-架构)。

### 管理 API

| Method | Path | 认证 |
|---|---|---|
| `POST` | `/api/v1/auth/login` | 无；body 为账号 username/password |
| `GET` | `/api/v1/auth/capabilities` | 无；返回 `registrationEnabled` |
| `POST` | `/api/v1/auth/register` | 无；仅 operator 启用 registration 时注册，否则 route 不存在 |
| `GET` | `/api/v1/me` | Bearer JWT |
| `GET` | `/api/v1/admin/users` | Bearer JWT + system-admin |
| `POST` | `/api/v1/admin/users` | Bearer JWT + system-admin |
| `GET` | `/api/v1/admin/users/:userId` | Bearer JWT + system-admin |
| `PATCH` | `/api/v1/admin/users/:userId` | Bearer JWT + system-admin |
| `POST` | `/api/v1/admin/users/:userId:disable` | Bearer JWT + system-admin |
| `POST` | `/api/v1/admin/users/:userId:enable` | Bearer JWT + system-admin |
| `PUT` | `/api/v1/admin/users/:userId/system-admin` | Bearer JWT + system-admin |
| `DELETE` | `/api/v1/admin/users/:userId/system-admin` | Bearer JWT + system-admin |
| `GET` | `/api/v1/teams` | Bearer JWT；`scope=all` 仅 system-admin |
| `POST` | `/api/v1/teams` | Bearer JWT |
| `GET` | `/api/v1/teams/:team` | Bearer JWT + membership/system-admin |
| `PATCH` | `/api/v1/teams/:team` | Bearer JWT + owner/admin/system-admin |
| `GET` | `/api/v1/teams/:team/members` | Bearer JWT + membership/system-admin |
| `PUT` | `/api/v1/teams/:team/members/:userId` | Bearer JWT + role-management authority |
| `DELETE` | `/api/v1/teams/:team/members/:userId` | Bearer JWT + role-management authority |
| `GET` | `/api/v1/teams/:team/invitations` | Bearer JWT + owner/admin/system-admin |
| `POST` | `/api/v1/teams/:team/invitations` | Bearer JWT + role-management authority |
| `DELETE` | `/api/v1/teams/:team/invitations/:invitationId` | Bearer JWT + owner/admin/system-admin |
| `POST` | `/api/v1/teams/:team/invitations/:invitationId:reissue` | Bearer JWT + owner/admin/system-admin |
| `GET` | `/api/v1/me/team-invitations` | Bearer JWT |
| `POST` | `/api/v1/me/team-invitations/:invitationId:accept` | Bearer JWT + target identity |
| `POST` | `/api/v1/me/team-invitations/:invitationId:reject` | Bearer JWT + target identity |
| `GET` | `/api/v1/health` | 无 |
| `GET` | `/api/v1/me/tokens` | Bearer JWT |
| `POST` | `/api/v1/me/tokens` | Bearer JWT |
| `DELETE` | `/api/v1/me/tokens/:tokenId` | Bearer JWT |
| `POST` | `/api/v1/me/tokens/:tokenId/reveal` | Bearer JWT + body 中的当前账号 password |
| `GET` | `/api/v1/namespaces/:namespace/plugins` | Bearer JWT + `plugin.list` |
| `POST` | `/api/v1/namespaces/:namespace/plugins` | Bearer JWT + `plugin.create` |
| `GET` | `/api/v1/namespaces/:namespace/plugins/:plugin` | Bearer JWT + `plugin.read` |
| `POST` | `/api/v1/namespaces/:namespace/plugins/:plugin:archive` | Bearer JWT + `plugin.archive` |
| `POST` | `/api/v1/namespaces/:namespace/plugins/:plugin:restore` | Bearer JWT + `plugin.archive` |
| `POST` | `/api/v1/namespaces/:namespace/plugins/:plugin:set-visibility` | Bearer JWT + `plugin.archive` |
| `GET` | `/api/v1/namespaces/:namespace/plugins/:plugin/versions` | Bearer JWT + `plugin.read` |
| `GET` | `/api/v1/namespaces/:namespace/plugins/:plugin/versions/:tag` | Bearer JWT + `plugin.read`；deleted tombstone 返回 `410` |
| `POST` | `/api/v1/namespaces/:namespace/plugins/:plugin/versions:publish` | Bearer JWT + `plugin.publish` |
| `POST` | `/api/v1/namespaces/:namespace/plugins/:plugin/versions/:tag:set-default` | Bearer JWT + `plugin.publish` |
| `DELETE` | `/api/v1/namespaces/:namespace/plugins/:plugin/default-version` | Bearer JWT + `plugin.publish` |
| `GET` | `/api/v1/namespaces/:namespace/plugins/:plugin/repository/refs` | Bearer JWT + `plugin.read` |
| `GET` | `/api/v1/namespaces/:namespace/plugins/:plugin/repository/tree` | Bearer JWT + `plugin.read`；query 为 `ref` 与可选 `path` |
| `GET` | `/api/v1/namespaces/:namespace/plugins/:plugin/repository/blob` | Bearer JWT + `plugin.read`；仅不超过 1 MiB 的 UTF-8 text blob |
| `GET` | `/api/v1/namespaces/:namespace/plugins/:plugin/repository/commits` | Bearer JWT + `plugin.read`；支持可选 path 与 page/size |

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

当前没有 distribution receive-pack、SSH route、Marketplace authoring API 或本文之外的完整管理 API。

## 当前持久化基础

`mod/backend/migrate.go` 按顺序装配 identity、Plugin lifecycle 与 distribution migrations。

Identity 当前迁移：

- users
- personal and Team namespaces
- system groups
- user/group memberships
- Team memberships and Team invitations
- personal access tokens、三档 cumulative preset、repeatably revealable plaintext 与 HMAC lookup index

Plugin 与 distribution 当前迁移：

- shared-ID repositories 与 Plugins
- Plugin versions 与 version histories
- repository orphan cleanup records
- durable receive batches 与 intents
- Marketplace templates、revisions 与 revision items
- projection artifacts、revision pointers、pointer transitions 与 GC jobs
- Marketplace distributions
- Plugin distributions

Identity 字段以 `mod/backend/domain/identity/model/` 为准，查询与 migration/legacy guard 以 `mod/backend/domain/identity/dao/` 为准；其他领域仍以各自 model/migrate 源码为准。当前没有 audit、outbox、job、session 或 SSH key migration。

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
- 没有 append-only audit storage/query API；当前 lifecycle mutation 仅产生结构化安全日志。
- Plugin 不提供独立 Repository CRUD 或物理删除；这是 shared-ID hidden Repository 产品边界，不是缺失的独立资源 API。
- 没有完整 Marketplace draft/publish/rollback 管理 API；receive/orphan/projection recovery 目前是可调用基础能力，尚无常驻 worker 调度。
- frontend 已交付 login、registration、当前用户 PAT、管理员用户、Team/invitation 和 Plugin 项目管理；first-run setup、通用 namespace、Marketplace、credential 与 audit 页面仍未实现，不等于完整管理 UI。
- PostgreSQL-backed 测试需要 `MARKETPLACE_TEST_POSTGRES_DSN`；未设置时会显式跳过。

## 相关文档

- [架构](architecture.md)
- [产品不变量](product-invariants.md)
- [协议](protocols.md)
- [运维与验证](operations.md)
- [路线图](roadmap.md)

Plugin source inspection now uses native `mod/git` [MarketplaceServer Plugin Profile v1](design/systems/02-plugin-lifecycle/git/api-contract.md#marketplaceserver-plugin-profile-v1--normative-source-validation): skills-only admission with strict manifest/frontmatter validation, without a Claude CLI runtime dependency. Manifest snapshot/digest and receive/backend contracts remain unchanged. Real-client acceptance confirms fresh incoming objects are visible during protected validation and invalid multi-ref pushes leave all refs unchanged; rejected objects may remain unreachable rather than being immediately removed.
