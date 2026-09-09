# 移除 RootConfigPolicy、恢复旧配置生成方式：交接任务

> 本文保留实施前的交接现场与范围；当前执行进度和验证记录见[实施计划](2026-09-09-remove-root-config-policy.md)。

> **For agentic workers:** 执行时使用 `superpowers:executing-plans`，按下列检查项逐项推进；本文件是交接材料，本轮没有实施移除。是否使用子 agent 遵循接手会话的指令，不自动创建新任务。

**Goal:** 恢复添加 RootConfigPolicy 之前的处理方式：保留订阅配置，仅覆盖 Mihari 管理的关键参数，由 mihomo 校验其配置语义，不再维护一套完整字段白名单。

**Architecture:** 复用现有 `subscription.Generate` 作为各平台共同的配置生成语义。保留 daemon 单写入、控制协议、mutation/revision、候选校验及原子替换/回滚；将 Unix 高权限执行与完整 YAML 策略解耦。provider 恢复旧流程时必须兼容已有数据与恢复日志。

**Tech Stack:** Go；go.mod 为准（当前 Go 1.26.0 / toolchain go1.26.5），Cobra、本地控制协议、mihomo、Unix 系统服务；发布 CGO_ENABLED=0。

**Spec:** 本文第 1–3 节记录用户在 2026-09-09 的最新决定，替代旧设计中“必须执行完整 RootConfigPolicy”的要求。旧设计背景为 `docs/superpowers/specs/2026-09-05-unix-base-dir-design.md` 第 7 节与 `docs/architecture/root-config-policy-v1.md`，不可把旧方案当作本任务仍需实现的目标。

## 1. 接手位置、状态与授权范围

- Worktree：`/home/kinema/dev/mihari/.worktrees/remove-root-config-policy`
- 工作分支：`codex/remove-root-config-policy`，从最新 `origin/dev` 创建；不要直接在 dev/main 修改或提交。
- 基线：`56bfeb2ac74a5d8b0a820742994bd52fc8d53611`。
- PR #214：https://github.com/mihari-proxy/mihari/pull/214 。head `0fe1add` 的 CI（含 Linux/macOS 原生安全测试）、Cubic、Pullfrog 全部成功后，于 2026-09-09 05:48:29 UTC admin squash 合并。安装引导残留和迁移修复必须保留。
- 当前文档是未提交的交接文件；没有在本分支改生产代码、运行系统安装、发版或创建移除 PR。
- 用户原话：“只覆盖关键参数，指的是添加 RootConfigPolicy 之前的处理方法。”不是补齐白名单、开兼容开关、增加更复杂的沙箱或换用非 root 安装。
- 当前明确任务是梳理并交接；执行移除由后续 agent 接手。后续提交、推送、合并、发版以其会话授权为准，不能把本次 #214 合并授权扩展成任意新发版。
- PR #215：https://github.com/mihari-proxy/mihari/pull/215 ，交接时 OPEN，head `ac561dd906cd0602411831ca9ba01daea43795ed`，分支 `codex/ubuntu-subscription-refresh`。它只给 PolicyError 添加 API 错误分类及测试/文档，不解决兼容性，也未包含在此基线。接手先查询其状态；不必依赖或先合并它，不要擅自关闭它。若后来已合入，应随策略移除清理相应专用测试与说明，保留通用未知错误脱敏行为。

## 2. 已确认的问题与尚未确认的事实

用户报告同一订阅在 Windows dev.3 可更新，Ubuntu 不可更新。实际 Ubuntu 当前运行的是本地修复构建 `v0.9.3-dev.3+local.c57750a`，不是官方 dev.3 原始二进制；该本地变更只针对安装引导/迁移，未改变订阅策略。

只读复现使用与 daemon 相同的 Downloader、ParseDocument、YAML marshal、RootConfigPolicy.Inspect：下载/解析成功，策略返回 `PolicyError{Field:"proxies[].[unknown]", Code:data_failure}`；原 API 将其吞成 internal/500。首次缓存 generation=0，故 TUI 显示 missing。未查明实际被拒绝的字段名，也未验证它是 mihomo 合法字段还是被 mihomo 忽略的附加字段；不能在提交说明里编造原因。

平台差异是有意接线造成：`cmd/mihari/unix_layout.go` 在 uid==0 时注入 TrustedCore、ResourcePreparer、RootConfigInput；Windows `windows_layout.go` 不注入这组依赖，走旧 Generate 路径。Unix 非 root 私有 daemon 也不启用该策略。不是 Linux 网络下载失败。

原 RootConfigPolicy 固定 mihomo v1.19.30，用闭合注册表重建配置、限制文件/监听/provider 能力。恢复旧方式意味着 Mihari 不再保证“所有传给 root 核心的字段都经过该注册表审计”。这是用户选择的职责调整，应在架构与使用文档明确说明；不能一面删除策略，一面保留原安全保证。关键 override 不等同于限制所有额外 listener 或所有文件访问。

## 3. 精确目标：恢复哪些旧语义

权威代码参考：`internal/subscription/generator.go` 的 `Generate(base Document, overrides map[string]any, settings config.Settings) ([]byte, error)`。旧提交可直接比较：

```console
git show 86a05ba866f5540fa3850b47a3347c2099706bdf:internal/subscription/generator.go
git diff 86a05ba866f5540fa3850b47a3347c2099706bdf -- internal/subscription/generator.go
```

`86a05ba` 为引入 Unix 大改的 #211（3b71c19）的父提交。只借鉴相关语义，不整体 revert #211。

除下述 TUN 修订外，应保留 Generate 的行为。2026-09-09 接手会话用户进一步明确：“新的方案应该仅修改tun enable”。该决定替代本交接最初要求的 TUN 整块覆盖旧语义。

| 输入/字段 | 旧处理方式 |
| --- | --- |
| 非托管节点字段、嵌套协议配置、规则等 | 深拷贝后保留，不因未在 Mihari 注册而拒绝 |
| overrides 参数 | 先应用，再由托管参数覆盖 |
| mixed-port、bind-address | 来自 settings.MixedAddr |
| allow-lan | 固定 false |
| external-controller、secret | 来自 Mihari settings |
| external-ui、external-ui-name、external-ui-url | 删除，保持面板管理边界 |
| tun | 仅在存在显式托管 enable 时覆盖 tun.enable；保留订阅（应用 overrides 后）的 stack、device、路由、DNS 及其他 TUN 字段；没有托管 enable 时保持原值。禁止整块替换或自动注入 stack |
| 缺少代理组/规则的节点订阅 | ensureRoutable 补 MIHARI 组与规则 |
| mode / log-level | 缺失时补 rule / info |

不继续强制 RootConfigPolicy 的 profile 缓存关闭、geo-auto-update=false、geox-url 清空、协议逐字段重建等额外语义。保留基础 YAML 解析、大小/超时限制、settings 校验和实际核心 `mihomo -t`；“不做全字段白名单”不等于接受损坏 YAML 或跳过核心校验。

## 4. 代码规模与依赖地图

基线下 `internal/subscription/rootpolicy*.go`：48 个生产文件约 7,622 行；59 个测试文件 7,093 行；`testdata/rootpolicy` 20 个资料文件 148,525 行。行数含空行/注释，资料含上游源码快照，不是运行时代码。此前 7,627 行统计包含 #215 的 5 行 Unwrap。以下关联代码没有计入这些数字，不应整文件机械删除。

| 区域 | 具体入口与需要处理的内容 |
| --- | --- |
| 策略本身 | `internal/subscription/rootpolicy*.go`、`generator_policy_test.go`、`generator.go` 的 PolicyBuilder/GenerateWithPolicy |
| Unix 装配 | `cmd/mihari/unix_layout.go` 的 uid==0 PrepareRuntime、早期 ResourcePreparer.Recover；`cmd/mihari/install_validation_unix.go` 的验证运行时 |
| 启动及迁移校验 | `internal/app/runtime.go` 的 RuntimeBuildOptions、StartupResources、startupPolicyInput、BuildValidationRuntime；`internal/app/install_migration.go` 的业务校验；不能只改在线刷新 |
| runtime | `internal/runtime/subscription.go` 的 RefreshSubscription/prepareConfigWithSettings；`manager.go` 的依赖装配、Install、Run；`provider.go` 的刷新/切换/定时任务/设置与 TUN 分支；`resource_activation.go`；`tun.go`、`onboarding.go` |
| provider/Geo | `internal/subscription/provider_resources*.go`、`provider_store*.go` 等：图、快照、缓存、事务、恢复；闭合策略和生命周期混在一起，需要按责任拆解 |
| 核心候选执行 | `internal/core/trusted_runtime.go` 的 BuildConfig、PrepareGenerated、InitializeConfig；`verified_command_unix.go`；不能删除候选 identity/hash、校验与原子发布保护 |
| 核心来源记录 | `internal/core/provenance.go`、`migration.go`、`supported_policy.json`：持久化 receipt 中 PolicyID 引用了 RootPolicyID；移除 YAML 策略不等于允许所有核心来源 |
| API/面板 | `internal/runtime/manager.go` UpdateRuleProvider → provider.go；旧 fallback 经 controller.UpdateRuleProvider，仍需 coordinator。保留 control/server、client、Web gateway 的协议和认证 |
| 测试/CI | `internal/integration/unix_provider_contract_test.go`、core trusted/resource 测试、app runtime_trusted/validation 测试、cmd 原生安全场景、scripts/test/unix_security*.py 及 workflow |
| 文档 | `docs/unix-layout.md`、`docs/architecture.md`、`docs/architecture/root-config-policy-v1.md`、`docs/commands.md`，及历史设计/计划的状态标识 |

检索辅助命令：

```console
rg -n 'RootConfigPolicy|RootPolicyID|GenerateWithPolicy|PolicyInput|PolicyOutput|PolicyError|PolicyRequirements' cmd internal docs scripts .github
rg -n 'providerResources|trustedCore|RootConfigInput|StartupResources' internal/runtime internal/app cmd
rg --files internal/subscription | rg '(rootpolicy|provider_resources|provider_store|generator)'
```

## 5. 不能顺手撤销的部分与必须先解开的耦合

1. **保留 #212–#214 的安装修复**、Unix 系统布局、socket/token、文件权限、安装 WAL、升级事务与迁移来源保留；不恢复旧版“装完服务无法启动”。
2. **保留核心二进制身份校验**。本任务针对 YAML 策略，不能通过令 TrustedCore=nil 就整体回退到不验证核心的路径。推荐将 PrepareGenerated 接收的 PolicyOutput 解耦成生成配置字节或最小专用类型，保留 GeneratedConfig 的所有权/hash/校验/发布机制。
3. **PolicyID 持久化兼容**。现有核心 receipt 含旧字符串；在 core 层保留读取兼容性，不能直接改字符串或删除校验导致已安装核心失效。是否扩大核心版本允许列表是另一项范围，不随本任务自动实施。
4. **provider 回到旧职责**。新输入应保留原始 provider 定义，由 mihomo 原生流程处理，不能再为全字段注册表下载并重建 provider。保留已有 CLI/Web 的刷新入口和 mutation 控制。移除新定时器时确保停止及无孤立 goroutine。
5. **旧数据不能丢**。现有安装可能已有哈希命名本地 provider、Geo 资源、已生成配置和 provider/resource WAL。先恢复未完成事务，再从原订阅缓存重新生成；原始 URL/缓存缺失时不得猜测、联网补数据或删除资源，应返回可诊断错误并保留旧有效配置。只有证明无引用后才考虑清理旧格式。
6. **不能替换成新的隐式白名单**。不通过静默删除全部未知字段、降低 case 覆盖或重新复制一份 mihomo schema 来“修复兼容性”。
7. **不扩大项目外的操作权限**。交接文档不授权接手者使用真实订阅、重装本 VM 服务、清空数据或执行旧 rollback.sh。普通测试用隔离 fixture；真实验证需在后续会话明确安排。

## 6. 建议实施顺序（每项 Red → Green → Review）

### A. 固化共同生成语义

- [ ] 阅读 CONTRIBUTING、README、最近 AGENTS，复核 Generate 与 #211 前版本的差异。
- [ ] 在 `generator_test.go` 增加合成 fixture：节点含 `x-client-metadata` 嵌套字段，断言深拷贝保留；恶意覆盖 mixed/controller/secret 后仍以 settings 为准；原输入不变。TUN 按接手会话最新决定，仅覆盖 enable，覆盖 true/false/未托管三态以及原 tun 缺失场景，其他 TUN 字段保持不变。
- [ ] 修改 `buildManagedTun` 及实际核心更新链路，使开启/关闭只改变 enable，不自动写入默认 stack；对遗留 settings.Tun.stack 等字段保留读取兼容，但不将其作为生成配置覆盖值。同步修订旧整块覆盖测试；检查 live 更新、刷新、重启和失败回滚均保留非 enable 字段。
- [ ] 在 runtime 测试里将同一 fixture 通过 Unix 当前受管路径运行，先确认因 policy unknown 失败，再接入共同 Generate。仅给 Generate 本身加测试不能证明 root 路径已切换。
- [ ] 用 `go test ./internal/subscription -run '^TestGenerate'` 和新增 runtime 定向测试记录正确的失败/通过原因。

### B. 解耦核心候选并接通所有入口

- [ ] 修改 `core/trusted_runtime.go` 的 BuildConfig/PrepareGenerated/InitializeConfig 与调用者：使用 Generate 的输出，保留实际核心验证与候选绑定。
- [ ] 删除 RuntimeBuildOptions/Manager 中仅为策略存在的输入回调，修改 Unix 在线和安装验证两处装配。
- [ ] 覆盖首次无核心启动、已有核心启动、重启、刷新、切换、设置/TUN 改变、安装验证和迁移；不要让其中某条仍隐式调用策略。
- [ ] 加失败回归：核心拒绝候选、reload 失败、revision 冲突时，旧缓存/配置/活动订阅仍可用；无“先写坏配置再报错”。

### C. provider 生命周期与旧数据衔接

- [ ] 使用本地 httptest provider 测试：入口及 provider 内的附加字段可到达核心 fixture，Mihari 不再进行闭合字段拒绝。
- [ ] 恢复 provider 刷新旧 fallback 的实际行为，验证控制路径、revision、错误码和核心 reload/更新，不只断言函数被调用。
- [ ] 构造已存在旧 managed provider、有效原始订阅缓存、缺失原始缓存、未完成 provider/resource WAL 四类临时目录 fixture，验证恢复顺序、数据保留和失败结果。
- [ ] 移除运行中的 managed scheduler/资源图构建；如为读取旧数据而保留恢复代码，应明确其只处理历史格式，不能继续成为新订阅字段校验路径。
- [ ] 保留 GeoIP 产品功能；不因不再闭合资源图而删除独立 GeoIP 更新、定位或目录配置功能。

### D. 删除无消费者的策略代码与同步合同

- [ ] A–C 全部接通后，删除 rootpolicy 实现/专用测试/专用 fixtures/GenerateWithPolicy。对 PolicyOutput 等跨包类型先完成消费者改造。
- [ ] 为持久化 receipt 的旧 policy_id 保留明确的兼容测试；不要为消除 grep 结果破坏已安装核心。
- [ ] 核查测试资料是否有其他消费者，再删除 registry/source snapshot 等大文件。
- [ ] 调整仅验证旧字段拒绝的 CI 场景和证据列表；保留文件系统、安装恢复、token、IPC、哈希、回滚等无关安全测试，不禁用整个 unix-layout-security workflow。
- [ ] 更新当前文档，历史设计注明已被本方案替代，不篡改其历史含义。不修改功能 PR 的 CHANGELOG。
- [ ] 若 #215 已合并，移除过时的策略错误映射及专用测试，保留一般 API 错误分类和秘密保护测试；若仍 OPEN，在 PR 描述说明被本任务取代的关系，是否关闭由用户决定。

## 7. 验收与命令

最终验收：同一合成订阅通过 Windows 旧语义与 Unix root 新接线产生等价非托管配置；核心认可的字段不再被 Mihari 注册表挡住。关键覆盖、输入不变、缓存/活动订阅一致性、失败回滚和安装迁移保持有效。没有新增策略开关，也没有关闭所有验证。必须明确原 provider WAL/缓存升级行为。

```console
GOTOOLCHAIN=go1.26.5 /usr/local/go/bin/go test ./internal/subscription ./internal/runtime ./internal/core ./internal/app ./internal/control/server
GOTOOLCHAIN=go1.26.5 /usr/local/go/bin/go test ./internal/integration
GOTOOLCHAIN=go1.26.5 /usr/local/go/bin/go test ./...
GOTOOLCHAIN=go1.26.5 /usr/local/go/bin/go test -race ./...
GOTOOLCHAIN=go1.26.5 /usr/local/go/bin/go vet ./...
/usr/local/go/bin/gofmt -l cmd internal
git diff --check
```

六目标 CGO-free 构建检查（输出到临时目录，避免产物入库）：

```bash
mihari_build_dir=$(mktemp -d)
for mihari_os in windows linux darwin; do
  for mihari_arch in amd64 arm64; do
    GOTOOLCHAIN=go1.26.5 CGO_ENABLED=0 GOOS="$mihari_os" GOARCH="$mihari_arch" \
      /usr/local/go/bin/go build -o "$mihari_build_dir/mihari-$mihari_os-$mihari_arch" ./cmd/mihari || exit 1
  done
done
```

代码验证通过后按授权提交/PR，等待 CI 与 review。原生 root CI 由隔离 runner 验证，不在用户 VM 伪造 CI 环境运行安全宿主测试。

## 8. 已运行验证、主机现场与交接注意

- 此 worktree 的基线定向 `go test ./internal/subscription -run '^TestGenerate' -count=1` 已通过；本轮只新增此 Markdown，没有实施上面 A–D，不声称移除后测试通过。
- 此 VM 本地日志导出测试有已复现的基线失败：control/client 的 TestOpenMachineSnapshot_RejectsMutatedFixtureWithoutPublishing 等报告 spool remains；integration 的 UnixSnapshotContract 类测试也曾在未修改 9e0c5bf 基线上复现。后续先复核环境/基线，不把这类失败默认为移除策略造成，也不能隐去它们。
- 主 checkout `/home/kinema/dev/mihari` 的 main 未修改；另外两个旧 worktree 保留。不要清理、重置或覆盖其他任务分支。
- 用户 VM 已添加真实业务配置。`/var/backups/mihari-local-c57750a` 是早期安装备份，早于用户后续订阅/核心配置；不能执行其中 rollback.sh 回到旧数据。不要把订阅 URL、凭据或内容复制进本文件/fixture/PR。
- 当前 9191 监听曾确认属于 Mihari 自身 Web 服务（127.0.0.1）；端口问题不属于本次策略移除。
- #214 后仍记录一个相邻的 install activation changed 启动竞态（偶发首启失败后 systemd 重试成功），不属于本次移除任务，不顺手扩大范围。
- #214 的后台轮询脚本在全绿后已结束，状态记录位于 `/home/kinema/mihari-bootstrap-verify-q1np5chm/pr214-status.json`；这不是对后续 PR 的持续监控。

交接完成标准：接手 agent 能从本 worktree 直接定位旧生成器、全部策略入口、需要兼容的数据与应保留的不变量，并先写行为回归测试；不得把“仅删 rootpolicy 文件”或“设置 TrustedCore=nil”当作完成。
