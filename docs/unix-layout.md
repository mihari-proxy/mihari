# Unix 布局、权限与安装恢复

本文说明 Linux/macOS 的当前实现合同。Windows 保留原有路径、DACL、服务和本地 ZIP v1 行为。原生安全验收结果见 CI 的独立 `unix-layout-security` 结果；代码或交叉编译通过不等于该结果通过。

| 角色 | Linux | macOS | owner / mode |
| --- | --- | --- | --- |
| 系统入口 B | `/var/lib/mihari` | `/Library/Application Support/mihari` | root / 0711 |
| 业务数据 D | `B/data` | `B/data` | root / 0700 |
| 控制 socket E | `B/control.sock` | `B/control.sock` | root / 0666 |
| credential C | `B/control.token` | `B/control.token` | root / 0644 |
| 应用通道 | `B/mihari-channel` | `B/mihari-channel` | root / 0644 |
| 安装根 I | `/usr/local/lib/mihari` | `/usr/local/lib/mihari` | root / 0755，binary 0755 |
| 本用户诊断根 U | 绝对 `XDG_STATE_HOME/mihari`，否则可信 home 的 `.local/state/mihari` | 可信 home 的 `Library/Logs/mihari` | 当前 UID / 0700 |

D 保存 settings、订阅、运行配置、GeoIP、面板、provider、核心及机器日志。普通业务文件 0600，核心 0700。daemon/mihomo 日志位于 `D/logs`；本用户 TUI 日志和导出分别位于 `U/logs`、`U/logs-export`。普通 CLI 不创建 B、D 或 credential；未安装时仍可记录本用户 TUI 日志。无法验证 U 时保留内存诊断，不退回机器 D。此布局统一机器发现，不宣称严格遵循 FHS 的日志/runtime 分类。

所有本机用户均可通过 E/C 认证并管理同一代理，读取受控机器诊断快照；这不是按用户隔离的代理管理权限。Unix 客户端每次请求和重连只读 C，并验证实际 socket peer owner；失败不使用旧 token，不自动重放 mutation。只有 daemon 创建 credential。轮换须先停止 daemon，删除选定 C，再启动 daemon；运行中原地替换不属于支持的轮换流程。

显式 `MIHARI_DATA=P` 保留单根语义：P 本身就是数据根，绝不是 P/data。私有 P 的目录/文件/socket 为该 daemon UID 的 0700/0600/0600；各平台使用共同的配置生成语义，root 私有实例仍校验核心二进制身份。P 不能等于、包含或位于默认 B/D 内。私有实例不新增第二个同名机器服务；root 显式私有服务仍由全局安装事务管理。`MIHARI_CONTROL_ENDPOINT`、`MIHARI_CONTROL_CREDENTIAL` 独立覆盖 E/C，`MIHARI_INSTALL_ROOT` 只覆盖 I，均在入口相对初始 cwd 固定一次。socket 最终字节上限 Linux 107、macOS 103。默认机器发现不采用 XDG_RUNTIME_DIR；root 不从 HOME/SUDO_USER/XDG 推断路径。

系统根与 I 必须经过从 `/` 开始的 no-follow owner/mode/ACL/挂载校验。Linux 要求支持安全能力的本地文件系统；macOS 要求启用 ownership 的本地 APFS/HFS。安装不会修复 `/usr/local` 等主机祖先权限；默认 I 不安全时可显式选择安全 I，例如 `/Library/PrivilegedHelperTools/mihari`。离线可信清单来自一次性解析的 `<I>/install-trust`，包括自定义 I；相邻下载 checksum、请求路径和旧用户树均不能成为 root 执行信任来源。

配置生成统一保留订阅中的非托管字段，仅覆盖 mixed 端口/地址、allow-lan=false、controller 地址和 secret，并删除 external-ui 三个字段。TUN 开关只覆盖 tun.enable，不覆盖 stack、device、DNS 或路由等其他参数；未托管开关时保留订阅原值。Mihari 不再使用完整 YAML 字段白名单，配置语义与 provider 下载/缓存由 mihomo 原生处理；CLI/Web provider 更新入口继续经统一 mutation coordinator。Mihari 保留候选核心校验、revision 复查、原子发布和 reload 失败回滚。

Unix root 仍只接受 v1.19.30 的四个 Unix OS/arch 内置核心 hash，继续核验 provenance receipt 与执行文件身份，不执行旧用户树 binary。移除 YAML 策略不扩大可信核心版本范围。Mihari 不再保证所有传给 root 核心的字段都经注册表审计；关键覆盖不限制所有额外 listener 或文件访问。

升级时先恢复旧 provider/resource WAL，再从活动订阅的原缓存生成配置。旧哈希命名 provider、Geo 资源和有效配置不会被主动删除；原缓存缺失或无效时返回可诊断错误并保留原数据，不从生成配置猜测 URL 或联网补回源数据。核心 receipt 中的历史 policy_id 字符串继续兼容，它不代表仍启用 YAML 策略。

Unix 安装、迁移、服务生命周期与已安装服务自更新统一经过 app installer 的停机事务。锁顺序为 B install → 私有服务 P install → data → endpoint；永久锁文件不 unlink。daemon/Manager 仍是运行业务的唯一写入者，窄例外仅包括 root installer 在停机事务中迁移业务文件及维护安装资源，以及固定应用通道 metadata 的受锁保护维护。TUI 可经 logging 写自己的固定日志序列，不能直接写 settings、订阅或 token。

迁移来源由可信已有服务定义或显式来源选项决定。没有已有服务时，固定 root-home 旧树存在可阻止错误的新建，但不会自动选择或导入该树。旧树与旧日志保留，不删除用户数据；迁移不会执行旧树 core。候选数据/核心/二进制先校验，持久化 action intent 后才改变安装资源。activation 持久化前可以恢复 source，之后只修复 target；恢复会核对 inode/hash/事务身份，重复恢复必须收敛。`mihari service apply --request <file>` 的 recover 请求是明确恢复入口；普通启动和通道写入不会悄悄恢复未完成事务。`service uninstall` 保留业务数据与恢复所需身份。

已安装服务启动必须匹配全局 B 的 activation/complete、选定 P/D/E/C/I 和当前 binary hash，并在取得 data/endpoint lease 后再次校验。普通未标记的 root P 前台只查看自己的 P；不存在安装权限绕过。TUI 更新先取消并等待工作与日志句柄退出，再执行安装事务和重新启动界面。

`self channel` 查询选定 B/P 的固定 sidecar，缺失为 main。写入要求系统 root 或私有 P 的实际 owner，只持选定 install.lock，不停止服务或取得 D/E。root P 写入还只读检查全局 B 的相关未完成私有事务，相关 pending/activation、损坏或不安全 journal 均拒绝；非 root P 不读取 B。无已安装服务时，`self update` 先完成可信服务检查，再使用编译通道选择下载，只替换 binary，不因选择通道读取/创建 B。

系统 Unix 导出通告 `machine-log-snapshot-v1`，将固定机器日志快照与本用户日志组合为 `mihari-logs-export/v2`。离线时必须明确选择仅本用户日志，不把不可用机器来源默认为空。Windows/显式私有 P 保留本地 v1。导出持有目标目录 identity、验证每个来源的字节/摘要及完整 EOF 后才发布 ZIP；不读取任意业务文件。脱敏会保留部分节点、域名/IP 和流量元数据，分享前需检查。日志轮转与保留数量沿用既有设置，迁移不删除旧日志。

维护验证使用一次性 hosted Linux/macOS、两个新实际 UID、隔离根与独立结果目录；Linux 的 bind mount 只存在于私有 mount namespace。禁止在工作站或持久 self-hosted root runner 使用安全脚本，也不运行真实系统服务、真实订阅或 core。计划使用的 hosted macOS 26 arm64（实际版本/架构由后续 job 记录）不能证明 macOS 12 amd64 最低兼容性；该独立环境缺口仍需显式补充证据。
