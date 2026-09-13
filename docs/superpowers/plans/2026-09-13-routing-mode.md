# 运行模式与 GLOBAL 出口管理实施计划

日期：2026-09-13
状态：设计已通过；计划已编写，尚未实施。
设计：[运行模式与 GLOBAL 出口管理](../specs/2026-09-13-routing-mode-design.md)
分支：`feat/tui-routing-mode`
工作目录：`.worktrees/feat-tui-routing-mode`
基线：`origin/dev` @ `0ba631f`

## 1. 交付范围

由 daemon 持久化管理 Rule/Global/Direct，以及按订阅隔离的 GLOBAL 出口；TUI、CLI 和 Web 面板调用同一用例。Proxies 顶部 Mode 按 Enter 打开美观且符合既有主题的选择弹窗，GLOBAL 提供独立出口入口。

保持默认 Rule、用户模式覆盖订阅、失效优先回退 DIRECT（不存在时回退 Rule）、不自动恢复失效前的选择、模式切换不主动断开已有连接、已知内核停止时可保存待生效等已批准行为。

不修改 CHANGELOG、不新增依赖、不修改系统服务、不连接真实订阅或真实 mihomo。未获本任务的提交/推送指令，不创建 commit 或 PR。

## 2. 技术落点

### 2.1 持久化与兼容

在 `config.Settings` 增加可选、类型化的 `routing` 块：

```yaml
routing:
  mode: global
  global-selections:
    <subscription-id>: <candidate-name>
  bootstrap-global: DIRECT
```

- 缺少 routing/mode 时有效模式为 rule。订阅 mode 和通用 overrides 不能覆盖受管模式。
- `global-selections` 只用当前 catalog 中的订阅 ID 作键；不使用订阅名称、URL 或候选名作文件路径。
- `bootstrap-global` 只记忆没有活动订阅时的选择，不借用任意订阅记录，也不发明可能与真实 ID 冲突的 map 键。
- Clone 深复制块和 map；验证模式枚举、值类型与长度，沿用 settings 文件大小限制。
- 删除订阅时清理其保存选择；过期清理失败不得把一项已失败的订阅事务报告为成功。必要的补偿与重启后清理纳入任务 3。
- 继续使用既有原子 settings 保存与 CommitResult 语义。已提交后的同步 warning 保留成功与内存发布，在锁外记录 WARN。
- 新字段会被旧版本的严格 settings 解码拒绝。这是新增持久化功能的降级兼容影响，需写进用户文档和实施验收；不得承诺旧二进制可直接读取。禁止顺带改 settings schema、现有升级/降级协议或清除用户状态。

### 2.2 控制协议与 CLI

新增可选 `routing-mode-v1` 能力，通过使用方附近的小接口提供：

- `GET /v1/routing`：类型化模式/出口状态。
- `PATCH /v1/routing`：修改模式，body 含 `operation_id`、可选 `if_revision`、`mode`。
- `PUT /v1/proxy-groups/GLOBAL`：复用现有节点选择请求与 MutationResult，内部增加当前订阅的持久化和恢复。其他组保持既有语义。

Routing 状态至少包含 `schema`、`revision`、`desired_mode`、可选 `live_mode`、`state`、可选 `subscription_id`、`global_selection`、可选 `live_global_selection` 和安全的状态说明。`state` 明确区分 `applied`、`pending`、`unknown`；不得因 controller 请求失败就把已保存值填成实际值。

候选列表继续来自 `/v1/proxies`，避免维护两份列表契约。新的 routing DTO 不含 controller 地址、secret、订阅 URL 或底层 cause。新接口使用既有 `/v1` envelope 和错误映射；不扩大现有 RuntimeAPI 强制方法集导致所有无关 fake 被迫实现新能力。

CLI：`proxy mode` 查询，`proxy mode rule|global|direct` 修改，`proxy select GLOBAL NAME` 选择出口；沿用全局 `--json`、operation ID 和退出码。CLI 参数只接受合法枚举，不在客户端保存文件或推断实际状态。

### 2.3 mutation 与恢复

实施细化：单独切换模式使用已有 controller PATCH，无需生成或 reload YAML；settings 是重启恢复的权威。正常配置生成仍覆盖 mode，并以 settings generation 拒绝过期候选；supervisor 每次健康检查在报告 running 前恢复保存的模式/出口。这样保留已有连接，也覆盖磁盘 runtime 文件尚含旧 mode 的情况。订阅配置重载的配置文件、目录、选择和保存回退仍在同一可补偿用例中。

实现应区分三类输入：已保存意图、内核实际观测、当前订阅身份/候选。不能把 HTTP 失败当成“内核已停止”。

1. 在准备阶段抓取 settings generation、订阅 ID/版本/缓存身份、核心身份和 revision，生成候选配置并完成校验；慢准备在提交所有权之外。
2. 提交时取得统一 mutation 所有权并复核所有输入；若任何身份改变，返回既有冲突错误，不应用旧候选。
3. 对在线操作捕获旧运行模式和 GLOBAL 选择；只在需要时更新对应字段，读回确认。未知结果通过有界恢复 context 核对，不直接假报成功。
4. runtime 配置、settings 与已确认 live 状态必须组成可补偿的用例。实施前在代码注释中列明每个失败点的恢复顺序；不能仅新增无补偿的 PATCH/Save 两步。
5. 当前事务内明确失败时恢复旧保存值和可确认的旧 live 值；补偿不能确认时使用现有 degraded 机制阻止后续业务写入，并提供安全诊断。
6. 成功持久提交后才发布新的内存状态和 revision。Settings warning 例外仍成功；不能在提交后因为警告回滚。
7. 已知内核停止时仅提交可在启动应用的受管配置和保存意图，状态为 pending。此时 GLOBAL 选择没有有效候选则拒绝选择操作，模式意图仍可保存。
8. mutation 本身不请求 `DELETE /connections` 或逐连接关闭。

全局模式恢复同时覆盖 `subscription.Generate`、无订阅 `core.BootstrapConfig`、app startup 和监督器重启。尤其核对非 trusted 分支的 `EnsureRuntimeConfig`：已有 runtime 文件不能成为跳过 settings 恢复的理由。

GLOBAL 恢复必须发生在新配置的候选可用后，并绑定本次订阅/核心身份。不能在 `Observe` 回调持有维护锁时再次进入同一锁，也不能引入无所有者 goroutine。需要异步恢复时由 Manager.Run 管理有界通知、context 与退出回收；UI 不负责触发恢复。配置 reload 与自动重启都必须接入，不能只覆盖显式 CLI restart。

首次无选择或旧候选不在新列表中，显式选择 DIRECT（在候选中时）；否则回退并保存 Rule。回退是新的持久意图，不保留会自动恢复的隐藏旧目标。保存变化与该次配置/订阅提交保持一致。自定义 GLOBAL 不能手动选择时遵循内核能力，不能假造候选或误报选择成功。

### 2.4 Web 兼容

- 保持 `/configs` 的 mihomo JSON 形状和成功的 204 响应；`GET /configs` 继续反映实际内核状态。
- `PATCH /configs` 单独传合法 mode 时，经 `webMutator` 进入 Manager 模式用例。GLOBAL 的既有 PUT 路由进入相同持久选择路径。
- TUN 既有处理保持不变；拒绝未知键、托管端口/controller/secret 字段和非法类型。
- 混合 mode 与 tun 的请求不得变成两个独立、可能部分成功的 mutation。本次两套面板的模式操作均为 mode-only；混合请求在任何副作用前拒绝，测试固定此边界。
- 不能把 `PUT /configs` 的完整配置重载开放为任意写入。本次新增 mode 能力仅适配 PATCH。
- 面板另行发送的连接关闭请求保持现有语义，不能用“保留连接”屏蔽合法的独立操作。

## 3. 按顺序实施的任务

每个行为先写会因为目标行为缺失而失败的测试，再写最小实现。新增 API 可先落类型和空实现让测试编译，红灯必须来自断言失败；编译错误不算 Red。

### 任务 1：受管模式与设置

文件：`internal/config/settings.go`、新增邻近 routing 设置文件/测试、`internal/subscription/generator.go`、`internal/core/config.go`。

- 首个红灯：已有生成器接收订阅 `mode: global`，默认 Mihari settings 时结果应为 rule；该测试可直接运行现有代码，无需新标识符。
- 增加枚举、可选 routing 块、默认值、克隆与校验。
- 证明订阅与 overrides 的 mode 不覆盖受管模式，且保留其他订阅字段和原输入对象。
- 覆盖旧 settings 不含字段的读取、新状态往返保存、非法数据、GLOBAL map 深复制和 bootstrap。
- 更新已有 generator override-mode 测试到新的明确契约，不删除仍有意义的 overrides 其他字段断言。

验证：`go test ./internal/config ./internal/subscription ./internal/core`。

### 任务 2：Manager 模式用例与事务失败

文件：新增 `internal/runtime/routing.go` / `routing_test.go`，按需修改 `settings.go`、`manager.go` 和配置提交路径。

- 使用注入 fake controller/settings saver，先证明在线模式切换读回及持久保存。
- 覆盖 mode 枚举、revision 冲突、operation 重复、无变化选择、已知停止的 pending、controller 超时与明确拒绝。
- 对每个提交边界注入失败，验证旧状态保留、补偿失败 degraded、同步 warning 不回滚与脱敏日志只记一次。
- 验证切换没有调用关闭连接或无关 TUN 字段写入。

验证：`go test ./internal/runtime -run 'TestRouting'`，通过后运行 runtime 全包。

### 任务 3：GLOBAL 持久选择与生命周期恢复

文件：`internal/runtime/manager.go`、`subscription.go`、新 routing 恢复实现与测试、`internal/app/runtime.go`、必要的核心启动装配及相应集成测试。

- `SelectProxy` 的 GLOBAL 分支验证当前内核候选，使用现有 mutation 入口保存当前订阅的选择；其他组行为不变。
- A/B 订阅分别保存出口，切回恢复各自记录；相同名称也不跨订阅复用记录。
- 无记录、候选消失、DIRECT 缺失、自定义 GLOBAL、无活动订阅都通过真实返回数据驱动 fake 测试。
- 回退结果保存后，即使原节点重新出现也不自动恢复。
- 刷新、切换、删除、启用/停用订阅、TUN 导致的配置重载、daemon 启动及核心自动重启逐一覆盖。
- 同时测试 trusted 与普通配置路径；不能绕过 trusted capability 的校验、发布和回滚。
- 注入暂停点验证准备之后的 subscription/settings/core 变化会阻止陈旧提交。重启恢复不能依赖 TUI/CLI 打开页面。
- 覆盖 daemon 在提交各阶段中断后的重新装配；重启后的模式和出口必须由已提交的 source authority 决定。

验证：目标 runtime/app 测试及 `go test ./internal/integration`。

### 任务 4：本地协议与 CLI

文件：新增 `internal/control/protocol/routing.go`、server/client 的 routing 适配与测试、capabilities、`internal/cli/proxy.go` 及 CLI 测试。

- GET/PATCH 接口验证精确 JSON、unknown fields、非法 mode、body 限制、revision 与 operation ID。
- 可选 capability 缺失时展示明确的不支持，旧客户端的其他功能不退化。
- CLI 查询显示保存/实际差异，写入走类型化 client；GLOBAL 选择仍使用既有命令。
- 文本、JSON、失败退出码、非法额外参数和无 daemon 场景都有断言。

验证：`go test ./internal/control/... ./internal/cli`。

### 任务 5：Web gateway

文件：`internal/web/server.go`、`internal/app/runtime.go` 中 webMutator、相应单元/集成测试。

- 先将 mode-only PATCH 的既有拒绝断言改为目标成功行为，确认 Red，再接入 Manager。
- 固定 zashboard/MetaCubeXD 使用的 mode PATCH 与 GLOBAL PUT 请求 fixture，验证由同一 owner 保存并回传实际结果。
- 断言 mode+未知字段、mode+TUN、非法 mode、null/错误类型以及不允许的 PUT 在产生副作用前拒绝。
- 覆盖上游拒绝、settings 保存失败、面板重读状态、TUI/CLI 可观察相同选择，以及独立连接关闭不受影响。

验证：`go test ./internal/web ./internal/app ./internal/integration`。

### 任务 6：TUI 状态同步与 Mode 弹窗

文件：`internal/tui/session`、根消息转发、`internal/tui/pages/proxies`、`internal/tui/ui/keymap.go`、相应 golden/行为测试。

- Session 按 capability 获取 routing 状态并携带重连代际；迟到旧请求不能覆盖新 daemon 的状态。
- 代理快照失败立即撤销候选的新鲜性，保留必要的旧显示但禁止提交 GLOBAL 选择；不能因列表还在屏幕上就认为候选有效。
- 顶部两行入口进入焦点模型并从代理组视口扣除高度；GLOBAL 行定位既有列表，不重复渲染候选组。
- Enter 在 Mode 行打开页面内选择弹窗；上下键预选、Enter 应用、Esc 取消并恢复焦点。页面内弹窗优先接管输入。
- 使用统一 Dialog/Title/Muted/RowFocus/RowSelected；当前生效与保存待生效标记独立于焦点。
- pending 防重复提交；成功后关闭，失败保留上下文并可重试。Rule/Direct 下选择 GLOBAL 不切模式。
- 接入 HelpMode/FooterHints；保持全局退出、现有测速、组展开、节点选择和滚动行为。
- 检查 72×22 Compact、100×28 Full、较大终端、长中英文名称、离线、pending/error。实际查看渲染产物并根据项目主题调整留白与对齐。

验证：先 Proxies 目标测试，再 `go test ./internal/tui/...`，golden 与实际渲染检查同做。

### 任务 7：文档与最终验证

同步 `README.md` / `README.zh-CN.md`、相关命令文档和 `docs/architecture.md`，说明：

- 三种模式、GLOBAL 的 DIRECT 与 Direct 模式的区别。
- 模式优先级、按订阅出口记忆和持久回退。
- CLI 命令、Mode 弹窗与 pending/unknown 状态。
- 面板 mode 请求统一持久化，以及面板自己的关闭连接选项。
- 新 settings 字段的降级兼容影响。

最后按风险执行并记录实际结果：

```console
go test ./...
go test -race ./...
go vet ./...
gofmt -l .
```

跨平台构建覆盖 Windows/Linux/macOS 的 amd64 与 arm64，使用 `CGO_ENABLED=0`，输出到不提交的临时目录。Windows race 所需工具链若不可用，记录限制而不声称通过；不安装系统依赖来绕过未授权环境修改。

检查 git diff 只包含本任务，保留原仓库未提交修改，不加入测试二进制/coverage/临时图片，不修改 CHANGELOG。只有真实执行过的测试标记通过。

## 4. 实施记录（2026-09-13）

任务 1–7 已完成。新增测试覆盖保存与克隆、模式生成优先级、在线/停止状态、超时读回、回滚失败的 degraded、revision 冲突、操作去重、按订阅恢复与持久回退、订阅删除回滚、TUN 外层事务保留回退、CLI/面板/IPC 共用状态，以及 72×22 / 100×28 的 TUI 布局、弹窗按键和迟到结果。fake mihomo 集成验证 runtime YAML 模式过期时的重启恢复；没有连接真实订阅或真实内核。

本地验证通过：`go test ./...`、`go test -race ./...`、`go vet ./...`；末轮事务修正后再次通过全仓普通测试及 config/runtime/app/integration/TUI 的 race。Linux 目标 `golangci-lint run ./...` 零问题，修改的 Go 文件 `gofmt -l` 无输出，`git diff --check` 通过。Windows/Linux/macOS × amd64/arm64 的 `CGO_ENABLED=0` 构建通过，产物保存在仓库外临时目录。Windows lint 对既有 Unix 专用 `maxGeoResourceBytes` 报 unused，未扩展修改该无关代码。

用户已追加授权提交、推送、创建指向 dev 的 PR，并跟进 CI 与可执行的 bot review；不自动合并。

2026-09-14 同步 `dev` 的 `04ee706`（provider 节点目录与 HTTP 错误诊断）：候选快照通过完整 ProxyCatalog 保留元数据和重名提示，同时绑定 routing revision/订阅；provider 失败保留页面并撤销 GLOBAL 选择权限。新增组合回归覆盖元数据与 revision 同时返回、失败页面保留 Mode、错误快照只发布一次且不阻断 Rules/Routing 更新。
