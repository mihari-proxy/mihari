# Mihari 代码质量审计报告（2026-08-13）

## 1. 执行摘要

本次审计覆盖仓库提交 `717cc05`（分支 `docs/code-quality-audit-2026-08-13`），同时检查生产代码、单元测试、开发集成测试、模糊测试、安装脚本测试及 CI 配置。审计以静态阅读、CodeGraph 调用链分析、覆盖率采样和本机动态验证相结合；不包含真实 mihomo、真实订阅、系统服务安装或公网端到端验证。

**综合评价：7.4/10（良好，但存在必须优先处理的供应链风险和测试门禁缺口）。**

仓库的主要优势是架构边界明确，daemon 单写入者、版本化本地控制协议、mutation coordinator、候选文件验证和回滚等关键不变量大体落实；测试体量充足，race、vet、gofmt、lint 及六目标 CGO-free 构建均通过。主要不足集中在：自更新二进制未做内容完整性校验、面板解压缺少总展开量限制、面板 GitHub 元数据请求缺省无超时、CI 使用的全仓覆盖率命令可重复触发 CLI 测试失败，以及 Windows 默认全仓并行负载下配置初始化锁可重复超时。后续复测还表明普通 `go test ./...` 并非稳定健康，因此测试门禁可靠性低于初次采样结论。

一期本地整改已落地 AQ-01、AQ-04、AQ-05；AQ-02、AQ-03 仍开放。CI-1 在 PR https://github.com/mihari-proxy/mihari/pull/73 HEAD `b06d990` 上全绿（run https://github.com/mihari-proxy/mihari/actions/runs/31722030062）。roadmap 状态 `已验收` 仅在本 closure commit 的 required jobs 全部 green 后生效。

### 风险概览

| 等级 | 数量 | 主题 |
|---|---:|---|
| 高 | 1 | 特权自更新未校验下载内容 |
| 中 | 4 | zip 总展开量、面板元数据超时、覆盖率 workflow 失败、配置初始化锁超时 |
| 低/改进 | 4 | 大文件复杂度、平台覆盖、测试并行度、错误回收可观测性 |

## 2. 范围与方法

### 2.1 审计范围

- `cmd/mihari/` 与 `internal/` 全部 Go 生产代码。
- 全部 `*_test.go`、`internal/integration/` 和 4 个 fuzz target。
- `scripts/install/test_parallel_download.py`、构建辅助程序和 coverage gate。
- `.github/workflows/`、`.golangci.yml`、`go.mod`。
- 重点调用链：daemon/IPC 生命周期、订阅配置事务、状态协调、mihomo supervisor、Web gateway、外部下载、归档解压、自更新与跨平台实现。

### 2.2 规模快照

| 指标 | 数值 |
|---|---:|
| Go 文件 | 354 |
| 生产 Go 文件 / 行数 | 200 / 30,506 |
| 测试 Go 文件 / 行数 | 154 / 29,709 |
| Go package | 48 |
| `Test*` 函数 | 900 |
| fuzz target | 4 |
| benchmark / example | 0 / 0 |
| 测试代码与生产代码行数比 | 0.97 |

测试代码体量接近生产代码，是明显的正向信号；但行数本身不能替代边界、失败路径和生产装配覆盖。

## 3. 动态验证结果

| 命令 | 结果 | 说明 |
|---|---|---|
| `go test ./...`（初次审计） | 通过 | 48 个 package；无真实公网依赖 |
| `go test -count=1 ./...`（补充复测） | **失败（2/2）** | 两次均为 `internal/config.TestConcurrentLoadOrCreateUsesOneControllerSecret` 在 10 秒后超时 |
| `go test -p 1 -count=1 ./...` | 通过 | 串行 package 对照组通过，表明默认包级并行负载是触发条件 |
| 目标配置测试单独重复 25 次 | 通过 | `-count=5` 与 `-count=20` 两组均通过 |
| `go test -race ./...` | 通过 | Windows/amd64；补充复测同样通过 |
| `go vet ./...` | 通过 | 无诊断 |
| `gofmt -l cmd internal scripts` | 通过 | 无输出 |
| `golangci-lint run ./...` | 通过 | v2.12.2，0 issues |
| Python 并行下载测试 | 通过 | 6 个测试，Windows 上 2 个 POSIX 信号用例按设计跳过 |
| `CGO_ENABLED=0` 六目标构建 | 通过 | Windows/Linux/macOS × amd64/arm64 |
| `go test -coverprofile=... ./...` | 通过 | 总语句覆盖率 73.9%（仅各包自身覆盖） |
| CI 同款 `-coverpkg=./...` | **失败** | `internal/cli` 的 `TestExecute_NoArgsInteractiveRunsTUI` 返回 exit 2；重复执行可复现 |

`-coverpkg=./...` 失败后不会生成完整 profile，因此本次不能把 73.9% 当作 CI 的全仓跨包覆盖率基线。失败证据本身应作为测试治理问题修复。

### 3.1 一期本地复测（2026-08-14，Windows/amd64，Go 1.26.5，HEAD `34f7649`）

以下为 Task 4 在本工作树实际运行的命令。未运行的远端 CI 不记为通过。

| 命令 | 次数 | 退出码 | 说明 |
|---|---:|---:|---|
| `gofmt -l .` | 1 | 0 | 无未格式化文件 |
| `go vet ./...` | 1 | 0 | 无诊断 |
| `golangci-lint run ./...`（v2.12.2） | 1 | **1** | `internal/update/self.go:235` staticcheck S1017：`should replace this if statement with an unconditional strings.TrimPrefix`；1 issue。按 Task 4 范围未改生产 Go |
| `go test -count=1 ./...` | 2 | 0 / 0 | 未使用 `-p 1`；`internal/config` 与 `internal/cli` 均通过 |
| `go test -count=1 -race ./...` | 1 | 0 | Windows/amd64 |
| `python scripts/install/test_parallel_download.py -v` | 1 | 0 | 6 个测试，2 个 POSIX 信号用例按设计跳过 |
| `go test -count=1 -covermode=atomic -coverpkg=./... -coverprofile=... ./...` | 3 | 0 / 0 / 0 | 三次均生成可解析 profile |
| `go run ./scripts/coverage-gate report -profile ...` | 3 | 0 / 0 / 0 | 74.13%（9480/12788）、74.13%（9480/12788）、74.12%（9478/12788）；观察期方差，未启用百分比门槛 |
| `CGO_ENABLED=0` 六目标 `go build -trimpath` | 6 | 0 | Windows/Linux/macOS × amd64/arm64；产物仅在 `%TEMP%\mihari-phase1-build`，仓库无 binary；`CGO_ENABLED`/`GOOS`/`GOARCH` 已恢复 |

覆盖率 profile 路径与大小：`%TEMP%\mihari-phase1-final-1.out` 701142 字节、`...-2.out` 701148 字节、`...-3.out` 701136 字节。

## 4. 分维度评价

| 维度 | 评分 | 评价 |
|---|---:|---|
| 架构与包边界 | 8.5/10 | 分层和不变量清晰，控制协议、runtime、持久化及平台边界基本匹配文档 |
| 正确性与事务 | 8.0/10 | 订阅采用 prepare/commit/reload/rollback；revision 和对象版本均有重检 |
| 安全与供应链 | 6.0/10 | 权限、secret 隔离、响应上限整体良好，但自更新完整性缺口严重 |
| 并发与生命周期 | 7.0/10 | race 通过，goroutine 多有 owner/cancel/wait；配置初始化锁在全仓负载下形成超时，部分后台错误被静默丢弃 |
| 可维护性 | 6.8/10 | 类型化接口良好，但 TUI/Web/Panel 出现 778–2,134 行生产文件 |
| 测试质量 | 7.4/10 | 数量、隔离和错误路径覆盖较强；默认全仓测试与 coverage workflow 均存在可复现失败，平台边界仍薄弱 |
| CI/CD 与跨平台 | 8.0/10 | 三 OS 单测、Windows race、六目标构建、lint/vet/fuzz 齐全，但两条关键测试路径不稳定 |
| 可观测性与错误语义 | 7.2/10 | 协议错误码和脱敏较好；scheduler/Web gateway 启动错误缺乏上报 |

## 5. 主要发现

### AQ-01（高）：特权自更新下载的可执行文件没有完整性校验

**证据：** `internal/update/self.go:111-147,220-252` 仅依据 release API 的 asset 名称和大小下载文件，随后直接 `replaceBinary`；下载路径没有读取 `SHA256SUMS.txt`、校验 asset digest 或验证签名。与之对照，`internal/core/install.go:223-263` 已实现 `sha256:<hex>` 校验，release workflow 也会生成 `SHA256SUMS.txt`。

**影响：** `mihari self update` 和 TUI 更新要求管理员/root 权限。一旦 release 元数据、资产存储、重定向链或上游发布账户被攻破，未经验证的字节会替换高权限运行的 Mihari 可执行文件，形成直接代码执行供应链路径。TLS 只能保护传输，不能提供发布物内容身份。

**建议：** 发布 checksum 资产后，自更新必须先解析受信 release 中的 checksum 清单并对目标资产做 SHA-256 比对；更稳妥的长期方案是对清单签名并内置验证公钥。任何校验缺失、重复条目、资产名歧义或 digest 不匹配都应 fail closed，并增加篡改回归测试。

**状态（2026-08-14）：** 本地整改完成。commit `34f7649`（`fix: 校验自更新二进制摘要`）与 `1647385`（S1017）。本机 Windows/amd64 上该行为随全仓两次 `go test -count=1 ./...`、一次 `-race`、三次 `-coverpkg=./...` 通过。CI-1 见 §11.3。

### AQ-02（中）：panel zip 只限制单文件，未限制总展开大小和条目数

**证据：** `internal/panel/archive/zip.go:15-19,49-100,115-143` 将压缩包限制为 128 MiB、单个展开文件限制为 64 MiB，但遍历全部 entry 时没有累计展开字节或条目数。`internal/panel/service.go:667-681` 只限制下载字节；测试覆盖单文件过大、路径穿越和 symlink，但没有多文件 zip bomb 用例。

**影响：** 一个由大量小于 64 MiB 文件组成的高压缩率归档可耗尽磁盘、inode 或长时间占用 daemon，影响更新、持久化及服务可用性。输入通常来自受信 panel 上游，因此评为中风险而非高风险。

**建议：** 在读取前累计 `UncompressedSize64`，同时在实际 copy 时累计真实写入量；设置总展开字节、最大 entry 数和可选目录深度上限，超限时删除整个 staging candidate。增加“许多合法小文件但总量超限”和伪造 size 元数据测试。

**状态：** 仍开放。不在一期范围。

### AQ-03（中）：panel release 元数据的默认 HTTP client 没有超时

**证据：** `internal/panel/release/github.go:43-48` 在没有注入 client 时返回 `http.DefaultClient`；生产装配 `internal/app/runtime.go:125-130` 使用 `zashboard.New(nil, "")` 和 `metacubexd.New(nil, "")`。因此 resolve latest 的 GitHub API 请求没有 client deadline。面板资产下载本身在 `panel.Service` 中有 2 分钟超时，但元数据发现阶段没有。

**影响：** 当 GitHub/API 连接建立后持续不返回响应时，panel install/update/reinstall 操作可能无限等待，持有 operation 条目并长期占用调用方；仅依赖上层 context 是否设置 deadline，不能满足库默认安全边界。

**建议：** `release.Client` 缺省使用带明确 timeout 的私有 `http.Client`，或要求调用方显式注入；同时保持 request context 传播。增加一个阻塞 handler 的 deadline 回归测试。

**状态：** 仍开放。不在一期范围。

### AQ-04（中）：CI 覆盖率命令可重复触发 CLI 测试失败

**证据：** `.github/workflows/ci.yml` 的 coverage job 执行 `go test -count=1 -covermode=atomic -coverpkg=./... -coverprofile="$profile" ./...`。本机连续两次执行均在 `internal/cli/runtime_test.go:20-31` 的 `TestExecute_NoArgsInteractiveRunsTUI` 失败（`exit=2 called=false`），而普通 `go test ./...` 和该测试单独重复 20 次均通过。测试向 `Execute` 传入 `nil` 参数；`Execute` 再调用 Cobra `SetArgs(args)`，nil 与明确空参数在 Cobra 中并非可靠等价，容易落回测试进程参数。

**影响：** coverage workflow 无法稳定产出 profile，观察期数据和后续相对回归门禁失真；如果 coverage job 被设为 required，会直接阻塞 PR。

**建议：** 测试无参数 CLI 时传入非 nil 空切片 `[]string{}`，并增加覆盖率命令的本地回归验证。修复后以 CI 完整命令至少重复运行数次，再开始累计 main 覆盖率观测数据。

**状态（2026-08-14）：** 本地整改完成。commit `6180790`（`test: 稳定无参数 CLI 覆盖率测试`）。本机 Windows/amd64 连续三次 CI 同款 `-covermode=atomic -coverpkg=./...` 命令 exit 0，profile 可被 coverage-gate 解析；`internal/cli` 三次均为 `coverage: 70.6% of statements`。无远端 CI 证据。

### AQ-05（中）：配置初始化锁在默认全仓并行负载下可重复超时

**证据：** 补充质量探测在 Windows/amd64、Go 1.26.5 上两次运行 `go test -count=1 ./...`，均由 `internal/config/settings_test.go:272-305` 的 `TestConcurrentLoadOrCreateUsesOneControllerSecret` 失败，错误来自 `internal/config/settings.go:172-191` 的 `acquireCreationLock`：`timed out waiting for settings initialization`。同一测试脱离全仓负载连续运行 25 次全部通过，`go test -p 1 -count=1 ./...` 与 `go test -race ./...` 也通过。

当前实现中，32 个调用方在首次观察到设置文件不存在后都会进入锁获取循环。首个调用方完成写入并释放锁后，其他调用方仍只竞争锁文件，不会在等待期间重新观察已经生成的设置文件，形成不必要的串行锁队列；默认全仓包级并行负载放大了 Windows 文件系统和调度延迟，使固定 10 秒上限可重复耗尽。

**影响：** 默认质量门禁在开发机和潜在 CI 高负载环境中出现非确定失败；同一锁路径也用于真实首次初始化，极端负载下可能向用户返回数据错误。简单增大超时只会降低复现概率，不能消除锁队列和无效等待。

**建议：** 把创建协调改为“重新观察最终设置文件 → 尝试获得创建锁 → 冲突时继续观察”的收敛循环。等待者一旦看到有效设置就直接返回，不再逐个获取和释放创建锁；锁拥有者在临界区内仍需二次检查后再生成 secret。保留总等待上限和 Windows `os.ErrPermission` 冲突兼容，并增加“锁仍存在但最终设置已就绪时等待者立即返回”的确定性回归测试。

**状态（2026-08-14）：** 本地整改完成。commit `8c27ed2`（`fix: 修复配置初始化锁队列超时`）。本机 Windows/amd64 默认 `go test -count=1 ./...` 连续两次通过，未使用 `-p 1`；`go test -count=1 -race ./...` 通过。无远端 CI 证据。

## 6. 生产代码审计

### 6.1 做得好的部分

- **控制平面边界明确。** CLI/TUI 通过 `internal/control/client`，本地控制服务位于 named pipe/Unix socket 抽象；controller secret 未直接暴露给浏览器。
- **稳定协议使用类型化 DTO。** `internal/control/protocol` 自身覆盖率为 100%，server/client 对响应大小、状态码、JSON 错误 envelope 和认证均有测试。
- **配置事务接近架构目标。** `internal/runtime/subscription.go` 在锁外下载、生成和验证候选文件，在锁内重检 profile version/revision，原子写入后 reload，失败时恢复旧配置并再次 reload；rollback 无法确认时发布 degraded 状态。
- **持久化保护较好。** 配置、catalog、credential 多使用 0600/0700 和原子写；临时文件位于目标数据树或明确 staging 目录。
- **外部数据大多有边界。** subscription、mihomo、GeoIP、panel asset、release JSON 均检查 HTTP status 和响应上限；GeoIP 同时校验 SHA-256 与 MMDB 格式。
- **归档路径防护扎实。** panel archive 拒绝绝对路径、`..`、Windows drive path 和 symlink；core zip 选择单一候选并限制解压后核心大小。
- **并发所有权总体清晰。** manager 对 Web gateway、scheduler 有 cancel/wait；control server 使用有界 shutdown；race 全仓通过。
- **secret 脱敏意识较强。** public subscription DTO 不包含 URL，错误通常转换为稳定信息；相关测试显式检查 token/URL 不泄漏。

### 6.2 可维护性债务

以下生产文件过大，已经超出“可一次理解和安全修改”的合理范围：

| 文件 | 行数 |
|---|---:|
| `internal/tui/pages/system/model.go` | 2,134 |
| `internal/tui/model.go` | 1,053 |
| `internal/tui/pages/subscriptions/model.go` | 954 |
| `internal/tui/pages/setup/model.go` | 843 |
| `internal/tui/pages/rules/model.go` | 834 |
| `internal/panel/service.go` | 795 |
| `internal/web/server.go` | 778 |
| `internal/runtime/manager.go` | 681 |

这些文件混合状态机、异步命令、渲染/HTTP 路由、生命周期和业务 mutation，增加回归风险与审查成本。建议按行为单元渐进拆分，而不是进行一次性大重构：例如 system page 拆为 update/service/network 子模型，Web server 拆为 auth/static/proxy/websocket handler，panel service 拆为 discovery/download/activation/catalog。

### 6.3 错误可观测性

`internal/runtime/manager.go:222-240` 和 `internal/app/runtime.go:192-237` 对 Web gateway 与 scheduler 返回错误采用 `_ = ...`。注释说明部分组件失败不应停止 supervisor，这个隔离策略合理，但完全丢弃错误会使 gateway 启动失败、scheduler 意外退出只能通过外部症状发现。建议把组件状态写入 daemon snapshot/日志，并避免把 context cancellation 当作告警。

## 7. 测试代码审计

### 7.1 优势

- 154 个测试文件、约 29.7k 行测试、900 个测试函数，生产/测试 LOC 接近 1:1。
- 大量使用 `t.TempDir()`、`httptest.Server`、注入 clock/runner/transport/fake，没有发现默认测试主动访问公网。
- `internal/integration/` 覆盖控制 IPC、runtime、Web gateway、sysproxy/TUN 等跨包路径，并使用 fake mihomo。
- runtime subscription 测试覆盖 reload 失败、catalog/cache rollback、并发 mutation 串行化和 stale revision/object identity。
- 每夜 bounded fuzz 覆盖 subscription document、userinfo、control JSON 和 archive path safety；failure corpus 上传前做大小与敏感模式检查。
- CI 在 Windows/Linux/macOS 跑普通测试，在 Windows 跑 race，并执行六目标 CGO-free 构建。

### 7.2 覆盖率解读

普通 per-package profile 的总语句覆盖率为 73.9%。高覆盖 package 包括 protocol 100%、transport 100%、daemon 95.7%、state 90.9%、mihomo 85.4%、subscription 80.1%；相对薄弱的是 `tundetect` 14.1%、`cmd/mihari` 20.4%、`elevate` 47.4%、`sysproxy` 47.8%、`service` 57.1%、`supervisor` 64.2%。profile 中有 195 个 0% 函数，其中较多集中在 TUI 页面、runtime、sysproxy、supervisor 和 service。

低覆盖并不自动代表缺陷：OS API、进程启动和 main 边界天然更难做普通单元测试。但这些包恰好承载跨平台与生命周期风险，后续应优先通过可注入的小接口、平台专用 CI 测试和开发集成测试补足，而不是追求无断言覆盖。

### 7.3 测试设计改进

- 全仓只有 15 处 `t.Parallel()`；当前 900 个测试执行时间可接受，但纯函数、DTO、parser 和独立临时目录测试可谨慎并行化。
- 有 6 处固定 `time.Sleep`。多数等待时间仅 1–50ms，当前 race 通过，但 Web/integration 的 readiness 更适合 channel、poll-with-deadline 或 waiter，降低慢 CI 偶发失败。
- 没有 benchmark。建议只为高频/高数据量路径增加基准：subscription YAML parse/generate、connection stream decode、TUI 大列表过滤/渲染、zip entry validation；无需为普通 CRUD 凑 benchmark。
- Windows 当前对 POSIX 安装脚本取消测试按设计跳过，依赖 Linux/macOS CI 补齐；审计文档应持续区分“本机未验证”和“CI 已覆盖”。
- 配置并发初始化测试揭示了锁队列在全仓负载下的时序脆弱性。后续并发回归测试应优先断言可观察条件和收敛行为；超时仅作为失败上限，不应把延长固定等待时间当作修复。

## 8. CI、依赖与工程治理

### 8.1 优势

- Go/toolchain 与 golangci-lint 版本固定；lint、vet、format、race、三 OS test、六目标 build、fuzz 和 DCO 均进入自动化。
- `CGO_ENABLED=0` 和目标矩阵符合架构不变量。
- coverage 采用观察期而非过早写死阈值，方法合理。
- fuzz artifact 做敏感数据筛查，release/retract workflow 有版本 gate 和显式确认。

### 8.2 改进建议

- 先修复 AQ-04，再把 coverage job 的成功运行作为观测数据有效性的前置条件。
- 增加依赖漏洞扫描（如 `govulncheck ./...`）和 workflow/action 更新策略；本次未联网执行漏洞数据库查询，因此不对依赖 CVE 状态下结论。
- release checksum 已生成，应把它接入 AQ-01 的 updater 验证闭环，而不是只供人工下载校验。
- 当前 lint 的 errcheck exclusions 对多类 Close/cleanup 做全局豁免；保持豁免清单最小，并对关键持久化/替换路径显式处理 close/sync 错误。

## 9. 建议整改路线图

全部阶段的依赖、范围和退出门禁见 [`docs/superpowers/plans/2026-08-13-code-quality-roadmap.md`](superpowers/plans/2026-08-13-code-quality-roadmap.md)；一期的任务拆分、Red–Green 步骤和验收命令见 [`docs/superpowers/plans/2026-08-13-code-quality-phase-1.md`](superpowers/plans/2026-08-13-code-quality-phase-1.md)。一期边界是发布阻断风险与质量门禁可信度，不包含 AQ-02、AQ-03 或大规模 lint 规则扩张。

### P0：立即（合并下一版本前）

1. 修复 AQ-01：自更新校验 SHA-256（最好进一步采用签名清单），补篡改、缺失 checksum、资产名歧义测试。
2. 修复 AQ-04：让 CI coverage 命令稳定通过，重新开始有效观测期。
3. 修复 AQ-05：消除创建锁队列并增加确定性收敛测试，恢复默认全仓测试可信度。

### P1：近期（1–2 个迭代）

1. 修复 AQ-02：panel archive 总展开量、entry 数和深度限制。
2. 修复 AQ-03：panel release client 默认超时及阻塞回归测试。
3. 为 `service`、`supervisor`、`sysproxy`、`tundetect` 的关键失败路径补平台测试/集成测试。
4. 将 Web gateway/scheduler 非取消错误写入状态或日志。

### P2：持续治理

1. 按责任渐进拆分超大 TUI/Web/Panel 文件，每次拆分保持行为测试不变。
2. 把可事件驱动的固定 sleep 测试改为 waiter/channel。
3. 为 parser、stream 和大列表渲染建立少量性能基线。
4. 在至少 10 次成功 main coverage workflow 后启用相对回归门禁；固定阈值仍按治理规则等待至少 30 次数据。

## 10. 结论

Mihari 当前不是“低质量代码库”：其架构约束、类型化协议、事务式配置更新、测试密度和跨平台 CI 都明显高于一般早期 Go 项目。静态检查、race 与跨平台构建健康，关键状态 mutation 的设计尤其扎实；但普通全仓测试和 coverage 路径尚未形成可信的稳定门禁。

“管理员权限自更新却不校验下载内容”与项目安全目标不相容，应作为发布前最高优先级问题；coverage workflow 与配置初始化锁的可复现失败也会阻断既定测试治理路线。完成 AQ-01 至 AQ-05 后，仓库可进入较稳健的 8+/10 区间；随后重点应从增加测试数量转向平台边界、生产装配、失败可观测性和复杂度控制。

一期行为修复已在独立 commit 中落地（见 §11）。S1017 已由 `1647385` 消除。CI-1 在 PR #73 HEAD `b06d990` 上全绿。

## 11. 一期本地整改记录（2026-08-14）

平台：Windows/amd64。工具：Go 1.26.5、golangci-lint v2.12.2、Python 3.13.11。工作树分支 `docs/code-quality-audit-2026-08-13`。PR https://github.com/mihari-proxy/mihari/pull/73。未合并进 main、未开始二期。

### 11.1 整改 commit

| 任务 | Commit | 主题 |
|---|---|---|
| Task 0 | `cb1d086` | `docs: 建立代码质量治理路线图` |
| Task 1 / AQ-05 | `8c27ed2` | `fix: 修复配置初始化锁队列超时` |
| Task 2 / AQ-04 | `6180790` | `test: 稳定无参数 CLI 覆盖率测试` |
| Task 3 / AQ-01 | `34f7649` | `fix: 校验自更新二进制摘要` |
| lint fix | `1647385` | `fix: 消除 checksum 解析的 S1017` |
| merge + fixture | `b06d990` | `test: 为服务同步自更新补 checksum fixture` |

### 11.2 发现状态

| 发现 | 状态 | 依据 |
|---|---|---|
| AQ-01 | 本地整改完成 | `34f7649` + `1647385`；本机默认测试两次、race、coverage 三次通过；S1017 已消除 |
| AQ-04 | 本地整改完成 | `6180790`；本机三次 CI 同款 `-coverpkg=./...` 命令与 coverage-gate report 均 exit 0 |
| AQ-05 | 本地整改完成 | `8c27ed2`；本机默认 `go test -count=1 ./...` 连续两次通过，未使用 `-p 1` |
| AQ-02 | 仍开放 | 不在一期范围 |
| AQ-03 | 仍开放 | 不在一期范围 |

### 11.3 CI-1（HEAD `b06d990`）

- PR：https://github.com/mihari-proxy/mihari/pull/73
- ci.yml：https://github.com/mihari-proxy/mihari/actions/runs/31722030062
- 已绿：lint、unit (windows/ubuntu/macos)、test、race、vet-format、coverage、build 六目标、cross-build
- DCO：https://github.com/mihari-proxy/mihari/actions/runs/31722030115 、https://github.com/mihari-proxy/mihari/actions/runs/31722024459
- 安装脚本 2 个 POSIX 信号用例在 Windows 上按设计跳过。
