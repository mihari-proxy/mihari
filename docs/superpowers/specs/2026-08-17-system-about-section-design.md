# TUI System 页 About 分区设计

日期：2026-08-17
状态：已定稿
目标分支：`feat/system-about-section`
工作目录：`.worktrees/feat-system-about-section`

## 1. 背景

Mihari TUI 没有产品介绍和仓库入口。用户要查项目说明或开 GitHub，只能离开终端去 README。

左侧 rail 已有 8 个操作页，数字键 `1`–`8` 按此顺序跳转（#28）。About 内容只有一句描述和一个公开链接，单独占第 9 个页签会挤操作页，也会打乱快捷键。

System 页已经承载 Mihari 版本和 `Update Mihari`，并且有 `openBrowser`（面板行 Enter 打开浏览器）。产品身份和打开链接放在这里最自然。

## 2. 目标

- 在 System 页最底部增加 `About` 分区，展示一句话描述和 GitHub 地址。
- GitHub 行可聚焦；Enter 用现有 `platform.OpenBrowser` 打开仓库。
- 描述行可聚焦；Enter 打开与 Daemon / Core 相同的详情页，展示稍长说明。
- 不依赖 daemon、不要求提权。断开或未提权时 About 仍可看、可打开链接。
- 不增加 rail 页签，不改 `1`–`8`，不改 `/v1`、CLI、Web GUI、帮助层。

## 3. 非目标

- 不新增 rail 页 `About`，不改 `ui.RailPages`。
- 不在 Overview banner 下加介绍。
- 不把 About 塞进 `?` 帮助层。
- 不把 GitHub 打开做成需要确认的 mutation，不新增 `ui.Action`。
- 不重复版本号（Daemon / Update Mihari 已有）。
- 不加许可证、贡献者、更新日志。
- 不为 About 单独做 System 页滚动；现有页面在矮窗口下本来就会裁切底部。
- 不改控制协议、持久化、发版或安装脚本。

## 4. 方案比较

### 4.1 采用：System 页末尾 About 分区

复用现有 `row` + `RenderBorderedSection`。两行：静态描述 + 打开 GitHub。不占 rail，和版本/更新在同一页。

### 4.2 不采用：Overview 大标题下加文案

Overview 是运行看板（KPI / banner）。产品介绍和实时状态混在一起，也没有现成的「打开链接」动作行。

### 4.3 不采用：独立 rail 页

内容撑不满一页。`1`–`8` 要改成 9 键，或把 About 挤出数字键。About 访问频率远低于操作页。

## 5. 用户界面

分区顺序：Daemon → mihomo core → System service → Network → **About**。

进入 System 页时焦点仍在 `Daemon`，不跳到 About。`↓` 可走到最后两行。

```text
┌─ About ──────────────────────────────────────────────┐
│   Mihari          A local manager for mihomo          │
│ > GitHub          github.com/mihari-proxy/mihari      │
└──────────────────────────────────────────────────────┘
```

- 未聚焦：两行都是普通标签 + 值。
- 聚焦：左侧 `>`，标签侧走现有 `RowFocus`。
- 值区不使用 StatusDot、Pending / Done / Failed chip。成功打开浏览器保持静默（与面板打开成功一致）。

矮窗口下 About 可能被 `View()` 的固定高度裁掉，与当前 Network / Service 溢出行为相同。本设计不新增滚动。

## 6. 交互

### 6.1 Mihari 行（查看）

Enter 走 System 页 `default` 分支：`m.detail = &selected`。详情标题为 `Mihari details`，正文为稍长说明。Enter / Esc 关闭，与其他只读行相同。

### 6.2 GitHub 行（打开浏览器）

Enter **不**走 `ActionIntentMsg`，也 **不**弹确认。直接：

1. 调用 `m.openBrowser`（nil 时回落到 `platform.OpenBrowser`）。
2. URL 固定为 `https://github.com/mihari-proxy/mihari`。
3. 页面只展示无 scheme 的 `github.com/mihari-proxy/mihari`。
4. 失败：写入现有 `lastError`（Danger，钉在内容顶部）。不得把完整 URL 以外的敏感信息写入错误；本链接是公开仓库，展示静态失败文案即可。
5. 成功：不打 Done 徽章，不记 mutation ledger。
6. `pending` 为真时忽略 Enter，与其他行一致。
7. 不检查 `mutationsEnabled`、elevation、capabilities。

不新增 `ui.Action` / `knownAction` / `RequiresDaemon`。面板打开必须走 `ActionOpenWebGUI`，因为它要 daemon 发一次性 token，且 `RequiresDaemon` 默认为 true。GitHub 是本地打开公开 URL，不需要这条链路。

### 6.3 页脚

继续用 `FooterSystem`：`Esc back  Enter activate  ? help  q quit`。不改帮助正文。

## 7. 文案与常量

全部放在 `internal/tui/ui/strings.go` 的 System 组，英文，与现有 TUI 标签一致。

| 常量 | 值 |
| --- | --- |
| `AboutSectionTitle` | `About` |
| `AboutNameLabel` | `Mihari` |
| `AboutDescriptionValue` | `A local manager for mihomo` |
| `AboutDescriptionDetail` | `Mihari is a standalone local manager for mihomo. CLI, TUI, and browser panels share one daemon control plane.` |
| `AboutGitHubLabel` | `GitHub` |
| `AboutGitHubDisplay` | `github.com/mihari-proxy/mihari` |
| `AboutGitHubURL` | `https://github.com/mihari-proxy/mihari` |
| `AboutGitHubOpenFailed` | `Could not open GitHub` |

仓库地址是项目身份，写死常量，不当 settings / 环境变量。

## 8. 实现边界

只改 TUI 表现层：

- `internal/tui/pages/system/model.go`
  - 增加 `rowAbout`、`rowGitHub`。
  - `rows()` 末尾追加 About 两行（`aboutRows()`）。
  - `enter` 对 `rowGitHub` 调用 `openGitHub()`；`rowAbout` 落入现有 `default` 详情。
- `internal/tui/ui/strings.go`：上表常量。
- `internal/tui/pages/system/model_test.go`：行为测试。

不改：`internal/tui/ui/page.go`、`actions.go`、`internal/control/**`、CLI、Web GUI、`platform.OpenBrowser` 签名。

`SetOpenBrowser` 测试缝继续用于注入假浏览器。

## 9. 错误处理

- `openBrowser` 返回错误 → `lastError = AboutGitHubOpenFailed`。
- 不把 `err.Error()` 原文渲到界面（可能含路径或系统细节）。
- 下一次成功打开或离开页面时清掉（沿用 System 页现有 `lastError` 清理时机；若离开已清则不必新开路径）。
- 空 URL 不会发生：URL 是编译期常量。

## 10. 测试

全部放在 `internal/tui/pages/system/model_test.go`，不访问公网。

1. **渲染**：`View()` 含 `AboutSectionTitle`、`AboutDescriptionValue`、`AboutGitHubDisplay`；不含 `https://`（展示名无 scheme）。
2. **顺序**：`rows()` 中 About 两行在 Network 之后；`focusID` 默认仍是 `rowDaemon`。
3. **焦点**：从首页连续 `down` 能落到 `rowAbout` 与 `rowGitHub`。
4. **描述 Enter**：`focusID=rowAbout` + Enter → `detail` 非空且含 `AboutDescriptionDetail`；再 Enter / Esc 关闭。
5. **打开成功**：注入 `SetOpenBrowser`，Enter `rowGitHub` 收到恰好 `AboutGitHubURL`；`View()` 无 `DoneLabel` / `FailedLabel`。
6. **打开失败**：假浏览器返回错误 → `View()` 含 `AboutGitHubOpenFailed`，且为 Danger 语义（页面顶部错误区）。
7. **离线 / 未提权**：`SetMutationsEnabled(false)`、`isElevated=false` 时，渲染与打开仍成功。
8. **pending 忽略**：`pending==true` 时 Enter GitHub 不调用浏览器。

不改 golden：现有 golden 拍的是 rail 上的 `8 System`，不是 System 页内容。

## 11. 安全与架构

- 打开的是固定 HTTPS 公共仓库，不是 controller / Web gateway / 订阅 URL。
- 不把 token、secret、完整订阅 URL 写进 About。
- 浏览器由 TUI 进程拉起，不经 daemon，不破坏「daemon 是唯一写入者」。
- 控制面仍是 named pipe / Unix socket，不新增监听。
- 保持 `CGO_ENABLED=0`。

## 12. 建议验证

- `go test ./internal/tui/pages/system/`
- `gofmt -l` 修改过的 Go 文件
- 不改协议包时不必跑 `./internal/control/...`
- 人工：System 页拉到最底看到 About；Enter GitHub 打开仓库
