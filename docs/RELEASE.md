# 版本发布流程

本文档说明如何发布新版本。

## 工作流触发机制

| 工作流 | 触发条件 | 运行内容 |
|--------|----------|----------|
| **CI** | 推送到 `main`、`dev`、`master` 分支<br>Pull Request | ✅ 代码格式检查 (`gofmt`)<br>✅ 静态分析 (`go vet`)<br>✅ 单元测试 (`go test ./...` + `-race`)<br>✅ 交叉编译验证（6 平台） |
| **Release** | 主路径：从 `main` 执行 `workflow_dispatch`（必填 `version` + `commit_sha`）<br>兼容入口：推送 `v*` 标签 | ✅ 版本闸门（拒绝 `-` 预发布后缀）<br>✅ 6 平台构建 + 版本号注入<br>✅ 打包 6 个 all-in-one 整合包<br>✅ 生成 SHA256 校验和（全量 + aio 专用）<br>✅ 创建 GitHub Release<br>✅ `ALIST_URL` 存在时上传整合包到 AList 网盘 |
| **Retract** | 从 `main` 执行 `workflow_dispatch`（必填 `version` + `confirm`） | ✅ 彻底撤回致命错误版本（GitHub release + AList 目录 + index 重建） |
| **DCO** | 推送到任何分支<br>Pull Request | ✅ 校验每个提交的 `Signed-off-by` 签名 |

各发布 workflow 的触发器、通道和写入范围如下：

| Workflow | 触发方式 | 通道 | 允许写入 |
|----------|----------|------|----------|
| `release.yml` | 主路径：从 `main` 手动 dispatch（稳定 `version` + 精确 `commit_sha`）；兼容 `v*` tag push | Stable | GitHub stable Release；`/mihari-release/mihari` 及其 stable `index.txt` |
| `release-dev.yml` | 从受保护的 `dev` 手动 dispatch（dev `version`） | Dev | 仅写 GitHub dev tag、prerelease 与精确 14 个 assets；不写 AList |
| `retract.yml` | 从 `main` 手动 dispatch（稳定 `version` + `confirm`） | Stable | 删除 stable Release 及其 assets、保留 canonical stable tag，并删除 `/mihari-release/mihari/<version>/`；必要时重建 stable `index.txt` |

GitHub dev prerelease 流程已经实际发布并验收 `v0.9.0-dev.2`（精确 14 个 assets，且不写 AList）。dev AList 发布与 dev retract workflow 仍不可用，因此没有 dev AList 版本目录、下载命令或撤回入口；稳定 AList、stable `index.txt` 与 `/releases/latest` 不受 dev 发布影响。

远端 active tag ruleset 覆盖 `refs/tags/v*`，禁止删除、更新和 non-fast-forward；canonical stable/dev tag 一旦创建不得改写。`v*` tag push 保留为兼容触发入口，但当前稳定发版操作使用 `main` 上的 `workflow_dispatch`。

## Stable 与 Dev 发布通道

| 通道 | 来源与版本 | GitHub Release | AList 根目录 | 稳定入口影响 |
|------|------------|----------------|--------------|--------------|
| Stable | `dev → main` 晋级 PR 合并后的精确 40 位 `main` commit SHA；`vX.Y.Z` | 正式 release，参与 `/releases/latest` | `/mihari-release/mihari` | 更新稳定 `index.txt`，供稳定安装器使用 |
| Dev | `dev` 上的 `vX.Y.Z-dev.N` | GitHub tag、prerelease 与 14 个 assets；`v0.9.0-dev.2` 已通过实际验收 | 尚不可用 | 不写稳定 `index.txt`、版本目录或 `/releases/latest` |

Dev 发布固定 `refs/heads/dev` 来源并校验 canonical `vX.Y.Z-dev.N` 版本身份；已有同名 tag 时必须验证其 commit，身份不符即拒绝且不覆盖。dev AList 发布与 dev retract workflow 尚不可用，不提供 dev AList 操作入口。

## 发版流程

### 1. 通过晋级 PR 合并到 main

将已经通过 dev 集成验证的 release-prep 变更通过 `dev → main` 晋级 PR 合并；不要直接推送或提交 `main`。

```bash
git checkout main
git pull origin main
```

### 2. 验证 CI 通过

推送代码到 `main` 后，CI 工作流会自动运行。在 GitHub Actions 页面确认所有检查通过后再继续。

### 3. 记录精确 main commit

确认 CHANGELOG 与 release input lock 已随晋级 PR 进入 `main`，记录远端 `main` 当前精确的 40 位小写 commit SHA：

```bash
git rev-parse origin/main
```

### 4. 从 GitHub Actions 触发 stable release

在 GitHub Actions 选择 `release` workflow，ref 选择 `main`，执行 `workflow_dispatch`：

- `version`：canonical stable 版本，例如 `v0.9.0`；
- `commit_sha`：上一步记录的、与当前 checkout 后 `main` 完全相等的 40 位 SHA。

`release.yml` 会自行创建或验证 canonical stable tag。不要把本地创建并推送 tag 作为当前稳定发版操作。

### 5. 自动发布与验收

手动 dispatch 后，GitHub Actions Release 工作流会自动：

1. 版本闸门校验（build / release 各一层，拒绝 `-` 预发布后缀）；
2. 使用 Go 1.26.5 交叉编译 linux / darwin / windows（各 amd64 + arm64），以 `-buildvcs=false -trimpath` 构建并注入版本号（`-X .../buildinfo.Version=<version>`）；
3. 只从仓库内已审核的 `scripts/release-inputs.lock.json` 读取精确 mihomo 与 GeoIP 输入，打包 6 个 all-in-one 整合包；
4. 生成 SHA256 校验和（全量 `SHA256SUMS.txt` + aio 专用 `AIO_SHA256SUMS.txt`）；
5. 创建 GitHub Release 并上传全部产物；
6. `ALIST_URL` 存在时进入 AList mutation，上传整合包到网盘版本目录、更新 `index.txt`、追加离线安装命令到 release notes；只有 `ALIST_URL` 缺失时才走 GitHub-only skip，不阻塞 GitHub 发布。URL 已存在但 `ALIST_USERNAME` 或 `ALIST_PASSWORD` 任一缺失时，客户端必须 fail closed，workflow 失败且不得静默跳过。

## 可复现构建输入

稳定和 dev 发布 workflow 都通过 `go-version-file: go.mod` 使用其中钉死的 Go 1.26.5 toolchain。Mihari 二进制同时使用 `-buildvcs=false -trimpath`：`-buildvcs=false` 防止同一 commit 在 tag 创建前后产生不同的 Go VCS/module 元数据，`-trimpath` 去除本机构建路径；用户可见版本继续由 `buildinfo.Version` 的 ldflags 注入。

all-in-one 的外部输入固定在仓库内的 `scripts/release-inputs.lock.json`。lock 记录精确的 mihomo release、六个平台 asset ID/URL/大小/SHA-256，以及 GeoIP commit、不可变 URL 和 SHA-256。构建阶段只读取 lock，**不会**解析 mihomo latest release 或 GeoIP 的可变 `release` ref，也不会在 workflow 中自动更新 lock。

维护者只应在独立的 release-prep PR 中更新它：

```bash
go run ./scripts/resolve-release-inputs --channel stable --out scripts/release-inputs.lock.json
```

若需要提高 GitHub API 限额，可设置环境变量 `GITHUB_TOKEN` 后运行同一命令。解析器会下载并验证候选输入，再以原子替换写入 lock；命令失败时旧 lock 保持不变。提交前必须人工审核 lock diff，确认 repository、channel、release/tag、六个平台资产、GeoIP commit 与摘要均符合本次发版意图。release workflow 绝不能调用该解析器。

workflow 调用 bundler 时必须显式提供 lock：

```bash
go run ./scripts/build-all-in-one \
  --lock scripts/release-inputs.lock.json \
  --mihari-dir dist --out bundles --platforms "linux/amd64,linux/arm64,darwin/amd64,darwin/arm64,windows/amd64,windows/arm64"
```

`--out` 必须是专用的受管 bundle 目录；允许使用当前工作目录下的专用子目录（例如 `./bundles`），但不得等于或包含当前工作目录，也不得等于或包含 lock 文件。输出目录与 `--mihari-dir`、`--scripts-dir` 两个输入目录必须双向不重叠。bundler 会先在临时目录完成构建与校验，再提交整个输出；不要把其他文件放进该目录。

在 commit、版本号、Go toolchain、lock 和仓库内安装脚本均相同时，重跑应产生逐字节一致的 6 个原始二进制、6 个 AIO 包和 2 个 checksum 文件。发布仍固定为下面列出的 **14 个 assets**；lock 只是构建输入，不上传到 GitHub Release 或 AList。

相同 SHA 重建的逐字节稳定性由固定 toolchain、构建参数与 lock 保证。仅 `release-dev.yml` 对已有同版本 Release 执行 existing-asset preflight：若报告 `existing release asset checksum conflicts with this build`，表示已发布 dev asset 与本次构建并非逐字节相同；dev workflow 会在 tag/asset mutation 前 fail closed，不得删除、覆盖或把冲突当成成功重试。先核对源 commit、版本、toolchain 与 lock；若现存 dev tag/release 来自旧的非确定性构建，保留现场并使用更高的 canonical dev 版本重新发布。

`release.yml` 当前不提供同等的 existing-asset preflight；stable 发布与重试仍遵循现有 stable Action 的创建/上传、AList 事务与最终校验契约。不要从 dev 的 fail-closed preflight 推断 stable 具有相同覆盖保护。

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

以上清单是固定的 14-asset 契约。`scripts/release-inputs.lock.json` 不属于发布产物，也不会上传到 GitHub Release 或 AList。

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

这是当前稳定发版的主路径：release-prep 先经 `dev → main` 晋级 PR，随后以 `main` ref、canonical `version` 和精确 40 位 `main` `commit_sha` dispatch。workflow 会核对 checkout 后的 `main` 与输入 SHA，身份不一致即 fail closed。稳定发布和 AList 写入均须通过 CI、版本闸门及维护者审批；审批前不得执行生产分发写操作。

兼容入口仍接受 `v*` tag push：它会将 lightweight 或 annotated tag 最终 peel 到 commit，并要求该 commit 可从可信 `main` 到达。它用于兼容既有自动化，不作为当前人工 runbook。无论入口为何，workflow 都在发布前后重新读取并 peel stable tag；该复核不是原子操作，因此还依赖已 active、覆盖 `refs/tags/v*` 的 tag ruleset 禁止删除、更新和 non-fast-forward。

> **为什么必填 version**：`workflow_dispatch` 从分支触发时 `GITHUB_REF_NAME` 是分支名而非版本号。显式输入统一全链路取值（build 注入的版本号、Release 的 `tag_name`、AList 版本目录名），避免二进制版本号被污染。版本闸门在 build / release 两个 job 各校验一次。

### Dev 手动发布

从 GitHub Actions 选择 `release-dev` workflow 和受保护的 `dev` ref，填写符合 canonical `vX.Y.Z-dev.N` 格式的版本（各数字段不允许前导零）。workflow 只创建或复用 GitHub dev tag、prerelease 并上传精确 14 个 assets，不写 AList。`v0.9.0-dev.2` 已按该路径发布并完成公开资产验收。

Dev workflow 会在 mutation 前和最终验收时重新读取并 peel 当前 dev tag；前后复核不是原子操作，因此还依赖已 active、覆盖 `refs/tags/v*` 的 tag ruleset 禁止删除、更新和 non-fast-forward。对已有同版本 Release 的 checksum 冲突会在 tag mutation 前 fail closed。

Dev 发布还要求仓库已经存在至少一个合法的 stable GitHub Release，且 `/releases/latest` 能返回非 draft、非 prerelease 的稳定版本；缺少该前置条件时 workflow 会在任何 dev mutation 前 fail closed。dev AList 发布与 dev retract workflow 尚不可用，因此这里不提供 dev AList 命令、版本目录或撤回入口。

## 回滚发布（致命错误撤回）

发现致命错误需要撤回某版本时，使用 `retract` workflow 永久移除其 GitHub Release、assets 与 AList 分发数据，但保留 canonical stable tag：

1. 在 GitHub 仓库 Actions 页面选择 `main` 分支/ref，再运行 `retract` workflow；
2. 填写 `version`（如 `v0.3.0`）并勾选 `confirm` 双保险；
3. workflow 自动：若撤回当前 latest，先计算并写入现存最高完整版本的替代 `index.txt`（没有其他完整版本则写空），并回读验证成功；随后永久删除 AList 版本目录；最后永久删除 GitHub Release 及其 assets，但保留 canonical stable tag。目录删除失败时，已切换的 index 保持不回退，重跑会删除不再被 index 引用的遗留目录。

Stable index writer 在事务前确认权威实时内容仍等于调用方观察到的原值，然后只执行一次 PUT 并做权威 readback：读到目标内容即成功；仍读到原值则报告 index unchanged，必须从头重跑完整 release/retract workflow；读到第三方值或无法确定回读结果时立即停止并保留远端现场，转入人工恢复，不自动 rollback。仅当 AList mutation 失败或在 mutation 期间取消时，workflow 才上传保留 3 天的 `stable-index-backup-<run_id>-<attempt>` artifact，其中同时包含 `index.txt` 与 `metadata.json`；AList mutation 已成功后发生下游步骤失败时不会上传该 artifact，因为远端 index 已验证提交成功，无需恢复旧 index。人工恢复必须按 metadata 的 `existed`、`channel`、`path`、`sha256` 校验并决定删除原本不存在的对象，或恢复原本为空/非空的 index 内容。

AList 不提供 compare-and-swap（CAS），因此事务前检查与单次 PUT 之间仍存在竞态，不能声称彻底原子。Stable release 与 retract workflow 共用 channel concurrency，避免这两类 Actions writer 并行；执行人工 `regenerate-index` 或 artifact 恢复前，必须确认相关 workflow 均未运行，并在整个检查、写入和回读期间禁止其他人工或自动 writer。

> **仅移除分发渠道，已安装用户不可回收**——canonical stable tag 继续受 ruleset 保护，不能同版本重切；必须快速发布更高版本号的修复版（`vN+1 > vN`），由自更新覆盖坏版本。详见 [分发方案 · 版本撤回](distribution.md#五版本撤回致命错误)。

> 旧的手动删标签 + 网页删 Release 方式仅删 GitHub 侧、不重建 AList index，已由 `retract` workflow 取代。

Dev AList 发布与 dev retract workflow 尚不可用；dev 撤回没有可执行入口。任何后续 dev 分发操作仍不得触碰稳定目录、稳定 `index.txt` 或 `/releases/latest`。

## CI/CD 检查项

每次发布前确保：

- [ ] 所有测试通过 (`go test -race ./...`)
- [ ] 代码格式正确 (`gofmt -l .`)
- [ ] 静态分析通过 (`go vet ./...`)
- [ ] [CHANGELOG.md](../CHANGELOG.md) 已更新
- [ ] 标签版本号与 CHANGELOG.md 一致
- [ ] `go.mod` 仍钉死 Go 1.26.5，发布构建仍使用 `-buildvcs=false -trimpath`
- [ ] `scripts/release-inputs.lock.json` 已在 release-prep PR 中更新（如需）并审核 diff；release workflow 未动态解析 latest/ref
