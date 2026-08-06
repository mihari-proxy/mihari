# Changelog

本文件记录 Mihari 每个版本的变更。版本遵循 [语义化版本](https://semver.org/lang/zh-CN/)。

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
