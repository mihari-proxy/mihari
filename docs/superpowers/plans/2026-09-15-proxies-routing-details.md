# Proxies 定位文案与 Routing 视觉细节执行计划

日期：2026-09-15

状态：T01–T05 实现与本地验证已完成；已创建 [PR #247](https://github.com/mihari-proxy/mihari/pull/247)，远端检查与 review 结果以 PR 最新提交记录为准。

设计：[已确认设计](../specs/2026-09-15-proxies-routing-details-design.md)

分支：`fix/proxies-routing-details`

工作目录：`.worktrees/fix-proxies-routing-details`

基线：`origin/dev` @ `f36563139805eebb1e46544512968ba9305bbf18`

## 1. 执行边界

用户已确认设计与文档，并进一步授权开始实施、提交并创建指向 dev 的 PR、检查 CI 与可用 bot review、根据反馈优化至全绿后汇报。PR 合并仍需用户确认。

- 在上述独立 worktree 实施，禁止直接修改或提交 `main` / `dev`；保留其他 worktree 和用户原有修改。
- 实施前复核 `.github/CONTRIBUTING.md`、适用 `AGENTS.md`、README 和设计；检查分支与工作区状态。
- 当前 worktree 没有 `.codegraph/`，不自行索引。若后续已建立有效索引，则优先使用 CodeGraph 查找代码。
- 本次编写计划依据已读的仓库实现，不引入库 API 方案。实施若涉及库 API 细节，按项目要求先用 `ctx7 library` 解析，再用 `ctx7 docs` 核对；不升级依赖或 toolchain。
- 只改 Proxies 展示、必要测试与快照，以及中英文 README。保持模式切换、候选当前性判断、定位身份、IPC 和 daemon mutation 行为。
- 不修改 `CHANGELOG.md`，不连接真实订阅、不启动真实 mihomo、不操作系统服务。
- 用户已授权本任务的 commit、push、PR 和反馈修复；合并仍遵守仓库确认要求。

## 2. 已核实的基线与风险

| 落点 | 基线事实 | 实施要求 |
| --- | --- | --- |
| `routing.go` / `routingHeader` | 标签使用 `Muted.Width(9)`，值使用紫色 `Title`；提示始终显示并右对齐 | 标签改白、值复用绿色；正常操作提示按焦点内联显示 |
| 同上 | 整行最后通过 `ApplyFocusStyle` 反色 | 仅标签和值部分反色，在追加提示前结束反色，防止浅灰提示变灰底黑字 |
| 同上 | 优先给提示预留宽度，再截断值 | 正常操作提示改为值优先、不够则整体隐藏；状态说明保持既有占位策略 |
| `locate.go` / `renderGroupHeader` | 已按定位控件宽度预留空间，文案为 `[Locate]`（8 列） | 文案改为 `Locate Selected`（15 列），增加 7 列后重新验证 30 列场景 |
| `routing_layout_test.go` | `TestRoutingHeader_LongSelectionPreservesHints` 要求旧 `Enter change` 可见 | 更新为新文案与新优先级；保留 pending、unknown、候选等待的有效断言 |
| `locate_layout_test.go` | 辅助函数及多个断言直接匹配 `[Locate]` | 同步测试定位辅助与期望，防止辅助函数先失败掩盖目标行为 |
| `golden_test.go` | `normalizeRender` 去除 ANSI；快照只证明文字与布局 | 单独增加 ANSI 样式验证，确认浅灰底/绿底/提示不反色 |

采用 Red–Green–Refactor。每项先运行目标测试，确认 Red 来自缺少目标行为，而非编译错误、fixture 无效或辅助函数找不到行；再做最小实现、运行相关包并重构。

下述新增测试名为建议名，实施时记录最终真实名称。现有测试中仍有效的导航、状态和边界断言应保留，不通过批量删断言或直接刷新快照绕过失败。

## 3. T01：定位入口文案与宽度

文件：`internal/tui/pages/proxies/locate.go`、`locate_layout_test.go`；回归 `locate_test.go`、`navigation_test.go`。

- [x] Red：增加独立断言，直接检查渲染行含 `Locate Selected` 且不含 `[Locate]`，避免依赖仍匹配旧文案的辅助函数。
- [x] 确认短节点布局为 `Now: two  Locate Selected`，入口紧跟节点名。
- [x] 最小实现：替换文案，沿用根据实际显示宽度预留空间的逻辑；同步测试辅助函数。
- [x] 验证 30、58、80、160 列，正常和 retained/stale 两种状态；中文、emoji 长名称截断，完整入口保留，原始渲染宽度不超过预算。
- [x] 验证组头焦点、入口焦点、内容区失焦、禁用入口样式；定位后仍使用完整节点名，不执行选择请求。

```console
go test ./internal/tui/pages/proxies -run '^TestLocateHeader_'
go test ./internal/tui/pages/proxies -run '^TestLocate_'
go test ./internal/tui/pages/proxies
```

完成证据：记录首次失败断言及修复后结果；旧布局/导航测试覆盖保持有效。

## 4. T02：Routing 配色、分段反色与提示

文件：`internal/tui/pages/proxies/routing.go`、`routing_layout_test.go`。

- [x] Red：添加 `TestRoutingHeader_FocusedActionHint`，在已确认且可操作的 fixture 下验证 Mode / GLOBAL 轮流聚焦时，仅本行显示精确内联提示。
- [x] Fixture 必须满足 `globalCandidatesCurrent` 的 revision、订阅、已知状态与快照当前性要求，不能把 `Waiting for candidates` 误当正常状态。
- [x] 增加失焦与移到组列表的用例：无正常操作提示、无 `›`、无反色。
- [x] 添加 `TestRoutingHeader_SegmentedFocusColors`，验证未选中白标签、绿色值；选中浅灰底黑标签、绿底黑值；提示为深底浅灰字。
- [x] 颜色断言检查相应字符区间的有效前景/背景/反色状态，不只检查输出任意位置存在绿色或 `\x1b[7m`。覆盖值后重置、提示及下一行不继承反色。
- [x] 最小实现：标签改为白色样式，值复用 `Success`；将标签和值作为焦点样式范围，再独立追加浅灰说明。
- [x] 区分正常操作后缀与状态说明；只对正常操作提示施加焦点限制，不修改 `routingKey` 的动作或授权条件。

```console
go test ./internal/tui/pages/proxies -run '^TestRoutingHeader_(FocusedActionHint|SegmentedFocusColors|InactiveContentHasNoFocusStyle|FocusMovesBetweenEntries)$'
go test ./internal/tui/pages/proxies
```

完成证据：文字、焦点和 ANSI 分段样式断言通过；全局 `ui.RowFocus` 与其他页面主题未改变。

## 5. T03：窄屏与状态说明优先级

依赖：T02。文件：`routing.go`、`routing_layout_test.go`；回归 `routing_test.go`。

- [x] Red：添加 `TestRoutingHeader_ActionHintWidthPriority`，测试短值可容纳后缀、恰好容纳后缀、少一列、超长值四种情况。
- [x] 不够时隐藏完整 ` · Press Enter to …`，不留下圆点、半句提示或额外换行；标签和值优先占用宽度。
- [x] 当前值截断时不为操作提示进一步减少值预算；状态说明仍按旧规则预留空间并截断。
- [x] 拆分或更新 `TestRoutingHeader_LongSelectionPreservesHints` 的旧假设，保留状态文本验收，替换正常提示必须常驻的断言。
- [x] 添加 `TestRoutingHeader_StatusNotesRemainVisible`：pending、unknown、尚未确认、候选快照过期在对应行聚焦/未聚焦、侧栏聚焦时仍显示说明且无操作提示。
- [x] 验证 `status.Message` 非空仍在边框内追加一行；Mode pending 的既有判定优先级保持不变。
- [x] 覆盖 30、58、80、160 列及中文/emoji 长值，精确边界用内容显示列预算计算，不用 ANSI 字节长度。
- [x] 回归 GLOBAL 跳转及视口测试，证明提示变化未改变卡片常规四行高度、列表扣高或展开滚动结果。

```console
go test ./internal/tui/pages/proxies -run '^TestRoutingHeader_'
go test ./internal/tui/pages/proxies -run '^TestRouting_'
go test ./internal/tui/pages/proxies
```

完成证据：正常提示与状态说明的优先级分别得到测试；原始行宽和最终卡片宽度均受约束。

## 6. T04：快照、使用说明与视觉核对

依赖：T01–T03。

- [x] 先运行已有 golden，查看具体差异；随后只更新受影响的 Routing / Proxies 快照，不全量覆盖无关页面。
- [x] 核对 `internal/tui/testdata/routing_header_compact.golden`、`routing_header_full.golden`、`full/proxies.golden`；检查 picker 快照是否因背景透出而受到影响，仅接受可由本设计解释的差异。
- [x] 使用 `TestGoldenRoutingMode` 的已有 `-update` 开关定向更新；其他快照先查对应真实测试名再定向执行。
- [x] 同步 `README.md` 与 `README.zh-CN.md`：Locate Selected 新文案、焦点行内联提示和窄屏隐藏规则；保留原有定位/模式切换行为说明。
- [x] 用本地合成 fixture 生成带 ANSI 的渲染样例，核对未聚焦、Mode 焦点、GLOBAL 焦点、组头焦点、Locate Selected 焦点及窄屏；不连接实际 daemon 或真实代理数据。
- [x] 视觉核对明确确认：浅灰底黑字标签、绿底黑字值，提示独立深底浅灰字；普通去色 golden 不能替代此检查。

```console
go test ./internal/tui -run '^TestGoldenRoutingMode$'
go test ./internal/tui -run '^TestGoldenRoutingMode$' -update
go test ./internal/tui -run '^TestGoldenRoutingMode$'
```

完成证据：列出实际变更的快照、ANSI 视觉核对结果与中英文说明差异；未执行的检查明确标记。

## 7. T05：最终验证与交付

依赖：T01–T04。

- [x] 对修改过的 Go 文件执行 `gofmt`，核对改动范围后运行下列 TUI 检查。

```console
go test ./internal/tui/...
go test -race ./internal/tui/...
go vet ./internal/tui/...
git diff --check
```

- [x] 在当前 Windows 平台运行测试；对 Windows、Linux、macOS 执行无 CGO 编译检查。示例 PowerShell 命令如下；构建产物留在忽略目录，不提交。

```powershell
# 在独立 PowerShell 进程内执行，避免把 GOOS/GOARCH 留给后续测试。
$env:CGO_ENABLED = '0'
$env:GOOS = 'windows'
$env:GOARCH = 'amd64'
go build -o bin/mihari-routing-details-windows-amd64.exe ./cmd/mihari
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
$env:GOOS = 'linux'
$env:GOARCH = 'amd64'
go build -o bin/mihari-routing-details-linux-amd64 ./cmd/mihari
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
$env:GOOS = 'darwin'
$env:GOARCH = 'arm64'
go build -o bin/mihari-routing-details-darwin-arm64 ./cmd/mihari
exit $LASTEXITCODE
```

- [x] 若 race 因本机 C 编译器等环境限制无法运行，记录准确原因，不把无 CGO 编译当作 race 通过。
- [x] 本次正常范围没有跨包业务行为变化，不预设新增 `internal/integration` 用例。若实现超出上述展示边界，先复核范围并按影响增加相关集成/全仓测试。
- [x] 检查 `git diff` 和 `git status`：仅预期页面、测试、快照、README 和这两份文档；无 CHANGELOG、临时样例或构建产物。
- [x] 更新执行记录和设计验收项，给出实际命令、结果、限制与未完成事项。不将未经授权的提交、推送或合并列为已完成。

## 8. 执行记录

| 项目 | 当前状态 | 证据 |
| --- | --- | --- |
| 独立分支与 worktree | 已创建 | `fix/proxies-routing-details`，基线 `f365631` |
| 产品设计 | 用户已确认 | 文案、配色、分段反色、提示焦点、窄屏和状态规则已写入设计 |
| 设计与执行文档 | 已编写 | 本文及关联设计文档 |
| T01–T05 | 本地已完成 | 下述 Red/Green、样式预览、TUI 检查与六目标构建记录 |
| PR / CI / bot review | 已创建 PR 并跟进 | [PR #247](https://github.com/mihari-proxy/mihari/pull/247)；以最新提交的检查与 review 记录为准 |

后续执行时逐项填写：失败测试及原因、最小修复、通过的实际命令、视觉核对结果，以及因环境无法验证的项目。


### 2026-09-15 本地实施结果

- Red：新测试最初有一个缺失闭合括号，修正后重新执行；有效 Red 为旧 `[Locate]` 文案、右对齐/失焦仍出现的提示、长值被提示挤压、标签颜色 `[245 0]` 与目标 `[7 0]` 不符。编译失败未计作行为 Red。状态常驻用例在基线已通过。
- Green：`go test ./internal/tui/pages/proxies` 通过。新增测试集中在 `routing_details_test.go`，覆盖六种焦点组合、宽度恰好/少一列边界、中文/emoji 长值、状态常驻、逐字符 SGR 前景/背景及反色重置。
- `go test ./internal/tui/...`、`go test -race ./internal/tui/...`、`go vet ./internal/tui/...` 均通过。race 使用本机已有 gcc；未改依赖或环境安装。
- 定向执行 `go test ./internal/tui -run '^(TestGoldenRoutingMode|TestGoldenProxiesFull)$' -update`，仅更新 `routing_header_compact.golden`、`routing_header_full.golden`、`full/proxies.golden`；picker 快照无变化。
- 从合成 fixture 的实际 ANSI 输出生成本地预览并查看：未聚焦、Mode/GLOBAL 焦点、组头/定位入口焦点及 30 列窄屏符合设计。确认标签浅灰底黑字、值绿底黑字、提示独立深底浅灰字。临时预览测试已删除，图片位于系统临时目录，不提交。
- `CGO_ENABLED=0` 的 Windows、Linux、macOS × amd64、arm64 六目标构建均通过；产物在忽略的 `bin/`。
- `git diff --check` 仅报告 `full/proxies.golden` 两条变更行的既有固定宽度行尾空格；这是快照保留的渲染列，不能删空格改变基线含义。普通文件用排除 `.golden` 的检查，快照另以允许行末空格的检查验证。
- 中英文 README 已同步。CHANGELOG、全局主题、协议、业务行为及其他 worktree 未修改。
- CI 执行全仓三平台 unit/race、lint、vet-format、coverage、六目标构建与 Unix 安全检查；在 PR #247 持续核实最新提交结果。Pullfrog 已自动开始 review；CodeRabbit 自动 review 被标签配置跳过，已通过其评论入口请求单次 review。

- 补充本地检查：`golangci-lint run ./internal/tui/pages/proxies/...`（v2.12.2）通过，0 issues。

### 首轮 bot review 跟进

CodeRabbit 在 `7658365` 上未发现 actionable 问题；其预合并说明报告修改函数的注释覆盖率不足。已为新增测试、fixture 和 Routing 渲染函数补充说明意图的注释，不改行为；同时记录 PR 链接及本地 lint 结果。后续审查与 CI 继续以 PR 最新提交为准。

### 第二轮 bot review 跟进

CodeRabbit 在 `b121cb0` 上建议补充中文/emoji 值恰好容纳操作提示的边界。已将 `香港🌏` 作为明确占 6 显示列的 fixture，覆盖恰好容纳、少一列、多一列和常规宽度；完整后缀、无半截提示和值保留均有断言。目标测试、Proxies 全包与 golangci-lint 均通过，生产实现未改。
