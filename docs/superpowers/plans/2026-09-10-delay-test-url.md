# TUI/CLI 测速 URL 对齐 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** TUI/CLI 测速使用内核 `testUrl`（省略时由 daemon 回落），并补齐并发上限与错误分类，修复 #204/#210。

**Architecture:** 控制面只读投影补上 `testUrl`/`test_url`。delay-test 允许省略 URL/超时，由 control server 在调用 mihomo 前解析。TUI/CLI 默认不再写死 gstatic。TUI 继续按叶子 `DelayProxy`，用 5 并发队列，跳过组名。

**Tech Stack:** Go 1.26（`go.mod` toolchain）、标准库 `net/http/httptest`、bubbletea v2、`go test`。不新增依赖。

**Spec:** `docs/superpowers/specs/2026-09-10-delay-test-url-design.md`

**工作目录:** `C:\Users\Kinema\Documents\modular_dev\mihari\.worktrees\feat-210-delay-test-url`

**分支:** `feat/210-delay-test-url`（从 `origin/dev` @ `f619cf4`）

## Global Constraints

- 不读磁盘 YAML；不改 Web 网关 delay 透传；不做 Dashboard/Core 双模式；不在 settings 增加测速 URL。
- 不升 `/v1` 版本；不加新端点；`RuntimeAPI.DelayGroup/DelayProxy` 与 control client 方法签名不变。
- 不改控制客户端或 mihomo HTTP client 的 10s `Timeout`；不删 `mihomo.Proxy.History`；不给 `ProxyNode` 加 `test_url`；不改 `CHANGELOG.md`。
- 默认测速 URL 只存在于 `internal/control/server`：`https://www.gstatic.com/generate_204`。TUI/CLI 源码不得再出现该字符串。
- 错误 envelope、默认 CLI 文本、TUI、slog 不得输出解析后的测速 URL。
- 每个 commit 使用 Conventional Commits 中文摘要，并带 DCO `-s`。禁止提交到 `main`/`dev`。
- 验证只认 worktree 内 `go test` / `gofmt`；worktree 的 codegraph/gopls 可能指向主 checkout，不可信。
- 测试不访问公网、不读真实用户目录、不用 `time.Sleep` 做同步。

## File Structure

| 文件 | 职责 |
| --- | --- |
| `internal/mihomo/types.go` | 适配层解码内核 `testUrl` |
| `internal/control/protocol/runtime.go` | `/v1` DTO：`ProxyGroup.test_url`、可省略的 `DelayTestRequest` |
| `internal/control/server/runtime.go` | 映射 `test_url`；delay-test/delay-proxy 回落 |
| `internal/cli/proxy.go` | 默认省略 `--url`/`--timeout` |
| `internal/tui/ui/strings.go` | `ProxyDelayInvalid` |
| `internal/tui/pages/proxies/model.go` | 省略 URL、队列、跳过组名、错误分类 |

不新建包。回落逻辑放在 server 包的小函数里，不放进 `protocol`。

---

### Task 1: mihomo 解码 `testUrl`

**Files:**
- Modify: `internal/mihomo/types.go`（`Proxy` 结构体）
- Test: `internal/mihomo/client_test.go`

**Interfaces:**
- Consumes: 现有 `type Proxy struct`
- Produces: `Proxy.TestURL string`，JSON 名 `testUrl`

- [ ] **Step 1: Write the failing test**

在 `internal/mihomo/client_test.go` 追加：

```go
func TestProxyJSONDecodesTestURL(t *testing.T) {
	var got Proxy
	if err := json.Unmarshal([]byte(`{"name":"HK","type":"URLTest","testUrl":"https://cp.cloudflare.com/generate_204"}`), &got); err != nil {
		t.Fatal(err)
	}
	if got.Name != "HK" || got.Type != "URLTest" || got.TestURL != "https://cp.cloudflare.com/generate_204" {
		t.Fatalf("got=%#v", got)
	}
	var empty Proxy
	if err := json.Unmarshal([]byte(`{"name":"HK","type":"Selector"}`), &empty); err != nil {
		t.Fatal(err)
	}
	if empty.TestURL != "" {
		t.Fatalf("TestURL=%q", empty.TestURL)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```powershell
cd C:\Users\Kinema\Documents\modular_dev\mihari\.worktrees\feat-210-delay-test-url
go test ./internal/mihomo -run TestProxyJSONDecodesTestURL -count=1
```

Expected: FAIL，`got.TestURL` 未定义或始终为空。

- [ ] **Step 3: Write minimal implementation**

`internal/mihomo/types.go` 的 `Proxy` 增加：

```go
TestURL string `json:"testUrl,omitempty"`
```

放在 `XUDP` 字段之后。不要改 `History`。

- [ ] **Step 4: Run test to verify it passes**

同 Step 2。Expected: PASS。

- [ ] **Step 5: Commit**

```powershell
git add internal/mihomo/types.go internal/mihomo/client_test.go
git commit -s -m "feat(mihomo): 解码内核 testUrl"
```

---

### Task 2: `/v1` DTO 增加 `test_url` 并允许省略 delay 请求字段

**Files:**
- Modify: `internal/control/protocol/runtime.go`（`ProxyGroup`、`DelayTestRequest`）
- Test: `internal/control/protocol/runtime_test.go`

**Interfaces:**
- Consumes: Task 1 的内核字段名（本任务只改 protocol）
- Produces: `ProxyGroup.TestURL string` `json:"test_url,omitempty"`；`DelayTestRequest` 的 `url`/`timeout_ms` 带 `omitempty`

- [ ] **Step 1: Write the failing test**

在 `internal/control/protocol/runtime_test.go` 追加：

```go
func TestProxyGroupTestURLRoundTripAndOmitEmpty(t *testing.T) {
	raw, err := json.Marshal(ProxyGroup{Name: "HK", Type: "URLTest", TestURL: "https://cp.cloudflare.com/generate_204"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"test_url":"https://cp.cloudflare.com/generate_204"`) {
		t.Fatalf("raw=%s", raw)
	}
	var got ProxyGroup
	if err := json.Unmarshal(raw, &got); err != nil || got.TestURL != "https://cp.cloudflare.com/generate_204" {
		t.Fatalf("got=%#v err=%v", got, err)
	}
	empty, err := json.Marshal(ProxyGroup{Name: "GLOBAL", Type: "Selector"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(empty), "test_url") {
		t.Fatalf("empty=%s", empty)
	}
	var old ProxyGroup
	if err := json.Unmarshal([]byte(`{"name":"GLOBAL","type":"Selector"}`), &old); err != nil || old.TestURL != "" {
		t.Fatalf("old=%#v err=%v", old, err)
	}
}

func TestDelayTestRequestOmitsZeroValues(t *testing.T) {
	raw, err := json.Marshal(DelayTestRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{}` {
		t.Fatalf("raw=%s", raw)
	}
	explicit, err := json.Marshal(DelayTestRequest{URL: "https://example.com/ping", TimeoutMilliseconds: 3500})
	if err != nil {
		t.Fatal(err)
	}
	if string(explicit) != `{"url":"https://example.com/ping","timeout_ms":3500}` {
		t.Fatalf("explicit=%s", explicit)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```powershell
go test ./internal/control/protocol -run "TestProxyGroupTestURLRoundTripAndOmitEmpty|TestDelayTestRequestOmitsZeroValues" -count=1
```

Expected: FAIL（`TestURL` 未定义，和/或空 `DelayTestRequest` JSON 含 `"url":""`）。

- [ ] **Step 3: Write minimal implementation**

`ProxyGroup` 增加：

```go
TestURL string `json:"test_url,omitempty"`
```

`DelayTestRequest` 改为：

```go
type DelayTestRequest struct {
	URL                 string `json:"url,omitempty"`
	TimeoutMilliseconds int    `json:"timeout_ms,omitempty"`
}
```

不要给 `ProxyNode` 加字段。

- [ ] **Step 4: Run test to verify it passes**

同 Step 2。Expected: PASS。再跑：

```powershell
go test ./internal/control/protocol -count=1
```

Expected: 全绿（含既有 `TestRuntimeDTOsUseStableSchema`）。

- [ ] **Step 5: Commit**

```powershell
git add internal/control/protocol/runtime.go internal/control/protocol/runtime_test.go
git commit -s -m "feat(protocol): 下发 test_url 并允许省略 delay 请求字段"
```

---

### Task 3: `GET /v1/proxies` 映射组 `test_url`

**Files:**
- Modify: `internal/control/server/runtime.go`（`orderedProxyGroups` / `toGroup`）
- Test: `internal/control/server/runtime_test.go`

**Interfaces:**
- Consumes: `mihomo.Proxy.TestURL`；`protocol.ProxyGroup.TestURL`
- Produces: `GET /v1/proxies` 组对象带 `test_url`

- [ ] **Step 1: Write the failing test**

在 `internal/control/server/runtime_test.go` 追加：

```go
func TestProxiesMapsGroupTestURL(t *testing.T) {
	fake := &fakeRuntime{proxies: mihomo.Proxies{Proxies: map[string]mihomo.Proxy{
		"GLOBAL": {Name: "GLOBAL", Type: "Selector", Now: "HK", All: []string{"HK", "leaf"}},
		"HK":     {Name: "HK", Type: "URLTest", Now: "leaf", All: []string{"leaf"}, TestURL: "https://cp.cloudflare.com/generate_204"},
		"leaf":   {Name: "leaf", Type: "VLESS"},
	}}}
	server := New(Options{Token: "token", Store: state.NewStore(state.Snapshot{}), Runtime: fake})
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, authorizedRequest(http.MethodGet, "/v1/proxies", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var got protocol.ProxyGroups
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Groups) < 1 {
		t.Fatalf("groups=%#v", got.Groups)
	}
	var hk protocol.ProxyGroup
	for _, group := range got.Groups {
		if group.Name == "HK" {
			hk = group
		}
	}
	if hk.TestURL != "https://cp.cloudflare.com/generate_204" {
		t.Fatalf("HK=%#v", hk)
	}
	if strings.Contains(response.Body.String(), `"test_url":"https://cp.cloudflare.com/generate_204"`) == false {
		t.Fatalf("body=%s", response.Body.String())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```powershell
go test ./internal/control/server -run TestProxiesMapsGroupTestURL -count=1
```

Expected: FAIL，`HK.TestURL` 为空。

- [ ] **Step 3: Write minimal implementation**

`toGroup` 构造 `protocol.ProxyGroup` 时抄入 `proxy.TestURL`：

```go
return protocol.ProxyGroup{
	Name: proxy.Name, Type: proxy.Type, Now: proxy.Now,
	All: append([]string(nil), proxy.All...), Nodes: nodes,
	TestURL: proxy.TestURL,
}
```

- [ ] **Step 4: Run test to verify it passes**

同 Step 2。Expected: PASS。再跑 `TestProxiesMapsNodeProtocolMetadata` 与 `TestProxiesPreservesGLOBALAllOrder`。

- [ ] **Step 5: Commit**

```powershell
git add internal/control/server/runtime.go internal/control/server/runtime_test.go
git commit -s -m "feat(control): GET /v1/proxies 下发组 test_url"
```

---

### Task 4: delay-test / delay-proxy daemon 回落

**Files:**
- Modify: `internal/control/server/runtime.go`（`delayTest`、`delayProxy`、新增回落函数与常量）
- Modify: `internal/control/server/runtime_test.go`（`fakeRuntime` 记录 URL/timeout，增加 `proxiesErr`）
- Test: `internal/control/server/runtime_test.go`

**Interfaces:**
- Consumes: `DelayTestRequest` 零值合法；`runtime.Proxies`；`orderedProxyGroups`
- Produces: server 在调用 `DelayGroup`/`DelayProxy` 前解析出非空 URL 与 `(0,60000]` 超时

常量（仅 server 包）：

```go
const (
	defaultDelayTestURL   = "https://www.gstatic.com/generate_204"
	defaultDelayTimeoutMS = 5000
	maxDelayTimeoutMS     = 60_000
)
```

回落顺序见 spec §6.2。显式非空 URL **不**调用 `Proxies()`。

- [ ] **Step 1: Extend fakeRuntime and write failing tests**

`fakeRuntime` 增加字段（`delayedProxy` 已存在，`DelayProxy` 继续写它）：

```go
proxiesErr     error
delayedGroup   string
delayedURL     string
delayedTimeout int
```

`Proxies` / `DelayGroup` / `DelayProxy` 改为：

```go
func (f *fakeRuntime) Proxies(context.Context) (mihomo.Proxies, error) {
	if f.proxiesErr != nil {
		return mihomo.Proxies{}, f.proxiesErr
	}
	return f.proxies, nil
}

func (f *fakeRuntime) DelayGroup(_ context.Context, group, testURL string, timeoutMilliseconds int) (mihomo.Delays, error) {
	f.delayedGroup, f.delayedURL, f.delayedTimeout = group, testURL, timeoutMilliseconds
	return mihomo.Delays{"DIRECT": 1}, nil
}

func (f *fakeRuntime) DelayProxy(_ context.Context, name, testURL string, timeoutMilliseconds int) (uint16, error) {
	f.delayedProxy, f.delayedURL, f.delayedTimeout = name, testURL, timeoutMilliseconds
	return f.proxyDelay, nil
}
```

追加测试（可放在同一文件）：

```go
func TestDelayTestFallsBackToKernelAndDefaultURL(t *testing.T) {
	cloud := "https://cp.cloudflare.com/generate_204"
	other := "https://www.gstatic.com/generate_204"
	proxies := mihomo.Proxies{Proxies: map[string]mihomo.Proxy{
		"GLOBAL": {Name: "GLOBAL", Type: "Selector", All: []string{"HK", "TW", "leaf"}},
		"HK":     {Name: "HK", Type: "URLTest", All: []string{"leaf"}, TestURL: cloud},
		"TW":     {Name: "TW", Type: "URLTest", All: []string{"leaf"}, TestURL: "https://example.com/tw"},
		"leaf":   {Name: "leaf", Type: "VLESS"},
		"plain":  {Name: "plain", Type: "Selector", All: []string{"orphan"}, TestURL: ""},
		"orphan": {Name: "orphan", Type: "Direct"},
	}}

	t.Run("explicit url wins", func(t *testing.T) {
		fake := &fakeRuntime{proxies: proxies}
		postDelay(t, fake, "/v1/proxy-groups/HK/delay-test", `{"url":"https://example.com/ping","timeout_ms":3500}`)
		if fake.delayedGroup != "HK" || fake.delayedURL != "https://example.com/ping" || fake.delayedTimeout != 3500 {
			t.Fatalf("fake=%#v", fake)
		}
	})
	t.Run("empty uses group testUrl", func(t *testing.T) {
		fake := &fakeRuntime{proxies: proxies}
		postDelay(t, fake, "/v1/proxy-groups/HK/delay-test", `{}`)
		if fake.delayedURL != cloud || fake.delayedTimeout != 5000 {
			t.Fatalf("fake=%#v", fake)
		}
	})
	t.Run("leaf uses first parent in GLOBAL.All", func(t *testing.T) {
		fake := &fakeRuntime{proxies: proxies}
		postDelay(t, fake, "/v1/proxies/leaf/delay-test", `{}`)
		if fake.delayedProxy != "leaf" || fake.delayedURL != cloud {
			t.Fatalf("fake=%#v", fake)
		}
	})
	t.Run("no kernel url uses default", func(t *testing.T) {
		fake := &fakeRuntime{proxies: proxies}
		postDelay(t, fake, "/v1/proxies/orphan/delay-test", `{}`)
		if fake.delayedURL != other {
			t.Fatalf("url=%q", fake.delayedURL)
		}
	})
	t.Run("timeout bounds", func(t *testing.T) {
		fake := &fakeRuntime{proxies: proxies}
		rec := postDelayRaw(t, fake, "/v1/proxy-groups/HK/delay-test", `{"timeout_ms":70000}`)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid_argument") {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), cloud) {
			t.Fatalf("leaked url: %s", rec.Body.String())
		}
		rec = postDelayRaw(t, fake, "/v1/proxy-groups/HK/delay-test", `{"timeout_ms":-1}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("neg status=%d", rec.Code)
		}
	})
	t.Run("proxies failure does not default", func(t *testing.T) {
		fake := &fakeRuntime{proxiesErr: protocol.APIError{Code: protocol.CodeUpstreamFailure, Message: "mihomo controller is unavailable"}}
		rec := postDelayRaw(t, fake, "/v1/proxy-groups/HK/delay-test", `{}`)
		if rec.Code != http.StatusBadGateway {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		if fake.delayedURL != "" {
			t.Fatalf("should not call DelayGroup, url=%q", fake.delayedURL)
		}
	})
	t.Run("explicit url skips Proxies", func(t *testing.T) {
		fake := &fakeRuntime{proxiesErr: protocol.APIError{Code: protocol.CodeUpstreamFailure, Message: "mihomo controller is unavailable"}}
		postDelay(t, fake, "/v1/proxy-groups/HK/delay-test", `{"url":"https://example.com/ping","timeout_ms":3500}`)
		if fake.delayedURL != "https://example.com/ping" || fake.delayedTimeout != 3500 {
			t.Fatalf("fake=%#v", fake)
		}
	})
}

func postDelay(t *testing.T, fake *fakeRuntime, path, body string) {
	t.Helper()
	rec := postDelayRaw(t, fake, path, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func postDelayRaw(t *testing.T, fake *fakeRuntime, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	server := New(Options{Token: "token", Store: state.NewStore(state.Snapshot{}), Runtime: fake})
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, authorizedRequest(http.MethodPost, path, bytes.NewBufferString(body)))
	return rec
}
```

- [ ] **Step 2: Run test to verify it fails**

```powershell
go test ./internal/control/server -run TestDelayTestFallsBackToKernelAndDefaultURL -count=1
```

Expected: FAIL（空 URL 仍 400 `invalid_argument`，或 timeout 0 被拒）。

- [ ] **Step 3: Write minimal implementation**

在 `internal/control/server/runtime.go` 增加常量与：

```go
func resolveDelayTimeout(ms int) (int, error) {
	if ms < 0 || ms > maxDelayTimeoutMS {
		return 0, errInvalidDelayTimeout
	}
	if ms == 0 {
		return defaultDelayTimeoutMS, nil
	}
	return ms, nil
}

func (s *Server) resolveDelayURL(ctx context.Context, name, requested string) (string, error) {
	if requested != "" {
		return requested, nil
	}
	upstream, err := s.runtime.Proxies(ctx)
	if err != nil {
		return "", err
	}
	if proxy, ok := upstream.Proxies[name]; ok && proxy.TestURL != "" {
		return proxy.TestURL, nil
	}
	for _, group := range orderedProxyGroups(upstream.Proxies) {
		if group.TestURL == "" {
			continue
		}
		for _, member := range group.All {
			if member == name {
				return group.TestURL, nil
			}
		}
	}
	return defaultDelayTestURL, nil
}
```

`errInvalidDelayTimeout` 用包内 `errors.New("delay test timeout is invalid")`，handler 转成 `writeInvalidArgument`，文案保持稳定、不含 URL。

`delayTest` / `delayProxy` 改为：先 `resolveDelayTimeout`，再 `resolveDelayURL`，再调用 runtime。超时非法 → 400；`Proxies()` 失败 → `writeControlError`。错误路径不要把 URL 写入 envelope `details`。

- [ ] **Step 4: Run test to verify it passes**

同 Step 2。Expected: PASS。再跑：

```powershell
go test ./internal/control/server -run "TestProxyDelayEndpointTestsOneNode|TestInstallAndQueryEndpoints|TestDelayTestFallsBackToKernelAndDefaultURL" -count=1
```

- [ ] **Step 5: Commit**

```powershell
git add internal/control/server/runtime.go internal/control/server/runtime_test.go
git commit -s -m "feat(control): delay-test 省略 URL 时回落到内核 testUrl"
```

---

### Task 5: CLI 默认省略测速 URL 与超时

**Files:**
- Modify: `internal/cli/proxy.go`
- Modify: `internal/cli/runtime_test.go`（`fakeRuntimeClient.DelayTest` 记录请求）
- Test: `internal/cli/runtime_test.go`

**Interfaces:**
- Consumes: `protocol.DelayTestRequest` omitempty 零值
- Produces: 默认 `mihari proxy test GROUP` 发送空 URL 与 timeout 0

- [ ] **Step 1: Write the failing test**

`fakeRuntimeClient` 增加：

```go
delayGroup   string
delayRequest protocol.DelayTestRequest
```

`DelayTest` 改为：

```go
func (c *fakeRuntimeClient) DelayTest(_ context.Context, group string, request protocol.DelayTestRequest) (protocol.DelayResult, error) {
	c.delayGroup = group
	c.delayRequest = request
	return protocol.DelayResult{Schema: "mihari/v1", Delays: map[string]uint16{"DIRECT": 1}}, nil
}
```

追加：

```go
func TestProxyTestOmitsURLByDefaultAndAllowsOverride(t *testing.T) {
	client := &fakeRuntimeClient{}
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	exit := Execute(context.Background(), []string{"proxy", "test", "HK"}, stdout, stderr, Dependencies{RuntimeClient: client})
	if exit != ExitOK || stderr.Len() != 0 {
		t.Fatalf("exit=%d stderr=%q", exit, stderr.String())
	}
	if client.delayGroup != "HK" || client.delayRequest.URL != "" || client.delayRequest.TimeoutMilliseconds != 0 {
		t.Fatalf("request=%#v group=%q", client.delayRequest, client.delayGroup)
	}

	client = &fakeRuntimeClient{}
	exit = Execute(context.Background(), []string{"proxy", "test", "HK", "--url", "https://example.com/ping", "--timeout", "3500"}, stdout, stderr, Dependencies{RuntimeClient: client})
	if exit != ExitOK || client.delayRequest.URL != "https://example.com/ping" || client.delayRequest.TimeoutMilliseconds != 3500 {
		t.Fatalf("override=%#v exit=%d", client.delayRequest, exit)
	}

	client = &fakeRuntimeClient{}
	exit = Execute(context.Background(), []string{"proxy", "test", "HK", "--timeout=-1"}, stdout, stderr, Dependencies{RuntimeClient: client})
	if exit != ExitUsage || client.delayGroup != "" {
		t.Fatalf("neg timeout exit=%d group=%q", exit, client.delayGroup)
	}
	client = &fakeRuntimeClient{}
	exit = Execute(context.Background(), []string{"proxy", "test", "HK", "--timeout", "70000"}, stdout, stderr, Dependencies{RuntimeClient: client})
	if exit != ExitUsage || client.delayGroup != "" {
		t.Fatalf("high timeout exit=%d", exit)
	}
}

func TestProxyGroupsTextOmitsTestURL(t *testing.T) {
	client := &fakeRuntimeClient{groups: protocol.ProxyGroups{Schema: "mihari/v1", Groups: []protocol.ProxyGroup{{
		Name: "HK", Type: "URLTest", Now: "leaf", TestURL: "https://cp.cloudflare.com/generate_204",
	}}}}
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	exit := Execute(context.Background(), []string{"proxy", "groups"}, stdout, stderr, Dependencies{RuntimeClient: client})
	if exit != ExitOK || strings.Contains(stdout.String(), "http") || strings.Contains(stdout.String(), "test_url") {
		t.Fatalf("stdout=%q exit=%d", stdout.String(), exit)
	}
}
```

确认 `ExitUsage` 已在 cli 包导出（现有 `TestConnectionsCloseAllRequiresConfirmation` 使用它）。

- [ ] **Step 2: Run test to verify it fails**

```powershell
go test ./internal/cli -run "TestProxyTestOmitsURLByDefaultAndAllowsOverride|TestProxyGroupsTextOmitsTestURL" -count=1
```

Expected: FAIL，默认请求仍带 gstatic 和 5000。

- [ ] **Step 3: Write minimal implementation**

`internal/cli/proxy.go`：

```go
testURL := ""
timeout := 0
```

`RunE` 在调用 `DelayTest` 前：

```go
if timeout < 0 || timeout > 60_000 {
	return invalidArgument("delay test timeout is invalid")
}
```

`--url` / `--timeout` flag 默认即上述零值。帮助文本可写 “empty uses daemon fallback”，不要写 gstatic。

- [ ] **Step 4: Run test to verify it passes**

同 Step 2，以及 `go test ./internal/cli -count=1`。Expected: PASS。

- [ ] **Step 5: Commit**

```powershell
git add internal/cli/proxy.go internal/cli/runtime_test.go
git commit -s -m "feat(cli): proxy test 默认走 daemon 测速回落"
```

---

### Task 6: TUI 测速错误分类

**Files:**
- Modify: `internal/tui/ui/strings.go`（`TimeoutLabel` 旁增加 `ProxyDelayInvalid = "Invalid"`）
- Modify: `internal/tui/pages/proxies/model.go`（`DelayKind`、`classifyDelayError`、`delayResultMsg`、`delayStyle`、`renderDelay`）
- Test: `internal/tui/pages/proxies/model_test.go`

**Interfaces:**
- Consumes: `protocol.APIError`；`diagnostics.Wrap`；`context.DeadlineExceeded`；`net.Error`
- Produces: `DelayFailed`、`DelayInvalid`；`classifyDelayError(error) DelayKind`

分类顺序（唯一，禁止 `"timeout"` 子串匹配）：

1. `APIError.Code == invalid_argument` → `DelayInvalid`
2. `errors.Is(DeadlineExceeded)` 或 `net.Error.Timeout()==true`（含 wrap）→ `DelayTimeout`
3. 其余 → `DelayFailed`（含裸 `upstream_failure`）

`context.Canceled` 不是 Timeout。

- [ ] **Step 1: Write the failing tests**

`strings.go` 的测试会在实现常量后通过；先改 `TestModel_DelayTimeoutPath` 并追加分类测试。

把 `TestModel_DelayTimeoutPath` 改成 `delayErr: context.DeadlineExceeded`，并用 `applyProxyCmd(t, model, model.testNode("n1"))` 代替 `model.Update(cmd())`（`testNode` 现在/之后都会返回 Batch，直接 `Update(cmd())` 会丢掉 `delayResultMsg`）。追加 Failed/Invalid 视图测试，同样走 `applyProxyCmd`：

```go
func TestModel_DelayErrorKindsRender(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"failed", protocol.APIError{Code: protocol.CodeUpstreamFailure, Message: "mihomo request failed"}, ui.FailedLabel},
		{"invalid", protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "delay test URL and timeout are invalid"}, ui.ProxyDelayInvalid},
		{"wrapped timeout", diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "local control operation failed"}, context.DeadlineExceeded), ui.TimeoutLabel},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := &fakeClient{delayErr: tc.err}
			model := New(client, func() string { return "op" })
			model.SetGroups(protocol.ProxyGroups{Groups: []protocol.ProxyGroup{{
				Name: "G", Nodes: []protocol.ProxyNode{{Name: "n1", Type: "ss"}},
			}}})
			model.expanded["G"] = true
			model.focus = FocusID{Group: "G", Node: "n1"}
			applyProxyCmd(t, model, model.testNode("n1"))
			view := model.View()
			if !strings.Contains(view, tc.want) {
				t.Fatalf("view missing %q:\n%s", tc.want, view)
			}
			if !themeDelayBadContains(model.theme, view, tc.want) {
				t.Fatalf("%q should use DelayBad:\n%s", tc.want, view)
			}
		})
	}
}
```

再追加：

```go
func TestClassifyDelayError(t *testing.T) {
	timeoutNet := timeoutNetError{}
	cases := []struct {
		name string
		err  error
		kind DelayKind
	}{
		{"deadline", context.DeadlineExceeded, DelayTimeout},
		{"wrapped data_failure deadline", diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "local control operation failed"}, context.DeadlineExceeded), DelayTimeout},
		{"net timeout", timeoutNet, DelayTimeout},
		{"invalid", protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "delay test URL and timeout are invalid"}, DelayInvalid},
		{"upstream", protocol.APIError{Code: protocol.CodeUpstreamFailure, Message: "mihomo request failed"}, DelayFailed},
		{"canceled", context.Canceled, DelayFailed},
		{"plain", errors.New("timeout"), DelayFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyDelayError(tc.err); got != tc.kind {
				t.Fatalf("kind=%v want %v", got, tc.kind)
			}
		})
	}
}

type timeoutNetError struct{}

func (timeoutNetError) Error() string   { return "i/o timeout" }
func (timeoutNetError) Timeout() bool   { return true }
func (timeoutNetError) Temporary() bool { return true }
```

`TestDelayStyle_BandsAndTimeout` 增加 `DelayFailed` / `DelayInvalid` → `"bad"`。Task 6 所有测速结果测试必须用 `applyProxyCmd`，禁止 `Update(cmd())`。

- [ ] **Step 2: Run test to verify it fails**

```powershell
go test ./internal/tui/pages/proxies -run "TestClassifyDelayError|TestModel_DelayTimeoutPath|TestModel_DelayErrorKindsRender|TestDelayStyle_BandsAndTimeout" -count=1
```

Expected: FAIL（`classifyDelayError` 未定义，和/或任意错误仍是 Timeout）。

- [ ] **Step 3: Write minimal implementation**

`DelayKind` 增加 `DelayFailed`、`DelayInvalid`。

```go
func classifyDelayError(err error) DelayKind {
	var api protocol.APIError
	if errors.As(err, &api) && api.Code == protocol.CodeInvalidArgument {
		return DelayInvalid
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return DelayTimeout
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return DelayTimeout
	}
	return DelayFailed
}
```

`delayResultMsg` 必须显式分叉：`typed.err == nil` → `DelayValue`（带 `Milliseconds`）。**不要**对 nil 调 `classifyDelayError`（其默认分支是 `DelayFailed`，会把既有 `"28 ms"` 测成 Failed）。非 nil 才 `Kind: classifyDelayError(typed.err)`。

`delayStyle`：`DelayTimeout`、`DelayFailed`、`DelayInvalid` 都用 `theme.DelayBad`。

`renderDelay`：

```go
case DelayTimeout:
	return style.Render(ui.TimeoutLabel)
case DelayFailed:
	return style.Render(ui.FailedLabel)
case DelayInvalid:
	return style.Render(ui.ProxyDelayInvalid)
```

`strings.go`：`ProxyDelayInvalid = "Invalid"`，放在 `TimeoutLabel` 旁。

- [ ] **Step 4: Run test to verify it passes**

```powershell
go test ./internal/tui/pages/proxies -count=1
```

Expected: PASS（含既有选择失败与 spinner）。

- [ ] **Step 5: Commit**

```powershell
git add internal/tui/ui/strings.go internal/tui/pages/proxies/model.go internal/tui/pages/proxies/model_test.go
git commit -s -m "feat(tui): 区分测速 Timeout、Failed 与 Invalid"
```

---

### Task 7: TUI 省略 URL、跳过组名、5 并发队列

**Files:**
- Modify: `internal/tui/pages/proxies/model.go`（删除 `delayTestURL`/`delayTimeout`；`queue`/`inFlight`/`delayTestGen`；`fillSlots`；`testAll`；`t`）
- Modify: `internal/tui/pages/proxies/model_test.go`（`fakeClient` 记录 `DelayTestRequest`；阻塞门闩测并发）
- Test: `internal/tui/pages/proxies/model_test.go`

**Interfaces:**
- Consumes: Task 6 的 `classifyDelayError`；`DelayTestRequest{}`
- Produces: `delayTestConcurrency = 5`；`fillSlots() tea.Cmd`；`startDelay(name string) tea.Cmd`

行为（spec §7）：

- 真正发请求时才 `context.WithTimeout(10s)` 并设 `DelayTesting`。
- `Ctrl+T`：`delayTestGen++`，`queue` 换成 unique 叶子（跳过 `m.groups` 的 Name），立刻 `fillSlots`。
- `fillSlots` 循环到 `len(inFlight)==5` 或没有可启动名字；跳过已在飞的名字，继续填后面的。
- `testAll` / `testFocused` / `delayResultMsg` 返回的 `tea.Batch` 必须是**一层扁平** Batch：worker `tea.Cmd` 与可选 spinner 并列。禁止 `tea.Batch(tea.Batch(workers...), spinner)`，否则测试一旦 `cmd()` 就会把叶子 worker 跑掉。
- `delayResultMsg` 删 inFlight、写 delays，然后**总是** `fillSlots`。
- `t`：不可测叶子 / 已在飞 → no-op；否则插到 queue 队头再 `fillSlots`，不替换整表、不 bump generation。
- 不测组名；测 `DIRECT`。

- [ ] **Step 1: Write the failing tests**

扩展 `fakeClient`：

```go
type fakeClient struct {
	selectedGroup string
	selectedNode  string
	operationID   string
	selectErr     error
	delay         uint16
	delayErr      error
	delayCalls    map[string]int
	lastDelayReq  protocol.DelayTestRequest
	started       chan string
	release       chan struct{}
	mu            sync.Mutex
	inProgress    int
	maxInProgress int
}

func (c *fakeClient) DelayProxy(_ context.Context, name string, req protocol.DelayTestRequest) (protocol.DelayResult, error) {
	c.mu.Lock()
	if c.delayCalls == nil {
		c.delayCalls = make(map[string]int)
	}
	c.delayCalls[name]++
	c.lastDelayReq = req
	c.inProgress++
	if c.inProgress > c.maxInProgress {
		c.maxInProgress = c.inProgress
	}
	started := c.started
	release := c.release
	c.mu.Unlock()
	if started != nil {
		started <- name
	}
	if release != nil {
		<-release
	}
	c.mu.Lock()
	c.inProgress--
	c.mu.Unlock()
	if c.delayErr != nil {
		return protocol.DelayResult{}, c.delayErr
	}
	return protocol.DelayResult{Schema: "mihari/v1", Delays: map[string]uint16{name: c.delay}}, nil
}
```

无 `started`/`release` 时行为与现在同步 fake 相同（测试须保证这两个 channel 为 nil）。

测试：

```go
func TestModel_DelayRequestOmitsURL(t *testing.T) {
	client := &fakeClient{delay: 10}
	model := New(client, func() string { return "op" })
	model.SetGroups(protocol.ProxyGroups{Groups: []protocol.ProxyGroup{{
		Name: "G", Nodes: []protocol.ProxyNode{{Name: "n1"}},
	}}})
	model.focus = FocusID{Group: "G", Node: "n1"}
	applyProxyCmd(t, model, updateProxyKey(t, model, tea.KeyPressMsg{Code: 't', Text: "t"}))
	if client.lastDelayReq.URL != "" || client.lastDelayReq.TimeoutMilliseconds != 0 {
		t.Fatalf("req=%#v", client.lastDelayReq)
	}
}

func TestModel_ControlTSkipsGroupNamesKeepsDIRECT(t *testing.T) {
	client := &fakeClient{delay: 10}
	model := New(client, func() string { return "op" })
	model.SetGroups(protocol.ProxyGroups{Groups: []protocol.ProxyGroup{
		{Name: "HK", Type: "URLTest", Nodes: []protocol.ProxyNode{{Name: "leaf"}, {Name: "TW"}, {Name: "DIRECT"}}},
		{Name: "TW", Type: "Selector", Nodes: []protocol.ProxyNode{{Name: "leaf"}}},
	}})
	_, cmd := model.Update(tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl})
	applyProxyCmd(t, model, cmd)
	if client.delayCalls["TW"] != 0 || client.delayCalls["HK"] != 0 {
		t.Fatalf("calls=%v", client.delayCalls)
	}
	if client.delayCalls["leaf"] != 1 || client.delayCalls["DIRECT"] != 1 {
		t.Fatalf("calls=%v", client.delayCalls)
	}
}

func leafCmds(t *testing.T, cmd tea.Cmd) []tea.Cmd {
	t.Helper()
	if cmd == nil {
		return nil
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		t.Fatalf("want flat BatchMsg, got %T", msg)
	}
	var leaves []tea.Cmd
	for _, child := range batch {
		leaves = append(leaves, child)
	}
	return leaves
}

func TestModel_ControlTCapsInFlightAtFive(t *testing.T) {
	started := make(chan string, 16)
	release := make(chan struct{})
	client := &fakeClient{delay: 1, started: started, release: release}
	model := New(client, func() string { return "op" })
	nodes := make([]protocol.ProxyNode, 7)
	for i := range nodes {
		nodes[i] = protocol.ProxyNode{Name: string(rune('a' + i))}
	}
	model.SetGroups(protocol.ProxyGroups{Groups: []protocol.ProxyGroup{{Name: "G", Nodes: nodes}}})
	_, cmd := model.Update(tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl})
	leaves := leafCmds(t, cmd)
	var wg sync.WaitGroup
	for _, worker := range leaves {
		worker := worker
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = worker()
		}()
	}
	for i := 0; i < 5; i++ {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for start")
		}
	}
	client.mu.Lock()
	max := client.maxInProgress
	calls := len(client.delayCalls)
	client.mu.Unlock()
	if max > 5 || calls != 5 {
		t.Fatalf("max=%d calls=%d", max, calls)
	}
	select {
	case <-started:
		t.Fatal("started a 6th before release")
	default:
	}
	close(release)
	wg.Wait()
}

func TestModel_QueuedNameIsNotTesting(t *testing.T) {
	model := New(&fakeClient{delay: 1}, func() string { return "op" })
	nodes := make([]protocol.ProxyNode, 6)
	for i := range nodes {
		nodes[i] = protocol.ProxyNode{Name: string(rune('a' + i))}
	}
	model.SetGroups(protocol.ProxyGroups{Groups: []protocol.ProxyGroup{{Name: "G", Nodes: nodes}}})
	model.Update(tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl})
	testingCount := 0
	for _, st := range model.delays {
		if st.Kind == DelayTesting {
			testingCount++
		}
	}
	if testingCount != 5 {
		t.Fatalf("testing=%d delays=%v", testingCount, model.delays)
	}
	if model.delays[string(rune('a'+5))].Kind == DelayTesting {
		t.Fatal("queued 6th node must not be Testing")
	}
}

func TestModel_TDoesNotReplaceQueueOrDoubleInFlight(t *testing.T) {
	started := make(chan string, 8)
	release := make(chan struct{})
	client := &fakeClient{delay: 1, started: started, release: release}
	model := New(client, func() string { return "op" })
	model.SetGroups(protocol.ProxyGroups{Groups: []protocol.ProxyGroup{{
		Name: "G", Nodes: []protocol.ProxyNode{{Name: "n1"}, {Name: "n2"}},
	}}})
	model.focus = FocusID{Group: "G", Node: "n1"}
	_, cmd := model.Update(tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl})
	var wg sync.WaitGroup
	for _, worker := range leafCmds(t, cmd) {
		worker := worker
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = worker()
		}()
	}
	<-started
	<-started
	_, again := model.Update(tea.KeyPressMsg{Code: 't', Text: "t"})
	if again != nil {
		if msg := again(); msg != nil {
			if _, ok := msg.(tea.BatchMsg); ok {
				t.Fatal("t must not start a second in-flight DelayProxy for n1")
			}
		}
	}
	client.mu.Lock()
	n1 := client.delayCalls["n1"]
	client.mu.Unlock()
	if n1 != 1 {
		t.Fatalf("parallel n1 calls=%d", n1)
	}
	close(release)
	wg.Wait()
}

func TestModel_TDoesNotReplaceUnstartedQueue(t *testing.T) {
	model := New(&fakeClient{delay: 1}, func() string { return "op" })
	nodes := make([]protocol.ProxyNode, 7)
	for i := range nodes {
		nodes[i] = protocol.ProxyNode{Name: string(rune('a' + i))}
	}
	model.SetGroups(protocol.ProxyGroups{Groups: []protocol.ProxyGroup{{Name: "G", Nodes: nodes}}})
	model.Update(tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl})
	if len(model.queue) != 2 {
		t.Fatalf("queue=%d want 2", len(model.queue))
	}
	before := append([]string(nil), model.queue...)
	model.focus = FocusID{Group: "G", Node: string(rune('a'))}
	model.Update(tea.KeyPressMsg{Code: 't', Text: "t"})
	if len(model.queue) != 2 || model.queue[0] != before[0] || model.queue[1] != before[1] {
		t.Fatalf("t replaced Ctrl+T queue: before=%v after=%v", before, model.queue)
	}
}

func TestModel_SecondControlTReplacesUnstartedQueue(t *testing.T) {
	model := New(&fakeClient{delay: 1}, func() string { return "op" })
	nodes := make([]protocol.ProxyNode, 7)
	for i := range nodes {
		nodes[i] = protocol.ProxyNode{Name: string(rune('a' + i))}
	}
	model.SetGroups(protocol.ProxyGroups{Groups: []protocol.ProxyGroup{{Name: "G", Nodes: nodes}}})
	model.Update(tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl})
	if len(model.inFlight) != 5 || len(model.queue) != 2 {
		t.Fatalf("after first inFlight=%d queue=%d", len(model.inFlight), len(model.queue))
	}
	firstQueue := append([]string(nil), model.queue...)
	model.Update(tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl})
	if len(model.inFlight) != 5 {
		t.Fatalf("second Ctrl+T must not start over cap, inFlight=%d", len(model.inFlight))
	}
	if len(model.queue) != 7 {
		t.Fatalf("second Ctrl+T must replace queue with all leaves, queue=%d first=%v now=%v", len(model.queue), firstQueue, model.queue)
	}
}
```

`leafCmds` 只展开**一层** `tea.BatchMsg`，把子 `tea.Cmd` 原样交给 goroutine。worker 只调用 `worker()` 取结果，**禁止**在 worker goroutine 里 `model.Update`（`Model` 无锁，会 concurrent map write）。断言并发上限只看 fake 的 `maxInProgress` / `delayCalls`。`TestModel_QueuedNameIsNotTesting` 与 `TestModel_SecondControlTReplacesUnstartedQueue` 只看 `Update` 之后的 `queue`/`delays`/`inFlight`，不必跑 worker。保留并更新 `TestModel_ControlTTestsEveryUniqueNodeOnce`：共享叶子仍只测一次，且请求 URL 为空。

- [ ] **Step 2: Run test to verify it fails**

```powershell
go test ./internal/tui/pages/proxies -run "TestModel_DelayRequestOmitsURL|TestModel_ControlTSkipsGroupNamesKeepsDIRECT|TestModel_ControlTCapsInFlightAtFive|TestModel_QueuedNameIsNotTesting|TestModel_TDoesNotReplaceQueueOrDoubleInFlight|TestModel_TDoesNotReplaceUnstartedQueue|TestModel_SecondControlTReplacesUnstartedQueue|TestModel_ControlTTestsEveryUniqueNodeOnce|TestModel_DelayTimeoutPath|TestModel_DelayErrorKindsRender" -count=1
```

Expected: FAIL（仍写死 gstatic，或一次 Batch 打出全部节点，或组名被测）。

- [ ] **Step 3: Write minimal implementation**

删除 `delayTestURL`、`delayTimeout`。

`Model` 增加 `queue []string`、`inFlight map[string]uint64`、`delayTestGen uint64`。`New` 初始化 `inFlight`。

```go
const delayTestConcurrency = 5

func (m *Model) groupNameSet() map[string]struct{} {
	names := make(map[string]struct{}, len(m.groups))
	for _, group := range m.groups {
		names[group.Name] = struct{}{}
	}
	return names
}

func (m *Model) delayTestLeaves() []string {
	skip := m.groupNameSet()
	seen := make(map[string]struct{})
	var leaves []string
	for _, group := range m.groups {
		for _, node := range group.Nodes {
			if _, isGroup := skip[node.Name]; isGroup {
				continue
			}
			if _, dup := seen[node.Name]; dup {
				continue
			}
			seen[node.Name] = struct{}{}
			leaves = append(leaves, node.Name)
		}
	}
	return leaves
}

func (m *Model) startDelay(name string) tea.Cmd {
	gen := m.delayTestGen
	if m.inFlight == nil {
		m.inFlight = make(map[string]uint64)
	}
	m.inFlight[name] = gen
	m.delays[name] = DelayState{Kind: DelayTesting}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		result, err := m.client.DelayProxy(ctx, name, protocol.DelayTestRequest{})
		delay := uint16(0)
		if err == nil {
			delay = result.Delays[name]
		}
		return delayResultMsg{node: name, delay: delay, err: err, gen: gen}
	}
}

func (m *Model) fillSlots() []tea.Cmd {
	var cmds []tea.Cmd
	for len(m.inFlight) < delayTestConcurrency {
		name, ok := m.popNextQueuedName()
		if !ok {
			break
		}
		cmds = append(cmds, m.startDelay(name))
	}
	return cmds
}

func (m *Model) delayCmds() tea.Cmd {
	cmds := m.fillSlots()
	if spin := m.delaySpinCmdIfNeeded(); spin != nil {
		cmds = append(cmds, spin)
	}
	return tea.Batch(cmds...)
}

func (m *Model) popNextQueuedName() (string, bool) {
	for i, name := range m.queue {
		if _, flying := m.inFlight[name]; flying {
			continue
		}
		m.queue = append(m.queue[:i], m.queue[i+1:]...)
		return name, true
	}
	return "", false
}

func (m *Model) enqueueFront(name string) {
	next := make([]string, 0, len(m.queue)+1)
	next = append(next, name)
	for _, existing := range m.queue {
		if existing != name {
			next = append(next, existing)
		}
	}
	m.queue = next
}

func (m *Model) testAll() tea.Cmd {
	m.delayTestGen++
	m.queue = m.delayTestLeaves()
	return m.delayCmds()
}

func (m *Model) testNode(node string) tea.Cmd {
	if node == "" {
		return nil
	}
	if _, isGroup := m.groupNameSet()[node]; isGroup {
		return nil
	}
	if _, flying := m.inFlight[node]; flying {
		return nil
	}
	m.enqueueFront(node)
	return m.delayCmds()
}

func (m *Model) testFocused() tea.Cmd {
	return m.testNode(m.focus.Node)
}
```

`Update`：`ctrl+t` → `testAll`；节点上 `t` → `testFocused`。`delayResultMsg` 删除 `inFlight` 后分类/写 delays，再 `return m, m.delayCmds()`。`delayCmds` 保证扁平 Batch。

保留 `testNode` 薄封装（走 `enqueueFront`+`fillSlots`）。Task 6 的 `TestModel_DelayTimeoutPath` / `TestModel_DelayErrorKindsRender` 必须已经用 `applyProxyCmd`；若仍是 `Update(cmd())`，本任务一并改掉，否则 Batch 会被丢掉、节点卡在 Testing。不要恢复旧的立刻 `DelayProxy` + 写死 URL 实现。

`delayResultMsg` 可加 `gen uint64` 字段供测试；补槽不要求 `msg.gen == delayTestGen`。

- [ ] **Step 4: Run test to verify it passes**

```powershell
go test ./internal/tui/pages/proxies -count=1
```

Expected: PASS。若并发测试偶发失败，用 channel 门闩修测试，不要加 `Sleep`，不要放宽到 >5。

- [ ] **Step 5: Commit**

```powershell
git add internal/tui/pages/proxies/model.go internal/tui/pages/proxies/model_test.go
git commit -s -m "feat(tui): 测速走 daemon 回落并限制并发"
```

---

### Task 8: 包级回归与 gstatic 不再出现在 TUI/CLI

**Files:**
- Test only（必要时修 Task 5–7 漏网）

- [ ] **Step 1: Grep production sources and run package tests**

在 worktree 确认 `internal/tui` 与 `internal/cli` 不再含 `gstatic.com`。允许 `internal/control/server` 含默认常量。

```powershell
cd C:\Users\Kinema\Documents\modular_dev\mihari\.worktrees\feat-210-delay-test-url
$hits = Get-ChildItem -Recurse -File -Include *.go internal\tui,internal\cli | Select-String -Pattern "gstatic.com" -SimpleMatch
if ($hits) { $hits | ForEach-Object { "$($_.Path):$($_.LineNumber):$($_.Line)" }; throw "gstatic.com must not appear in TUI/CLI sources" }
go test ./internal/mihomo ./internal/control/protocol ./internal/control/server ./internal/cli ./internal/tui/pages/proxies ./internal/tui/ui -count=1
gofmt -l internal/mihomo/types.go internal/control/protocol/runtime.go internal/control/server/runtime.go internal/cli/proxy.go internal/tui/pages/proxies/model.go internal/tui/ui/strings.go
```

TUI/CLI 必须无 `gstatic.com` 匹配。`internal/control/server` 可以有 `defaultDelayTestURL`。

Expected: 测试通过；`gofmt -l` 无输出。

- [ ] **Step 2: Commit only if grep/tests forced extra fixes**

无额外 diff 则跳过 commit。

---

## Spec coverage

| Spec | Task |
| --- | --- |
| §5.1 `mihomo.Proxy.TestURL` | 1 |
| §5.2 `ProxyGroup.test_url` omitempty；不加 ProxyNode 字段 | 2、3 |
| §5.3 省略 url/timeout；`{}` 合法；timeout 边界 | 2、4 |
| §6 daemon 回落顺序、默认 gstatic、显式 URL 不查 Proxies、Proxies 失败不默认 | 4 |
| §6.3 错误不含 URL | 4 |
| §8 CLI 默认省略、覆盖、文本 groups 不打印 URL | 5 |
| §7.4 错误分类与 wrap | 6 |
| §7.1–7.3 省略 URL、跳过组名、测 DIRECT、队列、5 并发、`t` 不替换队列 | 7 |
| 非目标（Web 网关、YAML、CHANGELOG、HTTP Timeout、History） | 全局约束，无任务 |

## Placeholder scan

无 TBD/TODO；任务含完整测试代码与命令。
