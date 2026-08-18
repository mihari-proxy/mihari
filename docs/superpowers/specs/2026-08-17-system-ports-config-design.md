# TUI System 页 Ports Config 与双栏布局设计

日期：2026-08-17
状态：实施中
目标分支：`feat/system-ports-config`
工作目录：`.worktrees/feat-system-ports-config`

## 1. 背景

v0.7.5 的 System 页把代理口和 Core API 挂在 Daemon 卡片里，只能看、不能改。Web 口（默认 9191）没有独立地址行。改三个托管端口只能走 Setup。

现场（v0.7.5，服务 running、daemon degraded）：

- Daemon 显示 `Proxy endpoint —`、`Mihomo Core API —`
- 页脚：`managed port mixed-addr 127.0.0.1:9190 is held by mihomo.exe`
- 用户要在 System 里直接改口，并希望宽屏时卡片不要一直单列堆叠

后端已经支持：`PATCH /v1/onboarding` 可改 `mixed_addr` / `controller_addr` / `web_addr`；改完 `RestartRequired`。不需要新协议。

上一版方案是在 Daemon 行内编辑。已否决：端口配置单独成区，并调整卡片优先级与双栏。

## 2. 目标

- 新增独立卡片 **Ports Config**，在 System 页修改三个托管端口。
- Daemon 不再承载 Mixed / Controller 地址行。
- 窄屏单列顺序固定；宽屏对后五个卡片做双栏。
- 写入复用现有 onboarding 更新与 `ActionApplyEndpointChange` 确认。
- 端口占用可探测、可一键换空闲口（对齐 Setup，解决当前 9190 被 mihomo.exe 占用的现场）。
- 每一行展示端口状态：`Owned` / `Occupied by …` / `Available` / `Unknown`。

## 3. 非目标

- 不改现有 `/v1` 字段语义、错误码、settings schema。允许 `GET /v1/status` **加法**可选 `pid`（本 daemon 进程 PID），旧客户端忽略即可。
- **禁止**用进程名判断 Owned。机器上可以同时有多个 `mihomo.exe` / `mihari.exe`（本实例内核、Sparkle、另一个 Mihari）。
- 不加 CLI 改口命令。
- 浏览器面板仍不能改这三个口。
- 不把 Zashboard / MetaCubeXD 打开行搬进 Ports Config。
- 不为 System 单独做滚动容器（矮窗口仍裁切底部，与现状相同）。
- 不改 Overview 的 `wideMinWidth`。

## 4. 卡片结构与优先级

### 4.1 单列（内容宽度 < 80）

自上而下：

1. **Ports Config**（新，始终全宽）
2. **Daemon**
3. **mihomo core**
4. **System service**
5. **Network**
6. **About**

Ports Config 置顶：daemon degraded 时页脚已经点名 mixed-addr 占用，打开 System 立刻能改口。

### 4.2 双栏（内容宽度 ≥ 80）

Ports Config 仍全宽置顶。其余五张按你给的优先级从左到右、再往下排：

```text
┌─ Ports Config ───────────────────────────────────────┐
│   Mixed           127.0.0.1:9190   ● Occupied by mihomo.exe (9736) │
│   Controller      127.0.0.1:9090   ● Owned                  │
│   Web             127.0.0.1:9191   ● Owned                  │
└──────────────────────────────────────────────────────┘
┌─ Daemon ──────────────┐ ┌─ mihomo core ─────────────┐
│   Daemon  ● degraded  │ │   mihomo core  …          │
│   Update Mihari       │ │   Core channel            │
│   Run Setup           │ │   Install / Restart       │
└───────────────────────┘ └───────────────────────────┘
┌─ System service ──────┐ ┌─ Network ─────────────────┐
│   Status  ● running   │ │   TUN  …                  │
│   Reinstall service   │ │                           │
└───────────────────────┘ └───────────────────────────┘
┌─ About ──────────────────────────────────────────────┐
│   Mihari   A local manager for mihomo                │
│   GitHub   github.com/mihari-proxy/mihari            │
└──────────────────────────────────────────────────────┘
```

- 配对：Daemon ↔ mihomo core，System service ↔ Network。
- 奇数张 **About** 全宽，不单独占半栏。
- 同一行两张卡片 `EqualizeLineCount`，底边对齐（与 Overview 相同）。
- 阈值 80 是 **内容区宽度**（不含 rail）。官方 Full 100×28 内容约 84，走双栏；Compact 72 列内容约 58，保持单列。80 保证半栏放得下 `Controller` + `127.0.0.1:9090`，避免 Overview 曾经把单位裁掉的问题。

### 4.3 Daemon 里留下什么

从 Daemon 去掉 `Proxy endpoint` / `Mihomo Core API`。留下：

- Daemon 健康 / 版本
- 已安装面板行（Zashboard / MetaCubeXD，Enter 仍打开浏览器）
- Update Mihari
- Run Setup（完整向导仍在）

## 5. Ports Config 交互

三行，标签短、和 Setup 首页一致：

| 行 | 标签 | 默认值 | 含义 |
| --- | --- | --- | --- |
| Mixed | `Mixed` | `127.0.0.1:9190` | 混合代理 |
| Controller | `Controller` | `127.0.0.1:9090` | mihomo API（必须 loopback） |
| Web | `Web` | `127.0.0.1:9191` | Web 网关（必须 loopback） |

### 5.1 查看

未编辑时一行三段：标签、地址、状态。状态是行值的一部分，不另起行。

```text
│   Mixed        127.0.0.1:9190   ● Occupied by mihomo.exe (9736) │
│ > Controller   127.0.0.1:9090   ● Owned                  │
│   Web          127.0.0.1:9191   ● Owned                  │
```

状态用现成 `StatusDot` + 着色文案，和 Daemon / Service 行同一套 tone，一眼能分出绿/红。半栏宽度不够时先裁状态、再裁地址，标签始终在。页顶仍可钉 daemon `lastError`。

### 5.2 端口状态：只认 PID，不认进程名

机器上可以同时有很多个 `mihomo.exe`（本实例内核、Sparkle / Meta Tunnel、别人装的 Clash）。**映像名等于 `mihomo.exe` 绝不能当成 Owned。** 同样不能因为映像名是 `mihari.exe` 就把 Web 口判给自己（可能还有第二个 Mihari）。

身份只有两个数字：

| 口 | 自己的持有者 | PID 来源 |
| --- | --- | --- |
| Mixed、Controller | 本实例监督的 mihomo 子进程 | 已有 `GET /v1/core` 的 `pid` |
| Web | 本实例 Mihari daemon（常是服务进程，不是 TUI） | **加法** `GET /v1/status` 的可选 `pid`（`os.Getpid()`，`omitempty`） |

TUI 自己的 PID 不是 daemon PID，不能用来认 Web 口。

分类（TUI 本地，lookup / listen 可注入）：

1. `net.Listen("tcp", addr)` 成功并立刻关掉 → **Available**。
2. Listen 失败则 `platform.LookupTCPOccupant(addr)` 得到占用者 PID：
   - Mixed / Controller：占用者 PID == `core.PID` 且 `core.PID > 0` → **Owned**。
   - Web：占用者 PID == `status.PID` 且 `status.PID > 0` → **Owned**。
   - 有占用者 PID，但对不上上面两个 → **Occupied**。行上必须带 PID，方便对照多个 `mihomo.exe`：
     - 有名字：`Occupied by mihomo.exe (9736)`
     - 没名字：`Occupied by PID 9736`
     - PID 也没有（lookup 失败）→ 走 Unknown，不编造数字
   - 查不到占用者 → **Unknown**。不标 Occupied，避免权限失败误红。
3. `core.PID == 0`（内核没起来 / Unknown）时，任何 `mihomo.exe` 占 Mixed/Controller 都是 Occupied。这就是你现在的 degraded 现场。
4. 禁止第三条规则：按 `mihomo` / `mihari` / `Sparkle` 字符串猜 Owned。

进入 System 页、core/status/onboarding 刷新、改口成功后各 probe 一次。不每秒轮询。

| 状态 | 行尾 | 颜色（必须上色） |
| --- | --- | --- |
| Owned | 绿点 + 绿字 `Owned` | `TonePositive` / Success |
| Occupied | 红点 + 红字 `Occupied by mihomo.exe (9736)` | `ToneDanger` |
| Available | 灰点 + 灰字 `Available` | Muted / Neutral |
| Unknown | 灰点 + 灰字 `Unknown` | Muted |
| 探测中 | 不闪红，可用 Pending chip `Checking` | Warning / Pending |

颜色走现有 `StatusDot` + `ToneStyle`，不要新色板。Occupied 整段状态（点 + 字）都是 Danger，不能只改点不改字。

Occupied 行上就要有 PID，不藏到详情里。Owned / Available / Unknown 行上不写 PID（Owned 的身份已经是 core/daemon PID，不必重复）。详情里仍可再列完整 `Holder PID 9736`。宽度不够时先裁进程名、保留 `(9736)`。

### 5.3 编辑

1. 焦点在某端口行时 Enter → 该行进入输入（`textinput`，预填当前值）。
2. 输入时 footer：`Type address  Enter apply  Esc cancel`。
3. Esc：丢草稿，回到查看。
4. Enter：校验 → **仅 Occupied（他人占用）** 才自动换成空闲口；Owned / Available 不换。校验失败则 `lastError`，不弹确认。
5. 值相对当前设置有变化 → `ActionApplyEndpointChange` 确认框（已有文案 `Replace configuration`）。
6. 确认后 `PATCH /v1/onboarding`，只带三个地址，**不**改 `complete`。
7. 成功：刷新 onboarding 行值；`RestartRequired` 则弹已有 `Restart required` 详情框。
8. 失败：行 Failed chip + 顶部错误；不把底层错误原文（路径、完整订阅 URL）渲出去。

`pending` 时忽略 Enter，与其它 System 行一致。`mutationsEnabled == false` 或缺少 onboarding capability 时不能进编辑。

面板行、GitHub、服务动作的 Enter 语义不变。

### 5.4 校验

与 Setup 同一套规则（抽到双方都能用的小函数，避免复制）：

- Mixed：合法 `IP:port`，端口非 0
- Controller / Web：必须 loopback + 合法端口
- 三个端口号互不相同

占用探测比 Setup 多一层 occupant **PID** 对照；Setup 首页可以继续只用 Free/Occupied，不必这次一起改。

## 6. 焦点与键盘

`↑/↓` 仍按 `rows()` 源顺序走，不按双栏视觉左右走：

Ports 三行 → Daemon 各行 → Core → Service → Network → About。

双栏时从 Daemon 最后一行再 ↓ 会进到右侧或下一行的 Core，视觉上可能跳一下，比按格子走更简单，也不改现有行模型。

编辑态：左右移光标，↑/↓ 离开编辑（等同 Esc 丢草稿），避免在输入里误切行。

## 7. 实现边界

只改 TUI System 表现层 + 抽出 Setup 已有的校验/换口，供两页共用：

- `internal/tui/pages/system/model.go`：Ports 分区、去 Daemon 地址行、双栏 `View`
- `internal/tui/ui/strings.go`：`PortsConfigSectionTitle`、`MixedLabel`、`PortOwned`、`PortOccupiedBy`、`PortOccupiedByOtherApp`、`PortAvailable` 等
- 校验/换口：从 `setup` 抽到例如 `internal/tui/ui/endpoints.go`
- 占用分类：TUI 调 `platform.LookupTCPOccupant`，只和 `core.PID` / `status.PID` 比；lookup 可注入
- `internal/control/protocol/status.go`：`Status` 增加可选 `PID int \`json:"pid,omitempty"\``
- daemon 填 `os.Getpid()`；旧客户端忽略该字段
- `internal/tui/pages/system/model_test.go`、必要时 `setup` / `control` 测试

System `Client` 增加 `UpdateOnboarding`（Setup 已有）。

不改错误码、settings schema、JSON envelope 版本号。

## 8. 测试

`internal/tui/pages/system/model_test.go`，不访问公网。

1. View 含 `Ports Config`、三行默认或注入地址；Daemon 不再含 `Proxy endpoint` / `Mihomo Core API`。
2. 单列（width 70）：六张卡片自上而下，无水平拼接。
3. 双栏（width 84）：Ports 全宽；下一行同时有 Daemon 与 mihomo core；About 全宽。
4. Enter Mixed → 编辑态；Esc 恢复原值且不调用 UpdateOnboarding。
5. 改 Controller 后 Enter → `ActionApplyEndpointChange`；Execute 发出的 PATCH 含三个地址、不含 `complete`。
6. 非法地址 / 非 loopback / 端口冲突 → 不弹确认，顶部错误。
7. 状态分类（注入 listen + lookup，不读真实网卡表）：
   - listen 成功 → 灰 `Available`
   - Mixed/Controller 占用者 PID == `core.PID` → 绿 `Owned`
   - Web 占用者 PID == `status.PID` → 绿 `Owned`
   - 占用者是另一个 `mihomo.exe`（PID ≠ core.PID）→ 红 `Occupied by mihomo.exe (9736)`
   - 映像名是 `mihomo.exe` 或 `mihari.exe` 但 PID 对不上 → 仍是 Occupied，不是 Owned
   - lookup 失败 → 灰 `Unknown`
   - 渲染含 StatusDot，Occupied 为 Danger，Owned 为 Positive
8. Occupied 行 Enter apply → 换成空闲口后再确认；Owned 行不改地址则不换口、不写盘。
9. 写入后 `RestartRequired` → 根 shell 弹出已有 Restart required 框（可放 `internal/tui/model_test.go`）。
10. 断开 / 无 capability：Enter 端口行不进编辑。
11. 面板行 Enter 仍走打开浏览器。

## 9. 安全与架构

- Controller / Web 仍强制 loopback。
- 浏览器拿不到 controller secret。
- 写入仍走 daemon + onboarding，TUI 不直接改 `mihari.yaml`。
- 保持 `CGO_ENABLED=0`。

## 10. 建议验证

- `go test ./internal/tui/pages/system/ ./internal/tui/pages/setup/ ./internal/tui/`
- `gofmt -l` 改过的 Go 文件
- 人工：窄窗单列；拉宽后 Ports 置顶全宽、Daemon|Core、Service|Network、About 全宽；改 9190 并确认后出现 Restart required
