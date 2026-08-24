# Contributing to mihari

感谢你考虑为 mihari 做贡献！

## 开发环境

### 要求

- Go 1.26.5（`go.mod` 的语言版本为 1.26.0，并用 `toolchain go1.26.5` 钉死工具链；CI 与发布 workflow 均通过 `go-version-file: go.mod` 选择该版本）
- Git

### Fork 并 Clone

```sh
# Fork 仓库后
git clone https://github.com/<你的用户名>/mihari.git
cd mihari
```

### 构建

```sh
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o bin/mihari ./cmd/mihari
```

发布构建会额外注入版本号：

```sh
CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags "-s -w -X github.com/mihari-proxy/mihari/internal/buildinfo.Version=<tag>" -o bin/mihari ./cmd/mihari
```

发布构建必须同时使用 `-buildvcs=false -trimpath`。前者避免同一 commit 在创建 tag 前后被 Go 写入不同的 VCS/module 元数据，后者去除本机构建路径；版本身份仍只由上面的 `buildinfo.Version` 注入。

all-in-one 发布输入固定在仓库内的 `scripts/release-inputs.lock.json`。发布 workflow 只消费该文件，不在发版时查询 mihomo 的 latest release 或 GeoIP 的可变分支。需要更新上游输入时，维护者应在独立的 release-prep PR 中运行：

```sh
go run ./scripts/resolve-release-inputs --channel stable --out scripts/release-inputs.lock.json
```

解析器会校验并锁定精确的 mihomo release/assets 和 GeoIP commit/digests；受 GitHub API 限流影响时可通过环境变量 `GITHUB_TOKEN` 提供凭据。生成后必须审核 lock diff，并在 PR 验证通过后才合并。不要在 release workflow 中运行解析器。

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
gofmt -l .
# 无输出表示格式正确
```

> Windows 上若 `core.autocrlf=true`，`gofmt -l` 会因 CRLF 误报未格式化，以 CI（checkout 后为 LF）为准。

### 静态检查

```sh
go vet ./...
```

### Lint（与 CI 一致）

CI 的 `lint` job 使用 golangci-lint v2（配置见 `.golangci.yml`）。本地安装并运行：

```sh
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2
golangci-lint run ./...
```

> 版本必须与 CI 一致（`ci.yml` 中钉死 `version: v2.12.2`），因为 v1 线构建于 go1.24，无法加载本项目 go1.26 的配置。

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

#### 本地 commit hook（推荐）

仓库自带 `.githooks/commit-msg` hook：提交信息缺少 `Signed-off-by` 时自动追加（取 `git config user.name` / `user.email`），忘记 `-s` 也不会被 CI 打回。安装：

```sh
git config core.hooksPath .githooks
```

安装后普通 `git commit` 即可，hook 会在消息尾部自动补签名：

```console
$ git commit -m "feat: your feature description"
commit-msg: appended Signed-off-by: Your Name <your.email@example.com>
```

如果 hook 提示 `git user.name and user.email must be set`，先配置身份：

```sh
git config user.name "Your Name"
git config user.email "you@example.com"
```

> `git commit --no-verify` 可绕过 hook，但 CI 仍会拒绝未签名的提交，不要依赖它。

## Pull Request 流程

### 分支与晋级策略

日常开发采用以下分支流：

```text
feat/*、fix/* ──PR──> dev ──晋级 PR──> main
hotfix/*（从 main） ──PR──> main
main ──同步 PR──> dev
```

普通功能和修复从 `feat/*` 或 `fix/*` 分支通过 PR 合并到 `dev`，在 dev 集成验证后再通过晋级 PR 进入 `main`。紧急修复从 `main` 创建 `hotfix/*`，通过 PR 合并到 `main`，随后必须用同步 PR 将 `main` 合并回 `dev`。普通 PR 使用 squash merge；`dev → main` 晋级和 `main → dev` 同步使用 merge commit，以保留发布历史和避免重复显示已发布提交。不得直接推送 `main` 或 `dev`。

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
   然后在 GitHub 上创建 Pull Request（目标通常为 `dev`；`hotfix/*` 目标为 `main`），按 [PR 模板](PULL_REQUEST_TEMPLATE.md) 填写。

5. **等待审核**
    - CI 检查必须通过（test / race / vet / cross-build / DCO）
    - 遵守仓库当时已配置的审核、状态检查与 bypass 规则；本文档不设定固定审核人数或 bypass 规则

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
├── scripts/install/      # 一键安装脚本（install / install-aio / install-aio-remote，.sh + .ps1）
└── go.mod                # 依赖声明
```

## 发布流程（仅维护者）

参见 [docs/RELEASE.md](../docs/RELEASE.md)。

## 问题反馈

- Bug 报告：使用 [GitHub Issues](https://github.com/mihari-proxy/mihari/issues)
- 功能请求：同样使用 Issues
- 安全漏洞：请参见 [SECURITY.md](SECURITY.md)

## 许可证

本项目采用 [GPL-3.0](../LICENSE)。通过提交 PR 贡献代码即表示你同意以相同许可证发布你的贡献（inbound = outbound）。
