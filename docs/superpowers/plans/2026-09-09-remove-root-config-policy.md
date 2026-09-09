# 移除 RootConfigPolicy 实施计划

> **For agentic workers:** 使用 Superpowers 按任务实施并独立审查。用户已批准本方案；未勾选项尚未完成验收。

**Goal:** Unix root 与其他平台共同使用 `subscription.Generate`，保留非托管配置，由 mihomo 校验语义；TUN 按用户最新决定仅覆盖 `tun.enable`。

**Architecture:** 配置生成与可信核心执行解耦；Manager 保留 mutation/revision、候选校验、原子发布和回滚。历史 provider/resource 事务先恢复，再从原订阅缓存重新生成，新输入交由 mihomo 原生 provider 流程处理。

**Tech Stack:** Go 1.26.0 / toolchain go1.26.5；现有 YAML、Cobra、IPC 和可信文件能力；不新增依赖。

**Spec:** [交接文档第 1–3、5 节](2026-09-09-remove-root-config-policy-handoff.md)记录已确认方向和兼容边界。

## 全局约束与准备结果

- 工作目录为本文件所在仓库的 `codex/remove-root-config-policy` worktree。2026-09-09 查询远端 dev 为 `56bfeb2ac74a5d8b0a820742994bd52fc8d53611`，与 HEAD 相同。
- #215 仍 OPEN，head 为 `ac561dd906cd0602411831ca9ba01daea43795ed`，不作为依赖，不关闭它；实施前再次查询。
- 已读 CONTRIBUTING、README、AGENTS、当前 architecture 与关键生成/核心/恢复入口。AGENTS 引用的 `docs/superpowers/specs/2026-08-03-mihari-architecture-design.md` 在本 worktree 中不存在，当前以 `docs/architecture.md` 和交接约束核对；不要伪称已读缺失文件。
- 对比 `86a05ba`：`generator.go` 只增加了 PolicyBuilder/GenerateWithPolicy 及 context import，`Generate` 本体未改。复用该实现，但按用户最新决定将 TUN 整块替换改为仅覆盖 enable。
- 2026-09-09 用户补充：“新的方案应该仅修改tun enable”。该要求替代交接最初的 TUN 整块覆盖语义。存在显式托管 enable 时，仅覆盖该键；未设置时保留原值。保留应用 overrides 后的其他 TUN 字段，不自动注入 stack。原 tun 缺失且开关已托管时，只新增含 enable 的块。非法结构按解析/校验流程报错，不静默丢弃。
- 兼容已有 settings.Tun 数据：旧 stack 等非 enable 字段仍可读取，但不再作为 Mihari 的配置覆盖值；本任务不要求删除历史字段或改变持久化 schema。启停操作不得新增默认 stack，公开状态 DTO 保持兼容。
- 本任务改变 YAML 审查边界：不再保证 root 核心的所有字段经过 Mihari 注册表审计；关键覆盖不能限制所有额外 listener 或文件访问。此影响已由交接记录确认，当前文档须随实现更新。
- 保留 TrustedCore、核心来源表、receipt、hash/identity 校验、Unix 布局、安装 WAL、#212–#214 修复、独立 GeoIP 功能和稳定 CLI/JSON 契约。
- 不修改 CHANGELOG，不进行真实订阅实验或重置数据。用户后续已授权开发、提交/推送并开 PR，等 bot review 和 CI 全绿后构建并安装 local 版本。安装可执行必要的服务重启，必须保留当前业务数据；本任务没有合并 PR 或发版授权。

## 任务 1：固化生成语义并取得 root 路径失败证据

**文件：** `internal/subscription/generator.go`、`generator_test.go`、`internal/runtime/tun.go` 及相邻测试、`internal/runtime/provider_test.go`、`internal/core/trusted_runtime_test.go`。

**接口：** 继续使用 `Generate(base Document, overrides map[string]any, settings config.Settings) ([]byte, error)`。最终 core 接口见任务 2。

- [x] 在 generator 测试中使用以下纯合成输入，同时检查托管字段、输入不变及嵌套字段保留。将断言 TUN 整块覆盖的旧测试修订为仅 enable 覆盖，先确认失败源于当前实现丢失非 enable 字段。

```yaml
mixed-port: 1
bind-address: 0.0.0.0
external-controller: 0.0.0.0:9999
secret: fixture-only
allow-lan: true
external-ui: fixture-panel
external-ui-name: fixture-panel
external-ui-url: https://example.invalid/panel
proxies:
  - name: fixture-node
    type: ss
    server: 127.0.0.1
    port: 443
    cipher: aes-128-gcm
    password: fixture-only
    x-client-metadata:
      labels: [fixture]
```

- [x] 对 base 做 YAML 快照，Generate 前后比较；输出解码后检查 `x-client-metadata.labels`。overrides 同时设置 mixed/controller/secret，断言 settings 胜出；检查三个 external-ui 字段均消失。
- [x] TUN 合成块包含 stack、device、mtu、dns-hijack、auto-route、route-exclude-address 和嵌套附加字段。表驱动测试托管 enable=true、false、未设置：只允许 enable 按意图变化，其他字段及输入保持不变。另测原 tun 缺失、旧 settings.Tun 含不同 stack、overrides 提供 TUN 字段和非法 tun 结构。
- [x] 将 Generate 的 TUN 整块替换改为对原块深拷贝后仅设置 enable；未托管时不修改。调整 buildManagedTun，不再默认生成 stack；验证已有持久化非 enable 字段不会重新覆盖原订阅。
- [x] 同一 fixture 进入当前 root 受管刷新路径，断言应成功并保留附加字段。运行后须记录由 policy unknown 引起的失败，不能使用 nil TrustedCore、编译失败或仅 generator 通过作为证据。
- [x] 核心层可复用 `newMemoryStore`、`seedInstalledReceipt`、`memoryConfigs`、`recordedExecutor`，证明候选确实到达验证执行器；runtime 测试另证明生产装配走到同一接口。
- [x] 运行 `go test ./internal/subscription -run '^TestGenerate' -count=1` 与新增 root 定向测试；记录现有语义通过和 root 路径失败各自含义。

## 任务 2：解耦候选字节，保留核心执行能力

**文件：** `internal/core/trusted_runtime.go`、相邻测试、`internal/runtime/subscription.go` 及其调用者。

**目标接口：** `(*TrustedExecution).PrepareGenerated(context.Context, []byte) (*GeneratedConfig, error)`；生成工作归 subscription/Manager，删除无消费者的 BuildConfig。InitializeConfig 接收已生成字节，保留首次无核心时的 bootstrap 初始化语义。

```go
content, err := subscription.Generate(document, nil, settings)
if err != nil {
    return configCandidate{}, err
}
// 将 content 交给已有可信候选流程；候选仍持有 cleanup、hash 和 owner。
```

- [x] 先扩展 core 回归：源 byte slice 后续变化不能改变候选；不同候选 hash 不可互换；核心拒绝候选不得写 committed；缺失 receipt 与二进制不成对必须失败。
- [x] PrepareGenerated 内复制字节并保留 `files.prepare → OpenInstalledCore → ValidateVerifiedConfig`。不改变 Publish 的 owner/hash/identity 复查、PreviousConfig/RestoreConfig 和执行租约。
- [x] 让 `prepareConfigWithSettings` 统一 Generate，再按是否存在 TrustedCore 选择既有候选验证方式；删除策略 input 回调依赖。
- [x] 改造 core 测试的 PolicyOutput 参数，保留其原本证明的候选、防替换与恢复行为。
- [x] 运行 core、runtime 定向测试及两包完整测试，审查所有 trusted 发布调用者。

## 任务 3：先恢复历史事务，再切换全部入口

**文件：** `internal/subscription/provider_journal.go`、`resource_journal*.go`、`provider_store*.go`、`internal/app/runtime.go`、`internal/app/install_migration.go`、`cmd/mihari/unix_layout.go`、`cmd/mihari/install_validation_unix.go`；相邻 app/core/cmd 测试。

**接口：** 保留 `(*ProviderStore).Recover(context.Context) error` 为历史恢复入口。当前 ResourcePreparer.Recover 只是调用它；内部顺序为 whole-resource 恢复、provider 恢复、事务清理，不能倒置。

- [x] 为四类 fixture 增加恢复断言：历史 managed provider + 原缓存；缺失原缓存；未完成 provider WAL；未完成 resource WAL。对失败场景逐项比较旧配置、缓存、catalog、资源字节，检查没有联网请求。
- [x] 提取仅供历史恢复所需的 journal/store 类型及函数；不要删除恢复依赖的路径约束、身份核验与同步步骤。新写入不再生成资源图事务。
- [x] 启动读取 catalog 之前恢复 WAL，再选择活动订阅原缓存。无活动订阅使用 DIRECT bootstrap，不改变 catalog/generation；活动缓存无效时返回脱敏 data_failure 并保留旧有效配置。
- [x] 不从已重写的 runtime YAML 猜测原始 provider URL，不联网修补缺失缓存，不自动清理历史资源。原缓存中原生 file provider 的合法路径按旧 Generate 保留，由核心验证。
- [x] BuildRuntime、BuildValidationRuntime、安装 staging 业务校验都改为同一生成语义；安装 staging 的后续核心校验和 activation 边界继续保留。
- [x] 覆盖首次无核心、已有核心、重启、安装验证和迁移；保留 bootstrap 残留、安装恢复及源树保留测试。

## 任务 4：移除受管 provider 新写入和 scheduler

**文件：** `internal/runtime/provider.go`、`manager.go`、`resource_activation.go`、`subscription.go`、`tun.go`、`onboarding.go`；`internal/integration/unix_provider_contract_test.go` 与相邻 runtime 测试。

- [x] 先补刷新、切换、settings/TUN/onboarding 的失败回归：核心拒绝、reload 失败、revision 或订阅版本变化均不能覆盖当前有效状态。
- [x] TUN 启停的 live 更新也必须保留当前非 enable 字段；核查 controller 更新接口的合并语义，必要时使用当前完整块只改变 enable 后提交。断言启停、刷新、重启后的有效配置一致，失败时恢复开关及原始非 enable 字段。
- [x] 将刷新/切换接回普通订阅事务，保留 mutation、版本复查、缓存提交补偿和降级行为；删除 providerResources 分支前逐一比对这些保证。
- [x] provider 刷新通过既有 controller 更新入口和 coordinator。以 loopback fake controller 记录方法、目标 provider、响应和结果 revision，验证重复 operation ID、错误码和失败时状态，而非仅验证调用次数。
- [x] 用 httptest provider + fake 核心证明原始 provider 定义和其附加字段可到达核心 fixture，Mihari 不再下载重建或拒绝未知字段。此测试不声称真实 mihomo 接受任意字段。
- [x] 删除受管 scheduler 启动及资源图构建消费者；通过取消/退出测试确认无残留 worker，订阅原有自动刷新继续保留。
- [x] 运行 runtime、app、control/server 和 integration；不删除独立 GeoIP 下载、更新及连接定位能力。

## 任务 5：删除策略实现并同步文档、CI

**文件：** `internal/subscription/rootpolicy*.go`、`generator_policy_test.go`、`testdata/rootpolicy`；`internal/core/provenance.go`、`migration.go` 及测试；README 双语版、CONTRIBUTING 双语版、`docs/architecture.md`、`docs/unix-layout.md`、`docs/commands.md`、`docs/architecture/root-config-policy-v1.md`；`scripts/test/unix_security*.py` 及对应测试/workflow。

- [x] 在 core 层定义 receipt 兼容常量，值严格保持 `mihari.root-config/v1/mihomo-v1.19.30`。fixture 使用固定历史 JSON，而非生产常量拼接，检查旧 receipt 可读且错误 ID/hash 仍被拒绝。
- [x] 删除 PolicyBuilder/GenerateWithPolicy 和已无消费者的 rootpolicy 实现、测试、资料；先检索所有资料引用。仅恢复所需类型移到职责明确的历史文件，不保留活跃白名单。
- [x] 更新当前文档，明确恢复旧生成职责和 provider 由核心处理；历史文档添加被替代状态，不改写历史叙述。缺失旧文档不新造同名文件。
- [x] 将只断言字段拒绝的 CI 场景替换成托管覆盖与核心校验证据；保留完整 Unix 文件权限、IPC、token、安装恢复、hash 和回滚验收。
- [x] 再查 #215；若仍 OPEN，在后续 PR 描述说明替代关系；若已合入，仅清理策略专用映射与测试，保留通用错误脱敏。

## 任务 6：最终验证与交付

- [x] 运行交接文档第 7 节完整命令：目标包、integration、全仓、race、vet、gofmt、diff 检查及六目标 CGO0 编译。
- [x] 运行 `python -m pytest scripts/test/test_unix_layout_security.py -q`；原生 root/two-UID 场景留给隔离 hosted CI，不在用户 VM 执行。
- [x] 按接口检查无活跃 PolicyInput/PolicyOutput/RootConfigPolicy 消费者；历史 receipt 字符串允许保留，不以 grep 清零代替语义检查。
- [x] 复核变更仅涉及移除范围，保留原交接文档与用户修改；提交、推送、PR、合并按后续明确授权执行。
- [ ] 创建指向 dev 的 PR，处理 bot review，确认最终 head 的全部 CI（含原生 Unix 安全验收）和 bot review 通过后才进入本机安装。
- [ ] 从通过检查的 head 构建 CGO0 local 版本，记录 commit/version/hash；备份当前安装二进制及必要恢复信息，使用当前业务数据完成更新，不使用早期过时备份覆盖数据。
- [ ] 核对安装后二进制、运行 daemon 版本、服务健康与核心状态；向用户报告 PR、检查结果、本地版本和验证范围。

## 实施前基线验证记录（当时生产代码未改）

命令：`GOTOOLCHAIN=go1.26.5 /usr/local/go/bin/go test ./internal/subscription ./internal/runtime ./internal/core ./internal/app ./internal/control/server`。

- subscription、runtime、core、control/server 通过。
- app 失败：`TestUnixMigration_TrustedCapStatIdentityAndMtime`、`TestNativeInstallState_BootstrapCleanupAcrossBoots`、`TestNativeInstallState_FinalizedForegroundCleanupKeepsRollbackShape`、`TestMigrationCapabilities_SameDirectoryHasMatchingIdentity`、两项 `TestUnixInstallationResources_*`，均报告 `writable directory ancestor: permission denied`。
- 已追踪：`migrationTrustedTempDir` 在包工作目录创建 fixture；本机 `/home/kinema/dev` 和仓库根权限为 0775；`TrustedRoot.checkLink` 拒绝 `mode & 0022 != 0` 的祖先。这是未修改基线的环境前置条件失败，不能作为移除回归，也不能报告全绿。未修改目录权限或放松校验。
- 实施验证可将 app 测试编译为临时二进制，在祖先满足权限要求的新建隔离测试目录内运行相关原生 fixture；需核对其他测试是否依赖包相对路径。不得直接 chmod 用户 checkout 或以跳过安全测试收口。
- 上述准备阶段未执行全仓、integration、race、vet、六目标编译或真实环境验证；后续实施验证另行记录，不将准备阶段结果当作最终验收。

## 实施验证记录

任务 1–5 已实施并通过独立审查。TUN 无活动订阅时曾发现 legacy settings 字段注入，已以普通路径和可信核心路径的失败回归修复；无源 bootstrap 仅生成托管 enable，TUN 操作单独保留 live 非 enable 字段。

- 在祖先权限合格的隔离镜像运行全仓普通测试与 race：app、core、runtime、subscription 等改动包通过，无数据竞争报告。两套完整命令均因既有日志导出 spool 清理问题退出非零；失败名单与原始 `56bfeb2` 基线一致，涉及 control/client、integration、logging 的 8 个顶层测试，没有新增失败。不能据此声称本地全仓全绿。
- 完整 vet 和 golangci-lint v2.12.2 通过；静态检查发现的两个无消费者 bindingIdentity 方法已删除，真实身份检查保留，core 完整测试复验通过。
- Windows、Linux、macOS 的 amd64/arm64 六目标 CGO_ENABLED=0 构建通过。
- Unix 安全验收器的 77 项普通 Python 测试通过；未在本机执行真实 root/two-UID 安全宿主场景。
- 原生平台验收与 bot review 仍以 PR 最终 head 的结果为准。通过之前不安装本地候选，不合并 PR 或发版。
