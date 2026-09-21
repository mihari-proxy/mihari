# Windows 更新退出与启动清理执行计划

日期：2026-09-21。关联 [Issue #282](https://github.com/mihari-proxy/mihari/issues/282)、[设计](../../investigations/issue-282/implementation-design.md)、[ADR 0006](../../adr/0006-windows-self-update-finalization.md)。

本次同一 PR 交付设计、计划、修复和回归测试。独立 worktree 分支基于 `dev@6a9da72`，不修改 CHANGELOG，不触碰其他 worktree 的改动。不增加辅助进程、依赖或清理 journal。

## 实施顺序与验收

1. **故障基线与 Windows 原语。** 用临时可执行文件复现映射旧镜像无法删除及直接 PID 停止遗漏后代。将调查探针转为正式回归，验证句柄删除、Restart Manager 进程身份、客户端退出事件、挂起创建再加入 Job。已完成。
2. **退出提示与启动清理。** Windows 完成发布后退出并提示“请重新输入 mihari”；app 只清理自身副本，TUI 持有与界面并行的任务，取消后 join 再关闭日志。daemon Ready 后启动清理，core 目录由 runtime 所有者清理。已完成。
3. **清理边界。** 精确旧文件名、普通单链接文件、固定目录句柄、ACL 校验和与更新共用的目录锁；失败保留到下次启动，原始详情进入 F2。卸载接受空锁文件，core 事务恢复文件不受影响。已完成。
4. **进程生命周期。** 每代 core 使用独立 Job，挂起进程在执行代码前加入 Job；终止整个 Job 并等待退出，避免继承输出管道卡住。提权 daemon 在启动子进程前建立受保护外层 Job，供更新器绑定与验证整树。已完成。
5. **在途排空。** 实际 mutation 的独立入场计数覆盖去重缓存溢出和内部启动/同步工作；prepare 拒绝新写入，等待已接收工作完成，超时恢复入场。认证 pipe 绑定真实提权 owner，许可不随单次连接或 TTL 消失；仅同 owner 释放或内核证明 owner 退出后释放。已完成。
6. **更新编排。** 先 staging，再取得维护锁和复验目标；准备 daemon 后退出相关客户端、强退手动 daemon 或 SCM 停服务，验证整树退出后替换。服务恢复与新版验证沿用现有流程，保留部分发布事实。未知或不支持准备的 daemon 要求先正常关闭。已完成。
7. **文档。** 同步中英文 README、术语和旧 relaunch 设计的取代关系，说明首次由旧 updater 升级仍执行旧行为。已完成。

行为回归按 Red–Green 实施。已观察到的正确 Red 包括：缺少启动清理、精确 stash 未删除、准备未等待在途工作、旧 Windows child 无后代等待能力、卸载不接受锁文件、staging 失败仍提前停止运行时；相应实现后的目标测试通过。

## 验证记录

- Windows 映射镜像、Restart Manager 身份与退出、core 整树退出使用合成子进程，相关原生回归重复 10 次通过。
- mutation 缓存超过 256 个并行执行、嵌套已接收工作、prepare 超时/恢复、owner lease、真实 named pipe、启动 Ready、无文件日志时 F2 原始详情均有回归。
- `CGO_ENABLED=0`：Windows、Linux、macOS 的 amd64/arm64 六目标编译通过。
- `go vet ./...` 通过。
- `python -m pytest scripts/test/test_unix_layout_security.py -q`：74 passed，4 skipped（Windows 不运行 Unix root 环境分支）。
- 全仓测试、race 和最终 lint/格式检查在提交前收口，最终结果记录于 PR。
- 当前本地 runner 未提权，受保护外层 daemon Job 的原生测试明确跳过；提权 CI runner 会执行。编排用 fake 服务验证，不安装、停止或修改开发机真实服务。真实 SCM/控制台与真实 mihomo 属于另行授权的 testenv。

## 提交、评审与合并

用户已授权本次代码提交、推送、PR 和满足条件后合并，不重复请求许可。

1. 检查完整 diff 与测试结果，创建带 DCO signoff 的中文 Conventional Commit，PR 指向 dev，关联 #282。
2. 检查 CI、ruleset 与已安装 bot；有实质反馈就修复并补回归，每次以最新 head 重新验证。
3. CI 或可用 bot review 未通过时，每 **10 分钟**检查一次。额度耗尽、未安装或明确不支持的 bot 记录为不可用，不能冒充已审通过。
4. 所有适用 CI 通过、可用 bot 完成且没有未解决的实质问题后，按匹配的 head SHA 正常 squash merge，不绕过检查或修改保护。
5. 合并后核验 PR 的 MERGED 状态、merge commit 与远端 dev 包含关系。
