# Claude Code Marketplace 格式来源

MarketplaceServer 的职责是生成 Claude Code 可读取的 `.claude-plugin/marketplace.json`，不是在运行时提供通用 Plugin/Marketplace JSON Schema validator。

## 格式参考

截至 **2026-08-11**，公开可用的 Marketplace SchemaStore 文档为：

- editor URL：`https://json.schemastore.org/claude-code-marketplace.json`；
- SchemaStore source：`src/schemas/json/claude-code-marketplace.json`；
- provenance PR：`SchemaStore/schemastore#5603`；
- pinned merge commit：`d6c59e8a9b85aa0bd5f8cad136c68e81d267fd70`；
- 该 PR 由 Anthropic 员工提交，并说明由 Anthropic canonical Zod definitions 生成；实际 bytes 托管在第三方 SchemaStore。

Anthropic 的 `anthropics/claude-code` Marketplace example 使用：

```json
{
  "$schema": "https://json.schemastore.org/claude-code-marketplace.json"
}
```

`$schema` 用于 editor autocomplete/validation，Claude Code load 时忽略。SchemaStore live URL 是 mutable alias，因此它只作为当前格式和开发时编辑器参考，不是 MarketplaceServer runtime trust anchor。

## 本项目的使用方式

- 不 vendor SchemaStore bytes；
- 不新增运行时 Draft-07 validator；
- 不把 unofficial best-effort schema 当作权威；
- 修改 `pkg/marketplacejson` 字段/source shape 前，重新核对当前 Claude Code 文档、SchemaStore schema 和官方 examples；
- 用 Go model、golden tests 和真实 Claude Code installation/validation integration test（适用时）证明生成结果；
- 只实现 MarketplaceServer 实际分发需要的官方字段子集，不为了覆盖整个 schema 增加无业务用途字段。

## 职责边界

### Plugin repository receive

Plugin 仓库负责内容 admission validation：

- protected default branch：`claude plugin validate <isolated-dir>`，errors reject，warnings allowed；
- protected tag：`claude plugin validate <isolated-dir> --strict`；
- `.claude-plugin/plugin.json` name 必须与服务端 Plugin name 精确、区分大小写一致。

### Marketplace generation

`pkg/marketplacejson` 只负责：

- 生成受支持的 `marketplace.json` shape；
- 使用确定性 Plugin ordering 和 serialization；
- 生成调用者可访问的 source URL；
- 输出所选 canonical tag/ref 与完整 distribution SHA；
- 不输出 relative HTTP source、自创 transport 字段、secret-bearing URL 或 server filesystem path；
- 每个 Plugin distribution 只暴露所选 tag。

这里的 `Validate` 是生成前的本地不变量检查，不是完整 Plugin validator，也不宣称覆盖 Claude Code 的全部 schema/runtime semantics。

## 更新检查

修改 Marketplace JSON 输出时：

1. 核对当前 Claude Code Marketplace documentation 和官方 example；
2. 比较当前 SchemaStore schema 的 relevant fields/source variants；
3. 明确区分官方格式与 MarketplaceServer 支持子集；
4. 先添加/更新 golden 和 deny tests；
5. 验证 deterministic output、HTTP/Git snapshot consistency 和 distribution read-only；
6. 报告未运行的 Claude CLI、database 或 Git integration coverage。
