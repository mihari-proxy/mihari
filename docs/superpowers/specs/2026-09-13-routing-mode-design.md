# Mihari 运行模式与 GLOBAL 出口管理

日期：2026-09-13
状态：用户已于 2026-09-13 确认整体设计通过；实现与本地验证完成，进入 PR 评审。
分支：`feat/tui-routing-mode`
基线：`origin/dev` @ `0ba631f`

实施计划：[运行模式与 GLOBAL 出口管理](../plans/2026-09-13-routing-mode.md)。

## 1. 目标与已确认范围

由 Mihari 管理 mihomo 的 `rule`、`global`、`direct` 运行模式及 GLOBAL 出口选择。分流由 mihomo 执行；Mihari 提供持久化、恢复和一致的控制入口。

| 决策 | 用户确认的行为 |
| --- | --- |
| 所有者 | daemon/Manager 管理持久状态，客户端经本地控制协议操作 |
| 模式保存 | 模式是 Mihari 全局偏好，刷新订阅、切换订阅及重启后保留 |
| 默认及优先级（Q4） | 首次默认 Rule；Mihari 的模式优先，订阅中的 `mode` 不覆盖它 |
| TUI 入口 | Proxies 页面最上方提供 `Mode`，按 Enter 打开模式选择弹窗；沿用项目现有设计美学 |
| GLOBAL 候选（Q5） | 使用 mihomo 返回的 GLOBAL 候选，可包含节点、策略组和内置出站 |
| GLOBAL 记忆（Q6） | 按订阅分别保存出口；切换 Direct/Rule 不清除该订阅的选择 |
| 客户端范围（Q7） | TUI 和 CLI 都支持 |
| 回退（Q8） | 无保存出口或出口消失时，若候选有 DIRECT，保持 Global 并选择 DIRECT；无 DIRECT 则退回 Rule。明确展示直连状态，失效时提示原因 |
| 连接（Q9） | Mihari 的模式及出口切换本身保留已有连接，不额外请求关闭连接 |
| 内核离线（Q10） | daemon 在线时可保存模式，明确显示待内核启动生效；无法取得有效候选时不能选择 GLOBAL 出口 |
| Web（Q11） | 通过 gateway 将模式及 GLOBAL 选择接入同一持久化用例；zashboard 已被用户确认具有模式切换入口 |
| CLI（Q12） | 使用下述命令，支持既有 `--json` 输出方式 |

```console
mihari proxy mode
mihari proxy mode rule
mihari proxy mode global
mihari proxy mode direct
mihari proxy select GLOBAL "节点或策略组名称"
```

查询应能区分保存的模式、实际模式与当前订阅的 GLOBAL 出口。候选查询复用代理组读取能力；精确 DTO 与能力声明在详细方案中定义。

## 2. 上游事实与证据

### 2.1 mihomo

- [官方运行模式定义](https://github.com/MetaCubeX/Meta-Docs/blob/e52690240f2e4a70de75002c6cf87e2e7921d29c/docs/config/general.md#L59-L69)：Rule 按规则匹配，Global 使用 GLOBAL 中选定的代理或策略，Direct 全局直连。
- [配置 API](https://github.com/MetaCubeX/Meta-Docs/blob/e52690240f2e4a70de75002c6cf87e2e7921d29c/docs/api/index.md#L98-L120)：`GET /configs` 读取运行配置，`PATCH /configs` 修改配置。模式请求为 `{"mode":"global"}` 等枚举值。
- [代理选择 API](https://github.com/MetaCubeX/Meta-Docs/blob/e52690240f2e4a70de75002c6cf87e2e7921d29c/docs/api/index.md#L235-L248)：候选和当前选择来自 `all`/`now`；`PUT /proxies/GLOBAL` 携带 `{"name":"候选名"}`。
- [配置解析实现](https://github.com/MetaCubeX/mihomo/blob/Meta/config/config.go#L840)：内置 DIRECT 加入默认代理列表，未自定义 GLOBAL 时该列表用于生成 GLOBAL。
- [GLOBAL 文档](https://wiki.metacubex.one/config/proxy-groups/built-in/)允许在配置中自定义该组。因此实现必须检查实际候选，不得自行向候选列表添加 DIRECT。
- [模式更新实现](https://github.com/MetaCubeX/mihomo/blob/Meta/hub/route/configs.go#L356)调用 `tunnel.SetMode`，该处理路径不把 mode 写回 YAML。不能以 PATCH 成功替代 Mihari 持久化。

Global + DIRECT 仍是 Global 模式，只是该组所选出口直连。回退到 DIRECT 意味着原本通过代理的后续流量可能转为直连；用户已接受这一回退策略。

以上源码事实来自查阅时的上游。实施测试还应核对仓库支持的 mihomo 版本，不能以移动分支代替版本兼容性证据。

### 2.2 Web 面板

两套面板都已有三种运行模式和 GLOBAL 选择，不需要修改面板来增加基础按钮。

- zashboard [模式控件](https://github.com/Zephyruso/zashboard/blob/b31d05f42702b121e0b17eab1e3697d5f1d6db8d/src/components/controls/ProxiesCtrl.tsx#L151-L160)经 [API 适配](https://github.com/Zephyruso/zashboard/blob/b31d05f42702b121e0b17eab1e3697d5f1d6db8d/src/api/clash.ts#L123-L129)发送 `PATCH /configs`；[候选选择](https://github.com/Zephyruso/zashboard/blob/b31d05f42702b121e0b17eab1e3697d5f1d6db8d/src/api/clash.ts#L36-L42)发送 `PUT /proxies/{group}`。
- MetaCubeXD [模式保存](https://github.com/MetaCubeX/metacubexd/blob/8bbc8f58fef71148a94fb5c0ff808f79b057337d/packages/ui/composables/useGeneralConfig.ts#L50-L84)同样走 `PATCH /configs`；[候选选择](https://github.com/MetaCubeX/metacubexd/blob/8bbc8f58fef71148a94fb5c0ff808f79b057337d/packages/ui/composables/useApi.ts#L368-L374)走 `PUT /proxies/{group}`。

模式或节点操作之外，面板可能主动请求关闭连接：MetaCubeXD 受 `autoCloseConns` 控制；zashboard 的节点选择受 `automaticDisconnection` 控制。Mihari 的 Q9 承诺针对 mutation 本身，不等于拦截用户从面板另外发出的连接关闭请求。

## 3. 当前仓库事实

- `internal/config/settings.go`：Settings 没有运行模式字段；严格解码会拒绝未知字段。
- `internal/subscription/generator.go`：生成器保留输入 mode，缺少时补 rule；实际 runtime/startup 调用传入的 overrides 为 nil。因此当前行为尚未实现 Mihari 模式覆盖。
- `internal/core/config.go`：无订阅的 bootstrap 配置使用 rule。
- `internal/mihomo/client.go`：已有 `Configs`、`PatchConfigs` 和 `SelectProxy` 底层能力。
- `internal/control/server/runtime.go` 与 `internal/tui/pages/proxies/model.go`：GLOBAL 已通过通用组读取和选择链路展示；本次需要明确顶部入口和独立出口交互。
- `internal/runtime/manager.go`：现有策略组选择没有 Mihari 自己的持久化恢复记录。
- `internal/web/server.go`：当前 `/configs` 写入仅允许 TUN；含 mode 的请求会返回 unsupported mutation。本次需增加明确的 mode 路由。
- `internal/control/protocol`：尚无运行模式类型化接口与对应能力声明。

AGENTS.md 指定的旧架构设计文件未找到；现有架构说明为 `docs/architecture.md`。`docs/architecture/root-config-policy-v1.md` 已标注完整 RootConfigPolicy 被后续移除方案替代，不能把历史白名单当作现行要求。

## 4. 实施约束与影响

所有 TUI/CLI 读写经 `internal/control/client`，由 Manager 在统一 mutation 路径上操作；Web 使用 gateway 适配到相同用例。不得将 mihomo controller 地址或 secret 暴露给客户端。

本次已确认涉及新增持久化状态、新 CLI 命令与本地协议能力，以及 Web mode 写入白名单。精确持久化布局、DTO 和恢复步骤仍需在实施前的最终设计中列明；保留既有 envelope、退出码和错误分类，未知 Web 写入继续拒绝。未引入新依赖或跨平台支持变化。

模式覆盖必须涵盖 daemon 启动、订阅生成、刷新和切换、内核重启及无订阅 bootstrap。GLOBAL 恢复应绑定订阅身份并重新检查当前候选，不能因别的订阅有同名节点就复用其记录。

慢准备与提交分离，提交时重查 revision、活动订阅及核心身份。模式热更新与持久化的一致性、重载后的选择恢复顺序，以及崩溃恢复必须被测试证明；不能以“先 PATCH 再写文件”两步无恢复调用宣称完成事务。

内核已知停止时的“保存待生效”，与运行中的上游请求失败、超时结果不明，应分别建模。现有 Settings 已提交后的同步 warning 仍按仓库约束保持成功，不得误回滚。

## 5. 回退、失败与交互的最终决策

### Q13：回退后不自动恢复旧选择

用户确认继续使用 DIRECT。回退结果成为新的持久选择，直到用户主动修改；旧出口重新出现不会自动切回。无 DIRECT 而退回 Rule 同样不因后台刷新自动恢复 Global。

### Q14：在线切换失败的承诺

用户确认：明确失败时保留旧保存值及旧实际值，并报告失败；只有内核已知未运行时才允许成功保存为待生效。超时等结果不明先由 daemon 重新核对，无法确认时明确显示未知/恢复中，不能假报成功或声称已恢复。

### Q15：Proxies 顶部的交互

用户明确要求 Mode 使用 Enter 打开的选择弹窗。

页面顶部提供 Mode 和 GLOBAL 两个紧凑入口；代理组内容仍使用既有布局。GLOBAL 入口定位并打开现有 GLOBAL 候选列表，避免同一组在页面上重复渲染两份候选。Rule/Direct 下也能预先选择 GLOBAL 出口，选择出口本身不切换模式。

以下为信息结构示意，实际字符宽度、对齐和留白由终端尺寸与统一主题控制：

```text
Mode       Rule                         Enter change
GLOBAL     Hong Kong 01                 Enter select
```

弹窗示意（当前生效模式为 Rule，焦点也位于 Rule）：

```text
╭─ Routing Mode ─────────────────────────────╮
│                                           │
│  › Rule       Current                     │
│    Follow routing rules                   │
│                                           │
│    Global                                 │
│    Use the GLOBAL selection               │
│                                           │
│    Direct                                 │
│    Connect directly                       │
│                                           │
│  ↑↓ Select   Enter Apply   Esc Cancel      │
╰───────────────────────────────────────────╯
```

- 打开后焦点位于当前保存的模式；上下键只移动预选，Enter 才提交。按 Esc 取消，关闭后焦点返回 Mode。
- 当前生效项使用明确文字标记，键盘焦点使用独立样式；不把预选误显示为已生效。内核离线时标记 Saved / Pending，不显示虚假的 Current。
- 复用 `internal/tui/ui/theme.go` 的 `Dialog`、`Title`、`Muted`、`RowFocus`、`RowSelected` 和语义状态色。沿用圆角边框、紫色强调、灰色说明文字及现有留白，不能在新控件硬编码另一套颜色。
- 弹窗居中并随可用尺寸约束宽高；长出口名称按终端显示宽度截断，完整选择保留在数据层。窄屏下说明可换行或收紧留白，不能遮掉操作提示。
- 提交中显示清晰的进行状态并防止重复提交；成功以 daemon 返回的状态更新页面。失败保留明确错误和可重试入口，不提前切换生效标记。
- 页脚与当前页帮助同步说明 Enter、上下键、Esc；弹窗打开时由弹窗接收输入，不能同时操作背景代理组。

视觉验收除了逻辑测试，还需检查正常尺寸、窄屏、长名称、离线待生效和失败状态的实际 TUI 渲染。

实现复用点已核对：根 `Modal` 当前没有多项选择类型；Connections 页已有页面内弹窗接管输入与居中渲染的范式。因此优先在 Proxies 包内实现小型选择状态与渲染 helper，复用 Theme 及 `HelpMode`/`FooterHints`，无需新增通用 UI 框架。

沿用现有最小终端尺寸 72×22、Compact 与 Full 布局。Mode/GLOBAL 两行入口占用的高度必须从代理组视口扣除，调整滚动与焦点计算；不以新增大边框卡片挤占列表。提交时在弹窗显示 pending，成功后关闭并回到 Mode；失败保留选择上下文与错误。全局退出按键仍遵循根模型现有行为。

## 6. 验证范围草案

行为改动执行 Red–Green–Refactor。至少覆盖以下外部可观察行为及错误边界：

- 默认 Rule、覆盖订阅 mode、刷新/切换/重启后保存模式恢复。
- GLOBAL 按订阅隔离；DIRECT 存在与缺失的回退；旧候选过期与并发订阅切换。
- 无订阅及内核停止状态；保存状态与实际状态的区别。
- 持久化失败、上游拒绝、请求结果不明与恢复；revision 冲突和重复 operation 不重复提交。
- TUI 顶部 Mode、GLOBAL 候选、待生效与失败提示；CLI 文本/JSON及参数校验。
- Web 两种面板使用的 PATCH/PUT 请求接入同一用例；未知字段拒绝；不附带关闭连接操作。
- 相关包、跨包集成与按风险执行的 race/vet、CGO-free 跨平台构建。

不连接真实订阅、不启动真实 mihomo、不操作系统服务。草案阶段只检查文档 diff；尚未运行实现测试，也未提交或推送。
