# Unix Base Dir 执行计划评审 R2

- 结论：**REQUEST_CHANGES**，剩余 **2 项 Major**。
- 审阅日期：2026-09-06；代码基线：`2d00f61e720fa27f115dea52f7b4a95cc35a599f`。
- 计划：`docs/superpowers/plans/2026-09-05-unix-base-dir-implementation-plan.md`，R2，546 行。
- **计划 SHA256：`67457FABB9E8AF9545244236551658007424351FD993B8F15A6B377BB1BFFA43`。**
- 对照规格 R4 SHA256：`3A97A33DBB1B1CA5122F2A25F8545FD5A1EBD73B5DA5A160047749FF8A942F3E`，规格技术 PASS 不受本次计划问题影响。
- 完整重读计划并核对现有调用路径；未修改计划、规格或生产代码，未执行真实服务或实施步骤。

## R1 关闭核对

| R1 项目 | 结果 | 证据 |
| --- | --- | --- |
| 01 跨任务接口 | 关闭原主要缺口 | 81–118、130、301、315、345：OwnedDaemonLease、ControlLocator.ExpectedOwner、verified dial；SourceStats/SourceReader.Finish/SnapshotSet.Finish；generation uint64、候选身份绑定和 runtime.Operation；两个跨包真实类型 fixture |
| 02 有效 Red | 关闭原主要缺口 | 28、169–191、249–263：先正常对照，再唯一危险字段/组件，ErrUnsafeComponent/PolicyError 不能被更早失败冒充；两个小示例问题见下文 |
| 03 Windows 构建边界 | 关闭原主要缺口 | 132–144、197、224、230：Unix capability 桥接独立文件、显式标签、common 仅接口、Windows dispatch、旧 transport 到 T18 保留；每阶段编译门禁。模型测试标签的一处文字需统一 |
| 04 core WAL/执行器 | WAL 归属关闭，执行闭环仍未关闭 | 270–287：core-local pair WAL、不依赖 app、恢复先于执行、统一 VerifiedCore；但尚不能验证未发布核心/配置候选，见 R2-01 |
| 05 隔离 runner | 主体合同关闭，报告收口未关闭 | 475–507：实际 tagged 命令、两 UID、可信 anchor、namespace、必需测试名与机器结果、fail-on-skip；报告先删除后归档，见 R2-02 |

## UBP-R2-01 — InstalledPair 验证器不能替代未发布核心与配置候选能力

**严重度：Major / 核心安装、配置事务可实施性阻塞。计划行 270–287、294–295。**

计划把 VerifiedCore 规定为从 root pair store 创建，Command 只有 purpose，VerifiedRunner 还明确忽略调用者 executable（287）。这可以约束已安装核心的执行，但当前两条候选流程不能用此接口完成：

1. `internal/core/install.go:180–219` 的 Installer.Prepare 下载并解压新 candidate 后，在 **Commit 前** 对 candidatePath 执行 `-v` 和 `-t`。第一次安装尚无 installed pair；升级时 installed pair 指向旧 binary。若只使用 R2 的 pair verifier，绿装会因无 pair 被拒，升级则可能忽略 candidatePath 而验证旧版本，无法证明新候选可执行。
2. `internal/runtime/subscription.go:408–447` 的 prepareContent 在 staging 创建唯一候选文件，交给 `validateConfig(ctx,path)`；`internal/core/config.go:97–105` 必须验证该候选。R2 的 Command(ctx,purpose) 没有绑定这份候选的参数/能力，却规定验证和正式配置均在固定 core-home（287）。现有 Paths.RuntimeConfig 是 `D/runtime/config.yaml`，候选在 D/staging；不能通过忽略 configPath 或总是校验已生效配置完成事务。

这不是要求实现全部方法体，而是两个真实生产入口在给定接口上没有语义位置。

**修订条件：**

- 明确两种可信核心来源：已安装 binary+receipt 与尚未发布的 TrustedCoreCandidate。后者只能由内置受支持 asset hash 校验、root 私有解压和 binary identity/hash 建立；在 Commit 前允许必要的 version/config validation，不能要求存在旧 receipt，也不能让任意路径生成此能力。
- 引入绑定验证对象的 ConfigCapability 或等价参数，持有候选身份/内容 hash/生命周期，只有可信生成流程可创建。Validate 必须消费这一能力；不能用 purpose 字符串替代候选输入。
- 唯一定义 `D/staging` 候选、`D/runtime/config.yaml` 持久配置与 core-home 的映射。如果复制到 core-home 验证区，验证和最终提交必须证明来自同一候选字节；配置回滚仍对应最后有效版本。
- 明确 Candidate.Prepare/Version/Validate/Commit/Cleanup、InstalledPair.Recover/Command 的所有者与过期条件，禁止在 candidate 验证失败时运行/提交旧 binary 冒充候选成功。
- 加入可执行验收：绿装没有 pair 时可信候选 `-v/-t` 成功；升级刻意设置旧 binary 成功、新 candidate 失败，必须失败并保留旧 pair；新旧配置内容不同，测试证明 `-t` 实际收到新 candidate；候选被替换/关闭/hash 改变时 runner 零调用或提交被拒。

## UBP-R2-02 — Runner 在归档前清理 anchor，会丢失唯一验收报告

**严重度：Major / 必需 CI 验收收口阻塞。计划行 475、484–507。**

调用示例把报告固定为 `$SECURITY_ROOT/result.json`（488），507 行又要求 Runner trap 回收临时目录，再由 workflow always 步骤“先复制 report 到 artifact 目录，再删除 anchor”。如果 trap 已经删掉 anchor，always 无法复制唯一报告；若 runner 为保留 report 不删 anchor，又不符合当前 trap/cleanup 状态契约。cleanup 结果必须由最终清理后更新，不能在仍未删除目录/账户时提前宣称成功。

现有 `.github/workflows/ci.yml` 没有此报告/脚本可供沿用，所以任务必须明确新增 runner 的具体归档生命周期。

**修订条件：**

- 指定 anchor 外的本 job 受管 artifact 目录；原始 go events、机器结果、cleanup 清单应在破坏性清理前复制/原子归档到该目录，且不让测试 UID 改写结果。
- 规定唯一清理所有者和顺序：终止进程/解除挂载/回收临时账户 → 归档必要结果 → 删除 anchor → 将最终 cleanup 成败写入外部结果。若中途失败，always 通过仍存在的外部清单重试并更新报告，不能依赖已删除 anchor 中的文件。
- runner、verifier、always 的任一失败必须形成非成功 CI 结果；上传报告本身不能把安全 job 或 cleanup 失败掩盖掉。
- 添加脚本级 fake 测试，覆盖正常成功、测试失败、TERM、cleanup 某一步失败、报告归档失败及 always 二次恢复；断言 artifact 最终存在且 cleanup 状态准确。无需现在运行真实 UID/服务操作。

## 非阻塞的具体文字/示例修正

1. **模型测试的编译范围。** 190–191 行仍称注入 fd backend 的模型测试“全 OS 运行”，但 TrustedRoot/RootPolicy/OpenTrustedRoot 明确仅 linux/darwin（132–144）。给这些直接调用 Unix API 的模型测试同样加 Unix 标签，或另建明确 common 的纯算法单元；不要让 Windows 模型测试引用未定义 Unix 类型。
2. **RootPolicy 正例的 managed secret。** 253 行使用 `config.Defaults()`；当前 `internal/config/settings.go:160–168` 的 Defaults 不含 ControllerSecret，现有 `subscription.Generate` 又要求 secret（`generator.go:15–16`）。正例 fixture 应补一个固定合法测试 secret，避免因缺少已托管设置在危险字段验证前失败。
3. **PrivateFS 示例不要继续使用任意 t.TempDir 作为严格系统根。** 201 行注释仍与 T02 新的可信 anchor 规则冲突；注明从同任务合法子能力 fixture 或 tagged 安全 anchor 获取 root。Close-once 模型不应靠放宽生产祖先策略取得句柄。

## 复审条件与范围

修订核心候选/配置能力和报告生命周期后，再整体检查一次接口与任务依赖即可。其余权限、provider、服务恢复、默认切换和用户授权门禁不需要重新扩张。计划的 T18 仅接线、T19 全部必需 job 成功才可合并，以及“当前仅计划/审核，不实施/提交”的边界正确。

本轮没有要求平台实现已经运行通过，也没有把 R4 的技术 PASS 升格为产品或实施批准。审核结论绑定本报告记录的 R2 计划哈希。
