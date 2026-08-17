# System 页 About 分区 Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 在 TUI System 页最底部增加 About 分区：一句话描述 + Enter 打开 GitHub。

**Architecture:** 复用 System 页现有 `row` 与 `RenderBorderedSection`。描述行走默认详情；GitHub 行直接调 `openBrowser`，不新增 `ui.Action`、不经 daemon。

**Tech Stack:** Go TUI（bubbletea / lipgloss），`internal/tui/pages/system` + `internal/tui/ui/strings.go`

**工作目录:** `.worktrees/feat-system-about-section`  
**设计:** `docs/superpowers/specs/2026-08-17-system-about-section-design.md`

---

### Task 1: 文案常量

**Files:**
- Modify: `internal/tui/ui/strings.go`（System 组，`NetworkSectionTitle` 附近）

增加 `AboutSectionTitle`、`AboutNameLabel`、`AboutDescriptionValue`、`AboutDescriptionDetail`、`AboutGitHubLabel`、`AboutGitHubDisplay`、`AboutGitHubURL`、`AboutGitHubOpenFailed`。值以设计稿第 7 节为准。

### Task 2: 失败测试

**Files:**
- Modify: `internal/tui/pages/system/model_test.go`

按设计第 10 节写测试（渲染、顺序/默认焦点、↓ 到达、描述详情、打开成功/失败、离线未提权、pending 忽略）。先跑，确认因缺少 About 行而失败。

### Task 3: 最小实现

**Files:**
- Modify: `internal/tui/pages/system/model.go`

- `rowAbout` / `rowGitHub`
- `aboutRows()`，`rows()` 末尾追加
- `enter`：`rowGitHub` → `openGitHub()`；`rowAbout` 走 default 详情
- 成功清 `lastError`；失败写 `AboutGitHubOpenFailed`；`pending` 时现有 early return 已挡住

### Task 4: 验证

```
go test ./internal/tui/pages/system/
gofmt -l internal/tui/pages/system/model.go internal/tui/pages/system/model_test.go internal/tui/ui/strings.go
```
