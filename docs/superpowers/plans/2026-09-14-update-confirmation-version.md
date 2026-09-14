# TUI 更新确认：执行方案

日期：2026-09-14。状态：任务 1–5 已实施，本地验证完成，PR 评审待跟进。

设计依据：[已安装未知版本展示与弹窗布局](../specs/2026-09-14-update-confirmation-version-design.md)。基线：`dev` 的 `6221d69e0e599d77cf2fdfde31a93d2f23886922`。分支：`fix/update-confirmation-version`。

当前 worktree：`.worktrees/fix-update-confirmation-version`。原工作区 `.gitignore` 的用户修改不得带入或覆盖。

## 1. 实施边界和依赖顺序

```text
任务 1：保留安全的已安装版本显示证据
    ↓
任务 2：贯通目标预览，锁定兼容性边界
    ↓
任务 3：生成 TUI 更新确认的结构化内容
    ↓
任务 4：专用分段弹窗与滚动交互
    ↓
任务 5：跨包回归、布局验收、文档收口
```

仅 TUI 更新确认消费新展示值。CLI、JSON、安装脚本、其它 TUI 页面、候选版本验证不扩展。所有新增行为遵循 Red–Green–Refactor。

新内部字段/类型如果是编译所必需，可以先添加零行为声明，使测试可编译；Red 必须由目标行为断言失败证明，不能用缺失类型或函数导致的编译失败代替。每个任务记录实际 Red/Green 命令及结果。

实施前重新检查分支、工作区和远端 dev。如需同步基线，先处理具体冲突与影响，不覆盖用户文件。不要在 main/dev 创建 commit。

## 2. 任务 1：保留受限的非标准版本标识

**目标**：同一次可信已安装程序探测，同时产生既有 canonical 版本和可选的安全非标准显示标识。

涉及文件：

- `internal/update/version_probe.go`
- `internal/update/version_probe_test.go`
- `internal/update/replacement.go` 中必要的内部字段声明
- 必要时新增 `internal/update/installed_version.go` / `_test.go`，仅承载已安装版本的安全显示规则

### Red

1. 使用现有 `versionRunnerFunc` 与临时文件/注入 observer，验证真实入口 `ObserveReplacementTarget` 得到的观测，不运行已安装程序。
2. 验证 `local`、`dev`、`dev-setup-6a47df6-dirty-20260913.2` 的显示证据被保留，而 canonical `Version` 仍为空。
3. 建立安全边界表：128 字节接受，129 拒绝；允许大小写/数字/`._+-`；空、内部空格、方括号、引号、反斜杠、路径分隔符、换行、ANSI、非 ASCII 拒绝。边缘控制字符不能经 trim 后放行。
4. 保留单一 envelope、重复字段、额外字段、多个对象、错误 schema、非字符串值的拒绝测试。
5. 不可信目标不调用 runner；失败/超时无显示证据；父 context 取消仍传播；这些现有测试不得放宽。

运行并确认新增行为断言失败：

```console
go test ./internal/update -run 'TestVersionProbe|TestInstalledVersion' -count=1
```

### Green / Refactor

- 增加 `UnrecognizedVersion` 或等义的内部字段，使用 `json:"-"`；不将其写回 `Version`。
- 将严格 envelope 解析与 canonical/显示分类分开，复用一次解析；保留已有 `decodeProbedVersion` 的 canonical-only 返回契约，避免影响其它调用点。
- 保持 3 秒、4 KiB、执行信任、临时环境、文件身份前后校验和资源回收逻辑。
- 安全字符和长度规则只定义一份，不在 TUI 再复制另一套宽松解析。

验收：上述测试 Green，再运行 `go test ./internal/update`。本任务不改模板、比较算法或候选格式。

## 3. 任务 2：预览贯通与兼容性回归

**目标**：显示证据经过 app/preview 后仍属于同一被观测目标；风险、公开错误和 preview ID wire 保持不变。

涉及文件：

- `internal/update/replacement.go`、`replacement_test.go`
- `internal/app/replacement_targets_unix.go`、`replacement_targets_unix_test.go`
- `internal/app/replacement_targets.go`、`replacement_targets_test.go`（优先只补测试，按实际需要改生产代码）
- `internal/cli/self_test.go`、服务 apply 相关既有测试

### Red

1. 通过 `NewReplacementPreview` 验证安全显示字段不被清空；canonical 已知或文件不存在时不会展示冲突字段。
2. 对同一路径的别名归一化测试：同值保留；身份/摘要/版本或显示证据冲突拒绝。不同路径的同版本不合并。
3. Unix 初次观测经过内部 `NewReplacementPreview` 后仍保留字段；在身份、路径、摘要、存在性一致的 bound recheck 中复用原证据，变化时仍拒绝。
4. 为一份既有观测分别附加和不附加安全显示字段，断言 preview ID 相同，序列化的 snapshot/错误 details 不含新字段。
5. `ReplacementConfirmationError` 的 `targets[].version` 仍为字面量 `unknown`，共享 `ReplacementWarning` 不出现 `Unknown[local]`；旧 JSON 字段集合与 CLI 退出码保持原值。
6. 原有 downgrade/unknown 混合风险优先级、候选变化、对象变化、`ExpectedPreview` 失配仍拒绝，不能因显示字段放松。

```console
go test ./internal/update ./internal/app ./internal/cli -run 'Replacement|PreparedConsent' -count=1
```

Windows 不执行 Unix 文件的测试；对应实际行为由 Linux/macOS 原生测试补齐，不把交叉编译当成测试执行。

### Green / Refactor

- 在预览 clone/canonicalization 中防御性过滤和保留证据。
- Unix 仅在既有 bound 身份条件成立时复制证据，不增加锁内子进程或 IO。
- 利用 `json:"-"` 保持旧 wire；不要把字段加到 `replacementDisplayTarget`、APIError.Details 或任何公开 DTO。
- 用测试锁定旧 JSON，避免“添加可选字段也是向后兼容”的误判：旧 Unix 脚本明确拒绝未知字段。

验收：目标测试 Green，`go test ./internal/update ./internal/app ./internal/cli` 通过。修改 `scripts/install/*`、持久化结构或 candidate parser 表明偏离范围，应先缩回设计。

## 4. 任务 3：TUI 专用确认内容

**目标**：把固定预览转换成有限、类型化、可分段渲染的内容，不复用 CLI 的长警告拼接。

涉及文件：

- `internal/tui/pages/system/replacement.go`、`replacement_test.go`
- `internal/tui/ui/action.go`
- `internal/tui/ui/strings.go`
- 可新增 `internal/tui/ui/update_confirmation.go`，放置仅此弹窗使用的内容结构

### Red

围绕既有 `replacementUpdater` / `replacementFixture` 增加行为测试：

- Prepare 的实际候选进入内容，旧 Check 的版本不进入确认。
- 单副本、多副本、相同未知版本的两个副本都逐一展示；不合并。
- 已知版本、`Unknown[local]`、无标识 Unknown、明确不存在分别正确表达。
- 未知风险使用设计中的英文段落；未取得标识时使用相应英文说明。
- 任一确定降级仍保留全部原风险事实；无风险省略 Compatibility。
- 没有服务目标时不承诺重启服务/验证 daemon；存在服务目标时包含这些步骤。
- 内容不包含 Path、FileID、SHA256、原始 probe 输出或被过滤的标识。
- 正常 Prepare、迟到结果、channel 变化、取消和重复 Enter 的既有测试保持有效。

```console
go test ./internal/tui/pages/system -run 'PreparedConsent|UpdateConfirmation' -count=1
```

### Green / Refactor

- 为 `ActionIntentMsg` 增加可选的专用内容指针；这是进程内 TUI 消息，不增加协议字段。
- System page 仅从 `PreparedUpdate.Preview` 构造它，复制所需 slice，避免异步结果共享可变展示数据。
- 保留 Action、Key、Page、Execute、Cancel 和 generation 绑定；无需新增服务查询。
- 固定产品文案使用英文并放在既有 ui 文案边界。
- 可以保留 `Object` 的简洁摘要供既有 action ledger 使用，但不得将安全非标准标识扩散到通用日志或 CLI；专用弹窗不重复渲染这个摘要。
- 不修改共享 `update.ReplacementWarning`，把 TUI 分段文案与命令行警告分离。

验收：System 页目标测试及全包测试通过，展示数据与行为回调属于同一次固定预览。

## 5. 任务 4：专用布局、滚动与 root 接线

**目标**：只为 Mihari 更新提供分段渲染和固定按钮，复用既有主题与确认调度。

涉及文件：

- `internal/tui/update_confirmation.go`（建议新增）及 `_test.go`
- `internal/tui/modal.go`、`modal_test.go`
- `internal/tui/model.go`
- `internal/tui/replacement_test.go`、`help_test.go`、`race_test.go` 中相关行为测试

### Red

通过 root 的 `ActionIntentMsg` 入口验证最终 modal，而非只测试独立格式化函数：

1. 只有 `ActionUpdateMihari` 且含专用数据时走新布局；其它 action 和无专用数据的确认保持原样。
2. 分段标题、目标版本、各副本版本、风险段和执行说明均出现且不重复。
3. 在 `72×22`、`100×28` 中测量去 ANSI 后的显示宽高，不超过终端；标题、Confirm 和 Cancel 同时可见。
4. 128 字节标识换行后，通过遍历滚动位置可以读全；不以截图中间截断作为成功。
5. ↑/↓ 与 PgUp/PgDn 改变正文，不改变选择、不触发 Execute；resize 后滚动夹取正确。
6. 默认 Enter 取消；切换到 Confirm 后 Enter 只提交一次；Esc 走现有取消和清理路径。
7. 按键不穿透侧栏或页面；缩小至全局 TooSmall 时不可激活不可见的 Confirm。

```console
go test ./internal/tui -run 'UpdateConfirmation|Confirmation|ModalKeys|Prepared' -count=1
```

### Green / Refactor

- 在 Modal 中增设专用 kind/数据引用，或等价的小范围渲染分支；不按标题文本猜测类型。
- root `handleActionIntent` 保持现有 gating、pending、cancel 回调和确认绑定，只选择不同的 modal 构造入口。
- 标题与按钮区先预留高度，正文按剩余高度换行、分页与夹取。滚动偏移按显示行计算，保留滚动指示。
- 复用已有 ANSI-aware wrapping 与 Theme；只点亮标题/目标版本/风险标记，不给整个正文上色。
- View 无 IO、无提交副作用；滚动与按钮选择由 Update 管理。resize 后允许布局计算更新有效滚动边界，但不得改变候选。
- 保持现有 TooSmall 策略，不扩展终端支持范围。

验收：新 modal 和既有通用确认测试都通过，再运行 `go test ./internal/tui/...`。

## 6. 任务 5：跨包、视觉与文档验收

涉及文件：

- `internal/integration/replacement_confirmation_test.go`
- `internal/integration/self_update_service_test.go`
- `internal/tui/golden_test.go` 及本次新增的 fixture
- `README.md`、`README.zh-CN.md`、`docs/architecture.md` 的必要短说明
- 本设计/计划的实际实施记录

### 行为覆盖

- fake 已安装版本探测 → app 目标 → preview → TUI 确认内容的贯通，用临时文件和注入 runner；不启动真实服务或真实自更新。
- 固定候选、版本/文件变化拒绝、确认之前不 Apply、取消不执行、服务副本独立验证保持原不变量。
- CLI 错误 JSON 字段和值保持原样，安全非标准显示值不进入 CLI 输出；执行现有 shared version matrix。
- 保持候选为标准 stable/dev 的测试场景；不新增“允许安装 unknown 候选”的功能或测试预期。

### 视觉覆盖

新增聚焦于此次弹窗的 golden，不批量改写其它页面快照：

| 场景 | 尺寸/检查 |
| --- | --- |
| 双副本相同开发标识 | `100×28`，两行均在，正文分段 |
| 双副本 + 128 字节标识 | `72×22`，初始与滚动后视图，固定按钮 |
| 读取不到版本 | Unknown 与英文说明，不出现空方括号 |
| 普通升级 | 无 Compatibility 段 |
| 确定降级且另有 unknown | 保留完整降级风险事实，正文可滚动 |

golden 固定布局和文案；配色另用主题样式断言/渲染检视验证，不能因为去掉 ANSI 的 golden 相同就声称颜色已验收。首轮生成时逐张检查，再以不带更新参数的测试验证。

README 中只补充更新确认会保留安全的非标准已安装版本标识；不宣称 CLI/安装脚本已有相同行为。架构文档只调整 TUI 未知版本说明与专用展示边界。`CHANGELOG.md` 不修改。

## 7. 最终验证顺序

先完成各任务的最小测试，最终按风险执行：

```console
go test ./internal/update ./internal/app ./internal/cli ./internal/tui/... ./internal/integration
go test ./...
go test -race ./internal/update ./internal/app ./internal/cli ./internal/tui/... ./internal/integration
go vet ./...
gofmt -l internal/update internal/app internal/tui
git diff --check
```

使用仓库已有 lint 配置，不调整 Go/toolchain 或 lint 版本。未修改的 Windows/Unix 特有既有 lint 问题若出现，应记录和归因，不能顺手扩大生产改动。

CGO-free 编译覆盖 Windows/Linux/macOS × amd64/arm64。PowerShell 用明确的任务输出目录与环境变量设置/恢复，构建产物放在仓库外，不把测试二进制提交。Unix 的专有行为必须在 Linux/macOS 原生环境运行相关测试；本机 Windows 的成功不能代替它。

用户已授权提交、推送和创建 PR，跟踪三平台 CI、race、跨平台构建和可运行的 bot review；修复有效意见后复核最新 head。本次不合并或发版。

## 8. 结束检查与实施记录

- diff 只含设计范围内的字段、TUI 接线、测试与必要文档。
- 新显示字段不进入 public JSON、preview ID wire、APIError details、持久化或通用日志。
- 未增加执行旧文件的授权范围，未连接用户真实订阅、mihomo 或修改服务。
- 不合并相同版本行，不修改 Overview 或安装脚本，不扩大候选版本规则。
- 所有修改的 Go 文件格式化，目标测试及实际可运行平台检查通过。
- 无无关 golden 变化、临时报告、coverage 文件、二进制或用户修改覆盖。

实施记录：

- 任务 1：安全标签探测测试先因非标准标识被清空而失败，随后实现通过；规范版本解析及失败/不可信探测回归通过。
- 任务 2：预览过滤与同路径证据冲突测试先失败后通过；标签不改变 JSON、错误、风险、preview ID，目标内容变化仍拒绝。Unix 绑定复用同时覆盖 canonical 与非标准证据，原生执行交给 CI。
- 任务 3：结构化内容测试先因缺少专用内容失败后通过；不同副本独立展示，缺失版本不猜测。
- 任务 4：分段/滚动测试先失败后通过；额外回归证明窗口缩至最小尺寸以下时不可确认、Esc 仍可取消。
- 任务 5：临时文件与注入 runner 贯通平台观察、预览和 System 页确认；CLI unknown JSON 回归通过。现有 app 绑定测试单独覆盖 Unix 停机后证据复用。7 张新增 golden 已逐张检查，主题颜色有独立断言。
- 最终验证：`go test ./...`、相关 update/app/cli/tui/integration 包 `-race`、`go vet ./...`、修改范围 gofmt、`git diff --check` 均通过；7 张 golden 不带更新参数复测通过。
- 六目标 `CGO_ENABLED=0` 构建通过：Windows/Linux/macOS × amd64/arm64。本机未运行 Unix 原生测试，由 PR 的 Linux/macOS CI 执行。
- `GOOS=linux GOARCH=amd64 golangci-lint run ./...` 零问题，与 CI 目标一致；Windows lint 仅报告 dev 基线已有的 `internal/subscription/legacy_resources.go:52` 未使用常量，未扩大修改。
- 未做真实自更新或服务安装；测试仅使用 fake 与临时资源。
