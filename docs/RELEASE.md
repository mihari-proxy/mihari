# 版本发布流程

本文档说明如何发布新版本。

## 工作流触发机制

| 工作流 | 触发条件 | 运行内容 |
|--------|----------|----------|
| **CI** | 推送到 `main`、`dev`、`master` 分支<br>Pull Request | ✅ 代码格式检查 (`gofmt`)<br>✅ 静态分析 (`go vet`)<br>✅ 单元测试 (`go test ./...` + `-race`)<br>✅ 交叉编译验证（6 平台） |
| **Release** | 推送 `v*` 标签<br>从 `main` 执行 `workflow_dispatch`（必填 `version` + `commit_sha`） | ✅ 版本闸门（拒绝 `-` 预发布后缀）<br>✅ 6 平台构建 + 版本号注入<br>✅ 打包 6 个 all-in-one 整合包<br>✅ 生成 SHA256 校验和（全量 + aio 专用）<br>✅ 创建 GitHub Release<br>✅ `ALIST_URL` 存在时上传整合包到 AList 网盘 |
| **Retract** | 从 `main` 执行 `workflow_dispatch`（必填 `version` + `confirm`） | ✅ 彻底撤回致命错误版本（GitHub release + AList 目录 + index 重建） |
| **DCO** | 推送到任何分支<br>Pull Request | ✅ 校验每个提交的 `Signed-off-by` 签名 |

各发布 workflow 的触发器、通道和写入范围如下：

| Workflow | 触发方式 | 通道 | 允许写入 |
|----------|----------|------|----------|
| `release.yml` | `v*` tag push；从 `main` 手动 dispatch（稳定 `version` + `commit_sha`） | Stable | GitHub stable Release；`/mihari-release/mihari` 及其 stable `index.txt` |
| `release-dev.yml` | 代码已准备，远程 dev/试发需另行授权 | Dev | 获授权并从受保护 `dev` dispatch 后，仅写 GitHub dev tag、prerelease 与 14 个 assets；不写 AList |
| `retract.yml` | 从 `main` 手动 dispatch（稳定 `version` + `confirm`） | Stable | 删除 stable Release 及其 assets、保留 canonical stable tag，并删除 `/mihari-release/mihari/<version>/`；必要时重建 stable `index.txt` |

Batch A 中，dev 发布代码已准备，远程 dev/试发需授权。P2 AList 发布与撤回 workflow 尚不可用，因此没有 dev AList 发布、撤回或下载命令；稳定 AList、stable `index.txt` 与 `/releases/latest` 不受 dev 准备代码影响。

> **关键点**：推送标签时只触发 **Release** 工作流，不会重复运行 CI 测试。`Release` 与 `Retract` 仅手动或标签触发。

## Stable 与 Dev 发布通道

| 通道 | 来源与版本 | GitHub Release | AList 根目录 | 稳定入口影响 |
|------|------------|----------------|--------------|--------------|
| Stable | `main` 上的 `vX.Y.Z` | 正式 release，参与 `/releases/latest` | `/mihari-release/mihari` | 更新稳定 `index.txt`，供稳定安装器使用 |
| Dev | `dev` 上的 `vX.Y.Z-dev.N` | 代码已准备；另行授权后创建 GitHub tag、prerelease 与 14 个 assets | P2 尚不可用 | 不写稳定 `index.txt`、版本目录或 `/releases/latest` |

Dev 发布实现固定 `refs/heads/dev` 来源并校验 canonical `vX.Y.Z-dev.N` 版本身份；已有同名 tag 时必须验证其 commit，身份不符即拒绝且不覆盖。Batch A 只完成代码准备，远程 dev/试发需授权。P2 AList 发布与撤回 workflow 尚不可用；在其交付并另获授权前，不提供 dev AList 操作入口。

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

1. 版本闸门校验（build / release 各一层，拒绝 `-` 预发布后缀）；
2. 交叉编译 linux / darwin / windows（各 amd64 + arm64），注入版本号（`-X .../buildinfo.Version=<version>`）；
3. 打包 6 个 all-in-one 整合包（mihari + mihomo 核心 + GeoIP）；
4. 生成 SHA256 校验和（全量 `SHA256SUMS.txt` + aio 专用 `AIO_SHA256SUMS.txt`）；
5. 创建 GitHub Release 并上传全部产物；
6. `ALIST_URL` 存在时进入 AList mutation，上传整合包到网盘版本目录、更新 `index.txt`、追加离线安装命令到 release notes；只有 `ALIST_URL` 缺失时才走 GitHub-only skip，不阻塞 GitHub 发布。URL 已存在但 `ALIST_USERNAME` 或 `ALIST_PASSWORD` 任一缺失时，客户端必须 fail closed，workflow 失败且不得静默跳过。

## 版本命名规范

遵循 [语义化版本](https://semver.org/lang/zh-CN/)：

- **主版本号 (MAJOR)**：不兼容的协议/API 修改
- **次版本号 (MINOR)**：向后兼容的功能新增
- **修订号 (PATCH)**：向后兼容的问题修正

Stable 版本必须匹配 `^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`；dev 版本必须匹配 `^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)-dev\.(0|[1-9][0-9]*)$`。每个数字段只能是单独的 `0` 或非零数字开头的整数，因此 `v01.2.3`、`v1.02.3`、`v1.2.03` 和 `v1.2.3-dev.01` 均会被拒绝。

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
mihari-all-in-one-linux-amd64.tar.gz
mihari-all-in-one-linux-arm64.tar.gz
mihari-all-in-one-darwin-amd64.tar.gz
mihari-all-in-one-darwin-arm64.tar.gz
mihari-all-in-one-windows-amd64.zip
mihari-all-in-one-windows-arm64.zip
SHA256SUMS.txt
AIO_SHA256SUMS.txt
```

命名规则：`mihari-<goos>-<goarch>[.exe]`（**不带版本号**）。`internal/update` 的 `SelectSelfAsset` 和 `install.sh` / `install.ps1` 都依赖这个固定命名，同时 GitHub 的 `/releases/latest/download/` 路径也因此可用。

`mihari-all-in-one-*` 是 all-in-one 整合包（mihari 二进制 + mihomo 核心 + GeoIP×2），供墙内用户离线安装，详见 [分发方案](distribution.md)。`SHA256SUMS.txt` 覆盖全部产物，`AIO_SHA256SUMS.txt` 仅覆盖 6 个整合包（AList 版本目录内同名）。

## 校验下载

下载后验证完整性：

```bash
# Linux/macOS
sha256sum -c SHA256SUMS.txt

# Windows (PowerShell)
Get-FileHash mihari-windows-amd64.exe -Algorithm SHA256
```

## 手动触发发布

如需手动触发稳定 `release` workflow，在 GitHub Actions 页面必须选择 `main` 分支/ref，并填写：

- `version`：如 `v0.3.0`，必须匹配上述 canonical stable 格式，不接受前导零或预发布后缀；
- `commit_sha`：必填的 **40 位十六进制**提交 SHA。workflow 会将该 SHA 与 checkout 后的源代码核对，身份不一致时 fail closed。

标签触发的稳定发布从标签（lightweight 或 annotated）最终解析/peel 到的 commit 推导提交身份，而不是信任可变分支名。稳定发布和 AList 写入均须通过 CI、版本闸门及维护者审批；审批前不得执行生产分发写操作。

Workflow 在发布前后都会重新读取并 peel 当前 stable tag，但这种前后复核并非原子操作，只能检测两个检查点观察到的身份变化，不能阻止检查间隔内的 tag 更新。当前远端尚未配置 stable tag-target ruleset，本批也未获授权修改该设置；真实 stable 发布前，必须另行取得授权，配置并回读 stable tag-target ruleset，禁止更新和删除 canonical stable tag。

> **为什么必填 version**：`workflow_dispatch` 从分支触发时 `GITHUB_REF_NAME` 是分支名而非版本号。显式输入统一全链路取值（build 注入的版本号、Release 的 `tag_name`、AList 版本目录名），避免二进制版本号被污染。版本闸门在 build / release 两个 job 各校验一次。

### Dev 手动发布

Batch A 只完成 dev 发布代码准备，远程 dev/试发需另行授权。实现会校验 `refs/heads/dev` 来源和上述 canonical `vX.Y.Z-dev.N` 格式（各数字段不允许前导零）；获授权并从受保护 `dev` dispatch 后，只创建 GitHub dev tag、prerelease 并上传精确 14 个 assets，不写 AList。在获得单独授权前，不应运行远程 dev 发布。

Dev workflow 会在 mutation 前和最终验收时重新读取并 peel 当前 dev tag，但 dev 前后复核并非原子操作，只能发现两个检查点已经可见的身份变化。当前远端尚未配置 dev tag-target ruleset，也未获授权修改该设置；真实 dev 发布前，必须另行授权，配置并回读 dev tag-target ruleset，禁止更新和删除 canonical dev tag。

Dev 发布还要求仓库已经存在至少一个合法的 stable GitHub Release，且 `/releases/latest` 能返回非 draft、非 prerelease 的稳定版本；缺少该前置条件时 workflow 会在任何 dev mutation 前 fail closed。P2 AList 发布与撤回 workflow 尚不可用，因此这里不提供 dev AList 命令或路径。

## 回滚发布（致命错误撤回）

发现致命错误需要撤回某版本时，使用 `retract` workflow 永久移除其 GitHub Release、assets 与 AList 分发数据，但保留 canonical stable tag：

1. 在 GitHub 仓库 Actions 页面选择 `main` 分支/ref，再运行 `retract` workflow；
2. 填写 `version`（如 `v0.3.0`）并勾选 `confirm` 双保险；
3. workflow 自动：若撤回当前 latest，先计算并写入现存最高完整版本的替代 `index.txt`（没有其他完整版本则写空），并回读验证成功；随后永久删除 AList 版本目录；最后永久删除 GitHub Release 及其 assets，但保留 canonical stable tag。目录删除失败时，已切换的 index 保持不回退，重跑会删除不再被 index 引用的遗留目录。

Stable index writer 在事务前确认权威实时内容仍等于调用方观察到的原值，然后只执行一次 PUT 并做权威 readback：读到目标内容即成功；仍读到原值则报告 index unchanged，必须从头重跑完整 release/retract workflow；读到第三方值或无法确定回读结果时立即停止并保留远端现场，转入人工恢复，不自动 rollback。仅当 AList mutation 失败或在 mutation 期间取消时，workflow 才上传保留 3 天的 `stable-index-backup-<run_id>-<attempt>` artifact，其中同时包含 `index.txt` 与 `metadata.json`；AList mutation 已成功后发生下游步骤失败时不会上传该 artifact，因为远端 index 已验证提交成功，无需恢复旧 index。人工恢复必须按 metadata 的 `existed`、`channel`、`path`、`sha256` 校验并决定删除原本不存在的对象，或恢复原本为空/非空的 index 内容。

AList 不提供 compare-and-swap（CAS），因此事务前检查与单次 PUT 之间仍存在竞态，不能声称彻底原子。Stable release 与 retract workflow 共用 channel concurrency，避免这两类 Actions writer 并行；执行人工 `regenerate-index` 或 artifact 恢复前，必须确认相关 workflow 均未运行，并在整个检查、写入和回读期间禁止其他人工或自动 writer。

> **仅移除分发渠道，已安装用户不可回收**——canonical stable tag 继续受 ruleset 保护，不能同版本重切；必须快速发布更高版本号的修复版（`vN+1 > vN`），由自更新覆盖坏版本。详见 [分发方案 · 版本撤回](distribution.md#五版本撤回致命错误)。

> 旧的手动删标签 + 网页删 Release 方式仅删 GitHub 侧、不重建 AList index，已由 `retract` workflow 取代。

P2 AList 发布与撤回 workflow 尚不可用；dev 撤回尚无可执行入口。后续设计仍要求 dev 操作不得触碰稳定目录、稳定 `index.txt` 或 `/releases/latest`，但本阶段不执行任何远程 dev 撤回。

## CI/CD 检查项

每次发布前确保：

- [ ] 所有测试通过 (`go test -race ./...`)
- [ ] 代码格式正确 (`gofmt -l cmd internal`)
- [ ] 静态分析通过 (`go vet ./...`)
- [ ] [CHANGELOG.md](../CHANGELOG.md) 已更新
- [ ] 标签版本号与 CHANGELOG.md 一致
