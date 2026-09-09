# Issue #193：Mihari 降级警告与确认设计

日期：2026-09-09。版本：R2，一次独立审核后修订。状态：用户于 2026-09-09 明确批准 R2 并要求创建执行计划；此处为计划审核时的状态；后续实现与验收进度见执行计划 §17。

需求来源：[Issue #193](https://github.com/mihari-proxy/mihari/issues/193)。基线：`dev` 的 `60ca3ea`。工作分支：`codex/issue-193-downgrade-warning`。

用户要求：关闭混合 PR #196，两个 Issue 分开从头；本轮先完成 #193 的范围、方向和新设计，调用一次 subagent 审核，修订后汇报。旧 PR 不作为实现基础；#194 的数据重置、全量卸载不进入本方案。

## 1. 需求与范围

目标：在一个现有入口确实要把 Mihari 换成更旧版本时，在停服务、覆盖二进制或修改受管数据之前展示风险，取得确认后才执行。

警告必须完整说明：旧版本可能不支持新版本写入的设置、订阅、状态及生成文件，可能无法启动或读取数据，表现为数据丢失；降级不是受支持的配置迁移，不会自动回滚磁盘状态。所有产品文案为英文。

范围内：

- TUI System 页的 Mihari 更新，包括当前已支持的跨通道候选。
- CLI `mihari self update`。
- 普通安装、离线 AIO、远程 AIO 的 `.sh` / `.ps1` 六个入口及实际安装调用链。
- 这些入口会覆盖的 PATH/运行副本和服务安装副本；不能只检查发起命令的程序版本。
- 必要的候选固定、确认传递、替换前复核、失败/取消测试及说明文档。

范围外：配置向下兼容、schema 放宽、状态备份与恢复、任意版本回滚新命令、改变更新候选选择、#194、安装框架重写、新依赖、目录或权限模型迁移。手工复制旧二进制或执行已发布的旧脚本无法由新代码追溯保护，文档明确这项边界。

## 2. 当前代码事实与需求解释

| 现状证据 | 对方案的影响 |
| --- | --- |
| `internal/update/channel.go` 的 `classifyUpdate` 保留 stable ahead，并允许 dev → stable 候选 | 判断“能否更新”与“是否降级”必须分开；不能为了弹警告而开启原本不执行的降级 |
| `internal/update/prepare.go` 已按 fixed tag 下载并校验，`PreparedUpdate` 拥有清理函数 | 扩展既有候选机制，避免 Check 与 Update 再解析 latest |
| `internal/app/installer_unix.go` 提供 Prepare/ApplyPrepared，TUI Run 关闭资源后才 Apply | 风险交互必须在 TUI 退出前完成；不可在提交阶段等用户输入 |
| `internal/tui/pages/system/model.go` 先对 Check 结果确认，再 Prepare | 当前提示版本可能不同于实际准备的版本，必须消除此差异 |
| Windows 装配使用 `update.SelfUpdater.Update` 与 `AfterReplace` 同步服务副本 | 必须纳入服务副本降级检查，不能只覆盖 Unix 分支 |
| Unix 三份脚本有独立 root bootstrap，并调用 `service apply` | 确认必须显式跨 sudo/入口传递，不能依赖环境保留 |
| Windows 普通安装仍用 mutable latest URL，AIO 会覆盖软件、core/GeoIP 并 reinstall 服务 | 应在任何这些写入前完成统一的版本风险确认 |
| `docs/distribution.md` 第五节声称已装用户 self-update 会先降到次高版本 | 与 stable ahead 行为不符，本 Issue 修正文档，不修改候选选择来迎合旧文案 |

同一基础版本 `v1.2.3-dev.8 → v1.2.3` 是版本顺序上的升级；`v1.3.0-dev.8 → v1.2.3` 才是降级。通道名称本身不能代替比较。

AGENTS 引用的 `2026-08-03-mihari-architecture-design.md` 在当前分支不存在；架构核对使用当前 `docs/architecture.md`、`docs/unix-layout.md` 和现存 Unix 安装设计。安装中断恢复有独立设计演进；本 Issue 不重做其策略。

## 3. 方案比较与决定

1. **表现层预检查**：改动少，但实际执行重新选择候选、覆盖另一个副本、旧安装器或并发安装都可能让确认失效。
2. **推荐：固定候选、用例判定、表现层确认、提交前复核**：复用准备与替换路径；确认始终对应实际候选和目标集合，额外接口集中在更新/安装边界。
3. **禁用降级或统一重写三平台安装器**：前者违背需求，后者超出范围。

采用方案 2。只增加表达风险确认必需的类型与平台适配；不建立通用审批系统、数据库或长期授权 token。

## 4. 版本、目标与风险模型

### 4.1 比较规则

统一复用 Mihari canonical stable/dev 语义，允许版本输出省略小写 `v`，去首尾空白。按 major/minor/patch 数值排序；同基础版本 stable 高于 dev；dev 序号数值比较。无法解析或数值溢出为 unknown，不按 0 处理。不引入第三方 semver 库。

Go/Unix 官方更新的候选必须来自既有校验后的 fixed release/bundle 身份；这些路径要求的合法 tag 缺失或验证失败直接报错，不能用 --yes 绕过制品校验。既有 Windows 离线本地安装没有可信 release tag 时，候选版本可以为 unknown，以候选内容摘要绑定并明确提示两端兼容性无法确定；不能执行尚未获信任的候选来探测版本，也不能把用户输入的版本字符串当作已校验版本。此例外不放宽原安装流程对制品的既有检查。

Unix 既有离线 authority 只绑定摘要而不记录 tag。对已被该 authority 接受的候选，复用既有可信父目录能力，把同一份已校验 bytes 复制到本次私有临时 stage；在锁外使用有界隔离版本查询，并要求观察摘要及规范版本同时匹配请求。未知或不匹配直接拒绝，yes 不能绕过。不得执行请求源路径或任何尚未获信任的候选，不增加联网或 authority 格式；临时文件按身份清理。

旧副本不存在为 fresh；存在但版本不能确定为 unknown。unknown 使用“无法判断兼容性”的独立文案并要求确认，避免伪称确定升级；损坏安装仍可在明确确认后沿已有修复路径处理。权限/路径不安全或读取失败不能伪装成 fresh，既有安全错误优先返回。

### 4.2 实际目标集合

用例先根据现有服务发现与平台路径确定这次实际覆盖的目标：独立 binary、PATH 副本、受管服务副本，按规范化的实际替换目录项路径合并角色。同一路径的观察必须在身份、摘要、存在性与版本上相符，否则拒绝；不同目录项即使是同一 inode/FileID 的硬链接也分别绑定，不能按文件身份合并。Windows 大小写、短路径等指向同一目录项的别名由平台层规范化，不能把不同硬链接路径当作别名。不扫描其它安装根、其它 UID 或无关程序。

每个目标在只读预检中记录角色、存在性、文件身份、内容摘要和版本；服务定义参与目标集合指纹。任一目标版本高于候选即为降级；有 unknown 时也要求兼容性确认。TUI 显示每个版本不同的目标角色，不显示凭据或敏感配置。

官方发布使用 `-trimpath -s -w`。Go 1.26.5 在 trimpath 下不记录构建信息中的 -ldflags，不能用 `debug/buildinfo.Read` 恢复注入版本，也不新增嵌入元数据格式来假装解决已发布旧版本。

| 对象 | 版本来源与约束 |
| --- | --- |
| 正在运行的 CLI/TUI | `buildinfo.Version` 仅作显示参考，不能证明磁盘待替换副本版本 |
| 经现有权限/路径信任规则验证的已安装 binary | 在锁外、固定绝对路径上执行 `self version --json`；探测前后核对身份及摘要，把结果绑定到目标观察 |
| 提权进程面对用户拥有或可写的 binary | 不执行；记录 unknown，要求兼容性确认，仍不得绕过原有安装路径安全检查 |
| 非提权用户的本用户安装副本 | 可以在同一非提权身份下探测；不为探测提权 |
| 无法执行、版本查询失败或损坏旧程序 | unknown，允许用户明确确认后进入既有修复路径 |
| 准备的候选 | 官方路径使用已校验 tag；Windows 离线无可信 tag 时为 unknown 并绑定实际 digest；不运行未获执行信任的候选 |

版本子进程使用 context，默认 3 秒上限，stdout/stderr 各最多 4 KiB，超限终止并回收。只接受 `mihari/v1` JSON 的单一 version，按 §4.1 规范化并分类；不把 stderr 或整个输出写入日志。探测在隔离临时数据/控制路径和清理过的环境执行，避免旧程序初始化真实配置；临时资源归探测调用者所有。权限/路径核验失败与“可信旧程序查询版本失败”分开处理，前者沿用安全错误或禁执行规则，不能提升执行权限。

Unix root 只执行经过 root-owned、不可被其它用户写入的路径链验证的安装程序；Windows elevated 路径必须通过 SYSTEM/Administrators 控制的 ACL 核对，否则只得到 unknown。不存在这种可信路径时不引入降权执行基础设施。被本次替换的包内候选以已校验的 release 身份为准，版本识别不授予信任。

Go 与脚本使用相同版本 fixture。未知版本确认是显式产品选择；部分便携/用户可写副本在管理员操作时可能显示 unknown，此限制比执行用户 binary 更可控。

### 4.3 生命周期内的确认

拟在用例内部引入 `ReplacementPreview`（候选 tag/digest/channel、目标观察集合、风险）及 `ReplacementConsent`（无确认、交互绑定确认、当前命令显式 yes）。不持久化，不经过 daemon /v1。

- 交互确认绑定候选及完整目标集合；可用普通类型化值，无需随机 nonce 或 HMAC；跨进程绑定采用下述 preview_id。
- CLI 的 --yes /脚本 MIHARI_YES=1 接受该次命令的兼容性风险，仍只作用于这次准备的候选；不授予替换任意路径的权限。
- 在提交复核时观察到服务定义、目标文件身份/摘要、候选 tag/digest/channel 变化，当前预览失效。即使 --yes 也不忽略已观察到的身份变化；当前尝试退出，用户可重跑命令。Windows 复核后的外部并发窗口及后续副本同步见 §5.3，不声称跨文件原子性。
- 持锁复核通过后才停服务/写入。不得在持锁期间等待 UI 或网络。准备下载继续在临界区外；本任务不顺带重构既有独立校验 IO，但不能新增锁内下载。

### 4.4 跨进程预览绑定契约

Unix helper 返回的风险错误 `details` 增加 `reason: replacement_confirmation_required`、`risk`、`targets`（安全角色与版本）、`target_version`、`preview_id`。preview_id 为 64 位小写十六进制 SHA-256，覆盖内部版本号、fixed candidate tag/digest/channel、规范化目标身份/摘要/存在性和服务定义指纹。服务不存在也进入指纹；候选临时文件路径不进入跨进程指纹，重建同一已校验候选不会伪报变化。目标和服务对象路径在摘要内部参与绑定，对外不输出路径或凭据。

使用固定顺序的 Go struct JSON 编码、按规范化目标路径排序并合并同一路径的角色；不同硬链接目录项分别保留；所有 helper 自己产生和比较，脚本只回传，不自行计算。内部指纹版本改变会造成旧确认失效，重新开始即可；不是长期存储或安全 token。

`service apply` 新增 `--expected-preview <preview_id>`。交互接受后的第二次调用必须同时传 `--yes --expected-preview <原值>`，在获得现有安装所有权后重新计算并比较，然后才能进入停机/提交。格式无效为 invalid_argument；不匹配为 invalid_state，不能自动去掉 expected-preview 重试。目标版本即使相同、文件内容/定义已变化也拒绝。

命令最开始就有 --yes 的非交互调用可以不带 expected-preview：它接受这次命令的风险，但仍在当前调用中预览并在提交前复核。expected-preview 不独立授予确认，也不能替代 --yes。直接 --expected-preview 缺 yes 时返回 invalid_argument，提示该参数必须与 --yes 一起使用；即使本次是升级也不执行。

## 5. 更新与安装执行流程

### 5.1 CLI / TUI 更新

`发现实际目标 → 选择并准备固定候选 → 生成预览 → 确认 → 取得既有替换所有权 → 复核 → 替换及既有补偿 → 清理候选`。

`Check` 只负责列表展示，可过期。实际操作不使用其旧结果作为授权：在现有准备流程中取得最终候选，再做最终风险确认。准备阶段可下载 inert 临时文件，但不得停服务或改受管数据。

进程内准备快照与公开确认参数分开传递：普通升级没有 --yes 时仍对准备快照进行提交复核，不自动构造必须配合 --yes 的 --expected-preview 参数。Unix ApplyPrepared 保留现有 start=false，直接安装保留原 start=true；风险确认不改变服务启动策略。

沿用现有 `Available/Ahead` 判断；无候选时按现有 up-to-date/ahead 返回，不弹降级确认。Windows updater 增加对既有 PreparedUpdate 的执行适配，复用原来的验证、平台替换及失败补偿、AfterReplace，不复制下载/替换实现。

TUI 点 Update 后进入可取消的 Preparing 状态，待候选就绪才展示最终确认（替换旧的“先确认 Check、再准备”顺序）。普通升级沿用现有确认文案；降级/unknown 使用强确认。取消、页面离开、TUI 退出及准备失败均由 Run 的既有 worker 所有者回收候选，防止迟到消息重新启动安装。

用户确认后再发 relaunch 请求，Run 关闭 worker、IPC、日志、文件所有者，再 Apply。若退出前发现预览变化，丢弃确认并回到准备流程；若退出后提交复核才失败，安全退出并输出“安装已变化，请重新打开 Mihari 后重试”，不替换，不承诺已退出的 TUI 能继续弹框。

CLI `self update --yes` 接受风险。未确认降级/unknown 时退出 2，错误使用现有 invalid_argument；成功 JSON envelope 不变。文本模式在执行前把警告写 stderr；JSON 模式先缓冲安全警告，执行及候选清理成功后写 stderr，失败则把警告并入现有 APIError.Message，由统一出口仅输出一个错误 envelope，不能先输出纯文本再输出错误 JSON。未确认时风险直接由错误 envelope 表达。--yes 不能完全隐藏风险文案。

### 5.2 Unix 安装与 service apply

`service apply` 增加 `--yes`（接受这次安装的兼容性风险）；确认及 expected-preview 经新增类型化 app 参数传入，保持 `mihari.install-request/v1` JSON 与 journal 格式不变。直接 service apply 不做交互；缺少确认返回现有错误 envelope 的 invalid_argument、退出码 2，details 按 §4.4 返回安全风险、版本、角色和不透明 preview_id。已确认路径通过本次调用可选的内部 `ReplacementConsent.Warn func(string) error` 回调报告安全文案，在锁外 Validate 成功后、任何安装写入前调用一次；回调失败则停止。它不进入指纹、持久化或公开 DTO。CLI 按 §5.1 在文本模式即时输出、JSON 模式缓冲并按最终成败分流。

脚本先解析固定候选并进入可信 root bootstrap，在仍未修改受管状态时调用受支持入口完成检查；若入口报告需要确认，脚本向用户显示准确风险，在锁外默认否询问，然后对同一固定候选重试并传 `--yes --expected-preview <原值>`。root bootstrap 持有固定候选和请求临时文件，释放 helper 调用取得的锁后才提问；提问期间保留 bootstrap 自己的临时文件，最终统一清理。显式 MIHARI_YES=1 提前转成固定参数跨 sudo 传递，内层仍输出警告。版本/目标变化不得被外层自动循环吞掉。

app 的缺确认检查必须在可能修改状态的 `RecoverLocked` / `session.prepare` / `ApplyLocked` 之前。存在 pending 事务、无法得到稳定目标集合时，先返回既有不可用/需要恢复提示；本 Issue 的预检不得为弹警告自动恢复旧事务。已经取得确认也不能据此覆盖 pending 事务。此限制仅针对准备新替换的分支；明确请求的既有 recover 操作及已获授权事务的补偿语义保持原样，不让新增 --yes 变成恢复操作的前置条件。

旧入口兼容：当前脚本通过可信 helper 的帮助输出检查同时存在 --yes 和 --expected-preview，确认其支持本契约；只检查 flag 不授予可执行文件信任。不能仅凭存在 service apply 就传新参数，也不能失败后退回无保护执行。优先使用已有可信、支持契约的安装器；线上可取得当前通道的官方已校验安装器作为 helper，候选 release tag 保持用户选择；候选与 helper 版本相互独立。若没有可用 helper，明确说明需要更新安装器后再安装所选旧版本。离线不隐式联网，要求已有支持契约的可信 helper，否则在写入前拒绝并给准备方式。远程 AIO 解压后的候选仍属于在线入口；在线/离线模式通过固定位置参数跨 sudo 传递，不能根据候选路径非空推断为离线。

这项限制针对旧入口能力，不是禁止安装旧版本。首次发布和撤回导致 latest 也不支持契约时需覆盖失败说明，不能声称旧代码已获得保护。

### 5.3 Windows 脚本与 AIO 交接

普通安装先解析 latest 为 tag，再使用固定 tag 下载。AIO 按实际候选摘要绑定已验证的 release 元数据；远程 index 的标签不能代替 bundle 身份。Windows 本地旧包没有可信版本证据时按 §4.1 的 unknown 候选处理，提示无法确定目标版本并要求确认，仍可沿既有安装检查后受控安装。远程外层可以提示下载计划，但最终风险确认必须在本地候选和全部替换目标已知后执行。

风险判断必须早于 PATH/sidecar 写入、服务停止、软件/core/GeoIP 拷贝。现有服务停机询问可合并到最终安装确认，不得在风险确认前已经停机。保留既有提权策略，不新增自动提权机制。

远程 -Yes/--yes 与 MIHARI_YES=1 显式传给内层；交互接受可传递进程局部确认，但必须绑定 tag/digest/目标指纹，不能把普通“开始下载”接受当作降级接受。临时环境传递在 finally 中恢复，不能污染后续命令。

Windows 当前 `replace_windows.go` 只有 stash/rename，AIO 是 Copy-Item，没有可复用的原子目标身份保护。本 Issue 不扩展为跨进程、多文件安装事务。新增小的“替换前核对”平台回调：在当前主程序替换前、服务 Stop 前、服务 binary stage 前，以及 AIO 首次受管写入前核对绑定预览；候选在每个实际使用边界核对 digest。普通脚本提权后也消费同一预览，而非重新认可新目标。

Windows 自更新把服务目标预览传给 `SelfUpdateServiceCompletion`，新增本次调用的校验回调/参数；不能只保留无状态 `AfterReplace(version)` 后重新无条件发现服务副本。若主程序替换前发现变化，返回未修改；主程序已替换而服务同步复核失败，则停止后续覆盖，返回 `Updated=true` 与既有同步警告。如刚停止服务再发现 stage 目标变化，按既有失败路径尝试恢复原服务运行状态，不能覆盖新发现的副本。

在同一进程内部序列化自己的提交操作，复用 Unix 已有安装锁/文件身份检查。Windows 只保证在上述边界对可观察变化拒绝；未参与同一操作的外部安装器可在检查与 rename/copy 之间修改对象，现有架构下仍有窗口。此次不声称原子 CAS、不新建机器互斥或持久锁格式；对敌对并发替换的全面防护属于独立安全工作，不能用一遍 Get-FileHash 宣称已经解决。测试验证明确边界与部分成功，不断言不存在窗口。

旧 Windows bundle 策略固定为：远程脚本只自动交接给声明本确认契约的当前本地安装器，缺失能力时在任何安装写入前拒绝，保留已校验解压包，并提示使用当前版本 `install-aio.ps1 -BundleDir <已解压旧包>`（需要无人值守时显式 MIHARI_YES=1）。当前本地安装脚本必须支持消费旧 bundle 数据并完成相同预览/确认，因此用户仍可受控降级；不是禁用旧目标版本。

当前本地脚本增加仅查询能力的 `-Capabilities` 参数，输出固定机器可读 `mihari.install-script/v1` 和 `replacement_confirmation_v1`，在访问 bundle/数据目录前返回。远程必须先对已校验来源的脚本静态检查固定整行标记 `# MIHARI_INSTALL_CAPABILITY: replacement_confirmation_v1`，没有标记绝不调用脚本做能力探测（旧 PowerShell 脚本可能忽略未知参数而直接执行安装）。只有标记存在才调用 -Capabilities，并严格校验 schema 和能力；失败视为不支持，不回退执行安装。标记不是信任根，来源校验仍必须独立成立。离线可以事先保存当前独立 install-aio.ps1，不隐式联网取得它。本轮不新增当前安装器脚本的运行时下载依赖。已发布旧脚本本身不受新代码追溯保护。

### 5.4 通用脚本规则

仅当本次操作需要交互确认且缺少显式确认时，非交互立即失败，不能无期限 Read-Host/read；有终端时接受默认否。新风险策略不因没有终端而阻止原本无需询问的普通升级/首次安装。取消返回非零并说明未安装，不输出成功。MIHARI_YES 只认值 1。测试版本注入只在 MIHARI_INSTALL_TEST_MODE=1 生效。

六份脚本仍独立分发，不增加运行时公共脚本下载依赖。Unix root bridge 必须修改 `scripts/install/root-apply.sh.in`，运行 `python3 scripts/install/generate_root_apply.py` 生成三份脚本，运行 `test_root_apply_generated.py` 检查漂移；不手工维护三个生成区。其它纯版本比较的必要小规模复制以共享测试数据控制语义一致性。下载/校验失败不自动选择另一个版本重试。

## 6. 错误、兼容性与安全边界

| 情况 | 对外结果与效果 |
| --- | --- |
| downgrade/unknown 缺确认 | CLI invalid_argument / 2；脚本失败提示确认方式；受管状态不变 |
| 用户取消 | TUI 回 System；脚本非零退出；释放临时候选 |
| 首次写入前观察到安装或候选变化 | invalid_state；释放候选，要求重新开始；无本次替换 |
| Windows 主程序已更新、服务同步前观察到变化 | 停止服务副本覆盖，保留 Updated=true 和同步警告，不伪报零修改 |
| 路径/权限/下载/校验失败 | 沿用已有错误分类；yes 不绕过 |
| helper 不支持契约 | 清楚报错，保留安装；不调用无保护路径 |
| 已经替换后的现有同步失败 | 保留既有 Updated/警告/补偿语义，不能伪报“未作修改” |

公开变化：self update --yes、Unix service apply --yes/--expected-preview、风险错误的 details 扩展、Windows 本地安装脚本 -Capabilities，以及降级/unknown 未确认时的拒绝行为。成功 CLI JSON、daemon /v1 DTO、安装请求 JSON、退出码定义及持久化格式不变。unknown 确认是本方案的保守产品选择，已随 R2 获用户批准。

不为日志输出凭据、订阅 URL、控制 secret、完整 ldflags 或环境变量。降级警告不表示软件获得了读写额外数据根的权限。既有 CGO-free 六平台目标不变。

## 7. 任务方向与变更落点

这是设计级任务划分，不代替设计批准后的逐步 TDD 实施计划。

| 顺序 | 工作 | 主要落点 | 完成条件 |
| --- | --- | --- | --- |
| 1 | 风险分类、有界可信版本探测、预览/确认类型 | internal/update、平台身份小接口 | 版本/目标集合矩阵及不提权执行用户 binary 的测试通过 |
| 2 | 绑定候选和最终复核 | update/prepare、Windows updater、app installer/binary target | 变化/拒绝测试证明停机和写入前拒绝 |
| 3 | CLI 与 TUI 生命周期接入 | cli/self、service_apply、tui System/Run/worker、cmd 装配 | 确认、取消、JSON、退出后复核失败和候选清理测试通过 |
| 4 | 三类六脚本接入 | scripts/install、root-apply.sh.in、生成器一致性测试 | latest/固定版本/旧 bundle/helper/无终端/指纹交接测试通过 |
| 5 | 集成验收与说明 | internal/integration、docs/commands、architecture、distribution、安装帮助 | 需求逐项追踪、独立 PR 只关联 #193 |

先解决 Go 执行边界再接脚本，避免只在 UI 上完成警告。实现若要求新持久格式或更广安装改造，应停下重新审阅范围，不在本设计里默许扩展。

## 8. 验收矩阵

| 场景 | 必须验证的可观察结果 |
| --- | --- |
| stable ahead / 同版 / 首次安装 | 原候选策略不变；不生成虚假降级操作 |
| dev → 旧 stable、stable 覆盖旧 stable、dev 序号倒退 | 完整英文风险；拒绝/取消无停机或受管写入；确认后执行 |
| 同基础 dev → stable | 比较为升级，保留一般确认 |
| 旧版本未知 / 查询超时或超限 / 版本溢出 | unknown 文案与确认；不执行用户拥有的旧 binary 来探测 |
| CLI/TUI 版本与服务/PATH 副本不同 | 按全部实际目标提示，任一降级需要确认 |
| Check 后 latest 撤回或换指向 | 预览使用准备后的候选；确认后不换包 |
| 准备/确认/提交复核时发现目标、定义或候选变化 | 即使 yes 也拒绝；首次写入前无修改，后续 Windows 同步按部分成功处理 |
| 两次 Unix helper 调用间目标或服务定义变化 | expected-preview 不匹配，版本相同也拒绝，不停服务、不自动重试 |
| 普通 install 默认 latest / MIHARI_VERSION | 固定实际 tag；两个路径均能触发降级确认 |
| 本地/远程 AIO 与旧 bundle | 确认先于 core/GeoIP/软件写入；旧内层不会绕过保护 |
| remote yes、MIHARI_YES、无终端 | 参数全链路有效；不重复提问、不挂起；取消不报成功 |
| pending 事务 / unsupported helper | 明确失败；为确认执行的检查无恢复/停机副作用 |
| TUI 取消、退出、迟到准备结果、Apply 失败 | 所有临时文件/worker 有明确清理；不在 TUI 退出后发确认框 |
| CLI --json | 成功 stdout envelope 不变，警告写 stderr；失败 stderr 只有一个包含风险的错误 envelope，包括已确认后执行或清理失败；不泄露配置 |

测试用 t.TempDir、fake 服务/安装器、httptest 与脚本临时 fixture；不访问公网/真实用户数据，不启动真实服务。版本探测测试使用固定 Go 1.26.5、官方 trimpath/stripped 构建形式的 fixture；各原生平台运行本机 ELF/PE/Mach-O 的纯 self version fixture，跨编译产物不能在错误宿主上执行。另用 fake runner 验证超时、输出上限、信任拒绝、身份变化及资源回收。不得读取或执行当前机器真实安装来验证。

按范围逐步运行相关包、integration、go test ./...、race/vet/format；脚本测试在 sh、Windows PowerShell 5.1 环境验证，不能只测试字符串里存在警告。发布验证包括六目标 CGO_ENABLED=0 编译及既有 unix-layout-security 必需检查。无法运行的平台项在交付中明确列出。

## 9. 设计交付与批准边界

本轮只修改此设计和独立审核记录，调用一次 subagent 审查范围、架构可实现性、入口完整性及测试，再由主代理核对并修订。用户未要求提交，本轮不 commit/push。

设计批准与功能完成分开记录。最终汇报应说明实际修订、保留的兼容性限制与需要用户确认的公开行为；不得把文档完成称为 #193 功能已完成。


## 10. 一次审核后的修订记录

审核来源：[R1 独立审核报告](../reviews/2026-09-09-issue-193-downgrade-warning-review-r1.md)。主代理逐项核对代码后作以下调整；此处记录首次设计审核阶段；用户随后授权的计划迭代审核另见 §11。

| 审核项 | R2 处理 |
| --- | --- |
| B1：trimpath 不记录 ldflags | 删除不可用静态版本主路径，明确可信版本子进程、提权禁执行用户 binary、unknown 和原生 fixture 矩阵 |
| M1：交互后 --yes 丢失身份绑定 | 增加 preview_id / --expected-preview 的生成、传递、复核及失败契约；显式 yes 与交互接受分开 |
| M2：Windows 不存在所称原子保护 | 改为具体写入边界回调；服务同步消费原预览；区分首次写入拒绝与 Updated=true 部分成功，明确外部并发窗口 |
| m1：旧 Windows bundle 策略未定 | 固定能力检查拒绝旧内层，保留旧包并指向当前本地安装脚本的受保护安装路径 |
| m2：Unix 生成模板遗漏 | 纳入 root-apply.sh.in、生成器和一致性测试；明确新替换预检不改变显式 recover 的既有语义 |

随 R2 已获用户批准的产品/兼容性选择：unknown 也要确认；新增 CLI 参数及 details；没有支持契约的 Unix helper 时拒绝；旧 Windows bundle 可能需要当前独立安装脚本；Windows 仅提供明确操作边界的复核而非新跨进程原子事务。

主代理最终自检另补：旧 PowerShell 能力探测先静态检查已校验脚本标记，避免旧脚本忽略未知参数后实际安装；Windows 离线未知候选通过摘要绑定和明确 unknown 确认保留修复用途；无风险的非交互普通升级不增加强制确认。

执行计划：[Issue #193 Downgrade Warning Implementation Plan](../plans/2026-09-09-issue-193-downgrade-warning.md)。计划创建不表示功能已实现或已授权提交/推送。

## 11. 已批准范围内的计划审核澄清

用户在批准 R2 及创建计划后，要求 subagent 迭代审核直到通过，并严格禁止扩大 scope。本次仅澄清 §5.1 的两个既有行为：内部准备快照不改变公开 yes/expected 参数组合；Unix 自更新和直接安装保留各自的服务启动策略。R2 产品选择、架构边界与功能范围不变，未发现必须扩大 scope 的 P0/P1。

详细问题、修订及复审结论见[计划迭代审核记录](../reviews/2026-09-09-issue-193-plan-review.md)。此处为计划审核时的状态；后续实现与验收进度见执行计划 §17。

## 12. 追加限定范围审核澄清

本轮按用户要求再次审查设计与计划。只澄清三项既定要求：JSON 失败仍为单一错误 envelope；目标按规范化目录项路径绑定而非按 inode 合并；验证固定使用 Go 1.26.5。没有增加入口、依赖、持久化格式或安全机制。已存在的实现工作保留，功能尚未完成；文档审核不能替代实现验收。各轮结论见计划迭代审核记录。
