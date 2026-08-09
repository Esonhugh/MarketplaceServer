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

### 固定运行时模块

MarketplaceServer 只注册以下五个顶层 `kernel.Module`。用户口语中的“四个模块”实际列出了五个名称，以这张表为准：

| 模块 | 职责 | DI 输入/输出 |
|---|---|---|
| `jin` | 创建并注入 `*jin.Engine`，在现有 cmux listener 上运行 HTTP server | consume `cmux.CMux`；produce `*jin.Engine` |
| `sql` | 创建、校验并关闭 GORM SQL 后端 | produce `*gorm.DB` |
| `git` | bare repository storage、Git Smart HTTP、SSH Git、refs/reconciliation | consume `*jin.Engine`, `*gorm.DB`；produce Git service contract |
| `backend` | 所有身份、团队、RBAC、Plugin、Version、Marketplace、Audit 的模型、service 和 `/api/v1` API | consume `*jin.Engine`, `*gorm.DB`, Git service |
| `frontend` | Svelte + Tailwind 静态构建与 `embed.FS` 托管 | consume `*jin.Engine` |

- `cmd/server/modList/list.go` 只能注册这五个模块。`b2x`、`grpcGateway`、`jinPprof`、旧 `jinx`、`myDB`、`pgsql`、`pyroscope`、`rds`、`uptrace` 和 `example` 可以暂时保留源码供迁移参考，但不得进入运行时模块清单。
- `identity`、`teams`、`authorization`、`plugins`、`versions`、`marketplaces`、`audit` 是 `backend` 内部领域 package，不是独立 kernel module。
- `backend` 可以按 `domain/<name>`、`handler/<name>`、`dao/<name>` 分包，但只能由一个 `backend.Mod` 统一装配和注册 API。
- Git protocol、仓库文件系统和 Git 子进程必须留在 `git`；普通 CRUD、权限和发布编排留在 `backend`。双方通过窄 Git service contract 通信，不跨模块访问内部 DAO/model。

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
- 每个已发布模板必须有稳定的分发 URL，例如：
  - `/distribution/marketplaces/{marketplacePublicKey}/marketplace.json`
  - `/distribution/marketplaces/{marketplacePublicKey}.git`
- Marketplace 首次创建时根据名称生成 `{normalized-marketplace-name}-{8-lowercase-hex}` 格式的 `marketplacePublicKey`，唯一持久化后保持不可变；改名不得重算或替换该 key。
- HTTP JSON 与只读 Git Marketplace 是并列安装方式，共享同一个不可变 `marketplacePublicKey`，管理页面同时展示两套完整步骤，不假定其中一种是默认方式。
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
- 私有模板与其中的私有 Plugin 必须同时通过权限检查。不得因为拿到索引 URL、Marketplace public key 或 Plugin distribution UUID 就绕过读取权限；Marketplace public key 与 Plugin distribution UUID 都不是访问凭据。
- 禁止在索引 URL、JSON 或 clone URL 中泄露分发密码、长期 Token、私钥或服务器文件路径。Private Git URL 可以包含非敏感且不可变的 BasicAuth username，以便 Git Credential Helper 选择 Marketplace 对应凭据，但绝不能包含 password。

## 领域数据模型

### 主体与租户

| 实体 | 关键字段 | 约束与说明 |
|---|---|---|
| `users` | `id`, `username`, `email`, `display_name`, `status`, `password_hash` | `username`、规范化 email 全局唯一；状态至少有 `active/disabled`；密码只存强哈希 |
| `user_emails`（可选） | `user_id`, `email`, `verified_at`, `primary` | 需要多邮箱时启用；同一规范化 email 不能属于多个有效用户 |
| `service_accounts` | `id`, `namespace_id`, `name`, `status` | 非人类主体，用于 CI 和自动发布；权限与 Token 独立管理 |
| `namespaces` | `id`, `kind`, `slug`, `display_name`, `owner_user_id` | `kind=user/team`；所有可共享资源统一引用 namespace；slug 全局唯一以形成稳定 URL |
| `teams` | `id`, `namespace_id`, `description`, `status` | 与 team namespace 一对一；团队业务字段不要塞入 namespace |
| `team_members` | `team_id`, `user_id`, `role`, `status`, `joined_at` | `(team_id,user_id)` 唯一；必须始终保留至少一个有效 owner |
| `team_invitations` | `team_id`, `email`, `role`, `token_hash`, `expires_at`, `accepted_at` | 邀请 Token 只存哈希；接受时重新校验邮箱、过期时间和当前授权 |

- 用户创建时同时创建个人 namespace，两个记录必须在同一数据库事务中完成。
- `namespace_id` 是租户范围的标准外键；`org_id` 只可作为兼容字段逐步淘汰，不应成为新领域模型的第二套租户概念。
- 用户禁用后立即拒绝新登录、Token、Git HTTPS 和 SSH 请求，但不删除其历史审计或团队资源。

### 凭据与会话

| 实体 | 关键字段 | 约束与说明 |
|---|---|---|
| `personal_access_tokens` | `subject_type`, `subject_id`, `name`, `token_hash`, `scopes`, `expires_at`, `last_used_at`, `revoked_at` | 只在创建时返回一次明文；数据库只存带 pepper 的哈希；scope 不能扩大主体已有权限 |
| `ssh_keys` | `user_id`, `name`, `algorithm`, `public_key`, `fingerprint`, `last_used_at`, `revoked_at` | fingerprint 对有效 key 唯一；禁止 DSA 等不安全算法；系统不接收用户私钥 |
| `sessions` | `user_id`, `refresh_token_hash`, `expires_at`, `revoked_at`, `ip`, `user_agent` | 若采用 access/refresh token；支持单会话撤销与全账户撤销 |
| `ssh_host_keys` | 不入普通业务表 | 通过受保护文件/secret mount 持久化；滚动时支持多 host key 过渡 |

- 密码建议使用 Argon2id；参数随哈希保存并支持登录时升级。
- 浏览器优先使用 `HttpOnly + Secure + SameSite` cookie；API/PAT 使用 Bearer Token。不要把长期 Token 放入 URL 或 localStorage。
- access token 短期有效；refresh token 旋转，发现重用时撤销对应 token family。

### Private 分发凭据

| 实体 | 关键字段 | 约束与说明 |
|---|---|---|
| `marketplace_distribution_credentials` | `id`, `marketplace_template_id`, `user_id`, `name`, `secret_hmac`, `expires_at`, `last_used_at`, `revoked_at`, `created_at` | 同一用户和 Marketplace 可有多个按设备/用途命名的有效凭据；password 为独立随机 UUID v4，明文只在创建时显示一次 |

- 每个 Marketplace 创建一个不可变、可公开的 BasicAuth username，例如 `mp-distrib-marketplaceA-7sbc`；显示名称或 slug 改名不得改变该 username。username 不代表用户身份，也不是秘密。
- 分发 password 是独立随机 UUID v4，不得复用 Marketplace、Plugin、version 或 distribution record 的 UUID。所谓“一次展示”只表示明文只返回一次；Git clone/fetch 会产生多个请求，因此密码在撤销或过期前必须可重复使用。
- 数据库不得保存 password 明文。使用 `HMAC-SHA-256(key=pepper, message=password)` 生成 `secret_hmac` 并建立唯一索引，用于等值定位凭据；pepper 通过 secret manager/runtime secret 提供，不进入普通配置或数据库。
- 凭据范围固定为 `user + marketplace_template`，只允许 `marketplace.read` 和该 Marketplace 已发布 distribution 的 `distribution.read`，永远不能用于 `repository.write`、发布或管理 API。同一用户和 Marketplace 可以创建多个凭据，以便按设备独立撤销。
- 创建凭据默认无固定过期时间、持续到撤销；同时允许用户选择固定有效期或长期有效。账号 disabled、用户离开目标团队或其他原因导致当前 `marketplace.read` 被拒绝时，即使凭据尚未撤销/过期，每次请求也必须动态拒绝。
- Private Marketplace 的 HTTP JSON 和只读 Git 索引都接受同一组 BasicAuth 凭据。Git Marketplace 与 Plugin Git 由 Git Credential Helper 获取凭据；HTTP JSON 不假定读取 `.git-credentials`，Claude Code settings 必须通过环境变量构造对应 Basic `Authorization` header。
- 第一阶段只提供手工 `.git-credentials` 安装说明，不开发自定义 helper。说明必须明确该文件是明文凭据存储、建议设置仅用户可读写权限，并提供撤销/删除步骤；不得把带 password 的命令作为默认复制内容写入 shell history。

### 仓库、Plugin 与版本

| 实体 | 关键字段 | 约束与说明 |
|---|---|---|
| `repositories` | `id`, `namespace_id`, `slug`, `visibility`, `default_branch`, `storage_key`, `status`, `size_bytes` | `(namespace_id,slug)` 唯一；`storage_key` 是服务端生成的不透明路径键；一个仓库只绑定一个 Plugin |
| `repository_refs` | `repository_id`, `ref_name`, `object_id`, `object_type`, `observed_at` | `(repository_id,ref_name)` 唯一；是 Git refs 的可重建镜像，不是权威来源 |
| `protected_refs` | `repository_id`, `pattern`, `allow_force_push`, `allow_delete`, `required_permission` | 版本 tag 默认保护；规则匹配必须确定且有测试 |
| `plugins` | `id`, `namespace_id`, `repository_id`, `slug`, `display_name`, `description`, `visibility`, `status`, `latest_version_id` | `(namespace_id,slug)` 和 `repository_id` 唯一；公开性不能高于仓库可读性 |
| `plugin_versions` | `id`, `plugin_id`, `version`, `tag_name`, `commit_sha`, `manifest_digest`, `manifest_snapshot`, `status`, `published_by`, `published_at` | `(plugin_id,version)`、`(plugin_id,tag_name)` 唯一；commit SHA 必须完整且发布后不可改 |
| `plugin_version_events` | `version_id`, `from_status`, `to_status`, `actor_id`, `reason`, `created_at` | 记录发布、撤回等状态转换，不能用更新覆盖历史 |

- `manifest_snapshot` 保存发布时规范化后的 manifest，便于审计和复现；原始内容仍可从 commit 读取。
- `latest_version_id` 只是加速字段，必须可由有效版本重建。
- 数据库软删除不能立即物理删除 bare repository；物理回收由延迟任务执行并可取消。

### Marketplace 模板与修订

| 实体 | 关键字段 | 约束与说明 |
|---|---|---|
| `marketplace_templates` | `id`, `namespace_id`, `slug`, `name`, `description`, `visibility`, `status`, `published_revision_id` | `(namespace_id,slug)` 唯一；管理 URL 使用 namespace + slug；`id` 和 `published_revision_id` 保持 UUID，不作为 Marketplace public route key |
| `marketplace_draft_items` | `template_id`, `plugin_id`, `version_selector`, `transport`, `position`, `overrides` | `(template_id,plugin_id)` 唯一；`transport=https/ssh` 也可由发布请求统一指定 |
| `marketplace_revisions` | `id`, `template_id`, `revision`, `transport`, `content_json`, `content_digest`, `created_by`, `published_at` | `(template_id,revision,transport)` 唯一；内容不可变；digest 基于最终响应字节 |
| `marketplace_revision_items` | `revision_id`, `plugin_id`, `plugin_version_id`, `distribution_id`, `source_url`, `commit_sha`, `position` | 保存解析后的精确版本、distribution 和 source，禁止保存浮动 selector |
| `marketplace_distributions` | `id`, `template_id`, `public_key`, `basic_auth_username`, `current_projection_id`, `status`, `created_at`, `revoked_at` | `id`、所有 FK 和 pointer 保持 UUID；`public_key` 在 Marketplace 首次创建时按 `{normalized-marketplace-name}-{8-lowercase-hex}` 生成、全局唯一并持久化为不可变 public route key；`basic_auth_username` 是不可变公开别名；publish/rollback 只原子切换投影指针 |
| `marketplace_distribution_projections` | `id`, `marketplace_distribution_id`, `revision_id`, `storage_key`, `content_digest`, `created_at` | 每个 revision 的 Git 索引投影不可变；构建完成后只读，稳定 URL 通过 `current_projection_id` 选择当前投影 |
| `plugin_distributions` | `id`, `template_id`, `plugin_id`, `plugin_version_id`, `repository_id`, `tag_name`, `tag_object_id`, `commit_sha`, `storage_key`, `status`, `created_at`, `revoked_at` | `id` 是随机 UUID；`(template_id,plugin_id,plugin_version_id)` 数据库唯一；创建后身份、tag、SHA 和 storage key 不可修改 |

- draft item 可以使用 `latest-compatible` 等选择器，但发布修订必须解析为确切 `plugin_version_id + commit_sha`。
- HTTPS/SSH 若输出内容不同，应生成独立 revision variant；两者共享业务 revision 号但 digest 独立。
- 稳定 URL 只切换 `published_revision_id`；旧 revision 永远可按不可变 URL读取，除非因安全事件显式撤回。
- 同一 Marketplace 的多个 revision 重复引用同一 Plugin version 时复用 `(template_id,plugin_id,plugin_version_id)` 对应的 `plugin_distributions.id`；新 revision 不再引用时不得自动关闭，因为旧 revision 仍需可复现。只有版本/Marketplace 安全撤回或显式审计过的 revoke 才停止未来下载。
- 每个 Plugin distribution 只分发该 version 发布时确认的单个完整 tag，例如 `refs/tags/v1.2.3`；只注册 `info/refs?service=git-upload-pack` 和 `git-upload-pack`，不注册 receive-pack、upload-archive、Dumb HTTP 或其他 service。
- distribution 必须使用独立只读 bare 投影，只包含指定 tag，并保存 annotated tag object ID 与 peeled commit SHA。不得仅依赖原开发仓库的 hideRefs/namespace，因为它们不是 object 隔离或保密边界。
- 分发请求面绝不修改开发 Git 仓库或任何分发投影。Plugin version 发布 worker 只能从开发仓库只读解析指定 tag，在 quarantine 临时目录构建全新的投影并验证后原子移动到分发存储；Plugin 投影激活后只能整体 revoke，修复或新版本必须创建新的 distribution UUID。
- Marketplace 稳定 Git URL 以持久化且不可变的 `marketplacePublicKey` 定位内部 UUID distribution，再通过可更新的 `current_projection_id` UUID pointer 选择不可变投影：显式 publish 构建并验证一个全新的不可变 Marketplace 索引投影后原子切换该 pointer；rollback 只复用并验证已有历史投影后切换 pointer，不重建或修改 Git。禁止对任何已激活投影执行原地 commit、update-ref 或对象写入。
- Plugin tag 投影创建后不可移动或改指向。开发仓库 tag 漂移只产生 integrity violation 和告警，不修改现有 distribution；若内容需要修复，发布新 Plugin version 和新 distribution UUID。
- Plugin distribution 必须始终净化为独立快照：从发布 tag 的 tree 创建新的无父级 distribution commit，并创建与原 tag 同名的单个 lightweight tag。数据库分别保留 source tag 类型、可选 annotated tag object ID、peeled source commit SHA、source tree SHA 与 distribution SHA；Marketplace `source.sha` 必须使用 distribution SHA，因此不会暴露源提交或父历史。

### 审计与异步任务

| 实体 | 关键字段 | 约束与说明 |
|---|---|---|
| `audit_events` | `id`, `occurred_at`, `actor_type`, `actor_id`, `namespace_id`, `action`, `resource_type`, `resource_id`, `request_id`, `ip`, `metadata` | append-only；metadata 必须脱敏；按 namespace、actor、resource、time 建索引 |
| `outbox_events` | `id`, `topic`, `aggregate_type`, `aggregate_id`, `payload`, `available_at`, `attempts`, `processed_at`, `last_error` | 与业务状态同事务写入；worker 至少一次投递，消费者必须幂等 |
| `jobs`（若需要） | `id`, `kind`, `dedupe_key`, `state`, `payload`, `attempts`, `lease_until` | Git reconciliation、物理回收、索引重建；`dedupe_key` 防止重复并发执行 |

### 数据库通用规则

- 所有业务主键使用 ULID/UUID 等不可枚举 ID；外部 URL 使用经过校验的 slug，不暴露自增序列。
- 所有表包含明确的创建/更新时间；需要软删除的表使用 `deleted_at`，不可变事件表不软删除。
- 外键和唯一约束必须由数据库强制，不仅依赖 service 检查。
- JSON 字段只承载边缘 metadata/snapshot，不替代可查询、有关联约束的核心列。
- 所有 DAO 查询必须显式带 namespace 或通过已解析的资源 ID 访问；列表接口禁止先全局查询再在内存中过滤。

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
- authorization 输入统一为 `Principal + Action + Resource + Context`；返回 allow/deny 和可审计 reason，不返回模糊角色判断。
- 系统管理员权限和团队 owner 权限必须区分；系统管理员跨租户操作必须显式进入管理路径并记录审计。
- 公开资源只对 read 类 action 匿名放行；匿名主体永远不能创建、写入、发布或查看私有元数据。

### 权限动作与角色矩阵

`✓` 表示角色默认拥有，`—` 表示默认拒绝；资源级自定义授权可进一步收紧，但不得隐式扩大 owner 以外的高风险权限。

| Action | owner | admin | maintainer | developer | viewer |
|---|:---:|:---:|:---:|:---:|:---:|
| `team.read` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `team.update` | ✓ | ✓ | — | — | — |
| `team.delete` / `team.transfer` | ✓ | — | — | — | — |
| `team.member.read` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `team.member.manage` | ✓ | ✓ | — | — | — |
| `repository.read` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `repository.create` | ✓ | ✓ | ✓ | ✓ | — |
| `repository.write` | ✓ | ✓ | ✓ | ✓ | — |
| `repository.settings` | ✓ | ✓ | ✓ | — | — |
| `repository.delete` | ✓ | ✓ | — | — | — |
| `repository.protected_ref.manage` | ✓ | ✓ | ✓ | — | — |
| `plugin.read` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `plugin.create/update` | ✓ | ✓ | ✓ | ✓ | — |
| `plugin.publish/yank` | ✓ | ✓ | ✓ | — | — |
| `plugin.delete` | ✓ | ✓ | — | — | — |
| `marketplace.read` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `marketplace.draft.write` | ✓ | ✓ | ✓ | ✓ | — |
| `marketplace.publish/rollback` | ✓ | ✓ | ✓ | — | — |
| `marketplace.delete` | ✓ | ✓ | — | — | — |
| `audit.read` | ✓ | ✓ | — | — | — |

- 个人 namespace 的用户视为该 namespace owner，但仍必须经过同一个 authorization service。
- PAT scope 是角色权限的交集，例如主体拥有 `repository.write` 但 Token 只有 `repository.read` 时必须拒绝 push。
- 受保护 ref、删除、所有权转移等动作在角色授权后仍要通过资源规则二次判定。

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
- `/api/`、`/gapi/`、`/git/`、`/distribution/`、`/marketplaces/`、`/healthz`、`/debug/`、`/metrics` 等后端路径禁止进入 SPA fallback。
- SPA history fallback 只处理适合前端导航的 `GET`/`HEAD` 请求；静态文件不存在且路径没有文件扩展名时才返回 `index.html`，API/Git 路径和缺失的 `.js`/`.css` 等资源必须返回 404。
- 带内容哈希的资源使用长期不可变缓存；`index.html` 和运行时配置使用 `no-cache` 或短缓存。
- 正确设置 MIME、`Content-Length`、`ETag`/修改时间，并支持 HEAD。
- 禁止把服务器密钥、数据库凭据、Git 凭据或私有运行配置编译进前端 bundle。
- 浏览器权限只用于体验优化，服务端仍必须对每个 API 操作授权。

### 开发模式

- Svelte dev server 仅用于本地开发，并将 API/Git 相关路径代理到 Go 服务；若 Go 端提供 dev proxy，目标只能是 loopback 地址且生产环境必须拒绝启用，避免形成 SSRF/open proxy。
- 生产和集成测试必须验证 Go `embed.FS` 实际提供的静态构建，而不只验证 dev server。
- 前端至少覆盖登录、用户/团队、成员权限、Plugin、仓库 clone 信息、版本发布、Marketplace 模板和发布 URL 等管理流程。

### 前端信息架构

```text
/login
/setup                           # 仅首次初始化且服务端明确允许
/dashboard
/settings/profile
/settings/security              # password、sessions、PAT、SSH keys
/teams
/teams/:team
/teams/:team/members
/:namespace/repositories
/:namespace/repositories/:repo
/:namespace/repositories/:repo/settings
/:namespace/plugins
/:namespace/plugins/:plugin
/:namespace/plugins/:plugin/versions
/:namespace/plugins/:plugin/versions/:version
/:namespace/marketplaces
/:namespace/marketplaces/:template
/:namespace/marketplaces/:template/edit
/:namespace/marketplaces/:template/revisions
/:namespace/audit
/admin                           # 仅系统管理员；与团队管理分离
```

主要导航：

- 顶部 namespace switcher：个人空间和所属团队；切换后列表请求必须携带 URL namespace，不依赖隐式全局状态。
- Repository 详情：clone URL（HTTPS/SSH 切换）、默认分支、refs、保护规则、容量和最近 push；不实现浏览器内任意 shell。
- Plugin 详情：元数据、manifest 校验结果、仓库链接、版本列表、发布/撤回动作。
- Marketplace 编辑器：Plugin 搜索、版本选择、排序、HTTPS/SSH 预览、schema 校验、发布差异和稳定 URL。
- Security：PAT 明文只显示一次；SSH key 展示 fingerprint；撤销需要确认并刷新服务端数据。
- Audit：按 actor/action/resource/time 筛选，仅展示脱敏 metadata。

前端状态规则：

- 服务端数据使用集中 query/cache 层；认证主体和 namespace 是全局状态，编辑表单状态保持页面局部。
- 任何 mutation 成功后按资源 key 精确失效缓存；不能靠整页刷新掩盖状态错误。
- 权限响应可控制按钮可见性，但提交时必须处理服务端 401/403/409；前端不推导最终授权。
- destructive/publish 操作使用二次确认，显示资源名和影响；高风险操作不做 optimistic update。
- 所有页面提供 loading、empty、403、404、conflict、retry 状态；错误信息显示 request ID 便于审计排障。
- 基础无障碍要求：键盘可操作、可见焦点、语义 label、对比度合格、dialog 管理焦点；不得只用颜色表达状态。

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

### 模块 contracts 与依赖图

跨模块 contract 放在稳定、无实现依赖的 package 中，例如 `internal/contracts/<domain>` 或 `pkg/contracts/<domain>`。contract 只包含跨模块需要的接口、ID/value object 和命令/结果 DTO；禁止暴露 GORM model、jin context 或具体 DAO。

建议核心 contract：

```go
type Principal struct {
    Type string // anonymous, user, serviceAccount
    ID   string
}

type Authorizer interface {
    Authorize(ctx context.Context, principal Principal, action string, resource ResourceRef) error
}

type RepositoryService interface {
    Resolve(ctx context.Context, namespace, slug string) (Repository, error)
    Create(ctx context.Context, cmd CreateRepository) (Repository, error)
    ReceivePack(ctx context.Context, principal Principal, repoID string, stdin io.Reader, stdout io.Writer) error
    UploadPack(ctx context.Context, principal Principal, repoID string, stdin io.Reader, stdout io.Writer) error
    ReadFileAtCommit(ctx context.Context, repoID, commitSHA, path string) ([]byte, error)
    ResolveRef(ctx context.Context, repoID, ref string) (string, error)
}

type PluginService interface {
    Get(ctx context.Context, principal Principal, id string) (Plugin, error)
    ValidateManifest(ctx context.Context, pluginID, commitSHA string) (ManifestSnapshot, error)
}

type VersionService interface {
    Publish(ctx context.Context, principal Principal, cmd PublishVersion) (Version, error)
    Yank(ctx context.Context, principal Principal, versionID, reason string) error
}

type MarketplaceService interface {
    Publish(ctx context.Context, principal Principal, cmd PublishMarketplace) (Revision, error)
    GetPublished(ctx context.Context, principal Principal, namespace, slug, transport string) (PublishedIndex, error)
}

type AuditWriter interface {
    Append(ctx context.Context, event AuditEvent) error
}
```

- 接口按使用者所需能力设计，不创建包含整个领域的“万能 service”。
- `RepositoryService` 的 Git stream 方法不得把命令字符串暴露给 handler；service 内部只接受已验证的 repo ID 和枚举操作。
- audit/outbox 写入若必须与业务状态原子一致，由拥有该事务的领域 service 直接写 DAO；通用 `AuditWriter` 只用于不要求同事务的读侧/协议事件。

目标依赖图：

```text
cmux ──→ jin ───────────────┬────────────→ frontend
                            │
sql ─────────→ git ─────────┴────────────→ backend
                 │                          │
                 └── Git service contract ─┘

backend internal domains:
identity → teams → authorization → plugins → versions → marketplaces
                                      └──────────────→ audit/outbox
```

五个顶层模块保持稳定；业务复杂度只在 `backend` 内部分层增长，不新增 kernel modules。为适配当前有限生命周期，首次功能实现前仍应优先修复 kernel 为确定性有序模块并补显式依赖检查。

### 模块生命周期/DI 清单

| 模块 | Config | 创建/Map | Load/消费 | Start/Stop |
|---|---|---|---|---|
| `jin` | HTTP timeouts | PreInit 创建并 Map `*jin.Engine` | Start consume `cmux.CMux` | Serve/Shutdown HTTP server |
| `sql` | driver、DSN、pool | PreInit 打开并 Map `*gorm.DB` | Init ping | Stop 关闭底层 `sql.DB` |
| `git` | storage root、public URLs、SSH addr、limits | Init/PostInit consume DB/Jin 并 Map Git service | Load 注册 Smart HTTP | Start SSH/reconcile；Stop drain |
| `backend` | auth、policy、publish、audit 设置 | PostInit consume DB/Git，组装内部领域 services | Load 注册 `/api/v1` 和 Marketplace JSON | Start/Stop 内部 workers |
| `frontend` | disabled/basePath/devServer | 不 Map | Load consume Jin 并设置 `NoRoute` | 无；dev transport 可关闭 idle conn |

- 当前 kernel 尚未具备显式 DAG。注册顺序固定为 `jin → sql → git → backend → frontend`，同时代码仍应尽量使用跨阶段生产/消费，避免同阶段隐式依赖。
- backend 内部领域对象不 Map 到全局 DI；只有确有跨顶层模块用途的窄 contract 才允许 Map。

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
- Git storage root 必须由配置提供。当前未实现的外部访问 base URL、SSH host/port 不得提前加入配置；配额和超时先使用集中且有界的代码默认值，只有出现真实部署调优需求时才暴露为 operator 配置。

## HTTP 与分层约定

### 统一路由边界

- 管理 REST API：`/api/v1/...`
- gRPC Gateway：保留 `/gapi/...`
- 用户开发 Git Smart HTTP：`/git/{namespace}/{repository}.git/...`
- Claude Code Marketplace 分发：`/distribution/marketplaces/{marketplacePublicKey}.git/...` 与 `/distribution/marketplaces/{marketplacePublicKey}/marketplace.json`
- Claude Code Plugin version 分发：`/distribution/plugins/{distributionUUID}.git/...`
- 前端：`/` 及非保留 SPA 路径

管理 API、用户开发 Git 和 Claude Code 分发是三个独立渠道。新增路由时不得与上述边界冲突；它们共享 Backend 的资源、发布和 authorization 语义，但不得共享 handler 或通过 User-Agent/可伪造 header 判断是否为 Claude Code。公开读取和需要认证的操作必须在路由与 service 两层都清晰区分。

### REST API 设计

统一约定：

- JSON 管理 API 前缀固定 `/api/v1`；错误响应至少包含稳定 `code`、面向用户的 `message` 和 `requestId`，不得把内部堆栈返回客户端。
- 创建成功返回 `201`；异步任务返回 `202`；无响应体删除返回 `204`；并发版本冲突返回 `409`；业务验证返回 `422`；限流返回 `429`。
- 列表统一使用 cursor pagination：`?limit=50&cursor=...`，返回 `items` 和 `nextCursor`。cursor 不得暴露未签名的内部查询状态。
- 可变资源返回 `ETag` 或 `version`，更新/删除支持 `If-Match`，防止管理页面覆盖他人修改。
- namespace 使用 URL slug，service 必须解析为 namespace ID 后再进行授权和数据库查询。

核心 endpoint：

```text
POST   /api/v1/auth/login
POST   /api/v1/auth/refresh
POST   /api/v1/auth/logout
GET    /api/v1/me
GET    /api/v1/me/tokens
POST   /api/v1/me/tokens
DELETE /api/v1/me/tokens/{tokenId}
GET    /api/v1/me/ssh-keys
POST   /api/v1/me/ssh-keys
DELETE /api/v1/me/ssh-keys/{keyId}

GET    /api/v1/teams
POST   /api/v1/teams
GET    /api/v1/teams/{team}
PATCH  /api/v1/teams/{team}
DELETE /api/v1/teams/{team}
GET    /api/v1/teams/{team}/members
POST   /api/v1/teams/{team}/invitations
PATCH  /api/v1/teams/{team}/members/{userId}
DELETE /api/v1/teams/{team}/members/{userId}

GET    /api/v1/namespaces/{namespace}/repositories
POST   /api/v1/namespaces/{namespace}/repositories
GET    /api/v1/namespaces/{namespace}/repositories/{repo}
PATCH  /api/v1/namespaces/{namespace}/repositories/{repo}
DELETE /api/v1/namespaces/{namespace}/repositories/{repo}
GET    /api/v1/namespaces/{namespace}/repositories/{repo}/refs
GET    /api/v1/namespaces/{namespace}/repositories/{repo}/protected-refs
PUT    /api/v1/namespaces/{namespace}/repositories/{repo}/protected-refs/{ruleId}

GET    /api/v1/namespaces/{namespace}/plugins
POST   /api/v1/namespaces/{namespace}/plugins
GET    /api/v1/namespaces/{namespace}/plugins/{plugin}
PATCH  /api/v1/namespaces/{namespace}/plugins/{plugin}
DELETE /api/v1/namespaces/{namespace}/plugins/{plugin}
POST   /api/v1/namespaces/{namespace}/plugins/{plugin}/validate
GET    /api/v1/namespaces/{namespace}/plugins/{plugin}/versions
POST   /api/v1/namespaces/{namespace}/plugins/{plugin}/versions
GET    /api/v1/namespaces/{namespace}/plugins/{plugin}/versions/{version}
POST   /api/v1/namespaces/{namespace}/plugins/{plugin}/versions/{version}/yank

GET    /api/v1/namespaces/{namespace}/marketplaces
POST   /api/v1/namespaces/{namespace}/marketplaces
GET    /api/v1/namespaces/{namespace}/marketplaces/{template}
PATCH  /api/v1/namespaces/{namespace}/marketplaces/{template}
DELETE /api/v1/namespaces/{namespace}/marketplaces/{template}
PUT    /api/v1/namespaces/{namespace}/marketplaces/{template}/draft/items
POST   /api/v1/namespaces/{namespace}/marketplaces/{template}/validate
POST   /api/v1/namespaces/{namespace}/marketplaces/{template}/publish
GET    /api/v1/namespaces/{namespace}/marketplaces/{template}/revisions
POST   /api/v1/namespaces/{namespace}/marketplaces/{template}/rollback/{revision}

GET    /api/v1/namespaces/{namespace}/audit-events
```

- Plugin 创建可以原子创建对应 repository，或绑定同 namespace 下未绑定且空的 repository；不能绑定其他 namespace 的仓库。
- `POST .../versions` 的请求应包含 `version + ref/tag`，service 将 ref 解析为完整 SHA 后校验，不接受客户端声称的 manifest snapshot。
- `publish` 是显式命令 endpoint，不通过 `PATCH status=published` 隐式触发高风险副作用。
- API schema 稳定后生成 OpenAPI；前端类型由 schema 生成或集中维护，禁止页面各自定义漂移的响应类型。

### Git Smart HTTP 协议

标准路径固定：

```text
GET  /git/{namespace}/{repo}.git/info/refs?service=git-upload-pack
POST /git/{namespace}/{repo}.git/git-upload-pack
GET  /git/{namespace}/{repo}.git/info/refs?service=git-receive-pack
POST /git/{namespace}/{repo}.git/git-receive-pack
```

处理顺序必须是：

1. 严格解析 namespace/repo，去除单个预期 `.git` 后缀，拒绝编码斜杠、NUL、`..` 和重复分隔符。
2. 识别且 allowlist `git-upload-pack` 或 `git-receive-pack`，未知 service 返回 404/403，不传递给 Git。
3. 从 Basic/Bearer credential 解析 Principal；不得在日志记录 Authorization。
4. 解析 repository ID 与 visibility，再分别检查 `repository.read` 或 `repository.write`。
5. 对 receive-pack 额外执行只读状态、配额、并发 push 和 protected ref 检查。
6. 用 `exec.CommandContext` 调用受控 Git binary 和服务端解析后的绝对路径；设置最小环境，禁止继承危险 `GIT_*` 变量。
7. 正确转发 Git content type 和禁缓存头；限制 header/body，流式传输，客户端断开时取消子进程。
8. push 成功后写 outbox 事件，由幂等消费者同步 refs、触发审计和 manifest/version 检查。

- Smart HTTP 必须实现 protocol v0/v1 的基本兼容；支持 protocol v2 前先增加真实 Git client 互操作测试。
- 不自行解析/重写 packfile。第一版复用系统 Git plumbing，业务层只处理认证、授权、路径和生命周期。
- public repository 可匿名 upload-pack；receive-pack 永远需要有效主体。

### Claude Code 分发协议

固定路由：

```text
GET  /distribution/marketplaces/{marketplacePublicKey}.git/info/refs?service=git-upload-pack
POST /distribution/marketplaces/{marketplacePublicKey}.git/git-upload-pack
GET  /distribution/marketplaces/{marketplacePublicKey}/marketplace.json
GET  /distribution/plugins/{distributionUUID}.git/info/refs?service=git-upload-pack
POST /distribution/plugins/{distributionUUID}.git/git-upload-pack
```

- `{marketplacePublicKey}` 必须是首次创建时生成并持久化的 `{normalized-marketplace-name}-{8-lowercase-hex}`；它全局唯一且不可变，是公开定位符而不是凭据。Marketplace internal ID、FK 与 projection pointer 均继续使用 UUID；不得注册或兼容旧的 Marketplace UUID public route。Plugin distribution public route 继续使用 `{distributionUUID}`。
- Marketplace Git 投影只承载 `.claude-plugin/marketplace.json`；Plugin distribution 只承载 `(Marketplace, Plugin, PluginVersion)` 唯一关系指定的 tag。分发 handler 只能根据当前指针打开并读取已 `active` 的不可变投影，不得调用 `InitBareRepository`、update-ref、receive-pack 或其他会修改 Git 仓库/对象/refs 的操作，也不得注册任何写路由。Marketplace publish/rollback 的新投影构建与指针切换属于 Backend 发布流程，不属于分发请求 handler。
- Public Marketplace/Plugin distribution 可以匿名读取。Private 请求必须使用 HTTPS BasicAuth；username 是 Marketplace 的不可变公开别名，password 由用户自己的 Git Credential Helper 或 HTTP Authorization header 提供。
- Private 认证流程必须先以 `HMAC-SHA-256(key=pepper, message=password)` 定位有效凭据，再验证 username 与 Marketplace 匹配、凭据未撤销/未过期、用户 active，最后动态调用 Authorizer 检查当前 `marketplace.read` 与目标 distribution 归属。认证失败使用一致的 401/404 策略，不泄露 UUID、用户或资源是否存在。
- Private Plugin URL 使用 `https://{publicUsername}@host/distribution/plugins/{distributionUUID}.git` 让 Git Credential Helper 区分 Marketplace，但不得嵌入 password。同域 `/api/v1`、`/git` 和 `/distribution` 仍须分别按 scope 授权，分发密码发送到其他渠道时必须拒绝。
- HTTP JSON 的 Basic header 与 Git 凭据独立传输：Claude Code settings 使用环境变量提供 `Authorization: Basic ...`，不得假定 Marketplace HTTP header 会转发给 Plugin Git，也不得假定 HTTP 下载会查询 `.git-credentials`。
- Public stable JSON 使用强 ETag 和 `no-cache`/短缓存；不可变 revision 可长期 `immutable`；Private JSON 使用 `private, no-store`。Git `info/refs` 禁止缓存，`git-upload-pack` POST 不可缓存。
- 分发读取必须限制请求大小、并发和超时，并记录 Marketplace、distribution、credential/user、结果和 request ID；不记录 BasicAuth、password、pack 内容或完整敏感 URL。

### Git SSH 协议

- SSH 使用独立 listener/configured port，不与当前 HTTP/gRPC cmux 强行复用。
- 服务端只允许 `session` channel 和 Git exec request；拒绝 shell、pty、subsystem、port forwarding、agent forwarding、X11 和任意环境变量。
- exec command 只接受严格语法 `git-upload-pack '<namespace>/<repo>.git'` 或 `git-receive-pack '<namespace>/<repo>.git'`；解析后转换为枚举操作和 repo ID，不把原命令交给 shell。
- 通过公钥 fingerprint 找到 active user；账号/key revoked 或未知 key 必须在启动 Git 子进程前拒绝。
- upload/receive 与 Smart HTTP 调用同一个 `RepositoryService` 与 `Authorizer`；两个 transport 的权限和 protected ref 结果必须一致。
- 配置并持久化 SSH host key；设置握手、认证、空闲和命令超时、最大并发连接及每用户并发限制。
- 审计记录 fingerprint/algorithm、actor、repo、action、结果和 request/session ID，但不记录完整公钥或 pack 内容。

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

## 发布状态机与一致性

### Repository 与 Plugin 状态

```text
repository: provisioning → ready → readOnly → deleting → deleted
                         ↘ error ──→ provisioning (retry)

plugin: draft → active → archived
          │        │
          └────────┴→ deleting → deleted
```

- repository 创建先在数据库写 `provisioning + outbox`，再由 worker 原子创建临时 bare repo 并 rename 到最终 storage key；成功转 `ready`，失败转 `error` 并可重试。
- repository 不是 `ready` 时禁止 Git 读写；`readOnly` 允许 fetch、拒绝 push。
- Plugin `active` 只表示可管理/可发布，不代表已有可安装版本。
- 删除先转 `deleting` 并撤销公开入口，再延迟物理回收；恢复窗口内可取消删除。

### Plugin Version 状态

```text
validating → published → yanked
     │
     └──────→ rejected
```

发布事务边界：

1. 授权 `plugin.publish`，锁定 Plugin 发布键，防止同版本并发发布。
2. 将请求 ref/tag 解析为完整 commit SHA；若是版本 tag，验证 tag 指向和 protected ref 规则。
3. 从该 commit 读取 `.claude-plugin/plugin.json`，校验 schema、名称一致性、路径、组件引用和大小限制。
4. 规范化 manifest 并计算 digest；写入 `validating` 记录。
5. 在数据库事务中写 `published`、版本事件、latest_version 更新、audit/outbox；唯一约束解决并发竞态。
6. 发布后禁止修改 version、tag、SHA 和 manifest snapshot。撤回只转换为 `yanked` 并记录 reason。

- `rejected` 保存安全的失败码/摘要，不保存可能含敏感内容的任意原始错误。
- `yanked` 版本默认不能用于新 Marketplace revision，但旧 revision 仍保留引用；是否继续下载由安全撤回策略决定。
- tag 被 Git 管理员越权移动时，reconciliation 必须报警并标记 integrity violation，绝不能静默改写已发布版本 SHA。

### Marketplace 状态

```text
template: draft ↔ active → archived → deleting → deleted
revision: building → published → superseded
              └───→ failed
published revision --security action--> revoked
```

发布流程：

1. 授权 `marketplace.publish`，读取带乐观锁版本的 draft snapshot。
2. 校验 marketplace 名称、owner 和 Plugin 集合；解析每个 selector 为确切可用版本和 SHA。
3. 为每个 `(Marketplace, Plugin, PluginVersion)` 获取或创建 UUID distribution，解析并固定唯一发布 tag、annotated tag object ID 与 peeled 40 位 commit SHA，生成只包含该 tag 的独立只读 bare 投影。
4. 按 HTTPS/SSH variant 生成官方 schema 中的外部 Git source；直接 URL 索引禁止相对 source，HTTPS source 使用 distribution UUID URL、指定 tag 与完整 SHA。
5. 使用稳定字段顺序/数组顺序序列化，计算 content digest 和 ETag，并在 quarantine 中构建只包含该快照 JSON 的全新不可变 Marketplace Git 投影；不得原地修改当前索引仓库。
6. 在一个数据库事务中插入不可变 revision/items，切换 `published_revision_id` 和 Marketplace distribution 的 `current_projection_id`，写 distribution 状态、audit + outbox；文件系统投影使用明确的 building/active/failed 状态与幂等补偿，不伪装为同一 ACID 事务。
7. commit 后清除/更新缓存。HTTP GET 和 Git 分发稳定 URL 只读取 published/current pointer；rollback 同样切换到已验证的不可变投影，不实时重算或修改旧投影。

- rollback 不复制或修改旧 JSON，只把稳定指针切换到一个仍有效的历史 revision，并写新审计事件。
- 普通新发布使旧 revision `superseded`，但不可变 URL 继续有效。
- 只有安全事件可 `revoked`；revoked URL 返回明确的 410/安全错误，不返回其他版本冒充原内容。
- 内容响应使用 `application/json`、强 ETag，并支持 `If-None-Match → 304`。公开 revision 可 CDN 缓存；私有索引必须 `private/no-store` 或基于授权安全缓存。

### Git/数据库一致性

- Git 对象与 refs 以 bare repo 为权威；DB 中 repository refs、size 和 latest 信息是投影。
- 每次 receive-pack 成功生成包含 `repoID + pushID` 的 outbox/event；消费者用唯一 event ID 幂等更新投影。
- reconciliation 定期执行：校验 repo 目录存在、读取 refs、核对已发布 tag/SHA、重算 size，并只修复可安全重建的投影。
- 数据库记录存在而目录缺失、目录存在而 DB 无记录、已发布 tag 漂移属于高优先级告警，不自动做破坏性“修复”。
- 所有 worker 使用 lease + heartbeat；进程崩溃后任务可重新领取。重试采用有上限的指数退避，永久失败进入 dead-letter/人工处理状态。
- outbox 至少一次，所有 handler 必须幂等；不得假设事件只处理一次。

### 审计规则

必须审计：认证成功/失败、Token/key 创建撤销、团队/成员/角色变化、仓库创建删除、Git read/write（写必记，读可按配置采样但私有资源建议记）、protected ref 拒绝、版本发布/撤回、Marketplace 发布/回滚/撤销、系统管理员跨租户操作。

- audit action 使用稳定枚举，例如 `repository.push.accepted`，不能使用随意自然语言。
- 审计事件包含 before/after 的安全摘要或变更字段列表，不保存 password/token/Authorization/packfile/完整 manifest secret。
- 审计写失败对高风险控制面操作应使事务失败；高容量 Git read 事件可通过 durable outbox 异步写入。
- 审计表对普通业务角色不可修改或删除；retention/export 任务也要被审计。

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
- 分发协议集成测试：Public 匿名安装、Private BasicAuth + `.git-credentials` 安装、账号禁用/离队/凭据撤销即时拒绝、只广告并下载指定 tag、所有 receive-pack/未知 service 拒绝，并证明任何分发请求都不会改变开发仓库或已激活投影的 refs/object 状态。
- Marketplace golden/schema 测试：同一 revision 输出字节稳定，并通过官方 schema 校验；HTTP JSON 与 Git Marketplace 指向同一快照，source 固定对应 distribution UUID、tag 和完整 SHA。
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

### 推荐生产拓扑

```text
TLS ingress / reverse proxy
       │
       ├── HTTPS 443 ── MarketplaceServer HTTP/gRPC
       │                    ├── REST / Git Smart HTTP / marketplace.json / frontend
       │                    ├── PostgreSQL or MySQL
       │                    ├── Redis (cache/locks; 非权威)
       │                    └── durable Git storage volume
       │
       └── SSH 22/独立端口 ─ MarketplaceServer SSH listener

optional: object storage for backups/exports, external OTEL/Sentry
```

- TLS 推荐由受控 ingress 终止并向应用传递可信 proxy headers；应用只信任明确配置的代理网段，防止伪造 client IP/scheme。
- HTTP 横向扩展前，Git storage 必须是所有实例可一致访问且满足 POSIX rename/locking 语义的共享存储，或通过 repository placement 将某仓库稳定路由到单一 storage node。不能把普通对象存储直接挂成 bare Git filesystem。
- SSH listener 可以与 HTTP 同进程但使用独立端口；多副本时 host keys 必须共享，连接经支持 TCP 的负载均衡器分发。
- Redis 只用于缓存、限流、lease 等可重建状态；授权、版本、发布指针不能只存在 Redis。
- 首个 MVP 使用单应用实例 + 单数据库 + 持久化本地 volume，先保证正确性；在有容量数据前不引入分布式 Git storage。

### 存储布局

```text
{storageRoot}/
  repositories/{shard}/{repositoryID}.git
  quarantine/{jobID}/
  trash/{repositoryID}-{deletedAt}.git
  locks/
```

- 物理路径只使用服务端 repository ID/storage key，不使用 namespace/repo slug。
- `storageRoot` 启动时解析为绝对路径并检查权限；repository 路径经 `filepath.Rel` 验证不得逃逸根目录。
- 创建在同 filesystem 的 quarantine 临时目录完成，再原子 rename；trash 和最终目录也应在同 filesystem。
- 备份任务不应直接复制正在写入的仓库而不协调；使用 Git-aware snapshot、文件系统快照或维护窗口。

### 备份、恢复与灾难演练

- 数据库执行持续备份/PITR；Git storage 做版本化快照；SSH host keys、JWT/pepper、配置 secret 由 secret manager 单独备份。
- 备份必须有同一逻辑时间/序列标记。恢复顺序：停止写入 → 恢复 DB/Git/keys → 运行只读 integrity scan → reconciliation → 恢复读取 → 恢复写入。
- 恢复后校验：每个 ready repo 目录存在、published version SHA 可达、published Marketplace revision 内容 digest 匹配、SSH host fingerprint 与公告一致。
- 定期执行恢复演练并记录 RPO/RTO 实测；“备份任务成功”不等于可恢复。

### 部署安全

- 运行进程使用非 root 用户；Git storage、host key、配置按最小文件权限挂载。
- 容器 root filesystem 尽量只读，只给 Git storage 和明确临时目录写权限。
- 固定并验证 Git、Go、Node 依赖版本；生产镜像不包含 npm、编译器和不需要的 shell 工具。
- 暴露独立 readiness/liveness；readiness 至少验证数据库和 storage root，不因外部可观测平台短暂不可用而下线。
- shutdown 顺序先停止接收 push/发布任务，再 drain HTTP/SSH 和 worker，最后关闭 DB/Redis；设置总超时并记录未完成任务。

## 分阶段实施路线图

路线图是依赖顺序和验收门槛，不是并行创建全部空模块。每阶段都必须保持主分支可构建、可测试，完成验收后再进入下一阶段。

### Phase 0：框架安全与确定性基线

范围：

- 将 kernel 模块存储改为有序 slice + 名称索引，生命周期按注册顺序稳定执行，Stop 反向或按明确策略执行。
- 增加重复模块、生命周期顺序、错误传播、Stop 完成的内核测试。
- 移除 MySQL DSN/密码输出；修复硬编码 JWT secret、pprof 凭据、全开放 CORS 和 gRPC auth 失败放行。
- 选择一个主数据库模块（建议 PostgreSQL 或 MySQL 二选一）；配置 migration 流程和测试数据库。
- 定义统一错误响应、request ID、Principal、namespace 和 authorization contracts。

验收：

- 连续多次测试证明模块生命周期顺序一致，缺失依赖启动失败且错误明确。
- 日志扫描不出现 DB password、Token、Authorization；默认生产配置没有硬编码凭据。
- 未认证请求不能通过受保护的 HTTP/gRPC 示例 endpoint。
- `go test ./...`、`go vet ./...`、`go build ./...` 全部通过。

### Phase 1：单用户 HTTPS Git + Plugin MVP

范围：

- `backend/domain/identity`：首个管理员初始化、登录、短期 access token/PAT。
- personal namespace。
- `backend/domain/authorization` 最小动作模型。
- `git`：bare repo CRUD、Smart HTTP upload-pack/receive-pack、storage layout。
- `backend/domain/plugins`：一个 repo 对应一个 Plugin、manifest validate、基础元数据。
- 最小管理 API；暂不实现团队和 SSH。

验收：

- 管理员可创建 Plugin/repo，并使用真实 Git client 通过 HTTPS clone、commit、push、fresh clone。
- 未认证用户不能读取 private repo；无 write 权限不能 push。
- 路径穿越、未知 Git service、过大请求和取消连接有测试。
- commit 上合法/非法 `.claude-plugin/plugin.json` 均有确定校验结果。
- 进程重启后 repo 与数据库关联保持正确；reconciliation 能发现缺失 repo。

### Phase 2：不可变版本与 Marketplace 发布

范围：

- `backend/domain/versions`：SemVer/tag/SHA 发布、保护 tag、yank、完整状态事件。
- `backend/domain/marketplaces`：draft items、validate、immutable revisions、HTTPS/SSH source variant 数据模型（此阶段至少交付 HTTPS）。
- 稳定 URL、revision URL、ETag/304、官方 schema golden tests。
- outbox + worker 基础设施、push 后 refs projection。

验收：

- 可发布两个 Plugin 版本，并创建 planA(A/B/C) 与 planB(B/C)，输出互不污染。
- 同 revision 多次 GET 字节完全一致；稳定 URL 仅在 publish/rollback 时变化。
- 直接 URL JSON 中没有相对 source，Git source 类型为 `url` 且固定完整 SHA。
- 移动已发布 tag 不会改写版本记录，会触发 integrity 告警。
- Claude Code 可添加服务输出的 Marketplace 并安装至少一个测试 Plugin。

### Phase 3：团队、RBAC 与审计

范围：

- team namespace、邀请、成员角色、资源归属。
- 完整 action matrix，REST/Git/worker 统一 Authorizer。
- PAT scope、SSH key 数据管理（SSH transport 可在下一阶段启用）。
- append-only audit 查询和高风险操作审计。
- 前端加入 namespace switcher、团队成员和权限 UX。

验收：

- owner/admin/maintainer/developer/viewer 的矩阵使用 table-driven tests 全覆盖。
- 用户离开团队后立即失去团队私有 repo、Plugin、Marketplace 权限，团队资源仍存在。
- PAT scope 不能越权；revoked Token/key 立即失效。
- 任意 namespace 列表/详情查询不存在跨租户泄露测试漏洞。
- push、发布、回滚、成员和权限变化均可按 request ID 在审计中追踪。

### Phase 4：SSH Git 与传输方案

范围：

- 独立 SSH listener、持久化 host key、公钥认证、严格 exec parser。
- upload-pack/receive-pack 复用 RepositoryService/Authorizer。
- Marketplace HTTPS/SSH 双 variant、clone UI 与安装指引。
- SSH 限流、超时、连接/进程 drain。

验收：

- 真实 Git client 可通过 SSH clone/push；权限结果与 HTTPS 完全一致。
- shell、pty、forwarding、任意 command、路径注入全部被拒绝并测试。
- revoked SSH key 无法建立 Git session；host key 重启后稳定。
- plan 的 HTTPS/SSH JSON 只有预期 source URL 不同，均通过 schema 和安装测试。

### Phase 5：Svelte 管理前端与单二进制交付

范围：

- Svelte + Tailwind 应用、认证、namespace、repo/plugin/version/Marketplace/team/audit 页面。
- Vite 静态构建、`embed.FS`、`NoRoute` fallback、缓存头和 Docker Node→Go 多阶段构建。
- OpenAPI/集中 API client、可访问性和关键 E2E 测试。

验收：

- 最终 runtime 无 Node，单 Go 二进制可完整管理 Phase 1-4 功能。
- 刷新任意 SPA 页面正常；API、Git、healthz、debug 和缺失 asset 不被 fallback 吞掉。
- hashed asset 长缓存、index no-cache；CSP/`nosniff` 等头符合设计。
- viewer 不显示写操作且直接调用仍被服务端拒绝；401/403/409 UI 可恢复。
- `npm ci && npm run check && npm run test && npm run build` 及 Go 全套验证通过。

### Phase 6：企业运行能力

范围：

- 配额、限流、worker lease/dead-letter、备份恢复、垃圾回收、retention。
- readiness/liveness、metrics、tracing、告警和容量看板。
- 安全管理员、服务账号、Token policy，可选 OIDC/SSO。
- 大规模部署前的 repository placement/shared storage 方案。

验收：

- 完成数据库 + Git storage + host keys 的恢复演练，published version/revision digest 全部验证通过。
- 模拟 worker/进程中断后无重复发布、无丢失 outbox，任务可恢复。
- 配额、并发 push、速率限制和磁盘低水位能安全拒绝请求并告警。
- 形成运维 runbook：扩容、备份恢复、host key 轮换、secret 轮换、integrity violation 和安全撤回。

### 每阶段共同 Definition of Done

- 新表有 migration、约束和回滚/前滚策略；测试不依赖开发者本地残留状态。
- 新 action 同时有 allow/deny 测试、审计策略和前端处理（如该阶段有 UI）。
- 新 API 有 request/response/error tests；新 Git 行为用真实 Git client 做集成测试。
- 新状态转换有 table-driven tests，非法转换默认拒绝。
- 配置已加入 `config.example.yaml` 且无 secret；DI 类型同步 `docs/di-reference.md`。
- 文档与代码一致；不得把后续 Phase 的设计描述为已经实现。
- 完成安全审查、`git diff --check`、Go/Frontend 对应检查后才提交。

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
