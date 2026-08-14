# Mihari 二期代码质量提升设计

Date: 2026-08-14  
Status: Draft  
Branch: `docs/code-quality-phase-2`  
Baseline: `a603423` (Phase 1 merged as #73)  
Roadmap: `docs/superpowers/plans/2026-08-13-code-quality-roadmap.md` §5  
Audit: `docs/code-quality-audit-2026-08-13.md` AQ-02, AQ-03

## 1. 背景与目标

Phase 1 关闭了特权自更新摘要缺口并恢复了默认测试/coverage 信号。Phase 2 收紧不可信外部输入：panel ZIP 展开总量、条目数与目录深度，以及 panel release 元数据请求的默认 HTTP deadline。同时加入可观察、非阻断的 `govulncheck` CI job，并记录 linter 观察结果，但不批量启用高噪声规则。

## 2. 范围与非目标

### 2.1 范围

1. AQ-02：`archive.ExtractZip` 在既有单文件/压缩包大小、路径穿越和 symlink 保护之外，增加总展开字节、最大 entry 数和目录深度上限；声明大小与实际写入量都要检查；超限时清理 destDir。
2. AQ-03：`release.Client` 在未注入 HTTP client 时使用带 2 分钟 Timeout 的私有 client，并继续用 `NewRequestWithContext` 传播调用方 deadline。
3. 确定性测试覆盖：多文件总量超限、伪造 UncompressedSize64、条目洪泛、过深路径、阻塞 metadata handler、默认 timeout。
4. CI 增加非 required 的 `govulncheck` 观察 job。
5. 记录 `bodyclose`/`noctx`/`errorlint` 观察命中；本阶段不把它们写入 required `.golangci.yml`（观察已显示 noctx 约 60 条、errorlint 8、bodyclose 6，批量启用会强迫无关重构）。

### 2.2 非目标

- 不更换 panel ZIP 格式或新增第三方归档库。
- 不统一全仓 HTTP client。
- 不因 linter 观察结果批量改测试或 Web gateway。
- 默认单元测试不访问公网或真实 panel 上游。
- 不改公开 CLI/JSON/`/v1`/settings schema/持久化/`go.mod`。

## 3. 不可破坏约束

- daemon 仍是持久化状态与 mihomo 生命周期的唯一写入者。
- panel 安装仍走既有 staging → ExtractZip → promote；失败不留下可服务的 candidate。
- 既有路径穿越、绝对路径、Windows drive、symlink、`index.html` 要求和单文件 64 MiB 限制不得退化。
- 错误文本不得包含 URL 或凭据。
- 发布构建保持 `CGO_ENABLED=0`。

## 4. AQ-02：panel archive 总量与结构上限

### 4.1 当前行为

`internal/panel/archive/zip.go` 限制**单文件** 64 MiB，并拒绝不安全路径和 symlink。常量 `MaxZipSize = 128 MiB` 存在于 archive 包，但 `ExtractZip` 并不检查压缩包文件大小；128 MiB 上限实际由 `panel.Service.download` 的 `defaultMaxAssetBytes` 在下载阶段执行。遍历 entry 时不累计总展开字节、不限制 entry 数、不限制深度。`ExtractZip` 仅在缺少 `index.html` 时 `RemoveAll(destDir)`。`internal/panel/install.go` 在 ExtractZip 失败后也会 `RemoveAll(stagingDir)`。

本阶段不在 `ExtractZip` 再加一遍压缩包文件 `Stat` 上限；下载路径已有 128 MiB，本地 `InstallFromZip` 的调用方须把 `destDir` 当作独占 extract root。

### 4.2 常量

保留：

- `MaxZipSize = 128 << 20`
- `MaxExtractedFileSize = 64 << 20`

新增：

- `MaxTotalExtractedBytes = 256 << 20`
- `MaxArchiveEntries = 4096`（每个 zip header，含目录）
- `MaxArchiveDepth = 16`（与 `SafeName`/`resolveTarget` 相同：`\`→`/`，`Clean`，`ToSlash`，跳过空与 `.` 后的组件数）

### 4.3 检查顺序

`zip.OpenReader` 之后、`MkdirAll` 之前：若 `len(reader.File) > limits.maxEntries` → `"panel archive has too many entries"`（中央目录已全部解析，此检查避免再创建 destDir）。

对每个 `reader.File`，在打开内容之前：

1. 既有 `SafeName` / symlink / `resolveTarget`。
2. 计算 depth（斜杠归一化路径）；若 `depth > limits.maxDepth` → `dataFailure("panel archive path is too deep")`。
3. 目录：`MkdirAll` 后继续（目录不计入实际写入字节）。
4. 文件：若 `UncompressedSize64 > limits.maxFile` 保持既有 `"panel archive file is too large"`。
5. `declaredTotal += UncompressedSize64`；若 `declaredTotal > limits.maxTotal` → `dataFailure("panel archive is too large")`。
6. `extractFile` 改为返回 `(int64, error)`：用 `LimitReader(limits.maxFile+1)` 复制，返回 **copy 的 written**，不得用 header 冒充 written。
7. `actualTotal += written`；若 `actualTotal > limits.maxTotal` → `dataFailure("panel archive is too large")`。

伪造 metadata（声明很小或 0、实际很大）由 per-file LimitReader 与 `actualTotal` 捕获，不能只靠 `UncompressedSize64`。

### 4.4 失败清理

`destDir` 是调用方拥有的独占 extract root。`ExtractZip` 在成功 `MkdirAll(destDir)` 之后的任何错误路径调用 `os.RemoveAll(destDir)`，然后返回原错误。失败测试断言 `os.IsNotExist(destDir)`。调用方已有的 staging `RemoveAll` 保持不变。

公开签名不变：`func ExtractZip(archivePath, destDir string) error`。

私有入口（禁止包级可变 hook）：

```go
type extractLimits struct {
	maxFile, maxTotal uint64
	maxEntries, maxDepth int
}

var defaultExtractLimits = extractLimits{
	maxFile: MaxExtractedFileSize, maxTotal: MaxTotalExtractedBytes,
	maxEntries: MaxArchiveEntries, maxDepth: MaxArchiveDepth,
}

func ExtractZip(path, dest string) error {
	return extractZipWithLimits(path, dest, defaultExtractLimits)
}
```

`defaultExtractLimits` 是不可变值；测试不得改包级变量。需要更小总量的测试直接调用 `extractZipWithLimits`。条目洪泛优先用真实 `MaxArchiveEntries+1` 个极小 header；若过慢才对该用例使用更小 `maxEntries` 的 limits 值（仍走同一函数，不是全局 var）。

### 4.5 测试

合法成功路径与既有拒绝用例通过公共 `ExtractZip` + `t.TempDir`。`archive/zip.Writer.Create/CreateHeader` 会在 Close 时用实际字节覆盖 `UncompressedSize64`，因此不能用普通 Writer 伪造声明大小。需要伪造 header 的测试用同包 helper：`zip.Writer.CreateRaw` 或在 Writer.Close 后改写 central-directory 的 uncompressed-size 字段。

- 既有 nested index / traversal / absolute / missing index / symlink 仍通过公共 `ExtractZip`。
- `TestExtractZipRejectsTooManyEntries`：优先 `ExtractZip` + `MaxArchiveEntries+1` 个极小 entry；失败则 `os.IsNotExist(dest)`。
- `TestExtractZipRejectsDeclaredTotalTooLarge`：伪造偏大 `UncompressedSize64`、实际 payload 很小；应在大量写入前以 `"panel archive is too large"` 失败。
- `TestExtractZipRejectsActualTotalTooLarge`：伪造偏小或 0 的 `UncompressedSize64`、实际 stored payload 超过测试 `maxTotal`；必须调用 `extractZipWithLimits`（不是改包级 var）；断言失败来自 `actualTotal` 或 per-file LimitReader，而不是声明之和。
- `TestExtractZipRejectsPathTooDeep`：17 层 `a/a/.../index.html`。
- 每个新失败用例断言 `os.IsNotExist(destDir)`。
- 合法 `dist/index.html` + 若干资产仍成功。

## 5. AQ-03：release client 默认超时

### 5.1 当前行为

`Client.httpClient()` 在 `HTTPClient == nil` 时返回 `http.DefaultClient`。生产 `zashboard.New(nil, "")` / `metacubexd.New(nil, "")` 走这条路径。`getJSON` 已用 `NewRequestWithContext`，但无 client timeout 时，未设 deadline 的 context 会一直等。

### 5.2 设计

```go
const defaultReleaseTimeout = 2 * time.Minute

func defaultHTTPClient() *http.Client {
	return &http.Client{Timeout: defaultReleaseTimeout}
}

func (c Client) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return defaultHTTPClient()
}
```

注入的 `httptest` client 不受影响。更短的 context deadline 仍优先。

签名不变。错误仍映射为 `CodeNetworkFailure` / `CodeDataFailure`，消息不含 URL。

### 5.3 测试

- `TestNilClientIsNotDefaultClient`：`Client{}.httpClient() != http.DefaultClient` 且 `Timeout == 2*time.Minute`。禁止只测 helper 而让 `httpClient()` 仍返回 DefaultClient。禁止修改 `http.DefaultClient.Timeout`。
- `TestClientLatestReleaseUsesNilClientPath`：`Client{HTTPClient: nil, APIBase: server.URL}` + 短 context 调用 `LatestRelease`，证明 nil-client 生产路径被执行。
- `TestClientLatestReleaseHonorsContextDeadline`：handler **阻塞在 `<-r.Context().Done()`**（禁止 `select{}`，以免 `Server.Close` 死锁）；短 deadline 返回 `CodeNetworkFailure`。
- 既有 `LatestRelease` / `BranchTip` 成功测试保持注入 `server.Client()`。

## 6. govulncheck 观察 job

在 `.github/workflows/ci.yml` 增加 job `govulncheck`：

- `runs-on: ubuntu-latest`
- checkout + setup-go（go-version-file）
- `go run golang.org/x/vuln/cmd/govulncheck@latest ./...`（不修改 `go.mod`）
- **job 级** `continue-on-error: true`；不加入 `test` fan-in 的 `needs`
- 先打印 `govulncheck -version`（或等价版本输出）
- 根据退出码向 `$GITHUB_STEP_SUMMARY` 写一行 `RESULT=clean|vulns|unavailable|tool_error` 并附 stdout

观察模式：发现漏洞、数据库不可用或工具失败都不得把 required `test`/`lint` 打红。

## 7. Linter 观察（不启用）

本机对当前树启用 `bodyclose`/`noctx`/`errorlint` 的 dry-run 约 74 issues（noctx 60、errorlint 8、bodyclose 6），多数在测试里的 `http.NewRequest` / `httptest.NewRequest`。`gosec` 不在本仓 golangci v2 默认插件集中作一次干净 dry-run；记录为“未启用 / 不进入门禁”。审计与 roadmap 更新包含四行评估表（规则、命中、真阳性判断、豁免、是否门禁），结论均为不启用。不改 `.golangci.yml` required 集合。

## 8. Key Decisions

| 决策 | 理由 |
|---|---|
| 总量 256 MiB / 4096 entries / 深度 16 | 覆盖真实 panel 资产，同时挡住 zip bomb；单文件与压缩包上限保持不变 |
| 声明与实际双累计 | 审计明确要求伪造 size 不能逃逸 |
| ExtractZip 失败即 RemoveAll destDir | 避免部分展开残留；与 install staging 清理一致 |
| 私有 `extractZipWithLimits` + 不可变 default 值 | 避免包级可变 hook；公共 `ExtractZip` 只传默认 limits |
| 默认 2 分钟私有 client | 与 panel 下载、self-update 一致，不用 DefaultClient |
| govulncheck 观察且不进 fan-in | 供应链可见，但不把 DB/网络抖动变成 PR 阻断 |
| 不启用 noctx/bodyclose/errorlint | dry-run 噪声高，违反 roadmap“不因工具告警批量重构” |

## 9. Alternatives

1. **只信 UncompressedSize64**：实现更简单，但伪造 header 可绕过。拒绝。
2. **要求调用方必须注入 HTTP client**：把安全边界推给装配层，当前 `New(nil,"")` 会漏。拒绝。
3. **把 govulncheck 设为 required**：在无 pin 数据库、无豁免流程时会误伤。本阶段观察即可。

## 10. Security

威胁：恶意或被替换的 panel ZIP、悬挂的 GitHub metadata 连接。缓解：总量/条目/深度、实际写入校验、失败清理、client timeout + context。不声称能抵抗被攻破的发布账户同时替换 ZIP 与上游。

## 11. Testing matrix

| 行为 | 入口 | 断言 |
|---|---|---|
| 总量声明超限 | `ExtractZip` 或 limits + 伪造偏大 header | `CodeDataFailure`，`IsNotExist(dest)` |
| 总量实际超限 | `extractZipWithLimits` + 伪造偏小 header | 失败来自 actual/LimitReader |
| 条目过多 | `ExtractZip` | `"too many entries"`，`IsNotExist(dest)` |
| 路径过深 | `ExtractZip` | `"path is too deep"` |
| 路径穿越/symlink | 既有测试 | 不退化 |
| nil client 不是 DefaultClient | `Client{}.httpClient()` | `!= DefaultClient` 且 Timeout=2m |
| nil client 走生产路径 | `LatestRelease` HTTPClient=nil | 成功或按短 ctx 网络失败 |
| metadata 阻塞 | handler 等 `ctx.Done` | `CodeNetworkFailure` |
| govulncheck | workflow YAML | job 级 continue-on-error，不在 test.needs，有 RESULT 行 |

## 12. Rollout

一个 PR：`docs/code-quality-phase-2` → `main`。本目标授权 commit、push、PR 和 merge。回滚即 revert 该 PR。

### PR Plan

1. **PR：fix: 收紧 panel 归档展开与 release 超时**
   - Files: `internal/panel/archive/*`, `internal/panel/release/*`, `.github/workflows/ci.yml`, 审计/roadmap/本设计与实施计划
   - 无依赖 PR

## 13. Open Questions

无。常量、错误文案、观察型 CI 与 linter 策略已由 roadmap 与本目标决定。
