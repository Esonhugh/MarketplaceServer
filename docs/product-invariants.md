# 产品与数据不变量

本文记录当前和未来实现都必须保持的业务、安全与一致性约束。它是规范，不是当前功能清单；当前实现以 [当前实现状态](current-state.md) 为准。

> **目标模型说明：** 本文标为“规划中”的实体、角色、状态机或流程不代表对应 migration、API、worker 或 UI 已经存在。

## 主体、namespace 与租户隔离

- Plugin、repository 和 Marketplace 属于一个 namespace；个人和未来团队资源都使用同一 `namespace_id` 语义。
- 用户创建与个人 namespace 创建必须在同一数据库事务中完成。
- namespace slug 和身份字段的不可变性必须由数据库约束或 guard 强制，而不只依赖 service。
- DAO 列表和 mutation 必须显式按 namespace 或已解析资源 ID 限定，禁止全局查询后在内存过滤。
- 团队资源属于团队 namespace，不因创建者离队而转为个人资源或消失。
- credential revoked 与团队/资源权限撤销必须在下一次受对应 policy 检查的请求生效，不删除历史审计或资源。Stateless 30-day frontend JWT 不检查 live user status；disabled 立即阻止新 login/PAT authentication，但已签发 JWT 认证可持续至到期，资源 policy 仍实时生效。
- 业务主键使用不可枚举 ID；slug/public key 只用于经校验的外部定位。

## Authorization

- 默认拒绝；没有匹配授权时不能降级为允许。
- 最终输入统一表达为 `Principal + Action + Resource + Context`，返回明确 allow/deny 与可审计原因。
- REST、Git HTTPS、未来 SSH、distribution 和 worker 复用同一 action/resource 语义。
- handler 只提取身份与资源；service/protocol boundary 必须执行服务端授权。前端隐藏按钮不是授权。
- 匿名主体只可能获得显式公开资源的 read action，永远不能写入、发布或读取私有 metadata。
- PAT preset 只表达 `sub-read`、`git-clone`、`git-write` 的逐级 credential capability，并与主体当前 Marketplace/Plugin policy 取交集；PAT 不能扩大权限，也不能用于 management API。
- 系统管理员和团队 owner 是不同权限域；跨租户管理必须走显式管理路径并审计。

### 目标团队角色矩阵 — 规划中

角色是 action 集合，不应在 handler 中散落角色字符串：

- `owner`：团队所有权、成员、高风险设置和删除；
- `admin`：团队配置、成员和资源管理，但不能转移/删除所有权；
- `maintainer`：repository 管理和 Plugin/Marketplace 发布；
- `developer`：repository 读写和 draft 编辑，默认不能发布；
- `viewer`：被授权资源只读。

高风险 action（删除、转移、protected ref、发布）即使通过角色检查，仍需资源规则二次判定。

## Plugin 与隐藏 Git repository

- Plugin 是唯一 user-facing resource；创建只接受 lowercase kebab-case `name`，默认 public，并原子创建隐藏的一对一 Git repository。
- Plugin name 同时是 immutable slug/Git path；Plugin ID、repository ID、storage key、filesystem path 和 clone URL 由服务端管理。
- Plugin read authorization 同时控制 metadata read 与 repository clone/fetch；Plugin write authorization 单独控制 push。Repository 没有独立 management CRUD 或独立产品权限。
- bare Git repository 中的 objects/refs 是内容权威；数据库只保存归属、权限和可重建 projection metadata。
- canonical tags 在 live bare repository 之外导出 proposed commit 后按 [Plugin Profile v1](design/systems/02-plugin-lifecycle/git/api-contract.md#marketplaceserver-plugin-profile-v1--normative-source-validation) 验证，并要求 manifest name 与 Plugin name 精确、区分大小写一致；普通分支不触发 Plugin validation。
- ordinary development branch 可以包含中间状态，不强制 Plugin validation。
- 删除先进入 owning Plugin lifecycle；物理 Git 删除是单独的高风险运维行为。

## Tag-driven Plugin version

- 发布只选择一个已存在、尚未发布的 canonical `v`-prefixed SemVer tag；manifest `version` 不作为发布 identity。
- 同一 Plugin 的 canonical tag 对应一个 logical version；tag 的当前 full commit SHA 是该 version 内容权威。
- tag 从 SHA-A 移到 SHA-B 时，同一 version 更新到 SHA-B，不创建新 version，也不自动 yank。
- protected tag update 必须先通过 native Plugin Profile v1 validation、exact name check，以及所有引用该 Plugin+tag 的 revision projection prebuild；任何失败都拒绝 push。
- branch 只用于 development，不是 installable version。
- latest 优先最高 stable SemVer；只有不存在 stable 时才选择最高 prerelease。
- periodic reconciliation 可以发现绕过 receive policy 的 drift，并进入显式 operator recovery；不能假装 DB 和 Git ref 是一个 transaction。

## Marketplace template、revision 与 public key

- Marketplace 是 Plugin 的组合视图，不是 Plugin repository。
- 每个 template 的 draft 独立，保存有序 Plugin+tag selection。
- `marketplacePublicKey` 在 Marketplace 首次创建时生成，格式为 `{normalized-name}-{8-lowercase-hex}`，全局唯一、持久化且改名后不变。
- public key 和 distribution UUID 是 locator，不是 credential；internal ID、FK 和 projection pointer 可继续使用 UUID。
- published revision 保留不可修改的 Plugin+tag configuration；其 projection bytes 不是历史冻结内容，会在 selected tag 移动时重建。
- tag push 必须为每个引用 revision 在 quarantine 中构建新 immutable projection artifact；全部成功后才允许进入后续 ref/pointer switch 流程。
- stable route 只读取 ready projection。GET 不能 build、update ref、切换 pointer 或修改 development repository。
- rollback/activate 直接切换到已有 revision；该 revision 应已针对所有 selected tags 的当前 SHA 完成 rebuild。
- Git ref 已更新而 DB/version/revision projection pointers 未成功切换的 failure semantics 尚未解决；在单独设计批准前，此 workflow 阻塞 production implementation，禁止伪装 cross-system ACID。

### Plugin distribution

- `(Marketplace, Plugin, selected tag)` 对应独立、只读的 Plugin distribution identity；同一 Marketplace 的 revision 重复引用相同 Plugin+tag 时可复用 identity。
- 每个 distribution 只暴露 selected tag，不提供 receive-pack、upload-archive、Dumb HTTP 或未知 service。
- 每次 build 产生隔离、不可原地修改的 projection artifact；tag move 通过构建并切换到新 artifact 应用，不改写旧 artifact。
- projection 与 development repository objects/history 隔离；Marketplace `source.sha` 使用当前 distribution SHA。
- source tag、source commit/tree identity、distribution SHA 和 build time 分别记录，以支持诊断和 reconciliation。

## Marketplace JSON

- 输出必须符合当前官方 Claude Code Marketplace schema；修改字段前重新核对官方文档/JSON Schema，不能臆造兼容字段。
- 顶层至少包含唯一 kebab-case `name`、带 `name` 的 `owner` 和 `plugins` 数组；不得仿冒官方保留名称。
- 直接 URL 下载的 JSON 不能使用 `./...` relative Plugin source。
- 通用 Git source 使用官方 `url` 对象和可访问 URL/ref/SHA；不能自创 `httpsUrl`、`sshUrl`、`cloneUrl`、`transport` 或 `git`/`ssh` source type。
- HTTPS/SSH variant 若内容不同，应形成独立 variant 和 digest；同一客户端同时注册时避免同名 Marketplace 相互替换。
- 不要同时在 Plugin manifest 与 Marketplace entry 重复维护冲突的 version。
- 序列化、Plugin 顺序和字段选择必须确定；草稿绝不能经正式分发 URL 暴露。

## Private distribution credential — 规划中

目标 credential 固定为 `user + marketplace_template` scope：

- Marketplace 有不可变、公开的 BasicAuth username；它不是用户身份或 secret。
- password 是独立随机值，不能复用 Marketplace/Plugin/version/distribution UUID；是否支持 repeatable reveal 由该系统 API contract 决定。
- 默认数据库只保存 `HMAC-SHA-256(pepper, password)` 等值索引；若获批 repeatable reveal，则按 ADR-0006 保存显式 `secret_plaintext`，并把数据库/备份纳入 credential trust boundary。
- 同一用户和 Marketplace 可有多个命名 credential，并独立设置 expiry/revocation。
- credential 只能用于目标 Marketplace 的 `marketplace.read`/`distribution.read`，不能用于 `/api/v1` 管理或 repository write。
- 每次请求都重新检查 credential、用户状态与当前 Marketplace authorization；离队或 disabled 后立即拒绝。
- HTTP JSON Authorization 与 Git Credential Helper 是不同传输链路，不能假设 header 自动转发或 HTTP 下载读取 `.git-credentials`。
- 文档必须警告 `.git-credentials` 是明文存储，建议仅用户可读写，并提供删除/撤销步骤；默认复制命令不得把 password 留进 shell history。

## 一致性、outbox 与审计

数据库 transaction 只覆盖数据库状态。Git refs、objects 和 filesystem 通过显式状态、outbox/event、幂等 worker、reconciliation 或补偿协调，不得宣称跨系统 ACID。

- receive-pack 成功应产生唯一 push/event identity，消费者幂等更新 DB projection。
- reconciliation 可以检测 tag/current-SHA 与 DB/projection pointer 不一致并重建新 artifact，但不能在未定义 failure semantics 时静默移动 Git ref、删除旧 artifact 或假装完成原子切换；不确定状态需要高优先级告警和人工恢复。
- worker 至少一次执行，使用 lease/heartbeat、有限指数退避与 dead-letter/人工处理；不能假设事件只处理一次。
- audit action 使用稳定枚举；metadata 脱敏，不保存 password、token、Authorization、完整 key、packfile 或 secret-bearing manifest。获批 `secret_plaintext` 只存在 owning persistence record 和明确 create/reveal response，绝不进入 audit。
- 高风险控制面 mutation 的 audit/outbox 需要与业务状态同事务；写失败应使 mutation 失败。
- audit 数据 append-only，普通业务主体不能修改或删除；retention/export 自身也需审计。

### 目标状态模型 — 规划中

```text
repository: provisioning → ready → readOnly → deleting → deleted
                         ↘ error ──→ provisioning

plugin: draft → active → archived → deleting → deleted

marketplace template: draft ↔ active → archived → deleting → deleted
revision: building → published → superseded
              └───→ failed
published revision --security action--> revoked
```

所有非法转换默认拒绝并以 table-driven tests 覆盖。

## 数据库通用规则

- 外键和唯一性优先通过 GORM tags/AutoMigrate 建立；其他 lifecycle/immutability 规则由 owning system 选择 service、Git policy 或数据库约束并用 deny tests 证明。
- 需要软删除的资源使用明确 `deleted_at` 或状态；不可变事件不软删除。
- JSON 字段只承载边缘 metadata/snapshot，不替代可查询且有关联约束的核心列。
- 列表必须 tenant-scoped 且使用有界 page/size；management API 返回 exact filtered total 和稳定服务端排序。
- URL、namespace、slug、ref、tag、UUID 和 filesystem path 分别校验，不共享一个宽松正则。
- 所有 mutation 接受 `context.Context` 并支持 timeout/cancellation。
