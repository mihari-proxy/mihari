# Unix Base Dir 设计独立评审 R2

- 结论：**REQUEST_CHANGES**。
- 日期：2026-09-05；基线：`2d00f61e720fa27f115dea52f7b4a95cc35a599f`。
- 审阅规格：`docs/superpowers/specs/2026-09-05-unix-base-dir-design.md`，修订 R2，267 行。
- SHA256：`EA6A71DC11A38EFC24B09CA68AE47F6622F3DB1D74B273E1CEFBE112C691FE34`。
- 完整重读规格并对照当前代码与 R1；仅新增本报告，未修改 spec、生产代码或运行真实系统服务。产品批准仍待用户作出，不视作技术缺口的豁免。

R2 已建立完整的推荐方向，尤其是按 UID 分离日志、provider 只读 credential、双锁加 peer 身份检查、停止后轮换 token、持久 activation 先于业务写、Windows 旧适配器等。剩余问题主要是新合同之间的冲突和中间状态可恢复性，而非要求再次选择产品方向。

## R1 逐项核对

| R1 ID | 结果 | R2 核对说明 |
| --- | --- | --- |
| 01 共享 root 输入边界 | 部分关闭 | 已给权限矩阵与拒绝未知语义的策略；新增 provider 代管仍需接线和生命周期合同，见 R2-06 |
| 02 Unix capability/祖先 | 部分关闭 | 已明确平台原语、系统祖先 ACL 与应用目录策略、安装根失败边界；外置公开文件的继承 ACL 仍有缺口，见 R2-04 |
| 03 显式模式/锁域 | 部分关闭 | 全模式表、数据与 endpoint 双锁、活跃 socket 检查已关闭主要风险；同名服务全局锁顺序/无服务分支仍冲突，见 R2-02 |
| 04 快照协议 | 部分关闭 | endpoint、帧、限额、关闭与 v2 已明确；精确字节、终帧及空来源合同需修正，见 R2-05 |
| 05 journal/activation | 部分关闭 | durable activation 方向正确；逐动作写前记录、验证入口和重启身份仍不足，见 R2-01/02 |
| 06 迁移清单与静止性 | 关闭原主要问题 | 完整白名单、资源保留、恶意 writer 与受管 writer 区分、重复导入和权威规则已明确；恢复机制按 R2-01 继续修订 |
| 07 安装器/服务事务 | 部分关闭 | apply/Prepare、制品信任、定义备份和平台适配器已定义；systemd mask 及事务分支见 R2-02/03 |
| 08 共用接口/credential | 关闭原主要问题 | NewPaths 旧语义、Windows 静态 token/local export、Unix provider、停机轮换、错误码和资源所有者闭环已明确 |

## UBD-R2-01 — journal 必须记录动作前意图，且区分跨重启 identity

**严重度：Major / 数据完整性阻塞。规格行 202–218。**

phase 表仍把阶段写入前的动作当作尚未发生。例如 durable phase 为 prepared 时，进程已经 disable/mask/stop，尚未来得及写 stopped 就崩溃；表第 208 行却规定“原服务不变”。同样，I binary 已替换但 PATH binary 尚未替换时，尚未进入 binaries_committed。第 204 行通过 hash 猜动作是否完成，可以帮助文件恢复，但不能让 prepared 的错误前提成立，也没有覆盖已改变的 enabled/mask/job/cgroup 状态。原 `internal/service/service.go:228–245` 没有 journal 可供沿用，必须由本稿明确新增机制。

另外 journal 把 mount identity 与 dev/ino 一同持久化，并在任意身份不匹配时拒绝恢复（202/204）。Linux mount ID 是运行时挂载身份，正常 reboot/remount 可能改变；它适合一次持有 fd 的安全检查，不能直接作为跨重启数据身份的永久相等条件。当前 `internal/platform/privatefs_unix.go:37–40` 也只把 identity 用于打开句柄的一次生命周期，未提供持久化对象 ID 合同。

**修订要求：**

1. 为每个不可逆外部动作写 durable intent（包含准确旧值/候选值/动作编号），完成后再记录 result；恢复按 intent+实际状态逐项收敛。至少覆盖禁启、mask、stop、D 发布、两个 binary、channel、定义/enable/disabled、验证进程和 activation。
2. 明确 prepared 后任意停机动作可能已发生，不得按 phase 名直接跳过原服务恢复。恢复器也应记录自己的动作，以便恢复过程中再次崩溃仍幂等。
3. 区分同一 boot 的 fd/mount identity 与跨 reboot 的重新获取策略：重新从可信祖先验证目标，核对 root 私有记录、对象类型/owner、候选/备份内容 hash 等稳定证据；不能无条件相信 pathname，也不能把正常 boot-local ID 改变等同于数据被替换。
4. 写出同布局升级的 D 未替换分支，避免 generic data_committed 恢复把现有权威 D 当作失败候选移走。

## UBD-R2-02 — 验证 daemon、私有实例与无服务更新的入口仍矛盾

**严重度：Major / 生命周期和可实施性阻塞。规格行 53–63、122、184–204、216–220。**

第 204 行要求 daemon 见 pending 就退出，服务启动入口先执行 recover；第 216 行又要求 definition_committed 状态下启动验证 daemon。没有定义该进程如何被授权进入验证模式、怎样绕过“一律 pending 退出”、谁持 install.lock，以及它是否再次尝试取得已由 installer 持有的锁。ready.json 包含 transaction ID 只定义结果，并不定义进入模式的可信通道。当前 `internal/daemon/run.go:25–50` 与 `service.program.Start`（`internal/service/service.go:367–400`）没有此模式。

此外第 196 行承诺无服务 self update 不创建 B/D/token，但第 184、202、204 行要求所有 Unix 更新经过固定 B 的 InstallTransaction、journal 和锁；两者不能同时执行。显式私有服务还同时涉及 P/install.lock 和默认 B/install.lock，第 122 行没有为这两把安装锁规定唯一顺序。非 root 便携 P 则不应进入 root journal/系统服务恢复路径。

**修订要求：**

1. 给出明确内部启动形式与权限验证：installer 在持全局锁时启动一个限定 transaction ID/私有 capability 的验证子进程；验证进程只取数据和 endpoint 锁，验证 journal 阶段/候选 hash，不能再次 recover 或拿安装锁。普通 `daemon` 与服务正常启动不接受任意用户指定的验证 bypass。
2. 区分 green foreground root daemon、普通已安装系统 daemon、system service 的 private P、非 root private P、无服务 self update 的操作流程与 journal 位置。没有服务的 binary-only 更新可使用受信任 binary 父目录下的独立事务，或其他明确不触碰 B 的方案，保留原目标。
3. 固定同名服务操作先拿默认 B 的全局安装锁，再拿私有实例锁，最后数据/endpoint 锁；恢复/stop/uninstall/start 必须遵守同一顺序。非服务 P 操作不应取得默认 B 锁。
4. 定义绿装“先验证/创建 B/D”与迁移“只接受不存在 D”（55/224）之间的分工：在识别旧来源和 journal 之前不要先创建会被误判为未知现存目标的 D。

## UBD-R2-03 — systemd runtime mask 不能保证当前安装的服务被禁启

**严重度：Major / 停机与迁移安全阻塞。规格行 192、209、242。**

当前固定依赖 `github.com/kardianos/service v1.3.0` 的 `service_systemd_linux.go:76–80` 将 system unit 写在 `/etc/systemd/system/<name>.service`；`internal/service/service.go:418–436` 使用这一默认配置。runtime mask 位于 `/run/systemd/system`，不能覆盖优先级更高的 `/etc` unit。disable 只移除启用链接，也不禁止依赖或显式激活。因此 R2 的禁启步骤不能证明旧 daemon 在复制期间不重启。

依据：[systemd 官方 unit 搜索路径](https://github.com/systemd/systemd/blob/main/man/systemd.unit.xml)、[systemd 官方 mask 定义](https://github.com/systemd/systemd/blob/main/man/systemctl.xml)。

**修订要求：**

1. 在受信任 journal 备份已完成后，使用能覆盖实际 unit 的持久禁启方案，例如在原 `/etc` 位置安全换成 mask，再 daemon-reload 并核验加载状态；不是简单追加 `--force` 猜测成功。
2. 为原 unit、drop-in、原 mask/enable 状态以及新 mask 的创建/移除分别记录写前意图和 identity；每个动作失败都可精确恢复。
3. 定义 install/apply/recover 的服务定义重建与解除 mask 顺序，以及验证 daemon 是否直接启动而不依赖已 mask 的 service。机器在停机阶段重启也必须保持旧服务禁启直到完成恢复。

## UBD-R2-04 — 外置 C/endpoint 锁的继承 ACL 与临时文件发布缺少保证

**严重度：Major / 文件安全阻塞。规格行 102、107–116、124。**

系统默认 B/D 在创建后去除应用目录 ACL，是可行方向；但显式外置 C/E 的 parent 只要求非 owner 无写/删除能力，并允许系统祖先的 ACL。一个 parent 当前对其他 UID 不可写，不代表它的 default/inherit-only ACL 不会给新文件额外写权。第 109 行明确治理的是 B/D/I/U 等应用目录，第 116 行的 C 临时文件只写 0600→0644，没有定义创建 regular 时的 ACL 授权及继承窗口。Darwin ACL 不能只通过 mode 值证明无额外权限；若不可信用户在继承 ACL 尚未移除时拿到 regular 文件写 fd，之后 chmod/清 ACL 并不能撤销已打开 fd 的写能力。

当前 `internal/control/credential/credential.go:14–18,48–65` 仅有路径 ReadFile/OpenFile，`privatefs_unix.go:175–192` 也只在打开后 Fchmod；这些现有实现不能补足本文对外置 credential/lock 的保证。`publish_acl_darwin.go:25–48` 目前保守拒绝非空 ACL，没有上述新继承策略。

**修订要求：**

1. 区分系统祖先 traversal 检查与“在此 parent 新建/发布 regular/socket/lock”的检查；后者必须证明不存在赋予非 owner 写权的继承规则。
2. 最小方案是对外置 C/E 父目录采用明确更严格的 creation ACL 要求，无法证明则拒绝该覆盖；也可在已隔离私有目录先创建并去 ACL，再通过同文件系统受控 rename 发布，但需定义句柄/同步和 final ACL 规则。
3. 把 regular 文件（C、channel、lock、journal、备份、binary）都纳入 final ACL 复核，并明确不能以“先公开创建，再 chmod 修复”消除曾经获得的文件句柄能力。
4. 提供 Darwin inheritable grant、Linux default ACL 与外置锁/credential 的负向 fixture。无需因此修改任意系统祖先权限。

## UBD-R2-05 — wire hash、结束语义和零来源 zip 仍不能唯一实现

**严重度：Major / 协议阻塞。规格行 167–180。**

“canonical record JSON”没有编码标准：key 顺序、数字 token（1/1.0/1e0）、Unicode/HTML escaping、换行都影响 hash。现有 `internal/logging/export_json.go:159–165,174–193` 使用 UseNumber 和 json.Marshal，但这不是跨客户端协议的 canonicalization 定义。接收端若先解码再编码同一对象，不保证重现发送端 hash。第 171 行要求 complete 后不能有额外数据，但第 172 行又称“任何 EOF”都失败，需要区分 complete 前 EOF 与 complete 后正常 HTTP body EOF。

第 178 行还同时要求“保留四个固定 zip entry 名”和 files 只含非空来源；当前 `internal/logging/export_zip.go:150–159,169–187` 实际只为非空来源创建 entry，最多四项。两种合法解释会产生不同 v2 artifact。此外 per-record 1 MiB 与 frame 2 MiB 的关系依赖 JSON 逃逸方式；必须以明确定义的编码计量。

**修订要求：**

1. 推荐传精确 UTF-8 JSON 字节的 base64 payload，并对解码原字节加 LF 求 hash；发送方一次生成，接收方先核验原字节再二次脱敏/重编码。也可采用真正明确的 canonicalization 标准，但不能只命名 canonical。
2. 指定每帧的 type 字段、record redacted 布尔、所有计数字段含义及编码预算（raw/decoded/wire），给出至少一份含 Unicode、数字和空源的完整 wire fixture。
3. complete 必须验证后继续确认正常 EOF；complete 前 EOF 失败，complete 后额外 frame 或垃圾失败。定义 response 关闭与客户端等待该 EOF 的时限。
4. 明确 v2 的允许 entry 名集合与实际创建条件，空来源是否创建零字节 entry，以及 manifest files/source_status 如何对应；显式选择 current_user_only 时与 Windows/private v1 的合同不要混淆。

## UBD-R2-06 — provider 代管已变成新业务子系统，需补最小生命周期而非仅声明策略

**严重度：Major / 安全和行为兼容阻塞。规格行 148–158、220、226–240。**

把任意 YAML 原样送入 root 核心的风险已被正面处理；但第 153 行选择由 Mihari 获取、校验和更新所有 provider，意味着不再只是配置字段校验。当前 `internal/runtime/manager.go:418–435` 在 mutation 锁内直接调用核心 `UpdateRuleProvider`，`internal/control/server/runtime.go:353–366` 暴露这一稳定操作；现有 subscription scheduler 管理顶层订阅，尚无本文新 provider graph/cache 的事务。只给“由 Mihari 原子更新”会留下 native core 自动刷新或显式刷新绕开策略、旧 API 不再实际刷新、订阅切换后异步结果回写等行为缺口。

第 153 行缓存写到 `D/runtime/provider-cache`，第 154 行又将核心工作根设为 `D/runtime/core-home` 并要求资源复制进去；最终配置究竟引用哪个受管路径也需要唯一化。第 155 行支持审计标识和固定 hash，但现有发行锁仅记录制品，未说明新 policy 版本映射怎样从可信程序获得。

**修订要求：**

1. 明确 provider 代管仅服务于系统 root 输入边界，不再附带无关功能。定义 provider 身份（订阅 ID/代际/名称）、允许类型/格式、下载和缓存上限、刷新触发/interval、取消所有者、离线旧缓存及错误行为。
2. 下载和校验在 mutation 锁外，提交时重新核对订阅及 provider revision；拒绝陈旧结果。更新本地 provider 和通知核心失败时必须保留最后有效资源，并说明是否回滚文件和核心状态。
3. 明确生成给核心的 provider 禁止 native 远程下载的具体输出策略；将现有 UpdateRuleProvider/相关 Web mutation 接入相同受管刷新用例，保持稳定 v1 可观察语义。
4. 唯一定义下载 staging、已验证缓存及最终 core-home 路径，配置仅引用最终受管文件；恢复/迁移清单纳入该新资源与引用关系。
5. 明确 policy compatibility 表由 Mihari 的版本化可信资源给出，哪些确切核心版本/hash/字段族可用；发布源的任意自报“兼容”标识不得自动授权。实现可填充详细 typed registry，但本 spec 必须列清初始支持范围及受限功能的用户可见结果。

## 复审门槛

本轮 6 项均为可检查的合同修正；无需另开产品方向讨论或缩减 Unix 迁移、普通用户连接和导出目标。补齐后应再次完整核对 phase/operation/mode 的交叉表、单个 wire fixture 和可信创建/发布路径。技术 PASS 仍不等于用户已批准新增公开契约、安全边界或实施。
