# MarketplaceServer Agent 开发指南

MarketplaceServer 是基于 jframe 模块化内核的 Claude Code Plugin 与 Marketplace 自托管控制、Git 托管和分发服务。本文只保留 Agent 必须始终读取的文档索引与不可违反规则；详细设计统一维护在 `docs/`。


## 文档索引

按任务选择相关文档，不要把根指南当作完整产品规格：

| 文档 | 权威职责 |
|---|---|
| [README.md](README.md) | 产品概览与快速开始 |
| [docs/current-state.md](docs/current-state.md) | 当前已实现模块、领域、路由、持久化基础与已知缺口 |
| [docs/project-goals.md](docs/project-goals.md) | 产品使命、目标与成功标准 |
| [docs/architecture.md](docs/architecture.md) | 五模块拓扑、生命周期、分层、contract 与 frontend 边界 |
| [docs/product-invariants.md](docs/product-invariants.md) | ownership、authorization、不可变发布、credential、一致性与审计规则 |
| [docs/protocols.md](docs/protocols.md) | REST、Git Smart HTTP、distribution、Marketplace JSON 与目标 SSH 协议 |
| [docs/usage.md](docs/usage.md) | 当前 CLI、配置和本地开发流程 |
| [docs/di-reference.md](docs/di-reference.md) | 当前 DI producer、consumer、类型与可用阶段 |
| [docs/operations.md](docs/operations.md) | 测试、构建、部署、存储、恢复和交付门槛 |
| [docs/roadmap.md](docs/roadmap.md) | 尚未完成的工作与交付顺序 |
| [docs/ai-development.md](docs/ai-development.md) | MarketplaceServer 的 Agent 工作流 |
| [docs/design/README.md](docs/design/README.md) | 通用数据、API contract、ADR 与后续 system design 索引 |
| [config.example.yaml](config.example.yaml) | 支持的 operator 配置键与安全示例 |

## 真相与状态

- 源码、测试、migration、路由注册与运行时配置决定什么已经实现。
- `docs/current-state.md` 是当前实现的维护摘要；发现偏差时与代码在同一变更中更新。
- architecture、invariants 和 protocols 约束当前及未来实现，但不证明 feature 已交付。
- `docs/roadmap.md` 只描述规划。不能把 target entity、状态机、API、SSH、团队或完整 UI 写成当前能力。
- 行为、配置、DI、route、schema 或 migration 变化时，同步更新其唯一权威文档，避免复制第二份清单。

## 固定运行时架构

运行时只注册以下五个顶层 `kernel.Module`，权威清单是 `cmd/server/modList/list.go`：

1. `jin`
2. `sql`
3. `git`
4. `backend`
5. `frontend`

Identity、authorization、distribution，以及未来的 teams、repositories、plugins、versions、marketplaces、audit，都是 `backend` 内部领域，不是新增 kernel module。

当前内核在 `core/kernel/kernel.go` 中按注册顺序确定执行 `Config`、`PreInit`、`Init`、`PostInit`、`Load`；各模块 `Start` 在独立 goroutine 运行；shutdown 先停止 `jin`，再逆序停止其余模块。不要恢复旧版“module map 无序”的假设。

## 不可违反规则

1. **保持五模块边界。** 普通产品能力进入拥有它的现有模块；新增顶层 module 必须是显式架构变更，并同步 module-list tests、architecture 与 DI 文档。业务逻辑不得写入 `main.go` 或 `cmd/`。
2. **使用窄 contract。** 跨模块通过稳定 interface/DTO 与 DI 通信；禁止导入另一模块的内部 DAO、GORM model、handler 或具体 service。
3. **遵守生命周期和 DI。** 所有 `hub.Load` 检查 error；同类型多实例使用语义 wrapper；Config 字段同时有 `yaml` 与 `mapstructure` tag；自定义 `Stop()` 第一行 `defer wg.Done()`。
4. **显式 tenant scope。** namespace 是资源 identity 的一部分；query、list、mutation 和唯一约束不能跨 namespace 混合或先全局查询再内存过滤。
5. **服务端默认拒绝。** 使用明确 `Principal + Action + Resource + Context` 授权；handler、Git、distribution 和 worker 都不能依赖 frontend 按钮或角色字符串推导最终权限。
6. **隔离请求平面。** `/api/v1`、`/git`、`/distribution` 使用独立 handler 与 credential scope；不得通过 User-Agent 或可伪造 header 建立信任。
7. **Git 实现留在 `git`。** 禁止 shell command 拼接；使用 `exec.CommandContext`、独立参数、最小环境、server-resolved opaque path 与 storage-root containment check。
8. **不伪装跨系统 ACID。** Git objects/refs 是 repository 内容权威，DB refs/size/latest 是 projection；DB 与 filesystem 通过显式状态、幂等 outbox/event、reconciliation 或补偿协调。
9. **保持 Plugin 与 tag authority。** Plugin 是唯一 user-facing resource，隐藏 repository 与其一对一；发布只选择 canonical `v`-prefixed SemVer tag，manifest version 不作为 identity；tag 移动更新同一 logical version，但必须先通过 protected receive validation 和所有引用 revision 的 prebuild。
10. **保持 Marketplace revision 配置可追踪。** draft 可变，published revision 保留不可修改的 Plugin+tag selection；projection artifact 可在 tag 移动时重建并切换。Git-ref/DB-pointer failure 尚未设计完成前不得实现或宣称跨系统原子性；public key 与 distribution UUID 是 locator，不是 credential。
11. **分发请求绝对只读。** distribution handler 不能 init repository、build projection、update ref、注册 receive-pack 或修改开发 repository/active projection。
12. **保护 secret。** 不存储 plaintext password、JWT signing secret、private key 或未获批准为 repeatably revealable 的 credential；获批准可重复查看的 credential 只能按 ADR-0006 使用显式 `secret_plaintext`，数据库与备份进入 credential trust boundary。任何 secret、Authorization、secret-bearing URL、pack body、server filesystem path 或 private runtime config 都不得进入日志、audit、error 或 debug output。
13. **以官方 Marketplace schema 为准。** 修改 `marketplace.json` 字段前核对当前官方 schema；不猜 source type/兼容字段；直接 URL 索引不使用 relative Plugin source，Git source 使用官方 `url` 形式和固定 SHA。
14. **frontend 只做静态嵌入。** Svelte/Tailwind 输出由 `frontend` package 的 `embed.FS` 托管，生产不运行 Node SSR；只有 `frontend` 设置全局 `NoRoute`，backend/Git/distribution/health/debug/metrics path 不能落入 SPA。
15. **安全规则必须有 deny tests。** tenant、authorization、path、credential、状态转换、publication 和 distribution 变更同时覆盖允许与拒绝路径；Git protocol 变更使用真实 client 测试。
16. **只做任务范围内的最小变更。** 不顺手重构无关 jframe 基础代码；框架缺陷先用测试证明，再做最小修复。
17. **完成前按 `docs/operations.md` 验证。** 报告 skipped database/frontend/SSH 等未执行覆盖，不能把规划测试写成已通过。

## 开发入口

- 查当前 feature 是否存在：先读 [当前实现状态](docs/current-state.md)，再核对对应源码和 tests。
- 修改 module wiring 或 contract：读 [架构](docs/architecture.md) 与 [DI 参考](docs/di-reference.md)。
- 修改 identity、authorization、publication 或 persistence：读 [产品不变量](docs/product-invariants.md)。
- 修改 HTTP、Git 或 distribution：读 [协议](docs/protocols.md)。
- 设计或修改会影响产品行为、用户/团队/企业隔离、Marketplace 组合或交付范围的能力：先读 [项目目标](docs/project-goals.md)，再读对应设计文档、源码和 tests。
- 设计未来 slice：读 [路线图](docs/roadmap.md)，但仍以当前代码为起点，不创建空模块冒充进度。
- 非平凡变更的设计批准、Agent 独占归属、TDD 和 feature commit 规则：遵循 [Agent 开发工作流](docs/ai-development.md)。
- 完成变更：按 [运维、测试与交付](docs/operations.md) 运行适用检查并同步文档。
