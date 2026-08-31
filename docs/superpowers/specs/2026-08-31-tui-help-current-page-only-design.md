# TUI `?` 帮助仅展示当前页按键

日期：2026-08-31
状态：待审核
关联：#162 已合入 `dev`（#178）。本变更是后续行为修正。
目标分支：`feat/162-help-current-page-only`
工作目录：`.worktrees/feat-162-help-current-page-only`
前序：`docs/superpowers/specs/2026-08-31-tui-help-page-design.md`

## 1. 问题

`?` 打开帮助后，`RenderHelp` 把 **Global + 当前页 + 其余全部 rail 页 + 全部 mode + Setup** 堆进同一份正文。在 Overview 按 `?` 仍会看到 Proxies / Conns / Rules / Logs 的键。用户无法按「当前打开的页面」过滤。

Catalog 仍然需要覆盖所有页（底栏 SSOT、同键异义），但**帮助正文只应回答：现在这个页面我能按什么。**

## 2. 目标

按 `?` 时帮助只包含：

1. **Global:** 始终（`1–8`、`?`、`q`、`Ctrl+C`、rail 导航等）
2. **This mode · …:** 仅当当前确实处于某叠加态（搜索、详情、列选择、表单、端口编辑）。这是当前页上下文，不是「其它页的百科」。
3. **This page · \<当前页名\>:** 当前页默认态绑定

不再输出：其它 rail 页节、未激活的 mode 节、非当前页的 Setup 节。

## 3. 非目标

- 不改 Catalog 内容、不改底栏 `RenderFooter` / `FitFooter`。
- 不新增快捷键。
- 不改帮助滚动、`Esc` 关闭、`InputText` 下不抢 `?`。
- 不把其它页的同键交叉引用写回来。
- 不改 `/v1`、CLI、daemon、`CHANGELOG.md`。

## 4. 行为

`RenderHelp(active, mode)` 删除以下段落：

- `for _, id := range RailPages()` 写其它页
- `for _, m := range []string{ModeSearch, …}` 写未激活 mode
- `if active != PageSetup { write(Setup) }`

保留 Global、可选 This mode、This page。

打开 Overview：`Global` + `This page · Overview`。看不到 Conns / Proxies。

打开 Connections 且正在搜索：`Global` + `This mode · Search` + `This page · Conns`。看不到 Rules。

同键异义改由「分别打开各页帮助」证明：`RenderHelp(Connections)` 的 This page 含 `p` pause，不含 `cycle proxy`；`RenderHelp(Subscriptions)` 的 This page 含 `p` cycle proxy。

## 5. 测试

- `RenderHelp(PageProxies, "")` 含 `Global:`、`This page · Proxies`、`Ctrl+T`；**不含** `Conns:`、`Rules:`、`Subs:`、`Logs:`、`Web GUI:`、`Setup:`、`Search:`、`Confirm:`。
- `RenderHelp(PageConnections, ModeSearch)` 仍是 Global < This mode · Search < This page · Conns，且不含 `Rules:`。
- 同键：分三次 `RenderHelp` 断言 Connections / Subscriptions / Rules 的 `p`/`u` Label。
- Shell：`?` 在 Proxies 打开后可见 `This page · Proxies`，不可见其它页节标题。
- `RenderHelp(PageSetup, "")` 含 Setup 的 `continue` / `skip GeoIP`，不含其它页节标题。
- 底栏 golden / `TestRenderFooter_MatchesCurrentLayout` 不变。

## 6. 文档

更新前序 spec §6 排序规则：去掉「其余 rail 页 / 未激活 mode / Setup」。本文件为行为收口。
