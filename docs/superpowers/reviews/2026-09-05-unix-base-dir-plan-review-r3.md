# Unix Base Dir 执行计划评审 R3

- 结论：**PASS（执行计划技术审核通过）**。没有剩余 Blocking、Critical 或 Major 问题。
- 审阅日期：2026-09-06；代码基线：`2d00f61e720fa27f115dea52f7b4a95cc35a599f`。
- 计划：`docs/superpowers/plans/2026-09-05-unix-base-dir-implementation-plan.md`，R3，564 行。
- **计划 SHA256：`0B708CE6FDBCE432FB26F16068F4B087F18CC6B2006A80BE39278BDC5CD3F74F`。**
- 对照规格 R4 SHA256：`3A97A33DBB1B1CA5122F2A25F8545FD5A1EBD73B5DA5A160047749FF8A942F3E`；本轮核验未改变。
- 本轮完整重读计划与 R4，核对历史问题、核心准备/配置提交/正式启动的当前代码及各任务的接口、平台边界、依赖和验收门禁。仅新增本报告，未修改计划、规格或代码，未实施、提交或运行真实服务。

## R2 问题关闭

| 项目 | 结果 | R3 修订证据与代码核对 |
| --- | --- | --- |
| UBP-R2-01：未发布核心与配置候选能力 | **关闭** | 279–301 明确 InstalledPair 与 TrustedCandidate 两种信任来源；候选来自内置压缩制品 hash、私有解压、binary hash 和私有 receipt，绿装不依赖旧 pair；候选只可 version/validate，Commit 后重新取得 InstalledPair 才可 Run。ConfigCapability 绑定实际 D/staging 候选或 D/runtime/config.yaml，`-d` 固定 core-home，`-f` 消费选中的能力。301 明确 prepareContent→验证→相同字节提交→重新绑定→reload/回滚的接线。309–310 的真实类型/fake executor 回归能区别旧 binary 与新 candidate、不同配置内容以及失效能力。 |
| UBP-R2-02：报告在归档前被清理 | **关闭** | 491–525 将报告及 cleanup 清单放到 anchor 外的独立结果目录；原始事件和清单 root0600，汇总发布0644。trap 先保存证据，再回收资源及 anchor，最后更新外部 cleanup 状态；always 依据保留的清单核查/恢复、更新并上传，再清理结果目录。归档或清理失败不能转为成功；525 的 fake 脚本测试覆盖失败、TERM、二次恢复和证据保留。 |
| R2 非阻塞项：Unix 模型测试标签 | **关闭** | 137–144、190–191：直接引用 TrustedRoot/RootPolicy 的模型与安全测试限 Linux/Darwin；common 纯路径模型才全平台运行。 |
| R2 非阻塞项：策略正例缺 managed secret | **关闭** | 251–266：基线先通过 ParseDocument，显式提供固定合法 ControllerSecret，再检查策略正例和唯一危险字段的精确错误。 |
| R2 非阻塞项：PrivateFS 使用任意临时根 | **关闭** | 201–208：合法 fd backend fixture 与 T19 可信 anchor 分开，底层关闭计数为实际断言，不放宽生产祖先规则。 |

核心候选修订与现有代码的对应关系已复核：`internal/core/install.go:145–220` 在 Commit 前对新 candidate 执行 `-v/-t`，因此独立 TrustedCandidate 是必要入口；`internal/runtime/subscription.go:408–447` 生成随机配置候选，450–472 提交和回滚，所以 ConfigCapability 必须绑定选定候选，而不能总指向已生效配置。`internal/supervisor/command.go:18–35` 是独立的正式执行入口，R3 301 行明确为它注入 InstalledPair 与已提交配置的 factory。R3 没有依赖新增配置镜像，也没有让原始路径/调用者 hash 自行建立核心信任。

## R1 历史问题完整关闭核对

| 项目 | 结果 | 当前计划证据 |
| --- | --- | --- |
| UBP-R1-01：跨任务接口 | **关闭** | 81–118、130、317、331、361：daemon 双锁 lease 与 listener 借用；locator mode/expected owner 贯穿 verified dial；逐请求 credential；SourceReader 统计与集合 Finish/正常 EOF 门禁；generation uint64、runtime.Operation、身份绑定及两个跨包合同 fixture。 |
| UBP-R1-02：有效行为 Red | **关闭** | 28、167–191、249–267：可编译 scaffold、正常对照、单一危险变化和明确错误阶段；编译错误/更早拒绝不计作 Red。补齐 secret 与可信根 fixture 后，示例可用于证明目标行为。 |
| UBP-R1-03：Windows 与 Unix 构建边界 | **关闭** | 132–144、197、225、230–231、295：Unix capability/工厂/安装用例带明确平台标签，common 只含可跨平台类型与 backend 接口；旧 transport/runner/Windows adapter 保留；阶段编译与最终原生测试门禁明确。 |
| UBP-R1-04：core pair WAL、恢复与受管执行 | **关闭** | 275–312：core-local journal 的路径/schema/动作/恢复权威及所有者明确，不依赖 app；下载在提交区外，pair 发布和恢复有逐动作故障测试；两种核心能力及配置能力覆盖 Prepare、版本探测、验证、正式 Run 和失败保旧。 |
| UBP-R1-05：隔离安全 runner | **关闭** | 488–528：实际 tagged 命令、独立可信锚点、两个临时 UID、原生 ACL/peer/identity 测试、Linux mount namespace、精确测试名和 fail-on-skip、外部报告与 cleanup 合同齐全；32、475、523、547 明确 T18 仅接线，T19 全部必需结果通过才可交付合并。 |
| R1 小项：实际 runtime 入口名称 | **关闭** | 277、301、311 使用现有 BuildRuntimeWithOptions，未再依赖不存在的 app.NewRuntime。 |

## 全量一致性与执行门禁

计划保留 R4 的全部目标，没有通过推迟必要安全功能缩小范围。任务依赖先建立布局/能力/IPC，再完成 root typed policy、provider、核心可信性和导出，最后汇合安装恢复与默认入口；共同文件的阶段编译要求补足了并行任务的合流风险。

以下关键路径已明确纳入实施与验收：

- T01–T05：纯布局、默认/显式私有模式、Windows 旧入口、ACL/挂载/身份验证、双锁、socket 生命周期、peer 先于 token、每请求 credential 与原退出码。
- T06–T08：固定版本 typed registry 及正负 fixture、全部配置 mutation、受管资源、provider 调度/手动刷新/恢复、核心 pair 信任；Web 未允许的写路由继续拒绝。
- T09–T11：精确字节快照及预算、来源统计/complete/正常 EOF 后才发布、Unix v2 与 Windows/私有 v1、明确 TUI-only 选择、退出前等待 worker 并释放资源。
- T12–T17：逐动作写前 journal、恢复锁借用、systemd 持久 mask、launchd 四种状态、来源保留与必要资源完整、无业务验证子进程、activation 后仅 target、无服务 binary-only、TUI 清理后 Apply 与 shell 统一入口。
- T18–T19：root 系统/root 私有策略、客户端不写机器树、残余危险入口审计、原生 Unix 安全 job、Windows 回归及六目标 CGO-free 构建、文档和准确验证记录。

typed registry 的逐字段内容、平台 ABI 和实际故障恢复实现仍须按各任务落地并接受规定的安全审阅；计划已给出它们的来源、拒绝规则、文件归属、测试与阻止提前切换的门禁。因此其尚未实现不是本次计划的技术缺陷。本次 PASS 不表示这些实现或平台测试已经通过。

## 授权边界

**通过的是这份固定哈希执行计划的技术完整性与可实施性。** 用户当前授权仅覆盖计划编写与审核；生产实现、安全边界和持久化合同的最终批准、真实服务/订阅/core/用户数据迁移，以及 commit/push/PR/发布均不由本报告授予。计划第 13、26、32、547 行保留了这些边界，未将规格技术通过等同于产品或实施批准。
