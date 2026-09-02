# MarketplaceServer Plugin lifecycle 运行时审计报告

日期：2026-09-02

## 审计结论

本轮按已批准的 Plugin lifecycle 设计逐项核对 backend、Git protected receive、management API、持久化、恢复原语、运行时装配和文档，并使用 SQLite、本地 bare Git repository、真实 Git client 与 `claude plugin validate --strict` 验证主要流程。

已复现并最小修复以下问题：

1. protected receive 的 pre-receive/proc-receive Unix socket 桥接会死锁；
2. 请求取消不能稳定中断 hook socket I/O；
3. branch-only push 成功后 session shutdown 会被误报为内部错误；
4. 生产 hook 只把 tag 交给 proc-receive，无法保证 mixed branch+tag push 的整批事务；
5. server 生命周期/configuration 失败可能被 recover 后以成功状态退出；
6. 真实 Git 测试会继承开发者全局 GPG signing 配置；
7. `.http` 示例使用了无效的 Basic authentication 格式；
8. 当前状态与设计文档存在已部署 contract、authorization action 与 migration 装配的事实偏差；
9. Version 发布/恢复未持久化 raw tag object ID，幂等比较、receive/recovery CAS 只绑定 peeled commit；
10. 首次发布与 tombstone 恢复未写 append-only Version history；
11. recovery manualization 未同步 transition 状态，pointer CAS 未绑定原 artifact；
12. management publish/default、protected receive 与 recovery 未共享同一 Plugin effect lock，且 publish/default 未拒绝 unresolved receive。

上述可复现问题均已以回归测试约束并做最小修复。仍有需要后续设计批准和运行时 slice 才能闭合的恢复调度、readiness 与运维入口边界，详见“剩余设计与运维边界”。

## 调试环境

成功运行时使用本地 SQLite 文件和本地 Git storage root：

```yaml
mode: development
port: "18081"
sql:
  driver: sqlite
  dsn: /tmp/marketplace-debug/marketplace-e2e.db
  debug: true
  maxIdleConns: 1
  maxOpenConns: 1
git:
  storageRoot: /tmp/marketplace-debug/git-e2e
  validatorBinary: /opt/homebrew/bin/claude
frontend:
  disabled: true
```

进程需要：

```text
MARKETPLACE_BOOTSTRAP_ADMIN_PASSWORD=<本地调试密码>
MARKETPLACE_API_KEY_PEPPER=<至少解码为 32 字节的有效 Base64 值>
MARKETPLACE_JWT_SECRET=<本地 JWT 签名密钥>
```

启动方式：

```bash
go build -o /tmp/marketplace-debug/marketplace-server .
/tmp/marketplace-debug/marketplace-server server -c /tmp/marketplace-debug/config-e2e.yaml
```

凭据、SQLite 文件、Git storage、客户端 worktree 和响应证据均保存在 `/tmp/marketplace-debug`，没有加入仓库。

## 已验证流程

| 流程 | 结果 | 说明 |
|---|---:|---|
| `/api/v1/health` | 200 | 当前是 backend liveness，不代表依赖 readiness |
| Bootstrap `admin` 登录 | 200 | 返回 management JWT |
| 错误密码登录 | 401 | 正确拒绝 |
| 在个人 namespace 创建 Plugin | 201 | 创建 draft Plugin 与 ready hidden repository |
| Plugin list/get | 200 | DTO 未暴露路径或内部 repository ID |
| 匿名读取 private Plugin | 404 | 保持 nondisclosure |
| 创建 Git write PAT | 201 | 返回 PAT |
| 匿名读取 private Git | 401 | 正确要求认证 |
| 无效 PAT 写 Git | 401 | 不降级为匿名 |
| 真实 Smart HTTP 推送 branch + canonical tag | 成功 | 创建 `main` 与 `v1.0.0` |
| strict Plugin source validation | 成功 | manifest 名称精确匹配且无 warning |
| 发布并设默认 Version | 201 | Version available，Plugin active |
| Version list/get | 200 | 返回当前 commit SHA |
| archive 后 push | 403 | 正确拒绝写入 |
| restore | 204 | 恢复 active，默认 Version 保留 |
| public Git advertisement | 200 | public upload-pack discovery 可用 |
| Public registration capability 与注册 | 200/201 | 配置开启后创建普通用户并自动进入管理界面 |
| 管理员用户管理 | 成功 | 创建用户、授予/撤销 system-admin，导航权限即时同步 |
| Team 管理 | 成功 | 创建 Team、发出 7 天邀请、目标用户站内接受并成为 viewer |
| Plugin 项目 UI | 成功 | 创建/列表/详情、clone URL、分支/tag 选择、目录树与 UTF-8 blob 预览 |
| Plugin commit history | 成功 | 浏览器显示真实 Git commit 与 SHA |
| Plugin Version UI | 201 | 发布 `v1.0.0` 并设为默认版本 |
| Plugin settings UI | 204 | private→public、archive、restore 均同步刷新 |

SQLite 只证明单进程开发/测试行为，不证明 PostgreSQL 生产约束与并发行为。

## 已发现并修复的缺陷

### 1. Protected receive 双工协议死锁

真实 canonical tag push 曾挂起。根因有两处：

- pre-receive 客户端发送完 command stream 后仍等待服务端响应，但该桥接没有响应阶段；
- proc-receive 先完整发送 stdin 再读取响应，而 Git 会等待 negotiation response 后才继续发送 commands，形成双向等待。

修复位于 `mod/git/receive.go`：

- pre-receive 发送并 half-close 后立即返回；
- proc-receive 并发转发 request 与 response；
- Unix socket 明确执行 read/write half-close 和 full close。

真实 push 修复后返回：

```text
[new branch] main -> main
[new tag]    v1.0.0 -> v1.0.0
```

### 2. Hook 取消与 accepted connection 泄漏

只关闭 listener 不能中断已经 accept 的 connection。若对端只发送部分 mode/pkt-line，`ReadString`、`io.Copy` 或 pkt-line parser 可永久阻塞，`Abort`/`Wait` 也会一直等待。

修复：

- session 跟踪当前 accepted connection；
- cancel/abort 同时关闭 listener 和 accepted connection；
- client 使用 `context.AfterFunc` 在 context 取消时关闭 socket；
- 正常 branch-only receive 的 listener shutdown 被识别为正常结束，不再返回伪内部错误。

新增回归覆盖 partial proc-receive、client cancellation、pre-receive send-and-return 与 branch-only session shutdown，并执行重复 race 测试。

边界：`runReceiveHookClient` 无法主动取消一个永远阻塞且不支持 context 的任意上游 `io.Reader`；生产 stdin 来自 Git hook 进程，进程退出/pipe close 提供生命周期边界。本轮未为抽象的不合作 reader 引入额外 goroutine 管理层。

### 3. Mixed push 未进入同一 ref transaction

批准的 contract 要求 ordinary branch 与 canonical tag 的完整 ref command set 通过同一 expected-old `git update-ref --stdin` transaction，不能依赖客户端 `--atomic`。

生产配置原先只有：

```text
receive.procReceiveRefs = refs/tags/
```

因此测试虽然将 heads/tags 都交给 proc-receive，实际安装逻辑却可能让 branch 绕过自定义 transaction。现已改为安装：

```text
receive.procReceiveRefs = refs/heads/
receive.procReceiveRefs = refs/tags/
```

并增加安装配置回归测试；真实 non-atomic mixed push deny test 证明拒绝 canonical tag 时 branch 也不落地。

### 4. Server 启动失败可能返回成功退出码

原命令在 kernel 生命周期 panic 时只记录日志并从 Cobra `Run` 返回，导致无效 API-key pepper 等启动错误可能表现为静默 `exit 0`。

`cmd/server/server.go` 已改用 `RunE`，将 config、listen、module start/stop 和 recovered lifecycle error 返回 Cobra；listener 与 signal registration 也明确清理。新增 missing-config 回归测试。

无效 pepper 的手工验证现在返回非零状态，并给出不含 secret 的安全错误上下文。

### 5. 测试受全局 Git signing 配置污染

真实 Git fixture 会继承全局 `commit.gpgSign`/`tag.gpgSign`，在配置了交互式 GPG 的开发机上失败或挂起。受影响 fixture 现显式设置：

```text
commit.gpgSign=false
tag.gpgSign=false
```

这只隔离测试，不改变生产 Git 配置。

### 6. HTTP 示例与文档事实偏差

- `.http` 文件原先使用 `Authorization: Basic <username> <pat>`，不符合 RFC Basic scheme，也无法被 Go `Request.BasicAuth()` 解析；现改为预编码 `base64(username:pat)` 变量。
- `docs/current-state.md` 原先把 restore/set-visibility 写为 `plugin.write`；实现和批准 contract 均使用 `plugin.archive`，现已纠正。
- Plugin routes 已进入 deployed `api/openapi/management-v1.yaml`，但设计文档仍称 deployed contract 尚未包含这些 routes；现已把 proposal 标为设计历史，并明确 deployed contract 为当前 wire authority。

### 7. Version raw tag authority 与 history 缺口

Version 原先只持久化 peeled commit SHA。annotated tag 可在 peeled commit 不变时更换 raw tag object，导致 management 幂等发布、protected receive 与 recovery CAS 无法验证实际 ref identity；首次发布和 tombstone 恢复也未写 history。

修复：

- available Version 必须持有 `raw_tag_object_id`，deleted tombstone 必须清空；SQLite/PostgreSQL 约束同步收紧；
- publish/restore 持久化 raw tag，并同时比较 raw tag、peeled commit、manifest digest/snapshot；
- receive prepare、live resolution 与 recovery resolution 的 Version CAS 同时绑定 expected raw object 与 peeled commit；
- 首次发布写 `publish` history，tombstone 恢复写 `restore` history，幂等重试不重复写入。

### 8. Recovery manualization 与 pointer CAS

Recovery 将 batch/intent 标为 `manual_required` 时原先未同步 projection transition，且 complete/abort pointer CAS 只比较 generation/available，未比较 prepared 时记录的 nullable current artifact。

修复后 recovery 会把相关 transition 同步标为 `manual_required`、保持 pointer fail-closed，并在完成/中止时使用与 live coordinator 相同的 nullable artifact CAS。新增 unexpected-ref manualization 与 stale raw-tag CAS 回归测试。

### 9. Management、receive 与 recovery effect 协调

原 management 使用 service 私有进程锁，receive 使用独立 SQLite 锁或 PostgreSQL advisory lock，recovery 不加锁；三者不能形成同一逻辑临界区。publish/default 还会在 unresolved `prepared|finalizing|manual_required` intent 存在时继续修改 Version/default。

现引入同一数据库 effect locker：SQLite 使用进程级 keyed lock，PostgreSQL 使用 bounded session advisory lock。backend 装配时将同一 locker 注入 management 与 receive；recovery 也按 Plugin 获取同一类锁。publish/default/clear-default 在锁内查询 unresolved intent 并返回 conflict；archive、restore、visibility 同样加入 effect serialization，避免状态校验与 receive admission 交错。

## 审计后排除的误报

### PostgreSQL advisory lock 不要求业务查询复用锁 connection

receive coordinator 在专用 PostgreSQL session 上持有 session-level advisory lock，而 GORM 查询可能使用池中其他 connection。只要所有 live receive 对同一 Plugin 都先获取相同 advisory lock并在 coordination close 时释放，锁仍能提供跨实例的逻辑临界区；受保护查询本身不需要在持锁 connection 上执行。因此“查询不复用 lock connection 就完全没有串行化”不是成立的缺陷。

本轮已把 management、receive 与 recovery 统一到同一 Plugin effect-lock 语义；PostgreSQL integration 仍需在提供 DSN 后验证跨实例竞争。

## 剩余设计与运维边界

### 1. Recovery 原语已实现，但没有常驻调度

`mod/backend/domain/plugin/recovery` 提供 receive reconciliation、orphan cleanup 和 projection GC，可根据 actual ref 确定性完成/中止 batch，且不 force-move ref。当前 backend runtime 没有构造常驻 scheduler，也没有启动时 reconciliation。

影响：如果进程在 prepared pointer 已 fail-closed 后崩溃，状态不会自动恢复，必须由尚未交付的 operator 调度/入口调用 recovery。

本轮没有直接增加 worker，原因是还需确定：

- startup reconciliation 与周期调度顺序；
- worker claim/lease、重试、dead-letter、shutdown drain 与可观测性；
- `manual_required` 的受审计管理员重试入口。

现有 reconciler 已与 live receive 共用 Plugin effect lock，但常驻 ticker 仍缺少 worker claim/lease、重试、shutdown drain、可观测性与管理员处理入口。这是下一独立设计/实现 slice，不应以临时 goroutine 冒充完整恢复能力。`docs/current-state.md` 已将其描述为“可调用基础能力，尚无常驻 worker”。

### 2. Git capability startup probe 未实现

批准的 Git contract 写明：安装的 Git 不具备所需 proc-receive 行为时，runtime startup fail closed。当前启动只解析 Git binary；hook/config 能力在 receive 时安装和验证。也就是说，不兼容 Git 可能到首次 push 才暴露为 503，而不是启动失败。

仅按 Git version 或 `git help --config` 判断不足以证明 negotiation、push-options、atomic result 和 ref transaction 行为。可靠 startup probe 需要用临时 bare repository 执行一次最小真实 proc-receive capability test，并定义启动成本、临时文件清理及平台兼容性。该 probe 尚无批准的实现细节，本轮记录为设计—运行时差距。

### 3. Health 不是 dependency readiness

`/api/v1/health` 当前只返回 backend ok；`docs/operations.md` 的目标 readiness 至少检查 SQL 与 Git storage root。当前没有独立 readiness route，也没有 SQL/storage check wiring。

不应把现有 liveness 改成依赖 fail-closed 而破坏进程存活探针。后续应明确增加独立 readiness endpoint，或批准 health contract 变更，并定义 timeout、状态码和不泄露路径的错误信息。

### 4. Publish wire 没有客户端 expected SHA

当前 publish request 只有 `tag` 与 `makeDefault`。服务端重复读取/校验 tag 并 CAS-bind 当前 SHA，可防止服务端操作窗口中的 tag move，但客户端不能表达“只发布我已审阅的 SHA”。

若产品要求用户可见 optimistic concurrency，应在单独 contract 变更中增加 expected SHA；若只要求发布调用时的服务端 authority，当前模型合理。本轮不擅自扩展 wire。

### 5. User/Team 与项目管理联合验收已闭合

后续 User/Team lifecycle 与管理前端已交付。使用 Chrome DevTools MCP 在全新 SQLite 状态中模拟真实用户完成：公开注册、登录、管理员创建用户及 system-admin 授予/撤销、Team 创建与邀请接受、个人 Plugin 创建、PAT 创建、真实 Git push、仓库树/文本预览/commit history、Version 发布/default、visibility、archive/restore。

浏览器验收发现登录与注册后 App shell 未立即刷新 profile，曾导致管理员导航缺失或沿用旧 profile；现由 App 统一接管认证后路由/profile 加载，并增加回归测试。嵌入 token 页面不再重复显示 logout。仓库 revision selector 已补充稳定 `id`，消除表单可访问性告警。

网络检查中业务请求均返回预期 2xx；目录探测先请求 blob 得到 409、再读取 tree 得到 200，是当前浏览器的类型判定流程，不是失败状态。控制台只剩静态 favicon 404，不影响 API 与状态一致性。

## 验证命令

Protected receive 聚焦与重复 race 回归：

```bash
go test ./mod/git -run 'TestReceiveHookSessionCancellationClosesAcceptedConnection|TestReceiveHookSessionWaitAllowsSuccessfulBranchOnlyReceive|TestReceiveHookClientCancellationUnblocksProcReceive|TestPreReceiveHookReturnsWithoutWaitingForResponse|TestInstallProtectedReceiveHooksRoutesHeadsAndTagsThroughProcReceive|TestRealGitClientProtectedReceiveDenyRejectsWholeNonAtomicPush|TestRealGitClientProtectedReceiveAllowsCanonicalTagAndOrdinaryBranch' -count=5

go test -race ./mod/git -run 'TestReceiveHookSessionCancellationClosesAcceptedConnection|TestReceiveHookSessionWaitAllowsSuccessfulBranchOnlyReceive|TestReceiveHookClientCancellationUnblocksProcReceive|TestPreReceiveHookReturnsWithoutWaitingForResponse|TestInstallProtectedReceiveHooksRoutesHeadsAndTagsThroughProcReceive|TestRealGitClientProtectedReceiveDenyRejectsWholeNonAtomicPush|TestRealGitClientProtectedReceiveAllowsCanonicalTagAndOrdinaryBranch' -count=5
```

本轮最终门禁已全部通过：

```bash
git diff --check
go test ./...
go test -race ./...
go vet ./...
go build ./...
```

另以 `-count=1` 执行 `cmd/server`、`mod/git`、Plugin domain/receive/recovery、distribution projection 与 Plugin handler 聚焦测试，全部通过；新增 raw-tag CAS、publish/restore history、recovery manualization/pointer CAS、unresolved receive management conflict 回归覆盖。

未执行/不适用覆盖：

- PostgreSQL integration：未提供 `MARKETPLACE_TEST_POSTGRES_DSN`；
- SSH Git：当前未实现；
- 浏览器验证使用 SQLite 与测试 validator shim；生产 `claude plugin validate --strict` 行为由 Git 模块测试和既有真实 validator 审计覆盖。当前本机 Claude CLI 版本不接受 `--strict`，该运行时兼容性仍应由 startup capability probe 提前暴露。

## 复现流程摘要

1. 配置 SQLite、可写 Git storage root 和 `claude` validator。
2. 设置 bootstrap password、有效 Base64 API-key pepper 和 JWT secret。
3. 执行 `marketplace-server server -c <config>`。
4. 使用 bootstrap `admin` 登录并创建 `git-write` PAT。
5. 在 `admin` namespace 创建 public Plugin。
6. 提交 strict-valid `.claude-plugin/plugin.json`，manifest name 与 Plugin slug 完全一致。
7. 使用标准 Basic `base64(username:PAT)` 通过 Smart HTTP 推送 `main` 和 canonical tag。
8. 发布 tag 并设为 default。
9. 验证 Version、archive push deny、restore 与 public upload-pack。
10. 对 receive 变更执行真实 mixed non-atomic push、partial socket cancellation 和 race 回归。
