# CLAUDE.md — MarketplaceServer Agent 开发指南

## 项目定位

MarketplaceServer 是一个基于 jframe 模块化内核的企业级 Claude Code Marketplace 服务端，目标是为个人、团队和企业统一提供：

1. Claude Code Plugin 的独立 Git 仓库托管、CRUD 与版本管理。
2. Git Smart HTTP（HTTPS）和 SSH 两种 clone/fetch/push 模式。
3. 可组合、可发布、可通过稳定 URL 获取的多套 `marketplace.json` 索引。
4. 多用户、团队空间、成员管理、资源共享和细粒度权限控制。
5. 由 Svelte + Tailwind 构建、通过 Go `embed.FS` 嵌入单二进制的管理前端。

- **仓库：** `github.com/Esonhugh/MarketplaceServer`
- **Go module：** `github.com/Esonhugh/MarketplaceServer`
- **Go 版本：** 以 `go.mod` 为准（当前 Go 1.24）
- **入口：** `main.go` → `cmd.Execute()` → Cobra 子命令

本仓库继承 jframe 的模块系统、DI、配置、jin HTTP、GORM、Redis、gRPC Gateway 和可观测性能力，但产品语义已经变更为 MarketplaceServer。不要继续把它当作通用脚手架产品开发。

## 当前状态与目标状态

### 当前已经存在

- `core/kernel`：模块注册、生命周期和 DI 容器。
- `mod/jinx`：jin HTTP 服务，映射 `*jin.Engine`。
- `mod/grpcGateway`：gRPC 与 REST Gateway。
- `mod/myDB`、`mod/pgsql`：GORM 数据库模块；当前注册的是 `myDB`。
- `mod/rds`：Redis。
- `mod/uptrace`、`mod/pyroscope`、`mod/jinPprof`、`pkg/sentry`：可观测性和诊断。
- `pkg/auth`、`pkg/stdao`、`pkg/settings`：可复用的 JWT、DAO、动态设置基础能力。

### 目标领域模块

以下是目标边界，不代表对应目录已经存在。开发前必须检查当前代码，不得假设模块已实现。

| 模块 | 职责 | 主要依赖/输出 |
|---|---|---|
| `identity` | 用户、登录凭证、Token、SSH 公钥、服务账号 | DB；输出身份服务 |
| `teams` | 团队、成员、邀请、团队资源归属 | DB、identity；输出团队服务 |
| `authorization` | RBAC/资源级授权和策略判定 | DB、identity、teams；输出授权服务 |
| `gitServer` | bare repository 生命周期、Smart HTTP、SSH、Git 命令授权、hook 与仓库维护 | DB、authorization、文件存储；输出 Git 仓库服务 |
| `plugins` | Plugin 元数据 CRUD、仓库绑定、manifest 校验、可见性、发布状态 | DB、gitServer、authorization；输出 Plugin 服务 |
| `versions` | Git ref/tag/commit 与 Plugin 版本映射、发布、撤回和版本查询 | DB、gitServer、plugins；输出版本服务 |
| `marketplaces` | 模板/方案、Plugin 组合、版本选择、发布修订和 `marketplace.json` 渲染 | DB、plugins、versions、authorization；输出 Marketplace 服务 |
| `frontend` | Svelte + Tailwind 管理端静态资源，通过 `embed.FS` 随 Go 二进制发布 | `*jin.Engine` |
| `audit` | 记录登录、授权、成员、仓库写入、版本发布和索引发布等安全事件 | DB；供各领域服务调用 |

如果拆分后只产生跨模块样板而没有独立生命周期或清晰领域边界，可以合并模块；不要为每张表创建模块。

## 核心领域约束

### Plugin 与 Git 仓库

- 一个托管仓库只承载一个 Plugin；Plugin 和 repository 是一对一关系。
- 仓库内容以 bare Git repository 为权威来源，数据库只保存资源归属、权限、展示信息、refs/版本镜像和审计信息，不复制完整 Git 对象。
- 每个 Plugin 必须在可发布 commit 上包含合法的 `.claude-plugin/plugin.json`。发布版本前必须从目标 commit 读取并校验 manifest。
- repository slug 在同一 owner namespace（用户或团队）内唯一；数据库 ID 才是内部主键，文件系统路径不能直接信任用户输入。
- 删除默认采用软删除/回收站语义；物理删除 Git 数据属于独立、可审计的高风险操作。
- Git push 后通过受控 hook/事件刷新 refs 和版本元数据，并提供周期性 reconciliation 修复 Git 与数据库不一致。

### Git HTTPS/SSH 双模式

- HTTPS 使用标准 Git Smart HTTP，支持 clone/fetch/push；读取和写入分别授权。
- SSH 只允许 Git 协议命令，例如 `git-upload-pack` 和 `git-receive-pack`，禁止任意 shell。
- SSH 公钥必须绑定用户或服务账号；同一把有效公钥不能被多个主体无歧义占用。
- 私有仓库的 clone/fetch 也必须鉴权；公开仓库可以匿名读取，但任何 push 都必须鉴权和授权。
- Git 协议层与 REST API 必须调用同一个 authorization service，不能只依赖前端隐藏按钮。
- 禁止拼接 shell 命令。若调用系统 Git，只能使用 `exec.CommandContext` 和独立参数，并将仓库路径解析到受控根目录后再次校验。
- 对 push 设置并发控制、对象大小、请求体、超时和资源配额；默认拒绝路径穿越、符号链接逃逸和未知 Git service。
- 受保护版本 tag 默认不可移动或删除；任何例外必须有显式权限并写入审计日志。
- HTTPS clone URL 和 SSH clone URL 都由服务端根据配置生成，不能由客户端提交任意本地仓库路径。

### Plugin 版本

- Git commit SHA 是不可变版本内容标识；语义版本通常映射到受保护 tag。
- 发布记录至少保存 Plugin、版本号、完整 commit SHA、tag/ref、manifest 摘要、发布者和发布时间。
- 同一 Plugin 的版本号唯一。已经发布的版本不能静默改指向其他 commit；修复应发布新版本。
- 分支代表开发状态，不等同于可消费版本。Marketplace 默认只引用已发布版本或显式锁定的 commit。
- 版本撤回不能篡改历史；应改变可安装状态并保留审计信息。

### Marketplace 模板与索引

- Marketplace 是 Plugin 的组合视图，不是新的 Plugin 仓库。
- 用户或团队可以创建多个模板/方案，例如 `planA`、`planB`；每个模板独立选择 Plugin 及版本策略。
- 每个已发布模板必须有稳定 URL，例如：
  - `/marketplaces/{namespace}/planA/marketplace.json`
  - `/marketplaces/{namespace}/planB/marketplace.json`
- `planA` 可以发布 A/B/C，`planB` 可以只发布 B/C；模板之间不能互相污染。
- 模板包含可编辑 draft 和不可变 published revision。稳定 URL 指向最新已发布修订，同时应提供按 revision 获取的可复现 URL。
- 发布时解析版本选择、校验每个 Plugin manifest 和 source、生成确定性 JSON，并保存发布快照；不要在每次 GET 时根据浮动分支产生不同结果。
- 输出必须符合 Claude Code 当前官方 Marketplace schema。实现或修改字段前先核对官方文档/JSON Schema，不得臆造兼容字段。
- 输出的顶层对象至少包含唯一、kebab-case 的 `name`、带 `name` 的 `owner` 和 `plugins` 数组；不得使用 Anthropic 官方保留或仿冒名称。
- 由稳定 HTTP URL 直接提供的 `marketplace.json` 只会被 Claude Code 作为单个 JSON 文件下载，因此 Plugin `source` 禁止使用 `./...` 相对路径，必须使用调用者可访问的外部 source。
- 通用 HTTPS/SSH Git Plugin source 使用官方对象格式 `{"source":"url","url":"...","ref":"...","sha":"..."}`；不要自创 `httpsUrl`、`sshUrl`、`cloneUrl`、`transport`，也不要把 Plugin source 类型写成 `git` 或 `ssh`。
- HTTPS 与 SSH 双模式通过不同模板 URL 输出不同的 `source.url`。同一客户端二选一的索引可以使用相同 marketplace `name`；如果需要同时注册，必须使用不同 `name`，因为 Claude Code 会用后加入的同名 Marketplace 替换旧来源。
- Git 发布 source 优先固定 40 位 commit SHA；若显式填写 Plugin `version`，内容更新时必须同步 bump version，否则 Claude Code 的版本缓存可能继续复用旧内容。
- 不要同时在 `.claude-plugin/plugin.json` 和 Marketplace Plugin entry 中维护 `version`；Plugin manifest 的版本优先且不会提示冲突。
- JSON 输出使用稳定排序和标准序列化，设置正确的 `Content-Type`、`ETag` 和缓存策略；草稿绝不通过正式 URL 暴露。
- 私有模板与其中的私有 Plugin 必须同时通过权限检查。不得因为拿到索引 URL 就绕过仓库读取权限。
- 禁止在索引 URL、JSON 或 clone URL 中泄露长期 Token、私钥或服务器文件路径。

## 多用户、团队与权限

### 资源归属

- Plugin、repository、Marketplace 模板必须归属一个 namespace：个人用户或团队。
- 团队资源不因创建者离队而丢失；资源所有权属于团队，不属于操作人。
- 所有查询和唯一约束都必须包含 namespace/tenant 范围，防止跨团队数据泄露。

### 建议角色

- `owner`：团队所有权、成员与高风险设置、资源删除。
- `admin`：团队配置、成员和全部团队资源管理，但不能转移/删除团队所有权。
- `maintainer`：仓库写入、Plugin/版本/Marketplace 发布。
- `developer`：仓库读写、创建草稿，默认不能发布或管理成员。
- `viewer`：读取被授权的资源。

角色只是权限集合。服务层应判定明确动作，例如 `repository.read`、`repository.write`、`plugin.publish`、`marketplace.publish`、`team.member.manage`，不要在 handler 中散落角色字符串判断。

### 授权规则

- handler 只负责提取身份与资源，最终授权必须在 service/authorization 边界执行。
- REST、gRPC、Git HTTPS、Git SSH、后台任务必须复用一致的授权语义。
- 默认拒绝；没有匹配授权时不得降级为允许。
- 用户 Token、密码、SSH 私钥/公钥材料和凭据属于敏感数据：Token/密码只保存强哈希，私钥原则上不进入系统。
- 成员变更、权限变更、SSH key、访问令牌、push、版本发布、模板发布和删除操作必须审计。

## Frontend 模块

### 技术与目录

前端使用 Svelte + Tailwind，执行纯静态构建，不依赖 Node.js SSR 运行时。推荐目标结构：

```text
mod/frontend/
  mod.go                 # kernel.Module；在 Load 注册静态资源和 SPA fallback
  embed.go               # //go:embed all:dist
  dist/                  # 前端构建产物，Go 编译时必须存在
  web/                   # Svelte + Tailwind 源码
    package.json
    package-lock.json    # 或团队选定包管理器对应的唯一 lockfile
    src/
    static/
```

若实际工具链要求将前端源码放在仓库根目录，可以调整位置，但构建产物仍必须由 `frontend` Go package 直接嵌入，不能依赖 `go:embed` 的父目录路径。

### 构建约束

- Svelte 必须输出静态文件到 `mod/frontend/dist/`；不启用必须常驻 Node server 的 SSR 模式。
- Tailwind 在前端构建阶段生成 CSS；生产二进制不包含 Node.js 运行时。
- 前端依赖安装必须使用 lockfile 的可复现命令，例如 `npm ci`；仓库只能保留一种生效的包管理器 lockfile。
- 标准构建顺序：前端依赖安装 → 前端静态构建 → `go build`/`go test`。
- `go build` 不会自动执行 `go generate`。若使用 `go:generate` 封装前端构建，CI、Docker 和发布脚本仍必须显式执行生成步骤。
- Docker 使用 Node builder stage 生成 `dist`，再由 Go builder 编译，最终镜像只包含 Go 二进制及必要运行时文件。
- 不手工编辑 `dist`。源码改动必须重新构建并验证嵌入产物。
- 只嵌入生产 `dist`；禁止嵌入 `.env*`、`node_modules` 或前端源码目录。
- 若 `go test ./...` 需要空的嵌入目录占位，使用明确的构建策略解决；不要提交伪造的生产 bundle。

### HTTP 服务约束

- `frontend` 在 `Load()` 中 `hub.Load(&jin.Engine)`，通过 `fs.Sub` 使用嵌入的 `dist`；不得直接 import `mod/jinx`，也不得自行监听端口。
- SPA fallback 必须由 `jin.Engine.NoRoute()` 提供；禁止注册 `/*path`、`/*any` 等根 catch-all。`frontend` 是唯一允许设置全局 `NoRoute` 的模块。
- `/api/`、`/gapi/`、`/git/`、`/marketplaces/`、`/healthz`、`/debug/`、`/metrics` 等后端路径禁止进入 SPA fallback。
- SPA history fallback 只处理适合前端导航的 `GET`/`HEAD` 请求；静态文件不存在且路径没有文件扩展名时才返回 `index.html`，API/Git 路径和缺失的 `.js`/`.css` 等资源必须返回 404。
- 带内容哈希的资源使用长期不可变缓存；`index.html` 和运行时配置使用 `no-cache` 或短缓存。
- 正确设置 MIME、`Content-Length`、`ETag`/修改时间，并支持 HEAD。
- 禁止把服务器密钥、数据库凭据、Git 凭据或私有运行配置编译进前端 bundle。
- 浏览器权限只用于体验优化，服务端仍必须对每个 API 操作授权。

### 开发模式

- Svelte dev server 仅用于本地开发，并将 API/Git 相关路径代理到 Go 服务；若 Go 端提供 dev proxy，目标只能是 loopback 地址且生产环境必须拒绝启用，避免形成 SSRF/open proxy。
- 生产和集成测试必须验证 Go `embed.FS` 实际提供的静态构建，而不只验证 dev server。
- 前端至少覆盖登录、用户/团队、成员权限、Plugin、仓库 clone 信息、版本发布、Marketplace 模板和发布 URL 等管理流程。

## jframe 模块架构

### Module 接口与生命周期

所有功能以 `kernel.Module` 组织，接口定义在 `core/kernel/module.go`：

```text
Config → PreInit → Init → PostInit → Load → Start
                                            ↓
                                           Stop
```

- `Config()`：返回模块配置结构体指针；无配置返回 `nil`。
- `PreInit()`：创建不依赖其他业务模块的基础资源并 Map 到 DI。
- `Init()`：加载 PreInit 已提供的基础依赖，初始化本模块服务并可 Map 输出。
- `PostInit()`：组装依赖其他模块 Init 输出的领域服务。
- `Load()`：注册 HTTP/gRPC/Git 路由和最终装配。
- `Start()`：运行 SSH server、worker 等长驻任务；内核会在 goroutine 中调用。
- `Stop()`：优雅关闭；实现时第一行必须 `defer wg.Done()`。

所有模块必须嵌入 `kernel.UnimplementedModule`，并添加：

```go
var _ kernel.Module = (*Mod)(nil)
```

### 生命周期顺序的重要现实

当前 `kernel.Engine` 将模块保存到 `map[string]Module`，所以同一阶段的模块遍历顺序不稳定。即使 `cmd/server/modList/list.go` 使用 slice 注册，也不能依赖该 slice 的顺序。

因此：

- 不允许模块在某阶段消费另一个模块同阶段才 Map 的依赖。
- 生产者在较早阶段 Map，消费者在下一阶段或更晚阶段 Load。
- 如果未来确实需要稳定同阶段顺序，必须先将 Engine 改为“有序 slice + 名称索引 map”并补测试，不能靠调整 `ModList` 顺序碰运气。

建议目标依赖节奏：

1. `PreInit`：DB、Redis、jin、Git storage 等基础设施。
2. `Init`：identity、teams、基础 repository 服务等只依赖基础设施的服务。
3. `PostInit`：authorization、plugins、versions、marketplaces 等跨领域装配。
4. `Load`：所有 REST/gRPC/Git HTTP 路由，以及 frontend 静态资源。
5. `Start`：SSH server、异步任务和 reconciliation worker。

实际实现若依赖层级超过这些阶段，应减少同步耦合或先修复内核的确定性依赖排序，不得偷偷依赖 map 遍历顺序。

### DI 规则

`Hub` 包装 `inject.Injector` 和模块命名空间日志：

- `hub.Map(&value)`：按项目现有约定映射依赖。
- `hub.Load(&variable)`：获取依赖，必须检查 error。
- `hub.Invoke(func(dep Type) {})`：函数参数注入。
- 同一具体类型只能保存一个值；多个同类型资源必须使用有语义的 wrapper type。
- 跨领域共享的服务接口/DTO 放在稳定的公共 contract package，避免模块互相导入形成环。
- 新增 DI 基础类型后同步更新 `docs/di-reference.md`。

### 配置规则

每个模块的 Config 字段必须同时有 `yaml` 和 `mapstructure` tag：

```go
type Config struct {
    StorageRoot string `yaml:"storageRoot" mapstructure:"storageRoot"`
    SSHPort     string `yaml:"sshPort" mapstructure:"sshPort"`
}
```

- 环境变量优先于配置文件。
- 密钥不得写入 `config.example.yaml`，只提供空值和安全说明。
- Git storage root、外部访问 base URL、SSH host/port、配额和超时必须由配置提供，不能散落硬编码。

## HTTP 与分层约定

### 统一路由边界

- 管理 REST API：`/api/v1/...`
- gRPC Gateway：保留 `/gapi/...`
- Git Smart HTTP：`/git/{namespace}/{repository}.git/...`
- Marketplace 发布索引：`/marketplaces/{namespace}/{template}/marketplace.json`
- 前端：`/` 及非保留 SPA 路径

新增路由时不得与上述边界冲突。公开读取和需要认证的操作必须在路由与 service 两层都清晰区分。

### 业务模块分层

```text
handler/  HTTP/gRPC 输入输出、认证上下文提取、状态码映射
service/  业务规则、授权调用、事务边界、审计事件
repo/dao/ 数据库访问或 Git repository adapter
model/    持久化模型与 DTO
e/        领域错误
```

数据流向通常为 `handler → service → dao/adapter → model`。Git 协议实现属于 adapter，不要把 `exec.Command` 放进 handler。

在 `Load()` 中自底向上组装依赖并注册路由。handler 不直接包含 GORM 查询。

### jin 不是 gin

HTTP 框架是 `github.com/juanjiTech/jin`：

- `HandlerFunc` 是 `interface{}`，支持 DI 注入的函数参数。
- JSON/Query 输入使用 `jin/middleware/binding`；Query DTO 使用 `query:"key"`。
- 响应使用 `c.Render(code, render.JSON{Data: data})`，没有 `c.JSON` 快捷方法。
- 所有外部输入都在系统边界校验；领域服务仍负责资源归属与授权校验。

## 数据一致性与安全

- 数据库事务只覆盖数据库状态；Git refs/文件系统与数据库之间使用明确状态机、outbox/event 或可重试补偿，不伪装成单一 ACID 事务。
- 所有写操作接受 `context.Context`，支持超时和取消。
- 列表接口必须分页并有租户范围；不得无界扫描仓库、refs、用户或审计日志。
- URL、namespace、slug、ref、tag 和文件路径分别校验，不使用一个宽松正则代替所有语义。
- 对登录、Token、Git push、索引访问和发布操作实施适当限流。
- 日志使用 `hub.Log`，不得记录密码、Token、Authorization header、SSH 私钥或完整敏感配置。
- 生产环境禁止硬编码 pprof 凭据；诊断端点必须可关闭并受强认证保护。

## 测试要求

每个领域模块至少覆盖：

- service 单元测试：权限、归属、状态转换、版本不可变规则。
- DAO 集成测试：唯一约束、租户隔离、事务和软删除。
- HTTP handler 测试：输入校验、认证、授权、错误码和响应 schema。
- Git 协议集成测试：HTTPS/SSH clone、fetch、push、拒绝未授权写入、受保护 tag。
- Marketplace golden/schema 测试：同一 revision 输出字节稳定，并通过官方 schema 校验。
- Frontend 构建/嵌入测试：关键静态文件存在、SPA fallback 正确、保留后端路径不被吞掉。

完成变更前运行：

```bash
go test ./...
go vet ./...
go build ./...
```

涉及前端时还必须运行 `mod/frontend/web/package.json` 中定义的 lint、test、build 命令，并验证 Go 嵌入构建。

## 构建与部署

```bash
# 后端开发
go run . server -c config.yaml

# 构建（前端 dist 已生成）
go build \
  -ldflags "-X github.com/Esonhugh/MarketplaceServer/conf.SysVersion=v1.0.0" \
  -o marketplace-server .
```

最终部署需要持久化：

- 关系数据库。
- bare Git repository storage root。
- 可选 Redis/队列状态。
- SSH host key、服务端密钥和运行配置。

Git storage 不能只存在于容器临时文件系统。备份和恢复必须同时覆盖数据库与 Git 对象，并记录一致性恢复流程。

## Agent 开发硬性规则

1. 新产品能力优先作为领域明确的 `kernel.Module`；不要把业务逻辑写进 `main.go` 或 `cmd/`。
2. 开发前检查目标模块是否真实存在；本文“目标模块”不是已完成声明。
3. 模块间通过 DI contract 通信，不直接跨模块访问 DAO 或内部 model。
4. 不依赖当前内核的模块 map 遍历顺序；生产和消费依赖必须跨生命周期阶段。
5. Config 字段必须有 `yaml` + `mapstructure` 双 tag。
6. 所有 `hub.Load` 必须检查错误；DI 同类型冲突使用 wrapper type。
7. `Stop()` 第一行必须 `defer wg.Done()`。
8. 使用 `hub.Log`；禁止输出凭据、Token、私钥或敏感 clone URL。
9. 权限必须在服务端统一执行，不能只在 Svelte 前端控制。
10. Git 命令禁止经 shell 拼接；所有路径必须限制在仓库存储根目录。
11. 发布版本和 Marketplace revision 必须可复现、可审计；不能用浮动分支伪装不可变发布。
12. `marketplace.json` 必须以当前 Claude Code 官方 schema 为准；不猜字段。直接 URL 索引不能使用相对 Plugin source，通用 Git Plugin source 类型使用 `url`。
13. frontend 必须是 Svelte + Tailwind 静态构建并由 `embed.FS` 托管；生产环境不启动 Node SSR。
14. 新模块设计使用 `jframe-module-design`，模块内部实现使用 `jframe-module-dev`。
15. 不顺手重构无关 jframe 基础代码；发现框架缺陷时先用测试证明，再做最小修复。
