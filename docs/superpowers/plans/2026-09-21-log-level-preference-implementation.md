# Logs 显示级别保存执行计划

日期：2026-09-21

设计依据：[已确认的 Q1–Q10](../specs/2026-09-20-log-level-preference-design.md)。本计划补录已完成的测试开发步骤，并规定后续 rebase、PR、CI、bot review 与合并验收流程。

## 1. 设计与范围

- 只保存 Logs 的精确级别组合；System 日志级别、流订阅、Wrap、搜索和 Pause 保持现状。
- Enter 立即应用显示选择，后台串行保存最新确认的值，Level 旁显示 Saving。失败显示 Unsaved 并进入 F2，不重试、不补交、不设置恢复缓存；退出不等待保存。
- 由 daemon 在既有 mutation 路径中写入同一份 TUI 偏好；已打开的窗口保持自己的显示选择，新窗口恢复最后成功提交值。
- 扩展既有 `log_levels` 字段和读取路径，保留旧文件缺字段时默认全选；不要求旧版读取新文件，不新增版本准入机制。

## 2. 持久化与本地控制 API

1. 先新增回归测试，确认当前实现拒绝包含 `log_levels` 的偏好文件。
2. 扩展 preferences 模型与磁盘文档；校验非空、已知且不重复的级别集合。更新时只合并请求中提供的字段，原子写入成功后发布内存快照。
3. 扩展 `/v1/preferences/tui` DTO 和 server 适配；Level 请求不附带全局 revision 条件，不修改 Connections 列。保持既有错误码，非法级别映射为 `invalid_argument`。
4. 保留显式空列表的 JSON 编码，让服务端拒绝空选择，不能将空列表省略成未修改。
5. 同步 Unix installer migration 的严格读取；不执行真实安装或服务操作。

验收证据：`internal/preferences/log_levels_test.go`、`internal/control/protocol/preferences_test.go`、`internal/app/install_migration_log_preferences_test.go`、`internal/integration/log_preferences_test.go`。覆盖全部 15 个合法组合、旧文件默认值、字段互不覆盖、非法输入无副作用、写失败不发布及重建 daemon 后读回。

## 3. TUI 交互与生命周期

1. 先用 shell 回归测试证明首次加载没有恢复 Level，再接入现有偏好加载事件。
2. 先证明 Enter 缺少异步 Saving 状态，再接通保存命令、结果路由和动画；结果消息在弹窗处理前接收，切页不会丢失结果。
3. 用页面内版本标记合并连续显式修改，旧结果不能覆盖当前选择或结束新请求的动画；不保留额外待恢复副本。
4. 首次读取只恢复未被本地修改的页面；重连不覆盖当前窗口选择。失败停止该次尝试，未改动确认不重试。
5. 退出仅取消客户端请求和动画后续调度，不等待、刷盘或提交尚未发出的修改；已到达 daemon 的请求遵循既有生命周期。

验收证据：`internal/tui/pages/logs/preferences_test.go`、`internal/tui/log_preferences_test.go`。覆盖非阻塞操作、连续修改与迟到结果、弹窗和切页、F2 单次诊断、动画与焦点样式、立即退出和取消清理。

## 4. 本地验证与 rebase

前述 Red–Green 回归、全仓测试、相关包 race、vet 和六目标 CGO-free 编译已在原开发基线完成。提交前核查准确文件范围，保留主 worktree 用户的 `.gitignore` 改动。

后续交付顺序：

1. 读取最新 `origin/dev`，创建包含设计、计划、实现、测试和中英文 README 的 DCO 签名提交，随后 `git rebase origin/dev`。
2. 若有冲突，保留上游快速翻页行为并重新验证 Logs 交互，不能用整文件覆盖解决。
3. 在 rebase 后的提交执行 `go test ./...`、`go test -race -timeout=30m ./...`、`go vet ./...`、`golangci-lint run ./...`、`gofmt -l cmd internal`、`git diff --check`。
4. 执行不操作真实用户、账户或挂载的 `python -m pytest scripts/test/test_unix_layout_security.py -q`；六目标 CGO-free 构建由本地已有结果及 PR 最新提交的 CI 共同验收。

## 5. PR、评审与合并

1. 推送独立分支，创建面向 `dev` 的普通 squash PR。说明可选 API 字段、偏好文件升级影响和用户确认的“不重试、退出不等待”语义；不得修改 CHANGELOG。
2. 检查当前仓库 ruleset 和 PR 全部 checks，特别是 lint、test、cross-build、DCO/check，以及项目要求独立验收的 unix-layout-security。
3. 收集可用 bot 在当前 diff 上的评审结果。逐条核实，修复有事实依据的问题；不采用违背 Q1–Q10 的建议。需要回复时以实际代码与测试说明依据，不伪造审批。
4. 每次推送后等待最新提交的 CI 和评审结果；旧提交的通过结果不能证明新提交可合并。处理需解决的 review threads。
5. 全部适用 checks 成功、无未处理的实质性评审问题、base 无冲突后，按用户授权以匹配 head SHA 的 bypass squash merge 合入 `dev`。若缺少 bypass 权限，报告 GitHub 实际拒绝原因，不修改保护策略。
6. 合并后核对 PR 的 MERGED 状态、merge commit 和远端 dev 归属。最终交付 PR 链接与验证结果；不在本地 dev 上直接提交，不自动删除其他 worktree。

CI、bot review 和最终合并状态以 PR 上对应提交的检查记录为准；本计划中的待执行步骤本身不作为已完成证据。
