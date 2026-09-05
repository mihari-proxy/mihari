# Unix Base Dir 设计独立评审 R3

- 结论：**REQUEST_CHANGES**，剩余 **1 项 Major**。
- 日期：2026-09-05；代码基线：`2d00f61e720fa27f115dea52f7b4a95cc35a599f`。
- 审阅规格：`docs/superpowers/specs/2026-09-05-unix-base-dir-design.md`，R3，295 行。
- 规格 SHA256：`3F969400114B2B3BEDAE56AF43ECECDA836E4205EA0CF871A51CBD8CB1AD3303`。
- 完整重读规格、逐项核对 R2，并复查当前 logging、service、provider 接线。未修改 spec 或生产代码，未执行真实系统服务/迁移。

## R2 关闭情况

| R2 ID | 结果 | R3 证据 |
| --- | --- | --- |
| 01 写前 journal / 跨 boot identity | 关闭 | 226–240：actions intent/done 覆盖外部动作与逆向恢复，phase 仅摘要；boot-local identity 与跨 boot 重获分离；create/retain 防止移动权威 D |
| 02 验证入口 / 锁序 / 无服务分支 | 关闭主要阻塞 | 124、214、230、242–246：全局/P/data/endpoint 锁序，匿名 pipe 租约验证子进程，无服务单 binary 原子替换，root 前台及非 root 便携分派；两处文字澄清见下文非阻塞项 |
| 03 systemd mask | 关闭 | 216–217：实际 `/etc` mask、daemon-reload/LoadState 核验，真实服务在 activation 前保持禁启，验证直接子进程；macOS 对等流程仍有本轮唯一 Major |
| 04 继承 ACL | 关闭 | 116–118：独立 creation parent，无 access/default/inherit-only ACL，regular 在写敏感内容前复核，外置 C/E 同约束，不能以事后 chmod 抹掉历史 fd 风险 |
| 05 wire 精确字节 / EOF / zip | 关闭 | 179–200：base64 原字节 hash、type/计数/EOF、编码预算、仅非空 entry，完整 fixture 的字节与 hash 已独立实算 |
| 06 provider 代管生命周期 | 关闭设计层阻塞 | 155–168：身份/格式/限额、daemon scheduler、离线缓存、revision/hash 复检、共享 RefreshProvider、回滚及 provider journal、唯一 core-home 路径、内置 policy/hash 与 provenance |

RootConfigPolicy 的逐字段 typed registry、接口方法体和具体 fixture 实现应在用户批准后的实施计划与代码中完成；R3 已明确语义范围、拒绝策略、版本可信来源和事务边界，本轮不要求把全部实现代码写进 spec。

## UBD-R3-01 — launchd 仍缺跨 reboot 禁启，以及 stopped 状态的恢复分支

**严重度：Major / 迁移生命周期阻塞。规格行 217、220、228、238–246。**

Linux 现在用持久 `/etc` mask；macOS 第 217 行仍只规定保存 disabled 状态后 `bootout`，没有持久禁用原 label 或从启动发现目录安全撤走原 plist。`bootout` 卸载当前 job，不等于原 `/Library/LaunchDaemons/*.plist` 在下一次系统启动时不能再次加载。因此它无法兑现同一段所说“实际 service 保持禁启到 activation_committed”。

另外该段写恢复/激活后 `bootstrap system`，但 220、246 行承诺 self update 保留 stopped 状态，service install 注册后保持 stopped。对带 RunAtLoad/KeepAlive 的 plist，bootstrap 会触发进程运行，不能把随后 stop 当成“从未正式启动”：它可能已经启动核心或进行自动业务 mutation。

**代码及平台依据：**

- 当前 `internal/service/service.go:418–436` 使用 kardianos 默认系统服务配置。
- 固定依赖 `github.com/kardianos/service v1.3.0` 的 `service_darwin.go:109–111` 使用 `/Library/LaunchDaemons/<name>.plist`；`209–210,330–342` 将 KeepAlive/RunAtLoad 写入 plist。当前配置不能被假设为加载但不运行。
- Apple 官方说明系统启动时加载系统 daemon 定义，以及 KeepAlive/RunAtLoad 对启动行为的影响：[Creating Launch Daemons and Agents](https://developer.apple.com/library/archive/documentation/MacOSX/Conceptual/BPSystemStartup/Chapters/CreatingLaunchdJobs.html)、[launchd.plist 官方手册源码](https://github.com/apple-oss-distributions/launchd/blob/main/man/launchd.plist.5)。

**具体修订要求：**

1. 在 bootout 前写 durable intent，持久 disable `system/<label>` 并核验 disabled 状态，再卸载 job、等待受管进程退出；或者选择同等可审计、跨 reboot 保持的 plist 隔离方案。不能只依赖内存 job 被卸载。
2. 将 disabled 改变、plist 备份/发布、bootout/bootstrap 分别纳入 actions。中途 reboot 后，旧 service 必须继续禁启，恢复者在重新验证并收敛 journal 前不得放行原 job。
3. 给出目标 `running/stopped × enabled/disabled` 的恢复规则。目标 stopped 时不执行会启动 RunAtLoad/KeepAlive 的 bootstrap；可以保留磁盘注册、恢复适当 disabled 状态但当前保持 unloaded，且 service status 能把它识别为 stopped。目标 running 时才按已批准的 enabled/disabled 策略显式加载/启动。
4. 激活前回滚使用旧状态，activation 后只恢复 target 状态；覆盖原来 stopped 的升级、只注册的 install、原 disabled 的服务、持久 disable 后 reboot 的开发集成场景。

## 非阻塞文字澄清（建议同次修订）

1. **安装锁由调用链借用，不重入。** 第 124 行 start 已拿全局锁，第 230 行又写 recover 先拿锁。接口应明确公开入口 acquire 一次，把 lease/capability 传给内部 RecoverLocked/StartLocked；不能让内部 recover 再 flock 同一文件，也不能调用会重新进入公共 service start 的路径。当前第 230 行“不能递归 recover”方向正确，补一句锁所有者即可。
2. **pending 的定义排除 durable activation 的正常启动。** 第 230 行普通 daemon 见 pending 退出，应明确这里指 prepared 到 definition_committed；246 行已规定 activation_committed/complete 可正常启动。若 activation 后尚有服务定义/enable actions 待完成，只有 root 恢复者补齐这些 target 动作，禁止回旧树。这样实现无需猜测“pending”的词义。

## 本轮实际验证

从 spec 的 jsonl 代码块解析五帧，独立进行 base64 解码、UTF-8 payload 字节计数和 SHA256：

- 帧数 5；payload 54 bytes，加 LF 共 55 bytes。
- daemon 来源 hash：`1625f1821f85ab2dc68c7da55c4fbe769637b7752174c7d5be8a83cd8d388a48`，与 spec 一致。
- 空 mihomo 来源 SHA256、bytes=0、complete total_bytes=55 一致。
- 规格文件哈希与派发的 R3 哈希一致。

上述验证仅证明文档 fixture 的字节合同自洽，不代表 Unix ACL、mount、service 或迁移实现已通过运行测试。修复本轮唯一 Major 后可进行聚焦且完整的一次复审；技术 PASS 与用户最终产品/公开契约/实施批准仍应分别记录。
