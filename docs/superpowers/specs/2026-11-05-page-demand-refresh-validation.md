# Issue #367：按页面需求刷新验证

日期：2026-11-06。实现分支：`fix/367-page-refresh`，基于 dev `fec11285`。

## 实现范围

按所选页面订阅普通快照，保留全局状态、core、诊断与流式消费者。共享同资源的在途读取，无消费者时取消；资源独立执行，普通快照总预算为 5 秒。进入页面、手动刷新及 mutation 后立即更新；代际检查拒绝旧结果。首轮显示 Loading/Fetching，成功空值与失败分开；相同数据跳过页面重建，诊断和 freshness 独立处理。

不包含 issue #368 的凭据生命周期重构。没有新增依赖或改变控制协议、持久化格式。

## 自动验证

- Windows：`go test ./...`、`go test -race ./internal/tui/... -count=1 -timeout=3m`、`go vet ./...`、`golangci-lint run ./...` 通过。
- Linux：交叉编译后在 WSL Ubuntu-22.04 执行 root TUI、session、System、WebGUI、Proxies、Rules、Subscriptions、UI 包测试，通过。UI 源码路径测试使用编译器 trimpath 映射到 WSL 路径。
- Windows、Linux、macOS 的 amd64/arm64 六个 CGO_ENABLED=0 构建通过。
- gofmt 检查无输出，git diff --check 通过。
- 回归覆盖慢资源隔离、资源取消及退出回收、代际失效、合并刷新、准确周期、F4 需求变化、首轮占位、旧候选禁止写入、异步结果页面归属、诊断保留、运行实例变化、连接历史保留、WebGUI 版本检查和 System 端口探测。

TUI 覆盖率（基线 → 实现，百分比）：session 87.0 → 88.3，root 87.7 → 87.2，connections 84.0 → 84.2，overview 82.4 → 81.0，proxies 89.2 → 88.9，rules 75.5 → 76.1，subscriptions 88.8 → 88.8，system 84.9 → 84.7，webgui 88.0 → 87.2，ui 81.6 → 81.3；logs/setup 不变。部分展示包增加了占位、失败及独立使用兼容分支，比例略降；未删减旧测试或排除生产代码，核心调度及交互路径有新增行为回归。

## Ubuntu VM 对比（较早实现构建）

在同一 VM、同一已运行 daemon/core 上仅替换独立 TUI 客户端，终端 140×40；预热后每页采样 60 秒，100% 表示一个逻辑 CPU。自动延迟测试临时关闭并在 finally 中恢复，未切换节点、刷新真实订阅或重启 core。该结果不是最终构建的 VM 验证，也不是服务安装/升级验证。

基线安装文件 SHA256：`6d27ad081144863482c6b97e93017c893d9ed0f899e28a8ff314a2d0dcb8c0e3`。
已确认修正周期后的实测构建 SHA256：`ae4ac47e482b55eb78c64e808e2c3c9ed84405b5b439793f98aea8ebbd88c4b7`。

| 页面 | TUI CPU 基线 → 实现 | daemon CPU 基线 → 实现 | core CPU 基线 → 实现 |
| --- | --- | --- | --- |
| Overview | 11.45% → 8.07% | 2.32% → 0.77% | 0.77% → 0.17% |
| Proxies | 12.93% → 8.08% | 2.30% → 0.67% | 0.75% → 0.73% |

这一单次样本中 TUI 降低约 30% / 38%。core 的独立活动会波动，不将其差异归因于本变更。页面 3–8、F4、F2 可打开，正常退出，daemon health 正常，偏好已恢复。

HTTP 请求量另用临时构建 overlay 的 transport 计数，未把计数开销混入 CPU 采样。仅记录 method/path，不记录 header/query/body/凭据。基线源码为与合并树相同的 `1223849`；请求统计与已安装基线二进制身份分别记录，不宣称二者为同一文件。

| 每 10 秒请求 | 基线 | 实现 |
| --- | --- | --- |
| Overview | 130 | 72 |
| Proxies | 130 | 54 |

必需的每秒端点保持 10 次/10 秒；Overview 不再查询 proxies/routing/rules/providers/egress，Proxies 不再查询 rules/providers/sysproxy/tun/egress/webgui。preferences 为 2 次/10 秒；Proxies 页 subscriptions 为 2 次/10 秒。strace 的 credential 文件打开次数没有当作 HTTP 请求次数使用。

## 最终补测限制

最终 Linux amd64 构建 SHA256：`622d16f60dabc855731ce9780c29c2e7939cf02abe29b2968fcdd063c82ff4c7`。最终本地及 WSL 检查已执行；2026-11-06 SSH 多次返回 `Connection closed by 192.168.247.128 port 22`，无法补跑此二进制的 VM CPU/交互测试，也无法取回另一轮测量报告。以上只采用此前已确认完成且恢复偏好的实测数据。恢复 VM 连接后应补测最终构建。

根据用户后续授权提交并创建指向 dev 的 PR；未修改 CHANGELOG，主工作区原有 `.gitignore` 修改保留。CI 和 bot review 结果以 PR 最新提交上的检查记录为准。
