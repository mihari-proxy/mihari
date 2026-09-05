# Mihari 开发规范

## 1. 适用范围与指令优先级

- 本文件适用于整个仓库。子目录若新增 `AGENTS.md`，以离目标文件最近的规则为补充或覆盖。
- 开始任何开发之前先阅读 `.github/CONTRIBUTING.md`；修改代码前再阅读 `README.md`、相关包代码与测试；涉及架构边界时同时阅读 `docs/superpowers/specs/2026-08-03-mihari-architecture-design.md`。
- 禁止在 `main` 或 `dev` 分支上直接修改或提交。任何变更都必须在独立分支中完成，验证通过并经用户确认后再通过 PR 合并。
- 指向 `dev` 的功能 PR 不得修改 `CHANGELOG.md`。CHANGELOG 只由正式发版的 `chore/release-*` PR 收口，或随 `main → dev` 同步回来。
- `dev` 创建并受保护前，Issue #115 的 bootstrap 变更仅可通过一次性 main PR 落地；该例外不允许直接修改或提交 `main`。
- 保留用户已有的未提交修改，不覆盖、不清理、不顺手重构无关代码。
- 只做当前任务要求的变更。发现邻近问题时记录并说明，除非它直接阻塞当前任务，否则不要扩大范围。
- 新增依赖、改变持久化格式、修改公开 CLI/JSON 契约、调整安全边界或改变跨平台支持范围前，必须先说明影响并取得用户确认。

## 2. 项目目标与不可破坏的架构约束

Mihari 是面向 Windows、Linux 和 macOS 的 mihomo 本地管理器。它使用单个、无 CGO 依赖的 Go 可执行文件，提供 daemon、CLI，并为后续 TUI 与浏览器面板共享同一控制平面。

以下约束视为架构不变量：

- daemon 是持久化状态与 mihomo 生命周期的唯一所有者和写入者。
- CLI、TUI 及其他本地客户端只通过 `internal/control/client` 和版本化本地控制协议访问 daemon，不直接修改 daemon 管理的业务文件。
- **窄例外**：TUI 仅可通过 `internal/logging` 确保共享日志目录存在，并向固定 `mihari-tui.log*` 序列追加/轮转；不得扩展为 settings、订阅、token、面板或其他业务状态写入。settings 与业务状态仍只有 daemon/Manager 可写。
- 本地控制 API 使用 Windows named pipe 或 Unix domain socket，不得退化为 TCP 监听。
- mihomo controller 仅绑定 loopback；浏览器不得获得 controller 地址或 secret。
- 所有 Web 面板的 REST、WebSocket 与写操作必须经过 Mihari Web gateway 和统一 mutation coordinator；未知写操作默认拒绝。
- 长耗时下载、解压和校验在提交临界区外执行；提交时重新检查对象身份或 revision，避免陈旧结果覆盖新状态。
- 配置写入遵循“生成候选文件 → 校验 → 原子替换 → reload → 失败回滚”，不得直接覆盖最后一个有效配置。
- `internal/control/protocol` 的 `/v1` DTO、错误码、JSON envelope 与 CLI 退出码属于稳定契约。语义破坏需要新协议版本。
- 平台专用实现通过小接口及 `_windows.go`、`_unix.go`、`_linux.go`、`_darwin.go` 等文件隔离；通用文件不得散布平台分支。
- 发布构建保持 `CGO_ENABLED=0`，目标覆盖 Windows、Linux、macOS 的 amd64 与 arm64。

## 3. 包边界

- `cmd/mihari/`：只负责依赖装配、启动和进程退出，不承载业务逻辑。
- `internal/app/`：与 CLI、TUI、HTTP 表现层无关的用例编排。
- `internal/daemon/`：组件生命周期、启动顺序和优雅关闭。
- `internal/control/protocol/`：稳定的本地协议 DTO、版本与错误码。
- `internal/control/server/`：协议解析、认证、校验和到运行时接口的适配。
- `internal/control/client/`：CLI/TUI 共用的类型化本地客户端。
- `internal/runtime/`：运行时 mutation 编排与跨域事务边界。
- `internal/mihomo/`：mihomo REST/WebSocket 适配，不向上泄露传输细节。
- `internal/supervisor/`：子进程、健康检查、重启与退避。
- `internal/subscription/`：订阅模型、缓存、下载、生成、刷新与切换。
- `internal/config/`、`internal/state/`：校验、revision 与原子持久化。
- `internal/platform/`：路径、权限和操作系统差异。
- `internal/cli/`：Cobra 命令、文本/JSON 渲染和退出码映射；不得成为业务实现层。
- `internal/integration/`：跨包开发集成测试及 fake mihomo。

依赖应朝领域与接口方向流动。接口通常定义在使用方附近，并保持为测试所需的最小方法集合。不得制造通用 `utils` 包来隐藏不清晰的职责。

## 4. Go 编码规范

### 4.1 基础规则

- 遵循 `go.mod` 固定的 Go 与 toolchain 版本；除非任务明确要求，不调整版本。
- 所有修改过的 Go 文件必须通过 `gofmt`。import 分组交给 Go 工具处理，不手工维持特殊排序。
- 包名简短、全小写、表意明确；导出标识符必须有符合 Go 文档习惯的注释。
- 优先使用零值可用、职责单一的小类型。避免无语义的 `map[string]any`，协议与配置使用明确 DTO。
- 不为单一实现提前抽象；只有存在真实边界、替换需求或测试隔离需求时才引入接口。
- 函数保持单一职责。解析、校验、IO、状态提交和输出渲染应能独立理解与测试。

### 4.2 错误处理

- 所有错误都必须处理。只有在语义上明确安全时才可忽略返回值，并写出原因。
- 用 `%w` 包装错误并补充操作上下文；调用方需要分类时使用哨兵错误、类型化错误及 `errors.Is`/`errors.As`。
- 错误信息描述失败的操作和对象，但不得包含凭据、controller secret、订阅 URL token 或敏感配置内容。
- 库包不调用 `os.Exit`、`log.Fatal` 或 panic 处理可恢复错误。进程退出只在 `cmd/mihari` 边界完成。
- 面向协议和 CLI 的错误必须经过既有错误码与退出码映射，不把底层实现文本当作稳定 API。

### 4.3 Context、并发与生命周期

- 可能阻塞的网络、进程、IPC、流式读取和长任务必须接收并传播 `context.Context`；不要保存 context 到结构体。
- 启动 goroutine 的代码必须明确其终止条件、所有者和错误回收路径。禁止无所有者的后台 goroutine。
- channel 由发送方或生命周期所有者关闭；不要用关闭 channel 代替不清晰的所有权设计。
- 共享可变状态必须有单一所有者、串行 mutation 路径或明确锁保护。锁内不执行网络、磁盘、进程等待等慢 IO。
- 并发逻辑优先通过事件、channel、可注入时钟或 waiter 测试，避免依赖固定 `time.Sleep` 的脆弱测试。
- 所有 stream、listener、response body、文件和子进程句柄都必须有清晰、可验证的关闭路径。

### 4.4 配置、文件与安全

- 所有路径通过 `internal/platform` 或明确注入获得；测试不得读写真实用户目录。
- 持久化写入使用同目录临时文件、必要的同步与原子替换，并保留最后一个有效版本用于回滚。
- 创建文件、目录、Unix socket 和凭据时使用最小权限；Windows 使用等价 ACL 语义。
- 外部下载必须设置超时和大小上限，校验 HTTP 状态；归档解压必须拒绝绝对路径、路径穿越和链接逃逸。
- 日志、事件、默认 CLI 输出和测试失败信息都不得泄露 secrets、tokens 或完整订阅 URL。
- 新网络监听默认绑定 loopback；任何扩大暴露面的行为都需显式设计与用户批准。

## 5. 测试驱动开发流程

行为变更采用 Red–Green–Refactor：

1. 先写一个能表达目标行为、并因缺少该行为而失败的测试。
2. 运行最小测试范围，确认失败原因正确，而不是编译错误或测试本身错误。
3. 写使测试通过的最小实现。
4. 再运行目标包及相关集成测试。
5. 在测试保持通过的前提下重构，最后按风险扩大验证范围。

缺陷修复必须包含回归测试。纯注释、纯文档或无法改变行为的机械格式化可不先写失败测试，但仍需执行与风险相称的验证。

### 5.1 第一阶段：单元测试

- 测试与实现放在同包邻近的 `*_test.go`，名称使用 `TestXxx_Condition` 或清晰的子测试名。
- 一次测试只证明一个行为；多组同构输入使用表驱动测试。
- 使用 `t.TempDir()`、`t.Setenv()` 和 `t.Cleanup()` 隔离文件、环境变量与资源。
- HTTP 边界使用 `httptest.Server` 或注入 transport；命令执行、时钟、随机源和等待器通过小接口或函数注入。
- 不访问公网、不依赖已安装的 mihomo、不读写用户真实配置、不依赖测试执行顺序。
- mock/fake 记录与断言必要交互即可，不复制生产实现。fixture 名称表达业务语义，避免 `data1`、`mock2`。

### 5.2 第二阶段：开发集成测试

- 跨包控制流、IPC、daemon 生命周期、事务回滚和 fake mihomo 场景放在 `internal/integration/`。
- 可使用 loopback 随机端口、临时 Unix socket/named pipe、临时目录和仓库内 fake；不得要求外部服务或真实用户数据。
- 集成测试验证外部可观察结果及关键不变量，不依赖未公开内部字段。
- 测试失败后必须清理 listener、goroutine、子进程和临时文件，且不得遗留后台 daemon。

### 5.3 第三阶段：真实环境验证

- 真实 mihomo、真实订阅、平台服务安装、升级和权限场景属于 testenv 验证，不混入默认 `go test ./...`。
- 这类验证必须由用户明确授权，并使用隔离的测试凭据、目录和主机。Agent 不得自行连接真实订阅或修改系统服务。
- 后续建立 testenv 时，应使用显式 build tag、独立命令或专用环境开关，并清楚记录前置条件与回滚步骤。

## 6. 覆盖率策略

当前采用渐进门槛，不预设固定百分比：

- 新增或修改可观察行为必须有对应测试；关键错误路径、回滚路径和安全边界同样需要覆盖。
- 变更不得无故降低相关包及全仓覆盖率基线。若结构调整导致指标下降，必须说明原因并证明行为覆盖未退化。
- 不以无断言测试、只调用不验证的测试或排除生产文件来抬高数字。
- 覆盖率用于发现遗漏，不替代测试质量判断。固定总覆盖率和逐包门槛待后续测试审计后确定。
- 本地生成的 `coverage.out`、测试二进制和临时报告不得提交。

## 7. 验证命令

按改动风险从小到大执行；先运行最小范围，再扩大范围：

```console
go test ./internal/<package>
go test -run '^TestName$' ./internal/<package>
go test ./internal/integration
go test ./...
go test -race ./...
go vet ./...
gofmt -l .
```

覆盖率按需检查：

```console
go test -coverprofile=coverage.out ./...
go tool cover -func=coverage.out
```

跨平台相关变更至少验证当前平台测试，并对受影响目标执行无 CGO 编译检查，例如：

```console
$env:CGO_ENABLED = '0'
$env:GOOS = 'windows'; $env:GOARCH = 'amd64'; go build -o bin/mihari-windows-amd64.exe ./cmd/mihari
$env:GOOS = 'linux';   $env:GOARCH = 'amd64'; go build -o bin/mihari-linux-amd64 ./cmd/mihari
$env:GOOS = 'darwin';  $env:GOARCH = 'arm64'; go build -o bin/mihari-darwin-arm64 ./cmd/mihari
```

不要声称“已通过”未实际运行的命令。若因平台、权限或外部环境无法执行，明确列出未验证项及原因。

## 8. 变更检查清单

提交或交付前确认：

- 改动遵守 daemon 单写入者、统一 mutation path 和本地控制协议边界。
- 新行为先有测试，缺陷有回归测试，错误与回滚路径得到验证。
- 无公网测试、真实用户目录访问、凭据泄露、固定端口冲突或不可控 goroutine。
- 协议、JSON、退出码、配置格式和持久化语义没有被意外破坏。
- 平台代码与构建标签配对，通用代码可在三个目标 OS 编译，且保持 CGO-free。
- 修改过的 Go 文件已格式化，相关测试、race 检查和 vet 已按风险执行。
- README、命令帮助或架构文档在用户可见行为变化时同步更新。
- 指向 `dev` 的功能 PR 未修改 `CHANGELOG.md`（仅 `chore/release-*`、`main → dev` 同步，或恢复成与 `main` 一致时例外）。
- `git diff` 中不含无关文件、临时产物或对用户既有修改的覆盖。

## 9. Commit 规范

- commit 只允许在 `main` 或 `dev` 之外的分支创建；`main` 或 `dev` 上的变更必须经分支 PR 合并完成，不在 `main` 或 `dev` 分支创建 commit。
- 仅在用户明确要求时创建 commit；提交前展示或检查准确的变更范围。
- 使用 Conventional Commits，类型保持英文、摘要使用简洁中文，例如 `feat: 增加订阅刷新事务`、`fix: 修复重启期间的竞态`。
- 常用类型：`feat`、`fix`、`refactor`、`test`、`docs`、`build`、`ci`、`chore`。
- 一个 commit 只表达一个可独立审查的意图，不混入无关格式化或重构。
- AI 协作提交在用户同意时加入约定的 `Co-Authored-By` footer，不伪造作者或审查者身份。

## 10. 当前治理状态

- 本文件只建立开发与测试规范，不代表现有代码已经通过规范审计。
- 用户已授权按 `docs/superpowers/plans/2026-08-12-test-governance-roadmap.md` 完整实施 Phase A-D，包括计划内测试、被测试证明必要的最小生产修复、CI/Actions、提交、推送与 PR。
- 各阶段仍按计划边界和独立评审切分实施，不把无关重构混入测试治理变更。
- 覆盖率当前先进入观察期；相对回归门禁必须满足至少 10 次成功 `main` workflow 的数据前置条件，固定覆盖率门槛仍须基于至少 30 次数据另行审核，不能提前写死。
- Git hooks、目录迁移、真实订阅、真实 mihomo、系统服务安装及其他 testenv 操作不在本次授权范围内。
