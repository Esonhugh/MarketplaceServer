# 产品与数据不变量

本文记录当前和未来实现都必须保持的业务、安全与一致性约束。它是规范，不是当前功能清单；当前实现以 [当前实现状态](current-state.md) 为准。

> **目标模型说明：** 本文标为“规划中”的实体、角色、状态机或流程不代表对应 migration、API、worker 或 UI 已经存在。

## 主体、namespace 与租户隔离

- Plugin、repository 和 Marketplace 属于一个 namespace；个人和未来团队资源都使用同一 `namespace_id` 语义。
- 用户创建与个人 namespace 创建必须在同一数据库事务中完成。
- namespace slug 和身份字段的不可变性必须由数据库约束或 guard 强制，而不只依赖 service。
- DAO 列表和 mutation 必须显式按 namespace 或已解析资源 ID 限定，禁止全局查询后在内存过滤。
- 团队资源属于团队 namespace，不因创建者离队而转为个人资源或消失。
- 用户 disabled、credential revoked 或团队权限撤销必须在下一次请求生效，不删除历史审计或资源。
- 业务主键使用不可枚举 ID；slug/public key 只用于经校验的外部定位。

## Authorization

- 默认拒绝；没有匹配授权时不能降级为允许。
- 最终输入统一表达为 `Principal + Action + Resource + Context`，返回明确 allow/deny 与可审计原因。
- REST、Git HTTPS、未来 SSH、distribution 和 worker 复用同一 action/resource 语义。
- handler 只提取身份与资源；service/protocol boundary 必须执行服务端授权。前端隐藏按钮不是授权。
- 匿名主体只可能获得显式公开资源的 read action，永远不能写入、发布或读取私有 metadata。
- PAT scope 是主体当前权限的交集，不能扩大用户、组或资源 policy。
- 系统管理员和团队 owner 是不同权限域；跨租户管理必须走显式管理路径并审计。

### 目标团队角色矩阵 — 规划中

角色是 action 集合，不应在 handler 中散落角色字符串：

- `owner`：团队所有权、成员、高风险设置和删除；
- `admin`：团队配置、成员和资源管理，但不能转移/删除所有权；
- `maintainer`：repository 管理和 Plugin/Marketplace 发布；
- `developer`：repository 读写和 draft 编辑，默认不能发布；
- `viewer`：被授权资源只读。

高风险 action（删除、转移、protected ref、发布）即使通过角色检查，仍需资源规则二次判定。

## Repository 与 Plugin

- 一个托管 repository 只承载一个 Plugin，Plugin 与 repository 一对一。
- bare Git repository 中的 objects/refs 是内容权威来源；数据库保存归属、权限、展示信息和可重建投影，不复制完整 Git 对象。
- repository slug 只在 namespace 中唯一；物理路径只使用服务端生成的 opaque ID/storage key。
- Plugin 可发布 commit 必须包含有效 `.claude-plugin/plugin.json`；manifest 必须从服务端解析的 commit 读取，不能接受客户端声称的 snapshot。
- Plugin visibility 不能高于 repository 可读性。
- 删除默认先软删除或进入回收状态；物理删除 Git 数据是独立、可取消、可审计的高风险操作。
- push 后的 ref、size 和 manifest metadata 通过幂等事件/reconciliation 更新；不得把 DB projection 当成 Git 权威。

## Plugin version

- Git content identity 必须固定为完整、不可变的 SHA；SemVer 通常映射到受保护 tag。
- 同一 Plugin 的 version 和发布 tag 分别唯一。
- 发布记录至少固定 Plugin、version、tag/ref、完整 SHA、manifest snapshot/digest、发布者和时间。
- 已发布 version、tag、SHA 与 manifest snapshot 不可修改；修复必须发布新版本。
- branch 是开发状态，不是可复现安装版本。Marketplace revision 只能引用已发布版本或明确固定的 commit。
- yank/revoke 改变可安装状态但不篡改历史；旧 revision 是否继续下载由显式安全策略决定。
- source tag 漂移只产生 integrity violation 与告警，不能静默更新已发布记录或 distribution。

### 目标版本状态机 — 规划中

```text
validating → published → yanked
     │
     └──────→ rejected
```

`rejected` 只保存安全错误码/摘要，不保存可能含敏感数据的原始输入。发布 transaction 必须使用唯一约束解决并发竞态，并记录状态事件。

## Marketplace template、revision 与 public key

- Marketplace 是 Plugin 的组合视图，不是新的 Plugin 开发 repository。
- 每个 template 的 draft 独立；不同方案的 Plugin 集合、排序和 selector 不能互相污染。
- `marketplacePublicKey` 在 Marketplace 首次创建时生成，格式为 `{normalized-name}-{8-lowercase-hex}`，全局唯一、持久化且改名后不变。
- public key 和 distribution UUID 都是 locator，不是 credential；internal ID、FK 和 projection pointer 继续使用 UUID。
- draft 可变；published revision 和 projection 不可变。
- publish 必须把 selector 解析为精确 Plugin version 与 content identity，校验 manifest/source，稳定序列化并保存快照。
- stable URL 只在显式 publish/rollback 后切换到一个已验证的 immutable projection；不能在 GET 时根据浮动 branch 动态重算。
- rollback 复用已验证历史 projection 并切换 pointer，不复制、重建或原地修改旧快照。
- superseded revision 仍可复现；只有显式安全事件可以 revoke。被撤回内容不能由其他版本冒充。

### Immutable Plugin distribution

- `(Marketplace, Plugin, PluginVersion)` 对应独立、只读的 distribution identity；同一 Marketplace 的 revision 重复引用同一 version 时复用该 identity。
- 每个 distribution 只暴露发布时确认的单个 tag，不提供 receive-pack、upload-archive、Dumb HTTP 或未知 service。
- projection 必须与开发 repository 隔离；hideRefs 或 namespace 不是 object 隔离边界。
- builder 只读解析 source tag，在 quarantine 构建新 projection，验证后原子移动；active projection 永远不原地写入。
- Plugin snapshot 从发布 tree 生成无父级 distribution commit 和同名 lightweight tag；Marketplace `source.sha` 使用 distribution SHA，避免泄露 source ancestry。
- source tag 类型、可选 annotated tag object ID、peeled source commit SHA、tree SHA 与 distribution SHA 应分别保存，以支持审计与完整性验证。

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
- password 是独立随机值，不能复用 Marketplace/Plugin/version/distribution UUID；明文只在创建响应显示一次，但撤销/过期前可供多次 Git 请求使用。
- 数据库只保存 `HMAC-SHA-256(pepper, password)` 等值索引；pepper 来自 secret manager/runtime environment。
- 同一用户和 Marketplace 可有多个命名 credential，并独立设置 expiry/revocation。
- credential 只能用于目标 Marketplace 的 `marketplace.read`/`distribution.read`，不能用于 `/api/v1` 管理或 repository write。
- 每次请求都重新检查 credential、用户状态与当前 Marketplace authorization；离队或 disabled 后立即拒绝。
- HTTP JSON Authorization 与 Git Credential Helper 是不同传输链路，不能假设 header 自动转发或 HTTP 下载读取 `.git-credentials`。
- 文档必须警告 `.git-credentials` 是明文存储，建议仅用户可读写，并提供删除/撤销步骤；默认复制命令不得把 password 留进 shell history。

## 一致性、outbox 与审计

数据库 transaction 只覆盖数据库状态。Git refs、objects 和 filesystem 通过显式状态、outbox/event、幂等 worker、reconciliation 或补偿协调，不得宣称跨系统 ACID。

- receive-pack 成功应产生唯一 push/event identity，消费者幂等更新 DB projection。
- reconciliation 可以重建安全投影，但不能破坏性“修复”缺失目录、孤立目录或发布 tag 漂移；这些情况需要高优先级告警。
- worker 至少一次执行，使用 lease/heartbeat、有限指数退避与 dead-letter/人工处理；不能假设事件只处理一次。
- audit action 使用稳定枚举；metadata 脱敏，不保存 password、token、Authorization、完整 key、packfile 或 secret-bearing manifest。
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

- 外键、唯一性和不可变字段由数据库强制，不只依赖 service check。
- 需要软删除的资源使用明确 `deleted_at` 或状态；不可变事件不软删除。
- JSON 字段只承载边缘 metadata/snapshot，不替代可查询且有关联约束的核心列。
- 列表必须 tenant-scoped 且有界分页；cursor 不暴露未签名内部查询状态。
- URL、namespace、slug、ref、tag、UUID 和 filesystem path 分别校验，不共享一个宽松正则。
- 所有 mutation 接受 `context.Context` 并支持 timeout/cancellation。
