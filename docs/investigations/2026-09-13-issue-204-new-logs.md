# Issue #204：2026-09-13 新日志排查

## 结论与证据强度

已在当前代码中复现 **provider 节点被送到普通节点测速接口，返回 404** 的缺陷。这是本次 TUI 全部 Failed 的强解释，优先级高于继续调整测速 URL 或 TUN。

用户现场的最终归因尚差一项证据：日志没有请求路径、HTTP 状态或节点所属 provider，不能直接证明其中 338 条简短错误全部都是 404。本文的 404 来自按上游接口语义构造的隔离复现，不能当作用户现场抓包。

## 输入与版本

- Issue：[mihari-proxy/mihari#204](https://github.com/mihari-proxy/mihari/issues/204)，含截至 2026-09-13 的评论。
- 输入 ZIP：`mihari-logs-20260913-010720+0800.zip`（省略本地用户路径）。
- SHA-256：`c8bd6ab68c6343d1ce8482f93b1d220719eced55e5b98488269b8d8502e876e7`。
- manifest：导出时间 2026-09-13 01:07:20 +08:00，实际选择的是 `last_24h`，不是评论要求的最近 60 分钟。
- daemon 367 行、TUI 60 行、mihomo 4979 行；均无无效行跳过。未将原始流量日志复制进仓库。
- 用户称版本为 v0.9.3；manifest 本身不记录 Mihari/mihomo 版本。代码核对基于本地 `aa93998`，上游接口语义核对基于 mihomo `v1.19.30`，不据此宣称已验证用户实际内核版本。

## 新日志事实

daemon 中有 346 条 `request_failed`，其中 8 条附带 connectex 连接拒绝，338 条仅有 `api error (upstream_failure)`。后者呈现明显批次：

| 时间（UTC+8） | 条数 | 首末错误间隔 | daemon 文件行号 |
| --- | ---: | ---: | --- |
| 09-12 16:19:55 | 48 | 490 ms | 13–60 |
| 09-12 16:23:08–09 | 48 | 426 ms | 61–108 |
| 09-12 23:06:55 | 1 | 单条 | 109 |
| 09-12 23:07:00 | 48 | 417 ms | 110–157 |
| 09-13 00:29:39 | 49 | 434 ms | 165–213 |
| 09-13 00:46:26 | 48 | 407 ms | 219–266 |
| 09-13 00:47:43 | 48 | 441 ms | 267–314 |
| 09-13 01:05:50 | 48 | 426 ms | 320–367 |

这些间隔是首末错误记录的跨度，不是每个请求的实测耗时。结合 TUI 并发上限 5、默认内核 timeout 5000 ms，反复约 48 条错误在半秒内结束，更符合快速拒绝，无法用“48 个节点都等满超时”解释。

另有独立现象：

- 16:12:07–22 的 8 次连接拒绝早于 16:12:33 的 core.install 成功和 controller 开始监听；不宜用它解释数小时后的批量 Failed。
- mihomo 第 21–22 行有 DNS TCP/UDP 1053 端口冲突，第 24 行有 TUN 接口创建冲突，时间均为 16:13:29。说明当时存在资源冲突，日志不足以判定占用者。
- TUI 有重启/断流、named pipe 文件不存在；daemon 另有一次 panel.activate invalid_state 和一次订阅刷新 EOF。不能将其自动并入测速根因。
- mihomo 外层 4979 行均是 INFO，但内层包含 330 条 warning、35 条 error；没有 delay/URLTest 诊断。控制面日志已进入此次导出，但上游 HTTP 状态仍丢失。

## 调用链缺陷

1. TUI `delayTestLeaves` 从组成员列表拿到叶子名称，`startDelay` 对每个名称调用 `DelayProxy`，请求体为空。
2. control server 可以正常解析组的 test URL，再经 Manager 调用 mihomo client。
3. [mihomo client](../../internal/mihomo/client.go) 的 `DelayProxy` 固定请求 `/proxies/{name}/delay`，没有解析所属 provider。
4. mihomo 的普通接口在 `tunnel.Proxies()` 查找名称；找不到直接返回 404，不执行 URLTest。[上游普通接口源码](https://github.com/MetaCubeX/mihomo/blob/v1.19.30/hub/route/proxies.go#L42)
5. provider 节点保存在另一个命名空间，专用单节点接口是 `/providers/proxies/{provider}/{name}/healthcheck`。[上游 provider 接口源码](https://github.com/MetaCubeX/mihomo/blob/v1.19.30/hub/route/provider.go#L28)
6. 用户公开 YAML 使用 `proxy-providers.subscribe` 和组 `include-all: true`，符合触发条件；公开 main 文件是可变参考，不能代替用户现场有效配置。[配置参考](https://github.com/Nullwhy/Mihomo/blob/main/mihomo/Nullwhy.yaml)

同一缺口还影响列表元数据：`orderedProxyGroups` 直接用 `proxies[name]` 取 Type/UDP/XUDP；当名字只属于 provider 时，得到零值，表现为类型空、UDP=false。组内能列出名字，并不代表它存在于全局 `/proxies` 映射。

面板有可用的差异路径：Zashboard 当前源码提供组测速及 provider 专用测速。日志没有面板请求，不能指定用户那次究竟用了哪个接口。[Zashboard API 源码](https://github.com/Zephyruso/zashboard/blob/main/src/api/clash.ts)

当前 CLI 的 `mihari proxy test GROUP` 使用组测速，与 TUI Ctrl+T 的逐节点路径不同。因此 Issue 中“TUI/CLI 面板”的说法不能视为已经证明 CLI 组测速也失败。

## 隔离验证

复现脚本：[scripts/diagnostics/issue204/main.go](../../scripts/diagnostics/issue204/main.go)。

在此 worktree 根目录运行：

```powershell
go run ./scripts/diagnostics/issue204
```

脚本使用真实 control client → server → Manager → mihomo client；两层 HTTP 都使用内存 transport，无监听、无公网、无真实 mihomo、无用户文件或系统服务操作。假控制器按已核对的上游路由区分普通节点、provider 节点和组。

实际输出的关键结果：

```text
Provider node is listed: name="Provider Node" type="" udp=false
Provider delay explicit_url=false: code=upstream_failure upstream_status=404
Provider delay explicit_url=true: code=upstream_failure upstream_status=404
Inline, group, and provider-specific controls: 42 ms (synthetic)
```

两次失败均生成 `request_failed` / `cause="api error (upstream_failure)"`，与新日志的简短错误形态一致。42 ms 是固定的假响应，不是网络测量。脚本以复现当前缺陷为成功条件，不是证明缺陷已修复的回归测试。

已执行：复现脚本、`go vet ./scripts/diagnostics/issue204`、`gofmt` 检查、`git diff --check`。没有修改生产代码，未运行全仓测试、race 或跨平台构建；未执行真实环境验证。

## 后续处理建议

修复应覆盖 provider 节点发现、身份解析与专用测速路由，并为“组成员只在 provider 中存在”增加回归测试。保持 daemon/Manager 所有权，由 mihomo 适配层封装上游 REST 细节。必须明确多个 provider 同名节点、普通节点与 provider 同名、刷新后身份变化的处理，不能随意挑第一个同名结果。

日志宜在不输出响应正文、secret、完整 URL 的前提下保留安全的上游状态及操作类别，避免下一份日志仍只剩错误码。若实现需要新增公开 CLI/JSON 字段，应先按 AGENTS.md 确认契约影响。

现有命令可提供低成本对照（由用户在自己的故障环境执行；本次未执行）：

```powershell
mihari proxy test AllProxy --url https://www.gstatic.com/generate_204 --timeout 5000 --json
```

如果该组测速正常而 TUI 单节点 Failed，将进一步支持此次定位。最终现场确认需要失败请求的 HTTP 状态，以及目标节点是否仅出现在 `/providers/proxies` 中；不用再次笼统地只调 Logging 到 debug。

本次仅形成诊断与可重跑复现，没有发布 Issue 评论、创建 commit、推送、合并或修改真实环境。原 main 工作区已有的 `.gitignore` 修改保留。
