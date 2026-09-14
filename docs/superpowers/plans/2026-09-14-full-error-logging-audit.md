# 完整错误日志审计总表

工作目录：`.worktrees/full-error-logging`；分支：`feat/full-error-logging`；创建基线：`e2865d04`，已无冲突 rebase 到 `origin/dev` 的 `8141430`。

用户要求使用 subagent 做一次全量扫描并修复。本轮按互不重叠的代码模块分组：先固定候选清单，再关闭候选；不把旧版全仓测试通过视为全模块审计完成，也不在每批修复后重启全仓发现。

## 本轮责任与进度

| 责任组 | 完整模块范围 | 当前状态 | 逐项证据 |
| --- | --- | --- | --- |
| 公共日志（主 agent） | `diagnostics`、`logging`；集成/安全测试协调 | 公共链路及三项差异复核修复完成；全仓普通测试通过 | [公共链路登记](2026-09-14-logging-scan-common.md) |
| 资源与存储 | `core`、`geoip`、`panel`、`subscription`、`config`、`state`、`onboarding`、`preferences`、`update` | 首轮 76 个生产文件、32 组候选已收口，组内套件通过 | [资源与存储登记](2026-09-14-logging-scan-assets.md) |
| 执行与系统操作 | `app`、`runtime`、`daemon`、`supervisor`、`platform`、`service`、`sysproxy`、`tundetect`、`elevate`、`cli`、`buildinfo`、`cmd/mihari` | 首轮 277 个生产文件已收口；生命周期增量回归通过 | [执行与系统登记](2026-09-14-logging-scan-owners.md) |
| 客户端与界面 | `control`（所有子包）、`mihomo`、`web`、`tui`（所有子包） | 首轮 134 个生产文件已收口；响应写入、关闭和协议原因增量回归通过 | [客户端与界面登记](2026-09-14-logging-scan-clients.md) |

每组登记必须包含实际文件/入口、原因丢失或记录点、处置依据、回归证据，以及无现成文件 reporter 的已接受边界。源码枚举数量只是扫描覆盖证据，不替代行为验收。

## 已确认的出口边界

- 普通 CLI 无文件 reporter 时不创建日志，不探测历史文件。
- TUI cleanup 后安装/self-update、logger 初始化前的路径无文件出口；不得借用已关闭 logger。
- 有主失败时保留独立清理 cause；成功后的清理失败若已有 reporter 则 WARN，不能仅以“不改变主结果”为理由丢弃原因。
- 日志资源自己的故障使用独立 FailureReporter；其最终终端输出保留既有安全文本、限速与不递归规则。
- 文件日志/快照/ZIP 保留原文，公开 API/JSON/状态/事件继续独立；真实环境服务、订阅和核心不用于默认测试。

## 当前可核实的验证

- 各组新增行为均有聚焦红→绿证据；第一遍统一验收发现的 unchecked 返回值已修复。原文导出拒绝无效 UTF-8 的既有 fuzz 样例预期已同步，目标回归通过。
- 同步 `dev` 后 `go test ./...` 全部通过，临时输出 `mihari-verified-all.txt`。
- 同步 `dev` 后 `go vet ./...`、`golangci-lint run ./...` 通过，lint 为 0 issues；`gofmt -l cmd internal` 与 `git diff --check` 无输出。
- Windows/Linux/macOS × amd64/arm64 六目标 `CGO_ENABLED=0` 构建均通过，临时输出 `mihari-verified-cross-results.txt`。
- `python -m pytest scripts/test/test_unix_layout_security.py -q`：74 passed、4 skipped；未运行真实服务、真实订阅或真实 mihomo 验证。
- 最终 `go test -race ./...` 全部通过，临时输出 `mihari-verified-race.txt`；结果来自同步 dev 后的当前实现。
- 本地验收已完成；PR 的 CI 与 bot review 状态以对应 PR 实时结果为准，Actions 查询间隔为 5 分钟。本地 Windows 测试和跨平台编译不能替代 Linux/macOS 原生 CI。
