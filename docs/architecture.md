# 系统架构

本文维护 MarketplaceServer 的稳定模块边界与装配规则。当前交付范围见 [当前实现状态](current-state.md)，跨模块类型明细见 [DI 参考](di-reference.md)。

## 五模块拓扑

MarketplaceServer 只运行以下五个顶层 `kernel.Module`：

```text
cmux ──→ jin ───────────────┬────────────→ frontend
                            │
sql ─────────→ git ─────────┴────────────→ backend
                 │                          │
                 └──── narrow contracts ───┘
```

| 模块 | 所有权 |
|---|---|
| `jin` | 创建和提供 `*jin.Engine`，在共享 listener 上运行 HTTP server |
| `sql` | 创建、校验和关闭 GORM SQL backend |
| `git` | repository 文件系统、Git subprocess、Smart HTTP、immutable projection storage |
| `backend` | identity、authorization、distribution 以及未来的 teams、Plugin、version、Marketplace、audit 领域 |
| `frontend` | Svelte/Tailwind 静态产物嵌入、静态响应与 SPA fallback |

权威注册文件是 `cmd/server/modList/list.go`。普通产品能力应进入现有模块；增加顶层模块属于显式架构变更，必须同步模块清单测试、DI 文档和本架构文档。

## 生命周期

接口定义在 `core/kernel/module.go`，执行由 `core/kernel/kernel.go` 负责：

```text
Config → PreInit → Init → PostInit → Load → Start
                                            ↓
                                           Stop
```

- `Config`：返回模块配置指针；无配置返回 `nil`。
- `PreInit`：创建最底层资源并 Map 到 DI。
- `Init`：校验基础依赖，创建本模块 contract。
- `PostInit`：装配跨模块与跨领域依赖。
- `Load`：注册 HTTP/Git 路由和最终 wiring。
- `Start`：运行长驻任务；每个模块由内核放入独立 goroutine。
- `Stop`：优雅停止；自定义实现第一行必须 `defer wg.Done()`。

当前内核的 assembly 阶段按模块注册顺序确定执行，不再存在旧版 map 无序问题。shutdown 先停止首个 `jin` 模块以停止入口，再逆序停止 `frontend`、`backend`、`git`、`sql`。

确定顺序不等于可以任意制造同阶段耦合。生产者应尽量在较早阶段 Map，消费者在后续阶段 Load；复杂依赖优先通过窄 contract 和分层降低，而不是依靠隐蔽的阶段副作用。

所有模块应嵌入 `kernel.UnimplementedModule` 并添加编译期断言：

```go
var _ kernel.Module = (*Mod)(nil)
```

## DI 与 contract

`Hub` 封装 `inject.Injector` 和模块命名空间日志：

- `hub.Map(&value)` 按项目约定注册类型；
- `hub.Load(&variable)` 获取依赖，必须检查 error；
- 同一具体类型只能有一个值，多实例使用有语义的 wrapper；
- 新增或变更跨模块类型时同步更新 [DI 参考](di-reference.md)。

跨模块 contract 放在稳定、无实现依赖的 `pkg/<domain>service` 等 package 中，只暴露调用方真正需要的 interface、value object 与 DTO。禁止暴露：

- GORM model 或 DAO；
- jin context 或具体 handler；
- 另一个模块的具体 service；
- 未校验的命令字符串或文件系统路径。

Git stream contract 只接受已解析的 operation 和 opaque repository/projection identity。需要与业务数据库事务原子写入的 audit/outbox 由事务拥有者完成，不通过一个无法保证原子性的万能 writer 伪装。

## backend 内部分层

`backend.Mod` 是唯一 backend kernel module，统一完成 migration、领域 service 装配、跨模块 contract Map 与 `/api/v1`、Marketplace JSON route 注册。

推荐数据流：

```text
handler → domain service → repository/adapter → model
```

- handler：协议输入输出、认证上下文、状态码和稳定错误结构；
- service：授权、业务规则、状态转换和事务边界；
- repository/adapter：GORM 或外部 contract 调用；
- model：持久化模型和最小领域 value，不直接作为 API DTO。

当前 identity 领域按 `domain/identity/model`、`domain/identity/dao`、`domain/identity/service` 拆分，management HTTP 位于 `handler/identity`。这是 backend 内部分层；不会改变五模块拓扑，也不把 GORM record/DAO 映射到全局 DI。

handler 不直接执行 GORM 查询。backend 内部领域对象默认不 Map 到全局 DI；只有 `git`、`frontend` 等顶层模块确实消费的窄 contract 才能共享。

## 请求平面

三个公开请求平面必须保持分离：

- `/api/v1`：管理和控制 API；
- `/git`：用户开发仓库的 Git Smart HTTP；
- `/distribution`：Claude Code 安装和不可变分发。

它们可以复用 principal、authorizer 和资源语义，但不能共享 handler，也不能根据 User-Agent 或其他可伪造 header 推断信任。详细协议约束见 [协议](protocols.md)。

## Git 与 backend 边界

`git` 所有：

- storage root 和路径 containment；
- bare repository 与 projection filesystem；
- `exec.CommandContext` Git subprocess；
- Smart HTTP protocol adapter；
- immutable projection build/read primitives。

`backend` 所有：

- namespace/resource resolution；
- identity 与 authorization；
- repository、Plugin、version、Marketplace 元数据；
- publication orchestration 与数据库状态；
- 管理 API。

URL slug 先由 backend resolver 转为 opaque identity；Git 模块不能用用户 slug 拼接物理路径。backend 不直接执行 Git 命令或访问 Git 模块内部存储实现。

## frontend 架构

生产 frontend 是 `mod/frontend/web` 的 Svelte/Tailwind 静态构建，输出到 `mod/frontend/dist`，由 `mod/frontend` 通过 `embed.FS` 编译进 Go binary。生产运行时不依赖 Node SSR。

`frontend` 是唯一可以设置全局 `jin.Engine.NoRoute` 的模块。fallback 必须：

- 只处理 `GET` 与 `HEAD`；
- 只在 base path 内工作；
- 缺失且没有扩展名的前端导航才返回 `index.html`；
- 缺失 `.js`、`.css` 等 asset 返回 404；
- `/api`、`/gapi`、`/git`、`/distribution`、`/marketplaces`、`/healthz`、`/debug`、`/metrics` 不进入 SPA；
- 为 `index.html` 使用 `no-cache`，为带 hash asset 使用长期 immutable cache；
- 设置 MIME、ETag、`nosniff` 并支持 HEAD。

不得把 server secret、数据库凭据、Git credential 或私有 runtime config 编译进前端 bundle。浏览器按钮可见性只优化体验，不能代替服务端授权。

## 配置约束

模块 Config 字段必须同时带 `yaml` 与 `mapstructure` tag。支持的 operator 配置键只在 [`config.example.yaml`](../config.example.yaml) 中维护完整清单；设计文档不复制第二份配置表。

示例配置可以列出空 secret key 以公开受支持的配置形状，但不得包含真实或可误用的 secret value；`backend.jwtSecret` 示例必须为空，并注明生产优先使用 `MARKETPLACE_JWT_SECRET`。尚未实现的 SSH listener、外部 base URL 或可调配额不能提前伪装成已支持配置。

## jin 兼容说明

HTTP framework 是 `github.com/juanjiTech/jin`，不是 gin：

- handler 支持 DI 注入的任意函数签名；
- JSON/Query binding 使用 jin middleware；
- 响应使用 `c.Render(..., render.JSON{Data: ...})`，没有 `c.JSON` 快捷方法。

外部输入在 handler/protocol boundary 校验，资源归属和授权仍由领域 service 或共享 authorizer 最终判定。
