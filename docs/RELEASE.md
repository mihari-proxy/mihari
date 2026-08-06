# 版本发布流程

本文档说明如何发布新版本。

## 工作流触发机制

| 工作流 | 触发条件 | 运行内容 |
|--------|----------|----------|
| **CI** | 推送到 `main` 分支<br>Pull Request | ✅ 代码格式检查 (`gofmt`)<br>✅ 静态分析 (`go vet`)<br>✅ 单元测试 (`go test ./...` + `-race`)<br>✅ 交叉编译验证（6 平台） |
| **Release** | 推送 `v*` 标签 | ✅ 构建发布产物<br>✅ 注入版本号<br>✅ 生成 SHA256 校验和<br>✅ 创建 GitHub Release |
| **DCO** | 推送到任何分支<br>Pull Request | ✅ 校验每个提交的 `Signed-off-by` 签名 |

> **关键点**：推送标签时只触发 **Release** 工作流，不会重复运行 CI 测试。

## 发版流程

### 1. 确保代码已合并到 main

```bash
git checkout main
git pull origin main
```

### 2. 验证 CI 通过

推送代码到 `main` 后，CI 工作流会自动运行。在 GitHub Actions 页面确认所有检查通过后再继续。

### 3. 更新本文档的变更日志

把本次发布的变更整理进下面的「更新日志」章节。

### 4. 创建版本标签

```bash
git tag -a v0.1.0 -m "Release v0.1.0: 初始开源发布"
git push origin v0.1.0
```

### 5. 自动发布

推送标签后，GitHub Actions Release 工作流会自动：

1. 交叉编译 linux / darwin / windows（各 amd64 + arm64）
2. 注入版本号（`-X github.com/mihari-proxy/mihari/internal/buildinfo.Version=<tag>`）
3. 生成 SHA256 校验和
4. 创建 GitHub Release 并上传所有产物

## 版本命名规范

遵循 [语义化版本](https://semver.org/lang/zh-CN/)：

- **主版本号 (MAJOR)**：不兼容的协议/API 修改
- **次版本号 (MINOR)**：向后兼容的功能新增
- **修订号 (PATCH)**：向后兼容的问题修正

示例：
- `v0.1.0` - 初始开源发布
- `v0.2.0` - 新增功能
- `v0.2.1` - Bug 修复

## 发布产物

每个版本会发布以下文件：

```
mihari-linux-amd64
mihari-linux-arm64
mihari-darwin-amd64
mihari-darwin-arm64
mihari-windows-amd64.exe
mihari-windows-arm64.exe
SHA256SUMS.txt
```

命名规则：`mihari-<goos>-<goarch>[.exe]`（**不带版本号**）。`internal/update` 的 `SelectSelfAsset` 和 `install.sh` / `install.ps1` 都依赖这个固定命名，同时 GitHub 的 `/releases/latest/download/` 路径也因此可用。

## 校验下载

下载后验证完整性：

```bash
# Linux/macOS
sha256sum -c SHA256SUMS.txt

# Windows (PowerShell)
Get-FileHash mihari-windows-amd64.exe -Algorithm SHA256
```

## 手动触发发布

如需手动触发（不推荐），可在 GitHub 仓库的 Actions 页面手动运行 `release` workflow。

## 回滚发布

如发现问题需要回滚：

```bash
# 删除远程标签
git push --delete origin v0.1.0

# 删除本地标签
git tag -d v0.1.0

# 删除 GitHub Release（需在网页操作）
```

## CI/CD 检查项

每次发布前确保：

- [ ] 所有测试通过 (`go test -race ./...`)
- [ ] 代码格式正确 (`gofmt -l cmd internal`)
- [ ] 静态分析通过 (`go vet ./...`)
- [ ] [CHANGELOG.md](../CHANGELOG.md) 已更新
- [ ] 标签版本号与 CHANGELOG.md 一致
