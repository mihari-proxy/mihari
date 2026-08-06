# Security Policy

## Supported Versions

| Version | Supported          |
| ------- | ------------------ |
| latest  | ✅ |
| < 1.0   | ❌ |

## Reporting a Vulnerability

**请勿在公开 Issue 中报告安全漏洞。**

请使用 GitHub Security Advisories 私密报告：

1. 访问 [Security Advisories](https://github.com/mihari-proxy/mihari/security/advisories)
2. 点击 "Report a vulnerability"
3. 填写漏洞详情

### 报告内容

- 漏洞描述
- 复现步骤
- 影响范围
- 可能的修复建议（如有）

### 响应承诺

- **确认时间**：3 个工作日内确认收到报告
- **评估时间**：7 个工作日内评估漏洞严重程度
- **修复时间**：根据严重程度，尽快发布修复版本

### 披露政策

- 未经报告者同意，不会公开披露漏洞详情
- 修复发布后，会在 Release Notes 中致谢报告者（如愿意）

## 安全模型

mihari 的设计目标是「本地唯一、不暴露到网络」。理解以下不变量有助于报告准确的问题：

- **控制管道不绑 TCP**：本地控制 API 使用 Windows named pipe（`\\.\pipe\mihari-control`）或 Unix domain socket，永不监听 TCP 端口。
- **控制器仅回环**：mihomo controller 只绑定 loopback，浏览器/CLI 永远拿不到 controller 地址或 secret。
- **Web gateway 默认回环**：`web-addr` 默认 `127.0.0.1:9191`；浏览器认证使用独立于 controller secret 的 Web access credential，绝不打印到 status/日志/默认输出。
- **未知写操作拒绝**：所有 Web 面板的 REST/WebSocket 写操作经过统一 mutation coordinator，未知写操作默认拒绝；核心升级与托管字段写入永远不直达 mihomo。
- **不覆盖他人代理**：`sysproxy enable` 遇到他人代理默认失败（`system_proxy_conflict`），`--force` 才覆盖；`sysproxy disable` 只清除 mihari 自己的代理。
- **订阅 URL 不外泄**：订阅 URL 只存 daemon 私有目录，list/show 响应与普通错误中不包含。

## 安全建议（用户侧）

- 不要将数据目录（含 `control.token`、订阅 URL）提交到公开仓库或分享给别人
- 默认仅回环访问 Web 面板；如需远程访问，配置 TLS 反向代理而不是直接暴露端口
- `sysproxy enable` 冲突时优先排查另一个产品的代理，而不是盲目 `--force`
- 定期更新到最新版本

## 处理范围

以下场景**不属于**本仓库的安全漏洞范畴（请使用普通 Issues 反馈）：

- 依赖 mihomo 自身的安全问题（请向 [MetaCubeX/mihomo](https://github.com/MetaCubeX/mihomo) 报告）
- Web 面板（zashboard、MetaCubeXD）自身的安全问题（请向对应上游报告）
- 需要管理员权限才能执行的本地操作被拒绝（这是设计约束：mihari 不自动提权）
