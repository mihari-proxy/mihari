# TUI 检索、连接历史与日志筛选执行计划

日期：2026-09-17

设计依据：[已确认设计](../specs/2026-09-17-tui-search-history-log-filter-design.md)。

用户授权：测试驱动开发、提交、推送、创建面向 dev 的 PR；每 10 分钟检查 CI 与 bot review，按反馈修复至全绿。合并不在本次执行范围内。

## 1. 回归测试先行

- 三个页面分别验证 Ctrl+F 保留 Unicode 查询、光标末尾、已编辑状态、详情/弹窗隔离。
- shell 验证侧栏/内容区均可进入搜索，紧接数字输入不切页，全局弹窗不被穿透。
- 连接历史验证默认构造与页面构造均保留 501、5000 条，5001 条时 FIFO 淘汰，活动连接无此上限。
- 日志通过实际按键验证多选、确认、取消、空选提示、Select all、连续/非连续摘要、warning 别名、查询交集及新记录。
- shell 验证级别弹窗屏蔽全局导航，F2/退出保持、切页/重连保留已选项，重建 TUI 恢复全选，以及宽窄窗口尺寸。
- 运行目标测试并记录正确的行为失败，不能以编译失败替代 Red。

## 2. 最小实现

- 通过页面搜索聚焦小接口连接 shell 与三页，保留原 `/` 行为及弹窗优先级。
- 单一默认容量常量供连接历史与页面共用，保持原记录模型和淘汰顺序。
- 日志页增加独立候选状态、多选级别匹配与摘要格式，复用现有居中遮罩；shell 优先将弹窗键交给页面。
- 帮助、页脚及中英文 README 同步用户行为，检查现有快照差异。

## 3. 验证与交付

- 先目标包与整个 TUI，再默认全仓测试、race、vet、格式与 diff 检查。
- Windows、Linux、macOS 的 amd64/arm64 六目标 CGO_ENABLED=0 构建。
- 使用合成日志与连接 fixture 验证，不访问真实订阅、mihomo 或系统服务。
- 检查准确 diff；Conventional Commit 中文摘要并 DCO sign-off；推送功能分支并创建 dev PR，描述行为和实际验证结果。

## 4. CI 与 review 循环

- 创建 PR 后获取初始 CI/review 状态，此后以 10 分钟为检查间隔。
- 同时查看 checks、review、行内评论和 PR 普通评论；判断反馈是否适用于当前实现。
- 适用问题先补失败回归，再做最小修复、验证、提交并推送；记录处理结果。
- 当前 PR 提交的 CI 全部完成并通过，bot review 无未处理的有效问题后汇报 PR、验证与剩余限制。

## 执行记录

- 已完成访谈、设计和实现；新增回归测试首先因 Ctrl+F 无效、历史在 500 条截断、Level 循环单选而失败，随后通过。
- 自查补充其他页面 Ctrl+F 路由及极小窗口禁止提交不可见选择的失败回归，并完成最小修复。
- `go test ./...`、`go test ./internal/tui/...`、`go test -race ./internal/tui/...` 均通过。
- `go vet ./...`、`golangci-lint run ./...` 通过；lint 为 0 issues。修改的 Go 文件 gofmt 无输出。
- 最终代码完成 Windows/Linux/macOS 的 amd64/arm64 六目标无 CGO 构建。
- 非快照 diff 空白检查通过；golden 文件保留固定终端布局所需的行尾空格。宽窄级别弹窗快照和已有 Logs 快照已核对。
- `go test -race ./...` 已完整通过。PR：[#272](https://github.com/mihari-proxy/mihari/pull/272)。
- 首轮远端三平台 unit/race/vet、六目标构建、coverage、lint、安全测试及其他 CI 检查全部通过。CodeRabbit 无可操作代码问题，Cubic 完成为非阻断 neutral，Pullfrog 通过并提出两条可读性建议。
- 按 Pullfrog 建议明确级别匹配表达式括号、说明连续后缀的摘要压缩条件，并增加空级别记录仅在全选时可见的回归覆盖。窄窗口页脚按既有预算省略部分快捷键，完整操作仍见帮助；不改变既有页脚优先级。
- CodeRabbit 的通用 docstring 覆盖率提示为非阻断建议；新增导出接口与方法已有 Go 文档注释，不引入项目未规定的固定注释覆盖率门槛。最终提交的 CI 与 review 状态继续维护于 PR。
- 未执行真实订阅、mihomo、系统服务或其他 testenv 操作。
