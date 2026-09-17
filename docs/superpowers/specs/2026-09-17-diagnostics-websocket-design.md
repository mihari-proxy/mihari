# F2 诊断页与 WebSocket 大消息修复

状态：用户已确认界面方向、逐条保留及 1 MiB 上游容量方案，代码与文档已实现。

## 工作位置

- 基于最新 `origin/dev` 的 `a636792`。
- Worktree：`.worktrees/fix-diagnostics-websocket`。
- 分支：`fix/diagnostics-websocket`。

## 已确认的需求

1. F2 使用紧凑事件列表加清晰详情区，强化选中状态、级别颜色、时间和来源，减少重复标题及空白。
2. 宽终端左右排列，窄终端上下排列。
3. 每次错误保留独立记录，不做内容聚合，不隐藏重复发生。
4. 同时调查截图中的 `websocket: message too big: read limited at 32769 bytes`。
5. 用户授权只读检查本机正在运行的服务。

## 已查明的事实

- `internal/tui/diagnostics.go` 是所有页面共享的 F2 窗口，修改会同时改善其他页面的 F2。
- 当前列表每条占两行，选中状态只有 `>`；详情复用 `TerminalText`，导致摘要与详情相同的时候重复显示错误。
- 当前小于 72 列时只展示列表或详情；窗口高度预算与 Dialog 边框、padding 不一致，存在底部裁切风险，需要尺寸回归验证。
- `internal/web/server.go` 的 `proxyWebSocket` 对上下游均未调用 `SetReadLimit`。
- 项目锁定 `github.com/coder/websocket v1.8.15`，默认单条消息上限为 32768 字节；内部加一字节检查消息结束，因此越界错误显示 32769。
- `internal/mihomo/stream.go` 已显式采用 1 MiB 单消息上限。
- 既有 `TestGatewayWebSocketReadLimitKeepsSingleFailureOwner` 使用约 88 KiB 数据证明默认网关上限拒绝消息；修改上限后该测试必须改为验证新边界，同时保留失败单点记录和 relay 清理断言。
- 本机 Mihari 服务处于 Running；二进制元数据中的 websocket 版本与仓库一致。
- 本机当前 daemon 日志有 15,023 条匹配 `32769 bytes` 的 `websocket.relay.failed`，最近一次为 2026-09-17 11:31:34 +08:00；只读取日志并输出聚合统计，未读取业务配置或凭据。
- 尚未取得实际失败消息的大小或路由证据，不能将具体故障断言为 `/connections`。

文档依据：[coder/websocket v1.8.15 的 SetReadLimit 实现](https://github.com/coder/websocket/blob/v1.8.15/read.go)，经 Context7 library → docs 查询并与本地模块缓存核对。

布局依据：[Lip Gloss v2.0.5 的渲染实现](https://github.com/charmbracelet/lipgloss/blob/v2.0.5/style.go)。Context7 查询后进一步核对项目锁定版本：Width 包含边框和 padding，制表符默认展开为四个空格，换行前需要保持相同的列宽计算。

## 已确认的实现方案

### 诊断页

- 复用项目现有 Theme，不新增 UI 依赖。
- 列表显示级别、时间、来源及摘要；突出当前选中行，显示记录位置。
- 详情显示完整日期时间、来源、事件、可用的操作/错误码及原始详情。仅在摘要与详情相同时省去重复摘要，复制仍返回原始详情。
- 按实际可用行列分配边框、列表、详情和帮助栏空间，宽屏左右、窄屏上下；支持中英文长行换行和详情滚动。
- 短历史与短详情按内容缩小弹窗，避免大量内部空白；高度不足以同时展示两区时提示扩大终端。
- 保持 Tab 切换焦点、方向键/PgUp/PgDn/Home/End 导航、c 复制、Esc/F2 返回；底部提示按焦点和宽度适配。
- 新记录到来时不抢走当前选择或滚动位置；详情过期、历史查询失败与截断仍明确显示。

### 网关容量边界

- 仅 controller → browser 方向将单消息读取上限显式设为 1 MiB，与现有 mihomo 流客户端一致。
- browser → controller 方向显式保留 32 KiB。
- 超限继续终止转发并记录一次失败；正常关闭保持安静，两方向 relay 必须完成清理。
- 仍使用整条消息缓冲；上游允许缓冲的单消息容量从 32 KiB 增至 1 MiB，约为原来的 32 倍。并发连接会叠加该成本。1 MiB 仍有可能不足以容纳极端连接快照，不能在没有实际负载证据时声称覆盖所有场景。
- 不更改公开协议、持久化格式或认证方式。当前任务不替换或重启真实服务。

## 验证计划

1. 先添加并运行失败回归：大于 32 KiB 且不超过 1 MiB 的上游消息成功逐字节转发；边界值、超过 1 MiB、浏览器方向超过 32 KiB 分别验证。
2. 验证真正超限仍只记录一次，正常结束无错误，两方向 relay 与上游均退出。
3. UI 回归覆盖宽屏、窄屏、较短终端、长中文/英文、选中高亮、摘要去重、末行可达、焦点切换、复制、过期提示、后台插入不跳选中。
4. 运行 `internal/tui`、`internal/web` 目标测试与相关集成测试，按风险扩大至全仓测试、race、vet、CGO-free 跨平台编译。
5. 使用合成诊断记录检查渲染，不在测试中访问真实用户配置或服务。
6. 同步 README 的 F2 布局与读取上限说明；不修改 CHANGELOG。

## 决策记录方式

术语已记录到根 `CONTEXT.md`。本次布局及容量常量均可局部回退，当前不满足难以逆转的 ADR 门槛，不另建 ADR。

## 验证记录

- Red：新增上游消息回归在 32,769 字节处因断连失败；布局测试确认窄屏隐藏窗格、边框裁切与重复摘要。补充长文本与短历史用例进一步捕获二次折行和空白过多的问题。
- Green：目标测试、`go test ./...`、`go vet ./...` 已通过；最终 TUI/Web 源码另行通过 `go test -race ./internal/tui ./internal/web` 和对应 vet。
- 已检查宽屏、窄屏和短窗口的 golden 渲染，使用合成记录检查彩色布局；测试和预览不读取真实用户数据。
- Windows/Linux/macOS × amd64/arm64 六个目标均通过 `CGO_ENABLED=0 go build -buildvcs=false -trimpath ./cmd/mihari`，产物位于仓库外临时目录。
- 修改的 Go 文件已通过 gofmt，`git diff --check` 无错误。没有修改 CHANGELOG，也没有替换或重启本机服务。
- 全仓 `go test -race ./...` 已通过；其中既有订阅包测试耗时约 503 秒，无 race 报告或测试失败。
