# Phase A Control Protocol and WebSocket Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 补齐订阅 `/v1` server/client 契约矩阵，并直接验证 Web gateway 的 WebSocket 双向代理与关闭语义。

**Architecture:** handler 测试使用现有 `fakeSubscriptionRuntime` 和 `httptest`；client 测试扩展现有有限端点表；IPC 仅增加删除与迟到 refresh 的跨包不变量；WebSocket 使用两个本地 `httptest.Server` 组成 browser → gateway → fake controller 链路。

**Tech Stack:** Go `testing`、`net/http/httptest`、`encoding/json`、`github.com/coder/websocket`、现有 protocol/runtime fake。

## Global Constraints

- 遵守总路线图的全部 Global Constraints 与 Governance Gate。
- 不改变现有订阅路由、DTO 或客户端公开方法签名。
- WebSocket 测试不得使用固定端口、固定 sleep 或公网。
- controller secret 只允许出现在 gateway → fake controller 请求头中。

---

### Task 1: Subscription Server Contract Matrix

**Files:**
- Modify: `internal/control/server/subscription_test.go`
- Verify: `internal/control/server/subscription.go:22`

**Interfaces:**
- Consumes: `Server.Handler() http.Handler`、`subscriptionAPI`。
- Produces: `fakeSubscriptionRuntime` 的调用记录字段，以及 list/show/refresh/use/enable/remove 的直接契约测试。

- [ ] **Step 1: 记录覆盖基线**

```console
go test -count=1 -coverprofile=subscription-server-before.out ./internal/control/server
go tool cover -func=subscription-server-before.out
```

Expected: `showSubscription`、`enableSubscription`、`removeSubscription` 等目标函数显示 0.0%；报告文件放在系统临时目录或验证后删除，不提交。

- [ ] **Step 2: 扩展 fake 调用记录**

在 `fakeSubscriptionRuntime` 增加 `catalog`、`operation`、`profileID`、`enabled`、`err` 字段；每个 mutation fake 保存收到的参数并返回语义明确的 profile。核心结构：

```go
type fakeSubscriptionRuntime struct {
    *fakeRuntime
    catalog   subscription.PublicCatalog
    operation runtimeapi.Operation
    profileID string
    enabled   bool
    err       error
    added     runtimeapi.AddSubscriptionInput
    setInput  runtimeapi.SetSubscriptionInput
}
```

- [ ] **Step 3: 增加读端点测试**

新增 `TestSubscriptionListAndShowRoutesReturnSecretFreeDTOs` 和 `TestSubscriptionShowRouteMapsNotFound`。构造包含流量字段、`ProxyMode` 和不应出现在响应中的 URL 语义数据，断言 schema、revision、active ID、字段映射及 `CodeInvalidArgument`。

- [ ] **Step 4: 增加 mutation 表驱动测试**

新增 `TestSubscriptionProfileMutationRoutesForwardOperation`，覆盖 refresh/use/enable/remove：

```go
tests := []struct {
    name, method, path, body string
    wantEnabled *bool
}{
    {name: "refresh", method: http.MethodPost, path: "/v1/subscriptions/one/refresh", body: `{"operation_id":"refresh-1","if_revision":7}`},
    {name: "use", method: http.MethodPut, path: "/v1/subscriptions/one/active", body: `{"operation_id":"use-1","if_revision":7}`},
    {name: "enable false", method: http.MethodPut, path: "/v1/subscriptions/one/enabled", body: `{"operation_id":"enable-1","if_revision":7,"enabled":false}`},
    {name: "remove", method: http.MethodDelete, path: "/v1/subscriptions/one", body: `{"operation_id":"remove-1","if_revision":7}`},
}
```

断言 source 为 `control`、ID/revision/profile ID 完整转发，显式 `false` 未丢失，响应 operation ID 和提交后 revision 正确。

- [ ] **Step 5: 增加公共错误测试**

新增 `TestSubscriptionRoutesRejectInvalidBodiesAndUnavailableRuntime`，覆盖未知字段、第二个 JSON 对象、超大 body、缺失 operation ID、runtime capability 缺失和 fake 返回稳定 `APIError`。

- [ ] **Step 6: 运行目标测试与覆盖验证**

```console
go test -count=1 -run '^TestSubscription' ./internal/control/server
go test -count=1 -coverprofile=subscription-server-after.out ./internal/control/server
go tool cover -func=subscription-server-after.out
```

Expected: 新测试通过；目标 handler 不再为 0.0%。

- [ ] **Step 7: Commit（仅用户明确要求时）**

```console
git add internal/control/server/subscription_test.go
git commit -s -m "test(control): 补齐订阅服务端契约矩阵"
```

### Task 2: Typed Subscription Client Matrix

**Files:**
- Modify: `internal/control/client/runtime_test.go`
- Modify: `internal/control/client/webgui_test.go`
- Verify: `internal/control/client/runtime.go:200`
- Verify: `internal/control/client/webgui.go:45`

**Interfaces:**
- Consumes: `Client.doRuntime`、现有 `TestRuntimeClientFiniteEndpoints` 表。
- Produces: 所有 subscription 与 panel mutation 的 method/path/body/response 契约断言。

- [ ] **Step 1: 记录客户端零覆盖方法**

```console
go test -count=1 -coverprofile=control-client-before.out ./internal/control/client
go tool cover -func=control-client-before.out
```

Expected: `Subscription`、enable/update/remove 及若干 panel mutation 为 0.0%。

- [ ] **Step 2: 扩展有限端点表**

向 `TestRuntimeClientFiniteEndpoints` 增加 show/use/enable/update/remove。路径中使用 `id/one` 证明 `url.PathEscape`：

```go
{"subscription show", http.MethodGet, "/v1/subscriptions/id%2Fone", "", subscriptionJSON, invokeShow},
{"subscription use", http.MethodPut, "/v1/subscriptions/id%2Fone/active", mutationJSON, subscriptionJSON, invokeUse},
{"subscription enable", http.MethodPut, "/v1/subscriptions/id%2Fone/enabled", enabledJSON, subscriptionJSON, invokeEnable},
{"subscription update", http.MethodPatch, "/v1/subscriptions/id%2Fone", updateJSON, subscriptionJSON, invokeUpdate},
{"subscription remove", http.MethodDelete, "/v1/subscriptions/id%2Fone", mutationJSON, mutationResultJSON, invokeRemove},
```

`updateJSON` 必须同时包含 `auto_refresh:false` 和 `interval:""`，证明 pointer 字段不会因零值丢失。

- [ ] **Step 3: 补齐 panel client mutation 表**

在 `webgui_test.go` 增加 update/activate/rollback/uninstall/reinstall 的方法、escaped path、operation ID 与 revision 断言，复用现有本地 HTTP server 模式。

- [ ] **Step 4: 增加 API error 解码断言**

让一个订阅端点返回 409 和 `revision_conflict` envelope，断言 `errors.As(err, *protocol.APIError)` 以及 code/details，不匹配底层错误文本。

- [ ] **Step 5: 验证客户端包**

```console
go test -count=1 ./internal/control/client
go test -count=1 -coverprofile=control-client-after.out ./internal/control/client
go tool cover -func=control-client-after.out
```

Expected: 新增方法全部被执行；请求 JSON 与稳定响应类型正确。

- [ ] **Step 6: Commit（仅用户明确要求时）**

```console
git add internal/control/client/runtime_test.go internal/control/client/webgui_test.go
git commit -s -m "test(control): 补齐订阅与面板客户端契约"
```

### Task 3: Subscription Removal IPC Invariant

**Files:**
- Modify: `internal/integration/control_test.go`
- Reuse: `internal/runtime/subscription_test.go:146`

**Interfaces:**
- Consumes: daemon IPC fixture、typed client、subscription service controlled fetcher。
- Produces: 跨 IPC 删除成功且迟到 refresh 不复活对象的集成证明。

- [ ] **Step 1: 记录跨包覆盖基线**

```console
go test -count=1 -covermode=atomic -coverpkg=./... -coverprofile="$env:TEMP\mihari-integration-before.out" ./internal/integration
go tool cover -func="$env:TEMP\mihari-integration-before.out"
```

Expected: runtime 的“迟到 refresh 不复活已删除对象”由包内测试覆盖，但没有 client→IPC→runtime 的 remove/refresh 完整链路。该任务属于既有不变量的跨层覆盖，不制造错误断言。

- [ ] **Step 2: 最小扩展 integration fixture**

给现有 fixture 注入 channel-controlled `subscription.Fetcher`，并用 `t.Cleanup` 释放阻塞调用。不要复制 runtime 的提交判断。

- [ ] **Step 3: 写跨 IPC 场景**

新增 `TestSubscriptionRemoveOverIPCRejectsLateRefreshCommit`。用 channel 阻塞 fetch：启动 refresh → 等待进入下载 → 通过 client remove → 释放下载 → 断言 refresh 返回冲突/不存在，最终 list 中无该 ID。

- [ ] **Step 4: 运行 Red–Green 验证**

```console
go test -count=1 -run '^TestSubscriptionRemoveOverIPCRejectsLateRefreshCommit$' ./internal/integration
go test -count=1 ./internal/integration
```

Expected: 两条命令通过，测试结束后无 daemon、listener 或 goroutine 遗留；若测试揭示跨 IPC 缺陷，先保留失败证据，再做最小生产修复。

- [ ] **Step 5: Commit（仅用户明确要求时）**

```console
git add internal/integration/control_test.go
git commit -s -m "test(integration): 验证订阅删除拒绝迟到刷新"
```

### Task 4: WebSocket Authentication and Relay

**Files:**
- Modify: `internal/web/server_test.go`
- Verify: `internal/web/server.go:650`

**Interfaces:**
- Consumes: `web.New(Options)`、`Server.Serve`、`websocket.Accept/Dial`。
- Produces: browser → gateway → controller 的双向消息和 secret 隔离测试。

- [ ] **Step 1: 记录 `proxyWebSocket` 0% 基线**

```console
go test -count=1 -coverprofile=web-before.out ./internal/web
go tool cover -func=web-before.out
```

- [ ] **Step 2: 增加 fake WebSocket controller helper**

helper 接受期望 secret，记录 path/query/auth，回显收到的 message type/data，并在 `t.Cleanup` 关闭：

```go
func newWebSocketController(t *testing.T, wantSecret string) *httptest.Server
```

- [ ] **Step 3: 增加成功双向代理测试**

新增 `TestGatewayWebSocketRelaysBidirectionallyAndInjectsOnlyControllerSecret`：browser 使用 Web credential 取得 session，再连接 gateway stream；发送 text 与 binary frame；断言 controller 收到 controller secret、原 path/query 和相同帧，浏览器响应及 gateway 可见 header/body 均不含 secret。

- [ ] **Step 4: 运行成功路径测试**

```console
go test -count=1 -run '^TestGatewayWebSocketRelaysBidirectionallyAndInjectsOnlyControllerSecret$' ./internal/web
```

Expected: PASS；`proxyWebSocket` 覆盖不再为 0%。

### Task 5: WebSocket Shutdown and Error Paths

**Files:**
- Modify: `internal/web/server_test.go`
- Modify only if test proves a defect: `internal/web/server.go:650`

**Interfaces:**
- Consumes: Task 4 WebSocket helper。
- Produces: 上游关闭、客户端关闭、context 取消、握手错误的有界退出证明。

- [ ] **Step 1: 写关闭行为测试**

新增：

```go
func TestGatewayWebSocketUpstreamCloseStopsRelay(t *testing.T)
func TestGatewayWebSocketClientCloseStopsUpstream(t *testing.T)
func TestGatewayWebSocketContextCancelReleasesBothSides(t *testing.T)
```

使用 `done` channel 和 3 秒 context deadline 等待确定事件；禁止 `time.Sleep`。

- [ ] **Step 2: 写握手失败表驱动测试**

覆盖上游 401、500、不可连接和非法 controller URL，断言安全 HTTP 状态、响应不含 controller secret 或上游 body。

- [ ] **Step 3: 运行测试并只在失败时修实现**

```console
go test -count=1 -run '^TestGatewayWebSocket' ./internal/web
```

Expected: 若现实现泄漏 goroutine 或没有传播关闭，测试先失败；最小修复应让一个 relay 方向结束时取消共享 context 并关闭两端，不改变公开接口。

- [ ] **Step 4: 扩大验证**

```console
go test -count=1 ./internal/web ./internal/control/server ./internal/control/client ./internal/integration
go test -count=1 -race ./internal/web ./internal/integration
go vet ./...
gofmt -l internal/control internal/web internal/integration
```

Expected: 全部通过，`gofmt -l` 无输出。

- [ ] **Step 5: Commit（仅用户明确要求时）**

```console
git add internal/web/server.go internal/web/server_test.go
git commit -s -m "test(web): 覆盖 WebSocket 双向代理生命周期"
```
