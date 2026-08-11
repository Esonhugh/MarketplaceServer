# ADR-0003: GORM AutoMigrate and Tags Are the Schema Authority

- **Status:** Approved
- **Date:** 2026-08-10
- **Last reviewed:** 2026-08-11
- **Approval record:** 用户确认新 schema 的公共规则只使用 GORM `AutoMigrate`

## Context

Backend 已使用 ordered GORM model lists 和 `AutoMigrate`。再引入一套通用 versioned SQL 或 mandatory per-driver guard policy 会形成重复 schema authority，并增加 PostgreSQL/MySQL 差异维护成本。

## Decision

- Explicit GORM records、tags、domain model order 和 `mod/backend/migrate.go` 的 ordered `AutoMigrate` 是公共 relational schema authority。
- 不新增平行 versioned SQL migration history。
- 不要求新系统默认编写 driver-specific SQL guards，也不因缺少 guard 默认 fail startup。
- Existing guards 不自动删除；其变更由 owning system 单独设计、测试和批准。
- 若某个具体系统确实需要 tags 无法表达的数据库约束，可在该系统设计中单独论证，而不是成为全局模板税。
- Destructive schema change、data rewrite 和 backfill 不由普通 `AutoMigrate` slice 自动授权，必须有单独兼容与运维方案。

Schema change 根据实际影响测试 clean database、repeat migration、upgrade compatibility、unique/FK/tenant behavior，并明确报告未运行的 database suite。

## Consequences

- Schema authority 保持简单且符合当前代码。
- 新系统不为假设性的跨 driver invariant 编写 SQL。
- 部分 invariant 可能先由 service + tests 保证；需要数据库级加强时再单独设计。
- `AutoMigrate` 的非破坏性便利不等于 destructive migration 获批。

## Rejected alternatives

### AutoMigrate 与 versioned SQL 并行

Rejected：两个 authority 容易在 columns、indexes 和 order 上漂移。

### 所有 invariant 都必须有每个 driver 的 SQL guard

Rejected：复杂度过高，并把具体系统风险扩展成所有 model 的默认负担。

## Related design

- [GORM persistence](../design/persistence-models.md)
- [通用数据边界](../design/common-data-contracts.md)
