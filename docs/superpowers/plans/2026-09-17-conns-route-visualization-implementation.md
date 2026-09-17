# 连接详情纵向链路执行方案

设计：[连接详情纵向链路](../specs/2026-09-17-conns-route-visualization-design.md)。

用户授权：先完成设计和执行方案，rebase 最新 dev 后按测试驱动开发；提交并推送 PR，每 10 分钟检查 CI 和 bot review 并修复反馈；全部检查通过后 bypass 合并，触发 dev 发版并每 10 分钟检查到完成。不得把该授权扩展到真实订阅、系统服务或无关修改。

## 实施步骤

1. 保存当前任务的文档及测试草稿，fetch origin/dev，在 feat/conns-route-visualization worktree rebase origin/dev 后恢复草稿；保留其他 worktree 和用户已有修改。
2. Red：运行连接详情目标测试，确认失败由缺少纵向链路、字段归属、颜色或特殊状态导致，修正测试自身问题后才能进入 Green。
3. Green：只改 TUI 详情展示；重组四阶段主体及流量区，反向展示选择链，按地址关联 GeoIP，处理缺失值、REJECT、长字段和限缩进树；保持观测对象与协议不变。
4. 更新相关 fixture 和渲染快照。验证真实页面导航到详情、滚动到底、缩放、暂停、关闭状态以及诊断窗口返回；用离线合成数据检查完整界面。
5. 同步 README 中英文说明和本设计/执行文档。默认包测试不得访问真实 daemon、订阅、mihomo 或用户配置。
6. 执行连接包、TUI、相关集成测试和全仓测试、vet；对修改范围执行 race，并完成 Windows/Linux/macOS 无 CGO 编译。CI 的全平台 race、lint 与其他必需检查必须通过。
7. 检查准确 diff，确保无 CHANGELOG、依赖、协议或其他无关改动；按 Conventional Commits 中文摘要和 DCO sign-off 提交，推送并创建面向 dev 的 PR。
8. 每 10 分钟检查该 PR 当前 head 的 CI、bot reviews 与 review threads。逐条审查反馈，修复有效问题并复测；等待最新提交全部检查通过且反馈处理完毕。
9. 再次验证目标分支、head、检查状态及合并能力，按授权 bypass squash merge；确认 dev 含合并结果。
10. 阅读当前 dev 发版 workflow 的 dispatch 输入与守卫，触发正确的 dev 发版。记录 run ID、目标 SHA，每 10 分钟检查该 run 至成功；若失败，调查原因并在授权范围内修复后重试。

## 验收映射

| 要求 | 验证证据 |
| --- | --- |
| 四阶段与字段归属 | 连接详情字段测试、完整及紧凑快照 |
| Inbound 名称优先与 Rule Matched 合并 | 组合字段断言、缺失值/无 payload 用例 |
| 出站选择顺序、深层名称完整 | 真实上游顺序 fixture、原观测不变断言、极窄/深层树测试 |
| Remote/Destination 各自 GeoIP | 不同 IP、同 IP、IPv6 等价地址、无目标 IP 与查询失败测试 |
| 上传绿色、下载蓝色 | 速率和累计量 ANSI 样式断言，正常/关闭、宽/窄布局 |
| REJECT 断线及灰色目标 | REJECT/REJECT-DROP 状态测试及视觉快照 |
| 生命周期与 88 列边界 | 既有滚动、resize、暂停、关闭、回到原列表测试 |
| PR 和发版完成 | 当前 head CI 与 review 检查、合并状态及 SHA、dev release run 最终状态与发布产物 |

## 执行记录

- 初始任务工作树位于 b91499c；保留先前设计访谈文档及未完成的测试草稿，重新执行 Red 验证，不声称先前中断测试已通过。
- 已 rebase 至 origin/dev 的 a636792，任务草稿完整恢复，无冲突。
- Red：目标测试确认旧布局缺少 Application/Outbound 等阶段、未按地址分配 GeoIP、未给流量数值着色，并缺少深层层级标记。
- Green：连接详情包测试通过；新增覆盖四阶段字段归属、叶子优先观测的反向展示、观测不变、REJECT/REJECT-DROP、深层长名称、速率/累计量配色，以及直连同 IP 与 IPv6 等价地址。
- TUI 全包与 internal/integration 测试通过。已更新完整、紧凑、关闭、暂停、滚动到底快照，并新增 DIRECT 与 REJECT 整页快照；核对了完整内容与视口边界。
- go vet ./...、golangci-lint run ./...、gofmt -l cmd internal、git diff --check 通过。首次 lint 指出的测试字符串判断已修正并复测。
- Windows/Linux/macOS 的 amd64 与 arm64 六目标 CGO_ENABLED=0 编译通过，产物仅在临时目录。
- Connections 包语句覆盖率 79.4%；本次涉及的 detail_content.go 与 detail_route.go 函数语句覆盖率均为 100%，未设固定覆盖率门槛。
- go test ./... 与 go test -race ./internal/tui/... 通过；合并仍须以最新 PR 提交的全平台 CI 与 bot review 结果为准。
