# Mihari 一期代码质量提升设计

## 1. 背景与目标

代码质量审计在提交 `717cc05` 上确认了三个必须在后续治理前关闭的问题：

- AQ-01：管理员/root 权限运行的 self updater 下载二进制后未校验内容摘要。
- AQ-04：coverage instrumentation 下，CLI 无参数测试可能继承测试进程参数并失败，导致观察 profile 无法稳定生成。
- AQ-05：配置首次初始化的所有等待者都串行竞争创建锁；Windows 默认全仓负载下可耗尽固定 10 秒等待上限。

Phase 1 的目标是关闭 AQ-01，并使默认测试和 coverage 成为可信的后续治理信号。它不追求一次解决全部审计项，也不以扩大超时、串行化测试或降低检查口径掩盖失败。

## 2. 范围与非目标

### 2.1 范围

1. 配置首次初始化改为等待“有效设置文件”或“创建锁所有权”中的任一结果。
2. CLI 无参数测试明确传入非 nil 空切片，避免 Cobra 将 nil 解释为未覆盖参数来源。
3. self updater 在替换 binary 前，从同一 GitHub Release 获取 `SHA256SUMS.txt`，严格解析目标资产唯一摘要并校验下载内容。
4. 完成静态检查、默认测试、race、coverage、安装脚本测试与六目标 CGO-free 构建。

### 2.2 非目标

- 不实现签名 checksum manifest、Sigstore、TUF 或内置发布公钥。
- 不声称 SHA-256 manifest 能抵抗发布账户与 manifest 同时被篡改；它提供发布物一致性与传输/存储损坏检测。
- 不处理 AQ-02 panel archive 总展开量和 AQ-03 panel release 默认超时。
- 不修改公开 CLI、JSON、`/v1` 协议、退出码或 settings schema。
- 不新增依赖，不修改 coverage 百分比门槛，不批量启用 linter。
- 不拆分 TUI、Web、Panel 或 runtime 大文件。

## 3. 不可破坏约束

- daemon 仍是持久化状态和 mihomo 生命周期的唯一所有者。
- self update 的 `Check` 保持只读：只获取 release metadata，不下载 checksum 或 binary。
- same-version `Update` 在下载任何资产前返回 `Updated=false`。
- 所有网络请求传播调用方 context，沿用 updater 的两分钟默认 client timeout。
- checksum 或 binary 的任一异常必须在 `replaceBinary` 前失败；旧 binary 和 `AfterReplace` 状态保持不变。
- 配置首次创建仍只有一个调用方生成 secret 并返回 `created=true`；等待者返回相同 secret 和 `created=false`。
- Windows sharing violation 继续按可恢复锁/读取冲突处理；真实权限和数据错误不能被无限重试吞掉。
- 测试只使用 `t.TempDir()` 与 `httptest.Server`，不访问公网、真实用户目录或系统服务。

## 4. AQ-05：配置首次初始化收敛

### 4.1 当前问题

`loadOrCreate` 首先读取设置。多个调用方同时观察到 `os.ErrNotExist` 后，都会进入 `acquireCreationLock`。首个调用方写入有效 settings 并删除 lock 后，其余调用方不会重新检查 settings，而是继续逐个获得 lock、二次读取、释放 lock。32 个调用方由此形成无意义的锁队列。

在 Windows 默认全仓包级并行负载下，审计连续两次观察到 `TestConcurrentLoadOrCreateUsesOneControllerSecret` 超过 10 秒。目标测试单独重复 25 次、全仓 `-p 1` 和全仓 race 均通过，说明负载放大了该算法的时序脆弱性。

### 4.2 目标状态机

```text
读取 settings
  ├─ 有效 → 返回 created=false
  ├─ 非 NotExist 错误 → 返回错误
  └─ NotExist
       └─ 循环协调
            ├─ 获得 creation lock → 成为创建者
            │    └─ 临界区内二次读取
            │         ├─ 有效 → 返回 created=false
            │         ├─ 非 NotExist → 返回错误
            │         └─ NotExist → 生成 secret、Save、返回 created=true
            ├─ 未获得 lock，但 settings 已有效 → 返回 created=false
            ├─ 非暂态锁/读取错误 → 返回错误
            └─ 总 deadline 到期 → 返回 initialization timeout
```

协调 helper 返回二选一结果：有效 `Settings` 或非 nil `*os.File` lock。调用方不得同时收到二者，也不得在无错误时同时收到零值。

所有观察到有效 settings 的分支——首次读取、锁冲突后的等待者读取、获得锁后的二次读取——都必须进入现有 `persistSidecarIfChanged`。只有真正创建 settings 的分支在第一次 `Save` 前调用 `applySidecarIfPresent`。因此 sidecar 的应用时机与 `created` 语义保持不变。

### 4.3 错误与超时语义

- 协调器在入口计算唯一绝对 deadline；settings 读取、锁获取和等待共享它，不再嵌套两个独立的 10 秒循环。所有 retry 在等待前和下一轮开始前检查同一 deadline。
- 错误分类固定为四类：`os.ErrNotExist` 表示尚未创建；`protocol.APIError{Code: CodeDataFailure}` 表示 settings 数据错误并立即返回；`os.ErrExist`（创建锁）或平台 helper 确认的 sharing/lock violation 表示暂态竞争；其他 I/O 错误（包括普通 `os.ErrPermission`）是 terminal error 并原样加操作上下文返回。
- Windows sharing/lock violation 识别放在 `settings_conflict_windows.go` 的小函数中，使用 `golang.org/x/sys/windows` 的明确错误码；`settings_conflict_unix.go` 必须声明 `//go:build !windows` 并返回 false。不得把所有 `os.ErrPermission` 一概视为暂态。
- 冲突后重新观察 settings；一旦 settings 可完整验证，立即返回，无需等待 lock 删除。
- settings 存在但被平台 helper 判定为 sharing/lock violation 时允许短间隔重试；无效 YAML、缺少 secret、schema 错误等数据错误必须立即返回，不能被当作“尚未创建”。
- deadline 到期时返回稳定的 `CodeDataFailure`：`timed out waiting for settings initialization`。terminal I/O 和 data error 不等待 deadline，也不被此错误覆盖。
- 为确定性测试引入非导出的窄 seam `settingsCreationOps`：`now func() time.Time`、`wait func(time.Duration)`、`load func(string) (Settings, error)`、`openLock func(string) (*os.File, error)`、`transientConflict func(error) bool`。生产 `loadOrCreate` 每次构造真实 ops 并调用 `loadOrCreateWithOps`；测试通过该 per-call 入口用 channel/latch 驱动“首次 NotExist → lock conflict → settings ready”的顺序，不修改包级全局变量。

### 4.4 验证

- 确定性测试预先持有 `.lock`，再写入有效 settings；协调 helper 必须在 lock 未释放时返回 settings。
- 32 goroutine 测试改用 `LoadOrCreateResult`，证明恰好一个 `created=true`、31 个 `created=false`、所有调用方获得同一个 secret，且最终文件可由 `Load` 验证。
- 通过 `LoadOrCreateWithSidecar` 覆盖“锁仍由其他调用方持有，但 settings 已就绪”的 waiter 路径，证明返回 `created=false` 且 sidecar 被持久化。
- 使用注入 ops 的确定性测试覆盖：malformed settings 立即失败、terminal permission error 立即失败、暂态冲突后读到有效 settings、stale lock 在单一 deadline 超时、lock owner 返回后的 close/remove 清理。
- 平台 helper 直接测试：Windows 的 sharing/lock violation 为 true、普通 permission 为 false；非 Windows helper 对普通错误为 false。协调 helper 同时覆盖“只返回 settings”和“只返回 lock”的二选一不变量。
- 目标并发测试重复 20 次。
- 默认全仓测试连续两次通过；`-p 1` 只作为诊断对照，不作为验收。
- `go test -race ./internal/config` 和最终全仓 race 通过。

## 5. AQ-04：CLI coverage 稳定性

### 5.1 当前问题

`Execute` 将测试提供的 `args` 传给 Cobra `SetArgs`。测试以 `nil` 表示“没有参数”，但 nil 对 Cobra 并不是可靠的显式空参数：在 coverage instrumentation 的测试二进制中可能回落到进程参数，从而走入未知参数/usage 路径，返回 exit 2 而不调用 TUI。

### 5.2 设计决策

- 生产 `Execute` 和 Cobra wiring 不变。
- 两个无参数测试使用 `[]string{}`，明确表达命令行参数集合为空。
- 不在生产代码中把所有 nil 输入归一化。nil 是测试调用方留下的模糊语义，而真实入口传递 `os.Args[1:]`。

### 5.3 验证

- `TestExecute_NoArgs*` 连续运行 20 次。
- CI 同款 `-covermode=atomic -coverpkg=./...` 命令连续运行三次，每次生成可由 coverage gate 解析的非空 profile。
- coverage 保持观察期，不因本修复启用阈值。

## 6. AQ-01：self update SHA-256 验证

### 6.1 信任边界

Release workflow 已在同一 Release 发布 `SHA256SUMS.txt`。Phase 1 将它作为目标资产的摘要声明，并确保下载到的 binary 与声明一致。

该方案能够检测 CDN/重定向/缓存/存储或下载链路返回了与 Release manifest 不一致的内容，也能防止 updater 在 manifest 缺失或歧义时继续替换。它不能抵抗有能力同时替换 Release metadata、binary 和 checksum manifest 的发布账户攻击者；签名 manifest 是后续独立安全增强。

### 6.2 资产选择

- 目标 binary 名由 GOOS/GOARCH 唯一计算：Windows 为 `mihari-<goos>-<goarch>.exe`，其他平台为 `mihari-<goos>-<goarch>`，与 release workflow 的固定命名一致。
- `SelectSelfAsset` 改为大小写敏感的完整名称匹配，并要求 Release 中恰好一个目标 asset；缺失或重复 exact asset 均 fail closed。前缀碰撞、大小写变体、debug/archive 邻居不得被选中。
- checksum asset 必须精确命名为 `SHA256SUMS.txt`。
- Release 中缺失 checksum asset 或出现多个同名 checksum asset均为数据错误并 fail closed。
- checksum asset 的 metadata size 必须为非负且不超过 1 MiB；响应实际读取同样限制为 1 MiB。

### 6.3 Manifest 语法

接受 release workflow 当前 `sha256sum *` 输出的两字段形式：

```text
<64 个十六进制字符><空白><可选 *>目标文件名
```

规则如下：

- 忽略空行。
- 非空行必须正好解析为 digest 和文件名两个字段。
- digest 必须解码为 32 bytes；十六进制大小写均接受。
- 文件名前可有 GNU binary marker `*`；去除 marker 后按大小写敏感的完整资产名匹配。
- manifest 中目标资产必须且只能出现一次。
- 任意非空行格式错误使整个 manifest 无效，而不是跳过；避免攻击者利用宽松解析制造歧义。
- 目标缺失、重复或摘要格式错误均映射为稳定的 `CodeDataFailure`，错误文本不得包含 URL 或敏感内容。

### 6.4 下载与校验顺序

```text
latest release metadata
  → same version? 是：返回，不下载
  → 选择目标 binary
  → 校验 binary metadata size
  → 唯一选择 SHA256SUMS.txt
  → 下载受限 manifest
  → 严格解析目标摘要
  → 创建 staging/candidate
  → 下载 binary，同时计算 SHA-256 和实际字节数
  → 校验最大大小、metadata size（若 >0）和 digest
  → chmod
  → replaceBinary
  → AfterReplace
```

manifest 在 staging 创建前获取，避免明显不可信 Release 留下不必要的 candidate。binary 在一次写盘中计算摘要，不二次读取文件。copy、close、size 或 digest 任一失败均删除 candidate；现有 staging defer 继续兜底清理。

为确定性覆盖 write/close 错误，verified download 接受非导出的 `openCandidate func(string) (io.WriteCloser, error)` seam；生产传入创建 0755 candidate 的真实实现，测试传入能在 Write 或 Close 返回指定错误的 writer。该 seam 只覆盖 candidate 写入边界，不替换 HTTP、hash、size 或 cleanup 逻辑。

### 6.5 失败保证

下列情况均不得替换旧 binary，且不得调用 `AfterReplace`：

- checksum asset 缺失或重复。
- checksum HTTP status 非 200、读取失败或超过上限。
- manifest 非空行格式错误、digest 非 SHA-256、目标缺失或重复。
- binary HTTP/读取失败、超过上限、metadata size 不符或 digest 不匹配。

`Check` 和 same-version `Update` 不要求 Release 包含 checksum，因为它们不执行替换。

### 6.6 测试

- 成功路径 fixture 同时提供 release metadata、manifest 和 binary，并验证最终内容。
- `SelectSelfAsset` 表驱动测试覆盖 exact success、目标缺失、重复 exact asset、前缀碰撞、大小写变体和 archive/debug 邻居。
- checksum metadata 测试覆盖 negative size 与大于 1 MiB；响应测试覆盖实际 manifest 超限、非 200 和读取错误。
- manifest parser 测试覆盖空行、GNU `*` marker、大小写 digest、目标缺失/重复、目标行无效、非目标行无效、额外字段和非 SHA-256 digest。
- binary verified-download 测试覆盖非 200、读取错误、实际超限、positive metadata size mismatch 和 digest mismatch。
- verified-download 还必须覆盖 candidate open、write 与 close failure；所有分支验证 candidate/staging cleanup。
- manifest request 测试断言 context cancellation 能中止请求、`User-Agent: mihari` 存在，并使用注入的 updater client；默认两分钟 timeout 由现有 `httpClient` 行为保持和代码审查确认。
- 每个 pre-replacement 失败用例都断言旧 binary 内容不变、`AfterReplace` 未调用、`.mihari-update` staging 不存在；旧 binary 不变同时证明 replacement seam 未到达。
- `TestSelfUpdaterCheckDoesNotDownloadAsset` 扩展为 checksum 与 binary 请求均为零。
- 邻接验证覆盖 `internal/update`、`internal/cli`、`internal/tui`。

## 7. 实施顺序与提交边界

1. AQ-05：先恢复默认测试稳定性；独立 `fix:` commit。
2. AQ-04：恢复 coverage profile 生成；独立 `test:` commit。
3. AQ-01：关闭自更新完整性风险；独立 `fix:` commit。
4. 更新审计报告与路线图状态；独立 `docs:` commit。

用户对本目标已明确授权每个 Phase 创建 commit；该授权不包含 push、PR 或 merge。Phase 1 的已审设计与实施计划在生产实现前先以独立 docs commit 固化，审计/roadmap 与本地、远端验收记录随后各自形成可追踪 docs commit。

该顺序让后续安全修复运行在可信的默认测试与 coverage 信号之上。每个生产行为变更必须先观察到对应测试按预期失败，再写最小实现。

## 8. Phase 1 验收门禁

以下条件必须同时满足：

- AQ-01、AQ-04、AQ-05 的回归测试和最小实现已通过独立代码审查。
- AQ-01 的 exact asset、checksum metadata/response、严格 manifest、binary status/read/size/digest 和 cleanup 失败矩阵全部通过。
- AQ-05 的单 deadline、错误分类、`created` 计数、single-secret、sidecar 和 lock cleanup 确定性矩阵全部通过。
- `gofmt -l .` 无输出。
- `go vet ./...` exit 0。
- `golangci-lint run ./...` 输出 0 issues。
- `go test -count=1 ./...` 连续两次 exit 0。
- `go test -count=1 -race ./...` exit 0。
- CI 同款 coverage 命令连续三次 exit 0，且 coverage gate 能解析 profile。
- `python scripts/install/test_parallel_download.py -v` 通过；平台设计跳过项单独记录。
- `CGO_ENABLED=0` 的 Windows/Linux/macOS × amd64/arm64 六目标构建全部成功。
- 当前主机完成上述全量命令；现有 GitHub Actions Windows/Linux/macOS unit matrix 和 Windows race 必须在 Phase 1 最终验收记录中为 green。若分支尚未推送而没有对应 CI 运行，只能记录“本地实现完成、跨平台运行时验收待完成”，不得声称 Phase 1 已完全验收。
- 获得 push/PR 授权后采用有限的条件生效闭环：先推送本地完成 HEAD 并要求 CI-1 green；随后创建 closure docs commit，记录 CI-1 的 run URL/commit/jobs。roadmap 状态列使用受控 token `已验收`，相邻证据说明明确写出“此状态仅在本 closure commit 的 required jobs 全部 green 后生效”。推送该 closure commit 并要求 CI-2 对这个 exact commit 全绿；CI-2 使已提交的条件状态生效，Phase 1 不再创建第三个 tracked 状态变更。CI-2 的 run/commit 关联由 GitHub commit checks/PR 证据保存，不要求把未来 run URL 回写同一 commit。
- 工作区无 coverage、binary、真实数据、凭据或无关改动。
- Phase 1 的设计、实施计划、代码与审计闭环均已 commit；只有此后才允许进入 Phase 2。
