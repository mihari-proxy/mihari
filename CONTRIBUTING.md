# Contributing to mihari

感谢你考虑为 mihari 做贡献！

## 开发环境

### 要求

- Go 1.26 或更高版本（见 `go.mod`，CI 使用 `go-version-file` 自动选择）
- Git

### Fork 并 Clone

```sh
# Fork 仓库后
git clone https://github.com/<你的用户名>/mihari.git
cd mihari
```

### 构建

```sh
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o mihari ./cmd/mihari
```

发布构建会额外注入版本号：

```sh
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X github.com/mihari-proxy/mihari/internal/buildinfo.Version=<tag>" -o mihari ./cmd/mihari
```

### 运行测试

```sh
go test ./...
go test -race ./...
```

> 默认 `go test ./...` 只使用 fixtures，不访问公网（面板下载等测试自动跳过）。

## 代码规范

### 格式化

确保代码通过 `gofmt` 检查：

```sh
gofmt -l cmd internal
# 无输出表示格式正确
```

### 静态检查

```sh
go vet ./...
```

### 代码风格

- 遵循 [Go Code Review Comments](https://go.dev/wiki/CodeReviewComments)
- 导出的函数/类型需添加注释
- 错误处理不要忽略
- 平台专用实现通过小接口及 `_windows.go`、`_unix.go`、`_linux.go`、`_darwin.go` 等文件隔离；通用文件不得散布平台分支

## 架构约束（不可破坏）

mihari 的架构不变量记录在仓库根目录的 `AGENTS.md`。核心几点：

- daemon 是持久化状态与 mihomo 生命周期的唯一所有者和写入者；CLI/TUI 只通过 `internal/control/client` 和版本化本地控制协议访问 daemon
- 本地控制 API 使用 Windows named pipe 或 Unix domain socket，**不得**退化为 TCP 监听
- mihomo controller 仅绑定 loopback；浏览器不得获得 controller 地址或 secret
- `internal/control/protocol` 的 `/v1` DTO、错误码、JSON envelope 与 CLI 退出码属于稳定契约；语义破坏需要新协议版本
- 发布构建保持 `CGO_ENABLED=0`

改动这些边界前，请先在 Issue/PR 中说明影响。

## 提交规范

### 提交信息格式

```
<类型>: <简短描述>

[可选的详细描述]
```

类型示例：
- `feat`: 新功能
- `fix`: Bug 修复
- `docs`: 文档更新
- `refactor`: 重构
- `test`: 测试相关
- `chore`: 构建/工具相关

示例：
```
feat(control): add subscription refresh endpoint

Persist refresh errors in the catalog so `sub show` can surface
the last failure reason without exposing subscription URLs.
```

### 提交要求

- 一个提交解决一个问题
- 提交信息使用英文
- 确保每次提交都能通过测试和构建

### DCO 签名

本项目要求每个提交包含 **Developer Certificate of Origin (DCO)** 签名，CI 中的 `dco` 工作流会校验。

在提交时添加 `-s` 参数自动签名：

```sh
git commit -s -m "feat: your feature description"
```

这会自动添加如下签名：

```
Signed-off-by: Your Name <your.email@example.com>
```

## Pull Request 流程

1. **创建分支**
   ```sh
   git checkout -b feat/your-feature
   ```

2. **编写代码并测试**
   ```sh
   go test -race ./...
   go build ./...
   ```

3. **提交变更**（记得 `-s`）
   ```sh
   git add .
   git commit -s -m "feat: your feature description"
   ```

4. **推送并创建 PR**
   ```sh
   git push origin feat/your-feature
   ```
   然后在 GitHub 上创建 Pull Request，按 [PR 模板](.github/PULL_REQUEST_TEMPLATE.md) 填写。

5. **等待审核**
   - CI 检查必须通过（test / race / vet / cross-build / DCO）
   - 至少等待一个审核通过

## 目录结构

```
.
├── cmd/mihari/           # 主程序入口：依赖装配、启动、进程退出
├── internal/
│   ├── app/              # 与表现层无关的用例编排
│   ├── buildinfo/        # 构建时注入的版本信息
│   ├── cli/              # cobra 命令定义
│   ├── config/           # 设置加载、校验与原子持久化
│   ├── control/          # 本地控制协议（protocol/server/client/credential/transport）
│   ├── core/             # mihomo 内核安装/更新
│   ├── daemon/           # 组件生命周期、启动顺序、优雅关闭
│   ├── elevate/          # 提权检测/提示
│   ├── geoip/            # 本地 GeoIP 数据下载与校验
│   ├── integration/      # 跨域集成用例
│   ├── mihomo/           # mihomo REST/WebSocket 适配
│   ├── onboarding/       # 首次设置流程
│   ├── panel/            # Web 面板安装与版本管理
│   ├── platform/         # 平台路径、浏览器打开等
│   ├── preferences/      # 用户偏好
│   ├── runtime/          # 运行时 mutation 编排与跨域事务
│   ├── service/          # 系统服务（kardianos/service 封装）
│   ├── state/            # 状态管理
│   ├── subscription/     # 订阅模型、缓存、生成、刷新、切换
│   ├── supervisor/       # 子进程、健康检查、重启与退避
│   ├── sysproxy/         # 系统代理（Windows 注册表 / GNOME / networksetup）
│   ├── tui/              # 终端交互界面（bubbletea）
│   ├── update/           # mihari 自更新（GitHub Releases）
│   └── web/              # Web gateway 与面板静态托管
├── install.sh            # Linux/macOS 一键安装脚本
├── install.ps1           # Windows 一键安装脚本
└── go.mod                # 依赖声明
```

## 发布流程（仅维护者）

参见 [RELEASE.md](RELEASE.md)。

## 问题反馈

- Bug 报告：使用 [GitHub Issues](https://github.com/mihari-proxy/mihari/issues)
- 功能请求：同样使用 Issues
- 安全漏洞：请参见 [SECURITY.md](SECURITY.md)

## 许可证

本项目采用 [GPL-3.0](LICENSE)。通过提交 PR 贡献代码即表示你同意以相同许可证发布你的贡献（inbound = outbound）。
