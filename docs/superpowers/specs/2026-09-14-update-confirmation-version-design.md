# TUI 更新确认：已安装未知版本展示与弹窗布局

日期：2026-09-14。状态：用户已确认产品设计及实施，核心代码和回归测试已完成，本地验证完成，PR 评审待跟进。

基线：`dev` 的 `6221d69e0e599d77cf2fdfde31a93d2f23886922`。工作分支：`fix/update-confirmation-version`。执行方案：[配套计划](../plans/2026-09-14-update-confirmation-version.md)。

## 1. 问题与目标

本机 PATH 副本与 Program Files 服务副本通过 `self version --json` 均报告 `dev-setup-6a47df6-dirty-20260913.2`。当前替换探测只保留可比较的版本，非标准标识在探针和预览规范化过程中被清空，TUI 因此显示两处 `Unknown`，再输出一整段通用兼容性警告。

目标：仅在 TUI System 页的 **Update Mihari 确认弹窗**中保留已安装副本的安全构建标识，并改善阅读层次。示例：

```text
Unknown[dev-setup-6a47df6-dirty-20260913.2]
```

`Unknown` 表示 Mihari 不能确定版本先后与兼容性；方括号内是实际读取到的标识。它不表示已经检测到降级，也不证明该构建来自官方发布。

## 2. 已确认的范围

| 用户决定 | 本设计落实方式 |
| --- | --- |
| Q1：只修更新确认 | 仅 TUI Update Mihari 弹窗消费新显示信息 |
| Q2：确实读不到仍为 Unknown | 不猜测、不用当前 TUI 版本替代服务副本 |
| Q3：改为准确的独立说明，英文 | 未知兼容性使用专用英文段落；确定降级保留完整风险说明 |
| Q4：安全、限长标识 | 仅接受不超过 128 字节的 ASCII 字母、数字及 `._+-` |
| Q5：分段、克制着色 | Installed/Target、Compatibility、After confirmation 分段，复用 Theme |
| Q6：不同副本不合并 | 即使版本相同也分别列出，不新增按版本归并逻辑 |
| Q7：换行、滚动、固定按钮 | 标题与按钮固定；正文滚动；默认 Cancel，Esc 取消 |

不在本次范围内：

- Overview、System 当前版本行、daemon 信息卡、`self version`、CLI `status` 的显示改造。
- CLI `self update` / `service apply`、六个安装脚本的文本或 JSON 输出改造。
- 待安装版本为 unknown 的识别、展示或放行策略；候选来源和版本校验保持原有规则。
- 增加版本模板、支持 rc/alpha/build metadata 比较、统一本地构建命名。
- 修改执行信任规则、为读取版本提权、调整安装路径或系统服务。
- 其他确认弹窗的外观重构、其他含义的 Unknown、版本比较或数据迁移策略。
- 新依赖、新公开 CLI 参数、daemon `/v1` DTO、持久化格式与退出码修改。

## 3. 初步调查与文档依据

### 3.1 当前版本格式

| 类别 | 示例 | 现有比较规则 |
| --- | --- | --- |
| Stable 模板 | `v0.9.3` | 可比较 |
| Dev 模板 | `v0.9.4-dev.2` | 可比较 |
| 默认源码构建值 | `dev` | 不可比较，不是第三种版本模板 |
| 自定义构建标识 | `local`、`dev-setup-…` | 不可比较，没有预制模板 |

已安装版本比较允许首尾空白和省略小写 `v`；这是现有规范化规则，不改变它。数值溢出仍为不可比较。

代码证据：`internal/buildinfo/buildinfo.go`、`internal/update/channel.go`、`internal/update/replacement_version.go`。仓库规则见 [发布流程](../../RELEASE.md#版本命名规范)。

SemVer 允许一般 prerelease 和 build metadata，而 Mihari 的模板是较窄的项目规则；例如 `1.2.3-rc.1` 和 `1.2.3+local` 不因此获得 Mihari 的比较资格。参见 [SemVer §9–11](https://semver.org/spec/v2.0.0.html#spec-item-9)。Context7 查询的 [Masterminds semver 文档](https://github.com/Masterminds/semver)区分原始字符串与规范版本，支持将显示信息与比较信息分开的思路；本项目不引入该依赖。

### 3.2 影响面审计

| 位置 | 事实与处理 |
| --- | --- |
| `internal/update/version_probe.go` | 严格解析单一 JSON envelope 后，`decodeProbedVersion` 将非 canonical 标识清空；需要保留受限的显示证据 |
| `internal/update/replacement.go` | `NewReplacementPreview` 再次规范化；风险、JSON 错误和共享警告依赖 canonical `Version`；不能把展示标记塞回此字段 |
| `internal/app/replacement_targets_unix.go` | 预览经过额外规范化，提交复核按路径/身份/摘要复用旧版本；需要检查显示证据不会在中间复制时丢失 |
| `internal/tui/pages/system/replacement.go` | 生成版本摘要和 Impact，当前重复展示版本；改为专用结构化内容 |
| `internal/tui/modal.go` | 通用确认连续拼接正文，无正文高度限制；增加仅供 Mihari 更新使用的渲染分支 |
| `internal/tui/pages/overview/model.go` | 自定义版本可能误加 `v` 前缀；记录为范围外，不顺手修复 |
| CLI / System 其他版本显示 | 非空原值通常保留；本次不统一格式 |
| Windows `install.ps1` / `install-aio.ps1` | 各自的版本探测也会清空非标准标识；本次不修改 |
| Unix `root-apply.sh.in` 及三份生成脚本 | 严格要求固定 JSON 字段和 canonical/`unknown` 值；连新增 raw 字段也会被旧脚本拒绝，因此公开 JSON 必须不变 |

沿用 [Issue #193 设计](2026-09-09-issue-193-downgrade-warning-design.md)中的固定候选、实际目标集合、预览确认和替换前复核；本次只在 TUI 未知兼容性文案及显示信息保留方面作局部修订。架构边界见 [当前架构](../../architecture.md#tui)。

## 4. 版本信息模型

### 4.1 分离比较字段与显示证据

保留 `ReplacementTarget.Version` 的既有语义：只能是规范化后的可比较版本，无法比较时为空。`ClassifyReplacementVersion`、`ReplacementWarning`、`ReplacementConfirmationError` 和候选 `ReplacementCandidate.Version` 均保持原行为。

在实际已安装目标上增加一个仅在内存中使用的可选字段，建议命名为 `UnrecognizedVersion string`，标注 `json:"-"`。该字段只存通过安全字符规则的不可比较标识，不存完整 stdout、stderr、路径或任意错误信息。它不是协议 DTO，也不是持久化状态。

探针仍只执行已经通过现有执行信任检查的已安装程序。解析内部可返回“canonical 版本 + 安全的非标准标识”两项；已有 `decodeProbedVersion` 的返回契约保持 canonical-only，通过薄适配复用解析。任何候选版本探测调用点不能消费新显示字段来绕过验证。

`NewReplacementPreview` 必须复制并保留合法的显示证据，同时防御性过滤手工构造的无效值。已存在可比较 `Version` 时忽略/清空非标准显示字段，避免相互矛盾的 UI。不存在的目标不得携带可展示的已安装版本。

### 4.2 输入与展示规则

对非标准显示标识使用下列顺序：

1. 必须先通过既有单一 `mihari/v1` JSON envelope 校验；拒绝重复字段、额外字段、多个对象、非字符串等。
2. 含控制字符、换行、ANSI、非 ASCII 字符时拒绝显示；不能通过 TrimSpace 偷偷去掉控制字符后放行。
3. 可去掉首尾普通 ASCII 空格，随后要求非空、长度 1–128 字节，且仅含 `[A-Za-z0-9._+-]`。
4. 超长或包含其他字符时整个显示标识丢弃，不截断成另一个看似有效的标识。
5. 保留允许字符的原始大小写和内容，不补 `v`，不推导日期、SHA 或先后关系。

| 观测结果 | TUI 更新确认显示 | 风险 |
| --- | --- | --- |
| `v0.9.3` / `0.9.3` | 沿用规范版本 `v0.9.3` | 现有比较结果 |
| `v0.9.4-dev.1` | `v0.9.4-dev.1` | 现有比较结果 |
| `dev` / `local` | `Unknown[dev]` / `Unknown[local]` | unknown |
| `dev-setup-6a47df6-dirty-20260913.2` | `Unknown[dev-setup-6a47df6-dirty-20260913.2]` | unknown |
| 有效 envelope 中不可显示的标识 | `Unknown`，可说明没有可用版本标识 | unknown |
| 未获执行信任、超时、探测失败、空输出、无效 envelope | `Unknown` | unknown；沿用原有错误优先级 |
| 待替换文件不存在 | `Not installed` | 沿用 fresh，不伪装成未知已安装版本 |

未获得版本时不能改用 `buildinfo.Version` 或 daemon Status 代替磁盘上另一目标的身份。既有 3 秒超时、stdout/stderr 各 4 KiB、隔离环境、探测前后身份/摘要校验和清理规则不变。

### 4.3 指纹与兼容性

安全显示证据来自已核对身份和摘要的同一目标，不能用于决定是否允许执行或判断降级。显示字段不进入既有 JSON 序列化与 preview ID 的 wire 数据；相同 canonical 输入添加显示证据后，原 preview ID 必须保持一致。

目标内容、路径、文件身份、存在性、canonical 版本或服务定义变化仍按原逻辑使确认失效。显示字段本身不成为新的授权依据，也不新增持久化或跨 helper 传输。Unix 在身份和摘要完全相符时复用原观测证据，不在持锁提交阶段重新启动探针。

不同文件仍独立观察。保留既有同一实际路径的角色归一化；不增加按版本相同合并行的代码。对于同一路径的重复观测，如果安全显示证据冲突，应在形成 TUI 预览前拒绝不一致观测，不能任意选取其中一个。

## 5. TUI 内容与状态

### 5.1 信息顺序

```text
Update Mihari

Installed
  Binary    Unknown[local]
  Service   Unknown[local]
Target      v0.9.4-dev.2

Compatibility unknown
The installed build uses an unrecognized version
label. Mihari cannot determine whether this is
an upgrade or downgrade, or confirm data compatibility.

After confirmation
Replace Mihari, synchronize and restart the installed
service, verify its version, then reopen the TUI.

If installation fails, reopen Mihari to retry.

                         Confirm    Cancel
```

上图表达内容顺序，不固定每行断点。版本只在 Installed/Target 展示一次，不再在警告段落追加 `service: ... -> ...; binary: ... -> ...`。

- 分别渲染每个实际目标；使用白名单角色 Binary、Service、Path、Managed。已有同一路径多个角色可以按原角色信息显示，不能按版本把不同目标合并。
- Target 使用 Prepare 已固定、验证过的版本；不使用可能已经过期的 Check 结果。
- 只有存在相应服务目标时，才说明同步服务、重启与验证 daemon 版本；没有服务时说明替换程序并重新进入 TUI。
- `Not installed` 只来自明确的文件不存在观测，不来自读取失败。

### 5.2 英文文案

未知且具有安全非标准标识：

> The installed build uses an unrecognized version label. Mihari cannot determine whether this is an upgrade or downgrade, or confirm data compatibility.

未知且没有可用标识：

> No usable version label is available for one or more installed copies. Mihari cannot determine whether this is an upgrade or downgrade, or confirm data compatibility.

上述表述不声称当前发生了数据丢失。存在安全标识与无标识目标的混合情况时，逐行区分版本，以第二段作为总体说明即可，不重复两段。

确定降级：使用 `Downgrade detected` 标题，并保留现有关于 settings、subscriptions、state、generated files、无法启动/读取、看似数据丢失、降级不是配置迁移、不会回滚磁盘状态的完整英文风险事实。可以分成短段落，不删减含义。任一已知目标为 downgrade 时沿用既有优先级；其他未知目标仍在 Installed 行明确显示。

没有兼容性风险时省略 Compatibility 段，不新增绿色“安全兼容”承诺。

具有服务目标的行为说明：

> Replace Mihari, synchronize and restart the installed service, verify its version, then reopen the TUI.

无服务目标的行为说明：

> Replace Mihari, then reopen the TUI.

底部辅助说明：

> This TUI closes after confirmation. If installation fails, reopen Mihari to retry.

这些新文案仅属于 TUI 更新确认，不修改共享 `ReplacementWarning`，因而 CLI 和安装脚本继续输出其既有警告。

### 5.3 外观

- 复用 `ui.DefaultTheme()` 的圆角边框、间距和字体风格，不加依赖或独立调色板。
- 标题与目标版本使用现有 accent；`Unknown[...]` 和兼容性标题使用 warning 琥珀色；确定降级可用既有 danger 色标题。
- 分段标题用适度粗体，正文正常前景色；角色标签、操作提示和重试说明用 muted。
- 分段空一行，不嵌套边框、不加装饰图标、不使用大面积填色或额外动画。
- 仅固定顶部标题和底部按钮/必要按键提示；Installed、Target、Compatibility、After confirmation 都处于同一正文滚动区域。

### 5.4 尺寸与按键

沿用仓库最小终端 `72×22`，验证 `72×22` 和 `100×28`。弹窗宽度遵循现有最大宽度约束，正文内部宽度必须扣除边框与 padding；高度不超过终端可用高度。

- 长标识按终端显示宽度完整换行，不用省略号截断合法的 128 字节标识。
- `↑/↓` 每次滚动一行；`PgUp/PgDn` 滚动一页；滚动位置在 resize 后重新夹取到有效范围。
- `←/→`、Tab、Shift+Tab 选择 Confirm/Cancel；Enter 激活当前选择；Esc 取消。
- 每次打开默认 Cancel。滚动不会改变按钮选择或提交，不要求重复输入版本号。
- 顶部/底部使用轻量溢出提示和简短按键说明；按钮始终可见。
- 小于全局最低尺寸时复用现有 TooSmall 页面与键盘门禁，不引入第二套最小尺寸规则；不得通过看不到的 Confirm 提交。
- 各种按键只在弹窗中消费，不穿透页面、侧栏或重复启动 Prepare。

## 6. 代码边界与生命周期

建议在 `internal/tui/ui` 增加只服务于 Mihari 更新确认的类型化内容载体，通过 `ActionIntentMsg` 的可选字段传递。载体仅含白名单角色、安全显示版本、固定候选版本、兼容性状态及执行说明所需的最小信息，不能携带可直接渲染的文件路径或完整 Preview。

`confirmPreparedMihariUpdate` 从固定的 `PreparedUpdate` 构造该载体。root 只在 `ActionUpdateMihari` 且专用数据存在时创建更新确认 modal；其它 action 继续调用原 `NewConfirmation`。无专用数据的旧测试或调用点保留原路径，不依赖标题字符串判断类型。

专用 modal 可以放在 `internal/tui/update_confirmation.go`，复用既有 ModalAction、按钮主题和 root action 调度，不开发通用富文本弹窗框架。

取消、channel/generation 变化、页面离开和迟到确认继续走既有 `DiscardPreparedUpdateMsg` 与 Run owner。确认仍为固定候选设置 `Yes` 和 `ExpectedPreview`，先恢复终端和关闭 owner，再 ApplyPrepared。不得因为改了布局绕过既有检查、提前停止服务或在 View 中执行 IO。

## 7. 验收标准

1. 可信已安装副本的 `local`、`dev`、示例开发标识显示为完整 `Unknown[...]`；正常 stable/dev 展示和风险比较不变。
2. 双副本同版本依然有两行；双副本不同版本逐一准确展示。
3. 不可信/失败探测保持 Unknown，不填入当前 TUI 的版本；安全过滤失败不泄露原值。
4. 未知兼容性使用英文独立说明；确定降级保留全部既有风险事实；无风险不显示该段。
5. `72×22` 与 `100×28` 标题和按钮可见，最长标识与正文可滚动读全，无越界；resize 不触发提交。
6. 默认取消、显式确认、取消清理、迟到结果、预览变化拒绝均有回归测试。
7. 公开 JSON、CLI 警告、安装脚本、比较结果、候选校验、preview ID wire、持久化格式及其他 modal 不变。
8. 仅使用 fake/临时文件测试，不连接真实 mihomo、订阅，不修改本机服务或执行实际升级。

## 8. 交付状态

设计已实施，实际验证记录见配套执行计划。用户已授权提交、推送和创建指向 dev 的 PR，并要求跟进 CI 与可用 Bot review；不包含合并或发版。
