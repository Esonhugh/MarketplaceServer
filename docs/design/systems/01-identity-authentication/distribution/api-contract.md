# Identity Subscription Distribution Credential Contract

- **Status:** `approved`
- **Request plane:** `/distribution`
- **Owner:** identity principal production and subscription authorization in `backend`
- **Created:** 2026-08-12
- **Last reviewed:** 2026-08-12
- **Approval record:** 用户通过逐项 AskUser 评审直接批准

本文件只定义 PAT credential boundary，不提前定义自定义 Marketplace subscription 的 route、locator 或 persistence；这些属于后续 Marketplace/subscription system design。

- Basic username + 任一含 `sub-read` capability 的 PAT 可以尝试读取该用户当前获准读取的 subscription Marketplace 输出。
- `git-clone` 和 `git-write` 逐级包含 `sub-read`。
- PAT 不绑定、选择或修改 subscription 内容；自动 `all` 和自定义 playlist-like Marketplace 的内容由 owning system policy 决定。
- Account password 和 JWT 在 subscription distribution 明确拒绝。
- PAT capability 仍与当前 Marketplace/Plugin/subscription authorization 取交集。
- Public Marketplace distribution 继续按 public locator 匿名读取，不要求 PAT。
