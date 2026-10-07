# System 后台版本检查与出口选择展示实施方案

日期：2026-09-22。

设计依据：[System 后台版本检查与出口选择展示](../specs/2026-09-22-system-background-check-design.md)。

工作区：`.worktrees/dev-20260922`；功能分支：`feat/system-background-check`；目标分支：`dev`。

## 范围与实现

1. 为 Core 与 Mihari 版本检查分别保留独立的 checking 状态。检查使用现有 Braille `Checking` 状态 chip 和同一行动画时钟，不再占用 `pending`、`pendingRow` 或 `pendingNote`。
2. System 页在检查进行时保持全部交互可用。下载、安装、通道切换和设置提交仍使用现有 mutation busy 锁；开始 Mihari 更新或切换 Mihari 通道会使旧检查结果失效，防止其覆盖后续状态。
3. Core 检查完成、失败或被新状态替代时停止其动画；保留现有五分钟成功缓存、in-flight 去重、Core 通道变更重查和离页结果投递语义。
4. System → Network 的 Outbound Interface 主行移除 `Saved ·` 前缀，并将当前 `Automatic` 或已保存网卡名以亮黄色 token（256 色 228）渲染。状态后缀维持原样。弹窗内的 `Saved` 文字、候选焦点和 Apply 语义不改。
5. 不改变本地控制协议、CLI、持久化、更新频率、下载或安装流程，也不修改 `CHANGELOG.md`。

## Red–Green–Refactor 与验收

- [x] 先新增测试，证明 Core 检查以状态 chip 动画显示、Mihari 检查不设全局 pending 且不阻止 Core action、过期结果不会覆盖后续 mutation。
- [x] 新增 Network 主行测试，证明无 `Saved`，选定模式使用亮黄色 228，弹窗保留 `Saved`。
- [x] 运行最小测试，确认失败来自缺少目标行为。
- [x] 用最小实现分离 checking 状态、驱动/停止动画，并仅调整主页面的出口渲染。
- [x] 运行 System 包、全部 TUI 测试、全仓测试、race、vet、gofmt 和 CGO-free 当前平台构建。
- [x] 构建 Windows 本地二进制到忽略的 `bin/`，验证版本命令，再安装到当前用户的 Mihari 程序目录。官方安装脚本只会下载 GitHub Release，不能装入这份未发布的本地构建。

## 执行记录

- 已建立独立 worktree；未触碰原工作区的未提交修改。
- 2026-09-22：`gofmt -l` 对改动的 Go 文件无输出。`go test -count=1 ./internal/tui/pages/system` 通过。`go vet ./...` 与 `go test -race -count=1 ./...` 通过（约 414s）。
- 构建产物与本机安装数据不提交。
- 2026-09-22：本地 Windows 构建将 `buildinfo.Version` 设为 `v0.9.6-dev.7-local.2`，用于和已发布的 `v0.9.6-dev.7` 区分。构建产物与本机安装文件不提交。
