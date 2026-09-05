# Unix Base Dir 设计独立评审 R1

- 结论：**REQUEST_CHANGES**。
- 审阅日期：2026-09-05。
- 代码基线：`2d00f61e720fa27f115dea52f7b4a95cc35a599f`，分支 `feat/system-data-root`。
- 审阅规格：`docs/superpowers/specs/2026-09-05-unix-base-dir-design.md`，170 行。
- 规格 SHA256：`6D57FFDAB3F842AFDC0854143E56A1772CC132E72D9A7B2630888904AF4B4FD0`。
- 同时审阅交接文档、AGENTS.md、CONTRIBUTING、当前 architecture 文档，以及 platform/control/logging/service/install/daemon/subscription 相关代码。缺失的旧总体架构 spec 未被当作依据。
- 这是文档的架构、安全和可实施性评审，未运行真实 Unix 服务或迁移，也未实现生产代码。以下代码位置均相对于该 worktree。

推荐保留 A 方向：系统私有业务与服务日志、按用户存储 TUI 日志、用户进程发布导出。方向本身合理，但目前的候选稿把关键协议与安全机制留到批准后定义，尚不满足交接文档第 6、7 节的可实施性要求。产品批准仍应在技术设计完成后由用户作出；本报告不要求用户先回答才能修订。

## UBD-R1-01 — 共享控制权缺少 root 输入边界证明

**严重度：Major / 安全阻塞**。规格行 79–89、115、136–137。

规格承诺所有本机用户可执行现有控制操作，同时断言这不允许任意 root 文件访问或代码执行，但没有定义可执行该承诺的边界。当前 `internal/control/server/subscription.go:66–75` 接受用户订阅 URL；`internal/subscription/document.go:15–38` 将内容解析为通用 YAML map，只检查尺寸和基本订阅形状；`internal/subscription/generator.go:18–46` 保留非托管字段，仅覆盖一部分端口、secret、UI、TUN 字段。`internal/runtime/subscription.go:400–442` 把生成内容交给核心校验。系统 root mihomo 将消费普通用户间接控制的配置；仅隔离 D 和只重建可信核心二进制不能证明输入安全。本项不宣称已证实某个上游利用链，但当前文本的安全断言没有依据。

**具体修订要求：**

1. 给出现有控制操作的权限矩阵，区分代理管理、可观察的机器流量/日志、服务安装/自更新、文件和进程能力；说明机器连接元数据本来就可被控制用户读取，不能只讨论新增日志读取。
2. 明确 root 核心消费订阅、provider、文件引用和其他有副作用配置的策略。提供字段类别及允许/拒绝/重写规则、路径约束与上游核心版本验证依据；在线新增/刷新/切换和迁移必须经过同一安全检查。
3. 若推荐以普通用户获得完整代理管理权为信任边界，应准确说明剩余能力及风险，不把未证明的 root 隔离当保证。不能通过取消普通用户无需 sudo 连接这一原目标来规避本项。
4. 明确该安全策略对合法既有订阅、迁移设置和 v1 请求失败语义的兼容影响及回归矩阵。

## UBD-R1-02 — Unix capability 与可信祖先规则仍不可落地

**严重度：Major / 安全阻塞**。规格行 78–97。

“逐段打开、验证 ACL、拒绝嵌套挂载”是要求，尚不是 Linux/macOS 可实施的算法。当前 `internal/platform/privatefs_unix.go:47–81` 首次打开根会跟随 symlink，随后直接 Fchmod；`102–125` 子目录也不是完整 owner/ACL/挂载验证。已有 `publish_acl_linux.go:13–36` 只接受一组本地文件系统并拒绝任何 access ACL；`publish_acl_darwin.go:15–48` 同样拒绝非空 ACL。这些 helper 不能未经论证就用于系统祖先，否则既可能漏掉继承 ACL，也可能拒绝正常系统目录。安装路径还必须穿越 `/usr/local`；不能假设每台 macOS 的此类祖先都禁止非 root 写入，也不能为满足表格修改任意现有祖先权限。

**具体修订要求：**

1. 逐类定义 bootstrap、只读 control discovery、D、U、migration source、publish target 所持 capability 与允许的创建/修复操作，给出 Unix 新适配器与现有 PrivateFS 的接口边界。
2. 分 Linux/macOS 指定无 CGO 的 owner、ACL、mount identity 检查机制及失败分类。说明 access/default/inherited ACL、mode 修复时机、硬链接检查和 fd/name identity 复查；不能只写“安全评审中验证”。
3. 列出可信系统路径别名和祖先规则，区分 OS 挂载边界与应用树内嵌套挂载，明确 Linux bind mount 的处理及不支持文件系统的可见失败。
4. 说明 `/usr/local/lib/mihari` 的祖先不可信时安装如何安全完成或明确拒绝，且不偷偷改 chmod/chown 或换掉默认路径。
5. 给出普通 UID 与 root 的可信用户根解析、缺失多级父目录的创建规则；不以 HOME/SUDO_USER 认定 owner，失败保留离线 TUI 的内存诊断能力。

## UBD-R1-03 — 显式覆盖模式的布局、权限与锁域不完整

**严重度：Major / 安全与一致性阻塞**。规格行 63–72、76–97、101。

默认 B/D 清晰，但 `MIHARI_DATA` 模式只定义 D，后文仍统一要求 root B、B/daemon.lock 和系统 bootstrap。非 root 便携 daemon 具体取哪个锁、token/socket mode、TUI 用 D 还是 U 都无法从本文唯一推导。此外两个不同 D 可配置同一个 endpoint：各自获得数据锁后仍可能删除另一实例 socket。当前 `internal/control/transport/unix.go:15–44` 正是无活动探测地删同名 socket，数据锁不能覆盖这个碰撞；`internal/platform/paths.go:82–87` 的 Absolute 还会按 Root 重建所有派生路径，可能丢掉拆分字段。

**具体修订要求：**

1. 给出默认 root daemon、普通默认客户端、非 root 显式便携、root 显式私有系统服务、待迁移旧 root 服务的完整行为表，包括 B/D/U、lock、token/channel/socket、owner/mode、创建者。
2. 明确显式隔离实例如何满足原“一机一个 daemon”目标的兼容例外，并保留原有 MIHARI_DATA 测试/便携能力，而不是靠未定义行为缩减范围。
3. 定义数据身份与 endpoint 身份的锁/占用检查，覆盖不同 D 同 endpoint、同 D 不同 endpoint、socket 别名、现存 live listener、崩溃遗留和锁文件替换；明确锁的获取顺序及关闭时只删除本实例 endpoint。
4. 定义各覆盖的关系、最终绝对路径固定化、Unix socket 路径长度/类型校验，以及显式 credential 的可信父目录、读写 mode 和 owner 条件。

## UBD-R1-04 — 机器日志快照缺少完整 wire 与导出合同

**严重度：Major / 协议和可实施性阻塞**。规格行 113–121、166、170。

推荐方向依赖尚未定义的 endpoint；“具体 DTO、流格式、字节上限、并发限制和错误表以后批准”不能支持当前技术 PASS。`internal/logging/export.go:45–58` 和 `cmd/mihari/main.go:205–210` 以一个 Paths/PrivateFS 读取三种日志；`internal/logging/snapshot.go:72–113` 在 shared lock 下打开文件并记录尺寸，然后锁释放，尚无远程总量预算。`internal/logging/export_json.go:21–42` 的 manifest 没有完整性字段。服务 HTTP 的 `ReadHeaderTimeout` 和五秒 Shutdown（`internal/control/server/server.go:39–43,112–139`）也不足以让慢消费者必然有界退出。

**具体修订要求：**

1. 选定 endpoint/method、capability 名、请求字段与校验、response content type/schema、流帧及成功终帧；定义部分帧后出错、EOF、取消、旧 daemon/未知 capability 的精确行为。
2. 给出单记录、扫描字节、输出字节、源文件数、并发、总时长、空闲传输/背压限额及超限错误；不能因新接口无总上限而将 private logging 的成本向所有本机用户开放。
3. 给出服务器 snapshot/redaction 与客户端多来源拼装接口。说明时间窗由谁固定、日志 rotation 的一致性范围、服务端 exact-secret 集合从何获得、客户端拿不到机器 secret 时如何安全二次脱敏。
4. 选定 manifest 兼容策略及固定 zip entries 对空来源、TUI-only、损坏行和能力缺失的具体表现，避免把未采集的来源记录成空日志。
5. 给出控制流取消/强制关连接/等待 worker 的有界关闭顺序。保持用户目标目录持有、no-follow、no-replace 和失败不发布，且新 endpoint 只暴露固定机器日志来源。

## UBD-R1-05 — 迁移 journal 没有可恢复状态机及激活提交点

**严重度：Major / 数据完整性阻塞**。规格行 127–142。

“Ready 前不开放 mutation，Ready 后不回退”仍有崩溃窗口：新服务开始接收写操作后、installer 尚未写 journal 成功状态时崩溃，恢复器无法区分是否能回退。现有 `internal/daemon/run.go:28–34` 在 listener 建立后立即关闭 Ready，甚至早于 Runtime.Run；它不代表业务校验成功或迁移激活。`internal/service/service.go:367–400` 仅等待这个 Ready。锁定安装事务也没有说明锁文件位置、与 daemon lock 的关系及恢复者如何在重启时取得权限。

**具体修订要求：**

1. 定义 journal 版本、最小字段、存放位置、机密字段保护、identity 校验、原子写及文件/目录同步顺序、未知/损坏版本失败语义。
2. 写出每阶段允许的持久化组合、崩溃后恢复动作与权威树，覆盖目录提交、二进制替换、服务定义更新、启动成功、激活记录及清理。
3. 选择 durable activation 提交点：在允许首个外部 mutation 前先持久化“不再自动回旧树”的决定，并保证 daemon 重启自行检查该状态；installer 不能只凭一次健康响应推断可回滚。
4. 区分 IPC Ready、业务健康/可恢复 degraded、版本身份验证、允许 mutation 四件事。定义未完成 onboarding/无核心的合法安装如何通过就绪检查。
5. 定义全局安装锁、数据锁、endpoint 锁、恢复进程的锁顺序；所有自更新/AIO/reinstall 共享同一事务边界，不能在停机前覆盖调用或服务二进制。

## UBD-R1-06 — 迁移白名单、来源静止和已有目标规则尚未明确

**严重度：Major / 数据完整性与安全阻塞**。规格行 57、131–144。

“明确业务白名单”没有列出实际白名单；“目标不可覆盖已有有效安装”又没有区分首次迁移与升级既有新布局，后者必然面对有效目标。当前 `platform.Paths` 含 subscriptions/catalog、cache、settings、onboarding、preferences、core-channel、GeoIP 和 web 等关联状态（`internal/platform/paths.go:40–70`，sidecar 见 `internal/runtime/manager.go:480`）。简单复制部分状态后以新 root 启动可能引用丢弃的 cache/resource 或旧路径。停止注册服务也不足以说明旧自动任务、旧 daemon 和 mihomo 句柄均结束；文档仅说“必须确保”，未提供受管升级的判定步骤。

**具体修订要求：**

1. 为当前 Paths 的每类文件列出复制/重新生成/重新下载/忽略/拒绝策略，含 token、controller/web credential、应用及 core channel、runtime config、catalog/cache 引用关系、panel active 与 GeoIP。列出总体尺寸、文件数和单文件限制。
2. 写出来源不是可信根时的 handle-relative 复制步骤；对被保留的数据做语义引用校验，并明确不会在验证阶段执行旧树 binary。
3. 指定 systemd/LaunchDaemon 的停机、阻止重启、等待受管进程树退出与重新核对方法及超时；不尝试以指纹把恶意用户 writer 证明为静止。说明来源 writer 无法排除时如何安全拒绝且恢复旧服务。
4. 区分无目标首次迁移、已提交新目标的后续升级、冲突目标、半成品 journal、源目标重叠与嵌套路径，给出每种权威与重试规则。
5. 明确成功保留旧来源如何被标记/记录而不再被二次导入，历史日志可供人工取回但不伪称新导出包含它们。

## UBD-R1-07 — 安装器入口及服务适配器缺少事务可执行接口

**严重度：Major / 可实施性阻塞**。规格行 70、125–129。

“shell 最终调用同一个 Go 事务”需要一个真实入口，但当前设计没有选择 CLI/JSON 契约，也没有定义候选包如何从下载进程交给特权事务。现有 `scripts/install/install-aio.sh:153–187` 直接 overlay 并调用 service；`internal/service/service.go:418–446` 只从当前进程构造服务配置，Controller 没有导出旧定义/恢复环境的能力；`internal/update/self.go:175–176` 仅在替换后回调，无法实现停机前保存/提交全部可执行副本的事务。

**具体修订要求：**

1. 选定普通安装、AIO、remote AIO、service install/reinstall、CLI/TUI self update 共同使用的 Go installer 用例及入口，明确其参数、退出码/JSON、是否 root、下载-only 和不存在服务时行为。
2. 说明特权进程如何验证非特权下载目录中的候选二进制、包清单/checksum、身份与信任来源，并在 root 私有 staging 内重新建立稳定候选，避免用户替换 staging 后被执行。
3. 为 Linux/macOS 定义服务配置读取、备份、停机禁重启、恢复、系统级注册接口及工具不可用时的失败；保留原 env/启动参数/运行和启用状态的准确范围。
4. 定义 PATH binary、受管 service binary、应用 channel sidecar 的提交顺序与回滚；包括只有下载、已安装但 stopped、未安装 self update、执行者正是待替换 binary 和 relaunch。
5. 默认服务不写 MIHARI_DATA 的选择正确，须与自定义布局的 endpoint/credential 持久化和旧 env 恢复测试一起固定下来。

## UBD-R1-08 — 共用接口和 credential 生命周期缺少明确的兼容闭环

**严重度：Major / 兼容与可实施性阻塞**。规格行 7、63–70、99–109、121。

仅声明 Windows 不变不足以指导对 Paths、PrivateFS、Client、logging.Export 和 service.Manager 的共用改动。`Paths.Absolute()` 会重建派生字段；进程级 `prepareLocalRoot` 同时保存 FS/token（`cmd/mihari/main.go:279–335`）；`client.NewHTTP` 和 `SetToken` 仍是固定 token 模型（`internal/control/client/client.go:34–56`）。服务端 token 也是启动时常量（`internal/control/server/server.go:18–31,51–61`）：只规定客户端重读文件，却不说明 token 在何时、由谁轮换及服务端如何切换，会出现全体新请求拿到新 token、现有 daemon 只认旧 token。当前 CLI 还会把若干客户端错误统一包装成 daemon_unavailable（`internal/cli/status.go:27`、`internal/cli/runtime.go:51`）。

**具体修订要求：**

1. 给出布局对象/构造函数及 bootstrap、client provider、TUI logger、export source、service adapter 的职责与所有权图，明确 NewPaths/Absolute/显式 MIHARI_DATA 保留哪些语义。Windows 选用旧路径/ACL/本地导出适配器，避免全局替换构造过程使其行为改变。
2. 选定 token 轮换合同：例如仅 daemon 重启时采用已验证新 credential，或另行设计原子双端激活；禁止只改文件却声称在线轮换已经支持。定义部分写入、损坏 credential、缺失后恢复、陈旧空值、request/stream 重连与缓存清除行为。
3. 给出统一错误分类的位置，确保 provider、dial、HTTP 认证失败在 CLI 和 TUI session 均保留第 107 行的约定，不被包装丢失；现有 v1 envelope 与退出码不变。
4. 明确 TUI 用户日志仍如何跟随 daemon logging 配置、离线采用何默认值、默认显示的日志路径和导出目录分别指哪一类；配置 DTO 中现有 Dir 不应误导成 TUI 本地路径。
5. 写出 daemon/TUI 的正常、初始化失败、运行失败和 self update/relaunch 各自资源关闭所有权，避免重复关闭共用 capability，且 worker 结束后才能释放其目录/快照句柄。

## 复审门槛

以上 8 项都需要在 spec 内形成可检查的候选合同，不能只移入“待产品批准”清单。可以把推荐方案完整写出并明确尚未授权实施；技术复审通过后再让用户批准路径、共享权限、新增协议/格式及兼容边界。复审将以新 spec 哈希为准，逐项检查修订及其内部一致性，仍不涉及生产实现或真实 testenv 操作。
