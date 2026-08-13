# Changelog

本文件记录 Mihari 每个版本的变更。版本遵循 [语义化版本](https://semver.org/lang/zh-CN/)。

## [Unreleased]

### Added

### Fixed

## [v0.5.0] - 2026-08-13

### Added

- mihomo 核心 stable / alpha 通道切换：System 页与 daemon 可在两通道间切换核心版本（#48）
- TUN 多实例冲突检测：开启 managed TUN 前检测系统其他 TUN 网卡与其他 mihomo 进程，冲突即拒绝（HTTP 409 / CLI exit 6），可用 `--force` 或 TUI 二次确认覆盖，并按情形给出证据（#45）
- System 页系统代理 / TUN 行拆为状态行 + 动作行：状态行持续显示真实状态（不再被 Done badge 遮盖），动作行承载 toggle 并绑定 pending / Done / Failed；Done badge 约 3s 自动淡出，Failed 保持 sticky（#43）
- TUI 选中行改用对比色背景渲染替代纯反白：焦点行用暗紫背景填充，rules / logs 最宽列获得连续背景，光标一眼可辨（#47）

### Changed

- 订阅配置加载引入分层：`KnownFields` 未知字段报错改为点名具体字段并提示可能为更高版本写入或拼写错误（#49）

## [v0.4.1] - 2026-08-12

### Added

- TUI stale footer 显示最后数据时间 `last observed HH:MM`：daemon 控制连接断开后，footer stale 横幅追加最后一次成功数据推送的本地时间，便于判断屏幕上冻结数据的新旧（#37）

### Fixed

- System 页 daemon / mihomo core 行在控制连接断开后降级显示：保留末值但圆点转黄并追加 `· Stale`,与 Overview core 卡及同页其它行一致（#36）

### Changed

- 仓库根目录整理：install 脚本归入 `scripts/install/`，本地编译产物统一到 `bin/`，社区文档（CONTRIBUTING/CODE_OF_CONDUCT/SECURITY）归入 `.github/`；在线一键安装 raw 地址相应变为 `.../main/scripts/install/install.{sh,ps1}`，AList 离线直链不变（#27, #44）

## [v0.4.0] - 2026-08-12

### Added

- 订阅级代理模式：每个订阅可独立配置拉取代理，失败回退直连（#33）
- TUI System 页自更新 Mihari：在 TUI 内检查 / 下载 / 应用 Mihari 自身新版本（#34）
- onboarding 状态反馈：Setup 流程新增端口预检、本地就绪检测、review 汇总与服务状态展示（#35）
- TUI 数字键 1–8 快捷跳转各 rail 页面（#31）

### Fixed

- Rules 页 Type 列宽自适应，完整显示 `DomainSuffix` / `DomainKeyword` 等长类型名（#30）

## [v0.3.1] - 2026-08-11

### Fixed

- Overview 页对系统代理 desired / owned 状态漂移打标（#22）
- 安装脚本中文乱码修复，新增下载进度显示与安装前确认（#21）

## [v0.3.0] - 2026-08-09

### Changed

- 离线分发去 sign 化 + 适配 AList 深层路径：AList 改为公开分发（关闭签名），所有直链去掉 `?sign=`；`install-aio-remote.{sh,ps1}` 改为硬编码固定公开 `INDEX_URL`（去掉 CI 占位符注入），README 安装命令变为永久固定字面量，复制即用、无需手改。base_path 适配当前 AList 拓扑 `/mihari-release/mihari`，`public_url` 注入 `/public` 中缀以绕过 AList 的 fs/API 路径与 `/p` 下载路由前缀不一致的 quirk。

### Fixed

- 修复国内离线安装命令失效：签名直链全部返回 `sign invalid`（401）。根因为 AList 签名机制在部署层失效；改公开分发后根除（前置：AList 存储关闭签名）。
- 修复 AList 发版文件落点错误 + 上传静默失败：**所有** fs API（get/list/put/mkdir/remove）都把传入路径当相对存储根 `/mihari-release` 解析、再拼一次前缀，导致读（`exists`/`list`）查的是 `/mihari-release/mihari-release/mihari/`、写也落到那里，而 `/p/public` 公开下载用的是逻辑路径 `/mihari-release/mihari/`——三者不一致：v0.3.0 的 `index.txt` 更新了但 bundle 仍指向读路径上不存在的文件。`alist_client._fs_path` 对**所有 fs 操作（读+写）**去掉首段，让读、写、公开下载落到同一处；同时 `upload`/`upload_text` 改为检查 AList body `code`（AList 永远返 HTTP 200，真失败此前被静默吞掉），写失败即报错。

## [v0.2.0] - 2026-08-07

### Added

- all-in-one 整合包：6 平台（linux/darwin/windows × amd64/arm64）离线整合包，内含 mihari 二进制 + mihomo 核心 + GeoIP（Country + ASN），供网络受限环境一键安装（#11）
- AList 国内分发：整合包发布到 AList 网盘的不可变版本目录，配 `index.txt` 路由表与签名直链，墙内用户免 GitHub 访问离线安装（#11）
- 离线安装脚本：`install-aio.sh/.ps1`（安装本地整合包）、`install-aio-remote.sh/.ps1`（从 AList 下载并安装，含 sha256 校验、版本检查、服务停启）（#11）
- release 工作流：新增整合包打包作业、版本闸门（`^vX.Y.Z$`，禁预发布后缀）、条件化 AList 上传（未配置 secrets 时不阻塞 GitHub 发布）（#11）
- retract 工作流：手动撤回致命错误版本（删 AList 版本目录 + 重建 `index.txt` + 删 GitHub release/tag，幂等）（#11）

### Fixed

- 控制平面优雅关闭误报 `context deadline exceeded`：`control/server.Serve` 在关闭预算内未排空在途连接时不再把超时当致命错误传播，消除集成测试在慢速/-race CI 上的 flake（#12）

### Docs

- 新增 [分发方案](docs/distribution.md)：安装入口、AList 布局、`index.txt` 格式、发布顺序、保留策略、撤回流程

## [v0.1.1] - 2026-08-06

### Fixed

- 修复 Windows 上并发首次加载配置时,文件原子替换窗口内的共享违规被误报为错误(`loadSettings` 以文件存在为信号重试,与锁文件兜底逻辑一致)
- 修复 GitHub Actions 的 Node 20 弃用警告:升级 `actions/checkout` v7、`actions/setup-go` v7、`golangci-lint-action` v9、`actions/upload-artifact` v7、`actions/download-artifact` v8、`softprops/action-gh-release` v3
- 修复分支保护 required check `cross-build` 因矩阵展开命名而永远 "Waiting for status" 的问题:新增 fan-in gate job 精确报告该状态
- 修复 `release` 工作流手动触发(workflow_dispatch)只构建不发布的问题

### Docs

- 新增 CHANGELOG.md,发布流程移至 docs/RELEASE.md
- README 标题品牌名统一为大写 Mihari

## [v0.1.0] - 2026-08-06

**初始开源发布**

- 本地守护 daemon：Windows named pipe / Unix domain socket 控制平面，不绑 TCP
- 系统服务支持（Windows Service / systemd / launchd，kardianos/service），不自动提权
- mihomo 内核安装 / 更新 / 重启 / 健康检查 / 崩溃退避重启
- 订阅管理：profile 模型、独立缓存、离线切换、定时刷新、原子配置生成与回滚
- 交互式 TUI：Setup、Overview、Proxies、Connections（本地 GeoIP）、Rules/Providers、Logs、订阅管理、System、Web GUI
- 浏览器面板：zashboard / MetaCubeXD 安装、更新、激活、回滚、打开；loopback Web gateway + 独立访问凭证
- 系统代理管理（Windows 注册表 / GNOME / networksetup）与 TUN 持久化
- GeoIP 本地解析（MMDB 下载、校验、30 天刷新）
- CLI 全命令 `--json` 输出与稳定退出码
- 自更新（GitHub Releases）+ 6 平台无 CGO 预编译发布
