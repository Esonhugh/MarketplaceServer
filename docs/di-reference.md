# DI 参考

本文档维护 MarketplaceServer 当前五模块运行时中可通过 jFrame DI 容器共享的类型。当前运行时模块固定为 `jin`、`sql`、`git`、`backend`、`frontend`。

## 基本用法

```go
hub.Map(&db)              // 注册（传指针）
var db *gorm.DB
hub.Load(&db)             // 获取（须检查 error）
hub.Invoke(func(db *gorm.DB) { ... })
```

**规则摘要：**

- `hub.Map` 按项目现有约定传入变量地址，例如 `hub.Map(&db)`。
- `hub.Load` 必须检查 error；缺失依赖应使模块启动失败。
- 同一具体类型只能 Map 一个值；多个同类依赖必须使用有语义的 wrapper type。
- 业务模块只通过稳定 contract 共享跨模块能力，不直接导入其他模块的 DAO/model。

## 当前共享类型一览

| Load 变量类型 | Map 来源 | 可用阶段 | 当前消费者 |
|---|---|---|---|
| `net.Listener` | 内核 `cmd/server/server.go` 创建 TCP listener 后 Map | PreInit 起 | 预留给需要底层 listener 的基础设施 |
| `cmux.CMux` | 内核 `cmd/server/server.go` 基于 listener 创建并 Map | PreInit 起 | `mod/jin` 在 `Start()` 中加载，用于 HTTP listener 匹配 |
| `*jin.Engine` (`github.com/juanjiTech/jin`) | `mod/jin` 在 `PreInit()` 创建并 Map | Init 起 | `mod/git` 注册 Smart HTTP；`mod/backend` 注册 `/api/v1`；`mod/frontend` 注册静态资源与 SPA fallback |
| `*gorm.DB` | `mod/sql` 在 `PreInit()` 打开数据库并 Map | Init 起 | `mod/backend` 在 `PostInit()` 组装服务 |
| `gitservice.RepositoryService` (`github.com/Esonhugh/MarketplaceServer/pkg/gitservice`) | `mod/git` 在 `Init()` 创建 Git filesystem/process contract 并 Map | PostInit 起 | `mod/backend` 在 `PostInit()` 组装服务；方法只接受已解析并严格校验的不透明 repository ID/storage key |
| `gitservice.RepositoryProvisioner` | `mod/git` 在 `Init()` Map Git-first bare repository provisioning 与受管 receive hook 安装能力 | PostInit 起 | `mod/backend` Plugin create；返回 receipt 用于精确补偿，不暴露物理路径 |
| `gitservice.RepositoryOrphanCleaner` | `mod/git` 在 `Init()` Map receipt-bound orphan cleanup | PostInit 起 | `mod/backend` Plugin recovery；只清理由持久化 cleanup record 指定的 provision receipt |
| `gitservice.PluginSourceInspector` | `mod/git` 在 `Init()` Map strict source inspection | PostInit 起 | `mod/backend` Version publish；解析 raw tag/peeled commit，在隔离目录验证 manifest 并返回 snapshot/digest |
| `gitservice.ProjectionBuilder` | `mod/git` 在 `Init()` Map 发布侧 immutable artifact builder | PostInit 起 | `mod/backend` publication 与 protected tag-move prebuild；distribution 请求 handler 不得加载此 contract |
| `gitservice.ProjectionRefReader` | `mod/git` 在 `Init()` Map projection ref 只读检查能力 | PostInit 起 | `mod/backend` receive recovery 判断 artifact/ref 实际状态；不允许移动 ref |
| `gitservice.DistributionReader` | `mod/git` 在 `Init()` Map 只读投影视图 | PostInit 起 | distribution Git handlers；仅支持 advertise/upload-pack，不包含 receive-pack、仓库初始化或投影构建 |
| `gitservice.RepositoryResolver` (`github.com/Esonhugh/MarketplaceServer/pkg/gitservice`) | `mod/backend` 在 `PostInit()` Map；通过 namespace relation 查询当前 Plugin/hidden Repository metadata | Load 起 | `mod/git` 在 `Load()` 获取，先把 URL namespace/repository slug 解析为不含 GORM model 的 `Repository{ID, NamespaceID, OwnerUserID, Slug, Visibility, Status}`，再调用 Git service |
| `gitservice.ReceiveCoordinator` | `mod/backend` 在 `PostInit()` Map durable protected-receive coordinator | Load 起 | `mod/git` 在 `Load()` 必需加载；effectful canonical-tag transitions 执行 durable prepare、projection prebuild 与 observed-ref resolution |
| `distributionservice.Resolver` (`github.com/Esonhugh/MarketplaceServer/pkg/distributionservice`) | `mod/backend` 在 `PostInit()` Map | Load 起 | `mod/git` Public distribution routes；`ResolveMarketplace` 按持久化且不可变的 `{normalized-marketplace-name}-{8-lowercase-hex}` public key 解析，`ResolvePlugin` 按 distribution UUID 解析；每个请求只解析一次当前不可变 projection grant |
| `auth.GitPATAuthenticator` (`github.com/Esonhugh/MarketplaceServer/pkg/auth`) | `mod/backend` identity service 在 `PostInit()` Map | Load 起 | `mod/git` Smart HTTP；只认证符合 operation preset 的 username+PAT，不接受账号 password 或 management JWT |
| `auth.Authorizer` (`github.com/Esonhugh/MarketplaceServer/pkg/auth`) | `mod/backend` authorization policy 在 `PostInit()` Map | Load 起 | `mod/git` 对已解析 Plugin/repository resource 执行最终 read/write 授权 |

> **Load 示例：** `mod/sql` Map 的是 `&db`（其中 `db` 类型为 `*gorm.DB`），消费者写 `var db *gorm.DB; err := hub.Load(&db)`。

## 五模块 DI 边界

- `jin`：消费 `cmux.CMux`；提供 `*jin.Engine`。
- `sql`：提供 `*gorm.DB`。
- `git`：在 `Init()` 提供 `gitservice.RepositoryService`、`RepositoryProvisioner`、`RepositoryOrphanCleaner`、`PluginSourceInspector`、`ProjectionBuilder`、`ProjectionRefReader` 与 `DistributionReader`；在 `Load()` 消费 `*jin.Engine`、`gitservice.RepositoryResolver`、`gitservice.ReceiveCoordinator`、`distributionservice.Resolver`、`auth.GitPATAuthenticator` 与 `auth.Authorizer`，注册 Git Smart HTTP 和 Public distribution routes。物理路径固定由 opaque ID/storage key 计算，不接受 URL slug。Marketplace public route 只接受 public key，不提供旧 UUID route；Plugin distribution route 仍接受 UUID。
- `backend`：在 `PostInit()` 消费 `*jin.Engine`、`*gorm.DB` 及上述 Git-side contracts，组装 shared-ID Plugin lifecycle、Version、receive coordination 与 recovery，并提供窄 `gitservice.RepositoryResolver`、`gitservice.ReceiveCoordinator`、`distributionservice.Resolver`、`auth.GitPATAuthenticator` 与 `auth.Authorizer` contract；projection resolver 按 Marketplace public key 或 Plugin distribution UUID 定位 ready pointer/artifact chain。Identity/Plugin 的 record、DAO、具体 service、management JWT authenticator 与 PAT management service 保持 backend 内部对象，不向全局 DI 暴露。
- `frontend`：消费 `*jin.Engine`；只注册嵌入式静态资源和 `NoRoute` fallback，不提供共享 DI 类型。

旧的 `jinx`、`myDB`、`pgsql`、`rds`、`grpcGateway`、`b2x`、`pyroscope`、`uptrace`、`jinPprof` 等模块不在 MarketplaceServer 当前运行时模块清单内；不要在新业务代码中依赖它们提供的 DI 类型。

## 何时读基础设施模块源码

**优先查本表** — 多数业务开发只需知道类型与可用阶段，在约定生命周期阶段 `hub.Load` 即可。

**仍不确定时** — 可以阅读对应模块（如 `mod/jin/`、`mod/sql/`、`mod/git/`）的 `mod.go`，确认 Map 时机、配置项或边界行为。

**不要**在业务模块里重复创建已有基础设施提供的连接（例如再 `gorm.Open` 一次），应 Load 容器内已有实例。

## 延伸阅读

- [当前实现状态](current-state.md) — 当前模块、领域与路由
- [系统架构](architecture.md) — 五模块边界与生命周期
- [使用指南](usage.md) — CLI、配置与本地开发
- [AI 开发指南](ai-development.md) — Agent 工作流
- [CLAUDE.md](../CLAUDE.md) — Agent 索引与不可违反规则
