# 系统设计目录

本目录按业务系统聚合详细设计。目录使用永久稳定的两位登记序号和 kebab-case 能力名称；序号一经分配不因路线图重排而修改，新系统使用下一个未占用序号。

## 文件分类

每个系统目录维护：

- `README.md`：范围、文档状态、批准记录和 contract 索引；
- `system-design.md`：用户结果、系统边界、authorization、TDD 和交付切片；
- `persistence-design.md`：GORM records、约束、查询和 AutoMigrate 设计；
- `<request-plane>/api-contract.md`：该请求平面的 API rationale 与 OpenAPI 链接。

请求平面目录固定使用 `management/`、`git/` 或 `distribution/`。同一系统涉及多个平面时分别维护各自的 `api-contract.md`，禁止用一个无分类的 API 文档混合协议。

Management API 的全部精确 HTTP wire contract 以 [`api/openapi/management-v1-design.yaml`](../../../api/openapi/management-v1-design.yaml) 为权威；系统 Markdown 只记录 rationale、ownership 和实现边界。实现并验证后的 wire 才能进入 deployed OpenAPI。

## 已登记系统

1. [`01-identity-authentication`](01-identity-authentication/)：账号登录、stateless frontend JWT 与用户 PAT 生命周期。
