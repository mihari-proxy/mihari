# TUI `?` 帮助页设计

日期：2026-08-31
状态：待审核
关联 issue：https://github.com/mihari-proxy/mihari/issues/162
目标分支：`feat/162-tui-help-page`
工作目录：`.worktrees/feat-162-tui-help-page`

## 1. 背景

TUI 页脚把 `? help` 当作受保护提示：窄终端优先保留 `?` / `q`，完整说明应在按 `?` 后打开的帮助页里。

当前帮助几乎是半成品。根 shell 在 `internal/tui/model.go` 里对 `?` 调用 `NewDetail(ui.HelpTitle, ui.HelpBody)`。`HelpBody` 写死在 `internal/tui/ui/strings.go`，只覆盖 rail 导航和 Subscriptions。

同时存在的问题：

- Overview / Proxies / Conns / Rules / Logs / Web GUI / System / Setup 以及搜索、详情、列选择、表单、确认框的按键未列出。
- 已实现的全局键未列出：`1`–`8`、`q`（非输入态）、`Ctrl+C`（始终）、`?` 本身。
- 页脚各页已有独立 hint，帮助没有跟着补。窄终端下 `FitFooter` 会丢掉中间动作，按 `?` 也看不到。
- 帮助走通用 `Modal` 详情：宽约 64 列、不滚动。把所有键堆进静态字符串会在最低 72×22 终端溢出。
- Setup 页在 `?` 处理之前就被 `dispatchPage`，Setup 里按 `?` 打不开帮助。

## 2. 目标

按 `?` 打开的帮助页成为完整、可读、和真实按键同步的说明。用户必须能一眼看到「这个键在这里做什么」。

- **每一行都是「按键 + 作用」**：左列 Display、右列 Label，两者同时出现。不允许只有键没有说明，也不允许把不同页的作用糊成一句。
- **同一物理键可以在不同页/模式下有不同作用**，帮助必须按页（和 mode）分开列出，互不覆盖。例如 `p` 在 Connections/Logs 是 pause，在 Subscriptions 是 cycle proxy；`u` 在 Subscriptions 是 activate，在 Rules 是 update provider，在 Web GUI 是 update panel。
- 覆盖所有当前生效的快捷键：全局 + 各 rail 页 + Setup / 表单 / 确认框 / 搜索 / 详情 / 列选择 / 端口编辑。只写真正绑了的键。
- 打开时先展示全局键和当前页键；若当前处于某种模式（搜索、表单等），该模式紧跟全局之后。其余页分组列在后面，每组都带页名。
- 最低 72×22 下正文必须能用 `↑/↓` 翻完，不能被 `Modal` 裁掉。关闭仍用 `Esc`。
- 帮助正文和底栏 hint **从同一份按键表生成**。底栏仍是单行短提示（窄屏 `FitFooter` 照旧丢中间、留 `?`/`q`）；`?` 仍是按页完整「键 + 作用」。视觉布局与现有 `Footer*` 字符串字节级相同。
- `?` 仍可从 rail 和内容区打开；Setup 在非文本步骤也能打开。

## 3. 非目标

- 不新增快捷键（含不给帮助加 `PgUp` / `PgDn` / `g` / `G`）。
- 不改页脚**视觉**布局、`FitFooter` 丢弃策略、确认框文案。底栏文案必须与今天的 `Footer*` 字符串相等；只改生成路径。
- 不为了 SSOT 给 `FormHelp` 补上 `? help  q quit`（那会改布局）。
- 不让按键表驱动各页 `Update()` 分发（那是重写全部页面）。
- 不改 `/v1`、CLI、daemon、Web GUI。
- 不改 `CHANGELOG.md`。
- 不为 `RestartRequired` 这类短详情弹层加滚动，除非它们改走帮助 kind。
- 不做「全局唯一按键表」（一个键只对应一个作用）。那会把跨页同键冲掉。
- 不在当前页的键下面做「also on Subscriptions: …」交叉引用。分节就是消歧义。

## 4. 方案比较

### 4.1 不采用：只把 `HelpBody` 扩成更长静态字符串

Issue 已否决。72×22 会溢出；之后每加一个快捷键要手改页脚和帮助两处。

### 4.2 采用：按键表同时生成页脚和帮助（真 SSOT）

`Catalog()` 是唯一文案源。`RenderHelp` 生成帮助；`RenderFooter` / `RenderRailFooter` 生成底栏。各页 `FooterHints()` 只负责报告当前 `(Page, Mode, 状态)`，不再手写 hint 字符串。

页脚仍是单行、状态相关：Web GUI 有无面板、搜索/表单/详情替换整行。这些状态继续由各页 `HelpMode()`（及 Web GUI 的 `available`）选择 recipe，token 本身来自 catalog 的 `Footer` 字段。生成结果必须等于今天的 `Footer*` 字符串，现有 `hints != ui.FooterSearchMode` 一类测试保持通过。

`FitFooter` 不改：仍按双空格切 token，中间可丢，`? help` / `q quit` 受保护。

### 4.3 不采用：按键表驱动真实 `Update()` 分发

正确，但会重写 shell 和每个页面的按键处理，超出 issue 范围。

### 4.4 不采用：只生成帮助、页脚靠测试约束

上一版方案。用户要求底栏与 `?` 数据联动，故升级到 4.2。

## 5. 按键表

### 5.1 类型

```go
type KeyScope uint8

const (
    ScopeGlobal KeyScope = iota
    ScopePage
    ScopeMode
)

type KeyBinding struct {
    Keys    []string // tea.KeyPressMsg.String()，如 "ctrl+c"、"1"、"?"
    Display string   // 帮助左列，如 "Ctrl+C"、"1–8"、"t"
    Label   string   // 帮助右列动作说明（完整、按页）
    Footer  string   // 底栏 token，如 "p pause"；空字符串 = 只出现在帮助里
    Scope   KeyScope
    Page    PageID // ScopePage 时必填
    Mode    string // ScopeMode 时必填；ScopePage 的默认态为空
}

type FooterOpt struct {
    WebGUIAvailable bool
}

func RenderFooter(page PageID, mode string, opt FooterOpt) string
func RenderRailFooter() string

const (
    ModeSearch    = "search"
    ModeDetail    = "detail"
    ModeColumns   = "columns"
    ModeForm      = "form"
    ModePortsEdit = "ports-edit"
    ModeConfirm   = "confirm"
    ModeSetup     = "setup"
)
```

**身份不是键本身，而是 `(Scope, Page, Mode, Keys) → (Label, Footer)`。** `Catalog()` 允许、也必须允许相同 `Keys` 出现多次，只要 Page 或 Mode 不同。帮助渲染禁止按 Display 做全局去重。底栏只收录当前 `(Page, Mode)` 且 `Footer != ""` 的 token，因此同一键在不同页的短提示也不会串台。

`Catalog()` 返回完整表，顺序即默认文档顺序。同一页、同一 Display 的别名写在同一个 `Keys` 里（例如 Web GUI 的 `"x","d"` 或 `"up","down","k","j"`）。不同页的相同 Display 必须是两条独立绑定。

数字键 `1`–`8` 合成一条：`Keys: []string{"1","2","3","4","5","6","7","8"}`，`Display: "1–8"`。

同键异义（必须同时出现在同一份帮助正文里，且分属不同节）：

| Key | Connections | Logs | Subscriptions | Rules | Web GUI |
| --- | --- | --- | --- | --- | --- |
| `p` | pause or resume | pause or resume | cycle proxy mode | — | — |
| `u` | — | — | activate | update the focused provider | update |
| `r` | — | — | refresh | reload | reinstall |
| `Enter` | details / activate control | details / activate control | details | details / activate control | — |

### 5.2 必须收录的绑定

只收录当前代码里真实处理的键。说明用现在页脚/现有帮助的语气，英文 UI 字符串保持英文。

**Global**

| Display | Keys | Label |
| --- | --- | --- |
| `1–8` | `1`…`8` | jump to a rail page outside text input |
| `?` | `?` | this help |
| `q` | `q` | quit outside text input |
| `Ctrl+C` | `ctrl+c` | quit always |
| `↑/↓` | `up`,`down` | select a rail page |
| `Enter` | `enter` | open the selected page from the rail |
| `Esc` | `esc` | return to the rail, close a dialog, or step back in Setup |
| `Tab` | `tab` | reserved for forms and dialogs |

**Overview**（无额外动作，只保留与页脚一致的返回）

| Display | Keys | Label |
| --- | --- | --- |
| `Esc` | `esc` | return to the rail |

**Proxies**

| Display | Keys | Label |
| --- | --- | --- |
| `Enter` | `enter` | expand a group or select a node |
| `t` | `t` | test the focused node |
| `Ctrl+T` | `ctrl+t` | test all |
| `↑/↓/←/→` | `up`,`down`,`left`,`right` | move |

**Connections**

| Display | Keys | Label |
| --- | --- | --- |
| `/` | `/` | search |
| `x` | `x` | close the focused connection |
| `p` | `p` | pause or resume |
| `Enter` | `enter` | open details or activate a control |
| `Ctrl+X` | `ctrl+x` | close all active connections |

`Ctrl+X` 已绑定但页脚未写。帮助要写；页脚不改。

**Rules**

| Display | Keys | Label |
| --- | --- | --- |
| `/` | `/` | search |
| `r` | `r` | reload |
| `u` | `u` | update the focused provider |
| `Ctrl+U` | `ctrl+u` | update all providers |
| `Enter` | `enter` | open details or activate a control |

**Logs**

| Display | Keys | Label |
| --- | --- | --- |
| `/` | `/` | search |
| `p` | `p` | pause or resume |
| `w` | `w` | wrap |
| `G` | `G` | jump to newest |
| `Enter` | `enter` | open details or activate a control |

**Subscriptions**

| Display | Keys | Label |
| --- | --- | --- |
| `a` | `a` | add |
| `e` | `e` | edit |
| `Space` | `space` | enable or disable |
| `p` | `p` | cycle proxy mode |
| `r` | `r` | refresh |
| `Ctrl+R` | `ctrl+r` | refresh all |
| `u` | `u` | activate |
| `d` | `d` | delete |
| `Enter` | `enter` | details |

**Web GUI**

| Display | Keys | Label |
| --- | --- | --- |
| `↑/↓` | `up`,`down`,`k`,`j` | select a panel |
| `Space` | `space` | set default |
| `o` | `o` | open |
| `i` | `i` | install |
| `u` | `u` | update |
| `r` | `r` | reinstall |
| `x` / `d` | `x`,`d` | uninstall |
| `b` | `b` | rollback |

**System**

| Display | Keys | Label |
| --- | --- | --- |
| `Enter` | `enter` | activate the focused row |
| `↑/↓` | `up`,`down` | move |

**Search mode**（Connections / Rules / Logs）

| Display | Keys | Label |
| --- | --- | --- |
| type | （不进 Keys，只展示） | filter the list |
| `←/→` | `left`,`right` | move cursor |
| `↑/↓` | `up`,`down` | leave the field |
| `Esc` | `esc` | finish search |

`type` 不是 `tea` 键，catalog 用空 `Keys` + `Display: "type"`，漂移测试跳过空 Keys。

**Detail mode**

| Display | Keys | Label |
| --- | --- | --- |
| `Enter` / `Esc` | `enter`,`esc` | close |

Connections 详情额外有 `←/→` 切 tab、`↑/↓` 滚动。这些是详情内部导航，写在 Detail mode 下作为可选第二行：`←/→` switch tabs, `↑/↓` scroll（仅当 Keys 在 `internal/tui/pages/connections/detail.go` 存在）。Rules / Logs / Subscriptions / System 详情只有 close。为避免“写了未实现的键”，Detail mode 只收录三页共用的 close；Connections 的 tab/scroll 作为 Connections 页的补充行，Mode 留空或使用 `ModeDetail` + `Page: PageConnections`。

采用：`ScopeMode` + `ModeDetail` 只写 close；另加一条 `ScopePage` + `PageConnections` + `ModeDetail` 写 tab/scroll。渲染时若当前页是 Connections 且 mode=detail，这两条都出现在 “This mode” 里；其他页的 detail 只出现 close。

**Columns mode**（Connections）

| Display | Keys | Label |
| --- | --- | --- |
| `↑/↓` | `up`,`down` | move |
| `Space` | `space` | toggle |
| `Enter` | `enter` | save |
| `Esc` | `esc` | cancel |

**Form mode**（Subscriptions add/edit）

| Display | Keys | Label |
| --- | --- | --- |
| `Tab` / `Shift+Tab` | `tab`,`shift+tab` | move between fields |
| `Enter` | `enter` | next or save |
| `Esc` | `esc` | cancel |

**Ports edit**（System）

| Display | Keys | Label |
| --- | --- | --- |
| type | （空 Keys） | edit the address |
| `Enter` | `enter` | apply |
| `Esc` | `esc` | cancel |

**Confirm**

| Display | Keys | Label |
| --- | --- | --- |
| `Tab` / `←/→` | `tab`,`shift+tab`,`left`,`right` | toggle Confirm / Cancel |
| `Enter` | `enter` | activate the selected button |
| `Esc` | `esc` | cancel |

不改确认框文案；帮助只描述键。确认框打开时 `?` 仍然被 modal 吃掉（保持现状）。

**Setup**

| Display | Keys | Label |
| --- | --- | --- |
| `Tab` / `Shift+Tab` | `tab`,`shift+tab` | move between fields |
| `Enter` | `enter` | continue |
| `Esc` | `esc` | previous step, or cancel on the first step |
| `q` | `q` | quit on non-text steps |
| `?` | `?` | this help on non-text steps |

Setup 文本步骤（endpoints / subscription）里 `?` 仍是字符，不能抢。

### 5.3 底栏 recipe（必须与现串相等）

`RenderFooter` 用双空格拼接 token，顺序固定。`? help` / `q quit` 来自 Global 绑定的 `Footer` 字段。

| 调用 | 生成结果（锁死） |
| --- | --- |
| `RenderRailFooter()` | `↑/↓ page  Enter open  ? help  q quit` |
| `RenderFooter(Overview, "")` / 无动作页 | `Esc back  ? help  q quit` |
| `RenderFooter(Proxies, "")` | `Esc back  Enter expand  t test  Ctrl+T test all  ? help  q quit` |
| `RenderFooter(Connections, "")` | `Esc back  / search  x close  p pause  Enter details  ? help  q quit` |
| `RenderFooter(Rules, "")` | `Esc back  / search  r reload  u update  Ctrl+U update all  Enter details  ? help  q quit` |
| `RenderFooter(Logs, "")` | `Esc back  / search  p pause  w wrap  G newest  Enter details  ? help  q quit` |
| `RenderFooter(Subscriptions, "")` | `Esc back  Enter details  a add  e edit  Space toggle  p proxy  r refresh  Ctrl+R refresh all  u use  d delete  ? help  q quit` |
| `RenderFooter(WebGUI, "", Available:false)` | `Esc back  ? help  q quit` |
| `RenderFooter(WebGUI, "", Available:true)` | `Esc back  ↑/↓ panel  Space set default  o open  i install  u update  r reinstall  x uninstall  b rollback  ? help  q quit` |
| `RenderFooter(System, "")` | `Esc back  Enter activate  ? help  q quit` |
| `RenderFooter(_, ModeSearch)` | `Type to filter  ←/→ cursor  ↑/↓ leave  Esc done`（不含 `?`/`q`：输入态下它们进搜索框） |
| `RenderFooter(Setup, "")` | `Tab fields  Enter continue  Esc back  Ctrl+C quit` |
| `RenderFooter(_, ModeDetail)` | `Enter/Esc close  ? help  q quit` |
| `RenderFooter(_, ModeColumns)` | `↑/↓ column  Space toggle  Enter save  Esc cancel  ? help  q quit` |
| `RenderFooter(_, ModeForm)` | `Tab/Shift+Tab fields  Enter next/save  Esc cancel`（**不含** `?`/`q`） |
| `RenderFooter(_, ModePortsEdit)` | `Type address  Enter apply  Esc cancel  ? help  q quit` |

帮助-only（`Footer == ""`，底栏不出现、帮助要出现）：`1–8`、`Ctrl+C`、`Tab` reserved、Connections `Ctrl+X`、Web GUI 的 `d` 别名（底栏只留 `x uninstall`）、Connections 详情 tab/scroll、确认框键、System `↑/↓`。

各页 `Footer` 短文案与帮助 `Label` 可以不同（底栏 `u use` / 帮助 `activate`），这是同一条绑定上的两个字段，不是两张表。

`strings.go` 里现有 `Footer*` / `FormHelp` / `SetupFooter` 改为由 `RenderFooter` 赋值的 `var`（或保留同名别名），这样 `hints != ui.FooterSearchMode` 等测试不用改断言对象。禁止再手写一份平行的 footer 字符串。

## 6. 帮助正文

`RenderHelp(active PageID, mode string) string` 输出纯文本，section 之间空一行。用户感知来自三件事：弹层标题带当前页名、每一行都是键+作用、每一节都有页/mode 名。

弹层标题：`Keyboard help · <PageLabel(active)>`（`HelpTitle` 常量仍是 `"Keyboard help"`，页名在 `NewHelp` 处拼接）。正文不再重复写死旧 `HelpBody`。

标题行用 `Section:` 前缀，便于测试。每一行固定两列：

```
Global:
  1–8     jump to a rail page outside text input
  ?       this help
  ...

This mode · Search:
  type    filter the list
  ...

This page · Connections:
  p       pause or resume
  Enter   open details or activate a control
  ...

Subscriptions:
  p       cycle proxy mode
  u       activate
  ...

Web GUI:
  u       update
  ...
```

硬规则：

- 行格式永远是 `Display` + 至少两空格 + `Label`。缺 Label 的绑定不得进 catalog。
- 去重只发生在**同一节内**，键为 `Display + "\t" + Label`。禁止按 `Display` 或 `Keys` 做跨节合并。
- 当前页节只含该页默认态绑定；不得把 Subscriptions 的 `p` 写进 Connections 节。
- 未绑定的键不要在该页节里占位（不写 “unbound”）。

排序规则：

1. `Global:` 永远第一。
2. 若 `mode != ""` 且 `mode != ModeSetup`，`This mode · <ModeLabel>:`。该节只含 `Mode == mode` 且 `Page == "" || Page == active` 的绑定。
3. `This page · <PageLabel(active)>:`。Setup 也走这一条。只含 `ScopePage && Page == active && Mode == ""`。
4. 停止。不输出其它页、未激活 mode、非当前 Setup。

左列 `Display` 对齐到该节最长 Display 的宽度（至少 8）。

不再使用 `ui.HelpBody`。

## 7. 可滚动帮助层

### 7.1 新 kind，而不是复用 `NewDetail`

`NewDetail` 还用于 `RestartRequired`。帮助使用：

```go
func NewHelp(title, body string) *Modal
```

`modalKind` 增加 `modalHelp`。`Update`：

- `esc` → `ModalClose`（与现在一致）
- `up` / `down` → 调整 `scroll`，不关闭
- 其他键忽略（含 `q`、`?`、数字键、确认框那套 Tab/Enter）

`q` 在帮助打开时不退出进程：现有 modal 优先于 `q` 处理，保持这一不泄漏约定。

### 7.2 视口

`View(width, height)`：

- `boxWidth = min(64, max(24, width-8))`（与现详情弹层同宽，72 列终端可用）
- `boxHeight` 不超过 `max(5, height-2)`，避免 22 行终端溢出
- 正文按行切分；可见行 = box 内高 − 标题 − 空行 − 边框 − 滚动指示
- 顶部有未显示行时画 `▴`；底部有未显示行时画 `▾ N more lines`（与 Connections 详情同一套文案模式）
- `scroll` clamp 到合法窗口
- 居中 `lipgloss.Place`

72×22 下整段帮助一定高于视口，因此必须出现 `▾`，`down` 必须改变可见首行。

## 8. Shell 接线

### 8.1 打开帮助

现在：

```go
if model.active == ui.PageSetup {
    return model.dispatchPage(message)
}
if name == "?" {
    model.modal = NewDetail(ui.HelpTitle, ui.HelpBody)
}
```

改为：

1. 若已有 modal，行为不变。
2. 若 `name == "?"` 且 `active != PageSetup` 且 `inputMode != InputText`：打开 `NewHelp(HelpTitle+" · "+PageLabel(active), RenderHelp(active, helpMode(page)))`。文本输入（搜索、订阅表单、端口编辑）里 `?` 是字符，与 `q` 一致。
3. Setup 继续 `dispatchPage`。非文本步骤里 Setup 对 `?` 返回 `ui.OpenHelpMsg{}`；文本步骤把 `?` 交给 textinput。
4. Shell 处理 `OpenHelpMsg` 与按 `?` 相同。

`helpMode(page)`：若 page 实现

```go
type HelpModeProvider interface {
    HelpMode() string
}
```

则用其返回值，否则 `""`。实现方与现有 `FooterHints()` 的分支一致：

| 页 | 条件 | HelpMode |
| --- | --- | --- |
| connections / rules / logs | detail | `ModeDetail` |
| connections | columnsOpen | `ModeColumns` |
| connections / rules / logs | searching | `ModeSearch` |
| subscriptions | form != nil | `ModeForm` |
| subscriptions | detail != nil | `ModeDetail` |
| system | editID != "" | `ModePortsEdit` |
| setup | 始终 | `ModeSetup`（当前页已经是 Setup 时，“This page”即 Setup，不再重复 This mode，见下） |

Setup 的 `HelpMode()` 返回 `ModeSetup` 时，渲染器把 Setup 当作当前页，而不是再插一个 This mode，避免两节重复。规则：若 `mode` 对应的是一个 Page（Setup）而不是叠加态，则跳过 “This mode”，只保留 “This page · Setup”。

实现上：`ModeSetup` 不进入 “This mode” 分支。

### 8.2 关闭

`Esc` → `ModalClose` → `model.modal = nil`。已有 `TestHelpDialogOpensFromRailAndContentAndClosesOnEsc` 继续成立，断言改为包含 `HelpTitle` 且正文含 `Global:`。

### 8.3 太小的终端

`TooSmall` 画面已经写 `Press ? for help or q to quit`。`?` 处理在 TooSmall 判断之前，保持可打开帮助。

## 9. 页脚（与 `?` 同源）

`FitFooter`、受保护 token（含 `?` 的片段、`q quit`）、`FooterHintProvider` 接口都保留。

Shell `View`：rail 焦点用 `RenderRailFooter()`；内容焦点用 `page.FooterHints()`，否则 `PageFooterHints(id)`。后两者都转调 `RenderFooter`。

各页 `FooterHints()` 只选 recipe，例如 Connections：

```go
func (m *Model) FooterHints() string {
    return ui.RenderFooter(m.ID(), m.HelpMode(), ui.FooterOpt{})
}
```

Web GUI 传入 `FooterOpt{WebGUIAvailable: m.available}`。`PageFooterHints` 对 Web GUI 仍生成无面板那条（与今天 `FooterWebGUI` 一致）。

订阅表单 overlay 里那行 `FormHelp` 同样改成 `RenderFooter(PageSubscriptions, ModeForm, …)`，这样 overlay 内提示也同源。

打开 `?` 时整屏仍被 modal 换掉，底栏暂时不可见（现状）。关闭后底栏回来，hint 与帮助当前页节对应同一批 catalog 条目。

## 10. 测试

全部留在默认 `go test`，不访问公网、不启真实 daemon。

1. **Catalog 完整性**：`Catalog()` 非空；每个 `ScopePage` 的 `Page` 是已知 `PageID`；每个非空 `Keys` 元素是非空字符串。
2. **页脚 SSOT**：`TestRenderFooter_MatchesCurrentLayout` 表驱动，§5.3 每一行 `RenderFooter` / `RenderRailFooter` 的返回值必须与锁死字符串相等。`FooterProxies` 等导出名字等于对应 `RenderFooter` 结果。Catalog 里每个非空 `Footer` 必须出现在对应 recipe 的生成串里；生成串里每个中间 token 必须能在 catalog 找到（`? help` / `q quit` / `Esc back` 来自 Global/chrome 绑定）。
3. **帮助覆盖 catalog**：`RenderHelp` 对每个 rail 页各渲染一次，输出必须包含该页所有 `Display` 与对应 `Label`；`RenderHelp(PageProxies, "")` 中 `This page · Proxies` 出现在 `Subscriptions:` 之前。
4. **当前 mode 优先**：`RenderHelp(PageConnections, ModeSearch)` 中 `This mode · Search` 出现在 `This page · Connections` 之前、`Global:` 之后。
5. **同键异义**：`RenderHelp(PageConnections, "")` 的 Connections 节含 `p` + `pause`，不得含 `cycle proxy`；同一份正文的 Subscriptions 节含 `p` + `cycle proxy`。`u` 在 Subscriptions / Rules / Web GUI 三节中分别是 activate / update the focused provider / update，不得互相覆盖。每一行都能用正则 `^\s+\S.+\s{2,}\S` 拆出键和说明。
6. **源码漂移**：每个非空 `Keys` 值必须作为带引号字面量出现在绑定所属文件。映射：
   - Global：`internal/tui/model.go`（`tab` 允许出现在 `internal/tui/modal.go`）
   - 各 Page：对应 `internal/tui/pages/<pkg>/model.go`（Connections 的 `ModeDetail` 额外键在 `detail.go`；Web GUI 的 `j`/`k` 在 `model.go`）
   - Search/Detail/Columns/Form/Ports/Confirm/Setup：各自 handler 文件
7. **滚动**：`NewHelp` 在 `View(72, 22)` 下，40+ 行正文时出现 `▾`；所有可见行 `lipgloss.Width <= 72`；`down` 后可见窗口下移；`esc` 返回 `ModalClose`；`up` 在顶端是 no-op。
8. **Shell**：rail 与 content 都能打开帮助；标题含当前页名；打开后可见当前页节的键+作用；Setup 非文本步骤发 `OpenHelpMsg`（或等价：shell 打开帮助）；Setup 文本步骤不打开帮助。
9. **回归**：`TestModalKeysDoNotLeakToRailOrPage`、`TestQuitIsReachableOutsideTextEntry`、`TestCompactFooterRendersWithoutOverflow` 保持通过。

不引入 golden 文件，除非现有 `testdata/` 因 View 变化必须更新。帮助层覆盖整个 View，若 golden 捕获的是无 modal 状态则无需动。

## 11. 文档

- 本 spec + implementation plan。
- README 目前没有快捷键清单，不新增。
- 不改 `CHANGELOG.md`。

## 12. 风险与回滚

- 帮助比现在长，滚动实现若高度算错会在 22 行终端把标题挤出屏幕。用 `height-2` 硬上限和 compact 测试锁住。
- 源码漂移测试是字符串包含，可能假阳性（注释里的 `"x"`）。把匹配写成 `"`+key+`"`，并允许在测试里为确知冲突的键指定文件。若某键只出现在注释，测试应失败——这正是我们要的。
- 打开帮助时整屏被 modal 替换（现状）。保持这一行为，避免和页脚/rail 焦点抢键。关闭后底栏回来，token 与帮助当前页节来自同一批 catalog 条目。
- `RenderFooter` 若拼接顺序与 catalog 声明顺序不一致，会和锁死字符串差一个空格或 token 次序。Task 1 的字节级测试会挡住。

失败时回滚范围为 `internal/tui` 与本 spec/plan，不影响协议和 daemon。
