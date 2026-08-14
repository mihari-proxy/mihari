# Proxies 页节点切换失败反馈设计

日期：2026-08-14
状态：已定稿，随 PR #80 修复实施
目标 issue：[#80](https://github.com/mihari-proxy/mihari/issues/80)
目标分支：`fix/proxies-select-silent-failure`

## 1. 背景

proxies 页切换节点是实时链路：`selectFocused()`（`internal/tui/pages/proxies/model.go:376`）→ control client `PUT /v1/proxy-groups/{name}` → control server `selectProxy` handler（`internal/control/server/runtime.go:184`）→ `Manager.SelectProxy`（`internal/runtime/manager.go:556`，doOperation 去重 + maintenance 锁 + revision 检查）→ mihomo `PUT /proxies/{group}`。

后端每一层都有规范化的 `APIError` → HTTP 状态码映射（502/403/409/422，非 `APIError` 兜底 500），但 TUI 前端对失败完全静默——`selectionResultMsg` 处理（`model.go:133-140`）在 `err != nil` 时只清 pending 标记（"…" 变回空格），无任何错误文案。用户感知是"按了 enter 没反应"。

这同时与项目自身模式不一致：其他所有页面的 mutation 结果消息（system / subscriptions / webgui / setup / rules / connections）都实现了 shell 的 action-outcome 契约（`Err() error`，`internal/tui/model.go:103`），`selectionResultMsg` 是唯一例外。

## 2. 目标

- 切换失败时页面给出可见的错误提示（Danger 色，页面内容首行）。
- `selectionResultMsg` 实现 `Err() error` 契约，对齐其他页面的模式。
- 成功路径行为不变：更新 `Now`，无错误的乐观更新（失败时 UI 与真实状态一致）。
- 失败提示在下次操作开始时清除（成功结果、或再次发起选择）。

## 3. 非目标

- 不修 `delayResultMsg` 把一切错误伪装成 Timeout 的问题（#80 只登记选择路径；延迟测速错误区分另行处理）。
- 不把选择动作迁移到 `ActionIntentMsg`/`executeAction` 全流程（ledger 可见性靠页面内提示解决；契约先行对齐，迁移属于更大重构）。
- 不透出 APIError 动态 detail（与 rules 页静态文案模式一致，避免把上游错误原文直接渲染进 TUI）。
- 不改 control server / runtime / mihomo client 任何后端代码。

## 4. 方案

全部改动收敛在 TUI proxies 页 + 一个文案常量：

1. **`selectionResultMsg` 契约**：增加 value receiver 的 `Err() error` 与 `var _ interface{ Err() error } = selectionResultMsg{}` 断言，命名与注释风格照抄其他页（如 `mutationResultMsg`）。
2. **错误状态**：`Model` 增加 `lastError string` 字段（rules 页同款模式）。
3. **Update 分支**：`selectionResultMsg` 失败时 `m.lastError = ui.ProxySelectFailed`；成功时清空并更新 `Now`（现有逻辑）。
4. **清除时机**：`selectFocused()` 在发起新选择前清 `m.lastError`——旧失败提示不跨越下一次尝试。
5. **View 渲染**：`buildContent()` 开头插入一行 `m.theme.Danger.Render(m.lastError)`。操作失败属于 Negative 语义（`StatusTone` 注释：failed/error → Danger），比 rules 页数据缺失用的 Muted 更醒目。位置安全性：`buildContent` 内 focus 行号映射基于 `sectionBase := len(lines)` 动态计算，首行插入自动保持 `View()` 裁剪与 `ensureFocusVisible()` 一致，无需调整偏移。
6. **文案常量**：`internal/tui/ui/strings.go` 增加 `ProxySelectFailed = "Proxy selection failed"`（proxies 组，`TimeoutLabel` 旁）。

## 5. 用户界面

失败时页面首行出现红色提示（其余布局不变）：

```text
Proxy selection failed
╭─ GLOBAL · SELECTOR · 1 ─────────────────╮
│ ▾  Now: old                              │
│ ...                                      │
╰──────────────────────────────────────────╯
```

## 6. 测试计划

`internal/tui/pages/proxies/model_test.go`（复用现有 `fakeClient` / `updateProxyKey` / `applyProxyCmd`）：

- 契约：`selectionResultMsg{}.Err()` 返回携带的 err；零值为 nil。
- 失败：fake client 返回错误 → view 含 Danger 渲染的 `ui.ProxySelectFailed`；`groups[0].Now` 不变；pending 标记清除。
- 成功清除：失败后再成功 → 提示消失、`Now` 更新。
- 新发起清除：失败后再次 enter（结果未回）→ 提示立即消失。

## 7. 风险

- 改动仅触碰单页 UI 状态，无后端 / 协议变更；`buildContent` 首行插入对滚动与 focus 映射无影响（§4.5）。
- CI 预检（memory 已记录）：gofmt 对齐新常量、golangci-lint 0 issues、`go test ./...` 全绿后才可 push。
