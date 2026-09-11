# TUI/CLI 测速对齐内核 test URL

日期：2026-09-10
状态：已定稿，待用户确认后写实施计划
目标 issue：[#210](https://github.com/mihari-proxy/mihari/issues/210)（根因来自 [#204](https://github.com/mihari-proxy/mihari/issues/204)）
目标分支：`feat/210-delay-test-url`
工作目录：`.worktrees/feat-210-delay-test-url`
基线：`origin/dev` @ `f619cf4`

## 1. 背景

用户在 TUI Proxies 页 `Ctrl+T` / `t` 测速全部 Timeout，同一套 mihomo 与同一批节点在 zashboard 正常。根因不是 TUN 或节点坏了，而是 Mihari 测速没用内核已经加载的测试 URL。

用户 YAML（#204）里 url-test 组与 provider health-check 都是 `https://cp.cloudflare.com/generate_204`。mihomo 启动后这份值出现在 `GET /proxies` 的 `testUrl` 上。zashboard 读的是这条内核 API，不读磁盘 YAML。

Mihari 走另一条投影：

1. daemon 同样请求 mihomo `GET /proxies`，JSON 含 `testUrl`。
2. `internal/mihomo/types.go` 的 `Proxy` 没有该字段，`json.Unmarshal` 静默丢掉。
3. `/v1/proxies` 的 `protocol.ProxyGroup` 也没有 `test_url`。
4. TUI（`internal/tui/pages/proxies/model.go`）与 CLI（`internal/cli/proxy.go`）写死 `https://www.gstatic.com/generate_204` + 5000ms。
5. `DelayTestRequest.url` 必填，空 URL 直接 `invalid_argument`，daemon 不会回落到内核。

这与「Generate 只覆盖极少托管字段、保留订阅其余配置」不冲突：YAML 里的 `url:` 仍在，内核也在用。丢掉发生在控制面**读路径**的窄 DTO 投影，不是写配置时删除。

现有 TUI 还会把失败放大成「全部 Timeout」：

- 任意错误一律显示 `Timeout`（#80 已登记为非目标，留给本次）。
- `Ctrl+T` 对所有组 `Nodes` 里出现过的名字一次 `tea.Batch` 打出，无并发上限，含嵌套组名与 `DIRECT`。每个命令立刻启动 10s context；排队中的请求会先耗尽自己的 deadline。
- 控制客户端与 daemon 的 mihomo HTTP client 默认都是 10s，单次业务超时是 5s。无上限并发时，URL 即使改对了仍可能集体撞上传输超时。

CLI `mihari proxy test GROUP` 走组接口 `POST /v1/proxy-groups/{name}/delay-test`，默认同样是 gstatic。Web 网关对 zashboard 的 `GET /proxies|group/.../delay` 已透传内核 URL，本次不改。

## 2. 目标

TUI/CLI 连通性测试对齐 zashboard 实际使用的内核 `testUrl`，且只走 daemon + 版本化本地协议。

- `/v1/proxies` 下发组的 `test_url`。
- delay-test 允许省略 URL 与超时；daemon 按内核值回落，再没有才用明确的 Mihari 默认。
- TUI 不再写死 gstatic；`Ctrl+T` 限流；不把组名当叶子测；Timeout / 上游失败 / 无效参数分开显示。
- CLI 默认与 TUI 共用 daemon 回落；`--url` / `--timeout` 仍可覆盖。
- 协议加字段，不升 `/v1` 版本。旧客户端继续传 URL 时行为不变。

## 3. 非目标

- TUI/CLI/daemon 读取磁盘上的订阅 YAML 或 runtime 配置文件来取测速 URL。
- 改变 Web 网关对 zashboard delay 的透传（`ActionMutateDelayTest` / reverse proxy）。
- zashboard 那种 Dashboard URL / Core URL 双模式开关。
- 在 Mihari settings 再增加独立测速 URL。
- 把 TUI `Ctrl+T` 改成整组 `DelayGroup`；本次仍按叶子 `DelayProxy`。
- 修改控制客户端或 mihomo HTTP client 的 10s `Timeout`。
- 从 `mihomo.Proxy` 删除未使用的 `History` 字段。
- 给 `ProxyNode` 增加 `test_url`。
- 修改 `CHANGELOG.md`。
- 真实订阅、真实 mihomo、系统服务或 testenv 验证。

## 4. 方案比较

### 4.1 采用：daemon 拥有回落，DTO 只读下发 `test_url`

TUI/CLI 默认省略 URL。daemon 在调用 mihomo delay 之前解析出具体 URL。`GET /v1/proxies` 仍带组 `test_url`，便于 JSON 检查与显式覆盖，但不作为 TUI 的唯一来源。

客户端不能再各自维护一份与内核不一致的默认值。叶子自身往往没有 `testUrl`，回落必须能走到所在组。

### 4.2 不采用：只改 TUI/CLI 常量到 Cloudflare

治标。用户 YAML 仍可能是别的 URL；CLI 与 TUI 继续分叉；控制面仍强制客户端自带 URL。

### 4.3 不采用：只在 DTO 下发 `test_url`，TUI 自己带上，daemon 仍强制 URL

TUI 能修好当前页，但 CLI 默认、省略 URL 的调用、以及叶子没有 `testUrl` 时仍要客户端猜。回落所有权散在表现层。

### 4.4 不采用：Dashboard / Core 双模式 + settings 测速 URL

超出 #210。内核组上已有 `url:` 才是与 zashboard Core 模式对齐的最小修复。

## 5. 协议

不加新端点，不升 `schema`。`DisallowUnknownFields` 继续生效；本次只增加已建模字段。

### 5.1 `mihomo.Proxy`

```go
type Proxy struct {
    Name    string            `json:"name"`
    Type    string            `json:"type"`
    Now     string            `json:"now,omitempty"`
    All     []string          `json:"all,omitempty"`
    History []json.RawMessage `json:"history,omitempty"`
    UDP     bool              `json:"udp,omitempty"`
    XUDP    bool              `json:"xudp,omitempty"`
    TestURL string            `json:"testUrl,omitempty"`
}
```

`testUrl` 是 mihomo controller 的既有驼峰字段，适配层保持原名。`History` 原样保留，本次不映射到 `/v1`。

### 5.2 `protocol.ProxyGroup`

```go
type ProxyGroup struct {
    Name    string      `json:"name"`
    Type    string      `json:"type"`
    Now     string      `json:"now,omitempty"`
    All     []string    `json:"all,omitempty"`
    Nodes   []ProxyNode `json:"nodes,omitempty"`
    TestURL string      `json:"test_url,omitempty"`
}
```

`test_url` 来自该组在内核对象上的 `testUrl`。空则 omit。`ProxyNode` 不加此字段；叶子测速 URL 由 daemon 回落解析。

旧 JSON 无 `test_url` 仍能解码。`orderedProxyGroups` 在构造 group DTO 时抄入 `proxy.TestURL`。

### 5.3 `protocol.DelayTestRequest`

```go
type DelayTestRequest struct {
    URL                 string `json:"url,omitempty"`
    TimeoutMilliseconds int    `json:"timeout_ms,omitempty"`
}
```

语义：

| 字段 | 省略或零值 | 显式非零 / 非空 |
| --- | --- | --- |
| `url` | daemon 回落 | 原样使用，不再查内核 |
| `timeout_ms` | 5000 | 使用该值 |

校验：

- `timeout_ms < 0` 或 `timeout_ms > 60_000` → `invalid_argument`。
- `timeout_ms == 0` 合法，表示默认 5000。
- `url` 为空合法。
- body 为 `{}` 合法（`decodeControlJSON` 仍要求恰好一个 JSON 对象）。
- 非空 `url` 不做格式深校验；非法 URL 由 mihomo 失败映射为既有 `upstream_failure` / `invalid_argument`。

旧客户端继续发送 gstatic + 5000 时，daemon 使用客户端值，行为与现在相同。

### 5.4 `DelayResult`

不变：`{"schema":"mihari/v1","delays":{...}}`。单节点失败仍返回错误 envelope，不把 URL 写入 `details`。

## 6. daemon 回落

解析发生在 control server 的 `delayTest` / `delayProxy`，在调用 `RuntimeAPI.DelayGroup` / `DelayProxy` 之前。`Manager` 与 mihomo 适配器仍接收已经解析好的非空 URL 和正超时。mihomo 的 delay query 继续带 `url` 与 `timeout`，不依赖内核「空 url 表示用组配置」的行为。

默认值只存在于 server 包内，不放进 `protocol`，也不再出现在 TUI/CLI 源码：

```go
const (
    defaultDelayTestURL    = "https://www.gstatic.com/generate_204"
    defaultDelayTimeoutMS  = 5000
    maxDelayTimeoutMS      = 60_000
)
```

默认 URL 对齐 zashboard Dashboard 缺省，**不**改成 Cloudflare。用户 Cloudflare 地址来自内核 `testUrl`。

### 6.1 超时

1. `timeout_ms > 0` 且 `<= 60_000` → 用请求值。
2. `timeout_ms == 0` → `5000`。
3. 其余 → `invalid_argument`（文案保持稳定，不回显数值以外的动态 URL）。

本次不从内核 JSON 读取 timeout 字段。

### 6.2 URL

请求 `url` 非空：直接使用，不调用 `Proxies()`。

请求 `url` 为空：

1. `runtime.Proxies(ctx)` 取当前内核快照。失败则按既有 `writeControlError` 返回，不改用默认 URL 掩盖内核不可用。
2. 若 `proxies[name].TestURL` 非空，使用它。`name` 对组接口是 path 里的组名，对节点接口是节点名。
3. 否则按 `orderedProxyGroups` 的顺序（`GLOBAL.All` 优先，再补漏组）取第一个 `All` 包含该 `name` 且 `TestURL` 非空的组。
4. 仍没有 → `defaultDelayTestURL`。

组对象自己的 `testUrl` 只在步骤 2 命中。步骤 3 只为叶子（或自身 `TestURL` 为空的名字）找父组，不把「组名等于 name」再搜一遍。

同一叶子出现在多个组且 URL 不同时，采用上述顺序中第一个非空组 URL。不把多个 URL 合成，不报错。

解析结果只用于本次 mihomo 调用，不写 settings、不改 YAML、不缓存跨请求。

### 6.3 调用后

`DelayGroup` / `DelayProxy` 失败路径不变。错误与日志不得包含解析出的 URL、订阅 URL 或 secret。

## 7. TUI

文件：`internal/tui/pages/proxies/model.go` 及同包测试、`internal/tui/ui/strings.go`。

### 7.1 请求

删除 `delayTestURL` 常量。真正发起时发送 `DelayTestRequest{}`（URL 与 timeout 均省略）。单次请求仍用 `context.WithTimeout(..., 10*time.Second)`，但 **context 在该名字进入 `inFlight`、即将调用 `DelayProxy` 时才创建**，不能在入队时启动。

`t` 与 `Ctrl+T` 共用同一套 `queue` / `inFlight` / `delayTestGen` 与启动规则，但 **`t` 不得整表替换 `Ctrl+T` 的 queue**：

- 焦点不是可测叶子（组头或组名卡片）→ 仍 no-op。
- 该名已在 `inFlight` → no-op，不并行第二次。
- 否则把该名插到当前 `queue` 队头（若已在队列中则先去掉再插到队头），然后按 §7.3 补槽。不增加 generation，不丢弃其余已排队名字。

### 7.2 谁测、谁跳过

组名集合 = 当前 `m.groups` 里所有 `Name`。

`Ctrl+T` 与 `t`：

- 跳过名字落在组名集合中的节点（嵌套 Selector / URLTest / Fallback / LoadBalance / Relay 等）。
- 不跳过 `DIRECT`、`REJECT` 以及其它非组叶子。
- 焦点在组头（`focus.Node == ""`）时 `t` 仍为 no-op（现有行为）。
- 焦点在组名卡片上按 `t`：no-op，不调用 `DelayProxy`。

去重仍按节点名；跨组共享叶子只测一次。daemon 负责为该叶子选组 URL。

### 7.3 `Ctrl+T` 队列

禁止对全部节点 `tea.Batch` 立刻启动。

- 并发上限：`delayTestConcurrency = 5`。
- 状态：`queue []string`（尚未启动的名字）、`inFlight map[string]uint64`（名字 → 启动时的 generation）、`delayTestGen uint64`。
- `DelayTesting` **只在名字进入 `inFlight`、即将发请求时设置**。写入 `queue` 不得把未启动的节点标成 Testing，否则 unique-leaf 集合变化或队列被替换时，被丢掉的名字会卡在 Testing。
- `Ctrl+T`：`delayTestGen++`，`queue` **整表替换**为当前 unique 叶子（已过滤组名，保持 `m.groups` 遍历顺序），然后立刻调用 `fillSlots`。不取消已在飞的请求，也不把队列里尚未启动的名字标成 Testing。
- 补槽 `fillSlots`：**循环**直到 `len(inFlight)==5` 或 queue 里没有可启动的名字。每次扫描 `queue`，跳过已在 `inFlight` 的名字（留在原位），启动下一个不在飞的名字（移出 queue、写入 inFlight、设 DelayTesting、发请求）。一次 `Ctrl+T` 必须能打出初始 burst（最多 5 个），不能理解成每个按键只启动一个。不得在队头是 in-flight 名字时停住。同一名字不得并行两次 `DelayProxy`。
- `delayResultMsg`：从 `inFlight` 删除该名，把结果写入 `delays`（过期 generation 也写入，避免卡片卡在 Testing）。然后**总是**对当前 `queue` 调 `fillSlots`，不要求 `msg.gen == delayTestGen`。过期结果只释放槽位，不得读已经丢掉的旧队列。
- 因此重复 `Ctrl+T` 会重测尚未启动的叶子；已在飞的叶子返回后若仍在新 `queue` 里，`fillSlots` 再测一次。

spinner 仍用现有 generation-owned `delaySpinCmdIfNeeded`。

### 7.4 错误显示

节点卡片第二行继续显示测速状态，不用页面首行 `lastError` 折叠全部节点（那是选择失败的位置）。

`DelayKind` 增加：

分类顺序（唯一顺序，禁止按 `err.Error()` 子串匹配 `"timeout"`）：

1. `errors.As` 到 `protocol.APIError` 且 `Code == invalid_argument` → `DelayInvalid`。
2. 否则，若 `errors.Is(err, context.DeadlineExceeded)`，或 `errors.As` 到实现了 `Timeout() bool` 且返回 true 的 `net.Error` → `DelayTimeout`。必须穿过 `diagnostics.Wrap`：控制客户端把 10s HTTP/context 超时包成 `APIError{Code: data_failure}` + cause，`errors.As` 到 APIError 会成功，**不能**因此走 Failed。`context.Canceled` 不是 Timeout。
3. 其余（含裸 `upstream_failure`、无 timeout cause 的 `data_failure`）→ `DelayFailed`。内核 5s 探测失败若只以 envelope `upstream_failure` 返回、没有 deadline/`net.Error` cause，显示 Failed。

| Kind | 文案 | 颜色 |
| --- | --- | --- |
| `DelayTimeout` | `ui.TimeoutLabel`（`Timeout`） | `DelayBad` |
| `DelayFailed` | `ui.FailedLabel`（`Failed`） | `DelayBad` |
| `DelayInvalid` | 新常量 `ui.ProxyDelayInvalid = "Invalid"` | `DelayBad` |
| `DelayValue` | `"%d ms"` | 现有阶梯 |

成功路径不变。不把 `APIError.Message`、`Details` 或 URL 渲染进卡片。`TestModel_DelayTimeoutPath` 不得再用 `errors.New("timeout")`；至少覆盖裸 `context.DeadlineExceeded` 以及 `diagnostics.Wrap(APIError{Code: data_failure}, context.DeadlineExceeded)`。

`delayResultMsg` 不实现 shell `Err() error` 账本契约（#80 只给了选择路径；本次不把每次节点测速记进 Recent operations）。

## 8. CLI

`mihari proxy test GROUP`：

- 默认 `--url` 为空、`--timeout` 为 0，请求体等价于省略字段，走 daemon 回落。
- `--url` 非空则覆盖。
- `--timeout` 为正整数则覆盖；0 表示默认。负值由 Cobra/校验变成 `invalid_argument`（与 server 上限 60_000 一致：CLI 在发请求前拒绝 `> 60000` 或 `< 0`，避免把明显非法值打到 daemon；0 放行）。
- 文本输出仍是 `name\tN ms`。JSON 输出仍是 `DelayResult`。
- `proxy groups` 文本行仍是 `name\ttype\tnow`，不打印 `test_url`。`--json` 会因 DTO 加字段而带上 `test_url`，这是向后兼容增量。

TUI 与 CLI 源码都不再出现 gstatic 字符串。

## 9. 安全与日志

- `test_url` 是连通性探测地址，不是订阅 URL，允许出现在 `/v1/proxies` JSON 与 CLI `--json`。
- 错误 envelope、默认 CLI 文本、TUI、slog 不得输出解析后的测速 URL、完整订阅 URL、controller secret。
- 不把 delay-test 升级为 mutation coordinator 事务；它仍是 runtime 只读探测，现有路由不变。

## 10. 测试计划

按包 Red–Green。不访问公网、不读真实用户目录。

### 10.1 `internal/mihomo`

- 含 `testUrl` 的 `GET /proxies` fixture 能解码到 `Proxy.TestURL`。
- 无该字段时 `TestURL` 为空。

### 10.2 `internal/control/protocol`

- `ProxyGroup` 有 `test_url` 时 round-trip；空值 omit。
- 旧 JSON 无 `test_url` 仍能解码。
- `DelayTestRequest` 空结构序列化不含 `url` / `timeout_ms`。
- 显式 URL 与 `timeout_ms` 仍按现有字段名编码。

### 10.3 `internal/control/server`

- `GET /v1/proxies`：组 DTO 带上内核 `testUrl` 映射的 `test_url`。
- delay-test / delay-proxy：
  - 显式 URL 原样传给 fake runtime，不因内核另有 `testUrl` 而改写。
  - 省略 URL：组自身 `testUrl` → 该值。
  - 省略 URL：叶子无 `testUrl`，所在组有 → 组 URL。
  - 省略 URL：多组包含同一叶子且 URL 不同 → `GLOBAL.All` 顺序中第一个非空组 URL。
  - 省略 URL：内核也没有 → `https://www.gstatic.com/generate_204`。
  - 省略 timeout → fake 收到 5000；显式 3500 → 3500；`timeout_ms: 70000` 或负数 → 400 `invalid_argument`。
  - `{}` body 被接受并走默认回落。
  - `Proxies()` 失败时 delay-test 返回对应控制错误，不静默改用默认 URL。
- 错误响应 body 不含测速 URL。

### 10.4 `internal/cli`

- 默认 `proxy test GROUP` 发给 fake 的 `DelayTestRequest` URL 为空、timeout 为 0。
- `--url` / `--timeout` 覆盖反映在 fake 收到的请求上。
- 文本 `proxy groups` 不含 URL 字符串。

### 10.5 `internal/tui/pages/proxies`

- `t` / `Ctrl+T` 发给 fake 的请求 URL 为空。
- `Ctrl+T` 跳过组名，保留 `DIRECT` 与普通叶子；共享叶子只调用一次。
- 并发：超过 5 个叶子时，在已返回结果之前 in-flight 不超过 5（用阻塞 fake 或计数门闩，不用 `time.Sleep` 作为同步原语）。队头是已在飞名字时仍能启动队列里其它名字。
- 写入 queue 不会把未启动节点标成 Testing；Testing 只在进入 inFlight 时出现。
- `t` 不替换 `Ctrl+T` 队列；已在飞的同一名字不会并行第二次。
- 重复 `Ctrl+T` 替换未启动队列；过期结果只释放 in-flight 槽位并写入 `delays`，随后按新队列补槽；同一名字不会并行两次 `DelayProxy`。
- 错误分类：裸 `DeadlineExceeded` 与 `diagnostics.Wrap(data_failure, DeadlineExceeded)` → 视图含 `Timeout`；无 timeout cause 的 `upstream_failure` → `Failed`；`invalid_argument` → `Invalid`；均带 `DelayBad` 样式。
- 既有选择失败、spinner、共享叶子去重测试保持通过。

## 11. 主要改动文件

| 文件 | 变更 |
| --- | --- |
| `internal/mihomo/types.go` | `Proxy.TestURL` |
| `internal/mihomo/client_test.go` | 解码 `testUrl` |
| `internal/control/protocol/runtime.go` | `ProxyGroup.TestURL`；`DelayTestRequest` omitempty |
| `internal/control/protocol/runtime_test.go` | 加字段契约 |
| `internal/control/server/runtime.go` | 映射 `test_url`；delay 回落 |
| `internal/control/server/runtime_test.go` | 回落与映射 |
| `internal/cli/proxy.go` | 默认省略 URL/timeout |
| `internal/cli/runtime_test.go` | 默认请求断言 |
| `internal/tui/pages/proxies/model.go` | 删除常量、队列、跳过组名、错误分类 |
| `internal/tui/pages/proxies/model_test.go` | 对应行为 |
| `internal/tui/ui/strings.go` | `ProxyDelayInvalid` |
| `docs/superpowers/specs/2026-09-10-delay-test-url-design.md` | 本文 |

控制客户端方法签名不变。`RuntimeAPI.DelayGroup/DelayProxy` 签名不变。

## 12. 风险

- 叶子只存在于没有 `testUrl` 的 Selector 时，仍回落到 gstatic。这与 zashboard Dashboard 缺省一致；#204 那种 url-test 组会在步骤 2/3 命中 Cloudflare。
- 不提高 HTTP client 10s 超时。单次 5s 探测加上 5 并发应落在 10s 内；若后续证据表明 daemon→mihomo 排队仍超时，另开 issue，不在本次改 Timeout。
- `GET /v1/proxies` 的 `--json` 会多出 `test_url`。这是加字段，不是破坏性变更。
- 部分测试若对整段 body 做 `strings.Contains(..., "url")` 可能被 `test_url` 误伤。现有这条禁令在 rule-providers 测试，不在 proxies；若碰到再改断言，不改产品语义。

## 13. 验收

完成本次后：

1. 内核组带 `testUrl` 时，TUI/CLI 省略 URL 的测速使用该地址，而不是 gstatic。
2. `Ctrl+T` 不再把组名当叶子打出，且同时在飞不超过 5 个测速。
3. 测速失败不再一律显示 Timeout。
4. Web 网关、YAML Generate、settings schema、`/v1` 版本号均不变。
