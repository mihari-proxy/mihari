# Mihari 代码质量治理路线图

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:writing-plans to create each phase implementation plan, then use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to execute it. Do not implement a phase directly from this roadmap.

**Goal:** 分四个阶段关闭当前审计发现，并把 Mihari 的安全、测试、平台生命周期和可维护性治理转化为可持续、可验证的工程门禁。

**Architecture:** 路线按依赖关系推进：先修复发布阻断风险并恢复测试信号可信度，再收紧外部输入边界，然后补齐运行时与平台可观测性，最后基于稳定历史数据治理复杂度、性能和覆盖率。Roadmap 只定义阶段边界与门禁；每个阶段必须另有独立实施计划、独立分支、独立评审和实际验证证据。

**Tech Stack:** Go 1.26.5、标准库测试与 fuzz、golangci-lint v2.12.2、`govulncheck`、GitHub Actions、Go coverage profile、Windows/Linux/macOS CGO-free 构建。

**Spec:** `docs/code-quality-audit-2026-08-13.md`

## 1. 治理原则

- daemon 单写入者、统一 mutation coordinator、本地 named pipe/Unix socket、loopback controller 和 Web gateway 隔离属于不可破坏的架构不变量。
- 不以提高分数或覆盖率数字替代行为正确性；新增或修改的可观察行为必须有回归测试。
- 安全边界默认 fail closed；下载、归档、checksum、超时、路径与权限异常不得静默降级。
- 每个 Phase 在独立非 `main` 分支和 worktree 中执行；保留用户已有修改，不混入邻近重构。
- 每个发现形成可独立审查的提交；文档、生产修复、CI 变更不得无边界地堆入单一 commit。
- 不新增依赖、不改变公开 CLI/JSON 契约、不改变持久化格式、不调整安全或跨平台范围，除非另行说明影响并取得用户确认。
- 所有阶段均采用 Red–Green–Refactor；纯文档和机械格式化除外。
- 不把未运行的命令写成通过；真实 mihomo、真实订阅和系统服务操作仍需用户单独授权。
- 一期结束前不新增大批 linter 规则；后续 linter 必须先观察告警质量，再逐条进入 required gate。
- coverage 相对门禁至少需要 10 次成功 `main` workflow；固定阈值至少需要 30 次可比数据并单独审核。

## 2. 总体阶段图

```text
代码质量审计基线
        │
        ▼
Phase 1：发布风险与质量门禁
  AQ-01 · AQ-04 · AQ-05
        │  产出可信 test/coverage 信号
        ▼
Phase 2：外部输入与网络安全
  AQ-02 · AQ-03 · 漏洞/安全静态检查
        │  收紧所有不可信输入边界
        ▼
Phase 3：运行时、平台与可观测性
  service · supervisor · sysproxy · tundetect
        │  建立失败状态与三平台证据
        ▼
Phase 4：可维护性与数据驱动治理
  渐进拆分 · 稳定等待 · benchmark · coverage gate
        │
        ▼
持续治理与周期复审
```

Phase 1 是其他阶段的硬前置：测试与 coverage 信号不可信时，不应据此批准更大范围重构或启用数字门禁。Phase 2 和 Phase 3 在 Phase 1 完成后可以分别规划，但默认仍顺序交付，避免安全边界与平台生命周期修改共享过大的审查面。Phase 4 必须等待前述行为边界稳定，并满足 coverage 数据前置条件。

## 3. 状态与文档索引

| Phase | 主题 | 状态 | 详细计划 | 核心发现 |
|---|---|---|---|---|
| 1 | 发布风险与质量门禁 | 已验收 | [2026-08-13-code-quality-phase-1.md](2026-08-13-code-quality-phase-1.md) | AQ-01、AQ-04、AQ-05 |
| 2 | 外部输入与网络安全 | 待一期验收后规划 | 一期完成后创建 `2026-08-13-code-quality-phase-2.md` | AQ-02、AQ-03 |
| 3 | 运行时、平台与可观测性 | 待二期验收后规划 | 二期完成后创建 `2026-08-13-code-quality-phase-3.md` | 平台覆盖、后台错误回收 |
| 4 | 可维护性与数据驱动治理 | 等待稳定基线与历史数据 | 三期完成且数据条件满足后创建 `2026-08-13-code-quality-phase-4.md` | 大文件、固定等待、性能与 coverage |

状态只允许使用：`待规划`、`已规划，待实施`、`实施中`、`已验收`、`阻塞`。更新状态时必须链接对应计划、PR 或验证记录；不能仅凭代码已写完标记“已验收”。

Phase 1 状态列为 `已验收`。此状态仅在本 closure commit 的 required jobs 全部 green 后生效。CI-1（实现 HEAD `b06d990`）已在 PR https://github.com/mihari-proxy/mihari/pull/73 上全绿：run https://github.com/mihari-proxy/mihari/actions/runs/31722030062 （lint、unit Windows/Linux/macOS、test、race、vet-format、coverage、六目标 build、cross-build）以及 DCO runs https://github.com/mihari-proxy/mihari/actions/runs/31722030115 与 https://github.com/mihari-proxy/mihari/actions/runs/31722024459。

## 4. Phase 1：发布风险与质量门禁

### 目标

关闭特权自更新的内容身份缺口，并恢复默认测试与 coverage workflow 的稳定可信信号。

### 范围

- AQ-01：self updater 在替换二进制前强制校验同一 Release 的 `SHA256SUMS.txt`。
- AQ-04：CLI 无参数测试显式传递非 nil 空切片，使 coverage instrumentation 不再继承测试进程参数。
- AQ-05：配置首次初始化等待者观察最终设置文件，不再全部串行争抢创建锁。
- 完成 lint、vet、format、默认测试、race、coverage、安装脚本测试和六目标 CGO-free 构建验收。

### 非目标

- 不实现签名 checksum manifest 或内置发布公钥。
- 不处理 panel ZIP 总展开量和 release client 超时。
- 不启用 coverage 百分比门槛，不扩展大批 golangci-lint 规则。
- 不拆分 TUI、Web、Panel 大文件。

### 进入条件

- 在独立 worktree 与非 `main` 分支执行。
- 审计报告和一期详细计划已获用户确认。
- 保存 AQ-04、AQ-05 的失败或历史复现证据，避免失去 Red 基线。

### 交付物

- 三个独立整改单元及其回归测试。
- 更新后的审计整改状态与实际验证记录。
- 可成功生成并解析的 coverage profile。

### 退出条件

- AQ-01 checksum 缺失、重复、畸形、目标缺失或 digest 不匹配时均在替换前失败；旧 binary 不变，`AfterReplace` 不执行。
- 默认 `go test -count=1 ./...` 连续两次通过；不得用 `-p 1` 替代。
- CI 同款 `-coverpkg=./...` 命令连续三次通过并生成可解析 profile。
- `go test -race ./...`、`go vet ./...`、`golangci-lint run ./...`、`gofmt -l .` 和六目标 CGO-free 构建通过。
- 审计报告保留原始失败证据，并记录实际 commit 与验证结果。

### 一期本地证据（2026-08-14，Windows/amd64）

| 项 | 记录 |
|---|---|
| 工作树 HEAD | `34f7649`（复测起点，Task 3） |
| Task 0 | `cb1d086` `docs: 建立代码质量治理路线图` |
| Task 1 / AQ-05 | `8c27ed2` `fix: 修复配置初始化锁队列超时` |
| Task 2 / AQ-04 | `6180790` `test: 稳定无参数 CLI 覆盖率测试` |
| Task 3 / AQ-01 | `34f7649` `fix: 校验自更新二进制摘要` |
| lint fix | `1647385` `fix: 消除 checksum 解析的 S1017` |
| merge main + fixture | `f88d9b6` / `b06d990` `test: 为服务同步自更新补 checksum fixture` |
| PR | https://github.com/mihari-proxy/mihari/pull/73 |
| CI-1 HEAD | `b06d990` |
| CI-1 ci.yml | https://github.com/mihari-proxy/mihari/actions/runs/31722030062 全绿 |
| `gofmt -l .` | 1 次，exit 0 |
| `go vet ./...` | 1 次，exit 0 |
| `golangci-lint run ./...` v2.12.2 | 1 次，exit 1；`internal/update/self.go:235` S1017 |
| `go test -count=1 ./...` | 2 次，exit 0 / 0；未使用 `-p 1` |
| `go test -count=1 -race ./...` | 1 次，exit 0 |
| installer `test_parallel_download.py -v` | 1 次，exit 0；6 测 2 跳过 |
| `-coverpkg=./...` + coverage-gate report | 3 次，exit 0；74.13% / 74.13% / 74.12% |
| `CGO_ENABLED=0` 六目标构建 | 6 次，exit 0；产物仅在 `%TEMP%\mihari-phase1-build` |
| 远端 CI | 无；三 OS unit / lint / coverage / cross-build 待验证 |

AQ-02、AQ-03 仍开放。一期状态列为 `已验收`，仅在本 closure commit 的 required jobs 全部 green 后生效。

## 5. Phase 2：外部输入与网络安全

### 目标

为 panel 下载、归档和依赖供应链建立明确、有上限、可中止的 fail-closed 边界。

### 范围

- AQ-02：panel archive 限制总展开字节、最大 entry 数和目录深度；同时校验元数据声明与实际写入量。
- AQ-03：panel release metadata 默认使用带明确 timeout 的私有 HTTP client，并继续传播调用方 context。
- 在不访问公网的测试中覆盖压缩炸弹、条目洪泛、伪造 size、阻塞 handler、deadline 和 staging 清理。
- 引入 `govulncheck ./...` 的 CI 观察任务；先记录工具版本、数据库可用性和当前结果，再决定是否 required。
- 以观察模式评估 `gosec`、`bodyclose`、`noctx`、`errorlint`；只把与 Mihari 边界相关且低误报的规则写入 `.golangci.yml`。

### 非目标

- 不更换 panel 格式或新增第三方归档库。
- 不把所有 HTTP client 强制统一为一个全局 client。
- 不因工具告警批量重构不相关代码。
- 不连接真实 panel 上游或公网漏洞数据库作为默认单元测试的一部分。

### 进入条件

- Phase 1 已验收，默认测试与 coverage 信号稳定。
- Phase 2 详细计划明确具体常量、错误语义、staging 清理策略和 linter 观察流程，并经用户批准。

### 交付物

- panel archive 总量/数量/深度限制和确定性回归测试。
- panel release 默认超时和阻塞服务器测试。
- `govulncheck` 观察 job 与记录格式。
- linter 候选规则评估表：规则、命中数、真阳性、豁免原因、是否进入门禁。

### 退出条件

- 超限 archive 在 promote 前失败并清理 candidate；路径穿越与 symlink 原有保护不退化。
- 默认 metadata 请求在规定时间内结束，上层 context 的更短 deadline 优先。
- `govulncheck` 能稳定运行并区分“发现可达漏洞”“数据库/网络不可用”“工具执行失败”。
- 新启用 linter 规则为零未解释告警；所有 exclusion 都有窄范围和原因。
- 全仓验证与 Phase 1 门禁继续通过。

## 6. Phase 3：运行时、平台与可观测性

### 目标

让关键后台组件和平台生命周期失败可发现、可分类、可测试，同时补齐当前低覆盖平台边界的行为证据。

### 范围

- 为 `internal/service`、`internal/supervisor`、`internal/sysproxy`、`internal/tundetect` 的关键成功和失败路径补平台专用或开发集成测试。
- 审查 Web gateway、scheduler、listener、goroutine、子进程和句柄的 owner、cancel、wait、close 路径。
- Web gateway 与 scheduler 的非取消错误进入既有日志或 daemon snapshot；正常 context cancellation 不产生错误状态。
- 为 Windows named pipe、Unix socket、服务状态查询和进程停止路径补与当前平台相称的 CI 证据。
- 对不可在默认 CI 安全执行的 testenv 场景建立清晰清单、前置条件和回滚步骤，但不自行执行。

### 非目标

- 不安装或修改真实系统服务，不切换真实系统代理/TUN。
- 不新建通用事件总线或大型可观测性子系统。
- 不改变 `/v1` DTO；若 daemon snapshot 需要公开新字段，必须另行设计协议版本或保持为内部日志状态。
- 不为了覆盖率强行 mock 操作系统所有 API。

### 进入条件

- Phase 2 已验收，外部输入边界稳定。
- 详细计划先列出每个平台可在默认 CI、安全 testenv、人工验证三层执行的场景。
- 任何新增日志或状态字段均完成 secret/token 泄漏审查。

### 交付物

- 平台关键路径测试矩阵和对应测试实现。
- 后台组件错误上报及 cancellation 降噪测试。
- 资源生命周期审计表：资源、owner、停止条件、错误回收、覆盖测试。
- 明确隔离的 testenv backlog，不混入默认 `go test ./...`。

### 退出条件

- 关键后台组件意外退出可通过日志或内部状态发现，且不泄漏 secret。
- 默认测试结束后不遗留 listener、goroutine、子进程、临时 socket 或句柄。
- 三平台 CI 各自验证适用的路径；平台不可执行项有明确记录而非笼统标记通过。
- race、静态检查、coverage 观察和六目标构建继续通过。

## 7. Phase 4：可维护性与数据驱动治理

### 目标

在行为与门禁稳定后，渐进降低高复杂度热点，消除脆弱等待，并用真实性能与覆盖率数据建立长期治理机制。

### 范围

- 按行为单元渐进拆分大型 TUI、Web、Panel 和 runtime 文件；每次只拆一个可独立审查的责任边界。
- 将测试中的固定 `time.Sleep` 替换为 channel、waiter 或带 deadline 的条件轮询。
- 为 subscription parse/generate、connection stream decode、ZIP validation 和 TUI 大列表过滤/渲染建立少量 benchmark。
- 在至少 10 次成功 `main` coverage workflow 后评审相对回归门禁；在至少 30 次可比数据后才评审固定覆盖率阈值。
- 建立季度或版本节点复审：开放风险、linter exclusions、依赖漏洞、覆盖率波动、benchmark 趋势和超大文件热点。

### 非目标

- 不进行一次性全仓重构或以文件行数为唯一拆分指标。
- 不为普通 CRUD 或低频路径凑 benchmark。
- 不通过无断言测试、排除生产文件或降低统计口径提高覆盖率。
- 不在数据条件不足时预设固定阈值。

### 进入条件

- Phase 1–3 已验收。
- coverage workflow 至少已有 10 次成功、同工具链、同 `-coverpkg=./...` 口径的 `main` 样本。
- 每个拆分子任务先有行为测试保护和明确责任边界。

### 交付物

- 每个复杂度热点的独立设计/实施计划与行为等价证据。
- 无固定 sleep 的关键并发/生命周期测试。
- 可重复 benchmark 基线与运行说明。
- coverage 观察数据、波动分析和经用户批准的门禁决策。
- 周期复审模板与首份复审记录。

### 退出条件

- 被拆分组件职责和依赖方向更清晰，公开行为、协议和测试结果不变。
- benchmark 能在固定输入下稳定运行并识别显著回归，不设置脱离数据的绝对性能指标。
- 相对 coverage 门禁仅在数据支持时启用；固定阈值若获批准，有至少 30 次数据和独立审核记录。
- 全部既有质量门禁继续通过，且无无关格式化或大范围重写。

## 8. 与既有测试治理 Phase A–D 的关系

既有 [`2026-08-12-test-governance-roadmap.md`](2026-08-12-test-governance-roadmap.md) 已覆盖控制协议/WebSocket、mutation/平台生命周期、CI/coverage 和 bounded fuzz。该路线图不是重复实施，而是把已建立的测试能力作为代码质量治理基础：

| 既有测试治理能力 | 本路线图如何消费 |
|---|---|
| Phase A：控制协议与 WebSocket | 所有质量修复必须保持 `/v1` 契约和流式生命周期测试通过 |
| Phase B：Mutation 与平台生命周期 | Phase 3 在既有边界测试上补失败可观测性和平台缺口，不重写 mutation 测试 |
| Phase C：CI 与 coverage | Phase 1 先修复 coverage 失效；Phase 4 等待既定 10/30 次数据门槛 |
| Phase D：Bounded fuzz | Phase 2 扩充 archive 边界时复用 bounded fuzz 原则，不把公网或无限资源引入 fuzz |

若两份路线图发生冲突，以根 `AGENTS.md` 当前治理状态、稳定协议约束和更严格的数据门槛为准；不得通过新 roadmap 提前绕过既有授权与观察期。

## 9. 阶段执行与评审流程

每个 Phase 按以下顺序推进：

1. 从最新目标基线创建独立 branch/worktree，核对用户未提交修改。
2. 使用 `superpowers:brainstorming` 确认该 Phase 的具体设计和非目标。
3. 使用 `superpowers:writing-plans` 创建独立详细实施计划，并由用户审核。
4. 按计划逐任务执行 Red–Green–Refactor；每个发现独立 commit。
5. 运行阶段最小验证、邻接包验证和全量退出门禁。
6. 使用 `superpowers:requesting-code-review` 做完成前审查，并按 `receiving-code-review` 核实反馈。
7. 更新审计报告和本路线图状态，记录实际 commit、验证命令和未验证项。
8. 用户确认后再合并；不得在 roadmap 批准时推定获得 commit、push、PR 或 merge 授权。

## 10. 风险、停止条件与回滚原则

- 若修复需要改变公开协议、持久化格式、依赖或安全边界，立即停止该任务并请求用户批准扩展范围。
- 若同一问题连续三次修复尝试仍失败，停止继续打补丁，回到架构假设和根因审查。
- 若 Phase 内出现与范围无关的问题，只记录到审计 backlog；除非阻塞当前门禁，不并入该 Phase。
- 生产修改必须能通过 revert 对应独立 commit 回滚；数据格式和迁移不得成为隐式回滚前提。
- CI 观察任务在工具基础设施故障时应明确报告不可用，不得将其伪装为“无漏洞”或“0 issues”。
- 任一 Phase 退出门禁失败时，状态保持“实施中”或“阻塞”，不得进入下一 Phase。

## 11. 总体完成定义

- AQ-01 至 AQ-05 均关闭并保留回归测试；所有低/改进项均已处理或由明确决策接受。
- 特权更新、外部下载、归档、HTTP deadline、平台生命周期和后台错误均具有可验证的安全边界。
- 默认测试、race、vet、format、lint、coverage 观察和六目标 CGO-free 构建形成稳定门禁。
- 三平台关键路径有与风险相称的自动化证据，testenv 场景与默认测试严格隔离。
- 高复杂度热点按责任渐进治理，关键等待不依赖脆弱固定 sleep，并建立少量真实性能基线。
- coverage 门禁只基于满足 10/30 次前置条件的历史数据，不通过人为提高数字损害测试质量。
- 审计报告、阶段计划、实际提交、验证证据和剩余风险之间可双向追踪。
