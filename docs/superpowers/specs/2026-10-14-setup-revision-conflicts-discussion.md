# Setup revision 冲突讨论记录

## 已确认范围与行为

- 本期只修复 setup，普通 TUI/CLI 和其他客户端同类问题由 [Issue #349](https://github.com/mihari-proxy/mihari/issues/349) 跟进。
- 无关后台变化不应使 setup 操作失败。
- 相关输入变化但不改变用户意图时，可有界地重新准备。
- 最新方向（Q8 A）：setup 管理的全部项目以本次提交目标为准，覆盖同一项目的并发选择，不额外确认；保留其他订阅条目、面板和 setup 未涉及的设置，不重置整个实例。
- setup 添加并启用订阅 A 时可覆盖 B 的当前生效配置，保留 B 条目。
- 覆盖全部 setup 步骤：端口保存、核心、订阅、GeoIP、完成标记（Q3）。
- 跳过订阅或 GeoIP 时保留现状，只有明确提交的目标参与覆盖（Q9）。
- 已有资源满足目标时直接复用，不强制重新下载安装（Q10）。
- 按步骤提交，后续步骤失败不回滚此前已成功步骤；失败步骤保留最后有效状态与用户输入，重开引导时从已有成果继续（Q11）。
- 采用显式 setup 执行模式，新增可选请求标识与能力声明；既有 `if_revision` 仍表示全局版本，普通 TUI/CLI 行为不变（Q12，用户已确认公开 JSON 扩展方向）。
- setup 添加并启用订阅由 daemon 在同一步事务中完成，失败不留下仅完成添加的半成品（Q12）。
- 相关输入变化时，首次准备之外最多自动重新准备两次，复用仍有效下载；无关后台观察不触发重做或消耗次数。仍不稳定时保留输入与已有成果，提示稍后重试；取消停止重做；通信中断先确认原操作结果，不直接重提（Q13）。
- 新客户端连接不支持 setup 模式的旧 daemon 时，执行前提示更新并重启 daemon，保留输入，不退回旧写入方式；只读检查仍可使用（Q14）。
- setup 优先只作用于该步提交；提交后的新操作正常生效，完成标记不重新应用此前步骤（Q15，用户答复“en”按同意记录）。
- 当前 setup 不新增核心通道选择。daemon 接受核心步骤请求时捕获已保存的通道作为目标，空设置按既有 stable 默认处理；下载解析得到具体制品后固定该制品，不因上游发布新版重选（Q16）。
- Country 和 ASN 都有效可用即可复用 GeoIP，不因时间较旧或上次更新失败强制下载；新鲜度交给现有后台更新机制（Q17）。
- Q4 此前确认同通道可用核心直接复用、异通道或不同来源先确认；后一规则已被最新“setup 全量覆盖”方向取代。满足目标的已有资源复用依照 Q10。
- 用户已确认设计并授权在独立 worktree 中完成实现、提交和创建指向 dev 的 PR。

## VM 实际证据

通过已有 SSH 配置只读检查 Ubuntu VM，daemon 为 v0.9.7-dev.6。以下时间使用 VM 自身时钟，不能与宿主当前日期直接等同。

来源：`/var/lib/mihari/data/logs/mihari-daemon.log`。

| VM 时间（+08:00） | 操作 | 日志结果 |
| --- | --- | --- |
| 2026-10-11 16:38:24.925 | core.install | revision_conflict，expected_revision=1，current_revision=2 |
| 2026-10-11 16:38:47.220 | core.install | 重试成功 |
| 2026-10-11 16:39:56.643 | subscription.add | revision_conflict，expected_revision=5，current_revision=7 |
| 2026-10-11 16:40:14.344 | subscription.add | 重试成功 |

TUI 启动记录为 16:38:08.818，GeoIP ASN 文件 mtime 为 16:38:08.871；mihomo 首次启动日志为 16:38:41。首次 core 冲突的时间线与 GeoIP 后台提交吻合，排除该次冲突来自已运行核心的健康观察。订阅冲突与后续核心状态及日志首次观察的递增路径吻合。日志未记录每次 revision 的来源，以上来源归因属于代码与时间线推断，不能视作逐次递增日志实证。

## 已核对的实现事实

- `if_revision` 目前表示全局状态版本，不能静默改成操作域版本。
- setup 安装核心、添加订阅和更新 GeoIP 使用页面先前读取的全局 revision；耗时准备后检查可能拒绝结果。
- 核心现代更新路径还有配置 generation 与更新 reservation，但 legacy candidate 提交路径不能直接假定已有等价保护。
- 新订阅提交向当时目录追加，生成运行配置的候选另有 generation 检查；首次添加是否自动激活还取决于提交时目录状态。
- 重试必须区分真正被拒绝的操作和未知提交结果，不能重复提交已成功的 mutation；使用旧 operation ID 可能重放缓存的失败。
- 当前 setup 没有核心通道选择控件；核心缺失时 CoreStatus.Channel 可能为空，不能据此将已保存的 alpha 推断成 stable。本次核心目标的捕获时点需明确。
- 当前 GeoIP 复用条件为 Country 与 ASN 均可用，不要求最新或没有历史更新错误。
- 旧 daemon 严格拒绝未知 JSON 字段，因此能力检查必须先于带新字段的写入。端口恢复是受限 onboarding surface，不应因新 setup 模式扩大到核心、订阅或完成标记写入。

## 已确认执行方案

### 协议与适用范围

在现有本地 /v1 写请求增加可选 `execution_mode` 字段；省略表示既有行为，`setup` 表示按本次 setup 目标执行。适用请求仅限 onboarding 更新、核心安装、订阅添加和 setup 内已保存订阅的显式刷新、GeoIP 更新；不扩展普通设置、核心重装、任意订阅编辑等入口。具体 DTO 复用不能让同一字段在其他 mutation 上意外生效；未知值及不适用入口显式拒绝。

增加 `setup-target-v1` 能力，表示对应已开放资源支持 setup 目标执行语义，不表示开放完整 setup 权限。TUI 在写入前检查该能力，并继续按既有资源能力判断可用步骤。受限端口恢复只在其端口写适配器实现新语义时声明支持，仍拒绝核心、订阅、GeoIP 以及 Complete 字段，不扩展受限边界。浏览器 gateway 不提供此执行模式。

Setup 新请求不携带 `if_revision`。旧字段的含义保持全局严格前提：若其他调用方同时传入它，仍必须匹配；不得因 setup 模式静默忽略或改写其含义。旧客户端省略新字段，仍走原路径。旧 daemon 缺少能力时，新 TUI 保留草稿并提示更新、重启 daemon，不先发送未知字段再降级。

### 各步骤的目标与保留范围

| 步骤 | 本次目标与覆盖 | 保留/复用 |
| --- | --- | --- |
| 端口 | 本次提交的三个端口值；每次在最新设置上应用这些字段，完成校验再保存 | 其他设置保留最新值；仍遵循已有重启提示与重连流程 |
| 核心 | 请求接受时捕获的保存通道；需要下载时固定首次解析的制品身份 | 满足目标且通过当前生成配置校验的可用核心复用；保留既有本地核心接纳政策，不增加官方来源证明要求；不能仅凭缺失核心时 DTO 的空通道判断 stable |
| 订阅 | 本次填写的名称、来源及文件引用确认；成功后新条目成为当前订阅并应用其配置 | 向最新目录加入条目，保留其他条目；运行配置使用最新未由 setup 覆盖的管理设置 |
| 已保存订阅刷新 | 用户已明确选择重试的条目，刷新并使其满足本步目标 | 不因操作重试重复创建条目；若原对象已删除或身份不符，保留草稿并报告具体原因，不复活旧对象 |
| GeoIP | Country 与 ASN 均通过现有验证并可用 | 提交前重新检查；后台已经完成准备时直接复用，不要求最新时间戳或清除历史错误 |
| 完成标记 | 只提交完成引导 | 不补写端口、核心、订阅或 GeoIP，不重新覆盖此前成功步骤 |

跳过步骤不产生对应写入。进入订阅步骤时已有订阅仍按现有 UI 展示与继续，不强制用户重新添加；只有明确提交新订阅或刷新才应用新目标语义。Setup 不清空实例、不删除其他订阅、不改面板或其他未涉及设置。

### daemon 执行与并发

每步仍由 Manager 作为唯一执行 owner，经既有统一 mutation 路径提交。全局 revision 照常更新，但不作为未传 `if_revision` 的 setup 请求拒绝依据。业务操作 ID 在一步内固定，内部重准备不产生新的对外 mutation；同一请求重放继续使用已有去重/结果机制。

耗时下载、解析、解压、生成与校验在提交临界区外执行。提交时核对真实资源身份、相关配置输入和候选完整性；不直接把过期候选当成“全量覆盖”写入。准备后相关输入变化时，在最新状态上重新准备受影响的部分，同时保持本次目标；不能把其他客户端的新选择重新捕获为 setup 的新目标。

准备和提交的保护须覆盖实际使用的所有核心路径，不能假定 legacy Candidate.Commit 已有现代更新路径的 generation 防护。核心替换继续遵循可信制品、生命周期所有权、journal、恢复与配置验证边界；如果另一操作正占有更新/恢复事务，不能绕过 reservation 强制替换。可重试的占用按既有可取消等待机制协调，超过已有操作时限时说明占用原因，不把它伪装成普通 revision 冲突。

核心健康观察、日志观察、无关 GeoIP 提交不得使 setup 失败或消耗重准备次数。如果日志级别等真正参与生成配置的输入变化，则使用最新设置重新生成，而不覆盖该字段。即使内部仍以较宽的配置 generation 做失效检测，重准备也应限定在确有依赖的变化，避免把无关观察等同于业务冲突。

订阅添加和激活作为同一步事务：使用提交时最新目录构造包含新条目且 ActiveID 指向它的候选，按最新管理设置生成并校验运行配置，经既有原子替换、reload、回滚路径提交。失败不留下仅添加成功的条目，也不破坏此前有效目录或运行配置；回滚失败仍使用既有 degraded 及诊断边界，不能宣称保留原状态成功。

### 重准备、取消和失败

- 首次准备之外最多追加两次准备，只对相关输入失效或可重做的陈旧候选生效；网络、权限、校验错误不以此机制无条件重试。
- 能复用的下载与解析结果保留，重新生成和校验受影响部分；失去身份或完整性保证的材料必须丢弃，不能为省下载跳过验证。
- 每次准备检查 context；所有候选与临时资源有明确 cleanup。取消停止后续重做，已进入提交/恢复的工作仍等待既有 owner 收敛。
- IPC 中断或响应丢失先查询同一操作的 settlement；结果未确认前不新增请求、不创建第二条订阅、不把未知结果当作安全重试。
- 重准备耗尽保留草稿和此前成果，提示具体受影响项目仍被修改及稍后重试。正常后台初始化不得展示笼统的 State changed。
- 一步成功后，其他客户端之后提交的操作正常生效；setup 不持有跨步骤覆盖权。后续步骤失败不回滚以前步骤。

### 验证与交付范围

先为目标行为写失败测试，再作最小实现。并发测试使用事件/channel/注入 waiter，下载使用本地 fake/httptest，不连接真实订阅或真实 mihomo、不安装或重启 VM 服务。

必要回归场景：

1. 首次 core 安装与后台 GeoIP 提交重叠，正常完成且不因全局 revision 拒绝。
2. 订阅导入与核心启动/日志观察重叠，正常完成且不产生重复条目。
3. 端口、核心、订阅、GeoIP、完成标记均走新模式；跳过与旧模式保持约定行为。
4. 其他客户端先添加/启用 B，本次 A 添加并激活后保留 B 条目；失败回滚保留最新有效状态。
5. 相关设置变化重新准备并保留最新无关字段；首次加两次重准备的上限、取消、清理、耗尽提示得到验证。
6. 核心各实际提交路径、同目标资源复用、GeoIP 后台先完成、对象被替换或删除均有针对性测试。
7. 同一 operation ID 去重、未知结果结算和失败记录一次；不在内部重准备时重复记录最终失败。
8. 普通客户端的严格 if_revision 行为、旧请求/响应 envelope、错误码与退出码不变；旧 daemon 在新模式写入前被识别。
9. 受限端口恢复支持新端口模式但不开放其他操作，Web 不暴露新模式。
10. 单步成功后的其他客户端修改不会在完成引导时被覆盖，失败/中断后能够复用已有成果。

按变更风险运行目标包、相关 integration、全仓测试及必要 race/vet/CGO-free 跨平台编译；只有实际运行的结果才标记通过。文档更新 README 与现有架构说明，不修改 CHANGELOG；用户已明确授权完成验证后提交、推送并创建指向 dev 的 PR，不自动合并。

## 最终确认

用户已通过“在 worktree 中完成修复，然后提交 PR”确认以上方案并授权实施。如实现发现与已确认行为冲突的技术限制，先修订设计，不静默缩减行为。


## Review follow-up

Subscription persistence errors may occur after an atomic replacement has committed. Setup must settle a retained catalog before returning a normal failure: restore the previous catalog and matching cache, or report degraded when compensation cannot be confirmed. The latter blocks further mutations and stops the core before returning. Setup provides F2 diagnostics and explicitly directs the user to resolve the cause and restart the Mihari daemon; restarting only mihomo is insufficient, and restarting the daemon does not guarantee recovery. Global recovery workflows remain tracked by [Issue #218](https://github.com/mihari-proxy/mihari/issues/218).

Refreshing a saved subscription retains the selected object and source. Harmless metadata changes can be rebased within the same bounded preparation budget without downloading again. A conditional response tied to a changed cache requires fresh preparation. A deleted object or changed source is rejected with a specific error rather than resurrecting the object or silently following a different source.


Setup compensation checks whether cache replacement committed before restoring catalog metadata. A failed cache replacement retains the committed cache generation and reference base; a post-replacement warning permits matching old metadata to be restored. If catalog rollback leaves the new catalog committed, compensation reinstates its matching cached bytes. Compensation errors remain degraded and preserve their causes; repeated IO failures that prevent restoring a consistent pair must not be reported as a successful rollback.
