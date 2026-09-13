# 使用与本地开发

本文记录 MarketplaceServer 当前可用的 CLI、配置和开发流程。系统边界见 [架构](architecture.md)，生产部署见 [生产部署](deployment.md)，验证与恢复要求见 [运维、测试与交付](operations.md)。

## 运行时概览

`main.go → cmd.Execute()` 提供 Cobra 子命令。`server` 加载 `cmd/server/modList/list.go` 中固定的五个模块：

```text
jin → sql → git → backend → frontend
```

装配阶段按该顺序确定执行；详细生命周期见 [架构](architecture.md)，可共享类型见 [DI 参考](di-reference.md)。

## 启动服务

复制安全示例：

```bash
cp config.example.yaml config.yaml
```

至少配置：

- `sql.driver`：生产使用 `postgres`；单进程本地开发/测试可使用 `sqlite`；
- `sql.dsn`：所选 PostgreSQL 或 SQLite 数据源；
- `git.storageRoot`：持久化 Git storage root；
- `MARKETPLACE_API_KEY_PEPPER`：Base64 编码、解码后至少 32 bytes 的随机 secret；
- `MARKETPLACE_BOOTSTRAP_ADMIN_PASSWORD`：首次初始化管理员时使用的 password；
- `MARKETPLACE_JWT_SECRET`：management JWT signing secret，优先于 `backend.jwtSecret`；生产必须通过受保护环境或 secret manager 提供。

```bash
export MARKETPLACE_BOOTSTRAP_ADMIN_PASSWORD='<initial-admin-password>'
export MARKETPLACE_API_KEY_PEPPER='<base64-secret>'
export MARKETPLACE_JWT_SECRET='<random-jwt-signing-secret>'
go run . server -c ./config.yaml
```

`config.example.yaml` 只保留空的 `backend.jwtSecret` 安全 fallback，不包含真实 secret。若 `MARKETPLACE_JWT_SECRET` 已设置但为空，启动会失败而不会退回 YAML；生产不应把 JWT secret 写入普通配置文件、日志或 frontend bundle。支持的配置键以 [`config.example.yaml`](../config.example.yaml) 为准。

## Identity 数据兼容

Identity migration 遇到旧 `personal_access_token_scopes` table，或已有 `personal_access_tokens` 缺少 `preset`、`secret_plaintext`、`secret_hmac` 时，会返回 `identity: legacy credential schema requires operator rebuild` 并拒绝 backend 启动；runtime 不会自动删除、backfill 或双读旧 credential schema。

开发环境若确认数据可丢弃，应先停止服务，再由 operator 删除并重建整个开发数据库，然后按上述环境变量重新启动和 bootstrap；不要让应用执行 destructive fallback。任何含需保留数据的环境都必须先备份，并为旧 PAT 制定显式迁移或轮换方案，不能直接套用开发重建流程。

## CLI

### `server`

加载配置、初始化 kernel 并启动五模块运行时：

```bash
go run . server -c ./config.yaml
```

### `config`

扫描当前已注册模块的 `Config()` 并生成 YAML：

```bash
go run . config
go run . config -p ./config.yaml -f
```

生成结果不包含可安全投入生产的 secret；仍需通过 protected runtime config/environment 注入。

### `create`

仓库保留了 jframe 的通用 module scaffolding 命令，但 MarketplaceServer 普通产品功能不得用它新增顶层 runtime module：

```bash
go run . create -n example
```

只有经过显式架构决策、确实需要改变五模块拓扑时才使用该命令，并同步 `cmd/server/modList/list.go`、测试、[架构](architecture.md) 与 [DI 参考](di-reference.md)。通常应在 `backend` 内新增领域，或修改现有 `git`/`frontend` 模块。

## Config 规则

模块 Config 字段必须同时带 `yaml` 和 `mapstructure` tag：

```go
type Config struct {
    StorageRoot string `yaml:"storageRoot" mapstructure:"storageRoot"`
}
```

- 环境变量覆盖配置文件；
- `hub.Load` 必须检查 error；
- 新增 operator 配置时同步 `config.example.yaml`，但不写 secret value；
- 尚未实现的 SSH、public base URL 或调优项不要提前加入配置冒充支持。

## 当前开发落点

- identity、authorization、distribution 以及未来的业务领域：`mod/backend/domain/`；
- HTTP handler：`mod/backend/handler/`；
- Git filesystem、subprocess、Smart HTTP、projection：`mod/git/`；
- Svelte/Tailwind 源码：`mod/frontend/web/`；
- frontend embedded output：`mod/frontend/dist/`，只由构建生成；
- 跨模块 contract：`pkg/*service/` 等稳定 package。

先读 [当前实现状态](current-state.md) 判断 feature 是否存在，再读目标模块与 tests。不要把业务逻辑写入 `main.go`、`cmd/`，也不要跨模块访问 DAO/model。

## Frontend 开发

在 `mod/frontend/web`：

```bash
npm ci
npm run check
npm test
npm run build
```

静态输出进入 `mod/frontend/dist` 并由 Go `embed.FS` 编译进 binary。生产不运行 Node SSR。完成后运行 frontend Go tests 和 Go build，验证 reserved backend path 不会落入 SPA fallback。

## 验证

基础检查：

```bash
git diff --check
go test ./...
go vet ./...
go build ./...
```

PostgreSQL integration tests 需要 `MARKETPLACE_TEST_POSTGRES_DSN`。适用的 race、真实 Git client、frontend 与 publication 检查见 [运维、测试与交付](operations.md)。

## Docker

仓库保留 `Dockerfile`、`docker-compose.yml` 与 `docker-compose-dev.yml`，但它们可能包含继承自通用 jframe 的开发假设。使用前应对照当前 `config.example.yaml`、SQL driver、frontend build 和 persistent Git storage 要求审查，不应把旧 MySQL/Redis 示例视为 MarketplaceServer 的权威生产拓扑。
