# 统一 CLI/TUI 错误汇报与诊断详情设计

- 关联：Issue #197（范围由 setup 扩展至全局 CLI/TUI）。
- 创建基线：`origin/dev` / `6bd40d1`；当前开发基线：`ad6ecae`（已接入 #260、#262、#261、#263）。
- 工作分支：`fix/197-cli-setup-diagnostics`。
- 状态：产品决策已通过 grilling 确认；实现与入口核对已完成，已 rebase 最新 dev，本地最终验收通过，正在推进 PR 与远端检查。
- 执行方案：[分阶段任务与验证](../plans/2026-09-16-unified-error-reporting.md)；[覆盖与验收登记](../plans/2026-09-16-unified-error-reporting-audit.md)。
- 原先 #197 worktree 中的 2026-09-03 草稿保留，本文取代其实现方向。

## 1. 已确认的决策

1. 日志与错误汇报均不脱敏。错误携带的凭据、URL、路径、配置片段保留原文；不额外读取或转储无关配置。
2. 展示区分概要和具体原因，保留错误码及已有 operation ID。概要不是原始详情的替代品。
3. 文本 CLI 失败时直接分段打印概要和详情，不要求为查看原因重跑命令。
4. JSON 增加可选结构化诊断；沿用现有错误码、退出码和业务成功语义。
5. TUI 全局 F2 打开记录列表和可滚动详情，支持复制、Esc 返回。优先当前操作或页面，不自动抢焦点。
6. 覆盖主动操作、读取、后台任务、警告、部分成功，以及启动和清理出口。
7. 保留有界采集与明确截断标记；已采集内容在后续传输、展示、复制中不再删减或脱敏。
8. daemon 内存保留近期诊断，TUI 重连可重新查询；客户端本地诊断按当前进程保留。重启后的追溯依赖现有日志，不新增诊断数据库。
9. 每次实际汇报独立记录。不按文本合并、不统计重复次数、不关联自动恢复。沿用现有重试和实际执行 owner 的汇报时机。
10. 正常使用要求 CLI/TUI 与 daemon 同版本。旧端不支持详情时明确提示，保留原有功能，不重放 mutation。
11. 终端控制字符转义显示，复制保留采集到的原始文本。转义不属于脱敏，不执行错误文本中的终端控制序列。

## 2. 覆盖清单

以下每项同时检查失败、警告、部分成功、结果不确定及已经存在的取消行为。没有该功能的 CLI 不新增对应命令。

| 范围 | 入口及场景 |
| --- | --- |
| 全局启动与退出 | 参数/flag/命令错误；本地目录、凭据、权限初始化；TUI/daemon 启动、监听、ready、退出与清理；stdout/stderr 写失败 |
| IPC 与后台连接 | 认证、连接、超时、响应/流解码、重连、后台轮询、stale/degraded 状态 |
| Setup | onboarding 加载、端口探测/保存、core 读取/安装、订阅添加/刷新、GeoIP 读取/更新、完成、取消后 settlement/readback |
| Overview / status | daemon/config/core/订阅/WebGUI/sysproxy/TUN/service 汇总异常，最近操作的详情入口 |
| Core | 状态、安装、更新、通道、启动/重启、健康检查、自动重启失败 |
| Proxies / routing | 代理组/provider 读取、节点选择、模式切换、GLOBAL 候选、单个/批量测速、超时与部分完成 |
| Subscriptions | 列表/详情/URL 查看、新增、编辑、启停、proxy mode、刷新单个/全部、自动刷新、切换、删除；已保存但首次下载失败 |
| Connections | 连接流、关闭单个/全部、列偏好保存、GeoIP 查询 |
| Rules | rules/provider 列表、刷新单个/全部 provider、批量部分完成 |
| System proxy / TUN | 状态、启用/禁用/force、权限/占用/revision 冲突、应用与回滚 |
| WebGUI / panel | 状态、安装/更新/激活/回滚/卸载/重装、open URL、本地浏览器打开；mutation 后 reload 失败 |
| 系统配置及本地交互 | 端口、日志设置、偏好、revision 冲突；剪贴板、目录和链接打开 |
| 服务/安装/卸载 | OS 服务读取与 install/uninstall/reinstall/start/stop/restart；安装浮层 inspect/plan/repair/fresh；Unix service apply；安装校验子进程；全量卸载 |
| Mihari self | version/channel、更新检查、下载/校验、prepare/apply/discard、版本探测、服务副本同步、cleanup/relaunch，以及替换成功后的警告 |
| 日志/监控/导出 | logs/traffic 命令，TUI logs/traffic/memory/connections 流；导出、路径复制；日志 open/write/rotate/close 及独立 FailureReporter |
| 跨模块原因链 | 网络/HTTP 状态及已采集失败正文、文件/权限、子进程、校验、commit/reload/rollback/cleanup；包装和合并原因 |

CLI 生产入口核查范围：无参数 TUI、status、daemon、core、proxy、connections、rules、traffic、logs、sub、panel、sysproxy、tun、service、self，以及 Unix service apply、daemon 的 system-service/launchd-process-group/install-validation 入口。help/completion 的输出失败走公共出口。

`service install-status/install-plan/repair/fresh` 当前仅有源码及测试，生产 CLI 未装配；不借此激活这些命令。相关真实 TUI 安装流程仍在范围内。TUI 有九个页面，没有独立 Traffic 页面。

不包含浏览器面板自身的错误 UI、独立安装脚本、CI 工具、新增业务命令、自动重试策略重构。Web 后端已有错误的文件日志继续遵守不脱敏规则。真实订阅、真实 mihomo、系统服务安装测试不在当前授权内。

## 3. 现状与必要改动

- CLI `Execute` 已有公共文本/JSON 出口，但通常只输出 `APIError.Message`。
- `diagnostics.Wrap` 保留内部 cause，而 `Error()` 仍只返回概要；客户端不能靠调用 `err.Error()` 恢复 daemon 的原因链。
- 当前 IPC 传输 code/message/details，内部 cause 只进入日志。必须增加明确的诊断传输，不从日志字符串猜测或反查一次操作的唯一错误。
- TUI 已有可滚动、可复制的 ErrorDetail modal，但只有 Setup 接入。其他页面多数只保存短字符串。
- 文件日志/快照/导出已不脱敏；Setup、独立日志失败终端提示等仍有过滤或固定文案。
- session 的部分轮询在首次失败后直接返回。需要保留并上报实际执行过的读取失败，不能把失败只变成 stale；本期不改变轮询重试策略，也不虚构未执行读取的失败。
- 普通 CLI 没有文件 logger。诊断获取和展示必须独立于 logger 是否建立及日志级别。

原规范引用的 `2026-08-03-mihari-architecture-design.md` 在当前基线不存在；本设计结合现行 AGENTS.md、docs/architecture.md 与完整错误日志设计核对边界。

## 4. 统一诊断模型及所有权

区分仍可 `errors.Is/As` 的内部 error，与不可变、可传输的诊断快照。不得直接序列化 error 对象。

诊断快照的最小字段：

| 字段 | 含义 |
| --- | --- |
| id / instance_id / sequence | 产生进程内的记录身份、启动实例和递增位置，用于查询及避免同一记录被重复展示 |
| time / severity | 发生时间与 info/warning/error；严重程度不改变原有业务退出码 |
| component / event | 产生来源及动作；使用现有 reporter 提供的信息 |
| operation_id / object | 已存在的操作身份及对象，缺失时省略；不制造假的 mutation ID |
| code / summary | 已有分类及简短失败说明；概要不进行敏感信息替换 |
| detail | 包装上下文、原始原因链、合并原因和已有 HTTP 诊断/堆栈，不额外生成无关转储 |
| truncated / truncation_reason | 采集是否因字节、原因图或上游既有上限截断；不能声称截断内容完整 |

诊断 ID 与 operation ID 不等价：一次操作可产生多条独立警告。同一记录通过响应和历史到达 TUI 时按 ID 只插入一次；这属于传输幂等，不是按内容合并不同失败。

共享有界格式化能力放在 `internal/diagnostics` 或其明确子包，供日志、IPC、CLI、TUI 使用，避免依赖日志文件才能得到详情。现有 formatter 的循环检测、typed nil、合并原因和预算行为保持回归覆盖。不得引入 diagnostics 与 protocol 的循环依赖。

daemon/app 装配提供进程诊断 owner；业务执行 owner 沿用现有 Reporter 汇报。owner 分别向内存历史与文件日志发布：文件日志受日志级别控制，内存错误详情不受文件 logger 的开关影响。CLI 本地任务通过注入的同类收集出口获取详情，不新建日志文件。

当前 Reporter 不返回诊断 ID。实现必须让 owner 在发布前创建快照/ID，并通过错误或用例结果显式带回同一个快照/引用；不能在响应阶段按文本寻找历史记录。现有 operation metadata 位于 logging context helper，调整共享元数据归属时保持现有日志关联。当前 collectWarning 上报后即丢弃，实现需把 warning 同时保留在用例结果中，贯穿成功响应。

错误先产生有界快照，再发布；不能为了采集诊断在 mutation 锁中执行慢 IO。错误诊断自身失败不得改变已提交的业务结果，不递归写回故障 logger。

## 5. 有界采集与历史

- 沿用原始诊断文本、HTTP 失败正文和 mihomo 逻辑行各 256 KiB 的采集上限，并保留原因图深度/节点预算。
- 256 KiB 是单条诊断的采集预算。一个响应的内联诊断文本另设 256 KiB 总预算；多条 warning 超过内联预算时改带引用，由客户端逐条只读获取，不因此裁剪已经采集的记录。
- 单条详情已采集后，HTTP 编码、终端换行和复制不减少其内容。
- 实现初始默认值提案：daemon 最多 256 条且原始诊断文本总计最多 32 MiB；客户端本地最多 128 条且最多 16 MiB；任何一个上限触发均淘汰最旧记录。ID、概要及来源元数据也必须有明确大小约束。
- 历史列表返回 oldest/latest 位置及丢失提示。记录被淘汰、daemon 重启或查询不可用时明确表达，不能展示另一条记录冒充旧详情。
- 不持久化诊断历史，不新增 settings 字段。容量是内部实现常量，不新增用户配置界面。
- 不按内容去重，不维护重复计数和恢复关联。恢复后的普通健康状态可以正常更新，但既有诊断记录不被自动删除或标为已恢复。
- 读取诊断历史失败不写回该历史形成循环；客户端显示本地读取失败，后续正常重连继续沿用既有生命周期。
- 格式化结果显式携带采集字节数及截断原因，不能通过在原文中搜索 `[truncated]` 推断。无法知道上游原始总长度时不编造 original_bytes。

## 6. 本地协议及 JSON 增量

以下为已授权方案的具体实现接口：

1. 增加 `diagnostics-v1` capability，仅使用现有认证 named pipe / Unix socket。
2. `APIError` 增加可选、强类型 `diagnostic` 字段。code/message/details 原义和错误 envelope schema 保持。新代码审计所有 clone/normalize/wrap 路径，不能漏掉诊断字段。
3. 实际可能出现部分成功的成功响应增加可选强类型 `warnings`；每条保留概要及诊断。未出现警告时省略。不得把 warning 变为失败、回滚已提交状态，或改变退出码。
4. 增加只读 `GET /v1/diagnostics`（有界分页元数据）和 `GET /v1/diagnostics/{id}`（单条详情）。列表不一次传回所有正文。游标绑定启动实例，不因重启误复用。
5. 流消息仅增加可选诊断引用，不塞入大正文；客户端通过类型化查询获取详情。同步失败和本地错误优先携带直接采集结果，不依赖之后仍能访问历史。
6. TUI 后台诊断同步复用既有 session 生命周期，不创建无所有者 goroutine。读取和退出可取消，处理返回乱序以及断线后的历史缺口。

当前 WebSocket 失败只发送 1011 close，无法可靠传递原因；增加受 capability/请求协商控制的终止诊断事件，再关闭流。无法发送终止事件时客户端记录实际 transport 错误，明确远端详情未收到，不伪造远端 cause。旧客户端保持原有关闭行为。

当前常规响应读取上限为 4 MiB，控制 stream 为 1 MiB。256 KiB 字符串最坏 JSON 转义可能超过 1 MiB，因此必须验证编码后预算；不得直接把大详情塞进现有流。列表分页和响应总预算分别检查；如果既有业务响应加诊断越过常规读取预算，使用明确的诊断引用和一次只读详情获取，保留业务成功结果，不把详情读取失败变成 mutation 失败。CLI JSON 仍在本地输出前解析详情，无法获取时显式说明不可用。

完整业务 JSON 加换行恰好填满 4 MiB 时，引用元数据通过可选 `X-Mihari-Diagnostic-References` 响应头传递（base64 JSON，最多 8 MiB，涵盖 64 条有界 warning 的最坏字符转义）。该头只包含引用/概要或明确的不可用状态，不含诊断正文；客户端仍从已认证的现有历史接口读取正文，再组装 CLI JSON/TUI 结果。旧客户端忽略该头，原业务正文不变。无效引用元数据降级为诊断不可用与明确的遗漏数量，不改变业务提交或错误分类，不重发 mutation。

列表与详情之间的淘汰竞态用明确的 expired 状态表达，区别于不支持能力、未知 ID、daemon 已重启和当前查询失败。JSON 字符串依循现有 UTF-8 文本采集策略；非法 UTF-8 保留既有替换/标识，不在本次扩展成二进制转储协议。

原则是兼容性增量：不重定义现有 code、退出码、Details 已有键或成功字段。新端同版本是正常路径；旧 daemon 缺少能力时显示“此 daemon 版本不提供完整诊断”，只展示已收到的信息。查询失败绝不重发原 mutation。若实现发现必须改变既有字段语义，应提出新协议版本方案，不能默默复用 v1。

原始诊断只提供给已认证本地客户端；不将本地诊断接口映射到 Web gateway，不让浏览器因此获得 controller secret。此项保持网络与认证边界，同时落实本地错误汇报不脱敏。

## 7. CLI 展示

文本错误按以下结构输出到 stderr：

```text
Error: <动作及失败概要>
Code: <现有错误码>
Operation: <有值时显示>

Details:
<完整的已采集原始诊断；显示层转义控制字符>
```

警告使用 Warning 标题，成功输出和退出码保持；多条实际警告分别展示。文本和 JSON 消费同一诊断快照，不各自重建原因链。

JSON 错误示意（字段以强类型 DTO 落地）：

```json
{
  "schema": "mihari.error/v1",
  "error": {
    "code": "network_failure",
    "message": "subscription refresh failed",
    "diagnostic": {
      "id": "<record-id>",
      "operation_id": "<existing-operation-id>",
      "detail": "<original collected diagnostic>",
      "truncated": false
    }
  }
}
```

示例省略了来源/时间等元数据。JSON 使用正常 JSON 转义，解码后原文一致；不能混入额外非 JSON 提示破坏普通非流式 envelope。流式命令保持既有流语义，结束错误按已定义的 stderr 路径汇报。

首次装配失败、TUI 尚未建立或已经退出时直接走终端出口。日志自身失败沿用独立出口，移除 redactor/路径隐藏；当终端写入本身失败时保留退出错误，不递归尝试无穷输出。

## 8. TUI 诊断窗口

- F2 在页面和输入模式均可用；帮助页、footer 同步说明。打开窗口保存原焦点，关闭恢复，不丢表单输入。
- 优先选中当前明确的操作记录，否则当前页面最近记录，否则全局最近记录。没有记录时展示空态。
- 列表显示时间、严重程度、来源和概要；详情展示原始原因、code、operation ID、截断/缺失状态。
- Tab 切换列表与详情焦点；方向键移动列表或逐行滚动详情，PgUp/PgDn 分页，Home/End 到首尾；`c` 复制当前诊断原文，Esc 返回。窄终端采用列表/详情切换，避免强制双栏挤压内容。
- 已有确认/编辑浮层上打开 F2 时保留该浮层状态；诊断窗口关闭后返回原浮层，不替换或自动确认待处理动作。Ctrl+C 保持全局退出语义。
- 错误与警告只更新概要/提示和记录，不自动打开窗口、不抢焦点。
- 窗口打开期间新记录不强制改变选中项和滚动位置。选中记录使用有界快照，在列表淘汰后仍可完成查看；关闭窗口后释放。
- TUI 本地错误与 daemon 记录按来源标识区分。只按记录 ID 处理同一传输记录，不推测两个相似错误是否相同。
- 复制失败保留窗口、选择和滚动位置，并提供本地失败详情；避免失败提示触发自动复制等递归动作。
- 正常主动取消不伪装为 error；取消后的清理失败、无法确认结果等单独保留诊断，沿用既有业务状态。

## 9. 规则与文档调整

在实现 PR 中同步 AGENTS.md、CONTRIBUTING 对应说明、README 中英文、commands、architecture 和错误/日志设计文档，明确：

- 日志、CLI/TUI 及本地诊断详情不脱敏；概要与原文分层，不再要求普通错误只能是安全固定文案。
- cause 通过新诊断字段/端点传递，不任意塞进原有业务 DTO 字段。
- 原始 cause 中可能包含敏感数据；沿用现有导出提示，诊断查看不另加确认流程。
- 保留本地 IPC、认证、业务单写入者、事务提交、跨平台及 CGO-free 约束。
- 不修改 CHANGELOG，不变更依赖或 toolchain，不变更持久化格式。

## 10. 实施拆分与验收

按共享数据链路推进，每阶段执行 Red–Green–Refactor，最终按第 2 节逐项登记“入口 → 原因产生 → 传递 → 展示 → 回归测试”，不能只凭全仓测试通过宣称全覆盖。

1. **共享诊断与历史**：抽取有界 formatter；类型化快照；进程 owner、有界历史、退出与并发；保留现有文件日志原文行为。
2. **控制协议与客户端**：capability、可选 diagnostic/warnings、历史列表与详情、流引用、旧端回退和超限处理；不改变 mutation 结果。
3. **CLI 与独立出口**：统一文本/JSON、装配失败、本地任务、daemon 服务入口、日志失败、清理及警告。
4. **TUI 公共窗口与本地错误**：F2、列表/滚动/复制、焦点保存、窄屏、同 ID 幂等、不可用与淘汰提示。
5. **页面和后台接入**：九个页面、全局 session/轮询/流、服务状态及后台任务；逐项消除固定文案丢 cause 和静默错误。
6. **完整回归与文档**：按覆盖清单完成测试、必要最小生产修复与规则更新。

关键测试：

- 使用合成 token/URL/password/path/配置片段，断言日志、IPC、CLI 文本/JSON、TUI 详情与复制均保留原文。
- 包装/合并原因、已有堆栈、HTTP 失败正文、typed nil/循环图及预算；采集截断明确，传输和展示不二次截断。
- 历史条数/字节上限、淘汰、重连、daemon 重启、来源隔离、并发 publish/query、窗口固定选中快照及退出释放。
- 重复内容产生独立记录；同一 ID 经响应和历史只显示一次；无自动合并次数或恢复标签。
- 成功附警告仍成功；commit 后 sync/reload/cleanup 的既有业务语义不变；诊断查询失败不重放操作。
- 旧端回退，响应字段保留，最坏 JSON 转义、4 MiB 响应/1 MiB 流边界、有界分页。
- 终端控制字符只显示不执行；复制/JSON 解码还原采集原文；窄屏换行、长文本滚动、焦点与按键隔离。
- 没有文件 logger 或日志级别过滤时仍能显示诊断；独立失败出口不脱敏、不递归依赖 logger。
- 所有测试只访问合成 fixture、临时目录、fake 和本地随机端口，不连接真实订阅、不修改系统服务。

验证从目标包扩大到 integration、全仓 test/race/vet、格式检查及 Windows/Linux/macOS × amd64/arm64 的 CGO0 构建。实际受环境限制的检查必须单列，不预先声称通过。

## 11. 最终审阅边界

Q1–Q12 产品决策已确认，用户已授权依据设计和执行方案完成开发、提交 PR，并每 10 分钟检查 CI 与 bot review，全部可用检查通过后汇报。实施遵守本方案范围；新增依赖、持久化或超出批准范围的协议/网络边界变化仍需说明。PR 合并另行确认。
