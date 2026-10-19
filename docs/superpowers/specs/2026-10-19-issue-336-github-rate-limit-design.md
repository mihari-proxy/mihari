# Issue #336：GitHub 限流提示设计

状态：代码、文档及验证完成；用户已进一步授权提交、推送并向 dev 创建 PR。

## 来源与工作区

- Issue：https://github.com/mihari-proxy/mihari/issues/336
- 基线：origin/dev，fad4470c。
- 分支：feat/336-github-rate-limit。
- worktree：.worktrees/feat-336-github-rate-limit。
- 现行约束：AGENTS.md、docs/architecture.md、统一错误诊断设计。
- AGENTS.md 引用的 2026-08-03 架构设计在当前基线不存在。

## Issue 已明确的目标

1. GitHub 确认限流时，主要错误提示、TUI 状态及操作结果、Diagnostics 概要和 CLI 失败输出直接说明限流，并保留失败操作上下文。
2. 覆盖 Mihari 更新、mihomo release/安装/更新、Web 面板 release/资源及其他 GitHub 下载入口。
3. 支持 403 且正文明确限流、429、secondary rate limit；普通权限 403 和网络错误不能误判。
4. 原 Issue 要求有可用重试时间时展示；访谈中用户明确将本次范围收敛为统一 `Please try again later.`，不处理重试时间。
5. 保留原始 HTTP 状态、URL、正文和原因链，沿用既有诊断采集上限。
6. 现有 JSON envelope、错误码、退出码不变；不增加 GitHub 凭据配置或无界重试。
7. 验收使用本地 fixture/httptest，不连接真实 GitHub。

## 官方依据

通过 Context7 CLI 查询 GitHub REST API 文档：

- https://docs.github.com/en/rest/using-the-rest-api/troubleshooting-the-rest-api
- https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api

主限流可返回 403 或 429，并提供 x-ratelimit-remaining: 0；次级限流可返回相同状态，并在错误信息中说明。Retry-After 提供等待秒数；主额度耗尽时 x-ratelimit-reset 提供 UTC epoch 秒。后者不能在次级限流且主额度未耗尽时被当成恢复承诺。官方建议的无时间头等待策略不等于本次响应提供的恢复时间。

## 决策树

第一轮结果：

- Q1 已确认并进一步收窄：仅处理程序内部固定从 GitHub 获取的资源，不处理用户自定义订阅地址，即使该地址指向 GitHub。不扩展到安装脚本；发布工具不新增专门修改，共享运行时包的提示改进可能自然影响其调用者。
- Q2 经截图进一步明确：已有红色错误说明统一使用 `GitHub rate limit exceeded. Please try again later.`，不再显示 `fetch mihari release failed` 等泛化提示。失败操作上下文由行标签及 F2 原始诊断保留；CLI 与 Diagnostics 概要使用同一限流文案。
- Q3/Q4 已确认：不解析 Retry-After/reset 或展示等待时间、恢复时刻及倒计时；统一 `GitHub rate limit exceeded. Please try again later.`，保留操作上下文。不改变现有请求、重试、回退、调度和缓存策略，不新增冷却或自动重试。
- Q5 已确认：采用下文全部三条识别规则。
- Q6 经截图确认：`Failed` / `Check failed` 等短状态保持。System 页 Update Mihari 的 Failed 后红字及页面顶部红字均替换为统一限流文案；F2 保留详细错误日志。不为 Core/Panel 的 Check failed 新增行内文字、弹窗或版面区域。

当前没有待定设计分支；下面是供最终确认的实施方案。

## 具体方案草案

### 固定资源范围

| 资源 | 入口 |
| --- | --- |
| Mihari release 列表/latest/tag、校验清单、程序制品、官方发布证据 | internal/update/self.go、official.go |
| mihomo release、制品身份复查、普通/受保护安装、迁移核心下载 | internal/core/release.go、release_target.go、install.go、protected_prepare.go、migration.go |
| 内置 Web 面板 release/branch、archive 与 release assets | internal/panel/release/github.go、service.go |
| GeoIP Country/ASN 数据库与校验文件 | internal/geoip/downloader.go、service.go |

不在范围：订阅下载及其定时刷新、mihomo 自行下载的 provider、AList channel index、安装脚本和专用发布工具请求。通用 HTTP/诊断 formatter 不根据任意 URL 或任意错误文本自动推断 GitHub 归属。

### 识别与错误组织

- 在 internal/diagnostics 中增加小型 GitHub HTTP 限流识别/受控概要能力，仅由上述固定 GitHub 入口显式调用；不改变通用 HTTPError.Error 行为。
- 已确认判定：429；或 403 且 X-RateLimit-Remaining 为 0；或 403 且有明确的主/次级限流正文。普通 Forbidden、其他 HTTP 状态、传输/读取失败不据此推断限流。Remaining 仅用于识别，不处理 Reset 或 Retry-After。
- 正文识别使用已采集的有界响应内容，优先 GitHub JSON message，兼容固定资源服务的明确限流文本；不使用任意包含 rate/limit 的模糊词匹配。
- 在 HTTP 响应边界生成受控限流原因；限流时最外层 APIError.Message 使用统一文案，GeoIP 同步其受控 failure reason，使 CLI、TUI、F2 概要收到同一提示。原始操作上下文留在既有包装原因链与 HTTP Operation 字段；不得因替换概要而丢弃。
- 保留各路径既有错误码及 Details 键。GeoIP 运行时现有 CodeInternal、迁移路径 CodeDataFailure 等不因提示改进改成 network_failure。
- 保留 HTTP 状态、实际 URL、原始正文、截断标记、读取/关闭错误及 errors.Is/As 链。正文只读取一次，沿用 256 KiB 上限。
- 不新增依赖、持久化字段、协议字段或错误码。

### 展示

- CLI 文本和 JSON error.message、Diagnostics summary、已有红色错误说明及操作结果使用统一限流文案；原始操作、状态、URL、正文和原因链继续保留在详情。
- 截图锚点：System → Update Mihari 行保持 `Failed`，其后的红字变为 `GitHub rate limit exceeded. Please try again later.`；同页顶部红色错误说明显示相同内容。
- Core 和 Web 面板版本行继续只显示短的 `Check failed`，不增加受控概要状态或新的展示区域；其 F2 概要明确限流，详细日志保留。
- Setup 已有错误说明包含 try again later，不再另显示一行重复建议；核心限流不附加通用 AIO 下载提示。普通网络失败保留原提示。
- 不自动打开 F2、不抢焦点、不引入倒计时，不使用响应时间头抑制后续检查。

### 实施步骤与验证

1. 先为共享识别规则写失败测试，再实现最小判定和固定提示。
2. 按 update、core、panel release/assets、GeoIP 逐条补回归测试，确认各入口主要概要变化且原始原因/旧错误码保留，再接入生产代码。
3. 补 System 行内红字及页顶红字、Setup、CLI 文本/JSON、F2 概要回归测试；断言 Core/Panel 的 Check failed 保持不变，单一 JSON envelope、原错误码/退出码与报告一次语义不变。
4. 负例覆盖普通权限 403、404/500、传输错误、成功响应及含普通 rate/limit 字样的非限流消息。带/不带/非法 Retry-After/reset 均输出相同提示，不触发额外请求。
5. 回归覆盖有界正文、read/close 合并失败、截断、终端转义、受控提示不直接展开响应原文、非 GitHub 范围入口行为不变。
6. 依次运行受影响包及相关 integration 测试，再执行 go test ./...、go test -race ./...、go vet ./... 和改动文件 gofmt 检查；不访问真实 GitHub 或真实系统服务。
7. 同步 README 中英文的用户行为说明，不修改 CHANGELOG。用户已进一步要求开发完成后提交 PR，使用当前功能分支提交、推送并向 dev 创建 PR。

当前选择可局部逆转，不为本次提示改进另立 ADR。

## 当前 retry 行为核查

以下是代码事实，不代表本次决定修改策略：

| 路径 | 当前行为 | 对 GitHub 限流的影响 |
| --- | --- | --- |
| update/core/panel/GeoIP 单次 HTTP 请求 | 直接调用 http.Client.Do，应用层没有针对 HTTP 403/429 的重试循环 | HTTP 失败结束当前调用，不消费响应的 Retry-After/reset |
| Mihari dev release 列表 | 分页读取，每页失败立即返回；选中 release 无 assets 时再按 tag 查询 | 分页与补查不是失败重试；失败不会继续下一页 |
| 订阅 Auto 下载 | 代理网络失败才回退直连，最多两条传输路径 | HTTP 403/429 不触发回退，不能把限流错误包装成可回退网络错误 |
| 订阅定时刷新 | 失败后默认 1 分钟指数退避，最高 30 分钟；另加以 1 分钟为基准的稳定 ±5% 抖动 | GitHub 来源同样采用该策略，目前不读取限流头；daemon 重启会丢失内存中的失败计数和 nextAllowed |
| GeoIP 定时刷新 | 启动立即检查，之后默认每 24 小时检查；缺失或超过默认 30 天才刷新 | 失败不立即重试，未来检查仍可能重新请求 |
| TUI 面板版本检查 | 请求中不重复；成功结果缓存 5 分钟；失败不享受该缓存 | 后续页面检查触发可以立即重新请求 |
| TUI System 版本检查 | 切换到 System 时 Load 触发；请求中防重，无结果缓存/冷却；Enter 进入页面使用 LoadStatus | 离开再切回可以立即重发；没有定时版本重试循环 |
| mihomo provider discovery | 最多 3 次、总预算 4 秒，支持 RetryAfter | 这是本机 mihomo controller 请求，不能误当成 GitHub 请求策略 |

证据：internal/update/self.go、internal/core/check.go、internal/panel/check.go、internal/subscription/downloader.go、internal/subscription/scheduler.go、internal/geoip/scheduler.go、internal/tui/pages/webgui/version_check.go、internal/tui/pages/system/model.go、internal/tui/pages/system/core_check.go、internal/runtime/proxy_catalog.go。

页面触发证据：internal/tui/model.go:1249-1255、1261-1290；面板 Enter 使用 Load，失败的版本检查也可能再次触发。动画 tick 不触发远端版本请求。

## 实施前置条件

依 grilling 技能完成逐轮设计确认后，用户明确回复“进入方案实现”。完成实现及验证后，用户进一步回复“开发完成之后提交PR”，授权本功能提交、推送及创建 PR；不自动合并。

## 实现记录

- 新增 internal/diagnostics/github.go，固定 GitHub 入口显式调用；普通 HTTP 错误和自定义订阅不参与识别。
- JSON 正文仅从顶层 message 提取判定证据，不使用 documentation_url 或嵌套内容；在 message 已完整采集但后续正文被截断时仍能识别。
- update/core/panel 的现有 HTTP 采集边界复用原始正文后生成统一概要，原概要保留在原因链中。GeoIP 继续通过其既有 failure reason 和运行时错误映射，保留 CodeInternal。
- System 行内原有 48 字符摘要会截掉这条固定提示的尾部；统一限流文案改按扣除行标签、间隔及 Badge 后的实际剩余宽度截断。宽窗口显示完整提示，窄窗口带省略号，避免边框静默裁切。其他错误摘要规则及所有 Badge 保持。
- Setup 的稍后重试建议已包含在概要中，不另显示重复建议；限流失败不附加 AIO 网络环境建议。重试时间头不读取，不增加状态或重试。
- README 中英文与 CONTEXT.md 已同步。

新增/扩展回归覆盖：共享识别正负例及无效时间头；Mihari 六类 HTTP 入口；核心 release、普通下载、迁移下载、制品复查及复查后的受保护下载；面板 release/branch 与 archive；GeoIP checksum/database 及运行时错误码；System 双处红字；Core/Panel 短 Badge；Setup 建议；CLI 文本/JSON envelope；本地 owner 日志与 F2 快照、报告一次；自定义 GitHub 订阅保持原行为。

## 验证结果

初始实现于 2026-10-19 在 Windows 开发机执行：

- 定向测试先因仍输出旧的泛化提示而失败，接入实现后通过；System 48 字符截断及 Setup 网络建议分别由回归测试先复现后修复。
- 受影响包测试通过。
- `go test ./...` 通过，包含 `internal/integration`。
- `go test -race ./...` 通过。
- `go vet ./...` 通过。
- 所有改动 Go 文件的 `gofmt -l` 无输出，`git diff --check` 通过。
- `CGO_ENABLED=0` 构建 Windows amd64、Linux amd64、darwin arm64 均通过，产物位于系统临时目录。
- 未运行真实 GitHub 限流、真实 mihomo 或系统服务验证；回归测试均使用本地 fixture/注入 transport。
- 未改 `CHANGELOG.md`；提交、推送和 PR 按用户后续授权执行。

## PR #353 首轮评审修正

首轮 CI 全部通过；Pullfrog 未发现新问题，Cubic 提出四项建议，经核查均处理：

1. GeoIP 的 ServiceOptions/Downloader 也支持自定义来源。只有初始 URL 精确匹配四个内置数据库/校验资源，且最终响应 URL 仍为无 userinfo 的 HTTPS raw.githubusercontent.com 默认 origin，才使用 GitHub 专用分类。真实 client 重定向的本地 transport fixture 覆盖自定义来源、同域自定义路径、域名伪装及双向跨域重定向，不改变下载或重定向策略。
2. System 限流说明按实际行内可用宽度显示，新增 50/60/70 列省略号回归，原 160 列双处完整红字回归保留。
3. Setup 概要已包含等待建议，移除重复的 advice 行；普通失败建议不变。
4. 时间头测试同时覆盖 429 与普通权限 403，包含有效秒数、未来 epoch、HTTP-date、无效及超大值；普通 403 不因时间头而被归类为限流。

上述行为修复均先通过回归测试复现，再进行最小生产修复；时间头负例用于补足已有正确行为的覆盖。

本轮本地验证：`go test ./...`、`go test -race ./internal/geoip ./internal/diagnostics ./internal/tui/pages/system ./internal/tui/pages/setup ./internal/runtime ./internal/integration`、`go vet ./...` 均通过；`gofmt -l cmd internal` 无输出，`git diff --check` 通过。新提交的全平台 CI 与增量 bot review 由 PR #353 继续验证。
