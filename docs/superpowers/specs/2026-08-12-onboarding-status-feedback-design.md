# Onboarding 全流程状态反馈设计

日期：2026-08-12
状态：设计中，待审核
目标 issue：[#26](https://github.com/mihari-proxy/mihari/issues/26)
目标分支：`feat/onboarding-status-feedback`

## 1. 背景

Onboarding 五步流程（`internal/tui/pages/setup/model.go`）在「状态反馈」上整体偏弱：

- **第一步 `stepEndpoints`**：三个 textinput（默认 `127.0.0.1:9190/9090/9191`），`endpointValues()`（`model.go:388`）仅 `TrimSpace`，`validateEndpoints()`（`model.go:392`）只做 netip 格式与 loopback/distinct 校验——**全仓库无任何端口可用性探测**。端口冲突要等到 `installCore` 启动 core 时才在 `runtime.go` 以 `"managed port is unavailable"` 暴露，用户已走完订阅/GeoIP 才发现。
- **第二步 `stepCore` / 第四步 `stepGeoIP`**：服务端 setup 本地预检**已实现**（aio 离线分发预置 core/MMDB 的配套，design §4.3）：core 的 `Manager.Install`（`runtime/manager.go:373-378`）在 `Source=="setup"` 时对本地二进制跑 `mihomo -v` 成功即秒过、不联网；geoip 的 `Manager.UpdateGeoIP`（`runtime/geoip.go:56-61`）在本地 MMDB 有效时直接返回。**但 TUI 两步是纯静态文案**（`model.go:311` / `:317`），用户看到的永远是「按 Enter 安装/更新」，既不知道本地已有，也无法预知会秒过还是联网下载。
- **第五步 `stepReview`**：review 只显示三个端口（`model.go:319-323`）。根因：`actionResultMsg{next, revision, err}`（`model.go:42-46`）只保留 revision，丢弃了各步结果——`installCore` 丢 `Version/Updated`、`addSubscription` 丢 `Subscription`、`updateGeoIP` 丢 `Status`；`RestartRequired` 也未展示；mihari 自身的 OS 服务注册状态无处可见。

本设计按步骤统一收口这三块反馈缺失，让用户全程对「端口能否用、是否用了本地资源、整体配置与服务注册是否正确」有清晰可见性。

## 2. 目标

- 进入/编辑 `stepEndpoints` 时用 `net.Listen` 探测三个端口可用性；占用端口标红，并把占用变成进入下一步的硬阻塞，提供「一键换可用端口」。
- 进入 `stepCore` 时显示本地 core 就绪 + 版本（复用 `Installer.DetectVersion`）；进入 `stepGeoIP` 时显示本地 MMDB 就绪状态（复用 `geoip.Status()`）。
- `stepReview` 汇总展示端口（含重启提示）/ core 来源与版本 / 订阅 / GeoIP / mihari 服务注册状态。
- 最大程度复用服务端**已实现**的本地预检与现有协议响应数据，不重复造探测逻辑。

## 3. 非目标

- 不改变 onboarding 五步的顺序、跳过语义（`s` 跳过 GeoIP）与 `Complete` 提交契约。
- 不改变 aio 离线分发预置 core/MMDB 的服务端预检逻辑（`Source=="setup"` 快速路径保持不变）。
- 不新增后台定时探测；探测仅在进入步骤、输入停顿、进入下一步前触发。
- 不为端口预检引入新依赖；`net.Listen` 探测后立即关闭，不与 core 抢占监听。
- 不重构与本次反馈无关的 TUI / control / runtime 代码。

## 4. 现状与缺口分析

排查后，**多数「缺口」比 issue 描述的更小**——数据多已存在，只是未被 TUI 使用：

| 反馈项 | 现状 | 缺口 |
|---|---|---|
| 端口可用性 | 仅 netip 格式校验 | 需新增 `net.Listen` 探测（纯 TUI 本地，无服务端改动） |
| 本地 core 就绪 | `DetectVersion` 藏在 `Manager.Install` 内（`manager.go:373`）；`CoreInstaller` 接口已含 `DetectVersion(ctx, string) (string, error)`（`manager.go:27`） | 缺独立只读入口 → **扩展 `GET /v1/core`** |
| 本地 GeoIP 就绪 | `Manager.GeoIPStatus`（`geoip.go:14`）已是独立只读方法；control 已暴露 `GET /v1/geoip/status`（`client/runtime.go:101`） | **无缺口**，setup 的 `Client` 接口加方法即可 |
| Review 各步结果 | control 响应**已含** `CoreInstallResult.Version/Updated`（`server/runtime.go:95`）、`SubscriptionResult.Subscription`（`server/subscription.go:80`）、`GeoIPUpdateResult` | TUI 的 `actionResultMsg` 丢弃了 → **回写 Model** |
| 端口重启提示 | `OnboardingStatus.RestartRequired`（`server/onboarding.go:60`）已有 | **无缺口**，review 渲染即可 |
| 服务注册状态 | `service.Manager.Status()`（`service.go:219`）返回 `StatusKind`，含 Windows SCM 非管理员降级查询；仅 CLI + TUI 根模型 badge 用 | 未接入 control → **新增 `GET /v1/service/status`** |

结论：真正的协议层缺口只有两处——本地 core 探测的只读入口、service 状态的 control 暴露。其余靠 TUI 侧接线。

## 5. 用户界面

沿用 setup 页单列文本布局（`model.go:301-332` 的 `View()`），不引入新组件。文案常量集中在 `internal/tui/ui/strings.go`。

### 5.1 `stepEndpoints` — 端口预检 + 一键换端口

```text
Local endpoints
▶ Mixed        127.0.0.1:9190
  Controller   127.0.0.1:9090   ✓
  Web          127.0.0.1:9191
Tab/Shift+Tab fields  Enter continue or auto-fix  Esc back
```

- 探测结果以行尾标记呈现：可用 → `✓`（`theme.Success`），占用 → `✗ in use`（`theme.Danger`），且占用端口的值用 `theme.Danger` 染色。
- 占用时页面提示行：「端口 9090 被占用。Enter 自动切换到可用端口，或手动修改后继续。」
- **Enter 语义随探测结果切换**（不新增快捷键，保持现有 Tab/Enter/Esc 极简交互）：
  - 格式无效 → 维持现状（`validateEndpoints` 报错）。
  - 格式有效且全部可用 → 进入 `stepCore`（现状）。
  - 格式有效但有占用 → **自动查找可用端口填入三个 input 并即时复探**，停留在本步让用户确认；再次 Enter（现已可用）才继续。
- 改端口后若与初始值不同，沿用现有 `RestartRequired` 语义在 review 提示需重启生效。

### 5.2 `stepCore` / `stepGeoIP` — 本地资源就绪提示

进入步骤时异步拉取一次本地状态，把静态文案替换为就绪/未就绪提示：

```text
mihomo core
已检测到本地 core v1.18.5，Enter 将直接使用。     # LocalReady=true
将下载安装 mihomo core。                          # LocalReady=false
Enter install  Esc back

Local GeoIP databases
已检测到本地 GeoIP，Enter 将直接使用。            # Country & ASN Available
将下载 Country/ASN 数据库。                       # 否则
Enter continue  s skip  Esc back
```

- 探测是只读、不联网的本地检查（core 跑 `mihomo -v`、geoip 读 MMDB 元信息），秒级完成；拉取期间显示现有 `LoadingLabel`，失败则回退为原静态文案，不阻塞流程。
- 保留 `stepGeoIP` 的 `s` 跳过；跳过在 review 如实标注。

### 5.3 `stepReview` — 全流程汇总

```text
Review
Mixed        127.0.0.1:9190
Controller   127.0.0.1:9091   （需重启生效）
Web          127.0.0.1:9191
Core         v1.18.5  本地已有
Subscription 我的订阅
GeoIP        Country ✓  ASN ✓
服务         running
Enter complete setup  Esc back
```

- **端口**：三个端口；若 `endpointsChanged()` 且 `status.RestartRequired` 为真，标注「（需重启生效）」。
- **Core**：版本 + 来源——`Updated=true` → 「新装」，`Updated=false` → 「本地已有」；失败 → 「安装失败」。
- **订阅**：本次添加的订阅名；跳过/未填 → 「未添加（已跳过）」。
- **GeoIP**：Country/ASN 就绪（取自 `GeoIPUpdateResult`）；跳过 → 「已跳过」；失败 → 「更新失败」。
- **服务**：`running` / `stopped` / `not_installed`；未注册时明确提示「未注册为开机自启」。
- 进入 review 时异步拉取一次 `GET /v1/service/status`；未返回前服务行显示 `LoadingLabel`，失败显示「未知」。

## 6. 协议与端点变更

所有变更向后兼容（新增可选字段或新端点，不改现有字段语义）。

### 6.1 扩展 `GET /v1/core`（本地 core 就绪）

- `protocol.CoreStatus` 新增可选字段 `LocalReady bool` 与 `LocalVersion string`（`json:"localReady,omitempty"` / `json:"localVersion,omitempty"`）。
- `RuntimeAPI` 接口（`server/runtime.go:23`）新增 `LocalCore(context.Context) (core.LocalCoreInfo, error)`（或等价方法）。
- `runtime.Manager` 实现：复用已注入的 `m.installer.DetectVersion(ctx, m.installRequest.BinaryPath)`（`manager.go:374` 同一处判据，DRY）。成功且版本非空 → `{Ready:true, Version:v}`；失败 → `{Ready:false}`。**不联网、不加锁、不写状态。**
- `coreStatus` handler（`server/runtime.go:74`）合并 `coreStatusDTO(snapshot)` 与 `LocalCore()` 结果写入新字段。
- onboarding 期间 core 尚未运行，`GET /v1/core` 的运行态字段为空，本地就绪字段正是 stepCore 所需，二者同源一次请求最顺手。

### 6.2 新增 `GET /v1/service/status`（服务注册状态）

- 新增 `protocol.ServiceStatus{Schema, Status string}`，`Status` 取 `service.StatusKind`（`running/stopped/unknown/not_installed`）。
- `runtime.Manager` 新增一个**只读 Status 注入**（不持有完整 `service.Manager`，避免 runtime 反向依赖 service 包装配）：新增字段 `serviceStatus func() (service.StatusKind, error)`，由 `cmd/mihari/main.go` 装配处注入 `serviceManager.Status`（main.go 已创建 `serviceManager`，见 `cmd/mihari/main.go:59`）。
- `runtime.Manager` 实现 `ServiceStatus(context.Context) (protocol.ServiceStatus, error)`，调注入的 `serviceStatus()`，复用 `service.go:219` 的 Windows SCM 降级查询能力。
- control 沿用分领域 type-assertion 模式（参考 `s.runtime.(onboardingAPI)` / `.(subscriptionAPI)`）：定义 `serviceStatusAPI interface { ServiceStatus(context.Context) (protocol.ServiceStatus, error) }`，新增 `serviceRoutes(mux)` 注册 `GET /v1/service/status`，handler 在 type-assertion 失败时返回现有 `CodeInvalidState`。
- `control/client` 新增 `ServiceStatus(ctx)` 方法（`doRuntime` GET）。

### 6.3 各步响应已含数据（无需新端点）

review 汇总的前四项数据均已在各步 mutation 响应中返回，仅靠 TUI 侧保留即可（见 §7.4）。

## 7. 实现边界

### 7.1 setup `Model` 与 `Client` 扩展

- `Client` 接口（`model.go:19-25`）新增两个只读方法：
  - `Core(context.Context) (protocol.CoreStatus, error)` —— 进入 `stepCore` 时拉本地 core 就绪。
  - `GeoIPStatus(context.Context) (protocol.GeoIPStatus, error)` —— 进入 `stepGeoIP` 时拉本地 MMDB（端点已存在）。
  - `ServiceStatus(context.Context) (protocol.ServiceStatus, error)` —— 进入 `stepReview` 时拉服务状态。
- `Model`（`model.go:67-82`）新增字段：
  - 端口预检：`portProbe [3]bool`、`portProbeGen uint64`（拒绝迟到探测）、`probe *portProbeConfig`（封装探测/查找）。
  - 本地资源：`coreLocal protocol.CoreStatus`、`coreLocalLoaded bool`；`geoipLocal protocol.GeoIPStatus`、`geoipLocalLoaded bool`。
  - 各步结果回写：`coreResult protocol.CoreInstallResult`、`addedSubscription *protocol.Subscription`、`geoipResult *protocol.GeoIPUpdateResult`、`geoipSkipped bool`。
  - 服务：`serviceStatus protocol.ServiceStatus`、`serviceLoaded bool`、`serviceGen uint64`。
- `NewWithContext` 签名不变（仍只注入 `ctx/client/newOperationID`）；新能力经 `Client` 接口获得，保持 setup 页纯 Client 依赖。

### 7.2 端口预检与一键换端口

- 探测函数 `probeEndpoint(addr string) bool`：`net.Listen("tcp", addr)`，成功则立即 `Close()` 返回 true（可用），失败返回 false（占用）。探测后不留 socket，不与 core 抢占（与 `runtime.go` 启动 core 之间无 TIME_WAIT 冲突——`net.Listen` 默认 `SO_REUSEADDR`，且 core 启动远晚于探测关闭）。
- 触发时机（`updateEndpoints` 与 step 进入处）：
  1. 进入 `stepEndpoints`、`onboardingResultMsg` 填充 inputs 后探一次默认端口；
  2. input 值变化后 **debounce ~600ms** 复探（调度一个延迟 `tea.Cmd`，产生 `portProbeMsg{gen, [3]bool}`；`gen` 与 `portProbeGen` 不符则丢弃，拒绝迟到结果）；
  3. Enter 前（`updateEndpoints` 的 `enter` 分支）取 `portProbe` 最新值决定行为。
- 一键换端口 `findAvailablePorts(current [3]string) [3]string`：从每个被占端口 `port+1` 起 `net.Listen` 试探，命中可用即停（上限 `+1024`）；三端口互不相同（复用 `validateEndpoints` 的 distinct 约束）。Enter 在「格式有效但有占用」时调用，结果回填 inputs 并触发复探。
- 渲染：`renderInputs` 对占用行用 `theme.Danger` 染色值并追加 `✗ in use`（`ui.StatusDot`/`ToneStyle` 体系，`statusdot.go`），可用行追加 `✓`（`theme.Success`）。占用提示文案进 `strings.go`。

### 7.3 本地资源就绪探测

- step 进入处发起只读拉取（异步 `tea.Cmd`，带 generation 拒绝迟到）：
  - 切到 `stepCore` 时调 `m.client.Core(ctx)` → `coreLocalResultMsg`，写入 `coreLocal/coreLocalLoaded`。
  - 切到 `stepGeoIP` 时调 `m.client.GeoIPStatus(ctx)` → `geoipLocalResultMsg`，写入 `geoipLocal/geoipLocalLoaded`。
- `View()` 的 `stepCore` / `stepGeoIP` 分支据 loaded + 字段渲染 §5.2 文案；未 loaded 显示 `LoadingLabel`，err 回退静态文案。
- 探测不阻塞 Enter（用户可在未 loaded 时直接 Enter，由服务端 `Source=="setup"` 快速路径兜底）。

### 7.4 Review 汇总数据流

- 扩展 `actionResultMsg`（`model.go:42`）携带各步结果，或在各 `installCore/addSubscription/updateGeoIP` 闭包内把结果回写到 Model 字段（优先后者，保持消息结构稳定）：
  - `installCore`（`model.go:416`）成功 → 回写 `coreResult = result`。
  - `addSubscription`（`model.go:424`）成功 → 回写 `addedSubscription = &result.Subscription`。
  - `updateGeoIP`（`model.go:432`）成功 → 回写 `geoipResult = &result`；`stepGeoIP` 的 `s` 跳过分支 → `geoipSkipped = true`。
- 进入 `stepReview` 时另发 `ServiceStatus` 拉取（`serviceStatusMsg{gen, status, err}`）。
- `View()` 的 `stepReview` 分支渲染 §5.3 五行；端口行的「需重启生效」判据 = `m.endpointsChanged() && m.status.RestartRequired`。

### 7.5 control / runtime / 装配

- `internal/control/server/runtime.go`：`RuntimeAPI` 加 `LocalCore`；`coreStatus` handler 合并本地探测。
- `internal/control/server/service.go`（新文件）：`serviceStatusAPI` 接口 + `serviceRoutes` + `serviceStatus` handler，模式对齐 `onboarding.go` / `subscription.go`。
- `internal/runtime/manager.go`：实现 `LocalCore`（调 `m.installer.DetectVersion`）；新增 `serviceStatus` 字段 + `ServiceStatus` 实现。
- `internal/runtime/geoip.go`：无改动（`GeoIPStatus` 已存在）。
- `cmd/mihari/main.go`：runtime 构造处注入 `serviceStatus: serviceManager.Status`。
- `internal/control/client/runtime.go`：新增 `Core` / `ServiceStatus` 方法（`GeoIPStatus` 已存在）。
- `internal/control/protocol`：`CoreStatus` 加 `LocalReady/LocalVersion`；新增 `ServiceStatus`。

## 8. 错误与部分成功

- 端口探测是本地 syscall，几乎不失败；若 `net.Listen` 因权限等失败，该端口按「未知」处理（不阻塞、不标红），Enter 仍可继续，交由服务端启动时兜底报错。
- 本地 core/geoip 探测或服务状态拉取失败：相应行回退为静态文案或「未知」，**不阻塞 onboarding**——这些是增强反馈，探测不应让流程卡死。
- 各步 mutation 失败维持现状（`actionResultMsg.err` → `SetupActionFailed` / revision 冲突重载）；review 的失败项如实标注。
- 一键换端口未找到可用端口（罕见，三端口连续占用超 +1024）：保持占用态，提示「未找到可用端口，请手动指定」，不静默写入非法值。

## 9. 并发与生命周期

- 所有探测/拉取命令传播 `m.ctx`（随 TUI 生命周期结束），不新建长期 context。
- 端口 debounce、core/geoip/service 拉取各用独立 generation（`portProbeGen` / `serviceGen` 等）拒绝迟到结果，避免旧响应覆盖新状态。
- 探测 `tea.Cmd` 在离页、退出、步骤切换后由 generation 自然失效，不遗留 goroutine 或 tick 循环（参考 self-update spec §9 的 spinner 生命周期约束）。
- setup 各步 mutation 维持现有 `loading` 串行（一步完成才下一步）；本地探测在步骤切换的同一 tick 触发，与 `loading` 不竞争。

## 10. 测试策略

行为变更采用 Red–Green–Refactor。

### 10.1 `internal/tui/pages/setup`
- 端口探测：用真实 loopback——占一个端口（绑定 `net.Listen` 不关）验证探测为占用、`✗ in use` 渲染、Enter 触发自动换端口；可用端口验证 `✓` 且 Enter 进 `stepCore`。
- debounce/迟到：连续两次 input 变化只采纳最新 generation 的 `portProbeMsg`。
- 本地资源：fake `Client.Core` 返回 `LocalReady=true/v1.18.5` → 渲染「已检测到本地 core…」；`LocalReady=false` → 「将下载安装」。geoip 同型用例。
- 各步回写：fake `InstallCore` 返回 `Updated=false/Version` → `coreResult` 入 Model、review 渲染「本地已有 v…」；`Updated=true` → 「新装」。订阅/geoip/跳过同型。
- review 汇总：给定各字段 + `endpointsChanged && RestartRequired` → 渲染含「需重启生效」；`serviceStatus=not_installed` → 「未注册为开机自启」。
- 回归：现有 onboarding flow（完成、esc 回退、revision 冲突重载、`s` 跳过）行为不变。

### 10.2 `internal/control/server`
- `GET /v1/core` 返回含 `localReady/localVersion`；`LocalCore` 在 installer 缺失时返回 `Ready=false` 而非 500。
- `GET /v1/service/status` 返回注入的 `StatusKind`；runtime 不实现 `serviceStatusAPI` 时返回 `CodeInvalidState`。

### 10.3 `internal/runtime`
- `LocalCore` 调用注入的 `DetectVersion`，成功/失败映射 `Ready`；不联网、不改 store。
- `ServiceStatus` 调用注入的 `serviceStatus` func 并映射为 `protocol.ServiceStatus`。

### 10.4 回归验证
```console
go test ./internal/tui/pages/setup ./internal/control/server ./internal/runtime ./internal/core ./internal/service
go test ./...
go test -race ./...
go vet ./...
gofmt -l cmd internal
```
真实 OS 服务查询、真实 `mihomo -v` 不进入默认测试；以注入 fake 覆盖，跨平台编译检查 `CGO_ENABLED=0` 的 Windows/Linux/macOS 受影响目标。

## 11. 文档影响

实现完成后更新：
- `docs/architecture.md` 的 onboarding 本地预检与端口预检说明；
- `README`/`README.zh-CN.md` 的 TUI onboarding 能力描述（仅当用户可见行为实际变化）。

control `/v1` 协议新增 `GET /v1/service/status` 与 `CoreStatus` 可选字段，需在协议说明（若有）登记为向后兼容增量。

## 12. 验收标准

- 9190/9090/9191 被占时，首页标红并支持一键换可用端口；切换后 core 正常启动，不再触发 `managed port is unavailable`。
- aio 预置 core+MMDB 环境：`stepCore`/`stepGeoIP` 显示「将直接使用」，按 Enter 秒过、全程不联网；全新环境显示「将下载」。
- 走完完整 onboarding 后，review 页显示端口（含重启提示）/ core（版本+来源）/ 订阅 / GeoIP / 服务注册全部状态；跳过项显示「未添加/未跳过」；服务未注册时明确提示。
- `GET /v1/core` 新增字段、`GET /v1/service/status` 新端点向后兼容；现有 CLI、control 协议字段、onboarding `Complete` 契约、持久化格式均未改变。
- 所有新增/修改行为有对应测试，并通过上述跨平台验证与 `gofmt` 预检。

## 13. 已选方案与否决方案

四个关键决策均已与维护者确认，选定方案与否决理由如下。

### 13.1 本地 core 探测暴露 — 扩展 `GET /v1/core`（已选）
复用现有端点与 handler，onboarding 期间 core 未运行时一次请求拿全本地就绪，改动最小、加可选字段向后兼容。否决：① 新增 `GET /v1/core/local`——语义最纯但多一次请求且多一个端点；② 扩展 onboarding DTO——把 core 资源状态塞进 onboarding 语义，职责变宽。

### 13.2 service 注册状态暴露 — 新增 `GET /v1/service/status`（已选）
让 service 状态像 core/geoip 一样有标准 control 协议入口，system page 未来可复用，最规整；review 时多发一请求可接受。否决：① 扩展 onboarding DTO 带 service——service 不属 onboarding 语义；② 复用 TUI 根模型本地 `serviceCtrl`——破坏 setup 纯 Client 依赖，且仅本地 TUI 可用、Web/远程拿不到。

### 13.3 review 汇总数据来源 — 保留各步结果到 Model（已选）
`CoreInstallResult.Version/Updated`、`SubscriptionResult.Subscription`、`GeoIPUpdateResult` 本就在各步响应里被丢弃，回写 Model 零额外请求、时序自然。否决：进入 review 并发拉取 `GET /v1/core`、`/v1/geoip/status`、`/v1/subscriptions`——实时性强但多请求，且需逐一确认 onboarding 期间这些端点的可用性，复杂度更高。

### 13.4 设计文档位置 — `docs/superpowers/specs/`（已选）
沿用 PR #34（self-update）设计文档先例，进仓库、随 PR 提交、可长期审核追溯。否决：`.gitignore` 已忽略的 `docs/plans/`——仅本地中间文件，无法随 PR 留档。
