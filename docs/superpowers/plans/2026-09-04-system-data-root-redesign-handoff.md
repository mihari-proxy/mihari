# System Data Root Redesign Handoff Plan

> **2026-09-06 执行计划审阅收口：** 用户已明确授权继续编写执行计划并循环审核；已完成 [Unix Base Dir 执行计划 R3](2026-09-05-unix-base-dir-implementation-plan.md)，共19个任务，经过 Astra 三轮独立审核与修订，最终 [计划 R3 技术 PASS](../reviews/2026-09-05-unix-base-dir-plan-review-r3.md)，未关闭 Blocking/Critical/Major 为0。计划 SHA256=`0B708CE6FDBCE432FB26F16068F4B087F18CC6B2006A80BE39278BDC5CD3F74F`；前两轮报告同目录保留，分别记录5项和2项Major及关闭证据。R4设计及其已通过哈希保持不变。计划正文的“待复审”为送审快照，当前审核状态以绑定该哈希的独立PASS报告为准。执行顺序为平台/IPC基础→输入安全与日志导出→安装迁移/恢复→默认布局接线→跨平台验收；T19全部必需安全job通过才具备可合并条件。本轮仅完成文档计划和技术审核，未修改生产代码、运行实施测试、提交或操作真实服务；实际实施与契约变更仍以用户明确授权为前提。下方“尚未编写实施计划”等文字保留为历史记录，当前状态以本段为准。

> **2026-09-05 技术审阅收口：** 已按用户要求完成 Astra 四轮独立审核和修订，最终 [Unix Base Dir 设计 R4](../specs/2026-09-05-unix-base-dir-design.md) 获得 [R4 技术 PASS](../reviews/2026-09-05-unix-base-dir-review-r4.md)，未关闭 Blocking/Critical/Major 为 0。审核规格 SHA256 为 `3A97A33DBB1B1CA5122F2A25F8545FD5A1EBD73B5DA5A160047749FF8A942F3E`。R1、R2、R3 报告同目录保留，分别记录 8、6、1 项 Major 及逐轮关闭证据。当前交付是完整 Unix 推荐设计，等待用户对分区、共享权限、RootConfigPolicy 兼容限制、公开协议及持久化格式的最终批准；尚未编写实施计划或修改生产代码。Windows 重设计不在本次技术 PASS 范围。下文保留历史交接材料，当前状态以本段及 R4 设计/报告为准。

> **2026-09-05 接续状态：** 用户要求先完善 Unix 统一 base dir 方案。工作分支已同步到 `origin/dev` 的 `2d00f61`，包含 #198/#199/#200 及 #202/#203/#205；第 4 节 11 份旧文档已校验备份后移出工作树。备份位于 `C:/Users/Kinema/AppData/Local/Temp/mihari-retired-system-data-root-3d2e2ad4b3de493eb798f5eba3f402db`。新的 [Unix 候选设计](../specs/2026-09-05-unix-base-dir-design.md) 尚待用户裁决和评审，不代表 Windows 设计或完整交接验收完成。缺失的总体架构 spec 暂以当前 `docs/architecture.md`、AGENTS.md 和最终代码为依据。下文原始快照保留为历史记录。

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:brainstorming` to produce and obtain approval for the replacement design. Use `superpowers:writing-plans` only after that design is approved. Do not implement the retired design described below.

**Goal:** 在 logging 功能全部完成并进入本任务的基线后，删除现有 system-data-root 方案，从当前代码和最终 logging 行为出发重新完成系统级数据根设计。

**Architecture:** 本文只保存产品目标、已确认的跨 PR 阻塞、重设计流程和验收门槛，不预先指定替代架构。新方案必须重新统一系统数据根、日志、权限、credential、迁移和多用户模型，不能把当前 v13 文档作为增量修改底稿。

**Tech Stack:** Go 1.26.0 / toolchain go1.26.5、Windows ACL 与 named pipe、Unix mode 与 Unix domain socket、`internal/platform.PrivateFS`、Mihari 本地 `/v1` 控制面。

**Spec:** 本任务没有可继续实施的有效 spec。`docs/superpowers/specs/2026-09-02-system-data-root-design.md` 及其评审将在前置条件满足后删除；替代 spec 必须重新编写并经用户批准。

## 1. 文档性质与停止线

本文是交接文档，不是 system-data-root 的最终设计，也不授权实现、提交、推送或创建 PR。

在 logging 工作尚未全部完成并进入本任务基线前：

- 不实现 system-data-root。
- 不继续修订 v13。
- 不删除旧文档，以免当前交接丢失历史材料。
- 不根据当前 worktree 名称或单个 PR 状态推断 logging 已完成；必须由用户明确确认，或确认 logging 设计中计划的基础日志、控制面和导出交互均已合并到目标基线。

前置条件满足后，旧方案应直接删除，再从最终代码重新调研和设计。禁止把旧方案改名后继续使用，也禁止以“v14”方式逐条修补 v13。

## 2. System Data Root 的核心产品目标

重设计仍需从以下目标出发，但每一项的具体实现都要重新论证：

1. 一台机器只有一个 Mihari daemon；daemon 继续是 mihomo 生命周期和持久化业务状态的唯一所有者。
2. 默认数据不得依赖启动进程的 `$HOME`、`%USERPROFILE%` 或 `SUDO_USER`，服务与本机客户端必须稳定地指向同一控制面。
3. 默认数据根是系统级位置：Windows、Linux、macOS 都要有明确且可测试的默认路径；`MIHARI_DATA` 等显式覆盖仍需保留。
4. 本机普通用户无需 sudo/UAC 即可连接已安装 daemon；控制通道保持 Windows named pipe 或 Unix domain socket，不得改为 TCP。
5. controller 地址和 secret 不得暴露给浏览器；既有 `/v1` DTO、错误 envelope、错误码和 CLI 退出码保持稳定，破坏性语义必须新版本化。
6. 从旧用户级数据根迁移时必须防止 symlink/junction/reparse、挂载点、硬链接和路径替换攻击；失败不得删除唯一有效来源。
7. 服务安装、升级和迁移必须明确停止、提交、回滚、服务 env 改写及重新启动的顺序。
8. 保持单个无 CGO Go 可执行文件，并支持 Windows、Linux、macOS 的 amd64 与 arm64。

这些是重设计的输入，不代表旧文档中的权限数值、目录布局、迁移算法或进程接线继续有效。

## 3. Logging 对旧方案形成的阻塞

### 3.1 TUI 直接日志 IO 与系统根权限模型冲突

旧 system-data-root 方案只允许普通用户穿越系统根并读取 `control.token`、`mihari-channel`，其它子目录和文件保持 daemon/root/SYSTEM 私有。它同时禁止未提权 TUI 创建默认系统根或其子目录。

最终 logging 设计则可能包含以下直接本地 IO：

- daemon 写 `logs/mihari-daemon.log`；
- mihomo 输出写 `logs/mihomo.log`；
- 每个 TUI 进程写或轮转 `logs/mihari-tui.log`；
- TUI 为导出直接读取多类日志；
- TUI 在 `logs-export/` 创建最终 zip。

若 `logs/` 继续是 `0700` 或无 Windows Users ACE，普通用户 TUI 无法完成这些行为；若向所有普通用户开放读写，又会改变系统根威胁模型、daemon 单写入者边界和多用户隐私边界。该冲突不能通过调整一两个 mode 位直接解决。

### 3.2 `PrivateFS` 的单 owner 加固策略会覆盖共享根 ACL

logging 基础当前依赖进程级 `platform.PrivateFS`：

- Unix `openRoot` 会把根目录加固为 `0700`；
- Windows 根 DACL 只保留当前 owner/SYSTEM 所需权限；
- `PrivateFS` 同时持有根和日志子目录句柄，负责 no-follow、identity、轮转和发布安全。

旧 system-data-root 方案要求系统根能被普通用户穿越，并让 token/channel 单独可读。若 daemon 沿用现有 `PrivateFS` 打开系统根，它可能把共享控制面所需 ACL/mode 改回私有状态。若普通用户 TUI 打开同一 `PrivateFS`，又通常没有打开、加固或创建系统根的权限。

替代设计必须重新定义 capability 和权限策略，不能让通用 `NewPrivateFS(root)` 同时代表“系统根治理”“daemon 私有数据 IO”“TUI 本地日志 IO”和“日志导出发布”四种不同权限。

### 3.3 Credential 创建与客户端刷新合同冲突

旧 system-data-root 方案要求：

- 只有 daemon 启动路径可以 `credential.LoadOrCreate`；
- help、service、自更新和 TUI 等客户端路径不得创建 token 或系统根；
- 客户端必须在请求或重连时重新加载 credential，不能把启动时的空值或旧值永久冻结。

logging 基础为了在写日志前建立可信数据根，目前引入进程级 root/`PrivateFS`/token 准备与缓存，并可能在选定命令的 pre-run 阶段调用 `LoadOrCreate`，再把 token 设置到长生命周期 client。

重设计必须拆开 daemon bootstrap、client credential provider、TUI logging bootstrap 和无 IO 命令。不能只把旧 `LoadOrCreate` 调用移动几行。

### 3.4 旧树迁移不再拥有静止来源

旧迁移方案通过停止 OS 服务和残留 daemon 进程树，使旧数据树在快照、复制、指纹复核和删除期间保持静止。logging 引入 TUI 对旧用户数据根的持续追加和轮转后，旧版本或并行运行的 TUI 仍可能修改迁移来源。

可能后果包括：

- 快照后新增日志没有进入目标树；
- 复制过程中发生轮转，导致集合不一致；
- 来源指纹变化，迁移已提交但旧树无法删除；
- Windows 目录或文件句柄阻止 rename/delete；
- 新旧版本同时运行时，日志继续写向已经失去权威性的旧根。

新方案必须明确迁移如何识别、阻止、容忍或隔离这些 writer，并覆盖“旧二进制仍在运行”的升级场景。

### 3.5 路径布局前提已经变化

旧方案声明 `Paths` 相对布局不变，但 logging 已把单一日志路径扩展为日志目录、daemon/TUI/mihomo 独立文件及导出目录。新的迁移白名单、权限修复、目录创建、文档和测试矩阵必须以 logging 完成后的实际 `platform.Paths` 为准。

### 3.6 多用户日志安全尚未裁决

“任意本机用户可凭共享 token 管理整机代理”不自动等于“任意本机用户可读取、覆盖或伪造其他用户的 TUI 日志”。新设计必须分别裁决：

- daemon/mihomo 日志的读取者和写入者；
- TUI 日志是机器共享、按用户隔离还是只进 daemon；
- 导出是否包含其他用户产生的记录；
- 谁可以创建导出文件，导出落在系统目录还是用户选择目录；
- 本机低权限用户是否可以通过日志内容、文件名、锁或轮转制造欺骗/拒绝服务。

这些是产品与安全决定，不能由平台 ACL helper 的实现细节代替。

## 4. 必须删除的旧方案

logging 前置条件满足后，开始重设计前删除以下文件：

```text
docs/superpowers/specs/2026-09-02-system-data-root-design.md
docs/superpowers/reviews/2026-09-02-system-data-root-design-review.md
docs/superpowers/reviews/2026-09-02-system-data-root-design-review-r2.md
docs/superpowers/reviews/2026-09-02-system-data-root-v10-plan.md
docs/superpowers/reviews/2026-09-02-system-data-root-v10-impl.md
docs/superpowers/reviews/2026-09-02-system-data-root-v11-plan.md
docs/superpowers/reviews/2026-09-02-system-data-root-v11-impl.md
docs/superpowers/reviews/2026-09-02-system-data-root-v12-plan.md
docs/superpowers/reviews/2026-09-02-system-data-root-v12-impl.md
docs/superpowers/reviews/2026-09-02-system-data-root-v13-plan.md
docs/superpowers/reviews/2026-09-02-system-data-root-v13-impl.md
```

删除前只需确认路径和 Git 状态，避免误删用户新增的同名替代文件。删除后不得从 Git 历史复制旧章节作为新 spec 主体；如需核对历史需求，只把它作为需求来源记录，并对照最终 logging 代码重新证明。

保留本文作为重设计任务入口，直到替代 spec、评审和实施计划都已建立。

## 5. 后续 Agent 的执行流程

### 阶段 A：确认基线，不进行设计

- 阅读仓库根 `AGENTS.md`、`.github/CONTRIBUTING.md`、`README.md` 和架构设计要求。
- 确认当前分支不是 `main` 或 `dev`，保存用户已有修改，不清理无关 worktree。
- 由用户确认 logging 已全部完成；确认目标基线同时含最终日志基础、配置控制面和导出行为。若任一部分仍在并行 PR 或未提交修改中，停止 system-data-root 重设计并报告缺失基线。
- 将 system-data-root 工作分支更新到用户指定的最终 logging 基线。分支同步、merge/rebase、提交、推送和 PR 均需遵循当时用户授权，不从本文推导权限。

### 阶段 B：删除旧方案并重新审计现状

- 删除第 4 节列出的 11 个文件。
- 若仓库根存在 `.codegraph/`，先使用 CodeGraph 定位最终实现；否则使用 `rg`。
- 从最终代码而非旧 spec 建立事实清单，至少覆盖：
  - `platform.Paths` 与默认路径；
  - `PrivateFS`、advisory lock、directory/file identity 和 publish primitives；
  - daemon、CLI、TUI 的启动与关闭顺序；
  - credential 加载与 control client 重连；
  - service install/reinstall/update 流程；
  - 三类日志 writer、配置 mutation 和导出数据流；
  - Windows ACL、Unix mode、socket/pipe 权限；
  - 旧版本升级及迁移来源。
- 记录事实、未知项和必须由用户裁决的产品问题，不写实现代码。

### 阶段 C：从零提出候选架构

至少比较以下方向，但不得预先宣布其中任何一个已获批准：

1. TUI 日志放入每用户目录；daemon/mihomo 日志保持系统私有，导出通过 daemon/control plane 或受控协作完成。
2. 系统根内建立明确的共享日志子树，使用按平台 ACL/mode、按用户命名空间和受控导出规则。
3. daemon 成为全部持久日志的唯一 writer，TUI 通过控制面发送结构化诊断事件，导出也由 daemon 执行。

每个方案必须比较：架构不变量、协议影响、离线 TUI 行为、跨平台权限、多用户隐私、迁移、旧版本兼容、故障降级、测试成本和实现复杂度。给出推荐方案和放弃其它方案的具体理由，然后取得用户批准。

### 阶段 D：编写并评审替代设计

- 使用执行当日日期创建新的 `docs/superpowers/specs/YYYY-MM-DD-system-data-root-design.md`，不要复用 `2026-09-02` 文件。
- 新 spec 必须明确第 6 节的所有决策，并给出跨平台行为表、启动/迁移时序、权限表、失败语义和测试矩阵。
- 对新 spec 做架构/安全审查和可实施性审查；评审必须基于 logging 完成后的代码。
- 修复所有 blocking/critical/major 问题后，请用户审阅并批准最终 spec。
- 用户批准前不创建实施计划、不修改生产代码。

### 阶段 E：批准后再规划实现

- 仅在用户批准替代 spec 后，使用 `superpowers:writing-plans` 创建新的实施计划。
- 计划按 Red–Green–Refactor 切分，标出精确文件、接口、测试、跨平台构建和回滚验证。
- 删除旧方案、编写新 spec、实现代码应保持可独立审查，不把无关 logging 修复混进 system-data-root PR。

## 6. 新设计必须明确回答的问题

新 spec 不得使用“按需”“适当”“沿用现有逻辑”等模糊措辞，至少要明确回答：

1. Windows、Linux、macOS 的默认数据根、控制 endpoint、安装根和覆盖优先级是什么？
2. daemon、TUI、普通 CLI、elevated installer 分别可以创建、读取、写入和删除哪些路径？
3. daemon/mihomo/TUI 日志分别存放在哪里，由谁拥有，具体 mode/DACL 是什么？
4. 多个本机用户同时运行 TUI 时，日志是否共享；如果隔离，identity 和路径如何确定且不依赖不可信环境值？
5. 日志导出由哪个进程执行，能读取哪些来源，目标目录由谁创建，如何保持 no-follow/no-replace/原子发布？
6. `PrivateFS` 是否拆分为多种 capability；谁有权修复系统根 ACL，谁只能在已授权目录内操作？
7. token 由谁创建；client 如何在每次请求或重连读取；credential 缺失、轮换或权限错误分别映射成什么稳定行为？
8. `mihari --help`、`self version`、service 命令和首次未安装 TUI 是否产生任何数据根 IO？
9. 绿装、升级、reinstall、自更新和 AIO 安装各自的停止、迁移、unit env、overlay、启动顺序是什么？
10. 迁移时如何处理仍在写旧根的旧 TUI、旧 daemon、mihomo 和日志轮转句柄？
11. 旧树中的日志、锁文件、socket/fifo、导出 zip 和临时 workspace 哪些迁移、忽略或拒绝；规则如何防止数据丢失？
12. 迁移提交后来源继续变化或无法删除时，哪一份是权威数据，用户看到什么错误，后续如何安全重试？
13. `MIHARI_DATA=<temp>` 的测试/便携场景是否继续允许 TUI 本地日志；它与默认系统根的权限策略如何区分？
14. Windows Users、Administrators、SYSTEM 以及 Unix root/普通 uid 的精确权限是什么？ACL/mode 修复是否会破坏正在使用的日志句柄？
15. 是否需要新增或修改 `/v1` 契约；若需要，如何保持兼容或引入新版本，并取得用户对公开契约变化的确认？
16. logging 初始化失败时，daemon、TUI、导出和控制连接各自继续、降级还是失败；错误如何脱敏？
17. 退出、自更新和 relaunch 前，session、export worker、logging runtime、lock、目录句柄及 root capability 按什么顺序关闭？

## 7. 验收标准

替代设计进入实施规划前必须同时满足：

- 第 4 节旧文件已经删除，没有 v13 增量修订或复制版本残留。
- 新 spec 明确引用并适配最终 logging 实现，而不是旧的单一 `mihari.log` 模型。
- 未提权客户端不会创建默认系统根、token、daemon 配置或系统私有日志目录。
- daemon 打开日志安全 capability 后不会撤销普通用户连接控制面所需的 token/channel/root 权限。
- TUI 日志和导出在线、离线、未安装、多用户、权限失败时的行为均有明确合同。
- 迁移算法覆盖仍在运行的旧 logging writer，不把“只停止 daemon”未经证明地当作静止来源。
- Windows ACL 与 Unix mode 表能够逐项映射到创建/修复函数，并有负向安全测试。
- credential 不在进程启动时冻死，首次安装和 token 轮换后的现有 TUI 可以恢复连接。
- 没有隐式扩大网络暴露面，没有把 controller secret 或完整订阅 URL 交给客户端。
- 新增依赖、持久化格式、公开 CLI/JSON 契约或安全边界变化均单独说明并取得用户确认。
- 测试计划覆盖单元、开发集成、race、Windows/Linux/macOS 行为和六目标 `CGO_ENABLED=0` 构建。
- 用户已经明确批准替代 spec，之后才允许编写实施计划。

## 8. 当前交接快照（仅供定位）

截至 2026-09-04：

- `feat/system-data-root` 尚无 system-data-root 实现提交；旧 spec 与评审文件均为未跟踪文档。
- `feat/file-logging-export` 已包含 logging 基础提交，并仍存在未提交代码和后续控制面/导出计划。
- 两个分支基线不同，不能把当前文本无冲突视为设计兼容。

该快照会过期。后续 agent 必须重新检查 Git 状态和最终代码，不能据此判断 logging 前置条件已经满足。
