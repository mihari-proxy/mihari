# Mihari 测试覆盖完善与治理设计

日期：2026-08-12  
状态：已批准；Phase A–D、计划内 CI、提交、推送与 PR 均已授权实施  
目标分支：`docs/test-coverage-governance-design`

## 1. 背景

本设计基于 2026-08-12 对仓库当前测试状态的审计。审计在 Windows amd64、Go 1.26.5 环境中完成，实际执行了默认测试、原子覆盖率、跨包覆盖率、race detector 与 vet。

当前基线如下：

| 指标 | 基线 |
|---|---:|
| Go package | 46 |
| 生产 Go 文件 | 190 |
| 测试 Go 文件 | 149 |
| 顶层 `TestXxx` | 778 |
| 常规包内语句覆盖率 | 72.0% |
| `-coverpkg=./...` 跨包语句覆盖率 | 74.6% |
| 跨包口径零覆盖函数 | 159 |
| 关键架构包零覆盖函数 | 54 |
| fuzz target | 0 |
| benchmark | 0 |

以下验证通过：

```console
go test -count=1 -covermode=atomic -coverpkg=./... ./...
go test -count=1 -race ./...
go vet ./...
```

现有测试已经较好保护以下架构不变量：

- 配置原子替换失败时保留上一份有效文件；
- mutation coordinator 串行提交并拒绝陈旧 revision；
- 下载期间删除订阅后，迟到结果不能重建已删除对象；
- reload 失败时回滚订阅激活和运行时配置；
- Web gateway 对未知写操作默认拒绝；
- Web credential 与 mihomo controller secret 相互隔离；
- supervisor 的崩溃退避、健康检查失败、显式重启和取消退出；
- 本地控制协议的主要 DTO 与错误 envelope。

主要缺口不是测试数量不足，而是稳定契约和高风险边界的覆盖不对称：订阅 `/v1` 只有部分端点被直接验证，WebSocket 成功代理链未覆盖，部分 app/runtime/panel mutation 只在单层测试中出现，平台生命周期测试偏弱，CI 也没有持续记录覆盖基线。

## 2. 目标

本设计建立一套风险优先、渐进收紧的测试治理体系：

1. 补齐稳定 `/v1` 控制协议的端点、客户端和错误映射矩阵。
2. 直接验证 WebSocket 认证、secret 注入、双向转发和关闭行为。
3. 覆盖所有公开 mutation 的 coordinator、revision、operation ID、失败回滚和敏感信息清理语义。
4. 在 Windows、Linux 和 macOS 原生运行与平台相关的测试，而不只做交叉编译。
5. 让 CI 持续生成可比较的跨包覆盖报告，并阻止无理由退化。
6. 对不可信输入边界增加 bounded fuzz，使解析器和解码器持续接受异常输入检验。
7. 保留真实 mihomo、真实订阅、系统服务和权限场景为显式授权的 testenv 验证。

完成后，测试结果应能回答“关键契约是否被验证、失败能否安全回滚、平台资源是否正确关闭”，而不仅是给出一个总覆盖率数字。

用户已明确授权完整实施 Phase A–D、计划内 CI、必要的最小生产修复、提交、推送与 PR，并要求修复 Actions 直到全部必需检查通过。各阶段仍保持独立实现与评审切分。Git hooks、testenv、固定覆盖率门槛及计划外治理仍须单独授权；相对覆盖率门禁须等待设计规定的观察期数据。

## 3. 非目标

- 不以提升总覆盖率百分比为目的给简单 getter、生成代码或不可达平台分支堆叠低价值测试。
- 第一阶段不设置武断的全仓固定百分比，也不要求每个包达到相同门槛。
- 不修改 `/v1` DTO、错误码、JSON envelope、CLI 退出码或持久化格式。
- 不为测试引入第三方 assertion、mock、覆盖率 SaaS 或 fuzz 框架；优先使用 Go 标准库和 GitHub Actions 原生能力。
- 不在默认测试中访问公网、真实订阅、真实用户目录、真实 mihomo 或系统服务管理器。
- 不借测试完善重构无关业务代码。只有无法建立确定性测试边界时，才允许引入最小注入点。
- benchmark 不在本轮治理范围内；只有出现明确性能预算后再单独设计。

## 4. 治理原则

### 4.1 风险矩阵优先于总覆盖率

总覆盖率用于发现趋势，不能替代关键行为断言。高风险路径即使所在包已超过 80%，只要没有直接验证，仍视为未完成。低风险包装函数即使为 0%，也不自动成为最高优先级。

### 4.2 每个稳定契约需要双侧测试

本地控制协议的每个公开端点至少包含：

- server handler 测试：HTTP 方法、路径、认证、输入限制、DTO 和错误码；
- typed client 测试：请求方法与路径、授权头、请求 JSON、成功响应和 API error 解码；
- 对关键跨包行为再增加 IPC 集成测试，而不是给所有端点重复第三层测试。

### 4.3 失败路径与成功路径同等重要

涉及文件、网络、进程和 mutation 的测试必须同时考虑：

- 准备阶段失败；
- revision 在准备后变陈旧；
- 提交或 reload 失败；
- context 取消；
- 部分成功后的资源清理；
- 错误文本不泄露 token、secret 或完整订阅 URL。

### 4.4 确定性优先

并发测试使用 channel、fake clock、waiter 或明确事件同步，不依赖固定 `time.Sleep`。端口使用临时 listener，文件使用 `t.TempDir()`，HTTP 使用 `httptest.Server`，所有 goroutine、listener、response body 和子进程都由测试拥有并在 `t.Cleanup` 中回收。

## 5. 测试层级与职责

### 5.1 单元测试

单元测试与实现放在同包相邻的 `*_test.go` 中，负责一个包内的输入、输出、错误分类和资源所有权。新增测试优先扩展现有 fake，而不是建立全仓 mock 框架。

主要目标包：

- `internal/control/server`
- `internal/control/client`
- `internal/runtime`
- `internal/app`
- `internal/web`
- `internal/subscription`
- `internal/supervisor`
- `internal/platform`、`internal/service`、`internal/sysproxy`、`internal/elevate`

### 5.2 开发集成测试

`internal/integration` 只验证跨包外部可观察行为和架构不变量：IPC 生命周期、daemon 单写入者、完整 mutation 链、Web gateway 与 fake mihomo。它不重复每个 handler 的表驱动输入校验。

集成测试覆盖到其他包的语句必须通过 `-coverpkg=./...` 计入治理报告。普通 `go test ./...` 的逐包百分比仍保留用于本地定位，但不作为仓库总覆盖率口径。

### 5.3 testenv

以下场景不进入默认测试：

- 真实 mihomo 二进制与 controller；
- 真实订阅 URL；
- Windows SCM、systemd、launchd 的安装和卸载；
- 真实系统代理、TUN 和权限提升；
- 真实 GitHub Release 下载与自更新。

未来 testenv 必须使用显式 build tag 或独立命令、隔离数据目录、测试凭据和可执行回滚步骤。没有用户授权时，Agent 和 CI 均不得运行。

## 6. 风险覆盖矩阵

### 6.1 P0：稳定控制协议

第一批补齐订阅 `/v1` 的对称矩阵。

| 能力 | Server | Client | IPC 集成 | 必须断言 |
|---|---|---|---|---|
| list | 补齐 | 已有/核对 | 不要求 | URL 不出现在响应中、revision 正确 |
| show | 补齐 | 补齐 | 不要求 | not found 映射、secret-free DTO |
| add | 已有 | 已有/核对 | 保留现有链路 | operation ID、proxy mode、URL 仅进入请求 |
| refresh | 补齐 | 已有/核对 | 保留现有链路 | revision、运行时错误映射 |
| use | 补齐 | 已有/核对 | 已有运行时集成 | reload 失败回滚 |
| enable | 补齐 | 补齐 | 不要求 | 显式 `false`、stale revision |
| update | 已有部分 | 补齐 | 不要求 | pointer 字段保留显式空值与 `false` |
| remove | 补齐 | 补齐 | 增加一条跨 IPC 场景 | 删除后迟到 refresh 不得复活对象 |

同一方法还应覆盖以下公共 handler 行为：错误或超大 JSON、未知字段、缺失 operation ID、错误认证、runtime capability 缺失，以及底层错误到稳定协议错误码的映射。

### 6.2 P0：WebSocket gateway

为 `internal/web.Server.proxyWebSocket` 增加真实的本地双向 WebSocket 测试：

1. 启动 `httptest.Server` 作为 fake controller，并校验收到的 `Authorization: Bearer <controller-secret>`。
2. 启动 Mihari Web gateway，浏览器侧只提交 Web credential，不得接触 controller secret。
3. 浏览器发送文本或二进制帧，fake controller 回显，断言两个方向内容和消息类型保持一致。
4. 上游主动关闭时，gateway 退出复制循环并关闭客户端连接。
5. 客户端主动关闭或 context 取消时，上游连接和两个复制 goroutine 可回收。
6. 上游握手返回 401/500 或无法连接时，gateway 返回安全错误，不反射 secret 和上游响应体。

测试不得依赖公网、固定端口或固定 sleep。每条测试结束后检查 listener 和 handler goroutine 均已退出。

### 6.3 P1：mutation 完整性

本节所述 panel runtime/coordinator、revision、回滚与 mutation 语义属于阶段 B。阶段 A Task 2 仅提前补齐 panel typed client wrapper 的传输契约测试，范围限于方法、路径、认证、请求 JSON 和响应解码；这些 wrapper 测试不代表 runtime mutation 语义已经覆盖。

为公开 mutation 建立统一清单，至少覆盖以下当前薄弱路径：

- app：选择代理、关闭单个/全部连接、配置 patch；
- runtime：panel update/uninstall/reinstall、subscription enable、连接关闭；
- control client：panel update/activate/rollback/uninstall/reinstall 的 wrapper 契约在 Phase A 覆盖，runtime mutation 语义在 Phase B 覆盖；
- web mutation：允许的写操作、拒绝的未知写操作和稳定错误响应。

每项 mutation 按适用性验证：

- operation ID 在同一 coordinator 中幂等；
- `IfRevision` 在慢 IO 前后均不允许陈旧提交；
- 下载、校验、解压等慢 IO 在提交锁外；
- mutation 的唯一写入者是 daemon runtime；
- reload/发布失败恢复上一份有效状态；
- 返回的 revision 是提交后的 revision；
- API、日志和测试错误不含敏感值。

不要求所有 mutation 都做端到端测试。一个路径只有在跨越 IPC、coordinator、文件或 fake mihomo 三个以上边界时才进入 `internal/integration`。

### 6.4 P1：平台与生命周期

平台测试分为可注入逻辑和真实 OS 原生实现两类。

可注入逻辑在所有平台运行：

- service 状态与错误映射；
- sysproxy owned/foreign/off 分类；
- supervisor 启动、终止、kill fallback 和 wait；
- endpoint/path 选择与权限意图；
- context 取消后的资源清理。

原生实现只在对应 runner 运行：

- Windows named pipe、job object/child termination、WinINET 数据映射的非破坏性部分；
- Linux Unix socket、路径和权限位、进程组终止；
- macOS Unix socket、路径和 `networksetup` 命令构造。

默认 CI 不安装服务、不修改系统代理。涉及系统状态的实现通过 command runner、registry/backend 或 filesystem 接口注入 fake，原生测试只验证不产生外部持久副作用的行为。

### 6.5 P2：输入边界 fuzz

第一批 fuzz target 限定为纯函数或完全内存边界：

| Target | 种子来源 | 不变量 |
|---|---|---|
| 订阅 YAML/节点解析 | 现有有效与无效 fixture | 不 panic；多文档、异常 tag 和超深结构安全失败 |
| panel archive entry/path 校验 | 正常路径、绝对路径、`..`、链接名称 | 不允许目录穿越、绝对路径或链接逃逸 |
| control JSON request 解码 | 现有 DTO JSON、未知字段、重复字段、超长 token | 不 panic；遵守大小上限与 unknown-field 策略 |
| subscription-userinfo 解析 | 正常 header、溢出、负值、乱码 | 不 panic；非法值不产生错误配额 |

fuzz 输入全部为合成数据，不包含真实 URL、凭据或用户文件。默认 `go test ./...` 只运行 seed corpus；持续 fuzz 放在定时或手动 workflow，每个 target 使用明确时间预算。

## 7. CI 设计

### 7.1 PR 必需门禁

保留现有 lint 和六目标 CGO-free cross-build，并把测试拆为以下职责：

| Job | Runner | 命令 | 目的 |
|---|---|---|---|
| unit-windows | `windows-latest` | `go test -count=1 ./...` | Windows 原生行为与 named pipe |
| unit-linux | `ubuntu-latest` | `go test -count=1 ./...` | Linux 原生行为与 Unix socket |
| unit-macos | `macos-latest` | `go test -count=1 ./...` | macOS 原生行为与 Unix socket |
| race | `windows-latest` | `go test -count=1 -race ./...` | 并发与生命周期，并保持与当前 CI 基线一致 |
| vet-format | 现有 runner | `go vet ./...`、`gofmt -l .` | 静态与格式检查 |
| coverage | `ubuntu-latest` | 跨包 atomic coverage | 可比较的仓库总口径 |

原生测试矩阵与 cross-build 的职责不同：前者执行对应 build tag 的测试，后者证明 `CGO_ENABLED=0` 的六种发布构建仍成立。

### 7.2 覆盖率采集

coverage job 使用 Linux/bash，避免不同 PowerShell 原生参数解析影响报告路径：

```console
go test -count=1 -covermode=atomic -coverpkg=./... \
  -coverprofile="$RUNNER_TEMP/mihari-coverage.out" ./...
go tool cover -func="$RUNNER_TEMP/mihari-coverage.out"
```

CI 将 profile 和逐函数文本报告保存为短期 artifact，并把总覆盖率写入 job summary。覆盖文件不写入仓库，也不提交。

### 7.3 渐进门禁

门禁分三步启用：

1. **观察期**：连续至少 10 个成功的 `main` workflow 只采集报告，不因百分比失败，用于确认波动范围。
2. **防退化期**：PR 同时计算 merge base 与 HEAD 的相同口径覆盖率。总覆盖率下降超过 0.5 个百分点时失败；关键包下降超过 1.0 个百分点时失败。阈值吸收生成代码、平台 build tag 和小规模结构调整的正常波动。
3. **收紧期**：当关键矩阵全部完成后，才讨论是否为关键包设置最低值。任何固定阈值都必须基于至少 30 次 `main` 数据另行审核，不能在本设计实施时直接写死。

关键包集合固定为：

```text
internal/config
internal/control/client
internal/control/protocol
internal/control/server
internal/daemon
internal/runtime
internal/state
internal/subscription
internal/supervisor
internal/web
```

覆盖率下降不自动等同于缺陷。若语义等价重构导致下降但风险矩阵未退化，PR 可以通过显式说明和审查更新基线；不得用空断言测试恢复数字。

### 7.4 Fuzz workflow

fuzz 不进入每个 PR 的必需门禁。新增 nightly 和 `workflow_dispatch` 任务：

- 每个 target 独立执行，初始预算 60 秒；
- 设置 job timeout，防止 runner 无界占用；
- 失败时上传最小化输入和测试日志；
- crash corpus 经确认不含敏感数据后才能提交；
- fuzz 失败按测试失败处理，不自动修改 corpus 或生产代码。

## 8. 实施阶段

### 阶段 A：P0 契约与 WebSocket

交付订阅 server/client 对称矩阵、仅限传输层的 panel typed client wrapper 契约，以及 WebSocket 成功/失败/关闭测试。panel runtime 的 coordinator、revision、回滚和 mutation 语义留在阶段 B；该阶段不修改 CI 门槛，避免基础缺口与治理工具同时变化。

验收：目标 P0 行为全部有直接断言；相关包、集成测试、全仓 race 与 vet 通过。

### 阶段 B：P1 mutation 与平台生命周期

按包逐项补齐 mutation 风险清单，并增加 Windows/Linux/macOS 原生 unit job。必要的注入点与对应测试放在同一小提交中，不先建立通用 mock 层。

验收：所有公开 mutation 都能在清单中映射到测试；三平台原生默认测试通过；默认测试不产生系统持久副作用。

### 阶段 C：覆盖可观测性与防退化

增加 coverage job、artifact 和 summary，完成观察期后再启用相对基线比较。观察期数据与阈值启用应分别提交，便于独立回滚。

验收：PR 能看到跨包总覆盖和关键包变化；允许范围外的下降会阻断 CI；仓库内没有 coverage 临时产物。

### 阶段 D：P2 fuzz

每次只引入一个 fuzz target，先以固定 seed 回归测试证明目标不变量，再加入 bounded nightly fuzz。fuzz 发现的缺陷必须先固化为最小回归输入，再修复实现。

验收：四类目标均能在默认测试中运行 seed corpus，在定时 workflow 中按预算完成；失败输入可复现且不含敏感数据。

### 阶段 E：testenv 设计

真实环境验证是独立后续设计，不与 A–D 混合实施。该阶段需要用户明确授权测试平台、凭据、服务状态变更和回滚方案。

## 9. 提交与评审切分

测试完善按可独立审查的意图拆分，不创建“全仓补覆盖”巨型提交。建议顺序：

1. `test(control): 补齐订阅服务端契约矩阵`
2. `test(control): 补齐订阅与面板客户端契约`
3. `test(web): 覆盖 WebSocket 双向代理生命周期`
4. `test(runtime): 补齐 mutation 回滚与 revision 路径`
5. `test(platform): 补齐平台生命周期边界`
6. `ci: 增加三平台原生测试矩阵`
7. `ci: 记录跨包覆盖率基线`
8. `ci: 启用覆盖率防退化门禁`
9. 按 target 分开的 `test(fuzz): ...`

每个行为变更遵循 Red–Green–Refactor。纯 CI 报告或文档变更不要求先制造失败测试，但必须在分支上验证 workflow 语法和本地等价命令。

## 10. 风险与缓解

### 10.1 CI 时间增长

三平台测试、race、双份覆盖比较会增加 runner 时间。先并行运行三平台 unit；coverage merge-base 比较只在 PR 执行；fuzz 放到 nightly。若耗时仍不可接受，应基于 job 数据调整，而不是删除平台覆盖。

### 10.2 平台测试不稳定

端口、进程和 socket 测试最容易产生时序波动。测试必须依靠事件同步、临时资源与有界 context。出现 flaky test 时先定位所有权和退出条件，禁止简单增加 sleep 或无条件 retry。

### 10.3 覆盖率指标被游戏化

风险矩阵是完成条件，覆盖率只是退化告警。评审拒绝无断言测试、只调用 getter 的测试和为了数字排除生产文件的做法。

### 10.4 `-coverpkg=./...` 与平台 build tag 差异

仓库总口径固定在 Linux coverage job，保证 PR 间可比。Windows/macOS 的覆盖率只用于诊断，不与 Linux 百分比混合。平台关键行为由原生测试矩阵和显式风险清单保护。

### 10.5 测试为了可注入性扩大生产接口

接口定义在使用方附近并保持最小。优先注入函数或现有小接口；只有真实 OS/IO 边界需要替换时才新增接口，不创建通用 `utils` 或全局 service locator。

## 11. 完成定义

本治理路线完成需要同时满足：

- P0/P1 风险矩阵中的每一项都能指向具体测试名称；
- 订阅 `/v1` server/client 契约对称，错误映射与敏感字段规则有直接断言；
- WebSocket 成功代理、认证、secret 注入、上下游关闭和 context 取消均有确定性测试；
- 公开 mutation 的 coordinator、revision、幂等和回滚语义按适用性覆盖；
- Windows、Linux、macOS 原生默认测试进入 CI，六目标 CGO-free 构建保持通过；
- CI 生成统一跨包覆盖报告，并在观察期后阻止超出容差的无说明退化；
- 四类 fuzz target 具备 seed corpus 和有界定时执行；
- `go test ./...` 不访问公网、不使用真实用户目录、不依赖真实 mihomo 或系统服务；
- `go test -race ./...`、`go vet ./...` 与 `gofmt -l .` 通过；
- 没有提交 coverage profile、测试二进制、fuzz 临时产物或敏感输入。

总覆盖率不是单独的完成条件。完成定义以稳定契约、事务回滚、安全边界、平台生命周期和持续防退化能力为准。

## 12. 已选方案与否决方案

采用“风险优先 + 渐进门禁”：先补高风险盲区，再建立跨平台和覆盖率治理，最后引入 bounded fuzz。

未采用：

1. **立即设置统一全仓最低覆盖率**：不能反映包风险差异，容易诱导低价值测试，也与仓库当前渐进门槛原则冲突。
2. **只补当前 0% 函数**：函数覆盖不等于行为覆盖，且会把平台包装器、简单 getter 与稳定协议端点错误地视为同一优先级。
3. **只做审计发现的短期缺口**：不能阻止后续退化，也不能解决当前只有 Windows 原生测试和没有 fuzz 的治理空白。
4. **默认测试连接真实环境**：破坏隔离性、可重复性和安全边界，且违反仓库 testenv 授权规则。
