# Issue #193 降级确认设计 R1 独立审核

日期：2026-09-09。审核对象：`docs/superpowers/specs/2026-09-09-issue-193-downgrade-warning-design.md` R1；基线 `60ca3ea`。一次 subagent 只读审核设计与生产代码，未修改设计正文或实现、未提交。Issue 正文通过 `gh issue view 193 --json body,title` 核对。

结论：范围方向基本正确，但不建议直接进入实施。发现 1 项 Blocker、2 项 Major、2 项 Minor。修订由主代理核实完成；本报告不代表修订后的方案已二次审核。

## Blocker

### B1：官方构建不记录设计依赖的 `-ldflags`，静态版本识别不能作为主路径

- 位置：设计 §4.2、§8。
- 证据：`.github/workflows/release.yml:143` 使用 `-buildvcs=false -trimpath -ldflags "-s -w -X ...Version=..."`；本地 Go 源码 `/usr/local/go/src/cmd/go/internal/load/pkg.go:2469` 起仅在 `!cfg.BuildTrimpath` 时记录 `-ldflags`。主代理指出后，本审核独立读取这两处确认。
- 影响：`debug/buildinfo.Read` 无法从正常发布产物获得设计指定的 Version 注入信息。绝大多数官方安装会被判 unknown，确定降级不会显示准确 current 版本；“全部官方构建形式可识别”的验收不可达。补一个非 trimpath fixture 不能证明生产可行。
- 修订建议：删除静态 ldflags 为官方主路径的承诺。明确分层来源：运行程序可用自身注入版本；对其它目标只在既有安全信任检查后、以合适身份及有界 context 执行 `self version --json`，或采用已有可信且绑定目标摘要的版本证据；不能安全探测的目标才降为 unknown。不得为此新增 root 执行用户可写程序。先定义实际可用的各平台版本来源表，并让官方 trimpath/stripped fixture 验证选定路径；如要新增嵌入元数据，须说明它只解决新发布版本，不能解决已发布旧版本。

## Major

### M1：Unix 脚本第二次调用只有 `--yes`，丢失第一次交互确认的目标身份

- 位置：设计 §4.3、§5.2，尤其第 97–99 行。
- 证据：设计要求交互绑定候选及全部目标，变化即拒绝；但流程是首次 `service apply` 返回错误、脚本提问、第二次同候选 `service apply --yes`。现有 `internal/cli/service_apply.go:11` 只有 request 文件参数，R1 只新增 yes；错误 details 又仅允许风险/版本/角色，没有目标或预览指纹输入输出契约。
- 影响：用户对目标 A 确认后，另一个安装改变目标或服务定义为 B，第二次调用会新建 B 的预览并把 yes 当成对 B 的许可。固定 release tag 不能修复此问题，且违背设计宣称“即使 yes 也拒绝身份变化”。
- 修订建议：选定跨进程预览绑定的具体契约，例如只读 preview 返回安全的摘要，apply 接受显式 expected-preview 参数并持锁比较；无需持久 token、无需改变 install-request/v1。把新增参数/输出影响列入公开变更。交互接受必须走绑定参数，用户一开始显式 yes 可以从本次调用建立预览后再复核。补测试：第一次检查后换目标、改服务定义、同 tag 换 digest，第二次调用不得停服务或写入。

### M2：Windows 原子保护及服务副本复核没有现成机制，当前复用描述不能兑现

- 位置：设计 §5.1、§5.3、§6。
- 证据：`internal/update/replace_windows.go:12` 仅通过 `os.Rename` stash/replace；`scripts/install/install-aio.ps1:83` 直接 `Copy-Item`；`internal/app/self_update.go:54` 的 AfterReplace 才调用服务同步，`internal/service/service.go:124` 的 `UpdateInstalledBinary` 无预览/consent 参数，读取状态后停机并 stage。不存在设计称可复用的目标句柄保护。
- 影响：新加哈希检查与现有 rename/copy 之间仍有并发窗口；服务定义/服务副本可能在主程序替换后变化。此时“变化即无本次替换”也不能同时满足已有 AfterReplace 顺序与部分成功语义。若追求全部文件和服务定义原子替换，会显著扩大 #193 范围。
- 修订建议：明确 Windows 最小平台适配和所有权范围，写出预览何时读、句柄/保护由谁持有和释放、允许哪些共享方式，以及服务同步如何消费同一预览。区分首个写入前拒绝与主程序已替换后的同步拒绝，后者必须返回现有 Updated/部分成功。无法在当前范围提供全面并发原子性的地方应收紧承诺，说明只保证各实际操作边界的复核，不能声称裸路径哈希或不存在的机制提供原子 CAS。补主程序替换后服务目标变化测试，确保不覆盖未经确认的服务副本、不谎报“没有修改”。

## Minor

### m1：旧 Windows bundle 交接仍是未决分支，需确定本轮实际支持策略

- 位置：设计 §5.3 第 117 行。
- 证据：`scripts/install/install-aio-remote.ps1:339` 后直接执行下载 bundle 内的脚本。R1 写“使用当前受保护的安装逻辑消费旧 bundle，或明确拒绝”，未选其中之一；§5.4 又排除运行时公共脚本下载依赖。
- 建议：明确本轮采用能力检查并拒绝旧内层，还是当前脚本自带执行逻辑；若拒绝，给出用户可完成旧版本安装的具体受保护替代路径，并在最终汇报列为兼容性限制。不要把对旧 bundle 的拒绝表述为其降级安装已获支持。

### m2：Unix 根 bootstrap 是生成块，任务落点应包括模板及生成一致性测试

- 位置：设计 §7 脚本任务。
- 证据：`scripts/install/root-apply.sh.in:2` 要求改模板后运行 `generate_root_apply.py`；已有 `test_root_apply_generated.py` 校验三份脚本的生成块一致性。
- 建议：在实施落点明确模板、生成器调用与一致性测试，避免把六脚本独立分发误解为手工修改三个生成区。直接 `service apply` 的恢复操作也应明确保留既有授权语义，pending 拒绝仅针对新替换预检，不顺带封禁专用恢复用途。

## 保留的正向结论与范围判断

- 正确区分候选选择与降级风险：保留 stable ahead，不为满足 Issue 背景中的过时描述而新增回滚行为；同基础 dev → stable 不能一律称降级。
- 覆盖 CLI、TUI、六个安装入口、实际服务副本及远程参数交接合理，未夹带 #194。
- 确认前不停止服务、不写业务数据；TUI 在候选准备后交互，并考虑退出后失败及迟到结果清理，方向正确。
- unknown 也需确认是超出“确定降级”的产品行为，应继续作为待用户明确审阅的选择；无需提前新增依赖或持久格式。
- 本审核未运行生产测试；报告是代码/设计证据核查，不是实现或测试通过声明。
