# Issue #193 Downgrade Warning Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在既有 Mihari 替换入口实际发生降级或无法判断兼容性时，于停机/受管写入之前完成准确的风险提示和确认。

**Architecture:** 固定候选，按实际替换目标建立预览；表现层负责确认，更新/安装用例在已有操作边界复核。复用 PreparedUpdate、Unix 安装锁和 Windows 既有替换/补偿；Windows 服务同步保留部分成功语义，不引入跨进程原子事务。

**Tech Stack:** Go 1.26.0 / toolchain go1.26.5、Cobra、Bubble Tea v2、POSIX sh、Windows PowerShell 5.1、Python pytest；只使用既有依赖和标准库。

**Spec:** [已批准 R2 设计](../specs/2026-09-09-issue-193-downgrade-warning-design.md)。用户于 2026-09-09 明确“审核通过，创建执行计划superpowers”。设计已完成首次 subagent 审核及主代理修订；用户随后要求对设计与执行计划进行限定 scope 的迭代审核，记录见文末。

## Global Constraints

- 工作目录 `/home/kinema/dev/mihari/.worktrees/issue-193`；分支 `codex/issue-193-downgrade-warning`；设计基线 `60ca3ea`。
- “所有产品文案为英文。”文档及提交建议摘要可用中文。
- “不引入第三方 semver 库。”不新增依赖或改变 go.mod/go.sum。
- “版本子进程使用 context，默认 3 秒上限，stdout/stderr 各最多 4 KiB，超限终止并回收。”
- “不持久化，不经过 daemon /v1。”预览/确认只存在于当前调用及必要的临时交接文件中。
- “成功 CLI JSON、daemon /v1 DTO、安装请求 JSON、退出码定义及持久化格式不变。”仅加入设计已批准的 flag、风险错误 details 和脚本能力查询。
- “此次不声称原子 CAS、不新建机器互斥或持久锁格式。”Windows 的外部并发窗口如实保留。
- “旧 PR 不作为实现基础；#194 的数据重置、全量卸载不进入本方案。”不 cherry-pick #196，不改 CHANGELOG。
- 官方更新必须通过原有 fixed tag/制品校验；Windows 离线未知候选按设计明确确认，不能拿测试变量或用户输入版本冒充可信版本。
- Unix root 不运行用户可写 binary，TUI 不直接写业务状态；保留安装锁顺序与无服务独立 binary 路径不创建 B 的约束。
- 真实订阅、真实 mihomo、工作站服务安装、账户/mount 测试不属于执行授权；只用 fake、临时目录及 loopback fixture。
- 六目标：Windows/Linux/macOS × amd64/arm64，发布检查 `CGO_ENABLED=0`。所有新 `_unix.go` / `_unix_test.go` 显式写 `//go:build linux || darwin`；Windows文件使用 `_windows.go` 配对测试，通用文件不散布平台分支。
- 新行为按 Red–Green–Refactor；Red 必须是行为断言失败，不把缺符号/编译失败记为 Red。
- 设计/计划已按用户要求迭代审核通过，随后用户持久目标明确授权实施、提交 PR 与每十分钟 CI/bot 检查（见 §17）；不扩大已批准 R2 scope。仅发现必须扩大 scope 才能处理的致命 P0/P1 时向用户汇报并单独说明；普通增强不纳入。合并仍须用户确认。
- 未获明确提交/推送授权前，各任务以检查准确 diff 为检查点；文内 Conventional Commit 是建议，不自动执行 git commit/push。

---

## 0. 执行起点、文件地图与接口约定

执行前先读 `.github/CONTRIBUTING.md`、`AGENTS.md`、README、已批准 Spec 及对应包测试。AGENTS 所引 2026-08-03 架构文件已不存在，以当前 `docs/architecture.md`、`docs/unix-layout.md` 和 R2 的现状记录为依据，不新建冒名旧文件。

先运行只读基线检查，记录结果，不能覆盖用户已有文件：

```sh
cd /home/kinema/dev/mihari/.worktrees/issue-193
git branch --show-current
git status --short
export PATH=/usr/local/go/bin:$PATH # 当前工作站 Go 启动器；不以其默认版本作为验证版本
export GOTOOLCHAIN=go1.26.5
go version # 必须显示 go1.26.5；较新本机 Go 也不能替代此固定版本
go test ./internal/update ./internal/app ./internal/cli ./internal/tui/...
```

预期分支如上；设计、审核、计划可能尚未跟踪，保留。若基线失败，先最小复现并记录是否与本任务有关，不把其它 worktree 的历史失败直接认作本分支基线。脚本测试若缺 pytest，使用仓库已有 `scripts/release/requirements-test.txt` 在临时 venv 安装测试工具，不改项目依赖。

### 文件责任地图

| 层 | 新增文件 | 需修改的已有文件 | 职责 |
| --- | --- | --- | --- |
| update | `replacement_version.go`、`replacement.go`、`version_probe.go`、`apply_prepared.go` 及邻近测试 | `channel.go`、`prepare.go`、`self.go`、现有测试 | 风险、预览、确认、版本探测、候选复用 |
| platform | `replacement_target.go`、`replacement_target_unix.go`、`replacement_target_windows.go` 及测试 | 仅必要的小接口调用点 | 路径/身份/内容观察、可执行信任判定；不包含风险业务 |
| app | `replacement_targets_unix.go`、`replacement_targets.go`、`install_replacement.go` 及测试 | `installer_unix.go`、`install_entrypoints.go`、`binary_update_unix.go`、`self_update.go` | 实际目标集合、Unix 提交检查、Windows 服务同步 |
| service | 相邻边界测试 | `service.go` | 在 Stop/stage 边界消费当前调用的检查回调 |
| cli | 邻近测试 | `self.go`、`service_apply.go`、`root.go`、相关 fake | 新确认参数、错误 details、stderr 警告 |
| tui | `pages/system/replacement.go` 及测试 | System `model.go`、`prepared_update.go`、`run.go`、`ui/action.go`、`ui/strings.go`、相关 fake | 准备后确认、取消与候选所有权 |
| cmd | 装配测试 | `unix_layout.go`、`windows_layout.go` | 注入实际 observer/服务同步回调，不能只在测试中启用 |
| scripts | `test_replacement_confirmation.py`、`testdata/replacement_versions.json` | 六脚本、`root-apply.sh.in`、已有 channel/root bridge 测试 | 固定 tag、能力兼容、确认交接及 fixture |
| verification/docs | `internal/integration/replacement_confirmation_test.go` | CI、commands/architecture/distribution、安装说明 | 可观察验收、保持契约、纠正文档 |

新增 Go 文件完整前缀为对应 `internal/<package>/`。不按上述地图移动无关已有代码；System 页只抽本功能的新逻辑，不整体拆分页面。

### 跨任务接口（本计划中的一致命名）

Task 1/4 在 `internal/update` 定义以下接口；其它任务不能另起同义类型：

```go
type ReplacementRisk string
const (
    ReplacementNone ReplacementRisk = "none"
    ReplacementDowngrade ReplacementRisk = "downgrade"
    ReplacementUnknown ReplacementRisk = "unknown"
)
type ReplacementCandidate struct {
    Version string
    SHA256 string
    Channel string
}
type ReplacementTarget struct {
    Roles []string
    Path string
    FileID string
    SHA256 string
    Exists bool
    Version string
}
type ReplacementSnapshot struct {
    Targets []ReplacementTarget
    ServiceDefinitionSHA256 string
}
type ReplacementPreview struct {
    Candidate ReplacementCandidate
    Snapshot ReplacementSnapshot
    Risk ReplacementRisk
    ID string
}
type ReplacementConsent struct {
    Yes bool
    ExpectedPreview string
    // Optional call-local warning sink; excluded from fingerprints and public DTOs.
    Warn func(string) error `json:"-"`
}
type ReplacementObserver func(context.Context, string) (ReplacementSnapshot, error)

func ClassifyReplacementVersion(current, target string) ReplacementRisk
func NewReplacementPreview(ReplacementCandidate, ReplacementSnapshot) (ReplacementPreview, error)
func ValidateReplacementConsent(ReplacementPreview, ReplacementConsent) error
func RecheckReplacement(ReplacementPreview, ReplacementCandidate, ReplacementSnapshot) error
func ReplacementWarning(ReplacementPreview) string
func ReplacementConfirmationError(ReplacementPreview) error
```

`Path/FileID/SHA256` 仅作内部比较，不直接 JSON 序列化整个 Preview 或 Snapshot 到输出。`Roles` 仅对同一规范化目录项路径合并并排序；同路径观察的身份、摘要、存在性、版本必须一致，否则拒绝。不同目录项即使共享 inode/FileID 也分别进入指纹；Windows 大小写/短路径别名由平台层规范化到同一目录项，不能把不同硬链接路径合并。不能只按版本或 FileID 去重。`ServiceDefinitionSHA256` 对“不存在服务”也有固定摘要，服务运行/停止等易变状态不属于定义摘要；修改 ImagePath/参数/注册定义必须改变摘要。

Task 2/3 定义平台及探测接口：

```go
// internal/platform
// Unknown execution trust does not imply the file is absent.
type ReplacementFile struct {
    Path string
    FileID string
    SHA256 string
    Exists bool
    MayExecute bool
}
func ObserveReplacementFile(context.Context, string) (ReplacementFile, error)

// internal/update
// Runner owns child start, bounded output, cancellation, and wait.
type VersionRunner interface {
    RunVersion(context.Context, string, string) ([]byte, error)
}
type ExecVersionRunner struct{}
func (ExecVersionRunner) RunVersion(context.Context, string, string) ([]byte, error)
func ObserveReplacementTarget(context.Context, string, string, VersionRunner) (ReplacementTarget, error)
```

ObserveReplacementTarget 参数依次为 ctx、role、absolute path、runner；runner 的两个 string 为 executable 与 probeTempDir。调用者用观察返回的 version，不拿运行中 buildinfo.Version 覆盖 unknown。

Task 5 扩展已有 `PreparedUpdate`，保留已有字段及幂等 Close：

```go
// Additional PreparedUpdate fields:
TargetPath string
Preview ReplacementPreview
Consent ReplacementConsent

// Additional SelfUpdater fields:
ObserveTargets ReplacementObserver
AfterReplacePrepared func(context.Context, PreparedUpdate) error

func (u SelfUpdater) ApplyPrepared(context.Context, PreparedUpdate) (Result, error)
```

现有 AfterReplace 保留作兼容内部调用点，优先使用 AfterReplacePrepared，两者不能重复调用。`Update` 成为 Prepare→ApplyPrepared 的无确认包装，不再保留绕过校验的下载/替换路径。新版 CLI 使用 Prepare/ApplyPrepared 显式传 Consent。

## Task 1：版本风险分类与跨语言 fixture

**Files:** Create `internal/update/replacement_version.go`、`internal/update/replacement_version_test.go`、`scripts/install/testdata/replacement_versions.json`；Modify `internal/update/channel.go`、`channel_test.go`。

**Interfaces:** Produces `ReplacementRisk` 常量和 `ClassifyReplacementVersion(current, target string) ReplacementRisk`；不改变 `classifyUpdate` 的可执行候选语义。

- [x] 写表驱动测试；公共 fixture 至少包含下列实际输入输出，Go 和后续脚本测试读取同一文件。

```json
[
  {"current":"v1.2.3","target":"v1.2.2","risk":"downgrade"},
  {"current":"1.2.3","target":"v1.2.2","risk":"downgrade"},
  {"current":"v1.2.3","target":"v1.2.3","risk":"none"},
  {"current":"v1.2.3","target":"v1.3.0","risk":"none"},
  {"current":"v1.2.3-dev.8","target":"v1.2.3","risk":"none"},
  {"current":"v1.3.0-dev.8","target":"v1.2.3","risk":"downgrade"},
  {"current":"v1.3.0-dev.10","target":"v1.3.0-dev.9","risk":"downgrade"},
  {"current":"dev","target":"v1.2.3","risk":"unknown"},
  {"current":"v1.2.3","target":"","risk":"unknown"},
  {"current":"v999999999999999999999999.0.0","target":"v1.2.3","risk":"unknown"}
]
```

```go
func TestReplacementVersion_DevToSameBaseStableIsUpgrade(t *testing.T) {
    if got := ClassifyReplacementVersion("v1.2.3-dev.8", "v1.2.3"); got != ReplacementNone {
        t.Fatalf("risk=%s", got)
    }
}
```

- [x] 先提供返回 ReplacementNone 的最小可编译函数声明，再运行 `go test ./internal/update -run '^TestReplacementVersion' -count=1`；降级/unknown 用例应因风险错误失败，不能把 undefined symbol 当作 Red。
- [x] 实现去空白、允许无小写 v 前缀、canonical 解析及逐段比较。复用 `parseCanonicalTag`，消除 `atoi` 忽略溢出和整段相减的溢出风险；使用检查错误的 strconv.Atoi 和 `<`/`>` 比较。不可解析返回 unknown，不把字段当作 0。

```go
func ClassifyReplacementVersion(current, target string) ReplacementRisk {
    current = strings.TrimSpace(current)
    target = strings.TrimSpace(target)
    if current != "" && !strings.HasPrefix(current, "v") { current = "v" + current }
    if target != "" && !strings.HasPrefix(target, "v") { target = "v" + target }
    order, ok := compareCanonicalTags(current, target)
    if !ok { return ReplacementUnknown }
    if order > 0 { return ReplacementDowngrade }
    return ReplacementNone
}
```

- [x] 在 channel_test.go 保留并明确断言 stable ahead 不可更新、同基础 dev→stable 可更新、更低 stable 跨 dev 候选的原行为；运行 `go test ./internal/update -count=1`。
- [x] gofmt 新改 Go 文件，检查本任务 diff。建议提交意图：`feat: 增加 Mihari 替换版本风险分类`，执行提交前需有明确授权。

## Task 2：只读目标身份与执行信任观察

**Files:** Create `internal/platform/replacement_target.go`、`replacement_target_unix.go`、`replacement_target_windows.go`、`replacement_target_test.go`、`replacement_target_unix_test.go`、`replacement_target_windows_test.go`。

**Interfaces:** Produces `ReplacementFile`、`ObserveReplacementFile(ctx, absolutePath)`；结果中 MayExecute=false 不是权限提升许可，也不能假装文件不存在。

- [x] 写临时文件观察、fresh、内容变化测试；完整输入均在 t.TempDir 内。用相邻平台测试注入 owner/ACL 判定，不修改真实机器权限或创建用户。

```go
func TestReplacementFile_DigestChangesWithContent(t *testing.T) {
    path := filepath.Join(t.TempDir(), "mihari")
    if err := os.WriteFile(path, []byte("first"), 0600); err != nil { t.Fatal(err) }
    first, err := ObserveReplacementFile(context.Background(), path)
    if err != nil { t.Fatal(err) }
    if err := os.WriteFile(path, []byte("second"), 0600); err != nil { t.Fatal(err) }
    second, err := ObserveReplacementFile(context.Background(), path)
    if err != nil { t.Fatal(err) }
    if !first.Exists || first.SHA256 == second.SHA256 { t.Fatal("content change was not observed") }
}
```

- [x] 先以总返回空观察的可编译声明跑 `go test ./internal/platform -run '^TestReplacementFile' -count=1`，确认存在性/摘要行为断言失败。
- [x] 实现同一已打开文件的身份、有限读取 SHA-256、读取前后核对，返回固定绝对路径。读取遵守 ctx；复用现有平台文件身份工具，读取上限与既有 Mihari binary 上限一致。文件消失/换身份报 invalid state 或现有路径错误，不回退为 fresh。

```go
// The platform adapter must fill both fields from the same opened object.
observed := ReplacementFile{Path: absolutePath, Exists: true}
observed.FileID = identityKey
observed.SHA256 = hex.EncodeToString(contentHash[:])
observed.MayExecute = allowedByExistingOwnerAndPathPolicy
return observed, nil
```

平台层返回的 Path 应为规范化目录项路径：Windows 同一目录项的大小写/短路径别名一致，不同硬链接路径仍不同；不能用 FileID 反推唯一替换路径。新增验收保持未勾选，不能从已有测试通过推断已覆盖：

- [ ] 用临时文件验证同一路径的多个角色合并；两个硬链接路径均保留，任一路径替换使预览失效；Windows 原生别名用例证明同一目录项只合并一次（平台无短名时明确记录该子例限制）。

本片段三个局部值的产生方式：identityKey使用现有platform.FileIdentity.Key（Unix）或volume/file index（Windows）对应的稳定字符串；contentHash对同一handle使用crypto/sha256计算；allowedByExistingOwnerAndPathPolicy来自该平台对父路径及文件的owner/ACL观察。它们都是本次打开对象的值，不能取路径字符串的hash充当文件身份。Unix root 判断整条父路径 owner/mode/链接规则，非 root 不提升；Windows elevated 判断 SYSTEM/Administrators 控制的 ACL，普通用户只允许同身份执行。不能安全执行但允许读取的路径返回 MayExecute=false；底层读取/路径错误原样分类。
- [x] 分别写“root 遇用户拥有路径禁执行”“文件不可读不等于缺失”“Windows unsafe ACL 禁执行”“父目录替换/链接变化被识别”的平台用例。运行本机 platform 全包；交叉编译两个其它 OS 的 platform 测试二进制到临时目录，只检查编译不执行。
- [x] 检查通用文件没有 runtime.GOOS 分支，无真实安装根访问。建议提交：`feat: 增加替换目标的只读身份观察`。

## Task 3：有界且不提升权限的版本探测

**Files:** Create `internal/update/version_probe.go`、`version_probe_test.go`；Test fixture Create `internal/update/testdata/versionprobe/main.go`。

**Interfaces:** Consumes Task 2；Produces VersionRunner、ExecVersionRunner、ObserveReplacementTarget；新增可注入平台观察函数只在使用方附近，不建 utils 包。

- [x] 写 fake runner 计数测试：MayExecute=false 时不执行；合法 JSON 得到规范版本；超时/超限/损坏输出得到 unknown；探测前后对象变化返回错误。

```go
type versionRunnerFunc func(context.Context, string, string) ([]byte, error)
func (f versionRunnerFunc) RunVersion(ctx context.Context, exe, dir string) ([]byte, error) {
    return f(ctx, exe, dir)
}
func TestVersionProbe_RejectsDuplicateVersion(t *testing.T) {
    raw := []byte(`{"schema":"mihari/v1","version":"v1.2.3","version":"v9.0.0"}`)
    if got := decodeProbedVersion(raw); got != "" { t.Fatalf("ambiguous version=%q", got) }
}
```

在本任务定义不导出的 `decodeProbedVersion([]byte) string`：严格单对象、唯一 schema/version、无尾随对象，未知字段遵守当前 self version envelope；失败返回空字符串表示 unknown。编译 stub 后运行 `go test ./internal/update -run '^TestVersionProbe' -count=1`，应因解析/禁执行断言失败。
- [x] 实现 ExecVersionRunner：`exec.CommandContext(ctx, executable, "self", "version", "--json")`，使用 caller 创建的探测临时目录；不通过 shell。给子进程的 MIHARI_DATA、控制 endpoint、credential、安装路径覆盖均指向该隔离目录，环境只保留平台启动必需项；不传递用户真实配置或敏感环境。

```go
probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
defer cancel()
raw, err := runner.RunVersion(probeCtx, before.Path, probeDir)
if err != nil && ctx.Err() != nil { return ReplacementTarget{}, ctx.Err() }
version := ""
if err == nil { version = decodeProbedVersion(raw) }
```

父 ctx 被取消应返回取消错误，不把取消当成 unknown 继续安装。子超时/查询失败可为 unknown。stdout/stderr 分别限 4096 bytes，writer 一旦超限主动 cancel，不是只截断后继续等待；保证 Wait 回收，用 cleanup/错误合并处理关闭失败。
- [x] ObserveReplacementTarget 先 ObserveReplacementFile，fresh 直接返回 Exists=false；MayExecute=false 不启动 runner；其它目标在 TempDir 探测后再次观察，只在 FileID/SHA256/Path 相同才绑定 version。SHA256/权限/执行输出不写日志。
- [x] 写可离线构建的 fixture 源码（仅输出 JSON，不启动网络/业务）；以与官方一致的 trimpath/stripped flags 在本机运行。fixture 注入变量定义为 `main.version`，不伪造 Mihari 官方执行身份。

```go
package main
import "fmt"
var version = "dev"
func main() { fmt.Printf("{\"schema\":\"mihari/v1\",\"version\":%q}\n", version) }
```

- [x] 运行 probe 用例及 update 全包。使用 channel 控制阻塞 fake，避免靠 Sleep 验证取消；Windows fixture 只在 Windows 原生 job 运行。建议提交：`feat: 安全探测待替换 Mihari 的版本`。

## Task 4：预览指纹、确认与公开风险内容

**Files:** Create `internal/update/replacement.go`、`replacement_test.go`。

**Interfaces:** Produces第 0 节的 candidate/target/snapshot/preview/consent 类型和五个函数：NewReplacementPreview、ValidateReplacementConsent、RecheckReplacement、ReplacementWarning、ReplacementConfirmationError。

- [x] 写已知降级、unknown、全部 fresh、多个角色同文件、不同版本服务副本测试。加入身份绑定测试；示例完全由临时字符串组成，不需要真实文件。

```go
func TestReplacementConsent_RejectsChangedFileWithSameVersion(t *testing.T) {
    candidate := ReplacementCandidate{Version:"v1.2.2", SHA256:strings.Repeat("a",64), Channel:"main"}
    snapshot := ReplacementSnapshot{Targets:[]ReplacementTarget{{
        Roles:[]string{"service"}, Path:"/fixture/mihari", FileID:"first",
        SHA256:strings.Repeat("b",64), Exists:true, Version:"v1.2.3",
    }}, ServiceDefinitionSHA256:strings.Repeat("c",64)}
    preview, err := NewReplacementPreview(candidate, snapshot)
    if err != nil { t.Fatal(err) }
    snapshot.Targets[0].FileID = "second"
    next, err := NewReplacementPreview(candidate, snapshot)
    if err != nil { t.Fatal(err) }
    err = ValidateReplacementConsent(next, ReplacementConsent{Yes:true, ExpectedPreview:preview.ID})
    var api protocol.APIError
    if !errors.As(err,&api) || api.Code != protocol.CodeInvalidState { t.Fatalf("err=%v",err) }
}
```

- [x] 先用可编译的恒定 ID/总允许授权实现运行 `go test ./internal/update -run '^TestReplacement' -count=1`；应因变化仍被接受而失败。
- [x] 构造固定顺序内部 hash struct，包含 version=1、candidate tag/digest/channel、规范路径/目标身份/摘要/存在性/版本、排序后的 roles、service definition digest。深拷贝输入；不包含候选临时路径、预览时间、服务 Running 状态。SHA256 hex 固定小写64位。序列化错误不得忽略。

```go
func ValidateReplacementConsent(p ReplacementPreview, c ReplacementConsent) error {
    if c.ExpectedPreview != "" && (!c.Yes || !validPreviewID(c.ExpectedPreview)) {
        return protocol.APIError{Code:protocol.CodeInvalidArgument, Message:"--expected-preview requires --yes and a valid preview ID"}
    }
    if c.ExpectedPreview != "" && c.ExpectedPreview != p.ID {
        return protocol.APIError{Code:protocol.CodeInvalidState, Message:"installation changed; start again"}
    }
    if p.Risk != ReplacementNone && !c.Yes { return ReplacementConfirmationError(p) }
    return nil
}
```

`validPreviewID(string) bool` 在同文件定义，按64位小写hex严格判断。RecheckReplacement 重建 preview 并比较 ID，不重新询问、不自动放弃 ExpectedPreview。全部目标不存在且原流程是 fresh 时无兼容性风险；任一存在目标且当前/候选未知则 unknown；确定降级优先，同时文案列明未知角色。
- [x] 固定英文文案及安全 DTO；用测试断言所有关键风险短句，以及输出中没有 Path/FileID/内部摘要/配置。

```text
Older Mihari versions may not support settings, subscriptions, state, or generated files written by the current version. Mihari may fail to start or load data, which can look like data loss. Downgrade is not a supported configuration migration and does not roll back disk state.
```

unknown 的首句为 `Mihari could not determine version compatibility for this replacement.`，随后同样说明旧版风险；不声称确定降级。

风险 APIError：CodeInvalidArgument，Details 包含 reason=`replacement_confirmation_required`、risk、targets（仅 roles/version）、target_version、preview_id。禁止直接把内部 Snapshot 放入 map。
- [x] 运行 update 全包及 `go test -race ./internal/update`，确认 ID 对目标顺序稳定、服务定义变化敏感、同候选不同 staging 路径稳定。建议提交：`feat: 将替换确认绑定到候选与安装预览`。

## Task 5：PreparedUpdate 统一校验并执行固定候选

**Files:** Modify `internal/update/prepare.go`、`self.go`、`prepare_test.go`、`self_test.go`；Create `internal/update/apply_prepared.go`、`apply_prepared_test.go`。

**Interfaces:** Consumes Task 3/4；Produces SelfUpdater.ApplyPrepared 及第 0 节新增字段。未配置 ObserveTargets 的无服务调用用 ObserveReplacementTarget 检查传入 binaryPath；生产 Windows 服务集合在 Task 7 注入。

- [x] 扩展现有 startSelfUpdateEnv fixture：Prepare 得到 v9.9.9 后改变服务端 latest，Apply 必须仍使用已准备 tag/digest；篡改 candidate 或目标后 Apply 必须拒绝且 AfterReplace 不被调用。

```go
func TestApplyPrepared_ChangedCandidateDoesNotReplace(t *testing.T) {
    env := startSelfUpdateEnv(t, selfUpdateServerConfig{
        checksumBody:fixtureSHA256Hex([]byte("prepared"))+"  mihari-linux-amd64\n",
        binaryBody:[]byte("prepared"),
    })
    p, err := env.updater.Prepare(context.Background(), env.binaryPath,"v1.0.0",ChannelMain)
    if err != nil { t.Fatal(err) }
    t.Cleanup(func(){ if err := p.Close(); err != nil { t.Error(err) } })
    before, err := os.ReadFile(env.binaryPath)
    if err != nil { t.Fatal(err) }
    if err := os.WriteFile(p.CandidatePath,[]byte("tampered"),0600); err != nil { t.Fatal(err) }
    result, err := env.updater.ApplyPrepared(context.Background(),p)
    after, readErr := os.ReadFile(env.binaryPath)
    if err == nil || result.Updated || readErr != nil || !bytes.Equal(before,after) {
        t.Fatalf("changed candidate accepted: updated=%v err=%v readErr=%v",result.Updated,err,readErr)
    }
}
```

- [x] 增加可编译 ApplyPrepared 的旧替换委托后跑新测试，确认失败因 candidate/预览未复核；不是缺字段编译错误。
- [x] Prepare 将传入 binaryPath 固定为 PreparedUpdate.TargetPath，Apply 不能从可变环境重新选择主目标。Prepare 保留原 classifyUpdate no-op；可更新时固定 tag 后下载，得到 Candidate 后建立 Preview。不得把 stale Check.Latest 放进预览；Preview 生成失败用 errors.Join 清理已下载候选。
- [x] ApplyPrepared 顺序固定如下，重用现有 replaceBinary 和候选校验实现，不复制下载代码：

```text
Available=false → 原 no-op Result
验证候选文件 bytes 与 p.SHA256/p.Version 仍对应
读取当前目标快照 → RecheckReplacement → ValidateReplacementConsent
现有平台 replaceBinary
立即设置 Result.Updated=true
AfterReplacePrepared(p)，未配置才调用旧 AfterReplace(p.Version)
返回既有同步错误与 Updated 状态
```

- [x] Update 改为 Prepare→defer Close→ApplyPrepared，默认 Consent{}；已有包测试中升级 fake 的未知旧字节必须注入真实预期版本 observer，不能通过默认确认绕过 unknown。同版/ahead/no-download 断言保持。
- [x] 测试原替换失败、后置同步失败、cleanup失败、Prepare取消：替换后错误不能重置 Updated，两个 completion callback 只能调用一个。运行 `go test ./internal/update -count=1`。建议提交：`refactor: 统一固定候选的 Mihari 更新执行路径`。

## Task 6：Unix 实际目标发现与安装提交门禁

**Files:** Create `internal/app/replacement_targets_unix.go`、`install_replacement.go`、`install_replacement_test.go`、`replacement_targets_unix_test.go`；Modify `installer_unix.go`、`install_entrypoints.go`、`binary_update_unix.go`、`install_entrypoints_test.go`、`install_harness_test.go`。

**Interfaces:**

```go
// internal/app
func (i *UnixInstaller) ApplyWithConsent(context.Context, InstallRequest, update.ReplacementConsent) (InstallResult,error)
func (i *UnixInstaller) ObserveReplacement(context.Context,string) (update.ReplacementSnapshot,error)
```

保留现有 Apply(ctx,req) 为 ApplyWithConsent(ctx,req,Consent{}) 包装。公共 Consent 仅表达实际输入，不自动填入 ExpectedPreview；Task4 的“expected 必须配合 yes”契约保持。准备时快照用独立的内部 expected 参数传递，不能把内部防陈旧校验转换为用户确认参数。

```go
// Private shared path; expected is an in-process prepared snapshot, not a CLI flag.
func (i *UnixInstaller) applyWithReplacement(ctx context.Context, req InstallRequest,
    start bool, consent update.ReplacementConsent,
    expected *update.ReplacementPreview) (InstallResult, error)
```

ApplyWithConsent 调用此路径时传 start=true、expected=nil，保持现有直接安装行为。ApplyPrepared 传 start=false、p.Consent、&p.Preview，保持 installer_unix.go 既有自更新不强制启动服务的行为，不转调固定 start=true 的公共入口。共享私有路径先取得本次 candidate/snapshot：expected 非空时无论 Yes 是否为 true，都独立 RecheckReplacement(*expected,candidate,snapshot)，之后校验公开 Consent；持锁提交前再次对同一预览复核。普通升级 Consent{} 可继续，但准备之后的目标变化仍拒绝；显式 yes 同样不能绕过内部快照。直接 service apply 的 expected=nil 只表示没有上一次进程内准备快照，本次预览及持锁复核仍必需。

RunService/显式 recover 保持原专用路径，不新增确认或启动策略。NewUnixInstaller将downloader.ObserveTargets绑定到i.ObserveReplacement；不得把当前 helper 自身版本当成待替换安装版本。

- [x] 在现有 memory install harness 增加事件记录，分别覆盖缺确认、错误 expected-preview、目标变化、pending、无服务更新不打开 B。基于现有 harness 的最小测试：

```go
func TestInstallReplacement_StalePreviewStopsBeforeMutation(t *testing.T) {
    h := newInstallHarness(t, InstallDataRetain)
    before := h.disk.snapshot()
    candidate := update.ReplacementCandidate{Version:"v1.0.0",SHA256:strings.Repeat("a",64),Channel:"main"}
    snapshot := update.ReplacementSnapshot{ServiceDefinitionSHA256:strings.Repeat("b",64)}
    p, err := update.NewReplacementPreview(candidate,snapshot)
    if err != nil { t.Fatal(err) }
    changed := snapshot
    changed.ServiceDefinitionSHA256 = strings.Repeat("c",64)
    _, err = runInstallReplacement(context.Background(),p,
        update.ReplacementConsent{Yes:true,ExpectedPreview:p.ID},
        func(context.Context) error { return update.RecheckReplacement(p,candidate,changed) },
        func(ctx context.Context) (InstallResult,error) { return h.tx.Apply(ctx,h.req) })
    if err == nil || before != h.disk.snapshot() || h.lease.acquires != 0 {
        t.Fatalf("changed preview reached transaction: err=%v acquires=%d",err,h.lease.acquires)
    }
}
```

在 `install_replacement.go` 定义门禁执行函数并由 applyService/applyBinaryOnly 的实际提交分支使用：

```go
func runInstallReplacement(ctx context.Context, preview update.ReplacementPreview,
    consent update.ReplacementConsent, recheck func(context.Context) error,
    apply func(context.Context) (InstallResult,error)) (InstallResult,error) {
    if err := update.ValidateReplacementConsent(preview,consent); err != nil { return InstallResult{},err }
    if err := recheck(ctx); err != nil { return InstallResult{},err }
    return apply(ctx)
}
```

调用方持有既有锁时，recheck只做已有身份/摘要读取；apply闭包进入既有事务，不在门禁里重获锁。在取锁前也做一次无副作用Validate以尽早拒绝。仅在该锁外 Validate 成功且风险非 none 时调用一次可选 consent.Warn；回调返回错误则停止，不在持锁复核时再次调用。不得只有函数自身的测试，后续同时覆盖真实调用者不会在门禁前执行Recover/prepare。
- [x] 添加编译 stub 后运行 `go test ./internal/app -run 'TestInstallReplacement|TestReplacementTargets|TestInstallEntry' -count=1`，缺确认旧调用链应因仍进入变更失败。
- [x] 实现目标发现：InspectDefinition 后按真实 operation 找出将覆盖的 managed/path binary、源/目的角色，调用 Task 3 观察并去重。先发现服务，再按既有布局访问 B/P；独立 binary 分支使用已有 OwnedBinaryLease，不能为新预览创建 B。
- [x] 新替换先做无副作用预览和 consent 检查；取得既有锁后只重读目标 identity/digest/definition，复用锁外已绑定 version 结果，不在锁内运行版本子进程。pending 拒绝分支放在任何 RecoverLocked 前；明确 `InstallOperationRecover` 仍走原专用分支。

```go
if req.Operation == InstallOperationRecover {
    return i.applyWithStart(ctx, req, start)
}
if err := update.ValidateReplacementConsent(preview, consent); err != nil { return InstallResult{}, err }
// Outside the lease, after validation; the callback owns text/JSON output policy.
if warning := update.ReplacementWarning(preview); warning != "" && consent.Warn != nil {
    if err := consent.Warn(warning); err != nil { return InstallResult{}, err }
}
// Inside the existing installation lease, immediately before transaction work:
if err := update.RecheckReplacement(preview, candidate, lockedSnapshot); err != nil {
    return InstallResult{}, err
}
```

片段中的 preview/candidate/lockedSnapshot 分别由锁外 observer、已验证 req release输入、锁内现有文件/服务观察得到；执行时将 guard 纳入已有 applyService/applyBinaryOnly 调用参数，不在 Acquire/Release 中另开套安装事务。req 文件不新增 JSON 字段。
- [x] 真实 app harness 补充普通升级 Consent{} 成功且不注入 expected flag、准备后目标变化即使无风险/显式 yes 仍拒绝、公开 expected 无 yes 仍报参数错误；验证 ApplyPrepared 保持 start=false，原本 stopped 服务不会因本改动启动，直接 ApplyWithConsent 保持 start=true。验证 first check→目标同版本换内容→expected-preview 重试被拒；yes 不忽略已观察到的变化；不把 service Running 变化当定义变化。运行 app、platform 与现有安装集成测试。建议提交：`feat: 在 Unix 安装事务前校验替换确认`。

## Task 7：Windows 自更新服务目标与部分成功

**Files:** Create `internal/app/replacement_targets.go`、`replacement_targets_test.go`；Modify `internal/app/self_update.go`、`self_update_test.go`、`internal/service/service.go` 及相邻测试、`cmd/mihari/windows_layout.go`。

**Interfaces:**

```go
// Per-call adapter: never persist callbacks or previews in service state.
type ServiceReplacementChecks struct {
    BeforeStop func(context.Context) error
    BeforeStage func(context.Context) error
}
func (m *Manager) UpdateInstalledBinaryChecked(context.Context, ServiceReplacementChecks) (bool,error)
// ServiceReplacementView is defined in internal/service; the native method is Windows-only.
type ServiceReplacementView struct {
    Registered bool
    BinaryPath string
    DefinitionSHA256 string
}
func (m *Manager) ObserveReplacementService(context.Context) (ServiceReplacementView,error)
// internal/app
func (c *SelfUpdateServiceCompletion) ObserveReplacement(context.Context,string) (update.ReplacementSnapshot,error)
func (c *SelfUpdateServiceCompletion) AfterPreparedReplace(context.Context,update.PreparedUpdate) error
```

`ServiceReplacementChecks` 和 ServiceReplacementView 定义在 internal/service，Manager 为 service.Manager；app的ObserveReplacement及AfterPreparedReplace使用平台中立的注入接口，必须放在通用replacement_targets.go/self_update.go以便Linux上的fake测试编译，不得让通用文件调用仅Windows编译的方法。app 使用最小消费者接口更新测试 fake。`InstalledServiceUpdater` 在app使用的最小接口同时声明上述Checked方法和ObserveReplacementService；更新相邻fake。ObserveReplacementService只读实际Windows服务定义与Mihari选定安装路径，不能按运行/停止状态猜路径。旧 UpdateInstalledBinary() 保留委托无检查版本，仅兼容非本功能旧内部调用；新的 Windows self-update 必须注入带预览路径，不能只让测试启用。

- [x] 写 app/service fake 事件测试：目标包含用户运行副本与安装副本、同文件多角色；主程序已替换后服务定义变化时不 Stop、不 stage；Stop后发现服务副本变更时不 stage，并尝试恢复原运行状态。

```go
func TestServiceReplacement_ChangedTargetStopsBeforeStage(t *testing.T) {
    t.Setenv("MIHARI_INSTALL_ROOT",t.TempDir())
    source := writeTempBinary(t,t.TempDir(),"candidate")
    events := []string{}
    controller := &fakeController{status:StatusRunning,events:&events}
    manager := New(Options{Executable:source,
        NewController:func(RunFunc,string,[]string)(Controller,error){ return controller,nil },
    })
    stageCalls := 0
    manager.stageBinary = func(string)(string,error){ stageCalls++; return "",nil }
    _, err := manager.UpdateInstalledBinaryChecked(context.Background(),ServiceReplacementChecks{
        BeforeStop:func(context.Context)error{ events=append(events,"check-stop"); return nil },
        BeforeStage:func(context.Context)error{ events=append(events,"check-stage"); return errors.New("changed") },
    })
    if err == nil || stageCalls != 0 || controller.stops != 1 || controller.starts != 1 {
        t.Fatalf("stage=%d stops=%d starts=%d err=%v",stageCalls,controller.stops,controller.starts,err)
    }
    if strings.Join(events,",") != "status,check-stop,stop,check-stage,start" { t.Fatal(events) }
}
```

此测试使用现有 fakeController 和真实 Manager Checked 方法，验证实际控制流；另加 StatusStopped 情况断言无自动Start，不用只调用回调本身的测试代替。
- [x] 加可编译 Checked 委托旧方法后，运行 service/app 对应新测试，确认因为旧代码先 stage 或忽略检查而失败。
- [x] 新方法在 Stop、stage 前分别调用 checks，复用原服务补偿/净化错误。只在原服务实际 running 时恢复 Start；不无条件启动原本 stopped 的服务。检查失败保留前面已完成的主程序替换事实。
- [x] app AfterPreparedReplace 消费原 service-role 预览，定义摘要仍与原一致；若 service角色与已更新主程序同文件，该文件预期内容应为已确认 candidate digest，不拿旧FileID拒绝自己的成功替换。仅豁免明确由本次完成的那一个文件，不能泛化为跳过全部目标检查。
- [x] cmd Windows 将 `selfUpdateCompletion.ObserveReplacement` 注入 ObserveTargets，将 `selfUpdateCompletion.AfterPreparedReplace` 注入 AfterReplacePrepared；同一进程内串行当前操作。不加全局 named mutex，不对外部安装器提供原子 CAS 保证。测试 Result.Updated=true+同步警告保持且服务新副本不被覆盖。
- [ ] 运行 `go test ./internal/app ./internal/service ./internal/update ./cmd/mihari`；Windows 专用测试在原生 Windows job 验证，当前宿主记录未执行项。建议提交：`feat: 在 Windows 服务同步前复核替换预览`。

## Task 8：CLI self update 与 service apply 的确认契约

**Files:** Modify `internal/cli/self.go`、`self_test.go`、`root.go`、`service_apply.go`、`service_apply_test.go`、`cmd/mihari/unix_layout.go` 及装配测试。

**Interfaces:**

```go
// internal/cli: replace the old Update-only dependency.
type SelfUpdater interface {
    Prepare(context.Context,string,string,string) (update.PreparedUpdate,error)
    ApplyPrepared(context.Context,update.PreparedUpdate) (update.Result,error)
}
// Dependencies field replacement; install-request JSON itself stays unchanged.
ServiceApply func(context.Context,app.InstallRequest,update.ReplacementConsent) (app.InstallResult,error)
```

unix_layout 的纯依赖拒绝函数、真实 installer.ApplyWithConsent 和所有 fake 同一任务更新，防止测试使用旧无保护委托。

- [x] 写 self update 无 yes 降级拒绝、yes 接受、unknown 提示、ahead/noop、参数错误及 --json 分流测试；更新 fake 记录 prepare/apply 次数和 Consent。

```go
type confirmedSelfUpdaterFake struct {
    prepared update.PreparedUpdate
    calls int
    consent update.ReplacementConsent
}
func (f *confirmedSelfUpdaterFake) Prepare(context.Context,string,string,string) (update.PreparedUpdate,error) {
    return f.prepared,nil
}
func (f *confirmedSelfUpdaterFake) ApplyPrepared(_ context.Context,p update.PreparedUpdate) (update.Result,error) {
    f.calls++
    f.consent=p.Consent
    return update.Result{Updated:true,Version:p.Version,Channel:p.Channel},nil
}
```

用 Task 4 NewReplacementPreview 构造真实风险 p.Preview；缺确认应在 CLI 调用 Apply 前由 ValidateReplacementConsent 拒绝，fake.calls=0；实际用例仍重复门禁，不能只依靠 CLI。
- [x] 添加 flags 的可编译壳但不改变行为，运行 `go test ./internal/cli -run 'TestSelfUpdate|TestServiceApply' -count=1`，确认无 yes 仍 apply/未转发 expected 的行为断言失败。
- [x] self update 实现准备/提示/确认/执行顺序；无可执行候选不生成风险提示。先校验 Consent，再执行；显式 yes 不隐藏风险。文本模式在 Apply 前输出安全警告，JSON 模式缓冲警告到执行与候选清理结束。用幂等 Close 和 errors.Join 处理所有清理路径。

```text
Prepare → 取得安全 warning → ValidateReplacementConsent
  校验失败：返回风险 APIError，由 Execute 输出；不先写纯文本
  文本模式：执行前写 warning，写失败则拒绝执行
  JSON 模式：只缓冲 warning，不写 stderr
ApplyPrepared → Close（合并执行/清理错误）
  JSON 成功：warning 写 stderr，成功 envelope 写 stdout
  JSON 失败：warning 合入已分类 APIError.Message，保留 Code/Details，
             Execute 向 stderr 只输出一个错误 envelope
```

上述分流用于已确认后提交复核、替换或清理失败，不能仅处理缺确认失败。stdout/stderr 的成功与失败契约保持；不增加成功 JSON 字段。
- [x] service apply 解析 --yes/--expected-preview，验证参数组合后传同一个 Consent 给 app。recover 请求不新增风险确认；expected 无 yes 或格式错误在 app 调用前失败。

```go
consent := update.ReplacementConsent{Yes:yes,ExpectedPreview:expected}
// Warn uses the same per-call text/JSON warning sink as self update.
consent.Warn = warningSink
result, err := deps.ServiceApply(cmd.Context(),req,consent)
// Finalize buffered warning with err before rendering the unchanged result.
```

warningSink 仅报告安全字符串：app 在锁外 Validate 成功后、任何安装写入前调用一次；文本模式即时写入并返回写错误，JSON 模式只保存文案。执行结束按上述分流处理。Warn 不参与确认权限、指纹、安装请求、持久化或公开结果；不引入新的通用输出框架。
- [ ] 两个 CLI 入口均用完整 Execute 回归测试 `--yes --json` 后用例失败：stderr 必须能一次解析且只含一个 envelope，风险仍可见、Code/Details 不变；self update 另覆盖候选清理失败；成功 stdout 格式不变。

- [x] 用完整 Execute 路径测试风险错误 reason/risk/targets/target_version/preview_id，而不只测 Cobra RunE。错误字段中不能出现候选真实路径或凭据。成功的 InstallResult JSON 仍被 app.DecodeInstallResult 接受。
- [x] 运行 `go test ./internal/cli ./cmd/mihari ./internal/app -count=1`；每个测试设置 MIHARI_DATA=t.TempDir，注入 elevated checker 后 cleanup 恢复。建议提交：`feat: 增加 CLI 降级确认与预览绑定参数`。

## Task 9：TUI 先准备，再按真实候选确认

**Files:** Create `internal/tui/pages/system/replacement.go`、`replacement_test.go`；Modify `model.go`、`model_test.go`、`internal/tui/prepared_update.go`、`prepared_update_test.go`、`run.go`、`ui/action.go`、`ui/strings.go`、`actions.go`、`internal/tui/model.go`、`model_test.go` 与现有 fake。

**Interfaces:**

```go
// systempage SelfUpdater no longer exposes the unprepared Update fallback.
type SelfUpdater interface {
    Check(context.Context,string,string) (update.CheckResult,error)
    Prepare(context.Context,string,string,string) (update.PreparedUpdate,error)
    ApplyPrepared(context.Context,update.PreparedUpdate) (update.Result,error)
}
// PreparedSelfUpdater becomes an alias, preserving Run type references.
type PreparedSelfUpdater = SelfUpdater
```

在 System Model 只增加本功能的 generation 和 pending prepared 引用；实际 candidate owner 仍是 runPreparedUpdater。函数命名固定：`startMihariPreparation() tea.Cmd`、`confirmPreparedMihariUpdate(update.PreparedUpdate) tea.Cmd`。`ui.ActionIntentMsg` 增加可选 `Cancel tea.Cmd`，既有行为零值保持。新增页面结果 `preparedMihariConfirmedMsg{prepared update.PreparedUpdate}` 并实现 `Err() error { return nil }`，让确认执行结果经过现有 action result→page 路由；页面收到此结果后才发 RelaunchRequestMsg。

候选释放约定：新增 `ui.DiscardPreparedUpdateMsg{Prepared update.PreparedUpdate}`；Run在root Model注入 `discardPrepared func(update.PreparedUpdate) error`，指向 `runPreparedUpdater.discard(p update.PreparedUpdate) error`，该方法调用p.Close。root处理Discard消息时在tea.Cmd调用此函数并返回 `discardPreparedResultMsg{err error}`，该消息实现Err并沿现有净化错误提示。Run最终关闭仍幂等Close已记录候选，避免新未闭合所有者。

- [x] 在新 replacement_test.go 用现有 fakeSelfUpdater 扩展 Prepare，构造 Check 显示 v2.0.0、Prepare 实际 v1.0.0 的场景；断言最终 Object/Impact 来自 prepared.Preview，准备完成不能直接产生 RelaunchRequestMsg。

```go
func TestPreparedConsent_BindsTheDisplayedCandidate(t *testing.T) {
    snapshot := update.ReplacementSnapshot{Targets:[]update.ReplacementTarget{{
        Roles:[]string{"binary"},Path:"/fixture/mihari",FileID:"old",
        Exists:true,SHA256:strings.Repeat("b",64),Version:"v2.0.0",
    }}}
    preview, err := update.NewReplacementPreview(update.ReplacementCandidate{
        Version:"v1.0.0",SHA256:strings.Repeat("a",64),Channel:"main",
    },snapshot)
    if err != nil { t.Fatal(err) }
    model := New(nil,func() string { return "operation" })
    cmd := model.confirmPreparedMihariUpdate(update.PreparedUpdate{Available:true,Version:"v1.0.0",Preview:preview})
    intent, ok := cmd().(ui.ActionIntentMsg)
    if !ok || !strings.Contains(intent.Object,"v1.0.0") || !strings.Contains(intent.Impact,"data loss") {
        t.Fatalf("wrong confirmation: %T",cmd())
    }
}
```

执行层测试必须驱动意图的 Execute，经现有 action result 路由回页面后，再检查 relaunch message 中 `Consent.Yes=true, ExpectedPreview=preview.ID`；取消只走 Cancel，不发 relaunch。
- [x] 提供最小可编译新方法后运行 `go test ./internal/tui/pages/system -run 'TestPreparedConsent|TestSystemMihari' -count=1`；旧“准备成功直接退出”行为必须在新断言中失败。
- [x] 进入 Update 行后直接 Prepare，显示 Preparing；no-op/ahead 更新原行文本。成功候选触发新的 ActionIntent，降级/unknown用 Task4 完整风险；普通升级保留通用确认。明确用户取消后的行状态，不把取消显示成更新成功。

```go
prepared.Consent = update.ReplacementConsent{Yes:true,ExpectedPreview:prepared.Preview.ID}
return preparedMihariConfirmedMsg{prepared:prepared}
```

页面收到确认结果后返回一个产生 `ui.RelaunchRequestMsg{Prepared:&prepared}` 的命令。此赋值只出现在用户接受后执行的闭包，不在 Prepare 完成时提前设置。
- [x] 将 Cancel 接入 root action controller 的取消分支（Esc、拒绝、切页）；Cancel命令只产生DiscardPreparedUpdateMsg，由root调用Run-owned释放方法。PreparedUpdate.Close 本已幂等，Run 的最终清理可以重复调用；generation 失效的迟到结果只清理，不能再确认。
- [x] 覆盖准备中退出、确认时取消、切换通道/页面、连续点击、Run cleanup失败、Apply前身份变化、Apply后服务同步失败。用阻塞fake的channel观察取消与Wait，不写固定Sleep。确保 cleanup→apply→relaunch 顺序；Apply拒绝后旧TUI已退出，输出明确重启提示，不重开确认框。
- [x] 运行 `go test ./internal/tui/... ./cmd/mihari -count=1` 和 `go test -race ./internal/tui/...`；验证动作权限仍要求已提权 shell。建议提交：`feat: 按准备后的候选确认 TUI 更新风险`。

## Task 10：Unix 三脚本的可信 helper、指纹重试与生成块

**Files:** Modify `scripts/install/root-apply.sh.in`、`generate_root_apply.py`（仅模板新内容需要时）、`test_root_apply_entry.py`、`test_root_apply_generated.py`；Generated `install.sh`、`install-aio.sh`、`install-aio-remote.sh`；Create `scripts/install/test_replacement_confirmation.py` 中 POSIX 场景。

**Interfaces:** root_apply 的现有位置参数保持前10项，新增第11项固定 yes 值（0/1）；helper CLI 消费 `--yes --expected-preview`，错误JSON在stderr；不新增安装请求JSON字段或依赖 jq/python 运行安装。

- [x] 扩展现有 RootApplyEntryTests 的真实模板片段执行器，fake helper 用同一次调用计数文件返回确认错误，然后验证第二次参数。以 Python 标准库 json 产生 fixture，脚本执行生产模板逻辑，不复制一份确认逻辑作被测对象。

```python
def confirmation_error(preview_id):
    return {"schema":"mihari.error/v1","error":{
        "code":"invalid_argument",
        "message":"Older Mihari versions may fail to load current data.",
        "details":{"reason":"replacement_confirmation_required",
                   "risk":"downgrade","targets":[{"roles":["service"],"version":"v2.0.0"}],
                   "target_version":"v1.0.0","preview_id":preview_id}}}

def test_root_confirmation_reuses_original_preview(tmp_path):
    source = (Path(__file__).parent / "root-apply.sh.in").read_text()
    block = source.split("# BEGIN REPLACEMENT CONFIRMATION\n",1)[1].split("# END REPLACEMENT CONFIRMATION",1)[0]
    stage = tmp_path / "stage"
    stage.mkdir()
    error_file = tmp_path / "fixture-error.json"
    error_file.write_text(json.dumps(confirmation_error("a"*64),separators=(",",":"))+"\n")
    helper = tmp_path / "helper"
    helper.write_text(
        '#!/bin/sh\nprintf "%s\n" "$@" >> "$ARG_LOG"\n'
        'if [ ! -f "$COUNT" ]; then : > "$COUNT"; cat "$FIXTURE_ERROR" >&2; exit 2; fi\n'
        'printf "accepted\n"\n')
    helper.chmod(0o700)
    env = dict(os.environ, ARG_LOG=str(tmp_path/"argv"),COUNT=str(tmp_path/"count"),FIXTURE_ERROR=str(error_file))
    command = "\n".join([
        "set -eu", "stage="+shlex.quote(str(stage)), "entry="+shlex.quote(str(helper)),
        "explicit_yes=0", 'fail() { printf "%s\n" "$1" >&2; exit 1; }',
        block, 'confirm_replacement() { return 0; }', "apply_with_confirmation"])
    result = subprocess.run(["sh","-c",command],env=env,capture_output=True,text=True,timeout=10)
    assert result.returncode == 0, result.stderr
    args = (tmp_path/"argv").read_text().splitlines()
    assert args.count("--expected-preview") == 1
    assert args[args.index("--expected-preview")+1] == "a"*64
    assert args.count("--yes") == 1

```

本用例使用生产模板函数，仅将用户接受的输入边界替换为测试回调；另用伪终端测试真实y/n读取。按同一模板片段执行方式覆盖无终端、n、坏JSON、错误code、空/重复preview_id、第二次invalid_state都没有第三次调用。当前 protocol.NewError 的 schema 是 mihari.error/v1（不是成功响应的 mihari/v1）；fixture与Go错误输出做一次本地交叉验收。
- [x] 运行 `python3 -m pytest scripts/install/test_root_apply_entry.py scripts/install/test_replacement_confirmation.py -q`，旧 helper 选择/重试缺失导致行为失败。
- [x] 先改模板的 helper 选择：检查可信现有 helper 是否同时声明两个flag；不支持时 online用当前通道固定helper tag下载并独立核checksum，目标tag/digest仍为用户选择。offline不联网；无能力时报清楚准备新版helper的方法。helper路径、candidate路径分开保存，不能再次赋成同一目标旧binary。
- [x] 将 MIHARI_YES 精确1或远程 --yes 转成固定位置参数，跨 `/usr/bin/env -i` 传递，避免sudo丢环境。root bootstrap捕获首次状态码，且分别保存stdout与stderr到自己0700 staging下0600文件：

```sh
status=0
"$entry" service apply --request "$stage/request.json" --json >"$stage/result.json" 2>"$stage/error.json" || status=$?
if [ "$status" -eq 0 ]; then
  cat "$stage/result.json"
elif [ "$status" -eq 2 ]; then
  preview_id=$(read_confirmation_preview "$stage/error.json") || fail "installation requires review; no changes made"
  confirm_replacement "$stage/error.json" || fail "cancelled; no installation changes made"
  "$entry" service apply --request "$stage/request.json" --json --yes --expected-preview "$preview_id"
else
  cat "$stage/error.json" >&2
  exit "$status"
fi
```

本任务在模板的 `# BEGIN REPLACEMENT CONFIRMATION` / `# END REPLACEMENT CONFIRMATION` 之间定义 `apply_with_confirmation()`（读取stage、entry、explicit_yes）、`read_confirmation_preview(file)` 和 `confirm_replacement(file)`；模板真实执行路径必须调用apply_with_confirmation。代码块需包在该函数内：前者只接受当前helper产生的单行、受限大小错误JSON，严格检查code/reason及唯一64位小写hex preview_id，拒绝控制字符、额外同名键/多行、多对象，不 eval；后者输出固定英文风险+安全版本/角色，使用可读终端且默认否。无法安全解析时拒绝，不能退回仅 --yes。显式yes首次调用直接带 --yes，仍由helper打印风险；不走交互重试。
- [x] 终端读取从 /dev/tty，仅检查可用后读；不是stdin为管道就无限等待。清理列表加入result/error/helper/candidate各自临时文件；错误不能把仍需用户手动安装的原bundle删除。真实执行不读MIHARI_TEST_*。
- [x] 运行生成器和一致性测试：

```sh
python3 scripts/install/generate_root_apply.py
python3 scripts/install/generate_root_apply.py --check
python3 -m pytest scripts/install/test_root_apply_entry.py scripts/install/test_root_apply_generated.py scripts/install/test_replacement_confirmation.py scripts/install/test_install_channel.py -q
```

- [x] 检查三份生成区完全来自模板；MIHARI_NO_INSTALL 下载-only 仍不触发安装确认。建议提交：`feat: 贯通 Unix 安装脚本的降级确认`。

## Task 11：Windows 普通与离线安装的确认边界

**Files:** Modify `scripts/install/install.ps1`、`install-aio.ps1`、`test_install_channel.py`；Extend `test_replacement_confirmation.py` Windows场景。

**Interfaces:** 两份独立脚本各自携带以下小函数，签名相同；测试从源码提取函数运行，之后运行隔离的完整流程测试：

```powershell
function Get-ReplacementRisk([string]$Current, [string]$Target) { }
function Get-ReplacementPreview([string]$Candidate, [string]$TargetVersion, [string[]]$Targets) { }
function Assert-ReplacementPreview($Expected, [string]$Candidate, [string[]]$Targets) { }
function Confirm-Replacement($Preview, [bool]$ExplicitYes) { }
```

空函数仅为首先编译的Red脚手架，完成时实现下面逐步规定。不得借空函数通过流程。预览为进程局部PS对象，包含候选digest和各目标规范路径/文件身份/摘要/版本及服务定义；不要求与Go计算相同preview_id，跨PS提权回传原对象的固定摘要并由同一PS实现复核。

- [x] 用 Task1 JSON矩阵参数化测试两份脚本的 Get-ReplacementRisk，断言normal、downgrade、unknown；为 UInt溢出/无法安全查询旧版本写用例。先运行新测试证明空函数不能正确分类。
- [x] 实现 canonical比較，超出可表示范围unknown，使用同一fixture；Get-ReplacementPreview 只列实际将覆盖的dest和已注册服务安装副本，大小写/短路径规范化后去重。管理员不得运行用户可写旧binary；版本探测超时/输出上限与Task3一致，不能安全探测时unknown。
- [x] 普通安装先解析latest API得到固定tag，固定URL下载到临时文件，候选与release身份依既有校验核对；AIO没有可信tag时用unknown候选，禁止为分类执行包内未获信任binary。未知候选的digest仍参与绑定。
- [x] 完整流程测试用临时USERPROFILE/LOCALAPPDATA/MIHARI_DATA/MIHARI_BIN、假服务和本地下载fixture，记录stop/copy/PATH/sidecar事件。production逻辑必须走到真正确认点，拒绝时事件全空，candidate准备临时IO允许。

```powershell
$preview = Get-ReplacementPreview -Candidate $tmp -TargetVersion $targetTag -Targets $targets
if (-not (Confirm-Replacement -Preview $preview -ExplicitYes ($env:MIHARI_YES -eq '1'))) {
    throw 'Cancelled. No installation changes were made.'
}
Assert-ReplacementPreview -Expected $preview -Candidate $tmp -Targets $targets
```

把此块放在设置PATH、sidecar、Stop-Service、Copy-Item/Move-Item及core/GeoIP复制之前；停机提示合并进最终确认。显式yes照样输出风险；没有风险的非交互原本可执行路径继续，不要求新yes。
- [x] UAC交接以临时JSON传原预览/候选信息，不拼接输入进PowerShell代码；提权端验证文件来源与目标/候选摘要再执行既有操作。其它环境、权限策略不扩展。若中途已写主程序后发现服务变化，输出部分成功，不能说No changes；AIO也不得在部分复制后输出零修改。
- [x] install-aio.ps1 在param后、任何bundle检查/目录创建之前实现能力查询并加入固定整行标记：

```powershell
# MIHARI_INSTALL_CAPABILITY: replacement_confirmation_v1
if ($Capabilities) {
    @{ schema = 'mihari.install-script/v1'; capabilities = @('replacement_confirmation_v1') } |
        ConvertTo-Json -Compress
    return
}
```

param增加 `[switch]$Capabilities`；测试 `-Capabilities` 在不存在的BundleDir下成功且目录仍不存在。
- [ ] Windows原生运行 `python -m pytest scripts/install/test_replacement_confirmation.py scripts/install/test_install_channel.py -q`，明确必须用PowerShell5.1覆盖而非仅pwsh；Linux缺解释器的skip不算Windows通过。建议提交：`feat: 增加 Windows 安装与离线包的降级确认`。

## Task 12：远程 AIO 的确认传递与旧 bundle 能力兼容

**Files:** Modify `scripts/install/install-aio-remote.ps1`、`install-aio-remote.sh`（仅非生成区）、`test_root_apply_entry.py`；Create `scripts/install/test_remote_replacement_confirmation.py`（独立远程入口测试，避免与 Task11 共改）。

**Interfaces:** 远程 -Yes/--yes 和 MIHARI_YES=1 合并为本次固定布尔值；通用下载计划接受不等于降级接受。Windows内层使用Task11能力接口，Unix使用Task10 helper契约。

- [x] 写旧PowerShell脚本fixture：没有能力标记，且忽略未知参数会写sentinel。远程必须在调用任何旧脚本前拒绝，sentinel不存在；保留已校验解压包，提示当前本地脚本安装路径。

```python
def test_remote_rejects_old_bundle_without_executing_it(tmp_path):
    exe = shutil.which("powershell") or shutil.which("pwsh")
    if exe is None:
        pytest.skip("PowerShell is unavailable")
    old = tmp_path / "install-aio.ps1"
    old.write_text("param([string]$BundleDir)\nSet-Content -LiteralPath (Join-Path $BundleDir 'executed') -Value 'yes'\n")
    source = (Path(__file__).parent / "install-aio-remote.ps1").read_text()
    block = source.split("# BEGIN VERIFIED LOCAL HANDOFF\n",1)[1].split("# END VERIFIED LOCAL HANDOFF",1)[0]
    def literal(path):
        return "'"+str(path).replace("'","''")+"'"
    driver = tmp_path / "driver.ps1"
    driver.write_text("$ErrorActionPreference='Stop'\n"+block+"\n"+
        "Invoke-VerifiedLocalInstaller -Installer "+literal(old)+
        " -BundleDir "+literal(tmp_path)+" -Channel main -ExplicitYes $true -VerifiedSource $true\n")
    result = subprocess.run([exe,"-NoProfile","-NonInteractive","-File",str(driver)],
                            capture_output=True,text=True,timeout=10)
    assert result.returncode != 0
    assert not (tmp_path/"executed").exists()
    assert old.exists()

```

此例提取真实生产交接函数执行，已校验来源仅由测试注入；完整流程测试仍需验证只有checksum通过后才传VerifiedSource=true。
- [x] 添加带标记但能力JSON错误/缺schema/错误capability fixture；有标记才可做有界能力查询，结果错误拒绝；缺校验来源不执行任意脚本。先运行相应用例确认旧无条件交接会触发sentinel而失败。
- [x] 修改远程PS：下载后校验/解压，静态精确匹配能力标记整行再调用-Capabilities。新增独立函数 `Invoke-VerifiedLocalInstaller([string]$Installer,[string]$BundleDir,[string]$Channel,[bool]$ExplicitYes,[bool]$VerifiedSource)`，把现有交接接到该函数；在源码以 `# BEGIN VERIFIED LOCAL HANDOFF` / `# END VERIFIED LOCAL HANDOFF` 包围该函数供测试提取；不得把函数替换成只用于测试的副本。只在验证成功后交接；旧包拒绝提示固定为：

```text
This bundle contains an installer without replacement confirmation support. The verified bundle has been kept. Use the current install-aio.ps1 with -BundleDir pointing to this extracted bundle to install it safely.
```

不在报错中回显未校验脚本内容。MIHARI_BUNDLE_URL等既有无校验覆盖路径不得借能力探测获得新执行信任；保持原安全检查或清楚拒绝自动探测/交接。
- [x] 显式yes只在受保护内层执行期间传MIHARI_YES=1；finally恢复原值/不存在状态。仅“开始下载”交互接受时不设置MIHARI_YES，由内层展示实际版本确认。

```powershell
$previousYes = $env:MIHARI_YES
try {
    if ($explicitYes) { $env:MIHARI_YES = '1' }
    & $localInstaller -BundleDir $workdir -Channel $Channel
    if ($LASTEXITCODE -and $LASTEXITCODE -ne 0) { throw 'Local installation failed.' }
} finally {
    if ($null -eq $previousYes) { Remove-Item Env:MIHARI_YES -ErrorAction SilentlyContinue }
    else { $env:MIHARI_YES = $previousYes }
}
```

使用当前脚本原有适配处理-Channel为空，不能新增非法空通道。脚本异常与exit code都检查，不依赖可能陈旧的LASTEXITCODE判断PowerShell异常；调用前清理本地退出码捕获状态，catch后保留错误并停止。
- [x] sh远程的YES合并MIHARI_YES，并显式传root_apply新增参数；保持从root-apply模板生成的区块不手改。测试no-terminal/yes/cancel/inner failure及不污染父命令。
- [x] 运行全部scripts/install pytest及现有parallel downloader unittest，确保Range、checksum、channel路由未被本功能改变。建议提交：`feat: 校验远程 AIO 安装器能力并传递确认`。

## Task 13：跨层验收、CI接入与用户文档

**Files:** Create `internal/integration/replacement_confirmation_test.go`；Modify `.github/workflows/ci.yml`、`docs/commands.md`、`docs/architecture.md`、`docs/distribution.md`、安装脚本头部帮助；README只在命令摘要需新增说明时作最小更新。

**Interfaces:** 不增加业务接口；跨层测试消费新版cli.Dependencies、真实update风险/候选用例及fake替换/服务边界。

- [x] 写CLI→真实准备/确认→fake替换的集成测试，使用httptest固定release，不是fake直接返回预期Result。断言缺yes退出2且目标bytes不变；yes路径一次替换、stdout JSON可解析、stderr包含完整风险。把Task4安全错误details与Task10脚本fixture做交叉核对。
- [x] 写跨层时序场景：latest从A变B，确认仍为A；目标定义在两次helper间变更拒绝；Windows主程序成功、服务目标后来变化返回部分成功。后者使用平台中立fake驱动真实app completion，Windows实际适配另由原生测试承担。

```go
// Assertion used after Execute and reading the temporary target:
if exit != cli.ExitUsage || !bytes.Equal(before, after) || replacementCalls != 0 {
    t.Fatalf("unconfirmed replacement: exit=%d calls=%d",exit,replacementCalls)
}
var envelope map[string]json.RawMessage
if err := json.Unmarshal(stderr.Bytes(), &envelope); err != nil {
    t.Fatalf("JSON error contract broken: %v",err)
}
```

这些变量来自本测试的临时binary、httptest和带计数的替换adapter，不能引用真实安装。Red先以现有缺失行为的断言失败记录，再修最小编排遗漏。
- [x] CI已有三OS unit矩阵、pytest依赖、race/vet/六平台build。只将新测试接入现有步骤，不新设覆盖率门槛或升级Actions：

```yaml
- name: Test replacement confirmation
  run: python -m pytest scripts/install/test_replacement_confirmation.py -q
```

Windows job应验证调用powershell.exe（5.1）可用，不能让全部PS用例skip但仍显示成功；Unix保持现有隔离security检查。新增fixture/测试文件由同一次PR纳入。
- [x] 文档精确写清楚：self update --yes；service apply --yes/--expected-preview；unknown确认；MIHARI_YES=1；旧helper/旧Windows内层的受保护替代路径；Windows复核窗口和部分成功。修正distribution“stable撤回会自动降到次高版本”为stable ahead保持，不增加回滚命令。

```text
When the installed stable version is ahead of the selected release, self update keeps the current version. An installation or an already-supported cross-channel replacement that selects an older version requires explicit confirmation. Confirmation does not migrate or restore configuration data.
```

- [ ] 先运行相关包和集成，再执行最终验证，不因一次通过重复跑无关检查：

```sh
go test ./internal/update ./internal/platform ./internal/app ./internal/service ./internal/cli ./internal/tui/... ./cmd/mihari
go test ./internal/integration
go test ./...
go test -race ./...
go vet ./...
gofmt -l cmd internal
python3 scripts/install/generate_root_apply.py --check
python3 -m pytest scripts/install scripts/test/test_unix_layout_security.py -q
```

- [x] 在临时目录编译六目标，不提交二进制；脚本变量避免系统保留名称：

```sh
build_dir=$(mktemp -d)
for target_os in windows linux darwin; do
  for target_arch in amd64 arm64; do
    CGO_ENABLED=0 GOOS="$target_os" GOARCH="$target_arch" go build -o "$build_dir/mihari-$target_os-$target_arch" ./cmd/mihari || exit 1
  done
done
```

构建产物目录记录在执行笔记，结束仅删除本次mktemp创建的目录。若本机不支持race或缺PowerShell5.1，记录限制并交由原有原生CI结果核实，不能以交叉编译替代原生运行测试。
- [x] 核对全部文件diff及设计覆盖表，不包含#194、CHANGELOG、依赖、用户修改、coverage.out或生成二进制。建议最终文档提交：`docs: 说明 Mihari 降级确认与兼容性边界`。创建/推送PR前需要既有明确授权；合并仍需用户确认。

## 14. 依赖顺序与检查点

```text
Task 1 → Task 2 → Task 3 → Task 4 → Task 5 → Task 6 → Task 7
       → Task 8 → Task 9 → Task 10 → Task 11 → Task 12 → Task 13
```

采用串行顺序避免接口/同文件并行冲突。每完成一个Task运行其最小验证并记录；Task4、Task7、Task9、Task12结束是语义检查点，分别检查预览契约、执行边界、TUI生命周期、脚本兼容性，不要求额外subagent。

## 15. Spec覆盖与计划自检

| 已批准设计要求 | 执行任务 |
| --- | --- |
| 范围、stable ahead、dev排序、文档冲突 | 1、13 |
| 实际所有替换目标与unknown | 2、3、4、6、7、11 |
| 3秒/4KiB、无root执行用户binary | 2、3、11 |
| fixed candidate、不受stale Check影响 | 5、9、10、11 |
| preview_id/expected-preview及JSON安全 | 4、6、8、10 |
| Unix pending/明确recover/无服务不建B | 6、8、10 |
| Windows服务副本与部分成功、并发限制 | 5、7、11、13 |
| TUI Prepare→确认→退出→Apply与清理 | 9 |
| 六脚本、非交互、yes、旧helper/旧bundle | 10、11、12 |
| 生成模板一致性 | 10、13 |
| 不改变成功JSON/持久格式/依赖/schema | 4、6、8、13 |
| 原生PS5.1、三OS、六CGO0build | 11、12、13 |

- [ ] 执行者在开始前确认全部新增接口声明与调用一致，尤其 CLI SelfUpdater 和 ServiceApply 签名一次性更新。
- [ ] 每次准备到下一检查点前确认代码仍可编译；不可编译过渡只在同一个本地编辑步骤内，不提交。
- [ ] 错误状态中区分“尚未替换”“主程序已更新”“服务同步失败”；测试报告不把设计限制变成实现通过声明。
- [ ] 结束时逐项填任务checkbox和实际验证结果；本计划创建时所有实施checkbox保持未完成。

## 16. 限定 scope 的迭代审核

用户要求审核直到 agent 通过，且不得扩大 scope。首轮发现两项 P2，已修正 Task6 的内部快照/公开确认混用及 start 策略遗漏；设计 §5.1 同步澄清。13 个任务边界保持不变；不新增功能、依赖或持久化契约。详见[迭代审核记录](../reviews/2026-09-09-issue-193-plan-review.md)。第二轮 subagent 复审结论为 **PASS**，两项问题关闭，无剩余阻塞；这是文档审核结论，不表示功能测试通过。

## 17. 执行记录

- 用户已授权按设计/计划开发、提交并创建 PR，随后每十分钟检查 CI 与 bot review；此前“待执行授权”的历史文字已由本次指令取代。scope 保持 #193。
- 执行按依赖顺序；平台观察作为独立文件任务交给 subagent，主代理处理环境验证与整合。并行不改同一文件。
- Task1 完成：共享 16 组版本 fixture；首次测试证明 downgrade/unknown 被错误分类及四种整数溢出被接受；实现后 Go 1.26.5 `go test ./internal/update -count=1` 通过。stable ahead 与同基础 dev→stable 选择保持。
- 初始基线误用了系统 Go 1.27.1（不计项目验证）；app/TUI 出现 root 在用户目录执行的信任/私有目录限制。后续固定 Go 1.26.5，并用原 HEAD 的隔离非 root 副本复核，不修改无关生产或测试安全策略。
- Task2 进行中；Task3–13 尚未完成。尚未提交、推送或创建 PR，CI/bot 轮询从 PR 创建后开始。

- 原 HEAD 的非 root 隔离验证使用 `/home/kinema/.mihari-193-verify-0co5ahr0`，Go 1.26.5；最初 baseline 全部指定包通过。复制 Task1–4 后 `go test ./internal/platform ./internal/update -count=1` 亦通过。该目录仅测试副本，开发仍在 issue-193 worktree，不提交副本。
- Task3 完成：严格单对象版本 JSON、隔离环境、有界子进程和取消回收；先观察解析/查询/取消/超限 Red，后 update 全包及定向 race Green。原生 fixture 由运行测试的固定 GOROOT/bin/go 编译，不运行真实安装。
- Task4 完成：预览深拷贝/稳定指纹、风险优先级、公开确认契约与安全文案；先观察未拒绝陈旧对象等 Red，再 update 全包及定向 race Green。为给 Task3 提供共享类型，先构建 Task4 纯逻辑；未改变任务内容或入口依赖。
- Task1–4 独立审核 `/root/review_193_foundation`：唯一 P2 为 missing target 的父链接未验证；platform agent 已补回归及修复，等待交叉编译与复审。不同实际路径即便 inode 相同仍保留每个替换目录项，避免漏绑定路径变化；审核认可。
- Task5 `/root/implement_193_prepared_update` 进行中（update prepare/self/apply 文件独占）；需要实际 replace 前再次复核，已通知实施者。
- Task7 仅 service 检查回调子步骤已实现：Stop/Stage 前执行本次检查，失败只恢复原本 running 的服务；先 Red 后 `go test ./internal/service -count=1` Green。app Windows 服务观察/预览与装配仍未实现，Task7 不标完成。
- Task6、8–13 未开始实现。当前所有变更未提交，PR 尚未创建；不能以以上局部通过声明 Issue193 完成。

- Task2 P2修复后独立复审通过；Task1–4 foundation spec/quality PASS。平台父链修复定向test/race/vet与Windows/Darwin CGO0测试编译通过；原生macOS/Windows待CI。
- Task5 完成且独立 spec/quality PASS：TDD补充copy中目标变化、stage内容篡改、可变Risk伪造，最终实际替换前复核并重算risk；Go1.26.5 update全包/race/vet通过。实现者未模拟真实文件系统cleanup失败；保留幂等Close与已有可注入cleanup测试，最终验收需记录此限制。
- Task6 由 `/root/implement_193_unix_gate` 施工，Task8由 `/root/implement_193_cli` 施工，文件不重叠。Task7主代理已加Windows service view、app确认快照消费、Stop/Stage检查及装配；正在跨包验证，未标完成。

- 范围内接口补足：service apply 的成功 InstallResult 必须保持 JSON 不变，但需要输出已批准的 yes 风险提示。ReplacementConsent 增加仅本次调用的可选 `Warn func(string) error`，app 在锁外确认校验后报告一次安全风险，不进入持久化或指纹。CLI 文本即时输出；JSON 模式缓冲，成功写 stderr、失败合入现有 APIError.Message，由 Execute 输出单一错误 envelope。self update 同样避免已确认后失败时混入非 JSON stderr。这是现有警告/JSON 契约的衔接，不新增产品范围。

- Task7 定向跨层验证已通过：Go1.26.5 app/service/integration 的 TestServiceReplacement、TestSelfUpdateServiceCompletion、TestSelfUpdateSynchronizes 及相同范围 race。Windows service测试CGO0编译通过，原生测试尚待CI；Task7仍需独立审查和完整装配检查。
- 当前 git user 配置通过实际用户读取为 LeeShunEE / 54445332+LeeShunEE@users.noreply.github.com；最终提交使用此实际配置的身份及DCO，不伪造身份、不自行加Co-Authored-By。

- 追加方案审核（用户再次要求严格 scope）：首轮三项 P2 已同步修订：JSON 警告分流及内部 Warn、按目录项路径合并目标、显式固定 Go 1.26.5。新增别名/硬链接与失败 JSON 验收保持未勾选；本轮不补实现、不宣称这些测试通过。开发工作已保存并暂停。追加第二轮 agent 复审为 **PASS**，三项 P2 关闭，无剩余文档阻塞、无 scope 扩大及越界 P0/P1；详见审核记录。

- 继续执行：用户持久目标仍为完成实现、提交 PR 并每十分钟检查 CI/bot。方案追加复审后恢复；分支/worktree 已再次核实，已有任务不重做。Task9/10实现已保存，最终验证/实现审查待完成；Task11 Windows 脚本由独立实现 agent 处理。
- Task6/7/8实现审查首轮发现一项 P2：离线可信摘要未绑定请求 tag。已 Red→Green：新增离线受信 bytes 的可信私有 stage 版本绑定（不执行请求源路径，不新增权限/格式/网络）；matching/mismatch/unknown/cancel/timeout/untrusted 检查与公共 Apply 伪 tag yes/no 回归、定向 native 临时 fixture race 通过。复审 agent `/root/review_issue_193_design` 返回 PASS，该问题关闭。
- Task7 别名修复：Windows 复用 finalPathFromHandle 返回规范目录项名，保留不同硬链接路径。已有平台定向 Linux 测试通过，Windows 别名测试已先写并交叉编译；本机无法运行原生 Windows Red/Green，待 CI，不宣称通过。
- Task13 新 CLI→真实 Prepare/Apply/临时文件替换集成：无确认拒绝、确认后固定候选、确认后目标变化拒绝、JSON风险/清理均通过。不是 fake 直接返回结果；只替换 CLI 当前进程路径并提供已知版本 fixture，身份和内容观察为生产代码。

- Task9 两项实现 P2 已 Red→Green 并复审 PASS：普通升级目标角色/版本标签；72×22 风险确认按钮可见（仅减少本流程一处多余空行，不重做通用 modal）。
- Go 非 root 隔离副本全量 `go test ./...` 通过；全量 race 仍运行，不能提前标通过。完整 `go vet ./...`、gofmt 已通过；固定 golangci-lint v2.12.2 最终运行 0 issues。lint修复仅同类型断言、错误首字母及 fixture 使用 PATH 的 go 并显式选择 runtime.Version 对应工具链。
- Task12 POSIX 远程显式 yes 传递已5个子例 Red→Green；生成器 --check 通过。为避免与 Task11 并行复审修复争用，远程 PS 新测试放独立 `test_remote_replacement_confirmation.py`，CI 同一现有测试步骤运行两文件；这是测试文件职责细分，不增加功能范围。
- Task11 独立实现审查 portable 51 passed/6 native skipped，当前主路径 PASS；实现者自查短路径别名的写后预览更新可能误拒绝，正按既定目录项语义补定向修复。Task12 远程 PS 实现进行中，最终脚本、native CI 和全分支审查未完成。

- Task11 两轮路径别名 P2 修复后独立复审 PASS：missing/native 的扩展路径与 UNC 表示统一，首次写入后仍绑定同一目录项，独立 hardlink 不合并。最终定向复审 8 passed/4 native skipped；原生 Windows 验收仍待 CI。
- Task12 远程 PS 实现交接：最终远程测试 47 passed；此前全 scripts/install 161 passed/8 skipped/5 subtests passed。后续两项仅远程 JSON 空白及错误流判断修正由最终47项覆盖，正在独立复审；不把该历史全量结果当作最终快照全量验证。
- Task7 追加预览阶段服务变化回归 Red→Green：尚未替换时错误不再误称“Mihari updated”，已有替换后的部分成功语义不变；定向 app race 通过。
- Task13 六目标 CGO0 build 全通过；全量 race 默认10分钟在未修改的 subscription 恢复矩阵超时，其余包通过。保留失败记录，正在以本地30分钟时限复核，不改该包或 CI 时限。
- Task13 将5项新增 native Unix replacement fixture 加入既有 supplemental 显式验收清单，仅接入本功能的已有原生 CI；runner单元测试77 passed，不运行本机账号/mount/真实服务场景。

- Task12 PowerShell与Task7预览错误文案独立复审 PASS，分别47项remote测试、8项root bridge及Go定向回归通过。该审核只覆盖远程确认交接，不替代Unix helper选择的整体入口审核。
- 最终跨层审核发现 Task10/12 的范围内 P2：远程AIO传解压候选却被helper选择视作离线，干净主机/旧helper提前拒绝。正在增加显式online/offline入口参数与回归；本地离线继续禁止隐式网络，候选保持原tag/digest，不改变设计scope。
- Task8 清理失败验收限制：现有包私有cleanup未为CLI增加公共注入接口，CLI完整Execute覆盖apply失败单一JSON，update覆盖清理行为、集成覆盖真实清理成功；未声称完成CLI文件系统清理失败的独立故障注入用例。独立审核确认生产Close先于JSON风险分流，未发现具体错误。

- 最终跨层复审 `/root/review_193_final` 为 PASS：唯一Unix remote helper选择P2已关闭；online/offline显式跨sudo，remote保持候选不变，local默认offline。复审45 passed/5 subtests，生成一致性通过。Task11、Task12 PS各自独立复审均PASS，无剩余具体实现阻塞、未扩大scope或引入#194。
- 最终Go快照 `go test ./...`、`go vet ./...`、gofmt、固定版本lint通过。全量 `go test -race -timeout=30m ./...`通过（subscription 563.211s）；随后唯一Go文案修正另跑相关app race通过。默认10分钟race的历史超时保留，CI不改变该限制。

- 提交前最终完整脚本验证（包含portable pwsh）：176 passed、10 native Windows skipped、5 subtests passed；未把skip标为原生通过。全部82个改动文件均属于#193代码/测试/文档/已有CI接入，无CHANGELOG、依赖、subscription实现或临时制品。文档链接/围栏及diff检查通过。

- 已提交268a971并创建PR #222（dev，Closes #193），工作树保留；首轮13:09 UTC及十分钟后13:19 UTC已实际检查CI与bot。Cubic/Pullfrog进行中；CodeRabbit按组织标签配置跳过，不计审核通过。
- 首轮CI：六目标build、Linux unit/race、三OS vet/format、lint/vuln/coverage、Linux/macOS原生安全及汇总门禁通过；Windows/macOS unit在新脚本测试失败，另外两OS race尚在运行。正在修正，不声明CI完成。
- Windows CI揭示PS7父shell→Python→PS5.1继承不兼容PSModulePath，新增replacement pytest步骤固定Windows原生powershell；不改产品环境权限。参数测试还揭示真实P2：表达式数组被当单参数传递，补count/type回归Red后改为命名string[] splat。
- macOS三个PTY用例看到提示后等待退出超时；fixture改为等待期间继续读取PTY echo/EOF，保留原时限/退出断言，Linux3case通过及独立复审PASS。BSD关闭等待排空属于根因推断，仍须macOS CI证实，不扩大或跳过测试。

- CI修复定向复审PASS：两份PS服务参数使用string[]命名展开（6项便携测试）；remote隔离probe仅搜索所选宿主内置Modules（新环境契约Red→Green、最终48 passed）。前次remote48中1项外层20秒超时，单独4项及无改动完整48复跑通过，未放宽任何时限。原生Windows/macOS仍待下一CI；无新产品范围。

- 13:30 UTC继续十分钟轮询：Cubic首轮23项已逐项核实，4项有效P2最小修复并独立复审PASS；不采纳扩展SCM写入、多文件事务或Windows发布框架，未确认需要扩大scope的致命P0/P1。完整判定见[bot triage](../reviews/2026-09-09-issue-193-bot-review.md)。CLI部分成功单错误JSON回归通过，remote退出码恢复12项通过，Windows脚本相关60 passed/10 native skipped。
- 9f7c4ff的CI文件因shell位置不能引用runner表达式而未启动（GitHub明确annotation）；已改为互斥if的平台步骤和字面量powershell，无Actions升级或验收放宽。首个head的三OS race最终均通过；新head仍需重新跑原生unit及所有门禁。
