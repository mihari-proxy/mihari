# Unix Base Dir Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. 此处不授予自动提交、推送或合并权限。

**Goal:** 在 Linux/macOS 落地机器级数据与控制入口、用户级 TUI 诊断，以及安全可恢复的安装迁移，保持 Windows 和非 root 显式便携实例兼容。

**Architecture:** 先构建纯布局解析、目录能力和有限协议，再实现 root 输入策略、provider 生命周期与安装恢复。最后一次性接入系统模式入口；不发布只有共享 socket 却缺少 root 输入防护的中间状态。业务写仍归 daemon/Manager，installer 只在停机事务内管理迁移和安装资源。

**Tech Stack:** Go 1.26.0 / toolchain go1.26.5，现有 x/sys v0.46.0、yaml/v3、Cobra、Bubble Tea、本地 IPC；不新增依赖。

**Spec:** `docs/superpowers/specs/2026-09-05-unix-base-dir-design.md` R4，SHA256 `3A97A33DBB1B1CA5122F2A25F8545FD5A1EBD73B5DA5A160047749FF8A942F3E`。实施者必须同时阅读规格；以下任务不缩减规格合同。

创建日期：2026-09-05；修订日期：2026-09-06；计划修订：R3；状态：已按两轮意见修订，待 Astra 复审。代码基线 `2d00f61e720fa27f115dea52f7b4a95cc35a599f`。用户本轮授权编写和审核执行计划；尚未开始实施。R4 §13 的“未获批准不写实施计划”由本轮明确指令解除，但不据此推定已授权所有生产变更、真实服务操作或发布。

## Global Constraints

- Linux B=`/var/lib/mihari`；macOS B=`/Library/Application Support/mihari`；D=`B/data`；E/C/channel=`B/control.sock`、`B/control.token`、`B/mihari-channel`；I=`/usr/local/lib/mihari`。
- B root0711、D root0700、C/channel root0644、socket root0666；业务文件0600、核心0700、安装 binary0755。非 root 私有 P 保留0700/0600单根语义，不能别名或覆盖默认系统布局。
- Linux U=`absolute XDG_STATE_HOME/mihari` 或可信 home 下 `.local/state/mihari`；macOS U=可信 home 下 `Library/Logs/mihari`；TUI 日志与导出为 U/logs、U/logs-export。root 不信 HOME/SUDO_USER/XDG。
- 默认服务不设置 MIHARI_DATA，固定绝对 E/C/I。显式 MIHARI_DATA=P 是 P 本身，不是 P/data；NewPaths/Absolute 旧语义不变。最终 socket 字节上限 Linux107/macOS103。
- 保留 `/v1` 既有 DTO、ErrorEnvelope 和退出码2/3/4/5/9；新增可选 `machine-log-snapshot-v1`、Unix ZIP `mihari-logs-export/v2`。Windows/显式私有 P 仍本地 v1 导出。
- root 策略初始核心 v1.19.30，仅内置四个 Unix OS/arch hash 条目；未知核心、字段、MRS拒绝；Windows/非root P不套用该新策略。
- 锁顺序 B install → 私有服务P install → data → endpoint；binary-only无B/P锁。install lease只由外层取得一次。锁文件永不unlink。
- credential仅daemon创建；客户端每请求/重连只读，不重放mutation。轮换限定停机删除再启动。
- activation前可回旧；持久化activation后只修复target；验证daemon不执行业务mutation、后台刷新或核心。
- 普通测试无公网、真实用户目录或主机服务；需要root/ACL/mount的测试仅在显式隔离CI运行。实际服务安装、真实订阅/core、用户数据迁移另需授权。
- 六目标 windows/linux/darwin × amd64/arm64，CGO_ENABLED=0；平台实现用构建标签/后缀隔离。不修改Go版本、CHANGELOG或无关代码。
- 每个行为任务先建立可编译的最小接口，再运行行为测试看到缺少目标行为造成的断言失败；按测试目标选scaffold，不一律使用拒绝stub。缺符号、编译失败、输入已被更早校验拒绝均不算Red。记录预期断言及实际失败原因；已有正确拒绝行为无需故意破坏来制造Red。完成后跑目标与相关集成测试。只有明确获授权才创建Conventional Commit，并遵守仓库DCO。

## 交付切分与依赖

三个审阅批次是同一feature分支的实施检查点，不是可随意独立发布的功能。A（T01–T05）提供可测试的内部平台/IPC组件；B（T06–T11）提供输入安全与导出；C（T12–T18）完成事务与入口。T18仅为接线检查点，T19完成跨平台验收。任何对dev的可合并PR均须T19全部必需安全job通过；不得为提前合并而开放临时环境变量绕过。

```text
T01 → T02 → T03 → T04 → T05
          └→ T06 → T07 → T08
T03 + T05 → T09 → T10 → T11
T03 → T12 → T13 → T14
T06 + T07 + T08 + T14 → T15 → T16 → T17
T05 + T08 + T11 + T17 → T18 → T19
```

同一文件的任务顺序执行；T12的纯journal模型可与快照逻辑分别开发，但合流前均重跑集成。每个任务以代码、测试和实际命令结果供独立审阅；不得仅勾选任务而缺失Red/Green证据。任务内每个测试表项按“写断言→运行→最小实现→重跑”循环，避免一次实现整个子系统才补测试。

## 文件和接口边界

表中的 Create 是计划新文件，Modify 是基线存在文件。测试文件与实现同包；集成fake在 `internal/integration/`，平台私有helper留本包。不要把新增业务逻辑塞入cmd。

| 单元 | 生产文件 | 职责/下游 |
| --- | --- | --- |
| 纯布局 | Create platform/layout.go、layout_linux.go、layout_darwin.go、layout_windows.go | T01值解析；T18装配 |
| Unix能力 | Create platform/trustedroot_unix.go、trustedroot_linux.go、trustedroot_darwin.go、ownedlease_unix.go；Modify privatefs.go/privatefs_unix.go | fd、ACL、mount、身份、锁；禁止把B传给日志 |
| IPC | Modify control/transport/unix.go、credential/credential.go、client/client.go、client/runtime.go；Create control/credential/provider_unix.go | 同端点互斥、peer与逐请求认证 |
| root配置 | Create subscription/rootpolicy.go、rootpolicy_registry.go、rootpolicy_resources.go；Modify document.go/generator.go | typed输出、可信资源图 |
| 核心可信性 | Create core/provenance.go、supported_policy.json；Modify core/install.go、supervisor/command.go | 制品固定策略、环境、receipt |
| provider | Create subscription/provider.go、provider_journal.go；Create runtime/provider.go；Modify runtime/manager.go、subscription.go | staging、refresh、coordinator、恢复 |
| 快照导出 | Create protocol/logging_snapshot.go、logging/machine_snapshot.go、logging/assemble.go、server/logging_snapshot.go、client/logging_snapshot.go；Modify既有export/ZIP | exact-byte wire与组合 |
| 安装journal | Create app/install_journal.go、install_transaction.go、install_recovery.go、install_validation.go、install_migration.go | 用例、WAL、激活、迁移 |
| 服务适配 | Create service/definition.go、definition_linux.go、definition_darwin.go | 平台管理器动作；无业务事务 |
| 装配/脚本 | Modify cmd/mihari/main.go、cli/service.go、update/self.go、tui/run.go、scripts/install/*.sh | 统一入口、兼容分派 |

以下是跨任务接口合同（方法体在对应任务实现，不是生产补丁）。包名省略处均为所在包；标准库context、io、time、encoding/json以及现有项目包按实际使用import。未导出状态由该包持有，不允许通过调用者填充fd、owner或journal身份伪造能力。

```go
// platform/layout.go (T01)
type LayoutMode string
const (SystemMode LayoutMode = "system"; PrivateMode LayoutMode = "private"; WindowsMode LayoutMode = "windows")
type LayoutInput struct { CWD, Data, Endpoint, Credential, InstallRoot, Home, XDGState string; EUID uint32 }
type LayoutDefaults struct { OS, BaseDir, InstallRoot, TrustedHome string; SocketLimit int }
type ResolvedLayout struct { Mode LayoutMode; BaseDir string; Data Paths; ControlEndpoint, CredentialPath, ChannelPath, InstallRoot string; ClientLogs Paths }
func ResolveLayout(LayoutInput, LayoutDefaults) (ResolvedLayout, error)
// platform/trustedroot_unix.go (T02/T03); opaques hold fd and identity privately.
type TrustedRoot struct { /* unexported fd/identity/closed state */ }
type RootPolicy struct { Owner uint32; Mode uint32; AllowCreate bool }
func OpenTrustedRoot(context.Context, string, RootPolicy) (*TrustedRoot, error)
func (r *TrustedRoot) Close() error
func NewPrivateFSFromRoot(*TrustedRoot) (*PrivateFS, error) // success moves ownership
type OwnedInstallLease struct { /* unexported locks, ownership */ }
func AcquireInstallLease(context.Context, ResolvedLayout) (*OwnedInstallLease, error)
func (l *OwnedInstallLease) Close() error
type OwnedDaemonLease struct { /* private data/endpoint flock handles */ }
func AcquireDaemonLease(context.Context, ResolvedLayout) (*OwnedDaemonLease, error)
func (l *OwnedDaemonLease) Close() error
// platform/control_locator.go: common pure values; Unix constructors enforce owner.
type ControlLocator struct { Mode LayoutMode; Endpoint, Credential string; ExpectedOwner uint32 }
func (l ResolvedLayout) Locator(euid uint32) (ControlLocator, error)
// control/client (T05); no cache fallback and no automatic replay.
type CredentialProvider interface { Load(context.Context) (string, error) }
func WithCredentialProvider(locator platform.ControlLocator, provider CredentialProvider) *Client
// transport/listen_owned_unix.go and dial_peer_unix.go (T04)
func ListenOwned(ctx context.Context, layout platform.ResolvedLayout, lease *platform.OwnedDaemonLease) (net.Listener, error)
func DialVerified(ctx context.Context, locator platform.ControlLocator) (net.Conn, error)
// subscription (T06): candidate bytes are generated, never original YAML passthrough.
type PolicyInput struct { YAML []byte; SubscriptionID string; Generation uint64; CoreTag, OS, Arch string; Settings config.Settings; Resources map[string][]byte }
type PolicyOutput struct { YAML []byte; Providers []ProviderSpec }
type ProviderSpec struct { SubscriptionID string; Generation uint64; Kind, Name, Format, Behavior, URL, ResourceID string; Interval time.Duration; Inline []byte }
type RootConfigPolicy struct { /* immutable typed registry */ }
func NewRootConfigPolicy() *RootConfigPolicy
func (p *RootConfigPolicy) Build(context.Context, PolicyInput) (PolicyOutput, error)
// logging (T09/T10): source is fixed enum, payload remains raw through wire validation.
type SnapshotWindow struct { From *time.Time; To time.Time }
type SourceID string
const (DaemonSource SourceID = "daemon"; MihomoSource SourceID = "mihomo"; TUISource SourceID = "tui")
type SourceStats struct { Source SourceID; Lines, SkippedInvalid, Redacted, Bytes int64; Files []string; SHA256 string }
type SourceReader interface {
    Next(context.Context) (payload []byte, redacted bool, err error)
    Finish(context.Context) (SourceStats, error) // only after Next EOF; repeated Finish returns same result
    Close() error
}
type SnapshotSource interface { Open(context.Context, SnapshotWindow) (SourceReader, error) }
type NamedSource struct { ID SourceID; Source SnapshotSource }
type MachineSnapshotSource interface { Open(context.Context, SnapshotWindow) (SnapshotSet, error) }
type SnapshotSet interface {
    Source(SourceID) (SourceReader, error)
    Finish(context.Context) error // validates complete and normal EOF; required before ZIP publication
    Close() error
}
func Assemble(ctx context.Context, request ExportRequest, scope string, sources []NamedSource, finish func(context.Context) error) (ExportResult, error)
// app (T12–T17): InstallRequest/Result are the concrete JSON schema in R4 §9;
// Journal/Action are the concrete schema in §10, generated structs with strict decoding.
type InstallTransaction struct { /* injected service, root FS, verifier, clock, journal */ }
func (x *InstallTransaction) Apply(context.Context, InstallRequest) (InstallResult, error)
func (x *InstallTransaction) ApplyLocked(context.Context, *platform.OwnedInstallLease, InstallRequest) (InstallResult, error)
func (x *InstallTransaction) RecoverLocked(context.Context, *platform.OwnedInstallLease) error
func (x *InstallTransaction) StartLocked(context.Context, *platform.OwnedInstallLease) error
```

`InstallRequest/Result/Journal/Action`的完整字段、枚举、权限、上限直接按R4 §9–10定义，T12将其固化为JSON fixture并测试重复/未知字段。`TrustedRoot`等注释表示明确禁止公开内部表示，而不是允许省略方法或安全步骤。测试构造真实临时句柄，不在产品增加“跳过安全”布尔参数。

**接口约束：** T01的Locator在SystemMode强制ExpectedOwner=0，PrivateMode强制当前euid；只有显式root管理器验证P后可构造root P locator，不能从socket返回值猜owner。WithCredentialProvider所有HTTP连接和WS握手使用同一DialVerified；连接复用仍每请求Load，断开后新连接再次验证peer。Windows继续New/NewHTTP，不调用Unix构造器。daemon/validation在任何数据/log打开之前AcquireDaemonLease一次（data→endpoint），ListenOwned只借用并核验layout匹配、不重复flock。daemon先关worker/log/listener/FS最后释放lease；installer按T16先释放自己的data/E lease再启动子进程。SourceReader.Finish只有成功读至该源EOF才返回可信统计，提前Close不能被当作成功；SnapshotSet按daemon→mihomo顺序消费，禁止并行Source或请求tui。机器adapter最后的Finish验证complete/EOF，assembler先调用所有source Finish再调用集合finish，之后才发布ZIP；任一失败全体Close并清spool。

**构建边界（必须随T01–T18逐步保持）：**

| 文件组 | 编译平台与适配 |
| --- | --- |
| layout.go、control_locator.go、rootpolicy、protocol/logging_snapshot、logging/assemble、app/install_request.go | common，只含跨平台类型，不引用Unix fd/lease |
| trustedroot_unix.go、ownedlease_unix.go及PrivateFS接管新文件privatefs_capability_unix.go | 明确 `//go:build linux || darwin`；NewPrivateFSFromRoot放新文件，不能放common privatefs.go里引用Unix类型 |
| client/provider_client_unix.go、transport/listen_owned_unix.go、dial_peer_unix.go、credential/provider_unix.go | linux或darwin；client.go只保存common CredentialProvider及注入Dial函数，Windows无需实现Unixpeer |
| app/install_transaction.go、install_journal.go、install_recovery.go、install_validation.go、install_migration.go、install_artifact.go | 明确linux或darwin标签，只有Unix装配引用；对应Unix专用tests同标签 |
| service/definition.go | 定义common只读值和接口；OS实现后缀linux/darwin；Windows原Controller接口不变 |
| core/provenance.go/局部WAL | common模型+注入持久化接口；实际可信FS适配在provenance_unix.go，Windows不启用策略 |
| cmd/mihari/main.go、app/runtime.go、cli/service.go、cli/self.go、tui/run.go | common只依赖函数/接口；分别Create unix_layout.go/windows_layout.go于cmd，install_dispatch_unix.go/install_dispatch_windows.go于app；cli依赖注入ServiceApply回调，Windows不给apply注册入口 |

这里的两个unix dispatch文件同样显式linux/darwin标签；不扩展本任务支持到其他Unix OS。每个任务Green后执行 `go test ./... -run '^$'` 检查本机全仓编译，并执行T19六目标CGO0 build；这些是阶段接线检查，不能等最终切换才发现Windows未定义类型。修改common公共类型时再在三个OS编译test package（CI原生运行），单纯go build不证明测试可编译。

### T01：无IO布局解析与兼容值

**Files:** Create `internal/platform/layout*.go`（上表四文件）、`internal/platform/layout_test.go`；Modify `internal/platform/paths_test.go`。保留paths.go现有入口到T18。
**Interfaces:** 产出上表LayoutInput/Defaults/ResolvedLayout；消费现有NewPaths，所有平台默认值由对应后缀函数提供。

- [ ] 写表驱动测试：root/普通默认、HOME/SUDO/XDG_RUNTIME差异、绝对/相对P/E/C/I、空覆盖、P与B/D重叠、NUL和最终socket字节长度、Windows原值、NewPaths(P).Absolute不变；至少一个断言如下。
```go
func TestResolveLayout_SystemIgnoresHome(t *testing.T) {
    got, err := ResolveLayout(LayoutInput{CWD:"/", Home:"/home/a", EUID:1000}, LayoutDefaults{OS:"linux", BaseDir:"/var/lib/mihari", InstallRoot:"/usr/local/lib/mihari", TrustedHome:"/home/a", SocketLimit:107})
    if err != nil { t.Fatal(err) }
    if got.Data.Root != "/var/lib/mihari/data" || got.CredentialPath != "/var/lib/mihari/control.token" { t.Fatalf("wrong machine layout: %+v", got) }
}
```
- [ ] Run `go test ./internal/platform -run 'TestResolveLayout|TestPaths' -count=1`；记录行为Red，Windows上避免用宿主filepath假装Unix路径解析。
- [ ] 实现按目标OS的纯路径解析，cwd入口捕获一次；可信home值这里只作候选，不代表已验证权限；T02验证后才可IO。
- [ ] 重跑同命令及 `go test ./internal/platform`；确认不调用Mkdir/Stat/UserHomeDir，不修改默认生产行为。

### T02：可信根、ACL与mount能力

**Files:** Create `internal/platform/trustedroot_unix.go`、`trustedroot_linux.go`、`trustedroot_darwin.go`和对应`_test.go`；Create `trustedroot_model_test.go`。消费T01；产出TrustedRoot/OpenTrustedRoot。

- [ ] 先写fd操作fake的拒绝表与本机临时目录测试：每个路径组件替换、链接/硬链接、foreign owner、ACL写权限、inherit-only/default ACL、未知FS、bind mount、Darwin三种OS别名；creation parent和祖先遍历必须分别断言。
```go
// Tagged security test; fixture anchor is provided by T19, never arbitrary /tmp.
func TestTrustedRoot_RejectsOnlyChangedSymlink(t *testing.T) {
    parent := os.Getenv("MIHARI_SECURITY_ROOT")
    if parent == "" { t.Fatal("security runner required") }
    target := filepath.Join(parent, "rootpolicy-positive")
    if err := os.Mkdir(target, 0700); err != nil { t.Fatal(err) }
    t.Cleanup(func(){ if err := os.Remove(target); err != nil { t.Error(err) } })
    policy := RootPolicy{Owner:0, Mode:0700}
    root, err := OpenTrustedRoot(context.Background(), target, policy)
    if err != nil { t.Fatalf("positive control failed: %v", err) }
    if err := root.Close(); err != nil { t.Fatal(err) }
    link := filepath.Join(parent, "rootpolicy-link")
    if err := os.Symlink(target, link); err != nil { t.Fatal(err) }
    t.Cleanup(func(){ if err := os.Remove(link); err != nil { t.Error(err) } })
    got, err := OpenTrustedRoot(context.Background(), link, policy)
    if got != nil { got.Close(); t.Fatal("accepted application symlink") }
    if !errors.Is(err, ErrUnsafeComponent) { t.Fatalf("wrong rejection stage: %v", err) }
    info, statErr := os.Stat(target)
    if statErr != nil || info.Mode().Perm()!=0700 { t.Fatal("target modified") }
}
```
Create platform/errors.go中的common哨兵ErrUnsafeComponent（API映射permission_denied），只在组件类型/链接检查处包装；祖先/ACL/FS错误不能包装成它。T02第一步用注入fd backend建立成功open及“只最终symlink变化”的模型测试；拒绝scaffold应在正常对照失败，不算攻击测试已证明有效。上面真实OS测试放 `trustedroot_security_unix_test.go`，标签 `unix_security && (linux || darwin)`，T19 runner覆盖platform包。
- [ ] Run `go test ./internal/platform -run 'TestTrustedRoot|TestCreationParent' -count=1`；直接使用TrustedRoot/RootPolicy的模型测试标记linux或darwin，纯路径模型全OS运行；普通Unix临时IO只测不需特权的子能力；严格系统根正例和双UID用T19命令。记录正例Red断言，确认测试真正走到目标检查后再计负例覆盖。
- [ ] 按R4 §5逐段openat/nofollow；实现Linux openat2→statx→fdinfo回退，Darwin ACL fd ABI/FS identity；创建前无继承ACL，0600写后sync再公开。fd identity验证后操作，禁止再按原路径打开。
- [ ] 对临时树实际验证mode与ACL，未知能力fail closed；双UID/mount测试在T19隔离环境验收，未执行不得记PASS。系统祖先不chmod/chown；U失败返回诊断不可用，不改机器路径。

### T03：PrivateFS所有权与锁域

**Files:** Modify `internal/platform/privatefs_unix.go`；Create `privatefs_capability_unix.go`、`ownedlease_unix.go`、`ownedlease_unix_test.go`、`privatefs_capability_unix_test.go`。消费TrustedRoot；产出NewPrivateFSFromRoot/OwnedInstallLease，不向common privatefs.go加入Unix类型引用。

- [ ] 测试转移前失败仍调用者拥有、成功后由PrivateFS关闭一次，B0711不被改0700；固定锁不unlink，同D异E、异D同E、锁名字替换失败。
```go
// In privatefs_capability_unix_test.go; root来自T02合法fd backend fixture；
// 实际系统能力测试使用T19可信anchor，不能放宽祖先策略来打开任意t.TempDir。
fs, err := NewPrivateFSFromRoot(root)
if err != nil { t.Fatal(err) }
if err := fs.Close(); err != nil { t.Fatal(err) }
if err := fs.Close(); err != nil { t.Fatal(err) }
```
- [ ] Run `go test ./internal/platform -run 'TestPrivateFSCapability|TestOwnedLease' -count=1`；helper需检查底层fd关闭计数，以上Close调用本身不代替断言。
- [ ] 实现B服务锁/P锁/data锁/endpoint锁独立对象，EX|NB，引用转移和幂等Close；borrowed lease不可自行释放。binary-only另用binary父目录锁，不调用AcquireInstallLease。
- [ ] subprocess验证冲突和进程退出释放；测试失败清理子进程；Windows现有PrivateFS与锁测试回归。

### T04：Unix socket安全生命周期

**Files:** Modify `internal/control/transport/unix.go`；Create `unix_peer_linux.go`、`unix_peer_darwin.go`、`unix_owned_test.go`（同transport包）；Modify `transport_test.go`。消费T03 data/endpoint锁；产出拥有listener与安全Dial接口，保持Windows现有导出签名适配。

- [ ] listener持锁后探测旧socket，500ms连接成功/超时/拒绝权限均不能删；只有ECONNREFUSED且inode复核才删；绑定后shutdown不能删后来替换的socket。
```go
// Table in unix_owned_test.go; probe is an injected transport-local function.
cases := []struct { name string; err error; removable bool }{
    {"live", nil, false}, {"refused", syscall.ECONNREFUSED, true},
    {"denied", syscall.EACCES, false}, {"timeout", context.DeadlineExceeded, false},
}
```
- [ ] Run `go test ./internal/control/transport -count=1`；实际socket测试验证0666系统/0600私有及peer验证先于发送token。
- [ ] 新ListenOwned实现受控清理并关闭标准自动unlink；Linux SO_PEERCRED、Darwin LOCAL_PEERCRED验证期望owner。旧Listen/DialContext保留兼容入口到T18，当前daemon/run.go在切换前仍走旧装配；不能把缺layout的旧调用直接绑到新共享模式。默认共享与私有endpoint碰撞由布局/锁拒绝。
- [ ] 双进程测试保留旧无锁listener；相同E不同D争同锁；初始绑定失败释放资源一次。

### T05：只读credential与全客户端错误边界

**Files:** Create `internal/control/credential/provider_unix.go`、`provider_unix_test.go`；Modify `credential.go`、`internal/control/client/client.go`、`runtime.go`、`webgui.go`及相应tests；Modify `internal/tui/session/client.go`、`session.go`、`internal/cli/status.go`。
**Interfaces:** CredentialProvider.Load；WithCredentialProvider(locator, provider)，Unix构造放provider_client_unix.go；已有New/NewHTTP/SetToken保持Windows固定token行为。T04的两个新transport文件与T01的control_locator.go均列为Create。

- [ ] 用httptest和调用计数provider测Status、普通REST、WebSocket每次调用读取一次；缺失→出现、读取错误不回旧缓存、流重连读新值；mutation一次失败不自动重试。
```go
type sequenceProvider struct { calls int; value string; err error }
func (p *sequenceProvider) Load(context.Context) (string, error) { p.calls++; return p.value, p.err }
```
- [ ] Run `go test ./internal/control/client ./internal/control/credential ./internal/tui/session ./internal/cli -count=1`；断言missing=3、denied/peer=5、malformed=9、badpath=2、busy=4，不能被status/session重包成3。
- [ ] credential读regular/nlink/owner/mode/64hex+可选LF；daemon独有create路径。provider每请求载入，成功token同时加入进程redactor历史集合；外部在线改写只报permission_denied并提示服务重启。
- [ ] 停机删C再启动集成测试确认长驻TUI恢复；只读客户端不EnsureDirs、不创建token/settings。生产装配待T18。

### T06：完整root typed字段注册与资源图

**Files:** Create `internal/subscription/rootpolicy.go`、`rootpolicy_registry.go`、`rootpolicy_resources.go`、`rootpolicy_test.go`、`testdata/rootpolicy/registry.json`和每字段正负fixture；Modify `document.go`、`generator.go`仅增加可注入策略。
**Interfaces:** PolicyInput/PolicyOutput/ProviderSpec/RootConfigPolicy.Build，输出YAML由typed值生成；字段注册表不可从下载内容加载。

- [ ] 首先对固定v1.19.30源码与现有document/generator列完整字段路径、类型、语义、允许/拒绝原因、资源映射、fixture文件的registry。使用当前文档技能/ctx7与官方固定tag源码，记录引用到 `docs/architecture/root-config-policy-v1.md`（Create）；不是从记忆生成支持表。所有可接收字段均须分类，不能只列危险key或放行未知嵌套map。
- [ ] 每类选一个行为Red，再逐字段循环；代表回归代码如下，资源file/inline/http、证书、DNS/TUN、代理各类型和嵌套字段分别覆盖。
```go
func TestRootPolicy_RejectsControllerUnix(t *testing.T) {
    valid := "proxies: []\nproxy-groups: []\nrules: ['MATCH,DIRECT']\n"
    // Registry fixture must first prove this complete baseline accepted by both parsers.
    if _, err := ParseDocument([]byte(valid)); err != nil { t.Fatal(err) }
    settings := config.Defaults()
    settings.ControllerSecret = strings.Repeat("a", 64) // isolated fixture value
    input := PolicyInput{SubscriptionID:"00000000000000000000000000000001", Generation:1, CoreTag:"v1.19.30", OS:"linux", Arch:"amd64", Settings:settings, YAML:[]byte(valid)}
    policy := NewRootConfigPolicy()
    if _, err := policy.Build(context.Background(), input); err != nil { t.Fatalf("positive control: %v", err) }
    input.YAML = []byte(valid + "external-controller-unix: /etc/cron.d/secret-value\n")
    _, err := policy.Build(context.Background(), input)
    var failure PolicyError
    if !errors.As(err, &failure) || failure.Field != "external-controller-unix" || failure.Code != protocol.CodeDataFailure { t.Fatalf("wrong failure: %v", err) }
    if strings.Contains(err.Error(), "secret-value") { t.Fatal("sensitive value leaked") }
}
```
T06定义 `type PolicyError struct { Field string; Code protocol.ErrorCode }`，实现Error仅输出安全字段路径，边界转APIError/data_failure。固定tag registry同时收录空代理列表和单字符串MATCH,DIRECT规则的语义证据。正例显式提供合法settings/测试secret，使全拒绝scaffold失败；攻击负例只改一个字段，不能以ParseDocument或settings先报错冒充RootConfigPolicy覆盖。
- [ ] Run `go test ./internal/subscription -run 'TestRootPolicy' -count=1`；registry测试要求每条注册规则有有效正例和边界负例，路径错误不含字段值/URL/token。未注册tag→invalid_state，数据不能安全处理→data_failure。
- [ ] 实现完整typed生成器；loopback、固定core-home、文件资源ID映射；拒绝危险/未知字段和MRS。`Resources`仅由已验证私有候选构造，不能直接传任意本机路径。普通P/Windows原逻辑保留。
- [ ] 独立安全审阅注册表及每条fixture后才进入T08/T18；缺少上游语义证据必须停止该任务，不能删掉必需兼容功能或加入unknown放行开关。

### T07：核心制品、provenance与受管进程环境

**Files:** Create `internal/core/provenance.go`、`provenance_unix.go`、`provenance_pair.go`、`provenance_pair_test.go`、`verified_command.go`、`supported_policy.json`、`provenance_test.go`；Modify `core/install.go`、`core/command.go`、`core/config.go`、`core/version.go`、`internal/app/runtime.go`、`internal/supervisor/command.go`及测试。消费T02、T06；产出root核心验证器，receipt schema见R4 §7。

**Pair WAL合同：** 属于core包，在可信D内 `staging/core/provenance-commit.json`，root0600；这是R4 §7“receipt+binary同一写前journal”的具体内部表示，不复用provider journal或app安装journal。schema=`mihari.core-provenance-commit/v1`，字段tx_id、phase=prepared|publishing|committed、old_present、固定binary/receipt角色的old/new hash、私有backup/candidate相对引用、两次替换各自intent/done、boot/object identity。备份在D/staging/core/tx_id/0700。journal以T02 fsync/rename能力保存；先所有候选/备份durable，再binary intent→replace→done，receipt intent→replace→done，二者重hash后写committed。committed前崩溃恢复整个old pair（绿装old_present=false则仅隔离已确认的新pair），之后只保留new pair。每次恢复也WAL，未知身份拒绝；cleanup不得删未收敛journal及备份。

**所有权/接口：** 现有PreparedCore的Version/Updated/Commit/Cleanup签名不变，root Candidate内部持有pair候选与store，Manager在现有维护/mutation协调范围内Commit；下载与hash在锁外。core提供 `RecoverProvenance(ctx context.Context, store ProvenanceStore) error`，由daemon在BuildRuntimeWithOptions前、任何-v/-t/Start前调用，执行时无核心进程；在线Commit失败先恢复pair再允许旧核心重启，恢复失败不执行任一版本。ProvenanceStore为core消费方接口，方法Load/Save/Apply/Inspect/Sync只接固定角色而不是任意路径；T02平台适配实现，core绝不依赖app。

```go
// core/verified_command.go: concrete capabilities have no exported identity setters.
type CoreCommand struct { Binary, Home, Config string; Args, Env []string }
type CorePurpose uint8
const (CoreVersion CorePurpose = iota; CoreValidate; CoreRun)
type VerifiedCore struct { /* private InstalledPair or TrustedCandidate identity/store */ }
type ConfigCapability struct { /* private held fd, root/role/identity/content hash */ }
func (c *VerifiedCore) Command(context.Context, CorePurpose, *ConfigCapability) (CoreCommand, error)
func (c *VerifiedCore) Close() error
func (c *ConfigCapability) Close() error
// Unix factory receives capabilities, never treats a caller's hash as a trust root.
func OpenInstalledCore(context.Context, ProvenanceStore) (*VerifiedCore, error)
func (c *Candidate) Verified(context.Context) (*VerifiedCore, error)
func BindGeneratedConfig(ctx context.Context, data *platform.TrustedRoot, relative string, expectedSHA256 [32]byte) (*ConfigCapability, error)
```

`BindGeneratedConfig`及Unix工厂放verified_command_unix.go（Create），common VerifiedCore内部用不含Unix类型的私有backend接口；Windows原runner仍可编译。配置内容只有T06策略输出能进root路径，expectedSHA256由Manager在生成时从该输出计算，不从API声明读取；工厂用held fd验证root owner/type/nlink/ACL、角色与实际hash。允许两种明确配置角色：D/staging下本事务生成的随机config候选，D/runtime/config.yaml已提交配置；拒绝其他D文件/任意路径。`-d`始终D/runtime/core-home，`-f`使用此能力绑定的实际候选或已提交路径，不把Config忽略或固定为另一个文件。provider/GeoIP等资源仍只位于core-home。不存在额外配置镜像或新的settings schema。

**两种核心信任来源：** OpenInstalledCore要求先RecoverProvenance再验证已发布binary+receipt，能力为InstalledPair。Installer.Prepare对压缩asset执行内置表hash验证、私有解压、binary hash、生成私有candidate receipt后构造Candidate，Candidate.Verified返回TrustedCandidate；它不依赖已安装pair，所以绿装可执行该候选-v/-t。来自下载的digest/版本输出不能替代内置asset信任；旧用户树candidate/receipt拒绝。TrustedCandidate只能version/validate，CoreRun必须InstalledPair。Candidate.Commit以自身已校验的binary+receipt做pair WAL，成功后旧candidate执行能力失效，正式Run重新OpenInstalledCore；Cleanup仅在不被journal引用时删候选，方法幂等。

CoreVersion要求config=nil并生成唯一-v；CoreValidate要求ConfigCapability并生成-t/-d/-f；CoreRun同样要求已提交config能力及InstalledPair。每次Command重新核验核心/配置身份、hash与类型后生成完整Args/Env，不接受额外调用者参数；OS执行适配器仅消费该命令（exec.Cmd.Env显式allowlist），stdout/退出分类沿用既有契约。配置与binary父目录持续root不可被普通UID写，生命周期锁阻止受管自身替换；未知身份拒绝。

**具体接线：** root Installer.Prepare不再通过裸CommandRunner.Run(candidatePath,args)，而以Candidate.Verified生成候选命令；`BuildRuntimeWithOptions`与localReadyVersion仅OpenInstalledCore做-v；runtime.prepareContent保留本次生成的content hash，借用D capability调用BindGeneratedConfig校验这个随机候选，用InstalledPair（或安装新核心时TrustedCandidate）生成-t命令。commitRuntimeConfig在mutation内重查revision/候选hash后发布相同content，重新绑定已提交ConfigCapability，再reload固定runtime路径；失败恢复旧content/hash并重新绑定旧配置。supervisor.CommandStarter新增注入factory取InstalledPair+已提交配置能力生成CoreRun。root路径彻底不走允许任意name/args的旧Runner，Windows/非root P保留原Runner。控制参数仍由调用方的现有类型adapter转换，不将旧candidatePath静默换成当前binary。

- [ ] 从既有 `scripts/release/release-inputs.lock.json` 抽取四个Unix条目成为编译内置表，并用一致性测试逐项比较OS/arch/tag/asset hash；不改release lock、不调用远端latest更新它。
```go
// Table drives provenance_test.go, each case asserts no child Start on rejection.
cases := []string{"missing-receipt", "binary-hash-mismatch", "unknown-tag", "alpha-no-entry", "asset-hash-mismatch", "user-owned-receipt"}
```
- [ ] Run `go test ./internal/core ./internal/supervisor ./internal/app -count=1`；对两次pair替换各intent/effect/done前后、committed写前后重建store并恢复两次；探测拒绝时runner调用数为0。验证器与正式核心均使用相同可信binary、固定-d/-f以及allowlist环境；LD_*/DYLD_*/SAFE_PATHS注入无效。
- [ ] 新增 `TestTrustedCandidate_GreenInstallWithoutInstalledPair`、`TestTrustedCandidate_UpdateExecutesCandidateNotOld`、`TestTrustedCandidate_CannotRunBeforeCommit`、`TestConfigCapability_ValidatesSelectedCandidate`、`TestConfigCapability_ChangedBytesRejected`；fake OS executor记录binary/config实际hash、Args/Env。正常候选-v/-t调用各一次，未提交Run零次；两个不同配置候选不可互换，发布字节hash必须等于已通过-t的候选。Run `go test ./internal/core ./internal/runtime ./internal/app ./internal/supervisor -count=1`覆盖全部实际接线。
- [ ] 升级负例让旧binary的-v/-t必成功、新candidate的-t必失败，断言Prepare失败、旧pair hash不变、Commit未调用；候选文件被替换、Close后或hash变化时executor零调用，Commit也拒绝。该回归不能只看最后Version字符串与预期相同。
- [ ] binary+receipt写前journal原子协调，崩溃恢复保持旧有效pair；启动重新hash匹配内置表；包括app.BuildRuntimeWithOptions中的DetectVersion、core.localReadyVersion/DetectVersion的-v探测，任何核心执行之前都验证，不只保护-t或正式Run。新安装缺资源setup_required合法，已有活跃核心更新失败必须继续旧核心。
- [ ] 测试旧树receipt不能提升信任、四平台asset离线fixture与Windows旧行为；实际mihomo执行不属于默认测试。

### T08：provider事务与全部mutation入口

**Files:** Create `internal/subscription/provider.go`、`provider_journal.go`、对应tests；Create `internal/runtime/provider.go`、`provider_test.go`；Modify `runtime/manager.go`、`subscription.go`、`settings.go`、`tun.go`、`geoip.go`、`panel.go`，`internal/web/classify_test.go`和 `control/server/runtime.go`相关tests。
**Interfaces:** `Manager.RefreshProvider(ctx context.Context, operation Operation, name string) error` 为内部用例；既有UpdateRuleProvider保留签名/operation/IfRevision并直接委派，不重建operation、不嵌套doOperation。ProviderSpec消费T06；PolicyInput的ID/generation由订阅候选快照绑定（uint64与Catalog一致），Build将其原样绑定到所有输出provider，提交重验而不重新补写身份；文件名对ID/generation/kind/name无歧义编码后SHA256。

- [ ] 测试在下载阻塞期间另一mutation仍能执行；释放下载后active/generation/hash变化必须revision_conflict；正常、reload失败回旧、双失败degraded且停止core；每个provider journal intent/done崩溃点重启恢复旧资源。
```go
// provider_test.go uses channel-controlled downloader; no fixed Sleep.
started, release := make(chan struct{}), make(chan struct{})
download := func(ctx context.Context) error { close(started); select { case <-release: return nil; case <-ctx.Done(): return ctx.Err() } }
// 将download注入本任务ProviderDownloader.Download(ctx)依赖；测试收到started后切换generation，
// close(release)放行，再等待结果并断言revision_conflict及旧文件hash不变。
```
- [ ] Run `go test ./internal/subscription ./internal/runtime ./internal/control/server ./internal/web/... -count=1`。
- [ ] 实现256 providers、16MiB各源/256MiB合计、5跳/30秒、interval默认3600最小60、私有staging；http/file/inline生成本地type=file，核心无url/native刷新。调度由daemon ctx拥有，启动先恢复journal再构建任务。
- [ ] 把RootConfigPolicy接到新增/刷新/切换/设置/启动最后有效配置，校验和正式核心同策略；本地rule-provider API、手动和scheduler共用同一路径，禁止残留controller直接下载。当前web/classify.go未允许provider写路由，保持默认拒绝并加回归，不新增Web公开写能力。GeoIP/profile/证书/面板等仅受管资源进入core-home。
- [ ] 关闭等待worker，缺必要资源不激活、离线合法缓存继续；API operation ID/IfRevision语义回归。记录实际调用图和入口测试清单供安全审阅。
- [ ] Create `internal/integration/unix_provider_contract_test.go`（Unix标签）：用完整合法订阅candidate(ID固定32hex,generation=7)→Build→ProviderSpec检查完全相同身份→RefreshProvider(Operation{ID:"op-1",Source:"cli",IfRevision:&revision})；下载期间切换generation=8后提交必须冲突，原operation幂等结果保持。测试调用真实类型，不复制生产DTO或用string generation适配掩盖不一致。

### T09：机器快照server与exact-byte协议

**Files:** Create `internal/control/protocol/logging_snapshot.go`、`logging_snapshot_test.go`；Create `internal/logging/machine_snapshot.go`、`machine_snapshot_test.go`；Create `internal/control/server/logging_snapshot.go`、`logging_snapshot_test.go`；Modify `server.go`、`runtime/capabilities.go`；Create `internal/control/protocol/testdata/machine-snapshot-v1.jsonl`（逐字R4 fixture）。
**Interfaces:** SnapshotWindow/SourceReader；server依赖只读MachineSnapshotSource，不接路径，不占业务mutation锁。

- [ ] 严格request decode≤4KiB，拒绝重复/未知key、缺to、from>to、未来>5分钟；fixture按原payload+LF得到55bytes及R4 hash。
```go
raw := []byte("{\"time\":\"2026-09-05T00:00:00Z\",\"msg\":\"节点\",\"n\":1e0}\n")
if len(raw) != 55 { t.Fatalf("bytes=%d", len(raw)) }
if fmt.Sprintf("%x", sha256.Sum256(raw)) != "1625f1821f85ab2dc68c7da55c4fbe769637b7752174c7d5be8a83cd8d388a48" { t.Fatal("wire hash changed") }
```
- [ ] Run `go test ./internal/logging ./internal/control/protocol ./internal/control/server -count=1`。
- [ ] 实现每来源锁下fd+长度前缀，解锁读取；两源固定顺序，空source_end，truncate/IO错误失败；动态秘密合并，不向客户端发送secret集合。R4 §8所有限额：1MiB记录、2MiB帧、20文件、2GiB扫描/1GiB输出、并发2、每分钟6接纳无队列、120s总/5s写/2s锁。
- [ ] fake clock测试预算和速率、慢消费者/取消；stream开始前HTTP错误，开始后error且无complete；所有worker归server shutdown所有。capability只在最终Unix系统装配开启。

### T10：client验证与Unix ZIP v2组合

**Files:** Create `internal/control/client/logging_snapshot.go`、`logging_snapshot_test.go`；Create `internal/logging/assemble.go`、`assemble_test.go`；Modify `export.go`、`export_zip.go`、`export_json.go`和对应tests。
**Interfaces:** T09SourceReader记录和结束统计输入assembler；网络reader先验证原字节，再二次脱敏。保留旧Export签名作为Windows/私有adapter。

- [ ] 以fixture逐个变异header/source顺序、重复终帧、padding/base64/UTF8/JSON、hash/count/bytes、缺LF/EOF、complete后空白、超时；每例断言没有发布ZIP且spool删除。
```go
// logging_snapshot_test.go table input mutations; response body is io.ReadCloser.
cases := []string{"truncated", "complete-plus-newline", "wrong-hash", "wrong-count", "unexpected-source", "error-frame", "complete-without-eof"}
```
- [ ] Run `go test ./internal/control/client ./internal/logging -count=1`。
- [ ] 独立stream HTTP client：空闲10s/总125s，complete后5s正常EOF；一帧缓冲、取消关闭body。每payload原字节hash通过后再解析脱敏，redacted用服务端标记OR本地变化，不重复计数。
- [ ] manifest v2保留原字段加scope/source_status；仅非空日志entry、全空ErrNoLogLines；TUI源≤10文件/1GiB、合并spool≤2GiB。沿用PublishDir身份、0600、sync/no-replace和cleanup warning合同；Windows/私有manifest v1不变。
- [ ] Create `internal/integration/unix_snapshot_contract_test.go`（Unix标签）：注入临时layout→Locator→WithCredentialProvider→client.OpenMachineSnapshot(ctx, window)返回logging.SnapshotSet→NamedSource adapter→Assemble(..., set.Finish)。client方法定义在T10，network source借用set，集合Close关闭body；Assemble关闭每源，调用者defer set.Close。正常fixture验证manifest lines/skipped/redacted/files及scope；伪complete/提前EOF保证Publish从未调用。真实peer跨UID部分由T19同名tagged场景运行，不以httptest替代peer验收。

### T11：用户日志、导出UI与退出所有权

**Files:** Modify `internal/tui/run.go`、`logging_applier.go`、`ui/exportlogs.go`、`pages/system/model.go`、`cmd/mihari/main.go`的buildExportLogs/openTUILogging辅助装配；对应现有tests及 `testdata/full/system-logging.golden`。
**Interfaces:** 消费T01 U、T03 PrivateFS、T05 provider、T10 assembler；logging factory只取U capability，不读D或创建C。

- [ ] fake local/remote sources测在线完整导出、离线显式TUI-only、默认不降级、U不可用仅内存诊断、同窗口时间、LoggingStatus.dir仍机器目录。
```go
// In run_test.go lifecycle fixture records ordered events; assert full sequence.
want := []string{"cancel-export", "close-response", "wait-workers", "close-logging", "close-user-fs", "relaunch"}
```
- [ ] Run `go test ./internal/tui/... ./cmd/mihari -count=1`；逐初始化故障注入检查close-once。
- [ ] 显示机器日志/当前用户日志/默认导出目录；在线配置沿用session，离线debug/100MiB/10；一个时间窗口给三源。TUI-only必须用户显式选择。
- [ ] export/session/config worker取消→强制关网络→等待→logging→FS→relaunch；5秒网络终止不能丢弃仍用FS的磁盘worker。Windows单目录UI及日志初始化失败仍可控制的行为回归。此任务装配函数可测，默认入口到T18才切换。

### T12：安装请求与write-ahead journal模型

**Files:** Create `internal/app/install_request.go`、`install_journal.go`、对应tests；Create `internal/app/testdata/install/{request,journal,result}.json`。
**Interfaces:** InstallRequest/Result按R4 §9，Journal/Action按§10；严格DecodeInstallRequest(io.Reader)、DecodeJournal(io.Reader)，编码前校验；journal store消费T02能力。

- [ ] 将R4完整schema字段写入struct/fixture；64KiB请求、1MiB journal，重复/未知字段、错误schema/enum、recover附带路径、未知journal身份拒绝。
```go
func TestInstallRequest_RecoverRejectsSource(t *testing.T) {
    _, err := DecodeInstallRequest(strings.NewReader(`{"schema":"mihari.install-request/v1","operation":"recover","source":"/tmp/x"}`))
    if err == nil { t.Fatal("recover accepted caller source") }
}
```
- [ ] Run `go test ./internal/app -run 'TestInstallRequest|TestInstallJournal' -count=1`。
- [ ] root0600 journal同目录O_EXCL临时→fsync→rename→parent fsync；每外部动作intent/done，逆向恢复也记录。记录服务原字节、对象identity/hash、data_action、source/target权威、transaction标识；不把env写进日志。
- [ ] 每次durability syscall失败有断言：action未开始/已开始可辨认；phase不能代替actions。跨boot重新可信解析+hash/私有标识，不要求mount ID与上次相等；未知实际状态停止恢复。

### T13：systemd与launchd定义/停止适配

**Files:** Create `internal/service/definition.go`、`definition_linux.go`、`definition_darwin.go`及tests；Create `internal/service/testdata/definitions/`下可信unit/dropin/plist快照。
**Interfaces:** 定义值含原始文件bytes/owner/mode、binary/args/env、enabled/running、process identity。方法为R4 §9 InspectDefinition/DisableAutostartAndStop/WaitOwnedTreeExit/WriteDefinition/RestoreDefinition/Start/Probe，均context且通过注入绝对路径runner执行；每个外部动作由T14 journal wrapper包围，不在adapter内部绕过WAL批量修改。

- [ ] fake runner断言Linux实际/etc mask→reload→核验masked→stop→cgroup空；未知dropin/陌生ExecStart/多个命令拒绝。macOS持久disable→核验→bootout，不能只有bootout或使用gui域。
```go
// Table used by platform-specific adapter tests.
states := []struct{ running, enabled bool }{{false,false},{false,true},{true,false},{true,true}}
```
- [ ] Run `go test ./internal/service -count=1` 在Linux/macOS各跑；Windows现有Controller不扩展必须实现的Unix方法。
- [ ] Linux30秒后只向验证cgroup TERM5秒/KILL5秒；Darwin启动identity重查，不按裸PID杀。工具缺失/未知状态invalid_state，不降级管理器。
- [ ] launchd stopped两格发布plist恢复enabled但不bootstrap；running+disabled受控enable/bootstrap/re-disable并核验；plist存在但unloaded=stopped；install只注册不启动和旧stopped升级均覆盖。记录原始定义/enable/mask恢复，所有程序参数数组传递。

### T14：事务调度、逐动作恢复与锁借用

**Files:** Create `internal/app/install_transaction.go`、`install_recovery.go`、对应tests；消费T03、T12、T13。
**Interfaces:** Apply外层锁一次→RecoverLocked→ApplyLocked；StartLocked同lease；service stop/uninstall/reinstall都用同B锁。平台adapter只提供动作，不反调公开service入口。

- [ ] 用可重建的内存磁盘+服务fake模拟每个intent写前/写后、外部动作前/后、done前/后进程崩溃；“新进程”从持久状态恢复，不能只调用rollback捕获error。
```go
// install_recovery_test.go enumerates actual journal action kinds, not phase alone.
points := []string{"before-intent", "after-intent", "after-effect", "after-done"}
// For every action × point, reconstruct deps and recover twice; both results converge.
```
- [ ] Run `go test ./internal/app -run 'TestInstallTransaction|TestInstallRecovery|TestInstallLease' -count=1`。
- [ ] 实现prepared/stopped/data_committed/binaries_committed/definition_committed/activation_committed/complete；验证旧/候选identity后重放，旧/新以外拒绝。data_action=retain从不回滚D；create仅隔离本事务新D。source从不删除。
- [ ] activation前逆序恢复所有intent，包含仅prepared已mask情况；activation后只target。一次安装锁可借给recover/start，普通daemon不取它、不循环等待。private服务全局B+P；binary-only独立T17分支。

### T15：可信候选与保功能迁移

**Files:** Create `internal/app/install_migration.go`、`install_artifact.go`、对应tests；Modify `internal/core/install.go`、`internal/panel/install.go`、`internal/geoip/service.go`仅复用候选验证；Create `internal/integration/unix_migration_test.go`。
**Interfaces:** migration准备结果由app私有类型持有source/target capability、已验证候选、资源hash和cleanup；只传给ApplyLocked，调用方不能伪造trusted。

- [ ] fixture包含活动订阅cache、http/file/inline provider、geoip/panel、logging/settings、旧secret和日志；验证迁移后活动引用保留、runtime重生成、secret不复制、旧树逐文件hash不变。
```go
// Migration negative cases; every failure asserts old service definition/data unchanged.
cases := []string{"missing-active-cache", "missing-provider-resource", "untrusted-core", "unknown-top-level", "nested-source-target", "concurrent-business-write", "oversize", "hardlink", "nested-mount"}
```
- [ ] Run `go test ./internal/app ./internal/integration -run 'TestInstallArtifact|TestUnixMigration' -count=1`。
- [ ] 制品在停机前准备：root从no-follow fd复制到私有staging，再独立官方TLS固定tag checksum或root只读离线清单核对，用户request hash不是信任根。binary256MiB、bundle压缩1GiB/展开2GiB/10000文件，拒绝链接/设备/重复/穿越；提权后不执行用户staging。
- [ ] 按R4 §11完整白名单及每文件限制，总10000/2GiB/depth16、业务候选256MiB；fd复制前后stat/hash/集合复核、私有候选跨文件typed语义验证。忽略logs等目录且不遍历；必要资源缺失整体失败而非静默setup_required；绿装才可无资源。
- [ ] 服务定义固定source或无服务显式source；未知目标D拒绝，完成journal不重复导入；受管写者停机确认，恶意owner变化拒绝/语义隔离，不声称捕获所有并发写。不杀不明手工daemon，恢复旧服务。

### T16：无业务验证子进程、activation与前台bootstrap

**Files:** Create `internal/app/install_validation.go`、`install_validation_test.go`、`internal/daemon/activation.go`、`activation_test.go`；Modify `internal/daemon/run.go`、`cmd/mihari/main.go`装配、`internal/runtime/manager.go`业务gate；Create `internal/integration/unix_activation_test.go`。
**Interfaces:** validation私有匿名双向pipe租约与ready.json；installer持install锁，释放data/E后启动子进程；子进程只data→E，不能recover。

- [ ] fake child/harness验证root+父启动identity+nonce hash+候选binary+transaction匹配；缺pipe/伪造ID/EOF拒绝；ready不能由普通status替代。
```go
// activation_test.go table: whether business mutation may run.
cases := []struct { phase string; allowed bool }{
    {"prepared",false}, {"definition_committed",false},
    {"activation_committed",true}, {"complete",true},
}
```
- [ ] Run `go test ./internal/app ./internal/daemon ./internal/integration -run 'TestInstallValidation|TestActivation|TestUnixBootstrap' -count=1`。
- [ ] 审计cmd/main.go的collectBaseLogSecrets/collectCatalogLogSecrets、EnsureRuntimeConfig、settings sidecar、onboarding和catalog初始化，验证分支改为只读收集已存在secret与业务对象，禁止日志装配LoadOrCreate偷偷写web credential；验证模式仅布局/settings/catalog/log/IPC/token，不启动核心、不后台写、不接收外部mutation；合法setup_required可ready。收到ready后先停child等锁释放，再durable activation，最后正式服务按running/enabled恢复。
- [ ] 正常daemon pending仅prepared到definition_committed，返回需recover；activation/complete可启动target。默认root绿装先install锁检查旧source/journal再创建D；root P非service用P journal/锁；非root P保持旧初始化。install只注册/旧stopped也验证后激活但不正式启动。
- [ ] 启动失败后的target权威、验证parent死亡、跨boot stale PID、初始化每步close-once集成覆盖；禁止用户可控bypass env。

### T17：统一service apply、自更新和shell安装

**Files:** Modify `internal/cli/service.go`、`internal/cli/self.go`、`internal/service/service.go`、`internal/app/self_update.go`、`internal/update/self.go`、`cmd/mihari/main.go`、`internal/tui/actions.go`、`internal/tui/pages/system/model.go`、`internal/tui/run.go`；Create `internal/update/prepare.go`、`prepare_test.go`；Modify `scripts/install/install.sh`、`install-aio.sh`、`install-aio-remote.sh`、`test_install_channel.py`；Create `internal/integration/unix_install_entrypoints_test.go`。
**Interfaces:** `SelfUpdater.Prepare(ctx context.Context, binaryPath, currentVersion, channel string) (PreparedUpdate, error)`，PreparedUpdate含固定tag、私有候选路径、hash及Close；Unix传Apply，Windows原Update/AfterReplace；InstallRequest/Result使用T12定义。

- [ ] CLI/parser与fake app测试apply JSON严格合同，euid检查不可由token/request声明；service install保持注册不启动、reinstall启动、sh apply install启动、update保留running/enabled四格。
```go
// No-service update spy must observe only candidate verify, sibling lock and rename.
forbidden := []string{"open-base", "write-data", "write-token", "write-channel", "write-journal", "register-service"}
```
- [ ] Run `go test ./internal/cli ./internal/app ./internal/update ./internal/service ./internal/integration -count=1`；Run `python -m pytest scripts/install/test_install_channel.py -q`（沿用现有pytest测试入口，全部IO/进程fake）。
- [ ] 将UnixAfterReplace改成Prepare→可信事务；无service只binary父锁/同目录候选/sync/一次rename，禁bundle/source/data/path_binary/改变channel；替换后cleanup失败仍Updated=true。不能创建B。
- [ ] shell移除HOME/SUDO路径猜测与AIO overlay，写完整JSON request调用一个受信任root apply；download-only仅用户产物。path binary与I binary按identity去重，两个目标均备份/校验/同步；失败由T14恢复。TUI system/model.go的tea.Cmd只Prepare，发出退出并携带候选消息，由run.go完成cleanup后才Apply，再relaunch；不能在现有updater.Update执行完以后才cleanup。CLI self同样经事务。
- [ ] 保留Windows.ps1/service/update现有行为；CLI错误不含env/token；service start/stop/uninstall同lease，uninstall保留数据且终止受管树。

### T18：最终系统模式接线与兼容门禁

**Files:** Modify `cmd/mihari/main.go`、`internal/platform/paths.go`、`channel_unix.go`、`install_unix.go`、`internal/runtime/manager.go`、`internal/app/runtime.go`；Create `internal/integration/unix_system_layout_test.go`、`unix_shared_control_test.go`；Modify既有main/logging/service集成测试。
**Interfaces:** 最终入口只ResolveLayout一次，按Mode构造正确能力、provider、root策略、安装事务和snapshot capability。

- [ ] 切换前运行A/B/C全部目标测试并独立安全评审T06 registry、T08 mutation闭环、T14 crash模型、T16 activation；未完成不启用默认系统模式。
- [ ] root daemon/普通CLI+TUI两UID连接、无daemon首次TUI、token缺失后恢复、degraded配置、通用logging契约、显式非rootP、rootP、Windows基线建立行为Red。
```go
// In unix_shared_control_test.go: spawn fixture daemon, then two UID clients.
// Assert both can read status; neither can open D/settings or the other's U logs.
// All requests carry authenticated IPC and route through Manager.
```
- [ ] 删除Unix client prepareLocalRoot/loadProcessToken写能力路径；help/version不IO，channel只读缺失main，service status只读管理器。所有daemon依赖接收layout.Data，C/E/channel不经Paths.Absolute重建。
- [ ] RootConfigPolicy在root系统和root P开启，snapshot仅共享系统模式；外置C/E按模式owner和creation parent校验。私有P不碰默认E/嵌套B/D。
- [ ] Run `go test ./...`、`go vet ./...`；目标平台运行T19权限测试之前不宣称多用户安全已验证。检索DefaultPaths/AbsoluteDataRoot/LoadOrCreate/AfterReplace/直接UpdateRuleProvider的残余调用，逐项证明仅兼容或daemon用途。

### T19：隔离平台验证、文档与交付

**Files:** Modify `.github/workflows/ci.yml`、`README.md`、`README.zh-CN.md`、`docs/architecture.md`、`AGENTS.md`仅同步本次日志权限边界；Create `.github/workflows/unix-layout-security.yml`、`internal/integration/unix_security_linux_test.go`、`unix_security_darwin_test.go`（显式build tag unix_security）、`scripts/test/unix-layout-security.sh`；Modify交接文档。
**Interfaces:** 安全runner必须验证专用CI标记、临时根、进程/UID资源所有权；默认go test不执行root/mount/服务操作。真实主机service testenv另行授权，不混入本门禁。

**Runner输入/运行合同：** `scripts/test/unix-layout-security.sh --root <absolute-anchor> --uid-a <decimal> --uid-b <decimal> --report <absolute-json>`，report必须在本run预备的**独立结果目录**内（Linux `/var/lib/mihari-security-results-<run-id>`、macOS `/Library/MihariSecurityResults-<run-id>`），不在测试anchor内也不嵌套它；结果目录root0755、单独root0600 ownership-marker，已验证创建父目录。脚本只允许EUID0、`CI=true`、`MIHARI_ISOLATED_SECURITY_CI=1`以及两个当前任务生成的marker（含run-id和各自identity）；缺任一条件exit2。它不是本机便捷测试入口。CI workflow使用一次性hosted runner、只读repo权限、无secrets/真实订阅、超时15分钟；预备job生成的测试二进制和脚本为本PR代码，不得在持久自托管root主机运行。

- Linux fixture preparation只在经检查无ACL且root不可被普通UID写的 `/var/lib` 下创建唯一 `/var/lib/mihari-security-<run-id>`（0711）。macOS只在经同样检查的 `/Library` 下创建唯一 `/Library/MihariSecurity-<run-id>`（0711）。检查失败就fail，不chmod/chown祖先、不改真实 `/var/lib/mihari` 或用户home。其下system=B（0711）、data（0700）、users/a和users/b（各对应UID0700）、shared traversable parent（0711）；UID客户端接收注入的LayoutDefaults，不能调用真实默认B。root模型测试专用positive锚点与攻击子树分开，攻击树修改不影响runner/report根。
- Linux分配两个不存在的系统UID，用`useradd --system --no-create-home --shell /usr/sbin/nologin`创建带run-id用户名，记录实际id，不重用runner登录用户；macOS用`dscl`创建两个run-id临时用户，选两个未占用且不同的UniqueID（扫描50000–59999），PrimaryGroupID=20、UserShell=/usr/bin/false，不创建home。创建前将意图记入root cleanup清单，每步复核实际UID。拒绝0/相同UID/既有账户。测试客户端通过Go测试helper的SysProcAttr.Credential指定这两个UID，不依赖登录shell/sudo环境，也不改真实账号home。
- Linux实际运行在`unshare --mount --fork --propagation private`内，测试按临时目录做bind mount并在defer解除；禁止退出namespace后保留挂载。macOS以临时anchor内ACL文件验证allow/deny/inherit-only及清除，原生Fstatfs检查APFS/HFS；其必需能力项是ACL ABI、owner/mode、peer、目录identity，不要求Linux特有bind-mount原语。所有ACL改动只作用于临时对象。

```sh
# Workflow先按上述规则准备root/UID/marker；所有变量都来自job准备结果。
# Linux invocation (macOS省去unshare，其他参数相同):
sudo env CI=true MIHARI_ISOLATED_SECURITY_CI=1 \
  unshare --mount --fork --propagation private \
  bash scripts/test/unix-layout-security.sh \
  --root "$SECURITY_ROOT" --uid-a "$SECURITY_UID_A" --uid-b "$SECURITY_UID_B" \
  --report "$SECURITY_RESULT_DIR/result.json"

# Script校验后执行，Go/toolchain预先在非root准备阶段下载并固定绝对路径，
# 不在root测试中联网；所有GO*缓存参数指向本job临时目录且固定GOTOOLCHAIN=local。
export MIHARI_SECURITY_ROOT="$validated_root"
export MIHARI_SECURITY_UID_A="$validated_uid_a"
export MIHARI_SECURITY_UID_B="$validated_uid_b"
"$validated_go" test -json -count=1 -timeout=8m -tags=unix_security \
  ./internal/platform ./internal/control/transport ./internal/integration > "$raw_report"
test_status=$?
"$validated_python" scripts/test/verify-unix-security.py \
  --events "$raw_report" --root "$validated_root" \
  --uid-a "$validated_uid_a" --uid-b "$validated_uid_b" --out "$validated_report"
verify_status=$?
if [ "$test_status" -ne 0 ] || [ "$verify_status" -ne 0 ]; then exit 1; fi
```

Create `scripts/test/verify-unix-security.py`，只用stdlib，解析go test JSON并要求以下精确顶层测试名存在且Action=pass：`TestSecurityTrustedRootPositive`、`TestSecurityTwoUIDControl`、`TestSecurityPrivateDataDenied`、`TestSecurityOtherUserLogsDenied`、`TestSecurityCreationACL`、`TestSecurityPeerOwner`、`TestSecurityDirectoryIdentity`；Linux另要求`TestSecurityBindMountDenied`，Darwin另要求`TestSecurityDarwinACLABI`。这些tagged tests分别落platform/transport/integration对应文件；正常对照失败不能仍发能力成功。任一缺失/skip/fail或go进程非0都exit1。报告schema=mihari.unix-security-result/v1，含os/run_id/root identity/实际两UID/checks(pass或fail)/cleanup状态；测试helper把其实际geteuid、成功连接、拒绝读的结果写root测试进程管理的pipe，由root断言并记录，不能只回显输入UID。

Runner trap先将测试结果和cleanup清单保存到独立结果目录，再按清单回收自己启动且identity匹配的子进程→临时mount→临时账户→测试anchor，等待并核验后更新独立report的cleanup状态并sync。结果目录在trap中保留，故anchor删除不丢报告；仅脱敏汇总result.json发布0644供artifact步骤读取，原始go日志及cleanup清单先复制并sync到独立结果目录root0600，不自动上传。每步失败也记录report，cleanup失败job失败。不按名称杀进程、不删除标识不匹配对象；anchor和结果目录分别验证绝对路径、父目录及run marker，不能通过字符串前缀检查后删除任意目录。收到TERM也清理；工作流`always()`核对剩余资源、更新report后上传汇总，上传结束才按独立marker清理结果目录。SIGKILL或runner丢失时由一次性VM销毁收口，不宣称shell trap可捕获KILL。runner、verifier、always核查任一失败均保留非成功job结果，上传成功不能抹掉原失败。独立workflow需作为必需验收结果；不得因未配置branch protection就忽略该人工合并前置条件。

- [ ] Create `scripts/test/test_unix_layout_security.py`，pytest+fake命令runner模拟流程，不真实创建UID/mount。覆盖成功、go test失败、必需测试skip、TERM、每步cleanup失败、报告归档失败、always第二次恢复；断言外部artifact在anchor删除后仍存在，cleanup字段反映最终实际状态且原测试失败不会被恢复/上传抹去。归档失败时保留可验证anchor和外部cleanup意图，返回失败交always处理，不为“清理干净”删除唯一失败证据。Run `python -m pytest scripts/test/test_unix_layout_security.py -q`。此脚本测试加入普通CI，不依赖unix_security权限。

- [ ] 在隔离Linux/macOS job用临时目录、两个专用UID和fake服务/core跑mode/ACL/mount/peer/跨UID矩阵；每个临时对象记录路径与identity并cleanup，失败不遗留进程或挂载。Linux测试mount namespace；macOS使用受支持本地FS临时目录验证ACL ABI；不修改真实Mihari服务。
- [ ] 安全job测试缺失能力不能skip成成功；报告逐平台结果。普通Windows/Linux/macOS job运行 `go test ./...`、`go test -race ./...`、`go vet ./...`；race使用平台可用工具链，不把CGO0构建约束错误套到race。
```powershell
# Cross-build in a temporary output directory; restore all caller environment values in finally.
$buildDir = Join-Path ([IO.Path]::GetTempPath()) ([guid]::NewGuid().ToString())
New-Item -ItemType Directory -Path $buildDir | Out-Null
$oldCGO = $env:CGO_ENABLED; $oldOS = $env:GOOS; $oldArch = $env:GOARCH
try {
  $env:CGO_ENABLED = '0'
  foreach ($targetOS in @('windows','linux','darwin')) {
    foreach ($targetArch in @('amd64','arm64')) {
      $env:GOOS=$targetOS; $env:GOARCH=$targetArch
      go build -o (Join-Path $buildDir "mihari-$targetOS-$targetArch") ./cmd/mihari
      if ($LASTEXITCODE -ne 0) { throw "cross build failed: $targetOS/$targetArch" }
    }
  }
} finally { $env:CGO_ENABLED=$oldCGO; $env:GOOS=$oldOS; $env:GOARCH=$oldArch }
```
- [ ] 比较相关包与全仓coverage基线，下降解释并证明行为覆盖；`gofmt -l .`、`git diff --check`、shell installer测试、Windows既有升级/export ACL回归。没有新失败/变更时不重复无关测试。
- [ ] 文档解释路径表、普通用户控制权、配置/版本/provider兼容成本、停机token轮换、TUI-only显式选择、旧树/日志保留、恢复入口、I/FS限制和非root便携例外。不宣称FHS完全合规或真实服务测试已完成。
- [ ] 交付记录实际命令、平台与未验证项、准确diff、Astra代码审阅结果；未经用户授权不commit/push/PR/真实迁移。计划通过不等于这些实现测试已经通过。

## 规格追踪与审核收口

| R4规格 | 任务 | 阻止提前交付的验收 |
| --- | --- | --- |
| §1–4 模式/路径/兼容 | T01/T11/T18 | 无客户端机器树写入；Windows/私有布局保持 |
| §5 capability/ACL/mount | T02/T03/T19 | 原生Linux/macOS验证，不仅交叉编译 |
| §6 锁/peer/token/错误 | T03–T05/T16/T18 | 多进程互斥、每请求载入、原错误码 |
| §7 root策略/provider/provenance | T06–T08/T15/T18 | 完整typed registry、所有入口、可信核心 |
| §8 wire/export/UI | T09–T11 | exact bytes/EOF/预算/明确离线scope |
| §9 service/安装/更新 | T13/T15/T17 | 四状态、无服务不碰B、统一apply |
| §10 WAL/activation | T12/T14/T16 | 每action故障恢复、持久activation先于业务 |
| §11 迁移 | T15 | 活动资源完整、旧树保留、拒绝不可信执行 |
| §12 生命周期/测试 | T11/T16/T19 | worker归属、无泄露、平台运行证据 |
| §13 产品授权 | 首部/T18/T19 | 本轮只计划；实施契约由用户批准 |

计划审核要求Astra阅读全稿及R4，逐项检查：路径存在性、接口可接线、TDD行为Red、依赖先后、安全默认切换、规格无遗漏、真实环境授权边界。每轮报告写 `docs/superpowers/reviews/2026-09-05-unix-base-dir-plan-review-rN.md`，记录计划SHA256、问题级别、修订证据及PASS/REQUEST_CHANGES。不能为了通过而把必需功能移到未来任务或把未经验证的假设写成事实。
