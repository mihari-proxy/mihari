# System Logging Level 行内编辑执行方案

日期：2026-09-17

设计依据：[已确认交互](../specs/2026-09-17-system-logging-level-editor-design.md)。

工作区：`.worktrees/feat-system-logging-level-editor`；功能分支：`feat/system-logging-level-editor`；PR 目标：`dev`。

## 范围与实现

1. System 页保留独立 Level 候选值，以现有 editID 表示编辑所属行。首次 Enter 只进入编辑；左右键预览四档循环与 SILENT 特殊入口，Esc 取消，上下和 Tab 不导航。
2. 再次 Enter 通过现有 typed client 提交候选与确认时最新 revision。值等于最新实际值时直接退出；外部状态刷新不覆盖候选。编辑和提交期间由 shell 输入模式阻止全局页面跳转。
3. 提交时复用行内 Braille 动画。成功以 root 已接受的状态为准并退出编辑；失败与 revision 冲突重载后保留候选、显示错误，不自动重放。断连或能力失效清除草稿、pending 并恢复 shell 导航。
4. 行内焦点从 Level 标签移到 `< LEVEL >`；提供级别选择与 Applying 专用 footer/help。更新英文及中文 README，不改 CHANGELOG、依赖、协议或持久化。

## Red–Green–Refactor 与验收

- [x] 先补回归测试并执行，确认因为尚无候选交互而失败。
- [x] 覆盖首次 Enter 无 PATCH、焦点高亮、左右循环及 SILENT、Esc 和被禁用的导航、无变化确认。
- [x] 覆盖外部更新保持候选、确认使用最新 revision、Applying 动画及输入锁定、成功退出、失败重试、冲突重载和断连清理。
- [x] 覆盖 root 输入模式和快捷键提示，调整依赖旧单次 Enter 行为的测试。
- [x] 最小测试通过后执行 `go test ./internal/tui/...`、`go test ./internal/integration`、`go test ./...`、`go vet ./...`；执行可用的 race 验证和六目标 CGO-free 构建。
- [x] 检查 gofmt、git diff、变更范围与文档；创建符合 DCO 的 Conventional Commit，推送并提交 PR 到 dev。

PR 最终验收标准：当前提交的 CI 和 Bot review 全部通过，相关 review 问题处理并重新验证后汇报，不自动合并。后续提交及最终验收证据以 [PR #270 的检查与 review](https://github.com/mihari-proxy/mihari/pull/270) 为准。

## 执行记录

- 已完成只读调查并建立独立 worktree。开发不触碰原工作区的 `.gitignore` 修改。
- Red：`go test ./internal/tui/pages/system -run '^TestLoggingLevelEdit_' -count=1` 首次失败，原因包括首次 Enter 直接进入 pending、无候选态及取消/重试不符合预期；不存在编译错误。
- Green：上述测试及 `go test ./internal/tui/...` 通过。真实 System 页与 root shell 组合测试验证输入锁定、成功恢复及断线恢复；动画用注入 tick 验证，无固定 sleep。
- `go test ./internal/integration` 通过；`golangci-lint run ./...`（2.12.2）返回 0 issues。
- 六目标 `CGO_ENABLED=0 go build ./cmd/mihari` 通过：Windows、Linux、macOS 的 amd64/arm64。构建输出写入 NUL，未生成待提交二进制。
- `go test ./...`、`go vet ./...`、全仓 `gofmt -l .`、`git diff --check` 通过。
- 功能提交：`03fea25`，包含 DCO；[PR #270](https://github.com/mihari-proxy/mihari/pull/270) 指向 `dev`。
- 首次本地 `go test -race ./...` 在既有 `TestLegacyResourceRecovery_WholeTupleEveryForwardAndRecoveryBoundary` 触发默认 10 分钟超时，未报告数据竞争；本次 TUI 包 race 已通过。按 CI 已有配置改为 `go test -race -timeout=30m ./...` 重跑，不修改订阅代码。
- 本地 `go test -race -timeout=30m ./...` 重跑全部通过，其中订阅包耗时 424.858 秒；没有数据竞争报告。
- 功能提交 `03fea25` 的 [CI](https://github.com/mihari-proxy/mihari/actions/runs/35179487340) 和 [原生安全检查](https://github.com/mihari-proxy/mihari/actions/runs/35179487347) 全部通过，包含三系统 unit/race/vet-format、六目标构建、lint 和覆盖率任务。后续文档提交及 review 收口按上述 PR 最终验收标准继续验证。
