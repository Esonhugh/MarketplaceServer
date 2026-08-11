# MarketplaceServer

MarketplaceServer 是一个自托管的 Claude Code Plugin 控制、Git 托管与 Marketplace 分发服务。它基于 jframe 模块化内核，以单 Go binary 提供管理 API、Git Smart HTTP、不可变 Marketplace/Plugin distribution 和嵌入式 Svelte frontend。

## 当前能力

- 固定的 `jin`、`sql`、`git`、`backend`、`frontend` 五模块运行时；
- 用户、个人 namespace、系统组和管理员 bootstrap；
- Basic 认证与 scoped personal access token 创建、列表、撤销；
- 开发 repository 的 Git Smart HTTP clone/fetch/push 与服务端 authorization；
- Public Marketplace/Plugin immutable Git distribution 与 Marketplace JSON；
- 按用户当前权限生成的私有 Marketplace JSON；
- Svelte/Tailwind 静态产物经 `embed.FS` 托管。

团队、完整 Plugin/version/Marketplace 管理、审计、SSH Git 和完整管理 UI 仍在规划中。精确状态见 [当前实现状态](docs/current-state.md)，不要用目标设计推断已交付能力。

## 前置条件

- Go 版本与 toolchain 以 [`go.mod`](go.mod) 为准；
- PostgreSQL 或 MySQL；
- 系统 Git binary；
- Node/npm 仅用于重新构建 frontend 静态产物。

## 快速开始

```bash
cp config.example.yaml config.yaml
```

在受保护配置中设置 `sql.dsn`，并通过环境变量提供 identity secret：

```bash
export MARKETPLACE_BOOTSTRAP_ADMIN_PASSWORD='<initial-admin-password>'
export MARKETPLACE_API_KEY_PEPPER='<base64-encoded-random-value-at-least-32-bytes>'
go run . server -c config.yaml
```

`MARKETPLACE_BOOTSTRAP_ADMIN_PASSWORD` 用于首次管理员初始化；API-key pepper 必须是解码后至少 32 bytes 的 Base64 值。不要把这些 secret 提交到配置或源码。

## 构建

frontend dist 已生成时：

```bash
go test ./...
go vet ./...
go build -o marketplace-server .
```

修改 frontend 时先在 `mod/frontend/web` 执行：

```bash
npm ci
npm run check
npm test
npm run build
```

完整验证和生产要求见 [运维、测试与交付](docs/operations.md)。

## 文档

| 文档 | 说明 |
|---|---|
| [CLAUDE.md](CLAUDE.md) | Agent 文档索引与不可违反规则 |
| [docs/current-state.md](docs/current-state.md) | 当前模块、领域、路由和缺口 |
| [docs/project-goals.md](docs/project-goals.md) | 产品使命和目标 |
| [docs/architecture.md](docs/architecture.md) | 五模块架构、生命周期和边界 |
| [docs/product-invariants.md](docs/product-invariants.md) | 领域、安全和一致性不变量 |
| [docs/protocols.md](docs/protocols.md) | REST、Git 与 distribution 协议 |
| [docs/usage.md](docs/usage.md) | CLI、配置与开发流程 |
| [docs/di-reference.md](docs/di-reference.md) | 当前共享 DI 类型 |
| [docs/operations.md](docs/operations.md) | 测试、构建、部署与恢复 |
| [docs/roadmap.md](docs/roadmap.md) | 未来交付顺序 |
| [docs/ai-development.md](docs/ai-development.md) | Agent 开发工作流 |
| [docs/design/README.md](docs/design/README.md) | 通用数据、管理 API contract、ADR 与 system design 索引 |

## License

[MIT](LICENSE)
