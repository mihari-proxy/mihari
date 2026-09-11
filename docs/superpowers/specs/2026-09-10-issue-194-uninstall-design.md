# Issue #194：简单全量卸载设计

状态：用户已批准收敛范围并要求继续实施。本文覆盖旧身份/事务设计。不得扩张 scope 或构建大型模块；未授权 commit、push、PR、真实卸载。

## 功能范围

仅做服务卸载与文件删除。沿用现有 `ResolvedLayout` 选择数据、程序、当前用户日志目录；按源码中 Mihari 实际生成的文件名和目录清单检查。未知项使预检失败，英文列出路径，不自动扩大清单、不强制跳过。名称匹配不是创建来源证明，不提供额外的身份/权限证明、事务日志、恢复状态机、维护协议或后台接管系统。

System 页 Logging 下、About 上增加 `Maintenance`，仅一行 `Completely Uninstall Mihari`。确认框默认 `Cancel`；Enter 默认取消，Esc 取消，必须主动选择 `Uninstall`。其余 section 和既有 `Uninstall service`、`Run Setup` 不变。所有新增固定应用文案英文。

CLI 增加 `service uninstall --purge --yes`，与 TUI 使用同一小型 app 用例。普通 `service uninstall` 保持原有行为。使用已有提权检查，不自动 sudo/UAC。

## 名称清单

清单直接根据 `internal/platform/paths.go`、写入代码和下载布局维护；不引入 manifest 生成器或新持久化格式。使用相对于各目标根的名称规则，精确匹配固定文件，严格限定随机 ID、日志轮转等已有模式。

- 数据根：`mihari.yaml`、`onboarding.json`、`control.token`、`daemon.lock` 及已有固定业务子目录（`bin`、`runtime`、`subscriptions`、`geoip`、`preferences`、`web`、`logs`、`logs-export`、`staging`、`locks`）。private/Windows 单根还包含既有控制、通道、安装锁名称。
- 程序根：当前 OS 的 `mihari`/`mihari.exe`，以及已有 Unix `.mihari-binary.lock`。
- 系统 B：`data`、`control.sock`、`control.token`、`mihari-channel`、`install.lock`、既有 endpoint 名称/锁、`install-control`。
- 当前用户 U：既有 TUI 日志/轮转/锁和默认日志导出。
- 固定 install-control：`state.json`、`previous-state.json`、`operation.lock`、`startup.lock`。只处理这个目录，不删除其 ProgramData/Mihari 父目录。
- 原子临时文件、缓存、provider/GeoIP/staging 使用源码已存在的精确前缀及 ID 格式；未知名字不放行。复用现有 Unix installer 的有限名称清单：`install-transaction.json` 和 `transactions/<32hex>/{transaction-id,unit,unit-bootstrap,ready.json,validation-launch.json}`，保证现有服务卸载留下的已知文件可删除；未列明 installer/scratch 仍拒绝。
- 面板资源位于 `web/zashboard/<build>`、`web/metacubexd/<build>`，下载内容文件名无法固定枚举。保守实现先将不能匹配清单的内容报告为未知项；用户尚未明确授权任意资源子树整体放行，不能静默添加 `**`。
- mihomo root 模式工作目录是 `runtime/core-home`，其他模式可能直接使用数据根；仅列入已确认的缓存名称。未识别的自定义 provider、core 临时产物同样停止提示。

所有目标先检查完再停服务。不存在的目标跳过；目录清单可以为空。普通遍历不跟随符号链接，未列明的链接/重解析目录视为未知项。已有安装 PATH 链接仅按现有服务/安装代码的精确路径处理，不扫描其他目录。自定义树外导出不在删除范围。

## 顺序执行

1. 检查管理员/root，解析既有布局，检查全部目标名称；展示将删除的目录。
2. 用户确认后退出 TUI alternate screen，复用现有 cleanup 关闭 worker、session、日志和 PrivateFS。
3. 调用既有 service 卸载入口（含 stop），用既有 status/控制探测等待服务停止；若仍有运行 daemon 则提示先停止，拒绝删文件。不新建 daemon 协议，不按裸 PID 杀进程。
4. 再检查一次名称，按当前数据/日志 → 程序 → 剩余控制目录顺序删除。重合或包含根按路径去重，避免重复；只删预览中的根。不调用任何真实服务进行开发测试。
5. 每一步向当前终端打印英文进度；任何错误立即停止并说明失败路径与可能残留，不回滚、不创建恢复记录。重试就是重新检查后再次执行同一命令。

Windows 当前运行镜像可能无法删除。沿用普通文件删除失败语义，报告程序残留并提示 `Run Mihari from a separate copy and retry the uninstall.` 不报告完整成功；不增加 helper、脚本、计划任务或重启删除机制。从 I 外启动时可以完成 I 删除。

## 小型实现边界

`internal/app` 只新增名称检查和顺序执行两个职责；平台专属代码仅用于既有布局/服务差异，不再使用前轮开发的卸载 identity observer。TUI/CLI 只做确认、呈现和调用。应用入口装配一次依赖。尽量复用现有 service、elevate、日志与 TUI cleanup；不新增第三方依赖。

## 验证

先行为失败测试再最小实现：已知名称通过、未知项不停止服务/不删文件、缺失根重试、服务失败阻断删除、删除失败报告残留、程序在用失败、TUI 默认取消/退出后执行、CLI 普通契约与 purge 必须 yes。使用临时目录与 fake 服务，绝不读写真实用户数据或系统服务。运行受影响包、相关集成测试，最终全仓/race/vet/格式及六目标 CGO0 编译；Windows/macOS 原生未执行项如实记录。
