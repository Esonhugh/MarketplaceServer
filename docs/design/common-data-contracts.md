# 通用数据边界

- **Status:** Approved
- **Last reviewed:** 2026-08-11
- **Approval record:** 用户在交互评审中确认“隔离数据库和 API 即可”
- **Scope:** GORM persistence record 与 `/api/v1` JSON DTO 的最小隔离规则
- **Implementation conformance:** 由各系统实现和测试单独证明

本文只规定两个必须隔离的数据边界，不建立 persistence/domain/projection/contract/wire 六层分类，也不要求每次调用都复制结构体或编写 mapper。

## 1. 必须隔离的两类结构

### GORM persistence record

GORM record 描述数据库表结构并拥有：

- `gorm` tags、table name、column type、index、relationship 和 hook；
- 数据库专用字段，例如 password hash、token HMAC、storage key 和关联字段；
- 数据库存储需要的 string ID 和时间字段。

GORM record 不得直接作为 `/api/v1` request/response，也不得因已有 `json` tag 就直接交给 handler 序列化。这样可以避免 schema 变化意外改变公开 API，或把关联、secret、内部路径暴露给客户端。

### Management API DTO

API DTO 只描述 `/api/v1` JSON：

- 使用 lower-camel JSON field；
- ID 使用 `string`；
- 时间使用 RFC 3339；
- 不包含 GORM tags、ORM association、hash/HMAC、storage key 或 filesystem path；
- request 与 response 可按 operation 需要分别定义。

handler 必须把 API DTO 转换为 service 可接受的值，并把 service result 转换为 API response。转换可以是直接字段赋值；只有存在复用价值时才引入专用 mapper。

## 2. 内部类型不强制分层

Domain command/result、query projection 和 cross-module contract 可以按实际需要定义或复用安全的内部类型。共同规则只有：

- 不把 GORM record 当 API DTO；
- 不让 API DTO 反向成为数据库 schema；
- 跨模块 contract 保持窄小，只包含 consumer 所需字段；
- 不跨边界暴露 GORM、Jin context、handler、DAO、concrete service、server path 或 secret；
- tenant-owned query 和 mutation 在数据库 predicate 中包含 namespace/resource scope，禁止全局读取后再内存过滤。

内部类型可以使用 `string`、`uuid.UUID` 或其他适合实现的强类型。无需为了统一 field type 做无业务价值的机械转换。

## 3. Identifier 规则

- GORM record 中的业务 ID 使用 `string`。
- Management API request/response 中的 ID 使用 `string`。
- UUID-backed string 在创建、数据库写入和 API ingress 处校验 canonical lowercase hyphenated UUID。
- 内部 service、domain 和 cross-module contract 可以使用 `uuid.UUID`；在进入数据库或 API 边界时显式转换。
- `StorageKey`、`PublicKey`、`CommitSHA`、`TagName`、`Slug`、`Username` 和 `RequestID` 虽然也是 string，但语义与校验规则不同，不能按 UUID 处理。
- storage key 和 filesystem path 不进入 management API。

## 4. Query 与 tenant scope

本设计不要求所有 query 都创建独立 projection type。repository 可以使用最简单且安全的形态，但必须保证：

- namespace、owner 或已解析 resource identity 在 SQL/GORM query 中限定；
- list 有固定上限和稳定排序；
- mutation 使用与 read 相同的 ownership scope；
- authorization 仍在 service/protocol boundary 执行，数据库 scope 不替代授权；
- secret 和无关 persistence 字段不会进入跨模块或 API result。

## 5. `marketplace.json` 边界

Claude Code `marketplace.json` 是 distribution 外部格式，不是 management API DTO。MarketplaceServer 的职责是通过 [`pkg/marketplacejson`](../../pkg/marketplacejson/) 生成受支持的官方格式，并施加本地 distribution 约束，例如稳定排序、可访问 URL、tag/ref 和完整 distribution SHA。

Plugin 仓库内容验证属于受保护 Git receive 流程：默认分支运行 `claude plugin validate`，tag 使用 strict validation，并检查 manifest name 与 Plugin name 精确一致。Marketplace 生成不重复承担 Plugin repository validator 的职责，也不需要在服务运行时引入通用 JSON Schema validator。

格式来源记录见 [`schemas/marketplace/README.md`](../../schemas/marketplace/README.md)。Management OpenAPI 不定义或包装 `marketplace.json`。

## 6. Review checklist

新增或修改数据结构时只需确认：

1. GORM record 没有被直接序列化为 management JSON。
2. API DTO 不包含 persistence-only 或 secret 字段。
3. DB/API ID 为 string，内部 UUID 在边界显式转换。
4. tenant/resource scope 在 query 或 mutation 中明确存在。
5. cross-module contract 只暴露 consumer 所需能力。
6. `marketplace.json` generation 与 Plugin repository validation 职责分离。
7. 设计批准不等于实现批准；代码仍需单独的 TDD slice。
