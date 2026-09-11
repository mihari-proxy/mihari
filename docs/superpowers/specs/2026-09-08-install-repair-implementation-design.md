# 三平台显式修复安装：实现设计

日期：2026-09-08。状态：用户已审核通过，正在实施；依据用户已确认的 [R5 策略](2026-09-08-interrupted-install-user-choice-design.md)。批准设计不表示实现或原生验证已完成。

**界面语言：** 用户后续明确要求保留产品全英文特性。所有 TUI/CLI/脚本提示、按钮、帮助、确认输入和错误消息均使用英文；本文中文描述说明语义，不作为产品显示文案。

## 1. 范围与取舍

三平台的机器服务统一采用“检测 → 用户选择 → 新安装操作”，不自动重放中断事务。便携前台和无服务的 binary-only 更新保留独立边界，不因为检查版本而创建机器安装目录。正常配置提交的失败回滚保持原行为。

选择一个受保护的当前安装记录，其状态只有 `applying`、`complete`。它说明安装资源是否完整，不记录每个外部效果的恢复步骤。服务运行状态从平台及控制面查询，不持久保存一个可能过时的 ready 布尔值。macOS 的运行组登记解决安全停机，属于不同职责，保留一份 generation 和永久 gate 的方向；R4 的 pending source 启动授权不实施。

## 2. 安装控制目录与持久记录

| 平台 | 控制目录 K | 权限与锁 |
| --- | --- | --- |
| Linux | 固定 B 下的 `install-control`，B 为 `/var/lib/mihari` | K root0700、文件0600；先取得已有 B/P install lease，再取得 K 永久 `operation.lock`。 |
| macOS | 固定 B 下的 `install-control`，B 为 `/Library/Application Support/mihari` | 同上；停止/启动交接再取得 launchd gate，之后才是 data/endpoint。 |
| Windows | OS Known Folder ProgramData 下的 `Mihari/install-control` | 从系统目录能力取得路径，不能信任调用者 ProgramData 环境变量；K 与文件仅 SYSTEM/Administrators 可写读，拒绝不可信重解析点；永久 `operation.lock` 使用持有文件句柄的排他锁。 |

K 与用户业务根 D/P 分离；自定义安装路径和 private 服务仍使用该机器固定控制目录，不通过环境变量创建第二把机器服务锁。Windows 保持原数据根、服务账号和 named pipe 语义，不迁移到 ProgramData。该新增目录只存安装控制资料。

目录首次创建以同级临时目录完整初始化后原子发布，包含 `operation.lock`、Windows 的 `startup.lock` 和 `state.json`；永久锁不删除。仅可信未安装或明确采用旧布局的服务允许显式入场初始化。真正未安装才可发布 uninstall/complete；已有旧实例首次入场直接发布绑定旧实例范围的 applying，不经过“未安装”中间状态。已存在 K 却缺少/损坏状态文件一律 unknown，不自动补空记录。没有新记录的旧安装先做 §8 的旧格式识别，不默认视为中断。

`state.json` 使用 schema `mihari.install-state/v1`，上限 64 KiB；严格类型、有界字符串、拒绝未知/重复键、完整 EOF。字段如下，所有路径仅是重新获得平台能力时的定位信息，不授予写权限：

| 字段 | 类型与约束 |
| --- | --- |
| `schema` | 固定字符串 `mihari.install-state/v1`。 |
| `id` | 当前安装操作 ID，32 个小写十六进制字符；初始化也分配 ID。 |
| `state` | `applying` 或 `complete`。 |
| `operation` | `install`、`update`、`repair`、`fresh`、`uninstall`、`migrate`；首次可信未安装记录使用 uninstall/complete。 |
| `owner` | applying 时为 `{boot_id,pid,start}`，pid>1，沿用平台规范身份；complete 时为 null。只用于诊断，锁是否被持有才是并发依据。首次 Windows 初始化尚无 boot key 时 boot_id 可空，获得已发布 K 的 startup gate 后补齐再停机；空值从不作为跨 boot 证据。 |
| `base` | null 或上次可验证的完整安装 Manifest；不嵌套记录。 |
| `target` | 本次目标 Manifest；首次未安装或卸载为 `installed=false` 的 Manifest。 |
| `data_policy` | `retain` 或 `reset`；reset 只可由 fresh 的显式删除确认生成。 |
| `reset_entries` | 最多 32 项相对 D 的受管理顶层项目，retain 时为空；不存任意绝对删除路径。 |
| `supersedes` | null 或旧记录的 `{id,sha256}`，用于检测/审查接管，不需要递归读取旧事务才能运行。 |
| `source_scope` | null 或迁移来源 `{data_root,data_identity,selection,evidence_sha256}`；selection 为 service_definition/explicit_request/legacy_v1，摘要绑定规范选择证据。源范围只保护、不作为 reset 权限；跨重复 repair 和 complete 保留，不递归增长。 |

Manifest 字段：`installed` 布尔、`data_root`/`install_root`/`endpoint`/`credential` 的规范绝对位置或平台端点、`data_identity` 为 `{boot_id,key,marker}`、`binary` 为 `{path,sha256,version}`、`definition_sha256`、`enabled`、`run_after_install`。installed=false 时 binary=null、definition_sha256 为空；其余路径保留所选实例范围，不代表要删除数据。首次真正无实例时允许空路径，不能用这份 Manifest 进行业务文件写入。

定义 hash 来自受支持服务定义的规范内容，包括 argv、固定数据/端点绑定与服务账号，不含瞬时 PID/running；环境只允许既有定义白名单，不序列化 secret。enabled 单独作为期望服务策略，实际开关不进入定义 hash，避免临时启用使正常启动被自己的完整性检查拒绝。Unix data_identity 复用现有同 boot identity/跨 boot 私有 marker 语义；Windows 使用受保护根 handle 的 identity 和个人主体 SID 校验，不能把旧 inode/文件 ID 单独当作跨 boot 授权。无法校验旧根时拒绝修复/删除，不对用户目录自动改变 owner。

Manifest 还必须显式包含 `data_parent_identity`，类型为 null 或 `{path,identity,relative}`；它是 `target`/`base` 内的 Manifest 字段，不是 state.json 的另一个顶层字段。`path` 是最近可信存在父目录的规范绝对路径，`identity` 使用上述身份结构，`relative` 是从该父目录到尚未创建的数据根的规范相对路径。

首次安装且 D 尚不存在时，applying 的 target.data_identity 允许 null，但 target.data_parent_identity 必填；没有该绑定不能创建。建立根后、写业务内容前，持锁更新同一 applying 的根 identity，将 data_parent_identity 置 null 并同步。complete 且 installed=true 不允许空 data_identity。修复已有实例禁止将无法确认的旧根降级成“尚未创建”，确认摘要同样绑定根不存在这一条件。

同目录临时文件写入、文件同步、身份约束原子替换、父目录/平台等价持久化成功后，才确认记录提交。`Published` 与 `Durable` 分开处理。daemon 接受 complete 前，须通过 held file/parent 能力确认该匹配版本的持久性：完成必要同步并重读版本；失败拒绝入场。它不修改记录内容、不写 complete、不取得安装操作锁。这样不能仅凭 rename 后可见、尚未同步的 complete 运行，然后断电回到 applying。Windows 原生持久化保证必须用受支持实现及故障注入核实，不以跨编译替代。

### 2.1 Windows 原生记录提交

使用 OS KnownFolder 确定 K，持有 K 目录句柄；通过句柄取得规范路径、volume identity 和 file identity 并检查 ACL/reparse。`LockFileEx` 持有永久 operation/startup 文件的排他字节范围；只读检测以已有文件探锁后立即释放，不能新建。新格式首版的 Windows 原生验收范围为本地 NTFS 安装控制和安装资源卷；不支持的文件系统/远程路径在停机前拒绝，不静默弱化持久性。此限制须随本设计审核，不改变 portable 路径。

记录发布顺序：创建同 K 的独占临时文件 → 完整写入并 `FlushFileBuffers(file)` → 重验旧目标 identity/hash → `SetFileInformationByHandle(FileRenameInfo)` 相对持有 K 进行替换 → 再同步目标文件 → 对从持有目录解析并核对的 volume GUID 句柄调用 `FlushFileBuffers(volume)` → 重读 state 的 id/hash。安装程序、数据和注册表资源须先完成各自同步，再发布 complete；跨卷时先同步所有变更卷。SCM 变更后在对应 SYSTEM hive 执行 `RegFlushKey` 并重新查询定义，不能只依赖 ChangeServiceConfig 返回。

微软提供文件句柄重命名和管理员卷同步能力；本设计据此选用“受保护命名空间替换后同步卷”的提交方案，原生故障测试仍须验证具体 adapter，不能从 API 存在推导测试已通过。既有 publishNoReplaceLocked 的 Flush 失败仅 warn，不直接复用为成功提交。任一同步失败返回 Published/Durable 的实际结果，停止后续副作用。daemon 的 complete 确认同样核验并同步 K 所在卷；只读 status 不执行卷同步。[句柄重命名](https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-setfileinformationbyhandle)、[卷同步](https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-flushfilebuffers)、[注册表同步](https://learn.microsoft.com/en-us/windows/win32/api/winreg/nf-winreg-regflushkey)。

## 3. 检测分类

app 提供 `InstallationStatus`：`kind` 为 `not_installed`、`in_progress`、`interrupted`、`installed`、`unknown`、`permission_required`；另有 `service_state`、`start_failed` 和无 secret 的 `reason`。kind 和服务状态是两维，不增加持久的 UI 状态。

1. 只读打开固定 K，探测已有永久锁；检测不创建 K/锁文件、不修改记录。
2. 有有效持锁者则 in_progress，不依据时长或单个 PID 宣告中断。锁探测失败与权限失败分别 unknown/permission_required。
3. 未持锁时，严格读取并验证记录；有效 applying 为 interrupted，complete 且目标已安装为 installed，完整卸载为 not_installed。程序/定义等安装资源验证失败为 unknown，reason=resource_mismatch；不由网络连接失败推导中断。未知原因不能授权删除，只有资源身份仍可独立验证的损坏安装才允许生成修复计划。
4. complete 的内容、binary、定义与 data root 身份验证通过后才检查服务/Ready；不把用户配置语法或兼容性错误视为安装资源不完整。`start_failed` 只在执行本次启动尝试的 outcome/error 中报告；独立 status 查询固定 false，不持久保存或采信历史 OS failure。正在运行而当前 Ready 失败时查询返回 installed、reason=service_not_ready；stopped/disabled 仅陈述当前状态。run_after_install 仅描述安装后的首次启动要求，不用它推断用户后续主动 stop 是故障；服务启用策略偏差以 reason=service_policy_mismatch 单独报告，不自动变为 interrupted。
5. 用记录 id/hash 和锁再次观察排除读取期间开始的新操作。UI 只消费快照，执行时再核验，不将状态查询持有的锁跨越用户交互。

若 complete 在活跃 installer 中已持久提交，daemon 可走正常启动，UI 暂显示 in_progress 直到启动尝试结束。这不阻塞 daemon 等待 installer Ready；daemon 不等待 operation.lock。

## 4. 显式修复与全新安装

安装执行统一位于 app 用例中，CLI/TUI 仅请求该用例；service 包只执行 OS 服务操作，platform 提供可信路径、进程、锁与原子 IO。三平台脚本只负责下载/验证新程序并调用它，不分别实现文件替换/恢复状态机。

准备阶段验证新制品、解析当前实例和生成计划，无停机/数据删除副作用。计划必须绑定记录 id/hash、所选根 identity、目标 binary hash、服务策略及删除范围。全新安装的二次确认使用该计划摘要；根或范围变化时计划失效，重新展示，不能沿用确认。

执行步骤：

1. 获得管理员权限和平台安装/operation 锁，重新读取旧状态。repair/fresh 是可接管 interrupted 的入口；普通 start/restart/update/install 不代为恢复。
2. 把旧 state 原始字节保存为 K 内 `previous-state.json`（仅固定一份，身份约束原子替换并同步）；此时 state.json 仍是权威，崩溃不发生接管。旧 Unix v1 journal/metadata 原位保留，不在此步修改。归档失败则不继续。
3. 用旧 applying 中已验证的实例范围及新完整制品构造新 applying，以 supersedes 绑定旧记录；不执行旧 action、不复制旧业务快照。base 只保留最近一次完整 Manifest，新的 target 绑定当前修复选择，不递归增长历史。新 applying 持久成功后才允许停机或内容写入。
4. 暂时阻止 OS 自动拉起旧服务，停止并证明实际受管进程树退出，然后获取 data/endpoint 权限。macOS 按共享组/generation/gate 方案处理；Linux/Windows 必须使用各自的树所有权和退出证据，不能只相信 service status=stopped。未知或残留拒绝继续。
5. repair 不修改用户业务数据，使用保留的 D/P；fresh 只清理获批的受管理条目，再创建默认数据。部署新程序及受支持服务定义、完成安装资源校验。当前进程只能清理自己持有的临时对象，报错后不自动进入旧事务恢复。
6. 将完整 target 写入 complete、owner=null，并完成持久同步。此时只允许服务启动/核验和非业务临时文件关闭，不能再追加未包含在提交中的程序、定义或数据内容变更。
7. 释放 data/endpoint 和启动 gate，再按 run_after_install 启动并验证同实例控制面。operation 锁可以保留到本次有界启动尝试结束；daemon 不需要它。Ready 失败返回“安装完成，服务启动失败”，记录保持 complete，不自动重装、不回写 applying。再次 start 只执行启动。

启用/禁启的最终策略必须在 complete 中绑定。平台若为一次启动而临时 enable 再 disable，必须把这种短暂动作明确封装在服务启动适配器中；执行者崩溃留下的不一致应报告服务策略不符，不能因策略已写入 Manifest 就视为实际完成。正常下次 start 在用户请求内收口该运行策略，不恢复安装内容。禁止运行期用 Manifest 的 enabled 值扩大 startup 权限。

新修复再次中断时，检测只看当前权威记录及运行者，提示同样两个选择。previous-state 不是自动回滚依据；记录丢失/损坏时保留全部证据并报 unknown，不用归档偷偷恢复。修复必须保持原 D/P，迁移中存在两个候选数据根且无法确定哪一个是当前用户数据时，拒绝自动挑选；显示数据位置供后续独立迁移处理。

普通 install/update/reinstall 使用相同 applying→complete 提交边界，只允许从可信完整/未安装状态发起，且不隐含 reset。uninstall 保留现有公开行为：在 applying 后安全停机、移除服务定义/注册，保留安装文件及用户数据，再提交 installed=false 的 complete；不执行首次启动步骤。此时 binary=null 表示不再将残留程序视为受管理启动资源，不表示该文件已删除。若卸载中断，两个重新安装选择仍指向原保留的数据实例。已有明确授权的迁移操作仍需自己的源/目标身份与数据提交验证；这套状态不授予新的迁移权限，repair/fresh 不自动继续未完成迁移。

### 4.1 数据范围

retain：不删除/覆盖 `mihari.yaml`、`onboarding.json`、`subscriptions`、`preferences`、`logs`、`logs-export`、用户导入文件和未知条目；不回放旧 data publish/restore。损坏配置保留，明确报告其错误。程序运行后的正常写入不属于安装清理。

reset 默认允许 D/P 中这些固定项目：`mihari.yaml`、`onboarding.json`、`subscriptions`、`preferences`、`runtime`、`logs`、`logs-export`、`bin`、`geoip`、`web`、`staging`。逐项通过平台能力核验；计划展示准确路径和类别。原子移除/重建固定条目，不递归删除 D/P 本身及永久锁。未知顶层项默认保留并说明，因此“全新安装”表示重建受管理状态，不表示任意抹除整个目录。内嵌链接只删除链接本身，不跟随至范围外；不修改硬链接目标内容。

control token 在 repair/fresh 中均不删除，预览明确保留该本地认证资源；缺失 token 只由原 credential 所有者按既有身份规则建立，安装器不越权读取/轮换。web credential 位于已列入 reset 的 `web/credential`，fresh 随 web 条目移除，下一次正常建站由原所有者重建；repair 保留。若自定义 credential 与 reset 条目重叠，则拒绝该删除计划，不扩大范围。Unix U 日志与机器 D 分离，本次机器 fresh 不删除 U、其他用户或迁移来源；Windows 当前 TUI 若占用所选日志，先按已有 prepared-update 模式 join 并关闭日志/FS，再执行，不从仍运行的 TUI 中直接清目录。

### 4.2 Windows 运行树身份与停机

新增 K 内永久 `startup.lock` 和 `runtime-windows.json`，权限同 K。后者 schema 为 `mihari.windows-runtime/v1`，字段全部必需：`schema`、`installation_id`、32 位小写 hex `generation`、`boot_id`、`pid`、十进制字符串 `creation_filetime`、`binary_sha256`、`job_name`、`state`（active/empty）。它只证明运行树归属，不记录安装恢复步骤；由 daemon 在启动 gate 中登记，由 installer/daemon 在同一 gate 中确认空树后写 empty；与 data/E 锁顺序一致。

boot_id 使用受保护的 HKLM 64-bit `SOFTWARE\Mihari\InstallControl\BootSessions` 下唯一 volatile 子键的 32 位随机名称。父键 SYSTEM/Administrators ACL；在 startup gate 下通过 `RegCreateKeyEx(REG_OPTION_VOLATILE)` 原子创建子键，不依赖后续写 value；已有多键/非法键/ACL 错误拒绝，不删除重建。普通 status 只读；首次具备权限的安装/daemon 入场才初始化。volatile 信息在完整系统关闭卸载 hive 时消失；快速启动可能保留，因此只能保守沿用同一 boot_id，不能把一次注销/快速关机当成旧树退出证据。这是额外的 Windows 控制状态，不是业务数据根迁移。[volatile key 语义](https://learn.microsoft.com/en-us/windows/win32/api/winreg/nf-winreg-regcreatekeyexw)。

新 daemon 在任何业务和 child 之前：取得 startup gate → 验证 complete/旧 generation → `CreateJobObjectW` 创建 `Global\Mihari.Runtime.<generation>`，SYSTEM/Administrators ACL、不可继承句柄，名称已存在则拒绝 → 配置 KILL_ON_JOB_CLOSE，禁止两类 breakaway → `AssignProcessToJobObject(GetCurrentProcess())` → `IsProcessInJob` 验证 → 将 SCM PID、进程创建 FILETIME、boot_id、binary hash 与 job 名写入 active 并同步 → 重验 complete、取得 data/E → 释放 gate 后进入业务。daemon 自身先入外层 Job，后代创建自然受约束，消除现有 child 启动后才入 Job 的窗口；不借 WMI 或 breakaway 派生受管子进程。已有 core 子 Job 只有在保留外层归属且平台支持时使用，失败在业务前退出，不降级绕过。[Job 归属与后代](https://learn.microsoft.com/en-us/windows/win32/procthread/job-objects)。

installer 持有 operation→startup gate 后：SCM 读取服务 PID → OpenProcess 持有查询/同步句柄并通过 GetProcessTimes 核对创建身份、程序和 SID → 重查 SCM PID → OpenJobObject 持有 QUERY/TERMINATE 句柄，核对登记与实际成员关系 → 持久禁启 → 请求正常停止，必要时仅向已确认的 Job 调用 TerminateJobObject → QueryInformationJobObject 的 ActiveProcesses 为零，并确认所持 daemon process handle 已退出 → 同步登记 empty → 才获取 data/E 和修改资源。不是等待 Job handle 一般性 signaled，也不是只收到一次通知就视为空。[ActiveProcesses 含义](https://learn.microsoft.com/en-us/windows/win32/api/winnt/ns-winnt-jobobject_basic_accounting_information)。

若 daemon 已消失但 Job 仍可打开，以可信 active 登记绑定的同名 Job 查询并收口，不信任复用 PID。若同 boot active 对应 Job 无法打开，不能由“名称不存在”证明所有进程退出：保持现场并要求完整系统重启；新的受保护 boot_id 必须确实不同，且此时 SCM/身份复核无旧树，才允许跨 boot 收口。正常 daemon 退出须先关闭全部业务/child，再持 gate 验证 Job 只剩自身并写 empty；新启动仍需确认旧 daemon 的持有进程句柄退出，empty 不允许双 daemon。这个保守错误路径可能要求重启计算机，但不触发数据清除。

daemon 的退出清理对 startup gate 只做非阻塞尝试：若 installer 已持 gate，daemon 完成业务/child join 后直接退出，保留 active，由持有 Job 的 installer 确认空树并写 empty；不能等待 installer 释放 gate 而使正常停机互锁。

## 5. 公共入口提案

新增命令，不改变既有 `service reinstall` 为清数据操作：

```console
mihari service install-status --json
mihari service repair --binary <absolute-path>
mihari service fresh --binary <absolute-path>
```

repair 使用指定的新完整程序；省略 --binary 时使用当前执行的新程序，不能强依赖旧 ImagePath。fresh 在交互终端展示清单并二次确认；非交互必须提供 `--yes --plan-sha256 <digest>`，摘要来自新增 `service install-plan --mode fresh --binary <absolute-path> --json`。计划计算与执行都基于同一 app 用例。repair 不要求清数据确认，但必须是显式命令/按钮；普通 reinstall 遇到 applying 返回提示并指向 repair。

repair/fresh/install-plan 均接受 `--enable=<bool>`、`--start=<bool>` 并将结果写入确认计划。未指定时：中断安装继承其 target 的期望策略，完整/legacy 安装采用可验证的当前策略（运行中为 start=true，主动停止为 false），真正新装默认两者 true；unknown 不猜测。平台为安装临时禁启的观察值不能覆盖中断记录中的原期望策略。

新增 JSON schema：`mihari.install-status/v1`（kind/service_state/start_failed/reason/id）、`mihari.install-plan/v1`（mode/instance/candidate_sha256/preserve/delete/plan_sha256）、`mihari.install-outcome/v1`（installation_complete/service_state/start_failed/id）。错误沿用 `mihari.error/v1` 和既有 code/退出码映射；启动失败必须非零退出，错误 details 增加 `installation_complete:true`、`installation_id` 与 `reason:service_start_failed`，不同时输出成功 outcome；也可通过安装状态查询得知内容已完整。schema 字段应由明确 Go DTO 定义，不能对错误 message 做字符串分类。

CLI 的 plan/执行摘要使用同一明确字段 DTO 的固定字段顺序序列化后 SHA256（UTF-8、无多余空白；路径先由平台规范化），包括 mode、记录 id/hash、根 identity、候选 hash、服务运行/自启策略、固定删除项；不含时间戳或 live health，避免仅刷新状态就使确认失效。plan 不是提权或身份授权，执行仍重验所有资源。

新增本地只读 `GET /v1/install/status`，返回脱敏 InstallationStatus；不返回私有路径、metadata、hash 或删除清单。复用 named pipe/Unix socket 认证，不新增 TCP，不增加远程写端点。运行中的 daemon 可通过 app 注入的只读安装检查器提供该结果。

TUI 正常情况下走 control/client；daemon 离线时，只有已具备权限的安装检查用例可读取 K。普通用户无权读取时显示 "Administrator privileges required to check installation status"，用户手动进入提权的 TUI 或执行管理员 CLI，再展示确实检测到的中断弹窗。不自动弹 UAC/sudo、不放宽 K 权限、不让 TUI 直接解析受保护业务文件。启动故障时不依赖这个端点才能修复，独立新程序具有相同 app 检查能力。

安装脚本的非交互默认遇到 interrupted 即退出并提示显式 repair/fresh；已有 unattended install 参数不隐含 reset。两种新模式都调用已验证新程序的上述入口；Windows 不再先调用旧程序 uninstall 或直接 Move-Item 替换后才记状态。

### 5.1 Wire 契约与错误映射

三个新 JSON 对象字段全部必需，不使用 omitempty；字符串无值用空串、列表无值用 []。对象只在表中明确允许时为 null。status 和 outcome 上限 4 KiB，plan 上限 64 KiB；路径上限 4096 UTF-8 bytes、reason 上限 64 ASCII bytes，sha256 固定 64 小写 hex，id 固定 32 小写 hex 或未建立记录时空串。读者不得依赖字段顺序，只有确认摘要的内部规范序列化固定顺序。

| Schema | 字段及类型 |
| --- | --- |
| mihari.install-status/v1 | schema:string，kind:string，service_state:string，start_failed:bool（独立查询固定 false），reason:string，id:string。 |
| mihari.install-plan/v1 | schema:string，mode:repair/fresh，instance:object，candidate_sha256:string，preserve:Entry[]，delete:Entry[]，plan_sha256:string。 |
| mihari.install-outcome/v1 | schema:string，installation_complete:bool，service_state:string，start_failed:bool，id:string；只在命令成功时作为 stdout 结果。 |

Entry 为 `{path:string,category:string}`；category 仅 config/subscriptions/preferences/logs/runtime/binaries/assets/web/staging/credential/unknown。instance 为 `{record_id,record_sha256,data_root,data_identity,data_parent_identity,source_scope,enabled,run_after_install}`：前三项 string，enabled/run_after_install 为 bool；其余对象同 §2，尚无根时 data_identity=null，已有根时 data_parent_identity=null。data_parent_identity 的完整结构为 `{path:string,identity:{boot_id:string,key:string,marker:string},relative:string}`。没有旧记录时 record_id/hash 为空；source_scope 可 null。plan 摘要覆盖这些 instance 字段、mode/candidate/保留与删除清单，不把 plan_sha256 自身计入摘要。仅已提权 CLI/TUI 安装用例可获得 plan。

service_state 只接受 running/stopped/not_installed/unknown。reason 只接受空串及 installing、operation_interrupted、record_invalid、resource_mismatch、permission_required、service_policy_mismatch、service_start_failed、service_not_ready、legacy_record_absent、migration_incomplete、process_tree_unproven；新增含义以后按兼容增加处理，客户端未知 reason 显示一般状态，不能据其授权操作。

| 场景 | status 查询 | 安装/启动执行 |
| --- | --- | --- |
| 未安装/完整/活跃/中断/可信读取但记录损坏 | 返回对应 kind 的 status；HTTP 200、CLI ExitOK。unknown 表示状态不可确认，不是安装成功。 | 活跃、中断下普通操作、损坏记录或摘要已变更返回既有 invalid_state。 |
| 已授权查询用例读 K 权限不足 | permission_required status；HTTP 200、CLI ExitOK，CLI 不因查询本身先强制提权。 | permission_denied，既有权限退出码。 |
| IPC 未认证或客户端连不上 daemon | 认证失败沿用原 HTTP/error；连不上沿用 daemon_unavailable，本地有权限可转 inspector。 | 不捏造 interrupted。 |
| 非法参数/取消/发布 IO 错误 | 参数错沿用 invalid_argument；unexpected IO 返回原标准 error。 | 沿用现有分类；取消保持现场，无删除授权升级。 |
| complete 后首次 Ready 失败 | 后续独立查询仍 installed，查询当下 stopped/运行健康另行表达，不保留历史失败标志。 | 非零退出；mihari.error/v1，code=invalid_state，details={installation_complete:true,installation_id:<id>,reason:service_start_failed}。 |

示例：中断查询返回 `{"schema":"mihari.install-status/v1","kind":"interrupted","service_state":"stopped","start_failed":false,"reason":"operation_interrupted","id":"0123456789abcdef0123456789abcdef"}`。该查询成功不意味着允许无确认执行；新命令的失败和权限仍使用原退出码表。API 没有新写权限，也不改变旧 service status 契约。

## 6. 普通启动与错误边界

安装内容是否可信先由 app reader 验证新 complete Manifest；daemon 在 data/endpoint 权限取得前后核对同一 id/target hash，必要的 complete 持久性确认在进入业务前完成。macOS 另在 gate 中核验旧组、登记当前 generation。登记或持久同步失败时，不创建 credential、不恢复业务、不派生 managed child。

新 complete 是新安装控制模式的启动依据，不伪造旧 v1 phase，也不向通用 Manager 注入“允许任意 pending”的 bool。app 在新 reader 通过后使用正常已安装启动路径；旧 journal 不再作为同一实例新模式的第二个 phase gate。切换由固定 K 中已校验的新 complete 明确决定，不能仅靠命令行隐藏标记选择。

applying 下禁止新业务 daemon 启动，validation 仍是 installer 持有的独立无业务能力；运行中持有 data/E 的 daemon 不因另一个安装器刚写 applying 就丢失清理能力。installer 仍须实际停机、join 后才能写数据。

只读检测永不直接返回“必须删除数据”。unknown、permission_required、interrupted 及 service_not_ready/service_policy_mismatch 分别展示对应状态或诊断；本次启动失败由执行 outcome/error 展示，不在独立查询中重建历史失败标志。均沿用既有稳定错误分类，不能把损坏记录当成一个可直接覆盖的合法 applying。

## 7. 包边界与实现文件

- `internal/app/installation_state.go`：DTO、纯分类器、计划绑定与安装结果；`installation_inspect.go`、`installation_repair.go`：只读检测及新安装编排，不含表现层交互。
- `internal/platform/install_control_{unix,windows}.go`：K 路径/ACL/原子 IO/永久锁，使用小接口注入；`internal/service`：平台服务定义、进程退出与启动，不自写业务数据。
- `cmd/mihari/{unix,windows}_layout.go`：组装 inspector/repair 用例及正常 startup；统一 Windows 脚本、self-update、service Manager 入口，不能留下写文件的旁路。
- `internal/cli/service_installation.go`：新增命令与 DTO 输出；`internal/control/{protocol,server,client}`：新增只读 status；`internal/tui`：状态消息、单次提示、两种安装选择及退出清理后执行。
- `internal/integration`：真实 app reader/编排/Manager，fake 仅替换 OS/IO/下载边界；`scripts/install`：调用新程序；`scripts/test`：中断注入及三平台证据。

## 8. 旧版本与迁移

旧 Unix v1 complete+target 并通过原始 binary/layout 绑定的安装，可在用户执行下一次安装操作时建立新 Manifest；v1 pending 只检测/提示，repair 不调用 RecoverLocked。旧 v1 source complete 只有旧定义/旧 hash/布局有充分证据才允许显式 repair 选择该数据根，不为其创建自动恢复启动例外。旧 `service apply` v1 请求结构不增加字段；新格式入场后旧 v1 recover 对该实例返回“安装记录版本不支持，请使用 repair”，不能忽略 K 再恢复旧日志。这是必须审核的兼容性限制。

旧 Windows 无记录不能可靠追溯曾经中断过哪一步：若旧服务与程序/数据根一致，显示 legacy 已安装并允许显式采用新记录；如果不一致显示安装状态无法确认，使用新程序 repair 验证固定实例后修复。不假称历史旧安装也有完整中断检测覆盖。

`repair` 也接受可验证但没有 K 的旧安装；查询显示 installed/reason=legacy_record_absent。首次入场先双重核验并合成可信 base，发布 repair/applying、supersedes=null，再持久禁启并重读确认，之后停止并证明整树退出，最后才修改程序/定义/数据。在禁启尚未持久前中断，旧 executable 仍可能启动，但此时只允许完全未修改且匹配 base 的旧实例；新 executable 看到 applying 始终拒绝业务启动。禁启持久后，替换全过程不得重新自动启动旧程序。这个首次采用窗口不授予新程序 pending source 权限。

Linux fence 是受支持定义的持久 mask/禁启并重读，macOS 为持久禁启该 label 并重读，Windows 为 SCM start type Disabled/关闭自动重启路径并重读；只有 fence 完成且整树退出后才能改内容。无法取得 legacy 整树退出证据时保留 applying 和旧内容，要求先完成可验证的跨 boot 隔离或既有独立卸载流程，不仅凭用户表示“已重启”继续。新记录不是该退出证据的替代品。

### 8.1 迁移接管

新迁移仍需既有显式授权，统一由 new app 编排：验证来源选择证据与两端能力 → 保存并绑定旧 v1 原始 hash → durable migrate/applying → 安全停机 → 执行本次明确迁移和资源发布 → durable complete。首次 legacy 的 base 可由旧服务合成；纯显式数据来源的 base 可 null，但 source_scope 必填。K 发布后 v1 apply/recover 不能另行写该实例，旧 journal 仅作只读证据。

repair/fresh 接管带 source_scope 的记录必须原样继承该范围：repair 只重新部署 target 的安装资源，保留两根现存数据，不继续复制来源或恢复旧快照；fresh 的删除范围只能位于 target，且不得与 source 相等、互相包含或经链接重叠。target 尚不存在可按已绑定父目录建立，身份未知则拒绝。目标中仅部分迁移的数据可能不足以启动，须明确提示原来源保留、需要单独选择迁移；不能把保留数据解释为已经补齐数据。source_scope 在 complete 后仍保留作保护/来源记录，不由其推导删除权限。

## 9. 验收及审核重点

必须先 RED 再 GREEN：活跃/中断/未知分类；complete 与 Ready 分离；普通查询零恢复副作用；repair 保留数据及重复中断；fresh 精确删除确认/越界拒绝；旧程序损坏时独立安装；旧格式及无权限 fallback；所有生产写入口统一；三平台停机和正常重启。

故障注入覆盖 applying 发布前后、停止旧进程、reset 部分完成、程序/定义部署、complete 的 rename 和 sync、锁释放、首次 Ready、再次掉电。complete 同步失败不得执行业务；已 durable complete 但启动失败不得回到 applying；重复 repair 不自动 reimport/restore 用户数据。

本稿的审核点是具体新增持久 schema/权限、CLI/只读 API、旧 v1 recover 的新模式拒绝策略、完整提交先于 Ready，以及 reset 的准确范围。用户已批准 R5 的产品方向，不需要重新选择那五项边界。新具体契约获批后才能按 [实施计划](../plans/2026-09-08-install-repair-implementation-plan.md) 写代码；不在本稿批准前部署或操作真实服务。

## 10. 本轮独立审计结果

subagent 审计提出的 legacy 首次入场、Windows 原生停机/提交、启动失败证据、迁移两端范围、认证资源及 wire 契约六项均已落实决策。限定复核又指出卸载不应改变保留安装文件的公开语义、独立 status 不应重新引入历史启动失败，两处已修正文案。该审计属于设计一致性检查，不是 Go 实现、真实系统服务或断电测试通过证明。

审核需特别看到新增 Windows 的受保护运行登记/volatile registry boot session、本地 NTFS 原生验收范围，以及极端进程证据缺失时要求完整重启的限制；这些不是用户此前对两个按钮的确认所隐含批准的技术边界。本次保留用户审阅后再实施。
