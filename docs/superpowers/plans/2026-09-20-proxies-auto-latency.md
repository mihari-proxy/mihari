# Proxies 自动测速与统一页面配置执行记录

设计：[已确认方案](../specs/2026-09-20-proxies-auto-latency-design.md)。

工作目录：`.worktrees/feat-proxies-auto-latency`。
分支：`feat/proxies-auto-latency`，初始基于本地 `dev` 的 `f1abdd37`，交付前 rebase 至 `origin/dev` 的 `58199b2`。
实施与验证：2026-09-20 至 2026-09-21。

## 实现

- Basic 第一行第二列提供 Page Settings 光标入口，保留两行布局；GLOBAL 和组头在当前名称之后显示最终节点的延迟。
- 新增当前页面可见性驱动的自动测速，复用名称去重和 5 路队列；每次进入重新计数，按用户要求直接覆盖已有测速显示。
- 自动请求具有可取消的所属上下文，异步结果固定返回 Proxies，并校验请求身份。取消后释放并发槽，不生成主动取消的错误诊断；旧结果不能覆盖新任务。
- F4 打开全局共用的 C 方案双列弹窗，右侧保留完整 Section 列表。来源 Section 默认展开；左侧跳转展开目标，保留其他折叠状态；右侧光标同步左侧高亮。
- Tab/Shift+Tab 在目录、配置区、Cancel、Save 间循环；Ctrl+S 任意位置保存，浅灰提示位于 Save 旁。保存失败保留草稿，错误完整详情进入既有 F2 诊断历史。
- 两个开关默认开启，扩展既有偏好 DTO、存储和迁移校验。不同页面更新互不覆盖；保存的非默认 Proxies 块在恢复默认后移除。README 说明共享作用域和旧版本兼容范围。

## Red–Green 证据

- 保存含 `proxies` 的偏好：最初因未知字段失败；扩展后验证显式 false 往返，以及 Conns 列更新不重置开关。
- F4 打开、组头选中叶子延迟、Basic 第一行设置入口、进入 Proxies 自动测可见选择：各最小测试先因缺少目标行为失败，再通过。
- 离页取消：新增断言先捕获了主动取消仍会记录错误的问题，按所属上下文排除正常取消后通过。
- 跨 daemon 的保存结果：回归测试先复现旧结果覆盖问题，增加会话校验后通过。
- 保存错误包含换行：回归测试先复现固定布局被打断，改为单行摘要、保留完整诊断后通过。
- 设置入口异步消息：回归测试先复现切页后误开弹窗，绑定来源页面并检查已有弹窗后通过。

## 验证

| 检查 | 结果 |
| --- | --- |
| 相关 TUI、偏好、control、runtime、app 和 integration 测试 | 通过 |
| `go test ./...` | 通过；随后对最终 TUI 修正重跑目标包，通过 |
| `go test -race ./...` | 通过；最终 TUI 修正另行重跑对应 race 测试，通过 |
| 最终 TUI 变更的 `go test -race ./internal/tui/...` | 通过 |
| `go vet ./...` | 通过 |
| Windows/Linux/macOS × amd64/arm64，`CGO_ENABLED=0 go build` | 六个目标均通过，最终修正后重新构建 |
| `python -m pytest scripts/test/test_unix_layout_security.py -q` | 74 passed、4 skipped（Windows 环境下的条件跳过） |
| 修改的 Go 文件 `gofmt -l` | 无输出 |
| 非 golden 文件 `git diff --check` | 通过；golden 保留终端渲染所需的行尾填充 |

UI 快照覆盖 72×22、100×28 的初始弹窗和跨 Section 跳转；布局测试另覆盖 180×42。更新其他页面的快照仅涉及适用页面的 F4 页脚提示。

## 交付范围

构建产物位于 worktree 的忽略目录 `bin/`。未连接真实订阅或真实 mihomo，未修改系统服务。原工作目录的用户 `.gitignore` 修改未改动。

用户随后授权提交、rebase、创建 PR、根据 CI 和可用 bot review 修正，并在全绿后 bypass merge 到 dev。rebase 保留了上游 PgUp/PgDn 功能，翻页和自动测速共用渲染几何；新增回归测试验证翻页后的可见及部分可见卡片会测速，屏外节点不提前测速，已见节点不重复测速。PR 中记录最终 CI 和 review 结果。

## PR 审查修正

- CodeRabbit 指出的重连间隙已由回归测试复现：新 Status 尚未到达时，旧保存响应只凭 status epoch 会被接收。新增偏好连接 generation，在开始重连时立即失效旧保存响应，保留草稿；回归测试由失败转为通过。
- 保存成功、取消、失败保留草稿拆为独立测试。两处 import 建议实际运行 `gofmt` 后均无差异。
- 未采纳过滤 Ctrl+T 正在测速节点的建议：基线 `TestModel_SecondControlTReplacesUnstartedQueue` 明确要求重新建立全部叶子队列；本任务保留已有手动重测语义，自动发现仍按每轮去重。
- Pullfrog 提出的 F2 丢失完整保存诊断无法复现：`observeDiagnosticMessage` 的 default 分支已经消费该消息的 `Err()` 和 `Warnings()`。补充两个端到端模型测试，确认多行长错误完整进入 F2、成功 warning 保留详情且不改变保存成功；无需修改生产逻辑。
