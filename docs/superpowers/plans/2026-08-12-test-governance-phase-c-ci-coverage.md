# Phase C CI and Coverage Governance Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在三平台原生运行默认测试，生成统一跨包覆盖报告，并在观察期后用同环境 base/HEAD 比较阻止无说明退化。

**Architecture:** CI 将原 `test` job 拆为 OS matrix、Windows race、vet-format 与 Linux coverage。一个无第三方依赖的 Go CLI 解析 coverprofile，按总仓和固定关键包集合聚合语句覆盖，并比较两个 profile；观察期只输出 summary，门禁激活通过显式 workflow 参数完成。

**Tech Stack:** GitHub Actions、Go coverprofile、Go 标准库、JSON/text summary。

## Global Constraints

- 遵守总路线图 Global Constraints 与 Governance Gate。
- coverage 的可比口径固定为 `ubuntu-latest`、相同 Go toolchain、`-covermode=atomic -coverpkg=./...`。
- 第一轮只观察至少 10 个成功 `main` run；不得在同一提交中直接启用百分比失败门禁。
- coverage artifact 位于 runner 临时目录，不写入或提交仓库。
- 六目标 `CGO_ENABLED=0` cross-build 保持不变。

---

### Task 1: Coverage Profile Parser Tests

**Files:**
- Create: `scripts/coverage-gate/main.go`
- Create: `scripts/coverage-gate/main_test.go`

**Interfaces:**
- Produces: `parseProfile(io.Reader) (report, error)`、`compare(base, head report, policy) result`。
- Consumers: Task 3/4 coverage job。

- [ ] **Step 1: 写 parser 失败测试**

在 `main_test.go` 用内存 profile 覆盖：mode header、覆盖/未覆盖 block、同包多文件、Windows 风格路径、格式错误和零语句。Expected: FAIL，因为类型和函数尚不存在。

```go
func TestParseProfileAggregatesTotalAndCriticalPackages(t *testing.T) {
    input := `mode: atomic
github.com/mihari-proxy/mihari/internal/runtime/a.go:1.1,2.1 4 1
github.com/mihari-proxy/mihari/internal/runtime/b.go:3.1,4.1 6 0
`
    got, err := parseProfile(strings.NewReader(input))
    // assert total 4/10 and internal/runtime 4/10
}
```

- [ ] **Step 2: 写 comparison 失败测试**

覆盖总覆盖下降 0.5pp 边界、关键包下降 1.0pp 边界、改善、base 中不存在的新包、head 缺包以及 deterministic 排序。

- [ ] **Step 3: 运行 Red**

```console
go test -count=1 ./scripts/coverage-gate
```

Expected: FAIL，缺少 parser/comparison 实现。

### Task 2: Coverage Gate CLI Implementation

**Files:**
- Modify: `scripts/coverage-gate/main.go`
- Modify: `scripts/coverage-gate/main_test.go`

**Interfaces:**
- CLI: `coverage-gate report -profile <path>`。
- CLI: `coverage-gate compare -base <path> -head <path> -total-drop 0.5 -critical-drop 1.0`。
- Output: Markdown summary to stdout; compare violation exits non-zero。

- [ ] **Step 1: 实现 profile parser**

定义明确类型：

```go
type coverage struct { Covered, Statements uint64 }
type report struct { Total coverage; Packages map[string]coverage }
type policy struct { TotalDrop, CriticalDrop float64; Critical []string }
type result struct { TotalDelta float64; PackageDeltas map[string]float64; Violations []string }
```

用 `bufio.Scanner`、`strings.Fields`、`strconv` 解析 profile；package 从 module path 到最后一个 `/` 之间提取。拒绝 malformed、负数、count 非法和重复 mode header。

- [ ] **Step 2: 实现比较与 CLI**

百分比只在展示/比较时计算；分母为零时显式输出 `n/a`。关键包列表硬编码为规格中的十个包，避免 workflow 与工具产生两份配置。

- [ ] **Step 3: 运行 Green 与命令烟测**

```console
go test -count=1 ./scripts/coverage-gate
go test -count=1 -covermode=atomic -coverpkg=./... -coverprofile="$env:TEMP\mihari-coverage.out" ./...
go run ./scripts/coverage-gate report -profile "$env:TEMP\mihari-coverage.out"
```

Expected: parser/comparison 测试通过；对有效 profile 输出 total 与关键包表。

- [ ] **Step 4: Commit（仅用户明确要求时）**

```console
git add scripts/coverage-gate
git commit -s -m "test(tooling): 增加跨包覆盖率比较工具"
```

### Task 3: Native OS Matrix and Coverage Observability

**Files:**
- Modify: `.github/workflows/ci.yml`

**Interfaces:**
- Consumes: Task 2 `coverage-gate report`。
- Produces: `unit (windows|linux|macos)`、`race`、`vet-format`、`coverage` job。

- [ ] **Step 1: 重构 test jobs**

将默认测试改为 `matrix.os: [windows-latest, ubuntu-latest, macos-latest]`，每项运行：

```yaml
- name: Test
  run: go test -count=1 ./...
```

保留独立 `race` 在 `windows-latest`，命令为 `go test -count=1 -race ./...`；vet/format 独立执行，避免某平台失败掩盖其他结果。

- [ ] **Step 2: 增加只观察 coverage job**

在 `ubuntu-latest`、bash 中执行：

```bash
profile="$RUNNER_TEMP/mihari-coverage.out"
go test -count=1 -covermode=atomic -coverpkg=./... -coverprofile="$profile" ./...
go run ./scripts/coverage-gate report -profile "$profile" | tee -a "$GITHUB_STEP_SUMMARY"
```

上传 profile，artifact retention 使用明确短周期；该 job 此时只因测试或 parser 错误失败，不因百分比失败。

- [ ] **Step 3: 本地等价验证**

```console
go test -count=1 ./scripts/coverage-gate
go test -count=1 ./...
go test -count=1 -race ./...
go vet ./...
```

- [ ] **Step 4: workflow 审核**

检查 job 名不会破坏当前 branch protection 的 `cross-build`；确认 matrix 不调用真实系统服务；确认 coverage profile 仅位于 `$RUNNER_TEMP`。

- [ ] **Step 5: Commit（仅用户明确要求时）**

```console
git add .github/workflows/ci.yml
git commit -s -m "ci: 增加三平台测试与覆盖率报告"
```

### Task 4: Relative Coverage Regression Gate

**Prerequisite:** coverage job 已在 `main` 上连续成功至少 10 次，且审查确认自然波动不超过设计容差。

**Files:**
- Modify: `.github/workflows/ci.yml`
- Modify if observations require: `scripts/coverage-gate/main_test.go`
- Update: `docs/superpowers/specs/2026-08-12-test-coverage-governance-design.md`（记录观察样本与最终批准阈值）

**Interfaces:**
- Consumes: Task 2 `coverage-gate compare`。
- Produces: PR merge-base 与 HEAD 同环境相对门禁。

- [ ] **Step 1: 记录观察结果并请求阈值批准**

在规格中记录 10 次 run 的 total/关键包最小、最大和波动。若数据支持原设计，请用户批准 total drop 0.5pp、critical drop 1.0pp；否则提出有证据的新阈值。未经批准不继续。

- [ ] **Step 2: 在临时 git worktree 计算 base profile**

workflow fetch PR base SHA，在 `$RUNNER_TEMP/base-worktree` 创建 detached worktree，使用同一 Go 版本和相同命令生成 base profile；用 `if: always()` 清理 worktree。不得覆盖 PR 工作区。

- [ ] **Step 3: 比较 HEAD**

```bash
go run ./scripts/coverage-gate compare \
  -base "$RUNNER_TEMP/base.out" \
  -head "$RUNNER_TEMP/head.out" \
  -total-drop 0.5 -critical-drop 1.0 | tee -a "$GITHUB_STEP_SUMMARY"
```

Expected: 超过任一批准容差时非零退出；改善或容差内波动通过。

- [ ] **Step 4: 验证 gate 自身**

在测试 fixture 中交换 base/head，证明下降会失败、改善会通过；在 draft PR 或专用分支验证 summary、artifact 和 worktree cleanup。

- [ ] **Step 5: Commit（仅用户明确要求时）**

```console
git add .github/workflows/ci.yml scripts/coverage-gate/main_test.go docs/superpowers/specs/2026-08-12-test-coverage-governance-design.md
git commit -s -m "ci: 启用覆盖率相对基线门禁"
```

### Task 5: Phase Verification

**Files:** no new files.

- [ ] **Step 1: 完整本地验证**

```console
go test -count=1 ./scripts/coverage-gate
go test -count=1 ./...
go test -count=1 -race ./...
go vet ./...
gofmt -l scripts
git diff --check
```

- [ ] **Step 2: 检查产物与 diff**

```console
git status --short
git diff --stat
```

Expected: 不含 `coverage.out`、测试二进制、临时 worktree 或无关文件。
