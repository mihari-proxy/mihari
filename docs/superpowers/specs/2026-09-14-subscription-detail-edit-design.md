# 订阅详情内编辑与 URL/缓存分离

后续布局调整：详情与添加的紧凑分组、焦点样式、滚动和底部提示采用 [2026-09-15 紧凑弹窗执行计划](../plans/2026-09-15-subscription-compact-dialog.md)；本文的订阅业务与保存状态语义继续适用。

2026-09-21 修订（[PR #300](https://github.com/mihari-proxy/mihari/pull/300)，用户已确认）：详情页新增立即执行的 Enabled 与 InUse 操作，替代原先必须返回列表的限制；设置草稿仍通过 Save 提交。Auto refresh、Mode、Enabled、InUse 仅高亮选项值；Interval 的支持单位移至输入值旁，以浅灰色显示。以下对应决策及 §7.2 已同步更新。

日期：2026-09-14
状态：用户已确认共同理解，grilling 完成，设计已定稿；后续授权实施已完成，验证见执行文档。
分支：`feat/tui-subscription-detail-edit`
基线：最初 `origin/dev` @ `6221d69`；开发前已 rebase 到 `a80a9b6`（2026-09-14）。
工作目录：`.worktrees/feat-tui-subscription-detail-edit`

设计定稿后，用户已授权 rebase dev 并实施本规格。本文已确认新增持久字段、公开布尔字段及 URL reveal 权限范围；实现与验证记录见 [执行文档](../plans/2026-09-14-subscription-detail-edit.md)。后续提交与 PR 按用户授权执行。

## 1. 目标

把 Subscriptions 页「只读详情」和「独立 edit form」合成一张 overlay：查看一条订阅的运行状态，并在同一张里改 Name、URL、Interval、Auto refresh、Mode。

同时把「订阅 URL」和「已缓存 yaml」分成两个状态。改 URL 默认不立刻拉新配置；列表用 Status 词表达来源变化、过期和是否正在使用。

不新增 CLI 命令、Web 面板编辑或 TUI `global-interval` 设置。共享 daemon 的 URL/interval 更新语义同时作用于既有 CLI 请求；协议 `proxy_mode` 与 CLI `--proxy` 不改名。

**TUI 的全部可见文字使用英文**，包括标题、字段、状态、帮助、按钮、错误、进度、确认和结果提示。本文中文仅用于解释设计，不直接作为 TUI 文案。

## 2. 已确认决策

| ID | 决策 | 选择 |
| --- | --- | --- |
| Q1 | 可改的「更新时间」 | 只改本条 `interval`；改 interval 后下次自动拉从**现在**起算 |
| Q2 | 详情与编辑 | 合成一张 overlay |
| Q3 / Q11 / Q37 | 可写字段 | Name、URL、Interval、Auto refresh、Mode 进草稿 + Save。Enabled 与 InUse 在详情的独立操作区按 Enter 立即执行；Refresh 仍回列表 |
| Q6 | 改 interval 的调度 | 复位到 now + 新 interval；不改 `updated_at`（仍是上次成功拉取缓存的时间） |
| Q7 / Q16 / Q32 | URL 回显 | 本地控制 `GET /v1/subscriptions/{id}/url`，供 TUI 回显；打开详情即预填；失败则空框，仍可粘贴。list/show 仍不带 URL。调用权限见 G1 |
| Q10 / Q29 | Mode | 列表头 Proxy→**Mode**；值仍是 DIRECT/PROXY/AUTO；footer `p` = cycle mode；协议字段仍是 `proxy_mode` |
| Q12 | 改 URL 且该条 InUse | 保持 InUse；**刷新成功后**才用新 yaml 替换运行配置。禁止 `fillDefaults` 把别的订阅顶上来 |
| Q20 / Q21 / Q23 | 改 URL 后何时拉 | 直接保存，无确认。不立刻拉。auto-refresh 开则从现在再计一个 interval 后用**新 URL**拉；也可手动 `r` |
| Q22 | Outdated 时 Use | 仍加载**现有**缓存 |
| Q19 / Q24 / Q26 | Status | Load 列改名为 Status。来源不一致时 Status=**Outdated**，盖住 Live/Cached/Expired。Stale 拆成 **Expired** 与 **Need refresh** |
| Q25 / Q28 | In use | 列表头 **InUse**；详情 `In use: yes/no`。`●` 在 InUse 列居中 |
| Q27 | 宽度 | Status `MinWidth: 12`；窄屏仍先丢 Next/Last update |
| Q30 | 列表键 | 只留 **Enter** 打开详情；**去掉 `e`** |
| Q31 / Q33 / Q34 / Q35 | 详情交互 | 显式 Save；方向键/Tab 移动；**仅 Save 上 Enter 提交设置草稿**，Enabled/InUse 操作项 Enter 立即执行；输入框 Enter=下一项；提交前 Esc 丢草稿并关；保存中与结果未知的例外见 G8，立即操作见 §7.2 |
| Q36 | 添加 | 同一套导航 + Save；字段只有 Name、URL、Mode（现有 POST，不扩展）。add 仍立刻拉一次 |
| Q18 | Enabled/Active 文案教育 | 增加操作项就地提示：停用会清除 InUse，启用不会自动选中；Use 需要已启用且有有效缓存。其他独立术语帮助稿仍搁置 |

### 2.1 本次 grilling 复核

以下 G 编号对应本次对话的 Q 编号，避免与上表历史编号混淆；发生差异时以下决策优先。

| ID | 决策 | 已确认选择 |
| --- | --- | --- |
| G1 | reveal 权限 | 所有通过现有本地控制认证的用户均可读取完整 URL；接受 Unix 所有本机用户可认证的现有权限范围，不新增 owner/admin 校验 |
| G2 | 混用版本 | TUI 与 daemon 必须同步升级；不增加本功能能力标记，不保证混用版本兼容 |
| G3 | 数据降级 | 不支持旧二进制直接读取新 catalog；升级前备份，降级时恢复兼容备份；不新增降级存储兼容方案 |
| G4 / G4.1 | interval 与 Status | 本条 interval 实际变化后立即标记 Expired，直到刷新成功；操作中、Disabled、Failed、Missing、Outdated 仍优先；Next 从修改时刻按新 interval 重算 |
| G5 | 旧源错误 | URL 实际变化时清 last_error；旧请求的迟到失败不得写回。仅改其他字段不清错误 |
| G6 | reveal 竞态 | 仅同一弹层、同一订阅且 URL 未被用户编辑时回填；关闭或切换后忽略迟到结果 |
| G6.1 | 空 URL | 读取失败且未编辑则 PATCH 省略 URL；主动清空则报字段错误并阻止保存。添加始终要求有效 URL |
| G7 / G7.1 / G7.2 | revision 冲突 | 直接二次确认是否覆盖，不做草稿恢复或逐字段处理；确认后只提交本次实际修改字段，使用最新 revision，再次冲突则再次确认 |
| G8 | 保存中 | 显示 Saving，禁用重复提交，Esc 暂不关闭；请求结束后恢复相应操作 |
| G8.1 | 明确结果 | 保存成功关闭弹层，列表保持选中并更新；明确失败留在弹层显示错误，可修改后重试 |
| G8.2 | 结果未知 | 暂停直接重提，重连后查询操作及订阅状态；允许 Esc 关闭，但不表示撤销。核对仍不确定时按 G12 允许用户二次确认后重新提交 |
| G9 | 小终端 | 72×22 下正文随焦点滚动，状态区与字段共用区域；当前焦点可见，底部提示固定，URL 单行横向滚动 |
| G10 | 自动刷新关闭 | Next 显示 Manual；interval 变化仍保存调度起点，并按 G4 标记 Expired |
| G11 | 添加后首次刷新失败 | 返回列表并选中新条目；提示已添加但首次刷新失败，按 r 重试，不再次 POST |
| G12 | 无法确定结果后的重提 | 用户可重新提交，但必须二次确认；说明上次可能已成功，添加场景可能重复创建；不新增持久化操作结果或恢复协议 |
| 语言补充 | 语言 | TUI 全部可见文字使用英文 |

## 3. 当前仓库事实

- 列表 Enter 打开只读 overlay（Name/Status/AutoRefresh/Load/Traffic/Cache/Interval/Last/Next/Last error）；Enter/Esc 关闭。`e` 另开 form：Name、URL（空白=保留）、Interval、Auto refresh。见 `internal/tui/pages/subscriptions/model.go`、`form.go`。
- 帮助规格：Subscriptions 的 Detail mode **只有 close**；Form 为 Tab / Enter next-or-save / Esc。footer 含 `e edit`、`p proxy`。见 `docs/superpowers/specs/2026-08-31-tui-help-page-design.md`。
- `protocol.Subscription` **无 URL 字段**。架构：URL 只存在 daemon 私有目录，list/show 与常规错误省略。见 `docs/architecture.md`、`docs/commands.md`。
- 改 URL 时 `SetSubscription` 将 `generation` 置 0、清 `updated_at` / ETag / LastModified；若该条是 `ActiveID` 则清空。随后 `Normalize`+`fillDefaults` 可能把**另一条**已缓存订阅设为 active 并加载其配置。见 `internal/runtime/subscription.go`、`internal/subscription/catalog.go`。
- 有效间隔：本条 `interval` 非空则用它，否则 `global-interval`（默认 `12h`）。scheduler 用 `updated_at + interval + jitter`；`updated_at` 为零则立刻到期。
- Load 优先级：操作中 → Disabled → Failed → Missing → Stale → Live（active 且未过期）→ Cached。**Live ≠ InUse**：InUse 且已过 interval 时当前显示 Stale，`●` 仍在。
- 表列 `PadCell` 只有左/右对齐，无居中。Active 列 `MinWidth: 6` 左对齐，故 `●` 贴左、不在标题正中。见 `internal/tui/ui/table.go`。
- `POST /v1/subscriptions` 只要 name、url、可选 `proxy_mode`。新条目 `AutoRefresh=true`、`interval` 空。add 成功后立刻 Refresh；失败保留条目和 `last_error`。
- `PATCH` 无 `enabled`；启用走 `PUT .../enabled`。
- Unix 默认 B/control.token 为 root0644、B/control.sock 为 root0666，所有本机用户可按既有方式认证。reveal 沿用该权限属于用户明确接受的 URL 读取边界扩展，不能称为技术上限定 TUI 调用。见 `docs/unix-layout.md`、`docs/architecture.md`。
- Catalog 严格 YAML 解码会使旧二进制拒绝新增字段；新代码补默认只解决正向读取旧目录，不保证降级。
- 现有刷新成功提交检查 profile version，但下载失败的错误写回还需补齐同样的陈旧结果保护；配置准备/apply 失败与回滚失败的处理也须按 §5.2 明确覆盖，不把目标行为当作现状。

## 4. 列表与 Status

### 4.1 列

| 现在 | 改为 | 备注 |
| --- | --- | --- |
| Active | **InUse** | `●` 水平居中；`TableColumn` 增加 `AlignCenter` |
| State | **Enabled** | 值仍 Enabled/Disabled |
| Load | **Status** | `MinWidth: 12` |
| Proxy | **Mode** | 值仍 DIRECT / PROXY / AUTO |

丢列顺序不变：先丢 Next update、Last update。Name / InUse / Enabled / Status 尽量保留。

### 4.2 Status 词与优先级

从上到下取第一个命中：

1. Fetching / Applying / Working（本地 pending）
2. Disabled（`enabled=false`）
3. Failed（`last_error` 非空）
4. Missing（无有效缓存，`generation==0` / `cached==false`）
5. **Outdated**（`cache_outdated==true`）
6. **Expired**：本条 interval 自上次成功刷新后发生变化，不论缓存年龄和 auto-refresh 状态
7. **Expired**：无上述 interval 变更标记、同源、已过 interval、`auto_refresh==true`
8. **Need refresh**：无上述 interval 变更标记、同源、已过 interval、`auto_refresh==false`
9. **Live**：InUse 且同源且未过期
10. **Cached**：非 InUse、同源、未过期

**Outdated**：磁盘上的 yaml 来自**上一份 URL**，当前订阅 URL 已变。可与 InUse 同时成立（内核仍跑旧 yaml）。列表靠 InUse `●` 区分「正在用的过时缓存」和「后台过时缓存」。

**Expired**：interval 变化后立即成立并保持到成功刷新，或同源缓存自然超过 interval 且开启自动刷新。Expired 不等于立即自动拉取；scheduler 仍按 Next 到期时间执行，仅在 enabled 且 auto-refresh 开启时执行。

**Need refresh**：没有待成功刷新清除的 interval 变更标记，同一 URL 的缓存自然超过 interval，auto-refresh 关，只能手动 `r`。

例如刚成功拉取后把 interval 从 12h 改成 6h：立即显示 Expired，Next 约 6h；若 auto-refresh 关闭则显示 Expired + Manual。此变化不修改 `updated_at`，不删除缓存、不改变 InUse，不立即拉取。该标记必须跨 daemon/TUI 重启保留，失败刷新及仅切换 Use 不清除；更高优先级状态暂时遮盖它也不清除。

改 URL 后、下一次针对新 URL 的刷新成功前，Next update：auto-refresh 开则显示 `schedule-from + interval` 的相对时间；关则 **Manual**。不要在 Outdated 时继续显示「按旧 `updated_at` 还剩 x 小时」。

## 5. URL 与缓存分离

Catalog 里一条订阅有：

- **`url`**：下次拉取地址（秘密）
- **`cache-url`**：生成当前 yaml 的地址（秘密，不进 list/show）
- **`schedule-from`**：URL 或 interval 变化后的调度起点
- **`interval-refresh-required`**：interval 变化后等待成功刷新的持久布尔标记，默认 false；仅 interval 实际变化置 true，刷新成功清除
- yaml 文件仍按 profile ID 存放；`generation>0` 表示缓存可用

`cache_outdated`（公开布尔）= 有缓存且 `url != cache-url`。

旧目录没有 `cache-url`：Load/`fillDefaults` 在 `generation>0` 且 `cache-url` 为空时填成当前 `url`，避免全部变成 Outdated。

`interval-refresh-required` 是实现 G4 所需、已获用户最终确认的新增持久字段与公开布尔。不能用 `schedule-from != 0` 代替：仅 URL 从 A 改 B 再改回 A，会留下调度起点却没有发生 interval 变化，不能因此强制 Expired。interval 改回原字符串仍保持此标记，直到成功刷新。

### 5.1 改 URL（PATCH `url` 且字符串变化）

1. 写入新 `url`。
2. **保留** `generation`、yaml、`updated_at`、InUse（`ActiveID`）。
3. **清空** ETag、LastModified（校验头绑定旧源；带去请求新 URL 可能错误 304）。
4. **不改** `cache-url`。
5. 写入 `schedule-from = now`（与改 interval 相同复位）。
6. 不调用 Refresh；不跑 `fillDefaults` 另选 active。
7. 清空旧 URL 的 `last_error`；仅改其他字段不清空。旧请求的成功结果与失败写回均需校验其 profile 身份/version，不能污染新 URL 的状态。

非 InUse 的条目改 URL：不得改 `ActiveID`。
InUse 的条目改 URL：保持该 ID 为 active，内核继续跑旧 yaml，直到一次**新 URL** 刷新成功。

### 5.2 刷新

`r` / scheduler / add 后的首次拉取：始终请求**当前 `url`**。

成功：更新 yaml；`cache-url = url`；`generation++`（或合法 not-modified 时保持）；`updated_at=now`；清 `last_error`；**清 `schedule-from` 与 interval 变更过期标记**。若该条 InUse，成功包括新文档通过校验、运行配置应用及相关提交完成，不能仅因下载成功就清除这些状态。

失败：下载、配置准备/校验、应用或提交失败且成功恢复旧状态时，保留旧缓存、`cache-url`、`updated_at`、调度与 interval 过期标记，InUse 与旧运行配置不变；当前有效刷新执行的安全 `last_error` 可见，Status 按优先级显示 Failed。旧请求的陈旧成功与错误结果均不得写回；正常取消不伪装成新的订阅故障。

若回滚自身失败，沿用既有 degraded 与 mutation 拒绝机制，不能声称旧配置已完整恢复；错误与受控诊断继续遵循现有边界。缓存、catalog、运行配置、generation 以及新字段一并纳入提交/回滚测试。

Use（`u`）：仍要求 enabled 且 `generation>0`。Outdated 时加载**现有**缓存，不隐含刷新。

### 5.3 调度

提交成功后唤醒 scheduler 重新计算等待；失败和同一操作缓存命中不发送通知。

`schedule-from` 非零：下次到期 = `schedule-from + EffectiveInterval + jitter`。
否则：`updated_at` 为零则立刻到期，否则 `updated_at + interval + jitter`。

写入 `schedule-from` 的时机：本条 **`interval` 字符串变化**（含空↔显式），或 **`url` 字符串变化**。改名、Mode、Enabled、Auto refresh、未改 URL 的保存不得复位。

仅 interval 实际变化设置独立的“等待成功刷新”的过期标记；仅 URL 变化不设置该标记。标记用于 Status，不改变 scheduler 到期公式。一次 PATCH 同时改 interval 与 URL，仅取一次提交时刻作为调度起点，并同时保留两种状态的含义。

成功刷新后清除 `schedule-from` 及 interval 变更过期标记，之后回到 `updated_at + interval`。

auto-refresh 关闭：scheduler 跳过该条，Next 始终显示 Manual。Status 仍按 §4.2 判断，可能为 Expired、Need refresh、Outdated 或更高优先级状态。重新开启 auto-refresh 不复位已有调度起点。

### 5.4 升级与降级

旧目录读取时补齐新字段默认值，不把历史缓存一律标成 Outdated 或 interval 变更过期。保持严格 YAML 解码，不为兼容旧程序放宽校验。

升级前备份兼容的业务数据。新 catalog 写入后不支持直接运行旧二进制；降级必须恢复相应兼容备份，恢复会丢失备份之后的业务变更。实施文档须说明备份/恢复范围与停机条件，本任务不执行真实数据备份、迁移或系统服务操作。

## 6. 协议

保持 `/v1` envelope、错误码、退出码。下列为**加性**字段与一条新只读路由，不升协议版本。

### 6.1 list/show DTO

`protocol.Subscription` 增加：

```go
CacheOutdated           bool      `json:"cache_outdated,omitempty"`
ScheduleFrom            time.Time `json:"schedule_from,omitempty"`
IntervalRefreshRequired bool      `json:"interval_refresh_required,omitempty"`
```

仍**不**包含 `url` 或 `cache-url`。订阅服务的公开投影与协议 DTO 同步携带上述字段；TUI 按 `cache_outdated`、`interval_refresh_required`、缓存年龄和优先级计算 Status，用 `schedule_from` 计算 Next。布尔零值缺省 false；时间零值的编码遵循仓库现有约定，不依赖 omitempty 一定省略 time.Time 零值。

### 6.2 URL reveal

```
GET /v1/subscriptions/{id}/url
```

仅本地控制平面（named pipe / UDS），沿用现有认证；**所有通过该认证的本机用户均可读取完整 URL**，不新增 owner/admin 校验，不按调用客户端是否为 TUI 限权，不向 Web gateway 挂载此路由。响应示例：`{"schema":"mihari/v1","url":"https://..."}`。

不进入 list、默认 show、事件、JSON envelope 的 `Message`/`Details`。TUI 失败时输入框留空，错误文案不得带 token。

CLI `sub show` 默认仍不打印 URL。本次不增加 `--reveal-url`。

### 6.3 既有 mutation

- PATCH 字段集合不变（含 `interval`、`url`、`auto_refresh`、`proxy_mode`）。语义按 §5.1 改，不再因改 URL 清 generation / 踢 active。
- POST add 不变。Save 提交 Name、URL、Mode。成功后仍立刻 Refresh。
- PUT enabled、PUT active、POST refresh 不变。

`global-interval` 仍仅 CLI `sub set --global-interval`，TUI 不暴露。

### 6.4 版本配套

本功能要求 TUI 与 daemon 同步升级，不新增能力标记，不保证新旧版本混用。reveal 读取失败后的留空编辑规则只描述受支持的配套版本，不承诺旧 daemon 已具备保留缓存与 InUse 的 URL 更新语义。发布说明与相关 README/命令文档同步说明行为与兼容范围。

## 7. TUI 交互

### 7.1 列表

- **Enter**：打开该条详情 overlay（不再打开只读详情）。
- **去掉 `e`**。
- **Space**：enable/disable（立刻 PUT enabled）。
- **`p`**：立刻循环 Mode（PATCH `proxy_mode`），footer「cycle mode」。
- **`r` / Ctrl+R / `u` / `d` / `a`**：行为同现网，仅当 overlay 关闭。

### 7.2 详情 overlay（已有订阅）

上半只读：In use、Status、Enabled、Traffic、Cache、Last update、Next update、Last error。
Outdated 时必须同时有 **In use: yes/no**，不能只靠列表 `●`。

下半草稿：Name、URL、Interval、Auto refresh、Mode。
Auto refresh、Mode 为可聚焦循环行（左/右或 Space 改草稿，**Save 才写出**）。
Interval 在输入值旁用浅灰色显示 `ns/us/ms/s/m/h`，不再另占一行说明；留空仍继承全局间隔。

设置区下方为 **Actions · Apply immediately**：

- **Enabled**：`[ Enable ]` / `[ Disable ]`，Enter 通过既有启用接口立即执行。停用当前订阅同时清除 InUse；重新启用不自动选中。
- **InUse**：已使用时显示 `In use`；未使用时提供 `[ Use this subscription ]`。禁用或无有效缓存时分别显示 `Enable first` / `Refresh first`，不发送 Use 请求。不新增独立取消 InUse 的接口。
- 操作成功后保持详情打开及设置草稿；只有本次操作自身推进的 revision 可用于后续 Save，外部变更仍触发既有冲突处理。
- 执行中阻止重复操作及 Save，允许 PgUp/PgDn 滚动和 Esc 关闭；关闭不取消已发出的请求。明确失败在当前详情显示；结果未知时阻止直接重试，查询目录供核对，提示关闭后重新打开详情检查。
- 迟到结果仍进入共享诊断；成功结果仅在 revision 不落后时更新目录。已关闭详情的结果不得设置或清空当前页面错误，也不得借错误后的 reload 清空新页面提示。

Auto refresh、Mode、Enabled、InUse 仅对选项值反色，标签和行尾空白不高亮。
底行可聚焦 **Save**。

打开时请求 reveal，预填 URL。URL 读取期间允许用户输入；只有同一弹层实例、同一订阅且该字段尚未被用户编辑时才回填。关闭、重新打开、切换订阅后的旧结果全部忽略；用户曾编辑后又删回空值也不恢复自动回填。

读取失败：框空，仍可粘贴；不把 reveal 响应 URL 写入 toast/日志。读取尚未完成或失败且用户未编辑 URL 时，Save 省略 URL 字段，保留原值；用户主动清空则显示 `URL is required.` 并阻止保存。读取成功且用户未改 URL 时同样省略 URL，不把回显动作当作修改。

导航：↑/↓、Tab / Shift+Tab 在设置字段、操作项和 Save 之间移动。文本框内输入即改草稿。设置字段 Enter = 下一项。**只有焦点在 Save 上时 Enter 才 PATCH 设置草稿**（一次提交相对编辑基线实际变化的 Name/URL/Interval/Auto refresh/Mode）；Enabled/InUse 操作项的 Enter 立即调用各自既有接口，不提交草稿。提交前 Esc 关闭并丢草稿，已生效的 Enabled/InUse 操作不回滚。

overlay 打开期间：`r`、`u`、全局 Space **不是**快捷键。刷新仍需先 Esc 返回列表；Use 和 Enabled 可聚焦详情内对应操作项后按 Enter 执行。焦点在 Auto refresh/Mode 行时 Space 只循环该草稿字段。

列表 `p` 与详情草稿 Mode：详情未打开时 `p` 立刻 PATCH。详情打开时不将 `p` 解释成列表快捷键；文本框中的 `p`、`r`、`u` 等正常作为字符输入。详情里改 Mode 走 Save。

保存、冲突与结果核对见 §7.5–§7.7。Esc 的效果取决于当前状态，不以“丢草稿”描述已经发出的请求。

### 7.3 添加 overlay（`a`）

同一套 ↑/↓/Tab + Save + Esc。字段仅 **Name、URL、Mode**（Mode 默认 DIRECT）。无状态块、不 reveal。Save 走现有 add（含立刻 Refresh）。Interval / Auto refresh 用默认值，建完再 Enter 进详情改。

添加完全成功后返回列表并选中新条目。条目已创建但首次刷新失败时也返回列表并选中新条目，提示 `Subscription added, but the initial refresh failed. Press r to retry.`，状态显示 Failed；后续使用 `r` 刷新同一 ID，不再次 POST 添加。

### 7.4 帮助与 footer

Subscriptions **不再使用 ModeDetail**（Enter 不再「close details」）。

列表 footer 去掉 `e edit`；`p proxy` 改为 `p mode`（或「cycle mode」，与 keymap Label 一致）。

Form mode（添加与详情共用）改为：

| Display | Keys | Label |
| --- | --- | --- |
| `↑/↓` | `up`,`down` | move between fields |
| `Tab` / `Shift+Tab` | `tab`,`shift+tab` | move between fields |
| `←/→` / `Space` | `left`,`right`,`space` | cycle Auto refresh or Mode when focused |
| `Enter` | `enter` | next field, save on Save, or apply immediately on Enabled/InUse |
| `Esc` | `esc` | cancel before saving |

循环键仅在 Auto refresh/Mode 行进入 catalog 的 This mode；文本框焦点下 Space 是字符。实现时按焦点过滤，帮助用一行说明「on cycle fields」。

帮助与 footer 按 Editing / Saving / Conflict / Unknown 的真实按键过滤：Saving 不显示 Esc cancel 或可再次 Save；Unknown 的 Esc 仅为 close，不能标为 cancel save。所有帮助文字均为英文。

详情操作项使用独立 footer：`Enter apply now`、`Esc close`；立即操作执行中或结果未知时仅提供滚动及关闭，`Esc close (does not cancel the action)` 明确关闭不会取消操作。

### 7.5 保存生命周期

| 状态 | 显示与操作 |
| --- | --- |
| Editing | 允许编辑和导航；仅 Save 焦点 Enter 提交设置草稿，独立操作项按 §7.2 执行；Esc 丢弃本次未提交编辑并关闭 |
| Saving | 显示 `Saving...`，冻结本次提交内容，禁用重复提交与编辑；Esc 暂不关闭，不改变普通请求的超时边界 |
| 明确成功 | 关闭弹层；列表保持选中该 ID，显示最新状态 |
| 明确失败 | 留在弹层显示安全英文错误，恢复编辑；用户可修改后重试，Esc 可放弃本次输入 |
| Revision conflict | 按 §7.6 二次确认，不能自动覆盖 |
| Unknown | 按 §7.7 核对结果；不能自动或直接重提，核对仍不确定时允许用户二次确认后重新提交；Esc 可关闭，关闭不代表撤销 |

保存期间不得因切页、重连或迟到回调将另一弹层误关闭、错误回填或解除其提交锁。一次提交只由其操作 owner 接收并报告结果，列表、弹层和根 shell 保持一致。

### 7.6 冲突覆盖确认

Save 的 revision 冲突提示为 `The configuration changed while you were editing. Overwrite your changed fields?`，按钮为 `Overwrite` / `Cancel`，默认 Cancel。

Cancel 或 Esc 放弃本次待覆盖提交并返回列表，不保存可恢复草稿。该取消只针对尚未发出的覆盖提交。

不实现持久草稿恢复、字段对比或逐字段合并。确认期间仅保留这次待提交的字段和值；用户确认 Overwrite 后，读取最新 revision，只重提**相对原编辑基线实际修改过的字段**，不得把未改字段的旧回显值覆盖回去。例：只改 Name，则另一客户端的新 Mode 原样保留。

每次用户明确确认后的提交仍携带 `if_revision`，不绕过并发检查；再次冲突就再次确认，不能循环自动重试。订阅已删除时不能覆盖或重建该 ID，显示 `This subscription no longer exists.` 并返回更新后的列表。

### 7.7 结果未知

请求超时、连接断开或响应无法确定时，显示 `Save outcome unknown.`。连接中断期间补充 `Reconnect to check the subscription state.`；连接仍可用时直接执行查询。暂停直接重提，允许 Esc 关闭，并明确 `Closing does not cancel the save.`。

重连后经现有本地 client 查询 operation 及订阅状态，查询本身不得重放 mutation；根据实际证据再恢复操作。操作已结束不等于业务成功，不能把请求超时当作未保存，也不能仅因订阅数量或名称相同就判定新条目身份。

现有 `OperationStatus` 只有 operation_id 与 state，记录仅在 daemon 内存中保留且有界，不包含保存结果或新增订阅 ID；finished 仅代表 handler 已结束，unknown 也不代表未执行。响应正常到达时，Add 已注册但首次刷新失败仍返回成功结果与新 ID、LastError，应走 §7.3。

如果核对仍无法确定结果，停止等待动画，显示 `Unable to confirm the previous save.`，允许用户选择 `Submit again`。该动作必须打开二次确认，默认 `Cancel`：

- 修改已有订阅：`The previous save may have succeeded. Submit your changes again?`
- 添加订阅：`The previous save may have succeeded. Submitting again may create a duplicate subscription. Continue?`
- 确认按钮：`Submit again` / `Cancel`。

只有用户明确确认后才重新发起 mutation，使用新的 operation ID；已有订阅仍只提交原本实际修改的字段，使用最新 revision，再次冲突按 §7.6 处理。Cancel 不发送请求，保持结果未知的界面，用户仍可 Esc 返回列表。不得在后台自动重提或用模糊匹配擅自修改/删除可能已创建的条目。

查询明确显示操作仍在运行时继续等待或允许关闭，不将其当作“核对后仍无法确定”的重提入口。已核实条目创建成功但首次刷新失败时只走 §7.3 的 r 刷新，不提供再次添加。本次不新增持久化 operation outcome、新增 ID 恢复协议或跨重启操作日志。

### 7.8 小终端布局

最低 72×22 下，状态区与字段共用一个正文滚动区域；移动焦点时自动滚动，确保当前字段或 Save 完整可见，底部操作提示固定。长 URL 保持单行并横向滚动，不能把整个弹层撑宽。状态与错误文字使用英文，换行内容不得遮挡字段与 Save；终端缩放后重新约束滚动位置。

不拆成状态/编辑两个页签。正文滚动必须允许回看顶部只读状态，不能只保证向下移动到 Save。

## 8. 架构约束

- 唯一写入者仍是 daemon。TUI 只经 `internal/control/client`。
- URL 与 `cache-url` 不得出现在 list/show、事件、常规错误与 TUI 错误。沿用最新 dev 的原始错误文件日志策略，诊断 cause 可能包含 URL；不恢复已移除的脱敏逻辑。reveal 响应不得主动写入日志。此 rebase 后调整已经用户确认。URL 读取沿用现有认证的边界扩展已获 G1 明确确认。
- 长拉取仍在提交锁外；成功提交与失败状态写回都核对 profile 身份/version，mutation 同时遵守 revision 检查。
- 配置应用仍是候选 → 校验 → 原子替换 → reload → 失败回滚。改 URL 本身不切换运行配置。
- 不引入 CGO、不改 Go 版本、不改跨平台范围。
- 指向 `dev` 的实现 PR **不改** `CHANGELOG.md`。

## 9. 验证

Red–Green–Refactor。至少覆盖：

- 改 URL：generation 保留、ActiveID 不变、ETag 清空、`cache_outdated=true`、`schedule-from` 复位；已 Stale 的条目也不会立刻被 scheduler 拉新 URL。
- 非 InUse 改 URL 不改变当前 InUse。
- InUse 改 URL 后内核仍用旧 yaml；手动或到期刷新成功后才替换；失败则旧 yaml 与 InUse 保留。
- `fillDefaults` 在 generation>0 时不得因改 URL 改选别人。
- 刷新成功：`cache-url` 对齐、Outdated 消失、`schedule-from` 清除。
- Outdated 时 Use 加载旧缓存。
- 改 interval 复位调度且 `updated_at` 不变；只改名不复位。
- interval 变化立即 Expired，即使缓存刚更新；跨重启仍保持，直到成功刷新；更高状态优先级不变；auto-refresh 关闭时 Next 为 Manual。
- 仅 URL A→B→A 不误设 interval-refresh-required；interval 改回原字符串仍保持标记；合法 304 成功清除标记，刷新失败保留。
- URL 变化清旧 last_error；仅改其他字段不清；旧请求迟到的成功/失败均不覆盖编辑后的状态。
- 刷新下载/校验/apply/提交及回滚失败矩阵包含新字段；不能把回滚失败宣称为完整恢复。
- reveal：TUI 预填；list/show JSON 无 `url`；reveal 响应不主动写日志。
- reveal 沿用现有认证，未认证拒绝，已认证可读；Web gateway 不挂载此路由；公开错误不暴露当前和旧缓存源 URL，文件日志遵循 dev 原始错误策略。
- reveal 迟到不覆盖用户输入，不串弹层或订阅；读取失败未编辑省略 URL，主动清空阻止保存。
- 旧目录无 `cache-url` 不全部变 Outdated。
- TUI：列名、Status 词、`●` 居中、Enter 开详情、无 `e`、Save 焦点才能提交设置草稿、Esc 丢草稿、add 三字段 + Save。
- 帮助/footer 与 keymap 测试同步。
- 详情 Enabled/InUse 操作保留设置草稿，禁用和无缓存时不能 Use；重复请求受阻，关闭详情后的成功、明确失败、冲突和结果未知均不覆盖当前页面或新详情的错误提示。
- 冲突只在用户确认后使用最新 revision 重提实际改动字段，保留其他字段的新值；再次冲突再次确认。
- Saving 不重复提交，Esc 暂不关闭；成功选中原条目；明确失败可编辑；Unknown 查询不重放请求，不把 finished 当作成功。
- Unknown 核对仍不确定时，Submit again 必须二次确认；Cancel 不发送请求；确认使用新 operation ID，保留 revision 检查；添加确认说明可能重复创建。
- 已添加但首次刷新失败返回列表并选中新 ID；r 重试不重复注册。
- 72×22、长 URL/错误和 resize 下焦点、顶部状态与 Save 可达，footer 固定。
- TUI 全部可见标题、提示、帮助、确认、错误、按钮和进度为英文。
- 相关包测试、按风险 `go test ./internal/integration`、`go vet`、`gofmt`；协议/调度变更做 CGO=0 的跨 OS 编译检查。

不连接真实订阅、不启动真实 mihomo、不操作系统服务。

## 10. 建议 PR 切分

1. Catalog：`cache-url`、`schedule-from`、`interval-refresh-required`、改 URL/interval 语义、调度到期公式、旧目录填充；runtime 回归（含禁止 fillDefaults 抢 active）。
2. 协议：`cache_outdated`、`schedule_from`、`interval_refresh_required`、`GET .../url`；list/show 仍无 URL。
3. TUI 列表：列名、Status 词、居中、footer/`p`、去掉 `e`。
4. TUI overlay：详情 + 添加 Save 交互、reveal、帮助 catalog。

## 11. 明确不做

- 不把 URL 写入 list/show DTO。
- 不改 CLI `--proxy` / 协议 `proxy_mode` 名称。
- TUI 不编辑 `global-interval`。
- 不在改 URL 时弹「必须强制拉取」确认（已否决）。
- 不扩展 POST add 的 interval/auto-refresh。
- 除详情操作项的就地提示外，Q18 的独立 Enabled/Active 术语帮助稿。
- Web 面板订阅编辑。
- 本功能能力标记、新旧 TUI/daemon 混用兼容、旧二进制直接读取新 catalog。
- reveal 的额外 owner/admin 权限分层、持久草稿恢复、逐字段冲突合并。
- 持久化操作结果、跨重启新增 ID 恢复协议或自动重新提交。
- 任何中文 TUI 文案。
