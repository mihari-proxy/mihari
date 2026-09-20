# 三平台核心版本与更新执行计划

日期：2026-09-17；更新：2026-09-19。用户已批准[收敛范围](2026-09-19-core-scope-reassessment.md)，继续测试开发、提交 PR 并跟进 CI/bot review。实现依据为[当前设计](../specs/2026-09-17-core-version-and-update-design.md)和 [ADR 0005](../../adr/0005-core-version-and-source-policy.md)。

## 授权与范围

在 `feat/unify-core-version-policy` 开发，PR 指向 `dev`。包含官方 stable/alpha 最新版、本地核心无来源凭据准入、离线保留、常规失败回滚、中断阻断及用户主动按原通道重装。保留现有平台保护，仅增加更新必要检查。不建设跨崩溃自动事务重放或新的三平台持续执行体系；普通 restart 不承担恢复。

已授权提交、推送与 PR，不包含合并、真实核心／订阅／系统服务测试。用户 `.gitignore` 修改不提交；不改依赖、Go 或 CHANGELOG。Dependabot 单独处理。

## 实施清单

- [x] 官方 ReleaseTarget 固定发布及资产；安装和版本检查复用目标校验；有摘要必须验证、无摘要警告。
- [x] Unix root 解除 receipt／编译版本表准入，保留安全根及文件身份检查；兼容旧未完成 pair WAL。
- [x] Unix root 与普通平台接入 daemon 更新 owner；候选、备份、发布及正常失败补偿覆盖字节、核心通道与运行意图。
- [x] Supervisor 支持单次试启动、健康确认、回滚和失败后阻断；移除 restart 重放旧事务的扩展。
- [x] 有效中断记录下保留修复控制面，阻止普通启动和 mutation；诊断列出材料位置及操作指引。
- [x] 本地认证 `POST /v1/core/reinstall`、client、CLI 和 TUI System 接通；失败保留材料与阻断，成功验收后解除。
- [x] 重装连续中断仍保存原通道、原运行意图及最初备份；完成记录残留且核心损坏时保留受限修复入口。
- [x] Unix 原生安装器不额外下载编译版本，接受已验证 AIO 包内核心；迁移保留本地核心／通道并拒绝丢弃未完成核心记录。
- [x] Windows AIO 仅为缺失核心写入包内版本，应用升级保留已有核心／通道。
- [x] README、命令说明、设计及决策文档同步收敛。
- [ ] 完成最终全仓测试、race、lint 和平台验收。
- [x] 检查精确提交范围，DCO commit，push，创建指向 dev 的 PR。
- [ ] 跟进 CI 与 bot review，按实际反馈修正至全绿；不合并。

以上完成项表示代码与对应合成测试已落地，不表示整项任务交付完成。

## 测试证据

2026-09-19 在当前 Windows 主机使用 fake core、合成 HTTP 和临时目录验证：

- 回归测试先失败后修复：Windows AIO 保留核心／通道；版本检查拒绝未上传完成资产；中断启动保留诊断与材料路径；Unix root 启动验证失败保留受限修复 owner；迁移保留通道并拒绝未完成更新；已完成记录下核心变化仍可主动修复。
- `go test ./internal/core ./internal/runtime ./internal/supervisor -count=1` 最新一轮通过。
- control server、CLI、TUI System 包测试通过；core reinstall 本地认证／请求元数据、System 确认流程有覆盖。
- Unix 迁移相关测试、app 中断启动测试通过；版本检查通过真实 core adapter → Manager → 本地 HTTP handler → client 的集成测试。
- 权限切换前，`go test -race ./internal/core ./internal/runtime ./internal/supervisor ./internal/app ./internal/integration -count=1` 通过。之后还有实现修改，不能视作最终版本 race 结果。
- Windows AIO 新测试在权限切换前 2 passed；后续复测因 PowerShell 执行策略受阻，不改产品脚本来绕过环境策略。
- Windows/Linux/macOS × amd64/arm64 六目标 `CGO_ENABLED=0` 构建通过。Linux amd64 和 macOS arm64 的 app 测试二进制交叉编译通过；没有在此主机原生运行 Unix 测试。
- 全仓 `go vet -p 4 ./...` 通过。`git diff --check` 通过；最终检查仍随修改执行。

## 2026-09-19 受限环境记录（历史）

会话切换为受限工作区后，原检查进程句柄失效，其最终结果没有计作通过。已把新 Go 缓存和检查日志放到忽略的 `bin/core-policy-checks`，不提交这些产物。

- 全仓 `go test -p 4 ./...` 已执行但失败：包含 named pipe、Windows ProgramData 和文件身份操作的访问拒绝，以及受限环境下 bundler 的工作目录解析失败。一次 runtime 通道测试失败后单独复测及整个 runtime 包复测通过；另一批并行检查中启动日志配置测试发生一次失败，随后定向连续 5 次及最新完整 runtime 包均通过，暂未复现，保留为验收关注项，不据此宣称所有失败均由环境导致。版本检查集成 fixture 缺少新要求的资产身份信息已修正，定向复测通过。
- 最新 affected race 运行未能编译：系统拒绝执行 gcc。不能宣称最新 race 通过。
- 最新 TUI 包测试还受到 named pipe 访问拒绝影响；System 页面及 CLI/control server 的最后一轮定向包测试通过。
- lint 无法启动：解析工作目录符号链接时 Access is denied。不能宣称 lint 通过。
- 当前权限说明把 `.git` 设为只读，禁止提权且限制网络；尚未提交、推送或创建 PR，尚无 CI／bot review 验收结果。

需要在允许 Git 写入、推送及平台测试的环境恢复最终检查和 PR 流程。不得通过删除安全测试、弱化平台权限检查或提交用户 `.gitignore` 来规避限制。

## 2026-09-20 完全访问恢复后的验证

用户已恢复完全访问。全源码包 `go test -p 4 ./cmd/... ./internal/... ./scripts/...` 通过；lint 同范围 0 issues。使用显式源码包是因为历史沙箱 pytest 临时目录仍不可读取，`./...` 扫描会碰到它；没有排除任何源码包或产品测试。清理该临时目录的操作被自动审批拒绝，未继续尝试删除。

脚本组合测试首次 159 passed、40 skipped、2 failed；两条失败来自 PowerShell 7 模块路径污染 Windows PowerShell 子进程。仅设置本次测试进程的 Windows PowerShell 模块路径后，两条失败和新增 AIO 两条测试全部通过。完整重跑、最终 race、同步最新 dev 和 PR 验收继续进行。

移除了已不再调用的旧核心白名单辅助函数。远端 dev 新增 10 个提交，需要同步并验证生命周期展示的衔接。用户 `.gitignore` 仍不包含在提交中。

## PR 与后续检查

已同步最新 dev，创建 [PR #284](https://github.com/mihari-proxy/mihari/pull/284)，未合并。更新接管时保留实际启动时刻的衔接回归已修复；最终全源码包 race 测试通过，脚本组合测试 161 passed／40 skipped。

首轮 CI 暴露 Unix 专用测试的旧 receipt 字段引用及未检查 Close 返回值，已修正，并将离线内附核心测试加入原生安全清单。Linux amd64／macOS arm64 的带 unix_security 标签测试二进制交叉编译通过；修正后的 CI 仍在执行。

2026-09-20 用户要求 GitHub Actions 每 10 分钟检查一次，已停止 30 秒轮询，改为本线程定时跟进。CodeRabbit 已请求实际审查；Cubic 因月度额度用尽返回 neutral，不能当作已审查。后续以 PR 最新提交的 CI／review 结果为准，处理完毕后停用跟进。
