# 统一日志级别与 mihomo 状态同步

日期：2026-09-16

状态：访谈决定已实现，执行记录与验证结果见执行方案。

执行方案：[Issue #209 统一日志级别执行方案](../plans/2026-09-16-unified-logging-level.md)。方案细化受控修改与外部采纳的不同提交路径，以及外部采纳后 runtime 生成物在正常生成/每次内核启动前追平的时序。

关联：[#209](https://github.com/mihari-proxy/mihari/issues/209)、[#257](https://github.com/mihari-proxy/mihari/issues/257)。

工作分支：`feat/209-unified-logging-level`，基于 `origin/dev` 的 `6bd40d1`。

## 1. 目标与范围

统一 Mihari 普通文件日志级别与 mihomo 全局 `log-level`。支持通过 System 设置、受支持的 gateway 配置请求及对内核实际状态的观察保持一致。日志实时流的订阅筛选独立，不受文件日志阈值额外限制。

#209 实现日志级别、捕获解析、日志级别配置请求、日志级别外部变化同步及相应 UI/测试。#257 跟踪更广泛的面板操作支持、运行模式等其他字段的外部变化采纳；不在 #209 顺带重写这些功能。

## 2. 已确认的产品决定

### 2.1 配置来源与持久化

- 实际生成输入是活动订阅缓存（无活动订阅时为 bootstrap 文档）和 Mihari settings。`Generate` 的 overrides 参数目前没有生产调用者传入非空值，不增加新的用户配置层。
- 生成 runtime config 时，Mihari 保存的统一级别覆盖源文档 `log-level`。原订阅缓存保持原样；刷新、切换、启动与迁移生成路径遵循同一规则。
- 从内核采纳的新级别也成为保存的统一级别，后续生成不得又恢复为更早的值。
- 默认仍为 `info`。文件大小、保留份数和导出范围不因本功能改变。

### 2.2 受控修改与停止状态

- gateway 配置修改由 mihomo 先执行，Mihari 回读确认实际值，再完成自己的持久化和状态发布；不得仅依据请求体提前声称修改成功。
- 所有写请求继续经过 gateway 认证和 daemon/Manager mutation 协调，不开放任意配置透传，不暴露 controller secret。
- 运行中的受控修改整体成功或整体回滚。校验、内核修改、回读、配置发布或 settings 保存的未提交失败需要恢复旧状态；无法确认恢复时显式降级，不假报完全回滚。
- 沿用 settings replace 后目录同步 warning 已提交、返回成功的现有语义。日志轮转清理失败仍是独立的维护问题。
- 内核停止或尚未安装时允许保存级别，Mihari 日志立即生效，内核下次启动使用保存值，不自动启动内核。
- runtime 文件生成、校验、原子替换、reload 及恢复仍走已有配置事务边界。实时 PATCH 与文件事务的组合需在实现计划中明确提交、补偿和崩溃恢复顺序。

### 2.3 外部变化同步

- gateway 受控修改立即回读同步；内核正常运行时每 2 秒检查外部日志级别变化。
- 内核启动时先应用 Mihari 已保存配置，不把启动、reload 或受控 mutation 的过渡状态当成外部修改。
- 仅采纳支持的字段和值；不采纳 controller、secret 等安全相关受管字段。
- 外部修改已生效但同步保存失败时保留内核实际值，显示同步未保存并重试，不撤销外部工具已经完成的操作。
- 每次重试重新观察最新状态，并复查核心实例、配置 generation/revision，不能用旧观察覆盖后来的用户操作。
- 成功保存前退出或重启仍可能回到最后保存值，UI 不得提前声称已持久化。
- 观察任务由 daemon 生命周期持有，可取消并等待退出。测试通过注入 tick/waiter 驱动，不依赖固定 sleep。

### 2.4 silent 的被动支持

- 用户主动选择仍只有 `debug`、`info`、`warn`、`error`，不新增主动切换到 `silent` 的功能。
- 只有观察到 mihomo 经其他途径已切换为 `silent` 时，Mihari 才采纳、保存、显示并应用该值。之后启动和重新生成应保留这个已保存值。
- 区分主动修改的允许值与内部观察/加载/状态返回的允许值；不能因主动选项不含 silent 而拒绝加载已同步的配置。
- silent 关闭普通日志记录，保留历史文件；操作失败提示、控制 API 错误和独立 FailureReporter 不由此取消。实时流继续独立。
- 从被动 silent 状态恢复时，用户仍可选择四个普通级别；仅修改轮转设置不得意外退出 silent。

### 2.5 捕获与实时流

- 捕获 stdout/stderr 时优先解析 mihomo 真实级别，warning/warn 归一；未知或无级别行回退 stdout=INFO、stderr=WARN。保留原文和既有有界截断/分片语义。
- 文件、快照及导出继续遵循 2026-09-14 完整错误日志策略，不恢复旧设计的脱敏行为。
- 内核全局日志配置与 `/logs?level=...` 的实时订阅阈值是不同概念；不能把面板的日志订阅参数当作全局配置修改。
- 保留 Web 日志订阅阈值独立性，不在 gateway 额外用全局日志级别收窄它；验证普通 HTTP 流和 WebSocket、参数、现有连接及重连。
- TUI Logs 的显示筛选保持独立，warn/warning 等价匹配；需验证 debug 实时日志可观察，不能依赖未带 level 参数时内核默认的 info 流来证明此项。

## 3. 面板事实与兼容边界

已核查的 Zashboard v3.27.0 与 MetaCubeXD v1.273.1，Logs 页 Level 保存在浏览器本地，通过重新订阅 `/logs?level=...` 生效，不发送 `PATCH /configs`。当前核查未发现其普通设置 UI 提供修改全局 `log-level` 的对应控件。

因此 gateway 支持全局日志配置请求是一项 API 兼容能力，不等于这两个面板已经有相应按钮。本次不修改或分叉第三方面板静态资源，不新增独立 Mihari Web 设置页面。其他配置操作和组合请求在 #257 逐项审查，未知写操作仍拒绝。

参考证据：

- [mihomo v1.19.30 日志事件与普通输出](https://github.com/MetaCubeX/mihomo/blob/v1.19.30/log/log.go)
- [mihomo 实时日志订阅](https://github.com/MetaCubeX/mihomo/blob/v1.19.30/hub/route/server.go#L454)
- [Zashboard 日志订阅](https://github.com/Zephyruso/zashboard/blob/98aa6e00a2217af141196f4439b91e184a55df23/src/assembly/logs/index.ts)
- [MetaCubeXD WebSocket](https://github.com/MetaCubeX/metacubexd/blob/8bbc8f58fef71148a94fb5c0ff808f79b057337d/packages/ui/composables/useWebSocket.ts)

## 4. 契约及文档影响

- `/v1` 已有字段、错误码、envelope 和 CLI 退出码不改变含义。若区分已保存/实际/同步失败需要新增状态字段，必须使用向后兼容可选字段并在实现方案中列明。
- 持久化 `log.level` 将能包含被动同步得到的 silent；旧版校验器不一定接受，应在升级/降级说明中明确，不能承诺直接降级兼容。
- System 文案说明全局级别控制 Mihari 与 mihomo 普通日志；Logs 文案说明显示/订阅筛选独立。
- 更新一期日志设计的“不同步内核级别”非目标及 README、架构/命令说明中的旧捕获语义。
- 无新增依赖，无平台范围变化，不修改 CHANGELOG。
- 仓库规则引用的 `2026-08-03-mihari-architecture-design.md` 在当前基线不存在；以当前 `AGENTS.md` 和 `docs/architecture.md` 约束为准。

## 5. 验证计划

行为实现采用 Red–Green–Refactor，使用临时目录、fake controller/进程和注入式时钟，不连接真实用户内核、订阅或系统服务。

1. 生成与 bootstrap：受管级别覆盖来源、缺省 info、刷新/重启保持已保存值、silent round-trip、原缓存不变。
2. 受控事务：内核确认后同步，校验/回读/保存/reload 失败及补偿失败，取消、并发、过期候选和 revision，settings 已提交 warning。
3. 外部同步：2 秒 tick、核心实例变化、过渡状态排除、保存失败保持 live、重试采用最新值、生命周期退出和幂等观察。
4. silent：不进入主动选项、被动采纳和恢复普通档位、轮转单独修改、普通日志静默而公开错误正常。
5. 捕获：真实级别及别名、无级别回退、分块/多行/截断、原文和导出保持。
6. IPC/TUI/Web：跨 TUI 同步、未保存状态、现有稳定契约、受支持的配置请求与未知写拒绝、HTTP/WS 流级别独立和重连、debug 观察。
7. 按风险执行目标包、integration、全仓测试、race、vet、gofmt 及受影响 CGO-free 跨平台构建。尚未执行这些检查，不预先声称通过。
