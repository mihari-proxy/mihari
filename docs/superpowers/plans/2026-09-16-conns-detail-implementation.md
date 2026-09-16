# Conns 单页连接详情实施计划

日期：2026-09-16

状态：实现及本地验证已完成，已创建 [PR #263](https://github.com/mihari-proxy/mihari/pull/263)，正在处理审查反馈及等待 CI。用户已授权开发、提交 PR，并每 10 分钟检查 CI 与 bot review 至可用检查全绿；不合并。下文为执行步骤，实际验证结果另行记录。

设计依据：[Conns 连接详情设计](../specs/2026-09-16-conns-detail-design.md)。用户后续决定优先：取消 Raw、Proxies 页面，取消整个标签栏，完整代理链保留在详情正文中。

## 1. 工作位置与范围

- Worktree：`C:/Users/Kinema/Documents/modular_dev/mihari/.worktrees/feat-conns-detail-design`。
- 分支：`feat/conns-detail-design`，创建时基线为 `origin/dev` 的 `03d33a1`；后续 PR 目标为 `dev`。
- 本次范围：单个连接详情的展示、浏览按键、帮助、回归测试和中英文使用说明。
- 继续使用现有连接 DTO、历史记录、GeoIP 查询和主题，无新增依赖、协议/持久化变更、daemon mutation 或系统服务操作。
- 保留原工作区 `.gitignore`、本 worktree 中已有 `CONTEXT.md` 修改和设计草稿；不把它们误认为本轮实现产物。功能变更不修改 CHANGELOG。
- 用户现已授权 commit、push 和以 dev 为目标创建 PR；检查/修复 CI 与 bot review，10 分钟轮询一次，不合并。

## 2. 用于实施的具体布局默认值

以下是对设计草稿待决定项的实施建议，不标记为用户逐项确认；后续视觉调整只改变布局，不改变连接数据含义。

### 2.1 面板与信息分组

保留详情替换 Conns 内容区的现有方式，外层 shell、导航栏和全局 footer 保持原位。内容区内使用一个居中圆角面板，外宽上限 88 个终端单元，随可用宽度缩小。宽度预算必须包含边框与 padding，不能给窄窗口强制 36 列最小宽度。

面板固定标题 `Connection details` 及 `Active` / `Closed` 状态；暂停列表时额外显示 `Paused`，明确这些是冻结的观测值。目标摘要与字段放在可滚动正文，避免长域名占满固定区。底部快捷键仅由 shell 输出一次。

| 区域 | 展示内容 | 排版 |
| --- | --- | --- |
| 目标摘要 | 域名与目标端口；域名缺失时回退到目标 IP，再缺失显示 `—`；进程、入站类型、Network | 域名强调，次要信息较淡；摘要可换行 |
| Traffic | 下载/上传速率、累计 Received/Sent | 内部可用宽度至少 64 列时两列，否则堆叠；复用现有 IEC 格式化 |
| Endpoints | Source、Destination、Host、Sniff host、Remote | 标签列对齐，值列换行；IPv6 与端口使用无歧义的方括号格式 |
| Routing | Rule 与 Rule payload、完整 Chain、GeoIP | 完整代理链按原数组顺序用 ` → ` 串联；不标注为已证实的数据包经过顺序 |
| Metadata | Process、Process path、Inbound name、Inbound user、Started、Closed observed、Connection ID | 日期使用本地时间到秒；Closed observed 仅对存在结束观测时间的记录显示 |

常用字段缺失显示 `—`；可选的 Sniff host、Remote、Process path、Inbound user 缺失时省略该行。ID、端点、规则、链、已有可选字段在单页内均可通过滚动完整阅读，不能因移除 Raw 而失去唯一的字段查看入口。

保留现有主题颜色，标题使用 accent、Active 使用 success、Closed 使用 muted；字段标签和边框使用 muted，值使用默认前景色。状态必须带文字，不仅依靠颜色。正文分组用留白和小标题，不嵌套卡片。

### 2.2 数据状态

- Active 使用当前连接快照的速率；列表暂停时保留数值并标明 Paused，不重算、不伪造实时值。
- Closed 将速率标为 `Last download rate` / `Last upload rate`，保留最后观测值和累计量，不修改 History 中的数据，也不假定速率已经清零。
- `Closed observed` 是 TUI 发现连接消失的时间，不称为内核精确关闭时间。零值时间显示缺失或按上述条件省略，不计算虚假的持续时间。
- GeoIP 加载显示 `Loading…`，不可用显示 `Unavailable`；不渲染底层错误原因。每条结果保留对应 IP，避免目标地址与 Remote 地址的两条结果混淆。
- 复用现有公开地址筛选和异步查询；新详情不接收其他 connection ID 的迟到结果。

### 2.3 滚动与窄窗口

- 保留 ↑/↓ 逐行滚动、Enter/Esc 返回；左右键不切页，也不重置滚动。默认不增加其他浏览快捷键。
- 先按终端显示单元宽度换行，再计算滚动范围；长路径、无空格域名、中文、emoji、IPv6 和含样式的文本均不能按字节切断。
- 多行字段的后续行与值列对齐。极窄窗口放不下标签列时改为标签和值分行。
- 正文溢出时预留一行位置提示，例如 `3–16 / 29`；位置提示行不改变可滚动正文的行数，避免越滚越多或尾行不可达。
- 刷新、GeoIP 到达和 resize 后，将保存的 scroll 限制到新的有效范围；滚动到底后按一次 ↑ 必须立即上移，不能积累隐藏的超额偏移。
- 高度不足以显示标题和至少一行正文时，显示有界的 `Resize terminal · Enter/Esc close` 提示；返回按键始终可用。
- 返回列表后保留选中连接、筛选、排序及暂停状态，不触发关闭连接或其他 mutation。

## 3. 代码落点与已知关联

| 文件 | 计划改动 |
| --- | --- |
| `internal/tui/pages/connections/detail.go` | 删除 tab 与 JSON 页面分支，实现单页内容、对齐换行、尺寸预算、滚动限制、状态语义 |
| `internal/tui/pages/connections/model.go` | 仅在需要时传入尺寸和暂停展示状态，保持既有刷新/返回/GeoIP 流程 |
| `internal/tui/pages/connections/render.go` | 对接新详情渲染参数，不重构连接列表 |
| `internal/tui/pages/connections/detail_test.go`（新） | 单页内容、布局边界、滚动、缺失值、Closed/Paused/GeoIP 测试 |
| `internal/tui/pages/connections/model_test.go` | 打开/返回、刷新、异步结果、帮助与现有断言调整 |
| `internal/tui/ui/keymap.go`、`keymap_test.go` | 删除 Connections/detail 的 switch tabs，显示该页的 scroll 提示，验证其他页面不受影响 |
| `internal/tui/ui/strings.go` | 需要时集中新增英文标签；不要删除 Logs 仍使用的 `RawTabLabel` |
| `internal/tui/pages/logs/model_test.go` | 如共享 footer 别名影响该断言，改为比较 Logs 自己的 RenderFooter；不改变 Logs 行为 |
| `internal/tui/golden_test.go`、`internal/tui/testdata/full/connections-detail*.golden` | 修正详情进入步骤，更新真正的详情快照并增加必要尺寸/状态样例 |
| `README.md`、`README.zh-CN.md` | 同步单页详情、长字段浏览、快捷键及关闭/暂停数据语义 |

当前 `FooterDetailMode` 是按 Connections 生成的别名，但 Logs 的测试也拿它作为预期；不能简单改公共字符串而让 Logs 继承 Connections 的滚动提示。

当前 `TestGoldenConnectionsDetailFull` 只发送一次 Down 再 Enter；基线 golden 实际展示列表搜索状态。必须先修正焦点路径并断言已进入详情，再更新快照，不能直接批准原有画面。

## 4. 执行顺序：每个行为先 Red，再 Green

### T1：移除标签并同步帮助

1. 新增 `TestDetail_SinglePage`：详情中不存在 Overview/Raw/Proxies 标签及 JSON 字段展示；右键、左键前后内容一致且滚动位置不重置。
2. 新增/调整 keymap 测试：Connections 详情帮助不再含 switch tabs，footer 有滚动及返回提示；Logs、Rules 帮助保持各自行为。
3. 运行下面的最小范围，确认因旧行为失败，不接受编译失败作为 Red：

```powershell
go test ./internal/tui/pages/connections -run '^TestDetail_SinglePage$'
go test ./internal/tui/ui -run 'Connections.*Detail|Detail.*Help|Footer'
```

4. 删除 `tab`、`encoding/json` 与 Raw/Proxies 渲染分支，删除左右键切页逻辑，更新 Connections 专属 keymap。
5. 运行两个目标包，检查 Logs 的共享 footer 断言；只调整正确的页面预期。

完成标准：单页成立、键盘文案与行为一致，其他页面没有被动增加或丢失按键提示。

### T2：实现信息布局与状态呈现

1. 先增加 `TestDetail_FieldsAndStates` 表驱动用例：字段齐全、缺失可选字段、Active、Closed、Paused、GeoIP loading/unavailable/两个地址。
2. 通过已有 DTO 构造合成数据；断言完整字段、原链顺序、累计量、last rate 标签和地址归属。Closed 不改变底层 Connection；GeoIP 错误字符串不能出现在渲染中。
3. 最小测试失败后，按第 2 节重写正文渲染；优先复用 `ui.FormatBytes`、`ui.FormatRate`、主题和现有终端文本工具。只有详情使用的字段布局辅助函数留在 connections 包。
4. 若要调用此前未核实的外部库 API，按仓库 Context7 规则先取得当前文档，不新增依赖。
5. 改写原本依赖 `Basic` 文案或固定 `scroll = 6` 的旧断言；保留“完整代理链可见”和“GeoIP 失败时仍可看详情”的实际行为证明。

```powershell
go test ./internal/tui/pages/connections -run 'TestDetail_FieldsAndStates|TestModel_ControlRowAndDetailsPreserveFullChain|GeoIP'
go test ./internal/tui/pages/connections
```

完成标准：所有已有连接字段有合适的查看位置，状态语义准确，缺失信息不产生混乱的 `—:—` 或多余空行。

### T3：换行、滚动和尺寸变化

1. 先增加 `TestDetail_LayoutBounds`、`TestDetail_ScrollAndResize`，用终端单元宽度及渲染行数断言边界，覆盖长中文节点、emoji、无空格域名、进程路径和 IPv6。
2. 内容区尺寸至少覆盖 `100×32`、`80×24`、`60×16`、`36×12`、`20×6` 和零尺寸；这些是页内容尺寸，不是包含 rail/footer 的整个终端尺寸。
3. 从第一行逐行滚到末尾，确认每个字段可达；反复 Down 后一次 Up 即向上移动；大窗口变小再变大、长内容变短后无空白页和溢出。
4. 最小失败确认后，实现统一的布局计算，供 View 与按键处理共享真实的 wrap 后行数和可见行数。尺寸/数据变化时同步 clamp。
5. 如需传递尺寸，由 `Model.SetSize` / 渲染入口接入；不保存 context、不新开后台任务。

```powershell
go test ./internal/tui/pages/connections -run 'TestDetail_LayoutBounds|TestDetail_ScrollAndResize'
go test ./internal/tui/pages/connections
```

完成标准：支持的尺寸下无越界、无丢字和不可达的尾行；极小尺寸降级提示可关闭。

### T4：连接生命周期与真实画面回归

1. 新增模型层回归：Enter 打开选中项、刷新时保持合理滚动位置、Active 转 Closed、Pause 冻结/恢复，以及 Enter/Esc 返回同一连接；不调用关闭连接方法。
2. 覆盖 GeoIP 迟到消息：先打开 A 再打开 B，A 的结果不得污染 B。保留已有只查询公网地址测试。
3. 修正 `TestGoldenConnectionsDetailFull` 的按键导航，增加生成快照前的 `Connection details` 和目标字段断言，防止再次把列表当成详情。
4. 使用合成 fixture 添加紧凑尺寸/Closed 的 golden；长字段底部画面可用滚动后快照验收。固定时间及时区，不连接真实 daemon 或 mihomo。
5. 仅更新本任务快照并检查实际文本对齐；检查 ANSI 着色样例的标题、状态与标签对比度，不只依靠去色 golden。

```powershell
go test ./internal/tui -run '^TestGoldenConnectionsDetail' -update
go test ./internal/tui -run '^TestGoldenConnectionsDetail'
go test ./internal/tui/...
```

完成标准：快照确实是详情、宽窄布局可读、更新/返回行为正确；无公网依赖。

### T5：文档与最终验证

1. 更新中英文 README 的 Connections 段落；同步设计草稿中的已落实布局和交互，保留历史行为与新行为的区分。
2. 对修改的 Go 文件执行 gofmt，运行以下检查并逐项记录实际结果。

```powershell
go test ./internal/tui/...
go test -race ./internal/tui/...
go vet ./internal/tui/...
git diff --check
```

前一步已通过且未再改代码的测试不重复运行。若 Windows 的 race 工具链不可用，记录具体错误和未验证项，不能用普通测试替代后声称 race 已通过。

3. 在临时会话环境中设 `CGO_ENABLED=0`，对 windows/amd64、linux/amd64、darwin/arm64 执行 `go build -o <任务产物目录>/<目标文件> ./cmd/mihari`。构建产物放在忽略目录或仓库外，结束后恢复环境变量；编译通过不代表其他平台真实终端已验收。
4. 本次仅改 TUI 表现层，默认验证范围为所有 TUI 包；若实施中实际涉及共享协议、runtime 或 daemon 边界，先检查任务范围，并按受影响路径扩大到集成测试及全仓测试。
5. 核对准确 diff：无 CHANGELOG、依赖、协议、真实用户数据或系统服务修改；用户既有改动完整保留。

交付证据：修改文件清单、实际执行命令及结果、宽/窄/Closed 渲染预览、明确列出的未验证项。完成本地实现和验证后再按后续授权处理提交与 PR。

## 5. 验收清单

- [x] Enter 打开居中单页详情，没有 Overview/Raw/Proxies 标签或切页提示。
- [x] 完整代理链只在正文展示，顺序与连接数组一致；长字段可完整浏览。
- [x] 速率/累计量/端点/规则/元数据对齐，缺失值与多地址 GeoIP 明确。
- [x] Closed、Paused 与结束观测时间没有被误标为实时或精确关闭数据。
- [x] 小窗口、Unicode、连续滚动、resize 和异步刷新均不越界、不丢尾行。
- [x] Enter/Esc 返回原连接，保留列表状态，无新增 mutation。
- [x] Connections、Logs、Rules 帮助各自正确；详情 golden 真正进入详情。
- [x] 中英文文档与实现一致；所有验证结果有实际执行证据。

## 6. 本地实施结果（2026-09-16）

- T1–T5 完成。正文实现拆到 `detail_content.go`，视口/按键保留在 `detail.go`。
- Red：旧版标签、左右切换、缺失字段、Closed 标签、越界尺寸及滚动积累均由新增测试复现。
- Red：修正 golden 前增加详情状态断言，确认原测试实际停在列表；完整 shell 测试进一步复现外层 padding 导致的折行，已修正详情可用宽度。
- 已通过 `go test ./internal/tui/...`（包含普通、紧凑、Closed、Paused 和滚动到底部的详情 golden）。
- 已通过 `go test -race ./internal/tui/...`、`go vet ./internal/tui/...`。
- 已通过 `golangci-lint run ./internal/tui/...`（v2.12.2，0 issues）。
- 已通过 CGO_ENABLED=0 的 windows/amd64、linux/amd64、darwin/arm64 构建；产物放在系统临时目录，没有进入提交。
- 修改 Go 文件已 gofmt；`git diff --check` 已通过。
- 已检查完整 shell 的文字快照，字段列、边框、状态和窄屏换行正常。尚未执行真实 daemon/mihomo 或 Linux/macOS 终端会话验证；这些不属于本次本地测试。
- 全仓及其他目标平台的运行测试由 PR CI 验证；CI/bot review 状态以 PR 当前 head 为准。

## 7. PR 审查记录

- 2026-09-16 15:03（北京时间）首次 10 分钟检查：无失败 CI，Windows/macOS race 和 Pullfrog 尚在运行。
- CodeRabbit 提出的两项测试职责拆分已处理：布局边界与字符保真分开，Connections 帮助/footer 与 Logs 隔离检查分开；补充变更函数说明。
- 以上测试整理后重新通过 `go test ./internal/tui/...`、`golangci-lint run ./internal/tui/...` 和 `git diff --check`，未改变生产行为。
- Cubic 因本月额度已用尽返回 neutral，没有执行审查；不能记为审查通过。CodeRabbit 此轮提示 clone-backed analysis 不可用，需在最终汇报注明其覆盖限制。

- 15:13 检查：初版提交 `08e2f8f` 的全部 CI、CodeRabbit 与 Pullfrog 检查成功。Pullfrog 无阻塞意见，仅建议清理已移除标签对应的两个常量；确认全仓无引用后删除，Logs 所用 RawTabLabel 保留。
- 将测试整理、注释和常量清理一起推送后，需对最终提交重新确认远端状态。
