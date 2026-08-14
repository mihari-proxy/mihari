# Mihari 二期代码质量提升实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (or implement in-session with the same TDD + review gates).

**Goal:** 关闭 AQ-02、AQ-03，并加入非阻断的 govulncheck 观察 job。

**Architecture:** `ExtractZip` 委托不可变默认 limits 的 `extractZipWithLimits`；声明与实际双累计；`release.Client` nil client 使用 2 分钟私有 HTTP client。

**Tech Stack:** Go 1.26.5、archive/zip、httptest、GitHub Actions、govulncheck via `go run`.

**Spec:** `docs/superpowers/specs/2026-08-13-code-quality-phase-2-design.md`

## Global Constraints

- 非 `main` 分支与独立 worktree；不改 `/v1`、CLI/JSON、settings schema、`go.mod`。
- 不启用 gosec/bodyclose/noctx/errorlint。
- 不新增依赖。
- Red–Green–Refactor；每个行为 Red 必须是断言失败。
- 测试只用 `t.TempDir` / `httptest`；无公网。
- 本目标授权 commit、push、PR、merge。
- 每个 Task 独立 signed commit。

---

## Task 1：AQ-02 archive 总量/条目/深度

**Files:**
- Modify: `internal/panel/archive/zip.go`
- Modify: `internal/panel/archive/zip_test.go`

**Interfaces:**
- 公共 `ExtractZip(archivePath, destDir string) error` 不变。
- 私有 `extractLimits`、`defaultExtractLimits`（值，非可变 var hook）、`extractZipWithLimits`。
- `extractFile` 返回 `(int64, error)`，written 来自 copy。

### Step 1：写失败测试

新增：

- `TestExtractZipRejectsTooManyEntries`：`extractZipWithLimits` + `maxEntries: 2`，zip 含 3 个小文件（含 index.html）；断言 `errors.As` `CodeDataFailure`、消息 `panel archive has too many entries`、`os.IsNotExist(dest)`。
- `TestExtractZipRejectsPathTooDeep`：17 层 `a/.../index.html`；公共 `ExtractZip`；消息 `panel archive path is too deep`；`IsNotExist`。
- `TestExtractZipRejectsDeclaredTotalTooLarge`：helper 伪造 **至少 2 个文件** 各自 `UncompressedSize64 <= MaxExtractedFileSize` 但之和 > `maxTotal`（用 `CreateRaw` 或 CD patch）；断言 `"panel archive is too large"`、`IsNotExist`。
- `TestExtractZipRejectsActualTotalTooLarge`：伪造偏小/0 的 header、实际 payload 超过测试 `maxTotal`；走 `extractZipWithLimits`；失败不得仅因声明之和（声明之和必须 < maxTotal）。

```
go test -count=1 -run '^TestExtractZipRejects' ./internal/panel/archive
```

Expected: 新用例断言失败（超时/过大/过深尚未实现），不是缺符号。可先加空 `extractZipWithLimits` 包装现有逻辑使测试编译。

### Step 2：实现最小 Green

按设计 §4.3 实现。深度用 `\`→`/`、`Clean`、`ToSlash`。失败 `RemoveAll(destDir)`。`len(reader.File) > maxEntries` 在 MkdirAll 前拒绝。

```
gofmt -w internal/panel/archive/zip.go internal/panel/archive/zip_test.go
go test -count=1 ./internal/panel/archive
```

Expected: PASS，既有 traversal/symlink/index 不退化。

### Step 3：提交

```
git add internal/panel/archive/zip.go internal/panel/archive/zip_test.go
git commit -s -m "fix: 限制 panel 归档展开总量"
```

---

## Task 2：AQ-03 默认 timeout

**Files:**
- Modify: `internal/panel/release/github.go`
- Modify: `internal/panel/release/github_test.go`

### Step 1：Red

- `TestNilClientIsNotDefaultClient`：`Client{}.httpClient() != http.DefaultClient` 且 Timeout == 2m。
- `TestClientLatestReleaseUsesNilClientPath`：`HTTPClient: nil` + httptest APIBase 成功取 release。
- `TestClientLatestReleaseHonorsContextDeadline`：handler `<-r.Context().Done()`；150ms ctx；`CodeNetworkFailure`。

```
go test -count=1 -run '^Test(NilClient|ClientLatestRelease)' ./internal/panel/release
```

Expected: `TestNilClientIsNotDefaultClient` FAIL（当前返回 DefaultClient）。

### Step 2：Green

实现 `defaultHTTPClient` / `httpClient` 按设计。

```
gofmt -w internal/panel/release/github.go internal/panel/release/github_test.go
go test -count=1 ./internal/panel/release
```

### Step 3：提交

```
git commit -s -m "fix: 为 panel release client 设置默认超时"
```

---

## Task 3：govulncheck 观察 job + 文档

**Files:**
- Modify: `.github/workflows/ci.yml`
- Modify: `docs/code-quality-audit-2026-08-13.md`
- Modify: `docs/superpowers/plans/2026-08-13-code-quality-roadmap.md`
- Add already-written: `docs/superpowers/specs/2026-08-13-code-quality-phase-2-design.md`
- Add: this plan

### Step 1

在 `ci.yml` 增加 job `govulncheck`：ubuntu、setup-go、job 级 `continue-on-error: true`、不加入 `test.needs`、打印版本、`go run golang.org/x/vuln/cmd/govulncheck@latest ./...`、summary 写 `RESULT=...`。

审计/roadmap：AQ-02/AQ-03 标本地整改；Phase 2 状态 `实施中` 直到 CI 绿；linter 四行表（gosec/bodyclose/noctx/errorlint）均为不启用。

### Step 2：结构检查

确认 YAML 中 `govulncheck` 不在 `test.needs`；`continue-on-error` 在 job 级。

### Step 3：提交

```
git commit -s -m "ci: 增加 govulncheck 观察任务"
```

（文档可同 commit 或随后 `docs: 记录二期质量整改`。）

---

## Task 4：验收、PR、merge

```
gofmt -l .
go test -count=1 ./internal/panel/archive ./internal/panel/release ./internal/panel
```

Push `docs/code-quality-phase-2`，开 PR，等待 required checks，全绿后 `gh pr merge --merge`。
