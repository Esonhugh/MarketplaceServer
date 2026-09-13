# 运维、测试与交付

本文维护 MarketplaceServer 的验证门槛、构建流程和目标生产运行要求。当前能力见 [当前实现状态](current-state.md)，可执行的单实例生产基线见 [生产部署](deployment.md)；标为“推荐/规划”的拓扑不代表现有镜像已完整交付。

## 变更验证

所有变更至少运行：

```bash
git diff --check
go test ./...
go vet ./...
go build ./...
```

涉及并发、认证/credential、Git transport/storage、worker、publication/projection 或其他 shared mutable state 时必须运行：

```bash
go test -race ./...
```

PostgreSQL-backed constraint 与 production-wiring tests 需要 `MARKETPLACE_TEST_POSTGRES_DSN`。未提供时测试会显式 skip；结果报告必须说明未执行的数据库覆盖，不能写成全部通过。MarketplaceServer 运行时不支持 MySQL；SQLite 仅用于单进程本地开发和测试。

### Frontend 变更

修改 `mod/frontend/web`、package metadata、构建行为或 embedded output 时，在该目录执行 package scripts：

```bash
npm ci
npm run check
npm test
npm run build
```

然后重新运行 Go frontend embed tests 和 Go build。不要手工编辑 `mod/frontend/dist`；源码变更必须由静态构建生成产物。生产 binary 不依赖 Node SSR。

仅修改 Markdown 时无需安装 frontend dependencies，但仍需执行 `git diff --check`、链接/现状检查，以及能验证关键文档事实的聚焦 Go tests。

## 测试矩阵

新增 feature slice 应按涉及边界提供：

- service unit tests：allow/deny、tenant ownership、状态转换和不可变规则；
- DAO/database tests：唯一约束、外键、tenant isolation、transaction、soft delete；
- HTTP tests：输入边界、认证、授权、错误 code/request ID、cache 与 response schema；
- Git real-client tests：clone/fetch/push、private read、unauthorized write、unknown service、path traversal、cancel/limit；
- distribution tests：anonymous public read、private credential（实现后）、只广告指定 tag、拒绝所有 write service，并证明请求不改变开发 repository 或 active projection；
- Marketplace golden/schema tests：相同 revision bytes 稳定、HTTP/Git snapshot 一致、source 固定到 distribution identity；
- frontend tests：关键静态文件、base path、SPA fallback、reserved backend path、missing asset、cache 和 HEAD。

规划中的 SSH、团队、audit 或 private distribution tests 只在相应能力实现后成为可执行 gate，不能在当前状态文档中声称已运行。

## 文档质量门槛

- `docs/current-state.md` 是唯一当前能力汇总；只根据代码、测试、migration 与路由注册更新。
- `docs/roadmap.md` 是唯一未来交付清单。
- `docs/di-reference.md` 是唯一共享 DI 类型表。
- `config.example.yaml` 是唯一完整 operator 配置清单。
- 设计文档中的未来 entity、状态机、API 或 topology 必须标注“规划中/目标”。
- 仓库内相对链接必须存在；不维护相互复制的大段正文。
- 修改 behavior、配置、DI、route 或 migration 时，在同一变更中更新相应文档。

## 当前开发与构建

```bash
# 启动
cp config.example.yaml config.yaml
# 通过受保护配置或环境变量补充数据库和 identity secret
go run . server -c config.yaml

# 构建（frontend dist 必须已生成；release metadata 使用当前 commit）
VERSION="$(git rev-parse --short HEAD)"
go build \
  -ldflags "-X github.com/Esonhugh/MarketplaceServer/conf.SysVersion=${VERSION}" \
  -o marketplace-server .
```

Go toolchain 版本以 `go.mod` 为准。支持配置键和安全示例以 [`config.example.yaml`](../config.example.yaml) 为准。

标准 release build 顺序：frontend lockfile install → static build → Go test/vet/build。`go build` 不会自动执行 `go generate`；CI、Docker 和 release script 必须显式生成 frontend dist。

## Identity schema 启动保护

Identity migration 在以下任一情况返回 `identity: legacy credential schema requires operator rebuild` 并拒绝 backend 启动：存在旧 `personal_access_token_scopes` table，或已有 `personal_access_tokens` 缺少 `preset`、`secret_plaintext`、`secret_hmac`。该 guard 位于 `mod/backend/domain/identity/dao/migrate.go`，不会自动 drop、backfill 或启用双读 compatibility。

开发数据库若可丢弃，operator 应先停止服务、确认没有需保留数据，再使用所选数据库的管理工具删除并重建整个开发 database/schema，然后重新启动让 migration 和 bootstrap 创建目标 schema。仓库不提供通用 destructive 命令，因为 PostgreSQL/SQLite、权限和部署形态不同。生产或任何有价值环境必须先备份，评估旧 PAT 的 revoke/rotation，并交付显式迁移方案；不得直接采用开发重建流程。

## 持久化数据

当前可执行的单实例 topology、主机配置、reverse proxy 和启动验收统一见 [生产部署](deployment.md)。本节只维护跨部署形态的数据与恢复不变量。

生产部署至少需要持久化：

- 关系数据库；
- bare development repository 与 immutable distribution projection storage root；
- server secrets/runtime config；
- SSH 实现后还包括 host keys。

Git storage 不能只存在容器临时 filesystem。数据库和 Git storage 必须联合备份，并使用可关联的逻辑时间/序列标记。

## Storage layout

推荐目标布局：

```text
{storageRoot}/
  repositories/{shard}/{repositoryID}.git
  distributions/
  quarantine/{jobID}/
  trash/{repositoryID}-{deletedAt}.git
  locks/
```

实际当前路径以 `mod/git` 实现为准；新增布局必须遵守：

- startup 将 storage root 解析为 absolute path 并检查权限；
- 物理路径只使用 opaque ID/storage key，不使用 namespace/repository slug；
- 通过 `filepath.Rel` 等 containment check 阻止 escape 与 symlink 绕过；
- quarantine、最终目录与 trash 在同一 filesystem，以便 atomic rename；
- active immutable projection 禁止原地写入；
- 备份不能在未协调写入时直接复制 repository，使用 Git-aware 或 filesystem snapshot/维护窗口。

## 推荐生产拓扑

> 以下是目标生产拓扑，不代表当前 Docker assets 已满足全部要求。

```text
TLS ingress / reverse proxy
       │
       ├── HTTPS ── MarketplaceServer
       │               ├── REST / Smart HTTP / distribution / frontend
       │               ├── SQL database
       │               └── durable POSIX Git storage
       │
       └── SSH TCP ── future SSH listener
```

- MVP 优先单应用实例、单数据库和 persistent local volume。
- TLS ingress 只向应用传递可信 proxy header；应用只信任明确 proxy range。
- 横向扩展前，storage 必须由所有实例提供一致 POSIX rename/locking，或使用 repository placement 将同一 repository 稳定路由到一个 storage node。
- 普通 object storage 不能直接挂载为 bare Git filesystem。
- Redis 若未来使用，只承载 cache、rate limit 或 lease 等可重建状态；authorization、publication pointer 和版本身份不能只存在 Redis。
- SSH 多副本需要共享 host key，并通过支持 TCP 的 load balancer 分发。

## Runtime security

- 进程以非 root 用户运行，container root filesystem 尽量只读，只给 Git storage 和明确 temp path 写权限。
- database credential、PAT pepper、bootstrap credential、JWT secret、future SSH host key 由 secret manager 或 protected mount/environment 提供；YAML `backend.jwtSecret` fallback 只用于明确的开发配置，生产使用优先级更高的 `MARKETPLACE_JWT_SECRET`。
- 按 ADR-0006 获批的 repeatably revealable PAT `secret_plaintext` 使关系数据库、replica、dump、snapshot、PITR archive 与备份进入 credential trust boundary；访问、导出、传输、恢复和销毁都按可直接使用的 credential secret material 保护。
- 日志不得包含 password、token、Authorization、private key、secret-bearing URL、pack body 或完整敏感 config。
- 固定并验证 Go、Git、Node 依赖版本；最终生产镜像不包含 npm、compiler 和不需要的工具。
- 诊断 endpoint 默认可关闭并受强认证，禁止硬编码 pprof credential。
- 对 login、token、push、distribution 和 publication 设置与风险匹配的 timeout、body/concurrency/rate limits。

## Readiness、shutdown 与 worker

Readiness 至少验证 SQL 和 storage root；外部 observability 短暂不可用不应使实例不 ready。

Shutdown 顺序遵循入口优先 drain：

1. 停止接受新 push/publication；
2. drain HTTP 与未来 SSH；
3. 停止 backend worker 与 Git task；
4. 关闭 SQL 和其他基础设施；
5. 在总 timeout 内记录未完成任务。

当前内核先停止 `jin`，再逆序停止其他模块。新增 worker 必须使用 context cancellation、lease/heartbeat、幂等执行、有限重试和明确 dead-letter/人工恢复。

## Backup 与恢复

推荐目标：

- SQL 使用持续 backup/PITR；
- Git storage 使用版本化 snapshot；
- secret、配置和未来 SSH host key 独立备份；
- backup 有同一逻辑时间/sequence 标记。

恢复顺序：停止写入 → 恢复 DB/Git/secrets → 只读 integrity scan → reconciliation → 恢复 read → 恢复 write。

恢复后至少验证：

- 每个 ready repository 的目录存在且 containment 正确；
- published version identity 可达且与记录一致；
- Marketplace revision bytes/digest 与 projection 匹配；
- stable pointer 只指向 active immutable projection；
- SSH 实现后，host fingerprint 与公告一致。

定期执行恢复演练并记录实际 RPO/RTO；“backup job 成功”不等于可恢复。

## Feature slice Definition of Done

设计批准、Agent 独占编辑归属、pre-edit TDD 与 feature commit 策略只在 [AI / Agent 开发工作流](ai-development.md) 中定义；本文只维护可执行验证与交付门槛。

1. contract、ownership 与 security boundary 明确；
2. allow/deny 与非法状态测试齐全；
3. database 约束和 tenant isolation 有集成验证；
4. Git protocol 变更有真实 client 测试；
5. API error、request ID 和 cache 行为稳定；
6. config 与 DI 文档同步且无 secret；
7. publication/distribution 变更验证 reproducibility 与 read-only boundary；
8. 文档不把规划能力描述为当前实现；
9. 完成适用的 test/race/vet/build/frontend checks 和 `git diff --check`；
10. 不顺手重构无关 jFrame 基础代码；框架缺陷先由测试证明，再最小修复。
