# AI / Agent 开发工作流

本文说明 Agent 在 MarketplaceServer 中如何选择上下文、确定修改归属并控制变更范围。通用 jframe scaffolding 不是本项目的产品架构；根 [CLAUDE.md](../CLAUDE.md) 的五模块规则优先。

## 开始前

1. 读 [CLAUDE.md](../CLAUDE.md) 获取索引和不可违反规则。
2. 读 [当前实现状态](current-state.md)，确认目标能力是已实现、基础能力还是规划中。
3. 按修改类型读取：
   - wiring/DI/lifecycle： [架构](architecture.md) + [DI 参考](di-reference.md)；
   - identity/auth/publication/data： [产品不变量](product-invariants.md)；
   - HTTP/Git/distribution： [协议](protocols.md)；
   - build/deployment/testing： [运维](operations.md)。
4. 再读取目标源码与 tests；设计文档不能代替当前代码证据。

## 先决定修改归属

MarketplaceServer 的顶层模块固定为 `jin`、`sql`、`git`、`backend`、`frontend`：

- 业务模型、service、authorization、管理 API：`backend` 内部领域；
- repository filesystem、Git subprocess/protocol、immutable projection：`git`；
- 静态 Svelte app、embedded serving、SPA fallback：`frontend`；
- HTTP/SQL lifecycle 基础设施：分别在 `jin`、`sql`。

普通 feature 不新增 kernel module。新增顶层 module 只适用于明确批准的运行时架构变更，并需要 module-list tests、architecture、DI 和 config 同步。

## 推荐流程

### 1. 调查

- 用代码、route registration、migration 和 tests 核实当前状态；
- 检查已有 contract/service，优先复用而不是创建平行抽象；
- 识别 tenant、authorization、secret、Git path 和 immutable publication 边界；
- 对照 roadmap，但不要创建空 package 或 future endpoint 冒充进度。

### 2. 设计

非平凡修改先给出可执行计划：

- 修改属于哪个现有模块/内部领域；
- lifecycle 中何时 Map/Load；
- 是否需要新增窄跨模块 contract；
- DB、Git filesystem 和 publication 是否涉及跨系统一致性；
- allow/deny、failure、real-client 与 migration tests；
- 哪些文档是该事实的唯一权威位置。

仅在确实设计新的 jframe 模块或改变模块拓扑时使用 `jframe-module-design`；普通 backend domain 工作不要套用“新 feature = 新 module”。在现有模块内部实现时可使用 `jframe-module-dev`，但必须以本仓库 architecture/invariants 为准。

### 3. 实现

- 先读后改，不跨模块访问内部 DAO/model；
- handler 只处理协议，service 负责授权和业务 transaction；
- 所有 `hub.Load` 检查 error，Config 保持双 tag；
- Git 命令使用 context-bound subprocess 与独立参数；
- 高风险状态变化、tenant scope 和 secret handling 同步写 deny tests；
- 不手工编辑 frontend dist，不顺手重构无关 jframe 源码。

### 4. 验证

按 [运维、测试与交付](operations.md) 运行适用检查，并明确报告：

- PostgreSQL suites 是否因缺少 DSN skip；
- frontend 是否实际重建；
- Git 是否使用真实 client 验证；
- 规划中的 SSH/team/audit tests 是否尚不适用。

### 5. 更新文档

- 当前能力变化：`current-state.md`；
- 模块/lifecycle/contract：`architecture.md` 与 `di-reference.md`；
- 领域或安全不变量：`product-invariants.md`；
- route/protocol/cache/schema：`protocols.md`；
- build/deploy/test：`operations.md`；
- 未来交付顺序：`roadmap.md`；
- operator config：`config.example.yaml`。

不要把同一清单复制到多个文档。

## DI 使用

先查 [DI 参考](di-reference.md)，确认 producer、consumer 和可用阶段。只有表中信息不足时再读对应模块 `mod.go`。

```go
var db *gorm.DB
if err := hub.Load(&db); err != nil {
    return fmt.Errorf("load database: %w", err)
}
```

不要在业务领域重复创建已有 connection/client。跨模块能力使用窄 contract；backend 内部 service 默认不进入全局 DI。

## jin 提醒

MarketplaceServer 使用 `github.com/juanjiTech/jin`，不是 gin：

- handler 可由 DI 注入参数；
- binding 使用 jin middleware；
- JSON 响应使用 `c.Render(..., render.JSON{Data: ...})`；
- 最终资源授权不因 handler 或 frontend 已检查而省略。

## 可复制约束

```text
- 先读 CLAUDE.md 与 docs/current-state.md，再核对目标源码和 tests
- 普通业务能力进入 backend domain、git 或 frontend，不新增顶层 kernel.Module
- 跨模块只用窄 contract/DI，不访问内部 DAO/model
- 所有 hub.Load 检查 error；Config 双 tag；Stop defer wg.Done()
- namespace query 与 authorization 默认拒绝，必须有 deny tests
- Git 不经 shell，路径限制在 storage root
- published version/revision/projection 不可变，distribution handler 只读
- secret 不进入日志、URL、前端 bundle 或示例配置
- 只更新事实对应的唯一权威文档，不把 roadmap 写成当前能力
```
