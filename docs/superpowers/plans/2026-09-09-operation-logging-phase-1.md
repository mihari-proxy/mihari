# Phase 1 操作日志上下文 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** 让现有 JSON logger 从显式传入的 ctx 提取 `operation_id` 和 `operation`，安全输出且不改变无操作上下文的行为。

**Architecture:** `internal/logging` 提供值类型元数据和私有 context key。现有 `redactingHandler` 在 Handle 时提取元数据、处理顶层保留字段并经过现有 redactor，再重放已有的属性/分组。没有任何业务入口接入、协议扩展或新日志文件。

**Tech Stack:** Go 1.26.0 language / go1.26.5 toolchain；标准库 context、log/slog、encoding/json、testing；现有 logging handler/redactor。

**Spec:** [操作日志上下文与分阶段诊断完善设计](../specs/2026-09-09-operation-logging-diagnostics-design.md)，仅实现 §5 和 §10 Phase 1；§4 的生成/跨进程传播与 §6–9、Phase 2–4 仅作为边界约束。

## Global Constraints

- 复用 `internal/logging`、`slog`、现有脱敏、轮转及生命周期管理，不引入第三方日志或 tracing 依赖。
- 保持 `/v1` DTO、JSON envelope、错误码、CLI 退出码和持久化格式；不向公开 Message/Details 填入内部 cause。
- 保持 daemon 单写入者、原生 IPC 和既有日志目录/权限边界。
- 不修改 mihomo，不扩展真实订阅、真实核心、系统服务或其他 testenv 操作。
- 不在本设计中实现 #197 的 setup 提示改进。
- 范围：`internal/logging` 的类型化元数据、ctx API、handler 提取、脱敏和派生行为。不批量修改业务模块、不新增协议字段。
- 禁止将 Phase 2 的错误封装、settings 修复、请求日志收口、启动 stderr 或 Phase 3/4 模块接入加入本计划。
- 不新增 operation ID 生成器、header、DTO 字段、CLI 文件日志、request/trace/instance ID、全局“当前操作”、goroutine 或队列。
- 只允许下表中的文件变更。新增建议先判断是否是本范围内的必要修正；范围外不得纳入，除非发现致命 P0/P1，先向用户汇报。
- 开发前读取根 AGENTS.md、.github/CONTRIBUTING.md、README.md 和 Spec。只在既有独立工作区/分支开发；用户未明确要求时不提交、推送或创建 PR。

---

## 0. 基线、文件边界与共同约定

工作区：`/home/kinema/dev/mihari/.worktrees/issue-216`；分支：`codex/issue-216-error-diagnostics`；代码基线：`d76e09d`。设计文档初审通过 SHA256：`dc1e57d86ce7b1aa86c73da1cd602528203bf7fa1757fdc6bdfadf47f802f745`。

| 文件 | 动作 | 职责 |
| --- | --- | --- |
| `internal/logging/operation_context.go` | 新建 | 类型化上下文 API，复制元数据值 |
| `internal/logging/operation_context_test.go` | 新建 | 继承、替换、取消和输入不变测试 |
| `internal/logging/operation_attrs.go` | 新建 | 日志 ID 校验、顶层保留属性过滤 |
| `internal/logging/handler.go` | 修改 | Handle 接入上下文，保留现有 WithAttrs/WithGroup/组件行为 |
| `internal/logging/operation_handler_test.go` | 新建 | 输出契约、派生、脱敏、不变性和并发测试 |

现有 `handler_test.go`、`redactor_test.go` 作为兼容回归，不修改其断言以迁就新实现。设计和本计划可为范围内的必要澄清更新；生产代码不得超出上述列表。

准备命令（所有命令在工作区执行）：

```bash
export PATH=/usr/local/go/bin:$PATH
export GOTOOLCHAIN=go1.26.5
go version
git -c safe.directory=/home/kinema/dev/mihari/.worktrees/issue-216 status --short --branch
go test ./internal/logging -run '^TestJSONHandler_' -count=1
```

先保存基线结果。全 logging 包有文件权限/导出测试，当前 root 环境历史上存在所有者相关失败；有失败时定位到确切测试并与未修改基线比较，不顺手修改权限、业务代码或将失败宣称为通过。实现后仍须执行本计划指定的检查。

### API 和空值规则

```go
// OperationMetadata identifies a logical operation in diagnostic logs.
type OperationMetadata struct {
    ID   string
    Name string
}

// WithOperation attaches a copy of operation to a non-nil context.
func WithOperation(ctx context.Context, operation OperationMetadata) context.Context

// OperationFromContext returns operation metadata explicitly bound to ctx.
func OperationFromContext(ctx context.Context) (OperationMetadata, bool)
```

`WithOperation` 的 ctx 必须非 nil，与标准库 context API 一致。`OperationFromContext(nil)` 返回零值、false，便于 handler 防御性读取。显式绑定零值返回 true，遮蔽父操作，不回退父 ID。API 不验证/改写 ID，不关联业务结果；过滤只发生在输出阶段。Name 是调用方提供的代码常量，本阶段不引入操作注册表或枚举。

“顶层冲突”指最终 JSON 顶层的 `operation_id` / `operation`。ctx 已绑定时移除这两个顶层同名调用属性/预绑定属性，包括空名 group 展开的字段和顶层同名 named group；保留普通 named group 内的同名字段。无 ctx 绑定时保持现有属性行为。非法 ctx ID 被省略时仍不恢复被遮蔽的旧 ID。

## Task 1：上下文值 API

**Files:** Create `operation_context.go`、`operation_context_test.go`（路径均相对 `internal/logging/`）。

**Interfaces:** 消费标准库 context；产出上述 `OperationMetadata`、`WithOperation`、`OperationFromContext`。

- [x] **1. 建立可编译的空壳，以便 Red 来自行为断言。** 在 `operation_context.go` 写入 API 注释、类型和下面的临时方法体。空壳不作为最终实现。

```go
package logging

import "context"

// OperationMetadata identifies a logical operation in diagnostic logs.
type OperationMetadata struct { ID, Name string }

// WithOperation attaches a copy of operation to a non-nil context.
func WithOperation(ctx context.Context, operation OperationMetadata) context.Context {
    return ctx
}

// OperationFromContext returns operation metadata explicitly bound to ctx.
func OperationFromContext(ctx context.Context) (OperationMetadata, bool) {
    return OperationMetadata{}, false
}
```

- [x] **2. 添加行为测试。** 文件使用 `package logging`，导入 `context`、`testing`。

```go
func TestOperationContext_ValueAndInheritance(t *testing.T) {
    input := OperationMetadata{ID: "op-a", Name: "settings.update"}
    parent := WithOperation(context.Background(), input)
    input.ID = "changed"
    child, cancel := context.WithCancel(parent)
    cancel()
    got, ok := OperationFromContext(child)
    if !ok || got.ID != "op-a" || got.Name != "settings.update" {
        t.Fatal("bound metadata must survive input mutation and cancellation")
    }
    got.ID = "edited-copy"
    original, _ := OperationFromContext(parent)
    if original.ID != "op-a" || child.Err() != context.Canceled {
        t.Fatal("metadata retrieval must not mutate parent or cancellation")
    }
    replacement := WithOperation(parent, OperationMetadata{ID: "op-b"})
    second, _ := OperationFromContext(replacement)
    original, _ = OperationFromContext(parent)
    if second.ID != "op-b" || original.ID != "op-a" {
        t.Fatal("replacement must be local to the derived context")
    }
}

func TestOperationContext_AbsentAndExplicitEmpty(t *testing.T) {
    for _, ctx := range []context.Context{nil, context.Background()} {
        if got, ok := OperationFromContext(ctx); ok || got != (OperationMetadata{}) {
            t.Fatal("unbound context must have no operation")
        }
    }
    parent := WithOperation(context.Background(), OperationMetadata{ID: "parent"})
    empty := WithOperation(parent, OperationMetadata{})
    if got, ok := OperationFromContext(empty); !ok || got != (OperationMetadata{}) {
        t.Fatal("explicit empty binding must mask the parent")
    }
    raw := "not valid/" + string([]byte{0})
    ctx := WithOperation(parent, OperationMetadata{ID: raw})
    if got, _ := OperationFromContext(ctx); got.ID != raw {
        t.Fatal("context binding must not rewrite input")
    }
}
```

- [x] **3. Red：** `go test ./internal/logging -run '^TestOperationContext_' -count=1`。预期因没有绑定/继承元数据而失败，不能以编译错误作为 Red。
- [x] **4. Green：** 增加私有 key，并替换两个方法体：

```go
type operationContextKey struct{}

func WithOperation(ctx context.Context, operation OperationMetadata) context.Context {
    return context.WithValue(ctx, operationContextKey{}, operation)
}

func OperationFromContext(ctx context.Context) (OperationMetadata, bool) {
    if ctx == nil { return OperationMetadata{}, false }
    operation, ok := ctx.Value(operationContextKey{}).(OperationMetadata)
    return operation, ok
}
```

- [x] **5. 验证：** 重跑步骤 3，再执行 `gofmt -w internal/logging/operation_context.go internal/logging/operation_context_test.go`。仅在测试通过时进入 Task 2；不生成 ID、不改任何调用方。

## Task 2：handler 提取、脱敏和保留键处理

**Files:** Create `operation_attrs.go`、`operation_handler_test.go`；Modify `handler.go` 的 Handle；保留其他方法的既有职责。

**Interfaces:** 消费 Task 1 的 API；新增包私有 `validOperationLogID(string) bool`、`operationKey(string) bool`、`withoutOperationAttrs([]slog.Attr) []slog.Attr`；不新增公共 handler 构造参数。

- [x] **1. 添加输出解码 helper 与回归测试。** 测试文件导入 `bytes`、`context`、`encoding/json`、`io`、`log/slog`、`strings`、`testing`。helper 逐个读取顶层 key，不能仅将整个 JSON 解码为 map。

```go
func decodeOperationRecord(t *testing.T, data string) map[string]json.RawMessage {
    t.Helper()
    d := json.NewDecoder(strings.NewReader(data))
    token, err := d.Token()
    if err != nil || token != json.Delim('{') { t.Fatal("expected JSON object") }
    out := make(map[string]json.RawMessage)
    for d.More() {
        token, err = d.Token()
        if err != nil { t.Fatal("invalid JSON key") }
        key, ok := token.(string)
        if !ok { t.Fatal("expected string key") }
        if _, exists := out[key]; exists { t.Fatalf("duplicate top-level key %q", key) }
        var value json.RawMessage
        if err := d.Decode(&value); err != nil { t.Fatal("invalid JSON value") }
        out[key] = value
    }
    token, err = d.Token()
    if err != nil || token != json.Delim('}') { t.Fatal("unterminated JSON object") }
    if _, err := d.Token(); err != io.EOF { t.Fatal("unexpected trailing JSON") }
    return out
}

func TestOperationHandler_ContextWinsAtRoot(t *testing.T) {
    for _, group := range []string{"", "request", "token", "operation_id", "operation"} {
        t.Run("group="+group, func(t *testing.T) {
            var buf bytes.Buffer
            base := slog.New(NewJSONHandler(&buf, new(slog.LevelVar), "daemon", NewRedactor()))
            logger := base.With("operation_id", "stale", "operation", "stale-name", "keep", true)
            if group != "" { logger = logger.WithGroup(group) }
            ctx := WithOperation(context.Background(), OperationMetadata{ID: "op-a", Name: "settings.update"})
            logger.InfoContext(ctx, "done", "operation_id", "record-old", "operation", "record-name", "value", 7)
            out := decodeOperationRecord(t, buf.String())
            if string(out["operation_id"]) != `"op-a"` || string(out["operation"]) != `"settings.update"` {
                t.Fatal("ctx fields must be authoritative at root")
            }
            if string(out["component"]) != `"daemon"` || string(out["keep"]) != "true" {
                t.Fatal("existing root fields must survive")
            }
            if group == "request" {
                var nested map[string]json.RawMessage
                if err := json.Unmarshal(out[group], &nested); err != nil { t.Fatal(err) }
                if string(nested["operation_id"]) != `"record-old"` || string(nested["value"]) != "7" {
                    t.Fatal("ordinary nested fields must survive")
                }
            }
        })
    }
}

func TestOperationHandler_IDFilteringAndRedaction(t *testing.T) {
    tests := []struct { name, id, want string }{
        {"ordinary", "op-1_A.b:c", `"op-1_A.b:c"`},
        {"max", strings.Repeat("a", 128), `"`+strings.Repeat("a", 128)+`"`},
        {"empty", "", ""}, {"long", strings.Repeat("a", 129), ""},
        {"space", "bad id", ""}, {"unicode", "操作", ""},
        {"control", "bad\nvalue", ""}, {"url", "https://invalid.test/x", ""},
        {"secret", "registered-secret", `"***"`},
        {"hex-secret", strings.Repeat("b", 64), `"***"`},
    }
    for _, test := range tests {
        t.Run(test.name, func(t *testing.T) {
            var buf bytes.Buffer
            r := NewRedactor("registered-secret")
            logger := slog.New(NewJSONHandler(&buf, new(slog.LevelVar), "daemon", r)).With("operation_id", "stale")
            ctx := WithOperation(context.Background(), OperationMetadata{ID: test.id, Name: "registered-secret"})
            logger.InfoContext(ctx, "done")
            out := decodeOperationRecord(t, buf.String())
            if string(out["operation_id"]) != test.want { t.Fatal("unexpected ID output") }
            if string(out["operation"]) != `"***"` { t.Fatal("operation must be redacted") }
            if got, _ := OperationFromContext(ctx); got.ID != test.id { t.Fatal("input changed") }
        })
    }
}

func TestOperationHandler_NoContextCompatibility(t *testing.T) {
    var buf bytes.Buffer
    logger := slog.New(NewJSONHandler(&buf, new(slog.LevelVar), "daemon", NewRedactor())).With("operation_id", "legacy")
    logger.Info("legacy")
    out := decodeOperationRecord(t, buf.String())
    if string(out["operation_id"]) != `"legacy"` { t.Fatal("legacy With behavior changed") }
    buf.Reset()
    logger.InfoContext(WithOperation(context.Background(), OperationMetadata{}), "empty")
    out = decodeOperationRecord(t, buf.String())
    if _, exists := out["operation_id"]; exists { t.Fatal("empty binding restored stale ID") }
    if _, exists := out["operation"]; exists { t.Fatal("empty binding emitted a name") }
}
```

- [x] **2. Red：** `go test ./internal/logging -run '^TestOperationHandler_' -count=1`。已有 handler 不提取 ctx，故上下文优先级及脱敏断言应失败；Task 1 必须仍通过。
- [x] **3. 新建 `operation_attrs.go`，实现只作用于日志输出的辅助函数。** ID 的字节检查在 redactor 前；redactor 结果不再次套 ID 字符限制。

```go
package logging

import "log/slog"

func validOperationLogID(id string) bool {
    if len(id) == 0 || len(id) > 128 { return false }
    for i := 0; i < len(id); i++ {
        c := id[i]
        if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
            (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.' || c == ':' {
            continue
        }
        return false
    }
    return true
}

func operationKey(key string) bool { return key == "operation_id" || key == "operation" }

// withoutOperationAttrs removes only attributes occupying reserved root keys.
// Empty-name groups are flattened by slog; named groups keep their children.
func withoutOperationAttrs(attrs []slog.Attr) []slog.Attr {
    out := make([]slog.Attr, 0, len(attrs))
    for _, attr := range attrs {
        if operationKey(attr.Key) { continue }
        if attr.Key == "" {
            value := attr.Value.Resolve()
            if value.Kind() == slog.KindGroup {
                children := withoutOperationAttrs(value.Group())
                if len(children) == 0 { continue }
                attr = slog.Attr{Value: slog.GroupValue(children...)}
            }
        }
        out = append(out, attr)
    }
    return out
}
```

- [x] **4. 修改 `handler.go` 的 Handle。** 完整方法如下；其余方法和已有 `handlerOp` 保持原样。`WithAttrs` 现有脱敏逻辑继续生效；ctx 元数据不进入共享 handler 字段。

```go
func (h *redactingHandler) Handle(ctx context.Context, record slog.Record) error {
    operation, bound := OperationFromContext(ctx)
    msg := record.Message
    if h.redactor != nil { msg = h.redactor.String(msg) }
    clean := slog.NewRecord(record.Time, record.Level, msg, record.PC)
    component := h.component
    attrs := make([]slog.Attr, 0, record.NumAttrs())
    record.Attrs(func(attr slog.Attr) bool {
        attr, component = h.extractTopLevelComponent(attr, component)
        if attr = h.cleanAttr(attr); !attr.Equal(slog.Attr{}) { attrs = append(attrs, attr) }
        return true
    })
    hiddenRootGroup := bound && len(h.groups) > 0 && operationKey(h.groups[0])
    if hiddenRootGroup {
        attrs = nil
    } else if bound && len(h.groups) == 0 {
        attrs = withoutOperationAttrs(attrs)
    }
    clean.AddAttrs(attrs...)

    root := []slog.Attr{slog.String("component", component)}
    if bound {
        if validOperationLogID(operation.ID) {
            root = append(root, slog.String("operation_id", operation.ID))
        }
        if operation.Name != "" {
            root = append(root, slog.String("operation", operation.Name))
        }
    }
    // Context metadata belongs to the root, not to h.groups.
    if h.redactor != nil {
        for i := 1; i < len(root); i++ { root[i] = h.redactor.ReplaceAttr(nil, root[i]) }
    }
    next := h.next.WithAttrs(root)
    grouped := false
    for _, op := range h.ops {
        if op.group != "" {
            if bound && !grouped && operationKey(op.group) { break }
            next = next.WithGroup(op.group)
            grouped = true
        } else {
            opAttrs := op.attrs
            if bound && !grouped { opAttrs = withoutOperationAttrs(opAttrs) }
            next = next.WithAttrs(opAttrs)
        }
    }
    return next.Handle(ctx, clean)
}
```

顶层名为 `operation_id`/`operation` 的 WithGroup 整支不能与 ctx 字段共存：停止重放该分组及其后续派生属性，并省略该分组中的 record 属性；此前根属性、time/level/msg/component 和 ctx 字段保留。普通 `request.operation_id` 不与顶层冲突，保持原内容与既有脱敏。ctx 字段使用根层级的 redactor，不继承 `WithGroup("token")` 等敏感子分组的祖先规则；该分组中的原有业务属性继续按原规则遮蔽。

- [x] **5. Green 与兼容回归：**

```bash
gofmt -w internal/logging/operation_attrs.go internal/logging/handler.go internal/logging/operation_handler_test.go
go test ./internal/logging -run '^(TestOperationContext_|TestOperationHandler_|TestJSONHandler_)' -count=1
```

## Task 3：派生属性、共享状态与并发验收

**Files:** 仅追加 `operation_handler_test.go`。若测试证明 Task 2 不满足同一契约，仅在既定三个生产文件中最小修正；不修改 redactor、rotator、业务调用点。

**Interfaces:** 消费 Task 1 API、Task 2 handler 和 `decodeOperationRecord`；不增加生产 API。

- [x] **1. 增加下列契约测试。** 在现有测试 import 中补充 `fmt`、`reflect`、`sync`、`time`。这些是前两任务的强化验证，若首次已通过则记录为覆盖补充，不伪称 Red；只有出现正确行为失败才进入最小修正。

```go
func TestOperationHandler_InlineGroupsAndRecordUnchanged(t *testing.T) {
    var buf bytes.Buffer
    handler := NewJSONHandler(&buf, new(slog.LevelVar), "daemon", NewRedactor())
    handler = handler.WithAttrs([]slog.Attr{slog.Group("",
        slog.String("operation_id", "stale"), slog.String("keep", "yes"))})
    record := slog.NewRecord(time.Now(), slog.LevelInfo, "test", 0)
    record.AddAttrs(slog.Group("", slog.Group("", slog.String("operation_id", "spoof")),
        slog.String("component", "daemon.settings"), slog.Int("keep_record", 1)))
    for i := 0; i < 10; i++ { record.AddAttrs(slog.Int(fmt.Sprintf("n%d", i), i)) }
    original := make([]slog.Attr, 0, record.NumAttrs())
    record.Attrs(func(a slog.Attr) bool { original = append(original, a); return true })
    ctx := WithOperation(context.Background(), OperationMetadata{ID: "op-a"})
    if err := handler.Handle(ctx, record); err != nil { t.Fatal(err) }
    out := decodeOperationRecord(t, buf.String())
    if string(out["operation_id"]) != `"op-a"` || string(out["keep"]) != `"yes"` ||
        string(out["component"]) != `"daemon.settings"` || string(out["keep_record"]) != "1" {
        t.Fatal("inline group filtering damaged fields")
    }
    after := make([]slog.Attr, 0, record.NumAttrs())
    record.Attrs(func(a slog.Attr) bool { after = append(after, a); return true })
    if !reflect.DeepEqual(original, after) { t.Fatal("caller record mutated") }
    buf.Reset()
    if err := handler.Handle(context.Background(), record); err != nil { t.Fatal(err) }
    if !strings.Contains(buf.String(), "spoof") { t.Fatal("operation handling mutated reusable attributes") }
}

func TestOperationHandler_DerivedLoggerAndDynamicSecrets(t *testing.T) {
    var buf bytes.Buffer
    r := NewRedactor()
    logger := slog.New(NewJSONHandler(&buf, new(slog.LevelVar), "daemon", r))
    derived := logger.With("keep", true).WithGroup("request").With("stage", "send").WithGroup("detail")
    r.ReplaceExact([]string{"later-secret"})
    ctx := WithOperation(context.Background(), OperationMetadata{ID: "later-secret", Name: "settings.update"})
    derived.InfoContext(ctx, "done", "password", "hidden-pass", "value", 1)
    out := decodeOperationRecord(t, buf.String())
    if string(out["operation_id"]) != `"***"` { t.Fatal("derived handler missed current redaction rules") }
    if strings.Contains(buf.String(), "later-secret") || strings.Contains(buf.String(), "hidden-pass") {
        t.Fatal("sensitive data leaked")
    }
    var request map[string]json.RawMessage
    if err := json.Unmarshal(out["request"], &request); err != nil { t.Fatal(err) }
    if string(request["stage"]) != `"send"` { t.Fatal("derived attributes lost") }
    var detail map[string]json.RawMessage
    if err := json.Unmarshal(request["detail"], &detail); err != nil { t.Fatal(err) }
    if string(detail["value"]) != "1" { t.Fatal("nested data lost") }
    buf.Reset()
    logger.Info("no ctx")
    out = decodeOperationRecord(t, buf.String())
    if _, exists := out["operation_id"]; exists { t.Fatal("previous operation leaked to base logger") }
}

func TestOperationHandler_CancellationAndLevel(t *testing.T) {
    var buf bytes.Buffer
    level := new(slog.LevelVar)
    level.Set(slog.LevelWarn)
    logger := slog.New(NewJSONHandler(&buf, level, "daemon", NewRedactor()))
    ctx, cancel := context.WithCancel(WithOperation(context.Background(), OperationMetadata{ID: "op-cancel"}))
    cancel()
    logger.InfoContext(ctx, "filtered")
    if buf.Len() != 0 { t.Fatal("operation bypassed level filtering") }
    logger.ErrorContext(ctx, "must survive cancellation")
    out := decodeOperationRecord(t, buf.String())
    if string(out["operation_id"]) != `"op-cancel"` { t.Fatal("canceled ctx lost diagnostic") }
}

func TestOperationHandler_ConcurrentIsolation(t *testing.T) {
    var buf bytes.Buffer
    // All derived loggers share this JSON handler's synchronized writer.
    logger := slog.New(NewJSONHandler(&buf, new(slog.LevelVar), "daemon", NewRedactor()))
    const count = 32
    start := make(chan struct{})
    var wg sync.WaitGroup
    for i := 0; i < count; i++ {
        wg.Add(1)
        go func(i int) {
            defer wg.Done()
            id := fmt.Sprintf("op-%d", i)
            ctx := WithOperation(context.Background(), OperationMetadata{ID: id, Name: "settings.update"})
            child := logger.With("operation_id", "stale").WithGroup("request")
            <-start
            child.InfoContext(ctx, "completed", "expected", id)
        }(i)
    }
    close(start)
    wg.Wait()
    lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
    if len(lines) != count { t.Fatal("missing or interleaved log records") }
    seen := make(map[string]bool)
    for _, line := range lines {
        out := decodeOperationRecord(t, line)
        var request map[string]json.RawMessage
        if err := json.Unmarshal(out["request"], &request); err != nil { t.Fatal(err) }
        key := string(out["operation_id"])
        if key != string(request["expected"]) || seen[key] { t.Fatal("operation IDs crossed or duplicated") }
        seen[key] = true
    }
}
```

- [x] **2. 运行新增测试：** `go test ./internal/logging -run '^TestOperationHandler_' -count=1`。若失败，保存断言与失败原因后最小修正；本任务不改变 ID 字符规则或顶层/嵌套字段的既定含义。
- [x] **3. 并发验证：** `go test -race ./internal/logging -run '^(TestOperationContext_|TestOperationHandler_|TestJSONHandler_)' -count=1`。无固定 sleep、无生产后台 worker；所有测试 goroutine 在返回前结束。
- [x] **4. 检查 diff：** 确认 metadata 未存入 handler 共享字段，record 和预绑定切片未原地修改，所有元数据在输出前经过 redactor。

## Task 4：阶段验收与交付检查

**Files:** 不新增生产文件。只执行验证，并在交付说明中记录实际结果。

**Interfaces:** Task 1–3 的已完成实现；不依赖 Phase 2 代码。

- [x] **1. 运行完整包检查：**

```bash
go test ./internal/logging -count=1
go test -race ./internal/logging -count=1
go vet ./internal/logging
gofmt -l internal/logging/operation_context.go internal/logging/operation_context_test.go internal/logging/operation_attrs.go internal/logging/handler.go internal/logging/operation_handler_test.go
```

任何失败需要说明具体原因，不能通过排除失败测试或顺手修复导出/权限来让本阶段“通过”。已知基线失败与新增失败分开报告；阶段验收必须明确未通过的完整命令。

- [x] **2. 验证消费者编译保持兼容。** 本阶段没有业务行为接入，使用六目标 CGO-free 构建验证共享 handler 在产品中的编译；产物在临时目录，不进入仓库。

```bash
build_dir=$(mktemp -d)
trap 'rm -rf "$build_dir"' EXIT
for target_os in linux windows darwin; do
    for target_arch in amd64 arm64; do
        CGO_ENABLED=0 GOOS="$target_os" GOARCH="$target_arch" go build -buildvcs=false -o "$build_dir/mihari-$target_os-$target_arch" ./cmd/mihari || exit 1
    done
done
```

这是未来实施验证命令；写计划本身不执行构建或修改系统环境。race 使用本机支持的工具链与平台，不与 CGO-free 构建混为一谈。

- [x] **3. 检查范围和格式：**

```bash
git -c safe.directory=/home/kinema/dev/mihari/.worktrees/issue-216 diff --check
git -c safe.directory=/home/kinema/dev/mihari/.worktrees/issue-216 diff --stat
git -c safe.directory=/home/kinema/dev/mihari/.worktrees/issue-216 status --short
```

除了文件清单及已存在的设计/本计划，不能有新变更；不改 CHANGELOG、go.mod/go.sum、公开协议或 CLI/TUI/runtime/daemon 代码。不删除用户既有改动。

- [x] **4. 交付声明：** 明确 Phase 1 只提供 logger 能力，没有全链路业务日志、没有修复/关闭 #216 或 #197。列出实际执行且通过的检查、失败/未验证项。未经用户明确授权不 commit/push/PR。

## 设计覆盖与审核规则

| Phase 1 要求 | 对应任务/测试 |
| --- | --- |
| ctx 元数据、继承、替换、输入不变 | Task 1 两个测试 |
| 顶层输出、With/WithGroup/保留键冲突 | Task 2 ContextWinsAtRoot；Task 3 InlineGroups |
| 非法/超长 ID 仅省略日志字段，不改输入 | Task 1 原值断言；Task 2 IDFilteringAndRedaction |
| 脱敏、派生 logger 当前规则 | Task 2 IDFilteringAndRedaction；Task 3 DerivedLoggerAndDynamicSecrets |
| 无 ctx 兼容、显式空值遮蔽 | Task 2 NoContextCompatibility |
| 取消 ctx、级别过滤 | Task 3 CancellationAndLevel |
| record 不变、并发不串 ID | Task 3 InlineGroupsAndRecordUnchanged、ConcurrentIsolation + race |
| 无跨模块/协议/依赖/写入权限变更 | Task 4 范围检查与六目标构建 |

本计划审核只检查与已通过设计的 Phase 1 一致性、可执行性和必要测试，不增补 Phase 2–4 功能。subagent 反馈先核实：有范围内必需修正则更新本计划并复审；无必需问题则以当前内容的校验值确认 PASS。若出现必须扩大 scope 的 P0/P1，先向用户汇报，不能在审核循环中自行实施扩展。

## 实施验收记录（2026-09-09）

Task 1–3 均通过独立 subagent 的规格与代码质量审核，无必改项。上下文 API 与 handler 的新增行为有断言失败后的 Red/Green 证据；派生/并发补充测试首次即通过，未虚称 Red。

Go 1.26.5 下完整 logging 测试、完整 logging race、vet、gofmt 与 diff 检查通过；linux/windows/darwin 的 amd64/arm64 六目标 CGO_ENABLED=0 构建通过。完整包测试使用既有普通开发用户；root 会触发现有私有目录保护，未为测试更改生产权限规则。

本阶段只交付 logger 操作上下文能力，尚不代表 #216 或 #197 已完成。用户已明确授权各阶段完成后 commit、最终 push/PR，因此执行阶段提交，不受原计划“未经授权不提交”条件限制。
