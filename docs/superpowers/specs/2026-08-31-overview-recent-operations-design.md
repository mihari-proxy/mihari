# Overview Recent operations 展示 SysProxy / TUN 动作与结果

日期：2026-08-31
状态：已定稿
Issue：https://github.com/mihari-proxy/mihari/issues/159
目标分支：`feat/159-overview-recent-operations`
工作目录：`.worktrees/feat-159-overview-ops`
基线：`origin/dev` @ `470e516`

## 1. 背景

Overview 的 **Recent operations** 对 System proxy / TUN 只记「对象 + 成败」，成功时看不出刚做了什么，失败时也看不出原因。实际界面类似：

```
System proxy · Succeeded
TUN · Failed
TUN · Succeeded
```

Enable / Disable / Force enable 被压成同一条 `System proxy · Succeeded`。用户无法从账本确认：

- 刚做的是打开、关闭，还是覆盖别人的配置
- System proxy 成功后系统代理指到哪里（mixed 目标、是否 Owned）
- TUN 成功后 Live 是否起来、用的哪个 stack
- `TUN · Failed` 是冲突、未提权、live 核对失败，还是别的错误

确认框里已经写了 Impact，动作结果里也带回了 `SystemProxyStatus` / `TunStatus` / 错误。记账时全丢掉了。

根因在 TUI 壳，不在 daemon：

- `recordActionOutcome`（`internal/tui/model.go`）只写 `Object` + `Succeeded`/`Failed`。`ActionIntentMsg.Title` / `Action` / `Impact` 和结果里的 status 都没用。
- Overview 渲染是 `Object · State`（`internal/tui/pages/overview/model.go`）。
- `systemProxyActionResultMsg` / `tunActionResultMsg` 已带动作 kind、返回 status 和 error，壳层却只看 `Err()`。这两个消息类型未导出，壳层不能对它们做类型开关。

System 页动作行结束后的 sticky 徽章也是 `Done` / `Failed`，同样不说「做了什么」。本设计以 Overview 账本为主；System 行不纳入本次范围。

## 2. 目标

Recent operations 对 System proxy / TUN **至少**写清三件事：动作、结果摘要、成败。失败还要有可展示的原因（已有错误码 / `last_error` / 冲突摘要），不要只写 Failed。

成功记录必须能区分 Enable / Disable / Force，不能再三条都叫 `System proxy · Succeeded`。

每条账本还要展示操作完成时间（`OperationRecord.At` 已写入，当前未渲染）。时间落在行尾、相对卡片内容宽度**右对齐**，SysProxy / TUN 与其它动作同一套时钟列。

## 3. 非目标

- 不改 daemon 行为、`/v1` 字段、错误码、JSON envelope、CLI 输出。
- 不改 Enable / Disable / Force 门控语义（那是 #95 / #102）。
- 不在本 issue 统一 Desired / SysConfig 三态（#102）。
- 不把整份 Impact / Rollback 长文塞进账本。
- 不 enrichment 其它账本条目的 Action / Detail（订阅刷新、选节点、重启核心等）；那些左侧仍渲染 `Object · State`，但同样右对齐显示时间。
- 不把时间改成相对时间（`2m ago`）、不跨日补日期、不写时区名。本列只服务当前 TUI 会话。
- 不改 `At` 的写入时机：仍是动作完成瞬间的本地 `time.Now()`，不引入可注入时钟，除非测试需要（测试直接构造 `At`）。
- 不改 System 页 sticky `Done` / `Failed` 徽章。失败行已经有 `outcomeDetail`；成功徽章改文案会搅一批 System 测试。需要时另开。
- 不把 Windows / registry / 底层 OS 原文当稳定 UI。
- 不写订阅 URL、secret、controller 地址。失败路径不把 `current_server` 写入账本。

## 4. 方案比较

### 4.1 采用：给 `OperationRecord` 加 `Action` / `Detail`，壳层从 intent + 结果填

账本仍由 `recordActionOutcome` 单一路径写入。动作名来自已有的 `ActionIntentMsg.Action`；摘要来自结果消息上的窄接口（因为 page 结果类型未导出）。Overview 按字段优先级排版：窄卡先丢 Detail，再丢右对齐时间列，永不先丢 Action 或 State。`At` 已经写入，只补渲染。

能复用现有词表（Enable / Force enable / Owned / Live / stack 名），不扩 `/v1`。

### 4.2 不采用：只把 `Object` 换成 `Title`（`Enable system proxy`）

能区分开关，但成功后仍看不到目标地址 / Live / stack / 失败原因，Force 和普通 Enable 也仍不够清楚。Issue 已否。

### 4.3 不采用：System 页自己发 `OperationRecordMsg`

会绕开 `recordActionOutcome`，账本所有权拆散，其它动作更容易漏记。壳层不再是「完成即记账」的单一路径。

## 5. 数据与记账

### 5.1 `OperationRecord`

`internal/tui/ui/monitor.go`：

```go
type OperationRecord struct {
    ID     string
    Object string
    Action string // 可空：Enable / Disable / Force enable
    Detail string // 可空：已消毒的短摘要
    State  string
    At     time.Time
}
```

其它动作两个新字段保持空，左侧渲染回退为今天的 `Object · State`。`At` 已由 `recordActionOutcome` 写成 `time.Now()`，本次不改写入，只补渲染。`At.IsZero()` 的记录（测试夹具或尚未完成的条目）不画时间列。

### 5.2 动作名

来自 `intent.Action`，用现有词表，不从结果 kind 再译一遍：

| `ui.Action` | 账本 Action |
|---|---|
| `ActionEnableSystemProxy` / `ActionEnableTun` | `Enable`（`EnableSystemProxyLabel` / `EnableTunLabel`） |
| `ActionDisableSystemProxy` / `ActionDisableTun` | `Disable` |
| `ActionForceSystemProxy` / `ActionForceTun` | `Force enable`（复用 `ForceEnableSystemProxyLabel`） |

只有这六种动作填写 `Action` / `Detail`。其它 intent 保持空字段。

### 5.3 结果窄接口

`systemProxyActionResultMsg` / `tunActionResultMsg` 未导出，壳层不能 type-switch 具体类型。结果消息增加访问器，壳层用未导出接口断言：

```go
type proxyOutcome interface {
    Err() error
    ProxyStatus() protocol.SystemProxyStatus
}

type tunOutcome interface {
    Err() error
    TunStatus() protocol.TunStatus
}
```

`systemProxyActionResultMsg.ProxyStatus()` 返回已有的 `status` 字段；`tunActionResultMsg.TunStatus()` 同理。`Err()` 已存在。

填表逻辑放在纯函数（建议新文件 `internal/tui/operation_ledger.go`），由 `recordActionOutcome` 调用。不把格式化散进 `model.go` 的 Update 分支。

### 5.4 冲突门控记 Failed

`CodeSystemProxyConflict` / `CodeTunConflict` 的 `Err() != nil`，**记为 Failed**。System 页随后弹出二次确认的行为不变；Force 是另一条账。

这与 issue 示例一致：Enable 被挡住是一次失败，Force 是另一次操作。若用户从 status 预检直接走 Force（`confirmForceSystemProxyFromStatus` / `confirmForceTunFromStatus`），账本只出现 Force 这一条。

## 6. 文案

摘要用短英文，和现有 TUI 词表一致。新增常量放进 `internal/tui/ui/strings.go`，测试断言常量而不是裸字符串。

### 6.1 成功

| 动作 | Detail | 字段来源 |
|---|---|---|
| SysProxy Enable | `127.0.0.1:7890 · Owned` | `observed.server`，空则 `target`；所有权用 `PortOwned`（`Owned`） |
| SysProxy Force enable | `overwrote foreign → 127.0.0.1:7890` | 写入后的 server / target |
| SysProxy Disable | `cleared` | 固定短句 |
| TUN Enable / Force enable | `gVisor · Live On` | `status.Stack` + `LiveLabel` + `OnLabel`/`OffLabel` |
| TUN Disable | `Live Off` | `LiveLabel` + `OffLabel` |

地址优先 `Observed.Server`，空则 `Target`。空地址时仍输出所有权 / Live，不编造 IP。

`OverviewValueOwned` 是小写 `owned`（状态徽章）。账本成功摘要用 `PortOwned`（`Owned`），与 issue 示例和端口行一致。

TUN stack 原样使用 DTO 的 `Stack`（如 `gVisor` / `system` / `mixed`）。Live 由 `LiveEnable` 决定：`true` → `Live On`，`false` 或 nil 且动作为 Disable → `Live Off`。Enable 成功但 `LiveEnable` 不是 true 时，Detail 仍写实际 Live 状态，不假装 On。

### 6.2 失败

只映射已有错误码 / `last_error` / 冲突摘要。查找顺序：

1. `errors.As` 得到 `protocol.APIError`，按 Code 映射短句。
2. 否则若 status 带非空 `LastError`，用它（daemon 已消毒）。
3. 否则域 fallback：`SystemProxyActionFailed` / `TunActionFailed`。

| 信号 | Detail |
|---|---|
| `system_proxy_conflict` | `foreign proxy in use` |
| `system_proxy_not_owned` | `foreign proxy in use`（短句；长文案 `SystemProxyNotOwnedMessage` 仍留在 System 页） |
| `tun_conflict` | `other TUN in use (<name>)`；`<name>` 取 `Details["other_tun_interfaces"]` 的第一项，没有则省略括号 |
| `permission_denied` | `not elevated`（复用 `ServiceNotElevatedLabel`） |
| `revision_conflict` | `SystemChangedMessage` |
| 其它带 Message 的 `APIError` | 已有 `APIError.Message`（协议安全文案） |

禁止：

- `err.Error()` 作为非 APIError 的展示文本（避免 registry / Win32 原文）。
- 失败路径写入 `current_server` / `target_server`。
- 订阅 URL、secret、controller 地址。

### 6.3 完整行示例

左侧动作摘要，右侧时钟。空格为示意，实际按卡片 `textWidth` 把时间顶到右缘：

```
System proxy · Enable · 127.0.0.1:7890 · Owned · Succeeded        14:32:01
System proxy · Force enable · overwrote foreign → 127.0.0.1:7890 · Succeeded  14:32:08
System proxy · Disable · cleared · Succeeded                      14:33:12
System proxy · Enable · Failed · foreign proxy in use             14:32:05
TUN · Enable · gVisor · Live On · Succeeded                       14:40:00
TUN · Force enable · gVisor · Live On · Succeeded                 14:40:11
TUN · Disable · Live Off · Succeeded                              14:41:02
TUN · Enable · Failed · other TUN in use (Meta Tunnel)            14:40:03
mihomo · Succeeded                                                14:12:44
```

最后一行是未 enrichment 的其它动作：左侧仍是 `Object · State`，时间同样右对齐。

## 7. Overview 渲染与窄卡

当前 `View()`：

```go
fmt.Sprintf("%s · %s", valueOr(operation.Object, operation.ID), state)
```

改为先拼左侧摘要，再把时间右对齐接到同一行。Recent operations 是全宽卡，行宽预算为 `SectionTextWidth(fullCardInner())`。

`RenderBorderedSection` 对整行 `MaxWidth` 裁尾。State 若在行尾、或时间被裁成半截，都会再踩 #100。因此**不能**先拼好长字符串再交给卡片裁切。

### 7.1 时间列

- 格式：`operation.At.Local().Format("15:04:05")`。与 Logs / Connections 同一套本地时分秒；同分钟内的 Enable 失败再 Force 成功必须能分开。
- 不写日期、不写相对时间、不写时区。会话账本跨日极少，省宽度。
- `At.IsZero()`：不渲染时间，左侧摘要使用整行宽度。
- 布局：`left + spaces + time`，使 `lipgloss.Width(line) == textWidth`。时间贴卡片内容区右缘。用显示宽度补空格，避免 ANSI 色码把列挤歪。
- 左侧摘要与时间之间至少 1 个空格；空不出 1 格时先丢时间列，不要让时间和 State 粘在一起。

### 7.2 优先级（高 → 低）

1. Action（本 issue 保证可见）
2. State（Succeeded / Failed）
3. Object（`System proxy` / `TUN`）
4. Time（右对齐时钟；窄于「时间宽度 + 1 空格」时整列丢掉）
5. Detail（最先丢掉或截断）

组行规则：

1. 宽度够：左侧 `Object · Action · Detail · State`，右侧 `15:04:05`
2. 不够：丢掉 / 截断 Detail，时间仍右对齐
3. 再不够：丢掉时间列 → `Object · Action · State`
4. 还不够：截 Object（末尾省略），保留 Action + State
5. `Action` 为空（其它操作）：左侧 `Object · State`，同样右对齐时间

失败时 Detail 跟在 State 后面（`Object · Action · Failed · reason`），与 issue 示例一致；成功时 Detail 在 State 前面（`Object · Action · Detail · Succeeded`）。

只给 SysProxy / TUN 走 Action/Detail 拼装；其它记录不引入空 ` · · `。时间列对所有记录生效。

## 8. 涉及文件

- 修改：`internal/tui/ui/monitor.go` — `OperationRecord` 加字段
- 修改：`internal/tui/ui/strings.go` — 账本短句常量
- 新增：`internal/tui/operation_ledger.go` — 从 intent + 结果填 Action/Detail
- 新增：`internal/tui/operation_ledger_test.go` — 表驱动填表测试
- 修改：`internal/tui/model.go` — `recordActionOutcome` 调用填表函数
- 修改：`internal/tui/model_test.go` — 更新 `TestActionCompletedRecordsOperations`；补 SysProxy/TUN 记账
- 修改：`internal/tui/pages/system/model.go` — 给结果消息加 `ProxyStatus` / `TunStatus`
- 修改：`internal/tui/pages/overview/model.go` — 渲染、右对齐时钟、窄宽优先级
- 修改：`internal/tui/pages/overview/model_test.go` — 渲染、时钟右对齐、窄宽断言

不改 `internal/control/protocol`、`internal/runtime`、CLI、Web。

## 9. 测试计划

行为变更走 Red–Green–Refactor。先写失败测试，再写最小实现。

1. **通用记账回归**（`TestActionCompletedRecordsOperations`）
   - 非 SysProxy/TUN 动作：`Action` 与 `Detail` 仍为空；`Object` / `State` / `At` 行为不变。
   - 空 Object 仍回退 Title。
   - Overview 仍能渲染该记录。

2. **填表纯函数**（`operation_ledger_test.go`，表驱动）
   - SysProxy Enable 成功：Action=`Enable`，Detail 含 server + `Owned`，State=Succeeded。
   - SysProxy Force 成功：Action=`Force enable`，Detail 含 `overwrote foreign →`。
   - SysProxy Disable 成功：Action=`Disable`，Detail=`cleared`。
   - SysProxy Enable + `CodeSystemProxyConflict`：State=Failed，Detail=`foreign proxy in use`。
   - SysProxy Disable + `CodeSystemProxyNotOwned`：Failed + 同一短句。
   - TUN Enable 成功：stack + `Live On`。
   - TUN Disable 成功：`Live Off`。
   - TUN Enable + `CodeTunConflict`（Details 含 `Meta Tunnel`）：`other TUN in use (Meta Tunnel)`。
   - `CodePermissionDenied`：`not elevated`。
   - 非 APIError：不出现 `err.Error()` 原文，用域 fallback。

3. **壳层集成**
   - `actionCompletedMsg` 携带真实 `systemProxyActionResultMsg` / `tunActionResultMsg`（经 system 页 Execute 路径或直接构造后由壳记录）。
   - 冲突二次确认仍弹出；账本先记 Failed，Force 成功后再记第二条。

4. **Overview 渲染**
   - 宽卡：示例行完整出现，行尾为 `15:04:05`。
   - 固定宽度下时间贴右缘：同一卡内多行的时钟列垂直对齐（用 `lipgloss.Width` 量，不是 `len`）。
   - `At` 为零：该行不出现时钟，也不为它对齐出空白列。
   - 窄宽度：Action 与 Succeeded/Failed 仍可见；不得只剩对象名。时间可被丢掉。
   - 无 Action 的旧记录左侧仍渲染 `Object · State`，有 `At` 时右侧同样有时钟。

不访问公网、不读真实用户目录、不依赖已安装 mihomo。

## 10. 风险

- 窄卡截断必须在拼行时做，不能依赖 `MaxWidth` 裁尾，否则会再踩 #100。时间列用显示宽度右对齐，避免色码把时钟挤到下一列。
- 冲突 Failed 会在二次确认弹出前写入账本。这是有意的：Enable 确实没做成。若产品后来希望「未终态不入账」，应另开 issue，不在本设计改门控。
- `OperationRecord` 是值类型，测试与 Overview snapshot 拷贝已按切片复制；加导出字段是向后兼容的加法。
- System 徽章本次不动，避免把失败反馈范围扩到 Network 行文案。

## 11. 关键决策

1. **账本仍由壳层 `recordActionOutcome` 写入**，不让 System 页直接发账本消息。
2. **只 enrichment SysProxy / TUN**；其它动作字段留空，渲染保持原样。
3. **动作名来自 `intent.Action`**，不依赖未导出的 page kind。
4. **结果通过窄接口取 status**，避免壳层依赖 page 未导出类型名。
5. **冲突门控记 Failed**，Force 另记一条。
6. **窄卡先丢 Detail，再丢时间列**，保证 Action + 成败可见。
7. **时间已记录、只补渲染**：`At` 用本地 `15:04:05`，贴卡片内容右缘对齐；所有账本行同一套时钟列。
8. **System sticky 徽章不做**，保持本 PR 只碰 Overview 账本。
9. **不改 `/v1` 与 daemon**。

## 12. PR 计划

单 PR，指向 `dev`：

- 标题：`feat(tui): Overview 账本展示 SysProxy/TUN 动作与结果`
- 关闭：#159
- 不修改 `CHANGELOG.md`
- Conventional Commit 摘要用中文；需要时再拆测试与实现 commit，但一个可审查意图即可
- 不在 `main` / `dev` 上直接提交
