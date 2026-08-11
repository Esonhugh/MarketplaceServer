# GORM Persistence 约定

- **Status:** Approved
- **Last reviewed:** 2026-08-11
- **Approval record:** 用户确认新 schema 继续使用 GORM `AutoMigrate` 与 tags，不建立额外通用 migration guard 体系
- **Scope:** backend relational persistence 的最小公共约定
- **Implementation conformance:** 由各系统实现和数据库测试单独证明

本文不新增 table、model 或 migration。当前 schema 以源码和 [`docs/current-state.md`](../current-state.md) 为准。

## 1. 显式 GORM record

每个 persisted entity 使用用途明确的 GORM record，不引入自动附加 ID、soft delete、tenant 或 lifecycle 字段的通用 base model。

常用字段约定：

| Concern | Rule |
|---|---|
| Table | 明确 `TableName()`，table ownership 属于对应 backend domain |
| Primary ID | GORM record 使用 `string`；UUID-backed ID 在创建/写入边界校验 canonical UUID |
| Foreign key | 使用 string field 和 GORM relationship/tag；delete/update 行为按系统 lifecycle 决定 |
| Tenant | namespace 是 ownership boundary 时直接保存 `NamespaceID string` |
| Slug/name | namespace-scoped identity 使用 composite unique index |
| Time | 只添加有业务语义的时间；nullable fact 使用 `*time.Time` |
| Lifecycle | aggregate 自己定义 string values，不使用跨 aggregate 的万能 status enum |
| Secret | 只保存 hash/HMAC/index，不保存 plaintext password、token 或 private key |
| JSON | 只用于确有必要的 snapshot/metadata，不替代需要查询和约束的关系 |
| Deletion | 不默认 soft delete；由系统 lifecycle 设计决定 |

GORM record 不得直接作为 management API DTO。API field 增删不能隐式改变数据库 schema，数据库字段也不能因可序列化而自动公开。

## 2. AutoMigrate authority

公共 schema authority 是：

1. 显式 GORM records 与 tags；
2. 各 domain 的有序 model list；
3. `mod/backend/migrate.go` 的有序 `AutoMigrate` 调用。

不引入平行的 versioned SQL migration history，也不要求新系统默认编写 driver-specific SQL guard。现有 guard 不因本设计自动删除；是否保留、修改或新增必须由 owning system 的单独设计和测试决定。

`AutoMigrate` 不授权 destructive schema operation。删除 table/column、不兼容 type change、data rewrite 或 backfill 必须另有明确批准的兼容与运维方案。

## 3. Tenant-scoped queries

Repository query 和 mutation 必须：

- 在 SQL/GORM predicate 中包含 namespace、owner 或已解析 resource scope；
- 对 list 设置有限 `size`，最大值遵循 API contract；
- 使用服务端固定稳定排序，并以唯一字段作为 tie-breaker；
- 对 page/size API 返回 filtered exact `total`；
- 不先全局读取候选，再依赖 service/frontend 过滤 tenant；
- 不因 pagination 而省略最终 authorization。

是否定义专用 projection struct 由 query 复杂度决定，不是公共强制层。简单查询可以直接扫描到安全内部 result；复杂或敏感查询应只 select 所需字段。

## 4. Migration tests

每个 schema change 按实际风险选择测试：

- clean database 上运行 owning migration 或完整 backend coordinator；
- 同一 migration 重复运行并保持幂等；
- 对现有 column/index/relationship/data 有影响时测试 upgrade path；
- 测试 unique index、foreign key、tenant scope 和 secret-column absence；
- 同名资源跨 namespace 不冲突，namespace 内按约束冲突；
- list 的稳定排序、page/size bound 和 exact filtered total；
- 报告因 `MARKETPLACE_TEST_POSTGRES_DSN` 等环境缺失而 skipped 的 suite。

测试只声明实际验证过的数据库和行为。未运行 MySQL/PostgreSQL integration test 时不能声称对应 driver 已验证。

## 5. 当前兼容性记录

以下是现状，不是本设计要求立即重构：

- persistence ID 已以 string 为主，部分内部 contract 使用 `uuid.UUID`；该用法符合新边界，无需统一改成 string；
- 部分 domain result 带 JSON tags；只有当它实际承担 `/api/v1` wire DTO 时才需要在 owning slice 中拆分；
- distribution 中多个 aggregate 复用 `active` 字符串；后续 system design 再定义各自 lifecycle；
- 部分 repository interface 暴露 GORM-shaped record；只有跨 API/module 泄漏或明显耦合时才在 owning slice 中调整；
- 用户动态 Marketplace candidate query 仍存在全局读取后过滤的问题，需要 distribution owning slice 以 tenant/resource-scoped query 修复；
- 尚未设计的 management list 不预先创建索引，索引随具体 query 和 TDD slice 添加。
