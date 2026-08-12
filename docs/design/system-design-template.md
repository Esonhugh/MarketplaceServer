# <系统名称> System Design

- **Status:** `proposed`
- **Owner:** <runtime module / internal domain>
- **Created:** YYYY-MM-DD
- **Last reviewed:** YYYY-MM-DD
- **Approval record:** <explicit approval or “not approved”>
- **Related goals/roadmap:** <links>

> 删除占位说明后再提交评审。`approved` 只表示设计语义获批，不自动授权 production code、migration、route、frontend 或 runtime wiring。实现必须另行确认 scope、TDD、Agent ownership 和 commit boundary。

## 1. 用户结果、scope 与当前基线

- **用户结果：** <可观察结果>
- **非目标：** <明确排除的相邻能力>
- **当前证据：** <current-state/source/tests/routes>
- **计划增量：** <只写 proposed target>
- **Owning module/domain：** <五模块之一及内部 package>
- **关键选择：** <必要的 alternatives/trade-offs；只为重大长期决策创建 ADR>

## 2. 数据、ownership 与 lifecycle

### Persistence records

| Record/field | Go/DB representation | Null/default | Mutable | Constraint/index | Secret/PII | Meaning |
|---|---|---|---|---|---|---|
| <field> | <GORM type/tag> | <value> | <yes/no> | <scope/unique/FK> | <class> | <meaning> |

- GORM record 与 API DTO 隔离；DB ID 使用 string。
- namespace/resource scope 必须进入 query/mutation predicate。
- schema 使用有序 GORM `AutoMigrate`/tags；destructive/backfill 另行批准。

### Lifecycle/commands

```text
<state> --<command/guard>--> <state>
```

| Command | Input/precondition | Authorization | Result/side effect | Failure behavior |
|---|---|---|---|---|
| <command> | <conditions> | <action/resource> | <bounded result> | <stable error/no-op> |

## 3. API、authorization 与 frontend

### Management API

| Operation | Auth/action | Request DTO | Success/status | Stable errors | Pagination/cache/concurrency |
|---|---|---|---|---|---|
| <method/path> | <plane-specific credential> | <handwritten DTO> | <data envelope/204> | <codes> | <page/size/total, cache, ETag> |

- API IDs 使用 string；unknown JSON fields ignored；malformed/trailing JSON rejected。
- Error response 返回框架生成的 ULID `X-Request-Id`，body `requestId` 与 header 相同；success 是否返回由 OpenAPI operation 定义。
- deployed 与 proposed OpenAPI 分开；`marketplace.json` 不由 management OpenAPI 定义。

### Authorization

| Principal/context | Action | Resource | Allow | Explicit deny/revocation |
|---|---|---|---|---|
| <case> | <action> | <tenant-qualified resource> | <conditions> | <wrong tenant/revoked/disabled> |

### Frontend

- handwritten `apiClient`；无全局 cache；mutation 后 refetch；
- localStorage 只保存 username/JWT；
- loading、empty、401、403、404、conflict、retry 和 accessibility states。

## 4. Cross-module、Git/filesystem 与一致性

| Fact/action | Authority | Cross-module capability | Atomic boundary | Failure state | Reconciliation/compensation |
|---|---|---|---|---|---|
| <fact> | <DB/Git/filesystem> | <narrow contract or none> | <transaction/ref/rename> | <state> | <idempotent recovery> |

- 不宣称 DB/Git/filesystem 跨系统 ACID。
- contract 不暴露 GORM、Jin context、handler、DAO、concrete service 或 server path。
- subprocess 使用 `exec.CommandContext`、固定参数、最小环境和 timeout。
- 记录 audit/outbox 所需安全字段与必须 redacted 的 secret/body/path。
- 对无法安全补偿的 failure 明确标为 implementation blocker。

## 5. TDD acceptance matrix

| Requirement/risk | First failing test | Layer | Allow case | Deny/failure case | Skip dependency |
|---|---|---|---|---|---|
| <behavior> | <test> | <unit/DB/HTTP/Git/frontend> | <case> | <case> | <DSN/npm/etc.> |

覆盖适用项：tenant isolation、authorization/revocation、state transition、migration、HTTP contract、真实 Git client、distribution read-only、deterministic Marketplace output、frontend states 和 race test。列出实际运行命令及 skipped gate。

## 6. Agent ownership 与 commit slices

| Slice/owner | Exclusive files/concern | Dependency | Tests before commit | Review boundary |
|---|---|---|---|---|
| <slice> | <scope> | <prior slice> | <commands> | <independent outcome> |

共享文件由唯一 integration owner 串行修改。每个 commit 可构建、可测试、可独立 review；不混入无关重构，不 amend，不跳过 hooks。

## 7. Approval 与 implementation record

- **Design approval:** <reference>
- **Implementation approval:** <separate reference or none>
- **Evidence:** <code/tests/routes/migrations/commit>
- **Validation:** <commands and skips>
- **Authority docs updated:** <current-state/architecture/DI/invariants/protocols/operations/roadmap/OpenAPI/config>
- **Remaining proposed scope:** <links>
- **Status:** 只有代码和适用证据齐备后才能从 `approved` 改为 `implemented`。
