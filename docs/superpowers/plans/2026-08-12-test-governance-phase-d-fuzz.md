# Phase D Bounded Fuzz Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为四类不可信输入边界增加可复现 seed corpus 和有界 nightly fuzz，不影响默认测试的隔离性。

**Architecture:** fuzz target 与被测包相邻，复用现有 fixture 作为 seed；每个 target 断言“不 panic + 明确安全不变量”，发现 crash 后先固化最小输入再修复。独立 workflow 顺序运行 target，每项 60 秒并设置 job timeout。

**Tech Stack:** Go native fuzzing、`testing.F`、`httptest`、`archive/zip`、GitHub Actions。

## Global Constraints

- 遵守总路线图 Global Constraints 与 Governance Gate。
- fuzz 输入只使用合成数据，不含真实 URL、凭据或用户文件。
- fuzz target 不访问公网，不写 `t.TempDir()` 之外路径。
- 默认 `go test ./...` 只运行 seed corpus；持续 fuzz 只在 nightly/手动 workflow。
- crash corpus 经人工确认无敏感数据后才允许提交。

---

### Task 1: Subscription Document Parser Fuzzing

**Files:**
- Modify: `internal/subscription/document_test.go`
- Modify only on proven defect: `internal/subscription/document.go`

**Interfaces:**
- Consumes: `ParseDocument([]byte) (Document, error)`。
- Produces: `FuzzParseDocument`。

- [ ] **Step 1: 增加 seed corpus**

加入有效 proxies、有效 proxy-providers、空输入、多 YAML 文档、错误 proxies 类型、未知 tag、嵌套 alias 和接近大小边界的小型代表输入。

- [ ] **Step 2: 写 fuzz 不变量**

```go
func FuzzParseDocument(f *testing.F) {
    for _, seed := range documentSeeds { f.Add([]byte(seed)) }
    f.Fuzz(func(t *testing.T, input []byte) {
        document, err := ParseDocument(input)
        if err == nil && document == nil { t.Fatal("nil document without error") }
        if err == nil {
            _, proxies := document["proxies"]
            _, providers := document["proxy-providers"]
            if !proxies && !providers { t.Fatal("accepted non-subscription") }
        }
    })
}
```

- [ ] **Step 3: 验证 seed 与短 fuzz**

```console
go test -count=1 ./internal/subscription
go test -run '^$' -fuzz '^FuzzParseDocument$' -fuzztime=10s ./internal/subscription
```

- [ ] **Step 4: Commit（仅用户明确要求时）**

```console
git add internal/subscription/document.go internal/subscription/document_test.go
git commit -s -m "test(fuzz): 覆盖订阅文档解析边界"
```

### Task 2: Archive Path Fuzzing

**Files:**
- Modify: `internal/panel/archive/zip_test.go`
- Modify only on proven defect: `internal/panel/archive/zip.go`

**Interfaces:**
- Consumes: `SafeName(string) bool`、包内 `resolveTarget(string, string)`。
- Produces: `FuzzSafeArchivePath`。

- [ ] **Step 1: 增加路径 seed**

包含正常嵌套路径、`.`、`..`、绝对 Unix/Windows 路径、UNC、混合分隔符、NUL、空名、Unicode normalization 和超长 segment。

- [ ] **Step 2: 写安全不变量**

在 `t.TempDir()` 下调用 `resolveTarget`；若接受，使用 `filepath.Rel` 断言结果不是 `..`、不以 `..+separator` 开头且仍位于目标根。`SafeName` 拒绝时不得要求 `resolveTarget` 接受。

- [ ] **Step 3: 验证**

```console
go test -count=1 ./internal/panel/archive
go test -run '^$' -fuzz '^FuzzSafeArchivePath$' -fuzztime=10s ./internal/panel/archive
```

- [ ] **Step 4: Commit（仅用户明确要求时）**

```console
git add internal/panel/archive/zip.go internal/panel/archive/zip_test.go
git commit -s -m "test(fuzz): 覆盖面板归档路径边界"
```

### Task 3: Control JSON Decoder Fuzzing

**Files:**
- Modify: `internal/control/server/runtime_test.go`
- Modify only on proven defect: `internal/control/server/runtime.go:432`

**Interfaces:**
- Consumes: 包内 `decodeControlJSON(http.ResponseWriter, *http.Request, any) bool`。
- Produces: `FuzzDecodeControlJSON`。

- [ ] **Step 1: 增加 DTO seed**

使用 `MutationRequest`、`SubscriptionUpdateRequest`、未知字段、重复对象、尾随 JSON、畸形 UTF-8 和超过 `maxControlBodySize` 的代表输入。

- [ ] **Step 2: 写 fuzz harness**

每次创建 `httptest.NewRequest` 和 recorder，轮流解码到两个明确 DTO。断言：成功只返回 2xx 前置状态且 body 恰好一个对象；失败状态为 400，envelope 可解码为 `CodeInvalidArgument`，响应不回显原输入。

- [ ] **Step 3: 验证**

```console
go test -count=1 ./internal/control/server
go test -run '^$' -fuzz '^FuzzDecodeControlJSON$' -fuzztime=10s ./internal/control/server
```

- [ ] **Step 4: Commit（仅用户明确要求时）**

```console
git add internal/control/server/runtime.go internal/control/server/runtime_test.go
git commit -s -m "test(fuzz): 覆盖控制请求 JSON 解码边界"
```

### Task 4: Subscription Userinfo Fuzzing

**Files:**
- Modify: `internal/subscription/userinfo_test.go`
- Modify only on proven defect: `internal/subscription/userinfo.go`

**Interfaces:**
- Consumes: `ParseUserInfo(string) (UserInfo, bool)`、`UserInfo.Used()`。
- Produces: `FuzzParseUserInfo`。

- [ ] **Step 1: 增加 seed**

包含正常 header、大小写/空白、重复 key、负值、`MaxInt64`、溢出、乱码、未知 key 和空输入。

- [ ] **Step 2: 写不变量**

断言所有返回字段非负；`ok=false` 时结构为零值；`Used()` 永不为负。若 upload/download 求和溢出导致 `Used()` 错误归零，先添加明确回归测试，再决定采用饱和还是安全归零语义并请求审查，不在 fuzz 修复中暗改公开展示语义。

- [ ] **Step 3: 验证**

```console
go test -count=1 ./internal/subscription
go test -run '^$' -fuzz '^FuzzParseUserInfo$' -fuzztime=10s ./internal/subscription
```

- [ ] **Step 4: Commit（仅用户明确要求时）**

```console
git add internal/subscription/userinfo.go internal/subscription/userinfo_test.go
git commit -s -m "test(fuzz): 覆盖订阅配额头解析边界"
```

### Task 5: Bounded Fuzz Workflow

**Files:**
- Create: `.github/workflows/fuzz.yml`

**Interfaces:**
- Consumes: Tasks 1–4 fuzz target names。
- Produces: nightly + `workflow_dispatch` 有界 fuzz job。

- [ ] **Step 1: 创建 workflow**

触发器使用每日 cron 和手动触发；runner 为 `ubuntu-latest`；job `timeout-minutes: 10`。顺序执行：

```console
go test -run '^$' -fuzz '^FuzzParseDocument$' -fuzztime=60s ./internal/subscription
go test -run '^$' -fuzz '^FuzzSafeArchivePath$' -fuzztime=60s ./internal/panel/archive
go test -run '^$' -fuzz '^FuzzDecodeControlJSON$' -fuzztime=60s ./internal/control/server
go test -run '^$' -fuzz '^FuzzParseUserInfo$' -fuzztime=60s ./internal/subscription
```

- [ ] **Step 2: 失败产物策略**

仅在失败时上传对应包 `testdata/fuzz/<Target>` 与测试日志，artifact retention 设为 7 天。上传前拒绝大于 1 MiB 的单文件，并运行 `rg -a -n '(https?://|Bearer |token=|secret=)' <corpus-dir>`；命中时只上传日志，不上传 corpus。

- [ ] **Step 3: 默认与 bounded 验证**

```console
go test -count=1 ./...
go test -run '^$' -fuzz '^FuzzParseDocument$' -fuzztime=10s ./internal/subscription
go test -run '^$' -fuzz '^FuzzSafeArchivePath$' -fuzztime=10s ./internal/panel/archive
go test -run '^$' -fuzz '^FuzzDecodeControlJSON$' -fuzztime=10s ./internal/control/server
go test -run '^$' -fuzz '^FuzzParseUserInfo$' -fuzztime=10s ./internal/subscription
go test -count=1 -race ./...
go vet ./...
```

- [ ] **Step 4: 检查 crash corpus**

```console
git status --short
```

Expected: 没有未经审核的 `testdata/fuzz` crash 文件或其他临时产物。

- [ ] **Step 5: Commit（仅用户明确要求时）**

```console
git add .github/workflows/fuzz.yml
git commit -s -m "ci: 增加有界 fuzz 工作流"
```
