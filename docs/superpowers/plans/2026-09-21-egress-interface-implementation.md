# Issue #291 出口网卡选择实施计划

状态：生产实现与相关测试已完成，正在执行最终验证并交付 PR。工作分支 `feat/291-outbound-interface`，基于 `origin/dev` 创建独立 worktree。用户随后已明确授权提交、推送、创建 PR，并根据 CI/bot review 修正至全绿；不包含合并。

依据：[Q1–Q24 设计记录](../specs/2026-09-21-egress-interface-design.md)、[ADR 0006](../../adr/0006-native-egress-binding.md)。所有实现以用户已确认行为为准，不重新采用访谈前的废弃建议。

## 架构与范围

- Manager 是出口选择、配置生成与核心应用的唯一写入者；CLI/TUI 只走认证本地 IPC。增加 `GET/PATCH /v1/egress`，不增加 TCP 控制监听或 Web gateway 写入口。
- 不修改系统路由，不重新实现 socket 绑定，不保证两个全局 TUN 的入口接管能够共存；本任务只处理出口。
- 原生接口绑定有既定例外、缓存与池化语义。缺失接口可保存，运行时连接失败不等于配置无效；同名恢复不额外触发自动 reload/restart。
- 架构旧引用 `docs/superpowers/specs/2026-08-03-mihari-architecture-design.md` 在当前 checkout 中不存在，实施使用实际存在的 `docs/architecture.md` 及根 AGENTS.md，不顺带修改失效引用或无关文档。
- 复用现有依赖，维持 CGO_ENABLED=0 与 Windows/Linux/macOS amd64/arm64 范围。若实施证明必须新增依赖或改变其他契约，先给出具体影响。

## 1. 设置、接口快照与协议

先写目标行为的失败测试，再实现最小代码；每一步保留红绿测试证据。

- `internal/config` 增加可选字符串 `egress-interface`。未配置/自动不输出字段，手动保存接口原名；不 trim 后静默改变名称，不以特殊名称字符串表示自动。不因机器当前找不到接口拒绝加载已保存配置。
- `internal/platform` 附近增加窄接口及平台文件，提供网卡名称、类型、地址与自身可用状态；允许 Down 网卡存在于快照。不要直接复用只返回 TUN 名称的 `tundetect` 作为完整列表。
- 接口名按平台真实绑定名称返回，描述与友好文案只用于展示。状态无法确定时返回 unknown；获取快照失败与快照成功但接口不存在分开，不能把读取失败伪造成所有接口消失。
- Manager 合并当前快照与当前保存但已消失的选择，按名称去重。不维护所有历史选择，不允许 API 手工新增未知名称。
- 自身 TUN 的判断结合受管配置中的设备名及可用的运行核心信息，不能把既有冲突探测中的“恰好一个就扣除”启发式用于禁选任意其他软件接口。核心停止时同样应按受管配置识别；平台无法确定身份的分支必须有测试和明确状态。
- 协议增加 capability `egress-interface-v1`、类型化状态和更新请求。复用 schema/revision/envelope/diagnostic/warnings 与既有错误、退出码体系。

建议新端点 DTO 结构在编码时以 contract tests 固定：

| 对象 | 字段与含义 |
| --- | --- |
| 选择 | `mode` 为 automatic/manual；手动时 `interface_name` 是原名 |
| 状态 | schema、revision、保存选择、应用状态、候选网卡列表 |
| 应用状态 | saved/applied/unknown；仅表达配置应用情况，不表达公网连通或所有旧 socket 已消失 |
| 网卡 | name、kind、availability、addresses、selectable、不可选原因；缺失保存项保留 name |
| 更新请求 | operation_id、可选 if_revision、mode、手动时 interface_name |

自动请求不能混入手动名称；手动必须有名称。提交时重新检查 revision、候选资格与自身 TUN 限制，不能只信任客户端列表。核心运行状态未知不能当作已停止直接保存并报应用成功。

验收：旧配置读写、自动字段省略、名称含空格/Unicode/特殊字符、真实名为 auto、Down、缺失保存项、未知新名称拒绝、自身 TUN 禁选、枚举失败、DTO JSON 精确契约与旧 capability 缺失。

## 2. 配置生成与实例级覆盖

在 `internal/subscription` 的共同 Generate 路径实施，仅修改克隆后的候选文档。

- 手动出口覆盖全局 `interface-name`、节点显式 `interface-name`、provider 的 `override.interface-name`，保留其他键和节点/代理组的选择语义。
- 对 DNS 的显式接口选择按 mihomo 实际语义处理；先区分代理选择、接口选择和其他 fragment 参数，不能一律删除 `#...`。代理优先匹配、同名代理、代理链、system/DHCP 等路径用固定上游源码和 fixture 核对。
- `dialer-proxy` 保留承载关系，在真正受管拨号节点上覆盖接口；不得为了选择网卡而改变代理链或 DNS 服务器地址。
- 自动模式跳过出口覆盖，从原订阅及既有 overrides 重新生成，不能在已污染的运行配置上删几个字段作为恢复。
- 启动/bootstrap、订阅切换/刷新、普通配置应用及 trusted core 路径全部进入同一生成规则；保持实例选择不随订阅变化。
- 不更改 TUN 的入口参数与冲突确认，不误删现有 controller、监听和日志等受管字段。

验收：全局与节点冲突、HTTP/file/inline provider override、DIRECT、自定义 direct、代理链、DNS 代理/接口歧义及参数保留、自动还原原值、原文档未修改、切换订阅仍用同一选择。

## 3. Manager 事务与核心生命周期

新增出口用例，复用既有 mutation coordinator、候选校验、原子写入、reload 与补偿设施；不另建一套业务持久化事务或重入 coordinator。

1. 读取配置 generation、revision、保存选择及核心状态，确认请求合法；重复 operation_id 复用既有结果，选择未变化时不重载、不关闭连接。
2. 锁外准备候选及慢校验；提交前复核 generation、revision、对象身份与候选资格。
3. 核心确定已停止时只保存设置并发布状态，界面显示 Saved；不访问 controller 应用，不启动核心。
4. 核心运行时生成候选文件、验证、原子替换、reload，并按 Q5/Q10 关闭活动连接。设置、生成文件与内存发布纳入同一逻辑事务；不能只 PATCH 全局接口。
5. 任何可观察的校验、提交或应用失败，使用有界恢复 context 补偿至原设置/配置；不能因请求 context 已取消就跳过必要恢复。连接关闭不能被“回滚”复活，错误详情如实保留。
6. 补偿失败沿用既有 degraded 与后续 mutation 拒绝规则；不假称旧设置已恢复。仅网卡不可用则保留设置、核心继续运行，不触发这套失败恢复。
7. 已完成 settings replace 后的目录同步 warning 保持成功，不因 warning 回滚；实际失败只由执行 owner 记录一次。

核对运行配置 readback 能力：不能把 reload 返回成功推论成后台资源下载成功，或所有流量已强制绑定。明确维护 saved 与已知 applied/unknown 状态，CLI/TUI 使用同一含义。

原生同名恢复依赖后续物理拨号。Windows/macOS 名称到 index 缓存 TTL 约 20 秒；完整 reload 会清缓存，但本功能不因网卡重新出现主动重载。状态列表查询可重新枚举，界面可用状态与内核暂时仍在缓存窗口并不矛盾。

验收：运行/停止/未知状态、缺失与 Down 可提交、旧连接关闭、关闭失败、校验失败、reload 失败、保存失败、取消、恢复失败、提交后 warning、并发 revision/generation、幂等重放、普通与 trusted core 路径、订阅及启动覆盖。

## 4. 本地接口与 CLI

- `internal/control/protocol/server/client` 类型化提供 GET/PATCH `/v1/egress`，沿用认证与 mutation 元数据。拒绝未知 payload、非法 mode、无资格候选和自身 TUN。
- `internal/cli` 增加 `egress list|status|set <interface-name>|auto`，支持全局 `--json`，set/auto 支持 `--if-revision`。文本输出名称和状态，停核保存用 Saved。
- 旧 daemon 缺少 capability 时给出明确不支持信息，不回退为直接写 settings。
- 原始失败详情使用共享诊断，普通网卡/应用状态字段不混入诊断正文；Web gateway 不暴露新接口。

验收：命令参数、引号名称、自动/手动、revision 冲突、JSON 单 envelope、错误与退出码、重复提交只执行一次、客户端取消、未授权访问、TUI/CLI 相同候选校验。

## 5. TUI B 布局

在 `internal/tui/pages/system` 的 Network 区域增加 Outbound Interface 入口；原型只作为视觉和交互依据，不直接移植 HTML 或连接真实 mutation。

- 顶部 Saved 与可用状态，左侧固定 Automatic 与网卡滚动区，右侧详情；区分保存选择与候选，失效保存项始终可见。自身 TUN 展示禁选原因。
- 打开时候选/焦点为当前保存项。↑/↓ 依序移动可选候选并保证选中项可见；到首尾停止，禁选项不能成为可提交候选。Tab 到 Cancel，Shift+Tab 返回列表；Esc/Cancel 放弃草稿。
- 固定底部 Apply/Cancel；未修改时 Apply 不可用。提交中阻止重复操作，保留可见进度；成功关闭，失败保留候选并展示共享诊断。
- 网卡刷新不能重置用户草稿或让迟到结果覆盖新会话；候选在提交前消失且不是保存项时，明确告知已不在可选列表，保持原设置。
- 复用现有 System 状态刷新机制获取网卡快照，daemon 查询不改变选择；不为自动恢复另建无所有者 watcher。
- 紧凑终端优先保证保存项、选择与操作可达，详情按空间重排。长名称和地址不挤掉操作按钮；文案英文，设备原名正常显示。
- 停核保存只显示 Saved；不可用独立展示，不增加 Pending 文案或双 TUN 确认。

验收：长列表方向键滚动、默认焦点、禁选项、首尾边界、Tab 往返、未改动 Apply、Esc/Cancel、异步失败、慢请求与重复按键、失效恢复、窗口缩放和长名称。

## 6. 集成、文档与交付

- 在 `internal/integration` 用 fake mihomo、临时目录及本地 IPC 验证跨包链路和补偿，不使用真实用户配置、订阅或已安装服务。
- 同步 README/README.zh-CN.md、commands.md 及架构说明中的已实现行为、原生边界与降级前切回自动的要求；实施完成前不把设计表述为已上线。
- 不修改 CHANGELOG.md，不覆盖原仓库用户已有的 `.gitignore` 修改，不提交预览服务文件、测试二进制或覆盖率产物。
- 小范围红绿测试通过后执行相关包与集成测试，再按风险运行 `go test ./...`、`go test -race ./...`、`go vet ./...` 与 gofmt 检查；六个 OS/架构组合保持 CGO-free 编译。无法运行的检查明确记录原因。
- 三平台实际网卡绑定、虚拟网卡重建、真实双 TUN 与真实 mihomo 流量归属属于单独授权的隔离 testenv；默认测试及本次原型验证不能替代。
- 原设计阶段未授权提交；用户后续已授权提交、推送及 PR/CI/review 迭代。合并和真实主机网络变更仍不在范围内。

## 当前验证记录

- 设计与原型阶段执行过 `git diff --check`，无空白错误。
- 浏览器验证过 B 原型的选择、滚动、首尾、Tab/Shift+Tab、取消等交互，数据全部为模拟。
- 生产实现覆盖设置、网卡枚举、生成器、Manager 事务、本地协议/客户端、CLI 和 B 布局 TUI。单元及本地 IPC 集成测试覆盖候选、自动恢复、启动、revision/generation、幂等、配置读回、回滚及 degraded、保存 warning、UI 滚动和失败保留草稿。
- 已通过 `go test ./...`、`go test -race ./...`、相关包复测、`go vet ./...`、`golangci-lint run ./...`，以及 Windows/Linux/macOS 的 amd64/arm64 CGO-free 构建。远端 CI/review 结果以 PR 的最终验证记录为准。
- 实现细化：DNS 接口片段无法无歧义表达所选名称时，生成失败而不悄悄改选代理；网卡类型无法可靠识别时显示 Unknown。长详情通过 PgUp/PgDn 滚动。
- 尚未运行真实 mihomo、实际网卡切换或双 TUN 流量验收；这些属于另行授权的 testenv，不能由单元测试和构建结果替代。
