# 仓库根目录整理设计（Issue #27）

- 日期：2026-08-12
- 关联 issue：#27
- 类型：chore（仓库结构整理，无运行时行为变更）

## 1. 背景

根目录聚集了 17 个顶层文件 + 6 个目录，主要问题：

- **7 个 `install*` 脚本散落根目录**（占顶层文件近一半），而 `scripts/` 已存在（`build-all-in-one`、release/retract 辅助脚本），install 脚本却未归拢。
- **本地编译产物 `mihari.exe` 物理躺在根目录**：已被 `*.gitignore` 的 `*.exe` 正确忽略、未入版本库，但 `go build` 默认输出到当前目录会持续制造散落二进制。
- **社区文档分散**：`CONTRIBUTING`/`CODE_OF_CONDUCT`/`SECURITY` 散落根目录，而 `.github/` 已是 GitHub 平台识别这些文档的标准位置。

## 2. 目标

1. install 脚本归入 `scripts/install/`，同步更新所有引用，**不破坏分发链路**。
2. 本地编译产物统一到 `bin/`，根目录不再出现散落二进制。
3. 社区文档归入 `.github/`（GitHub 自动识别）。

## 3. 设计决策

### 3.1 install 脚本归 `scripts/install/`（而非顶层 `install/`）

- `scripts/build-all-in-one` 打包的就是 `install-aio.*`，两者高耦合，同归 `scripts/` 最自然。
- `release-alist.py` 上传 `install-aio-remote.*`，同目录寻源一致。
- 集中在 `scripts/` 下，辅助脚本语义统一。

### 3.2 编译产物归 `bin/`

CI 本就用 `/tmp`（`ci.yml` 的交叉编译）和 `dist/`（`release.yml` 的产物），不污染根目录。`bin/` 主要约束**本地开发**——把 README/CONTRIBUTING/AGENTS 里的 `go build -o mihari` 示例改为 `-o bin/mihari`。`.gitignore` 加 `/bin/` 兜底（`*.exe` 已覆盖 `.exe`，但 linux/macOS 无扩展名二进制需要 `bin/` 整体忽略）。

### 3.3 社区文档归 `.github/`

GitHub 自动识别 `.github/{CONTRIBUTING,CODE_OF_CONDUCT,SECURITY}.md`。CONTRIBUTING 内部对 SECURITY 的相对引用因两者同移到 `.github/` 而无需改；指向根目录 `LICENSE`、`docs/RELEASE.md`、`.github/PULL_REQUEST_TEMPLATE.md` 的相对链接需随 CONTRIBUTING 的迁移修正一层。

## 4. 影响分析与引用更新

### 4.1 分发链路（关键，不可破坏）

- **AList 离线直链不变**：`install-aio-remote.*` 的网盘 URL 由 `release-alist.py` 上传时的**文件名**决定，文件名不变，只需同步 source 路径（`release-alist.py` 的 `upload_root_scripts` 改为从 `scripts/install/` 读）。README/release notes 里的 AList URL 全部不动。
- **bundle 内部不变**：`install-aio-remote` 解包后找 `install-aio` 用的是解包目录内的文件名，与仓库源码位置无关；bundle 内文件名仍为 `install-aio.sh`/`.ps1`。
- **raw URL 变更（已知破坏）**：在线安装命令 `raw.githubusercontent.com/.../main/install.sh` 路径变化为 `.../main/scripts/install/install.sh`，同步更新 README/distribution/ONBOARDING/脚本自身注释。这是 main raw 链接，合并后旧 URL 即失效——issue 验收明确接受此变更。

### 4.2 CI / 发版流程

- **`release.yml` 不改**：build job 用 `dist/`；bundle job 靠 `build-all-in-one` 新默认 `--scripts-dir=scripts/install`；release job 的 `release-alist.py` 调用参数 `--repo-root .` 不变（内部路径已改）。
- **`retract.yml` / `retract-alist.py` 不引用 install 脚本**，不改。
- **`docs/RELEASE.md` 不改**：`:85` 是脚本名叙述（说明这些脚本依赖 `mihari-<os>-<arch>` 命名规则），非路径。

### 4.3 代码 / 测试

- `scripts/build-all-in-one/main.go`：`--scripts-dir` 默认 `.` → `scripts/install`（flag default + `run` 空值回填）。测试用临时目录造脚本，不依赖真实路径，无需改测试。
- `scripts/test_alist_client.py`：`test_install_scripts_hardcode_public_index_url` 从 `repo_root` 改读 `scripts/install/`。

## 5. 明确不改的项

- `internal/core/install.go:337`：网络失败错误提示里的脚本名（非路径引用）。
- `CHANGELOG.md`、`docs/plans/`、`docs/superpowers/` 历史文档：历史快照不追溯（CHANGELOG 随发版由维护者统一记录）。

## 6. 验证

- `gofmt -l`（CI Format 预检，`test -z "$(gofmt -l .)"`）
- `go build ./cmd/mihari` + `./scripts/build-all-in-one`
- `go test ./scripts/build-all-in-one/`
- `pytest scripts/test_alist_client.py`
- `rg` 复查仓库内无指向根目录的 install 路径残留

## 7. 风险

raw URL 变更使已传播的旧在线安装命令（`…/main/install.sh`）失效。缓解：主推的 AList 离线直链（`install-aio-remote`）不受影响；raw URL 随 main 合并立即生效，所有文档与脚本注释已同步更新。
