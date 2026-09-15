# 订阅详情与添加表单：紧凑弹窗 A 执行计划

日期：2026-09-15
状态：T01–T06 实现与本地验证已完成；[PR #245](https://github.com/mihari-proxy/mihari/pull/245) 已创建，持续跟进 Actions 与 bot review。
分支：`design/subscription-detail`
工作目录：`.worktrees/design-subscription-detail`
基线：最初 `origin/dev` 的 `cb08ec2`（已包含 #241 订阅详情编辑）；开发完成后按用户指令 rebase 到 `d132db2`（#244）。
依据：本次对话已选择方案 A，并确认 grilling Q1–Q9；计划编写后，用户授权开始开发、提交 PR，每 10 分钟检查 Actions 和 bot review，修复直到全绿并汇报。
既有功能背景：[订阅详情编辑执行计划](2026-09-14-subscription-detail-edit.md)。该历史计划中的提交、推送授权不扩展到本任务。

## 1. 目标和已确认设计

将当前接近全高、状态与字段连续堆叠的订阅弹窗改为居中、随内容收紧的分组弹窗。添加和详情继续使用同一套表单实现。

| 决策 | 已确认行为 | 验收要点 |
| --- | --- | --- |
| A / Q6 | 保留居中和当前宽度上限，高度由内容决定 | 大终端不补空白撑满；小终端正文滚动，编辑态 Save 固定可见 |
| Q1 | 首行合并 In use、Status、Enabled | `● In use · Live · Enabled`；非使用中明确显示 `Not in use`；状态语义来自现有计算 |
| Q1 | Traffic、Cache、Last/Next update 放在首行下方 | 标签与值对齐；Cache 显示 `Available` / `Missing`，不显示原始布尔 |
| Q2 | 普通字段一行，URL 两行 | Name、Interval、Auto refresh、Mode 标签与值同行；URL 标题和输入框各一行 |
| Q3 | 循环字段反色，输入框不整行反色 | 循环项复用 RowFocus，保留焦点标记；文本使用白色光标和浅色输入底；光标所在单元格不视作整行反色 |
| Q4 | 无错误时隐藏错误行 | 有错误时状态区底部红色呈现，长文本换行且可滚动，不挤掉 Save |
| Q5 | 添加和详情共享表单 | 添加只有 Name、URL、Mode；详情增加状态区、Interval、Auto refresh |
| Q7 | 快捷键只放根层 footer | 输入框提示 Enter next，循环项提示左右/Space，Save 提示 Enter save；不在弹窗再画一份 |
| Q8 | Last update 使用本地时间、分钟精度 | `2026-09-15 03:59`；零值保持 `—`；Next 继续使用既有相对时间 |
| Q9 | 空白 Interval 表示继承全局 | 占位显示实际 `Global · <interval>`；聚焦显示 `Leave blank to use global interval`；不向 Value 注入占位文本 |

统一沿用现有等宽字符、灰色圆角细边框、紫色标题、语义状态色。颜色仍按终端主题表现，不引入新的整站视觉主题。表单自有文案使用英文。

### 1.1 结构示意

```text
╭── Subscription · kanata ──────────────────────╮
│ ● In use · Live · Enabled                     │
│ Traffic       52.5 / 80.0 GiB                 │
│ Cache         Available                      │
│ Last update   2026-09-15 03:59                │
│ Next update   in 5h                          │
│                                              │
│ ─── Settings ──────────────────────────────── │
│   Name          kanata                       │
│   URL                                        │
│   https://example.test/subscription           │
│   Interval      Global · 12h                 │
│ › Auto refresh  ‹ On ›                        │
│   Mode          ‹ PROXY ›                     │
│                                              │
│                  [ Save ]                    │
╰──────────────────────────────────────────────╯
             根层 footer：随焦点变化
```

示意不规定精确列数。长名称、状态首行和错误必须按可见字符宽度处理；窄屏优先换行保持语义，不截掉 In use 或 Status。URL 由原输入组件横向滚动，完整值留在草稿内。

## 2. 范围与约束

- 只修改 TUI 表单布局、相关帮助/文案及对应测试与使用说明。
- 沿用现有 `formModel`、`newAddForm`、`newEditForm`、校验、请求构造和 `savePhase`；不另写一套添加表单，不引入通用表单框架。
- Enter 仅在 Save 焦点时提交；文本框 Enter 下一项，循环字段只改草稿；编辑态 Esc 丢草稿。
- 保存中、保存结果未知、等待核对、冲突确认遵循现有规则。Esc 取消草稿不能被泛化为取消已发出的保存。
- 使用中与缓存状态独立：`In use + Outdated` 必须同时可见；不改状态优先级、自动刷新调度、缓存替换或模式语义。
- TUI 继续只通过控制客户端访问 daemon；不改控制协议、CLI/JSON、持久化格式、后台任务和平台支持范围。
- 不新增依赖、不调整 Go/toolchain、不改 `CHANGELOG.md`。不连接真实订阅、真实 mihomo，不操作系统服务。
- 所有演示、截图和 fixture 只用合成数据与 `example.test` 地址，不复制用户截图中的订阅地址。
- 在已建立的独立 worktree 中执行；保留主工作目录的未提交文件。后续授权包含 commit、push、指向 dev 的 PR 与 CI/review 修复，不包含合并。

## 3. 已核实的代码落点

该 worktree 没有 `.codegraph/`，本次直接读取其文件，不建立索引，也不使用主工作目录旧版本的索引代替当前源码。

| 文件 / 符号 | 现状 | 计划修改 |
| --- | --- | --- |
| `internal/tui/pages/subscriptions/form.go`：`formModel.View` | 所有字段固定两行，Save 追加在正文末尾 | 共用紧凑字段渲染；正文与 Save 行分开；输出各字段实际行范围 |
| 同文件：`newForm` / `move` / `reveal` | 已共用 textinput，已有焦点、粘贴和 URL 草稿保护 | 保留输入机制；配置所需焦点外观与有效输入宽度 |
| `internal/tui/pages/subscriptions/dialog.go`：`formStatus` / `wrappedFormStatus` | 八行文本包含布尔和 RFC3339 时间，Last error 总显示 | 语义首行、对齐元数据、条件错误和可见宽度换行 |
| 同文件：`formBodyHeight` / `formView` | `m.height-8` 后用 Height 补齐，导致过高；内部重复 footer | 统一高度预算，正文按需增长，编辑态固定 Save，移除内部快捷键 |
| 同文件：`ensureFormFocus` | 按 `index*2` 推算字段行 | 使用渲染产生的实际行范围，覆盖 URL、提示、错误和折行 |
| 同文件：`formHelpMode` / `formFooter` | 区分循环项与保存状态，普通输入和 Save 共用 ModeForm | 订阅页细分输入/Save 提示，保留所有保存态提示 |
| `internal/tui/pages/subscriptions/model.go`：`SetSize` / `updateForm` / `FooterHints` | 缩放和移动会定位焦点，PgUp/PgDn 手动滚动；Enter 处理提交 | 与新布局共用计算，保留键盘和 mutation 语义 |
| 同文件：`formatTimestamp` | 本包当前只由详情状态使用，输出本地 RFC3339 | 改为本地分钟格式；不修改协议时间 |
| `internal/tui/ui/keymap.go` | 帮助和 footer 共用 catalog | 添加必要的订阅页专用上下文，不修改其他页面通用 ModeForm 行为 |
| `internal/tui/model.go`：根层 View/footer | 已消费页面 FooterHints | 原则上仅加根层回归测试；确有必要才做最小适配 |
| `internal/tui/ui/theme.go` | 已有 RowFocus、Title、Dialog、Muted 与语义色 | 优先直接复用；文本输入外观局限在订阅表单，不全局改色 |

### 3.1 实现需要闭合的细节

1. **单一布局结果**：用包内小结构同时承载正文行、字段起止行、正文窗口高度和 Save 行。名称可在实现时调整；不要让绘制和焦点分别维护行高公式。
2. **计算顺序**：先按实际内容区宽度设置输入宽度，再构建字段/状态行和映射，最后计算窗口与滚动。缩放、URL 异步回填和状态刷新不得使用旧尺寸定位。
3. **固定区域**：编辑态标题和 Save 为固定区域；状态、字段、错误及辅助文案组成可滚动正文。可用高度扣除边框、padding、分隔和固定区域后，正文高度取内容高度与预算的较小值。
4. **Save 焦点**：Save 仍参与原 Tab/Shift+Tab/↑/↓ 循环，只是绘制在固定行。滚动正文不能制造第二个 Save，也不能改变提交条件。Save 聚焦复用既有按钮焦点惯例。
5. **手动滚动**：PgUp/PgDn 可查看状态和长错误；渲染期间不能每帧强行滚回字段。字段焦点移动或尺寸变化才保证焦点可见；保留过量 PgDown 后 PgUp 立即生效的既有回归。
6. **错误出口**：订阅 LastError 属于状态区；`form.errorText` 的校验/保存拒绝属于表单反馈。后者现在借内部 footer 显示，移除快捷键时必须迁到正文中的明确错误位置，触发时确保可见。不能删除错误，也不能覆盖根层快捷键。
7. **辅助提示高度**：Interval 焦点提示在字段后呈现，参与真实行范围计算；没有聚焦时不预留一大片空白。
8. **保存状态隔离**：发送、等待、冲突、未知结果的正文不强行套用编辑态 Save 固定行；继续使用当前操作和确认按钮，并按内容紧凑布局。
9. **全局间隔**：占位取订阅列表的 `GlobalInterval`，缺失时复用现有有效间隔规则；全局值变化只影响占位，不改草稿或 revision。显式间隔和清空为继承仍按原 PATCH 比较。

## 4. 执行顺序（Red–Green–Refactor）

每项先运行相关现有测试取得基线，再加入表达目标行为的测试并确认正确失败，随后做最小实现。测试名称为建议名称，开始执行后记录实际名称、命令和结果。

### T01 — 状态摘要与时间展示

- [x] 在订阅包新增 `dialog_layout_test.go`，覆盖：状态首行合并、Not in use、In use + Outdated、Cache 可读文案、空错误隐藏、非空错误保留。
- [x] 增加固定时区和零时间用例，验证本地 `2006-01-02 15:04`；测试不依赖开发机当前时区。
- [x] 运行新测试确认因旧布局失败，再修改 `formStatus` / 状态换行和 `formatTimestamp`。
- [x] 继续调用原状态解析和 `nextRefreshLabel`，不另写状态机。

验收：宽屏首行三种信息完整；窄屏换行后信息完整；非空错误不截掉且能阅读。

### T02 — 共享紧凑字段与焦点样式

依赖：T01。

- [x] 在 `form_test.go` 增加 add/edit 表驱动用例：普通字段单行、URL 两行、添加三字段、详情五字段。
- [x] 覆盖长 URL 横向编辑、粘贴、中文/宽字符名称；只裁剪显示，不裁剪 Value、URL baseline 或请求。
- [x] 覆盖循环项反色、文本行不反色及白色光标/浅色输入底；在确定的颜色配置下验证，不能只 strip ANSI 后声称样式通过。
- [x] 增加 Interval 空值实际全局占位、显式值、清空继承、全局值刷新不改草稿测试；占位和帮助绝不进入 PATCH。
- [x] 实现包内字段布局与行范围，共用 add/edit 渲染；为 T03 提供正文和独立 Save 行。
- [x] 保留 `move`、`valid`、请求构造与 reveal 保护，复跑现有表单测试。

涉及 textinput/Lip Gloss 的样式 API 时，执行前通过项目要求的 ctx7 `library` → `docs` 查证当前依赖对应接口，每个问题最多三条命令。这里只规定目标，不凭记忆写未核实的 API 调用；不为样式升级依赖。

### T03 — 内容驱动高度、固定 Save 与滚动

依赖：T01、T02。

- [x] 加入 `TestDetailLayout_HeightFollowsContent`：同样内容在两个足够高的窗口中边框高度相同，且不被撑满；测实际弹窗边框，不用已 Place 到全屏的总行数代替。
- [x] 加入编辑态每个焦点、PgUp/PgDn 后 Save 始终可见且只出现一次的测试。
- [x] 扩展现有 `TestDetailLayout_FocusedFieldVisible`、`StatusCanScrollIntoView`、`PageUpAfterExcessPageDown`；更新旧文案断言但保留原行为保证。
- [x] 覆盖长错误、校验错误、状态首行折行、Interval 提示、打开后缩放和异步回填后的焦点可见性。
- [x] 用同一布局结果替换 `index*2` 和强制补满逻辑；按新的固定区域预算夹紧滚动范围。
- [x] 对错误反馈安排可见位置；验证触发校验失败时看到原因，Save 和根层提示仍可见。

验收：页面级 68×19 及根层 72×22 均能完成全部字段导航；内容无边界溢出；大屏明显收紧。

### T04 — 单一 footer 与帮助一致性

依赖：T02、T03。

- [x] 在 `ui/subscription_help_test.go` / `ui/keymap_test.go` 增加订阅输入、循环、Save 上下文断言；保留其他页面通用 ModeForm 测试。
- [x] 在根层 `subscription_detail_test.go` 加入快捷键只出现一次的断言，覆盖 add/edit 和三类焦点。
- [x] 修改 `formHelpMode` / catalog / `formFooter`：输入 Enter next、循环 Enter next 和左右切换、Save Enter save。
- [x] 删除 `formView` 内的快捷键渲染；只由现有 `FooterHints` 交给根层输出。
- [x] 焦点提示、滚动帮助与实际按键一致；短 footer 优先保留当前动作与 Esc，不为省字符显示错误操作。
- [x] 保存中不出现 Esc cancel；未知结果与等待继续明确关闭不等于取消保存。

### T05 — 根层与保存状态回归、视觉验收

依赖：T01–T04。

- [x] 扩展根层 add/edit 尺寸矩阵：72×22、100×30、160×42，逐个遍历文本、循环和 Save 焦点，并测试宽→窄→宽。
- [x] 保留 `TestSubscriptionDetail_TextKeysStayInForm`；字母、数字、Space 等不得触发页面跳转、刷新、使用或退出。
- [x] 复跑 `save_state_test.go` 中 reveal 迟到、连接/弹窗身份、空 PATCH、不确定结果核对、冲突再次确认、添加首拉失败不重复创建等现有测试。
- [x] 为受布局影响的 saving/conflict/unknown/waiting 状态补充小窗口渲染断言：消息与可用操作可见，无编辑态 Save 误呈现。
- [x] 使用合成数据实际渲染或离线 fixture 进行视觉检查：正常详情、添加、长 URL、长错误、小窗口、输入焦点、循环焦点、Save 焦点。按仓库既有 golden 方式记录必要布局，不接真实 daemon。
- [x] 验证焦点可辨识、输入底与白色光标有对比、分区留白适度、边框完整、没有无意义的远端省略号、没有重复 footer。

仅凭去掉 ANSI 的字符串不能证明颜色和光标外观；视觉结果需单独记录。若自动测试不能覆盖真实终端颜色呈现，应明确该限制，不冒称已完成真实环境验证。

### T06 — 文档、整体检查与交付

依赖：T05。

- [x] 更新 README 的相关 TUI 说明及对应中文说明，说明共享紧凑表单、Save 和小窗口操作；只更新本次用户可见变化。
- [x] 按实际帮助调整必要文档；历史设计若保留原描述，添加指向本计划的后续调整说明，不抹去历史行为。
- [x] 执行 §5 验证，记录已运行结果与未验证项。
- [x] 检查变更范围不含业务层、协议、CHANGELOG、临时产物和主 worktree 既有修改。
- [ ] 提交并创建指向 dev 的 PR；按后续授权每 10 分钟检查 Actions 与 bot review，修复相关问题直到全绿，交付结果。合并仍需另行确认。

## 5. 验证命令与验收矩阵

从最小测试逐步扩大，不因本计划列出命令就声称已经执行。

```powershell
# 每个 TDD 小步：先用实际新增测试名确认 Red，再确认 Green。
go test ./internal/tui/pages/subscriptions -run '^TestDetailLayout_HeightFollowsContent$' -count=1
go test ./internal/tui/pages/subscriptions
go test ./internal/tui/ui ./internal/tui
go test ./internal/tui/...
go test ./internal/integration
go test ./...
go test -race ./internal/tui/...
go vet ./...
gofmt -l internal/tui
git diff --check
```

上述命令在实施中按先小后大执行，重复运行只用于新的变更或失败。gofmt 只写入本任务修改过的 Go 文件；若出现基线 CRLF 或无关格式问题，记录而不顺手修改。Windows race 若缺少所需本机工具链，报告原因并交由相应 CI 验证，不自动安装或声称通过。

本次不改变平台代码或支持范围；完成当前平台测试后做 CGO=0 三平台 smoke build，产物写临时目录并恢复进程环境变量，避免影响后续测试：

```powershell
$priorCGO = $env:CGO_ENABLED
$priorGOOS = $env:GOOS
$priorGOARCH = $env:GOARCH
$compactBuildDir = Join-Path ([IO.Path]::GetTempPath()) ('mihari-compact-' + [guid]::NewGuid())
New-Item -ItemType Directory -Path $compactBuildDir | Out-Null
try {
    $env:CGO_ENABLED = '0'
    foreach ($target in @(
        @{ OS = 'windows'; Arch = 'amd64'; File = 'mihari-windows-amd64.exe' },
        @{ OS = 'linux'; Arch = 'amd64'; File = 'mihari-linux-amd64' },
        @{ OS = 'darwin'; Arch = 'arm64'; File = 'mihari-darwin-arm64' }
    )) {
        $env:GOOS = $target.OS
        $env:GOARCH = $target.Arch
        go build -o (Join-Path $compactBuildDir $target.File) ./cmd/mihari
        if ($LASTEXITCODE -ne 0) { throw ('Build failed: ' + $target.File) }
    }
} finally {
    $env:CGO_ENABLED = $priorCGO
    $env:GOOS = $priorGOOS
    $env:GOARCH = $priorGOARCH
}
```

| 维度 | 必须覆盖 |
| --- | --- |
| 表单 | 添加、详情；各文本字段、各循环字段、Save |
| 状态 | 使用中/非使用中、Live/Outdated、无缓存、禁用、刷新错误 |
| 输入 | 长 URL、中文名称、空/显式/清空 Interval、全局间隔变化、粘贴与 URL 迟到回填 |
| 布局 | 72×22 根窗口、100×30、160×42；正文滚动；缩放；错误出现和消失 |
| 保存 | 校验失败、未改动关闭、成功、明确拒绝、发送锁定、未知、等待、冲突确认 |
| 提示 | 单一根层 footer、焦点上下文正确、帮助与按键一致 |

## 6. 完成标准与当前记录

执行完成必须同时满足：Q1–Q9 均有实现或验收证据；相关行为测试通过；合成数据视觉检查可审阅；共享保存状态和请求语义未退化；修改范围受控；未运行项目及原因明确列出。

### 6.1 已执行记录

- Red：状态合并与分钟时间测试先因旧展示失败；共享字段、内容高度、固定 Save、全局占位测试先因旧布局失败。根层单 footer、校验错误可见、状态刷新焦点保持、非聚焦长 URL 展示地址开头均先确认目标行为缺失，再修复。
- Green：`go test ./...`（包含 integration）、`go test -race ./internal/tui/...`、`go vet ./...`、`golangci-lint run ./...`（0 issues）通过。
- 修改 Go 文件经 gofmt；`git diff --check` 无错误。
- CGO=0 的 Windows/Linux/macOS × amd64/arm64 六目标构建通过，产物写入临时目录。
- 六份确定性 golden：detail、cycle、add、compact-url、compact-save、error；生成后使用默认只读比较模式验证通过。
- 对 Go fixture 的 ANSI 输出做离线图片渲染，检查输入底、白色光标、循环反色和窄窗口。渲染脚本及图片留在临时目录，生成用的临时测试文件已移除。该检查不代表真实终端、真实 daemon 或真实订阅验证。
- 视觉检查后，循环焦点统一为整行反色；非聚焦长 URL 从地址开头显示，聚焦时继续使用原组件横向滚动，完整草稿和光标位置保留。
- README 中英文及历史设计指针已同步。未运行本地全仓 race；完整跨平台 race 由 PR CI 验证。

### 6.2 PR 跟进

- PR：https://github.com/mihari-proxy/mihari/pull/245，目标 `dev`。
- 2026-09-15：按后续指令 rebase 到最新 `origin/dev` @ `d132db2`，无冲突；rebase 后 `go test ./internal/tui/...` 通过。更新 PR 分支后继续检查新 head 的 CI 和 bot review。

创建 PR 后记录链接；每次检查同时核对当前 head commit 的 Actions、reviews、review comments 与未解决线程。新反馈先确认适用性，补回归测试并修复，不通过取消检查、降低门禁或忽略有效反馈获得绿色。最终汇报 PR 链接、通过结果和剩余限制，不自动合并。
