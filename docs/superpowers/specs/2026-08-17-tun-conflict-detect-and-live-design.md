# TUN 冲突探测与 Live 校验设计

日期：2026-08-17
状态：待审核
Issue：https://github.com/mihari-proxy/mihari/issues/91
目标分支：`fix/tun-conflict-detect-and-live`
工作目录：`.worktrees/fix-tun-conflict-detect-and-live`

## 1. 背景

Windows 上 Sparkle（以及同类 mihomo GUI）开启 TUN 后，Mihari System 页出现两组错误观感：

1. **认不出别人的 TUN。** TUI 只显示 `N other mihomo`，没有 `Conflict · N TUN`。Enable 不走 `CodeTunConflict`。
2. **Enable 像成功，内核其实没开起来。** 界面为 `Desired On · Live Off · Active · gVisor`。磁盘 `mihari.yaml` 与 `runtime/config.yaml` 都是 `tun.enable: true`，但本实例 `GET /configs` 的 `tun.enable` 是 `false`。

2026-08-17 在 v0.7.3 服务模式（elevated）上的只读现场：

| 对象 | 值 |
|---|---|
| Sparkle 网卡别名 | `mihomo` |
| `GetAdaptersAddresses` 描述 / ifDesc | `Meta Tunnel` |
| 驱动 | `wintun.sys`（描述 `Wintun Userspace Tunnel`） |
| 地址 | `198.18.0.1/30` |
| 默认路由 | `0.0.0.0/0 → 198.18.0.2`（metric 0） |
| Sparkle 内核 | `mihomo-alpha.exe`（用户会话） |
| Mihari 内核 | `mihomo.exe`（Services，听 `127.0.0.1:9090` / `9190`） |
| Mihari 现场 tun | `enable:false`，`device:""`，`stack:gVisor`，`auto-route:true`，`inet4-address:[198.18.0.1/30]` |

`/30` 是点对点假 IP 前缀，不是 TCP 端口：`198.18.0.1` 为本端，`198.18.0.2` 为对端网关。`198.18.0.0/15` 是 Clash/mihomo 常用 Fake-IP 段。两家默认抢同一段地址和同一条默认路由，第二个必然失败。

代码上的三处断裂：

1. `internal/tundetect/tundetect_windows.go` 的 `isWintun` 只认 Description / FriendlyName 含 `wintun`。现代 mihomo/Sparkle 把隧道类型写成 `Meta Tunnel`、别名写成 `mihomo`，两个字段都不含 `wintun`。驱动描述在 `GetAdaptersAddresses` 里根本看不到。
2. `Classify` 对网卡和进程都按切片第一项盲删「自身」。Toolhelp 顺序不稳定：现场曾把 Sparkle 的 `mihomo-alpha.exe` 当 self 删掉，把本实例 `mihomo.exe` 标成 other。
3. `applyTun` 只要 regenerate/reload 或 PATCH 任一 HTTP 成功就返回 nil，不读 live。服务模式下 mihomo stdout/stderr 被丢弃；`buildTunStatus` 的 `lastError` 实参恒为 `""`。用户看不到失败。

既有门控契约仍然成立，只是信号 A 喂不进去：Enable 仅在 `OtherTunInterfaces` 非空且非 force 时拒绝；Disable 永不门控；检测失败不得阻塞 Enable。

## 2. 目标

- Windows 把 Sparkle/mihomo 这种 `Meta Tunnel` + 别名 `mihomo` 的 Wintun 网卡识别为信号 A。
- `Classify` 按本实例内核 PID、以及（当 live 且知道设备名时）网卡名扣除自身，不再按列表位置盲删。
- Enable 在 persist 成功后必须确认 live `tun.enable` 与请求一致；不一致则回滚 Desired，并返回可映射的错误。
- `GET /v1/tun` 在 apply 失败或 Desired/Live 漂移时填写已有的 `last_error`（加法，不改 schema）。
- TUI/CLI 继续用现有分情形文案：信号 A 出现后显示 `Conflict · N TUN` 并走 force 确认。
- 保持 `CGO_ENABLED=0`，不改 `/v1` 既有字段语义，不杀外部进程，不把两个 `auto-route` TUN 做成共存。

## 3. 非目标

- 不给 Mihari 换独立 `device` / `inet4-address` 来和 Sparkle 同时 `auto-route`。同一主机只允许一个 TUN 所有者。
- 不把 `Sparkle.exe` / `clash.exe` 等 GUI 进程名扩进信号 B。信号 B 仍只匹配映像名含 `mihomo` 的内核；GUI 有无 TUN 以网卡为准。
- 不安装或随包分发 `wintun.dll`。本次失败是地址/路由冲突，不是缺驱动。
- 不改 Linux `tun_flags`、macOS `utun` 的枚举规则。Unix 只跟进 Classify 的身份扣除。
- 不新增协议版本，不改 `CodeTunConflict` 的 message，不加 TCP 控制面。
- 不自动关闭别人的 TUN，不在 force 之后改路由表去拆别人的默认路由。
- 不把 mihomo / 系统错误原文、controller secret、订阅 URL 写入 `last_error`、日志或 TUI。

## 4. 方案比较

### 4.1 采用：收紧分类器 + 按身份扣除 + Enable 后核对 live

三处最小修补对准三处断裂，不改「谁拥有 TUN」的产品规则：

- 探测仍只观察，不写入。
- 门控仍只看信号 A。
- 本实例 live 未起来时，别人的网卡全部算 other（当前现场 Desired On / Live Off，正该拦住下一次 Enable）。
- Enable 成功的定义从「HTTP 没报错」改成「live 与请求一致」。

能复用现有 `CodeTunConflict`、force 确认、`TunStatus.LastError`、TUI `tunConflictLabel`。

### 4.2 不采用：只靠换 `device` / `198.18.0.5/30` 做成双 TUN

能避开 IP 冲突，避不开 `0.0.0.0/0`。两个 `auto-route: true` 仍会抢默认路由或成环。用户已确认这种情况只保留一个所有者。

### 4.3 不采用：把所有 `IfType=53` 虚拟网卡都当 TUN

会把 Hyper-V、部分企业 VPN、无关虚拟 NIC 算进信号 A，误拦 Enable。分类必须落在 Wintun/mihomo 家族的名字与隧道类型上。

### 4.4 不采用：检测失败也改成硬失败

与现有设计决策相反：检测 best-effort，失败视为无证据，不得让不透明的枚举错误挡住合法 Enable。本次保持。

## 5. 详细设计

### 5.1 Windows 信号 A：`isWindowsTunAdapter`

`enumerateWintunAdapters` 继续走 `GetAdaptersAddresses`。展示名优先 FriendlyName，空则用 Description；若两者都非空且不忽略大小写相等，格式为 `friendly (desc)`，例如 `mihomo (Meta Tunnel)`。

新增可单测的纯函数（无 syscall）：

```go
func isWindowsTunAdapter(desc, friendly string) bool
```

判定（大小写不敏感）：

1. `desc` 或 `friendly` 包含 `wintun`、`meta tunnel`、`wireguard` 之一；或
2. trim 后的 `friendly` 或 `desc` 等于 `mihomo` 或 `meta`。

不匹配：普通以太网、WLAN、蓝牙、`TAP-Windows`、深信服 VNIC、网易 UU TAP。这些名字不含上述针，也不等于 `mihomo`/`meta`。

`isWintun` 删除或改成对 `isWindowsTunAdapter` 的别名，避免两套规则。

不在本阶段用 SetupAPI 读 `SWD\Wintun` 硬件 ID。名字规则覆盖已证实的 Sparkle/mihomo 现场，且可在无网卡的单元测试里钉死。

Linux / Darwin 枚举函数保持不变。

### 5.2 Detection 与 Classify 按身份扣除

`Detection` 的进程列表改为带 PID 的结构，避免再解析 `"name (pid)"`：

```go
type Process struct {
    Name string
    PID  int
}

type Detection struct {
    TunInterfaces   []string
    MihomoProcesses []Process
}

type Self struct {
    TunActive bool
    TunName   string // live 且 /configs.tun.device 非空时使用；否则空
    CorePID   int    // supervisor 观察到的本实例 mihomo PID；未知则为 0
}

func Classify(d Detection, self Self) *protocol.TunConflict
```

扣除规则：

| 集合 | 条件 | 行为 |
|---|---|---|
| 网卡 | `self.TunActive && self.TunName != ""` | 删除**名字相等**的一项（展示名或 `friendly` 前缀，见下） |
| 网卡 | `self.TunActive && self.TunName == ""` | 与现在一样删除恰好一项（名字未知时只能保守少算自身） |
| 网卡 | `!self.TunActive` | 不删。本实例没占网卡，列表里每张都是别人的 |
| 进程 | `self.CorePID != 0` | 删除 PID 相等的一项 |
| 进程 | `self.CorePID == 0` | 不删。信号 B 不门控，多报一个自身可以接受 |

网卡名比较：先对展示字符串做相等（忽略大小写）；若展示串是 `alias (desc)` 而 `TunName` 只是 alias 或只是 desc，也算命中。`TunName` 来自本实例 `GET /configs` 的 `tun.device`；空字符串表示未知，走「删一项」回退。

协议层 `TunConflict.OtherMihomoProcesses` 仍是 `[]string`，格式保持 `mihomo-alpha.exe (11220)`。不改 JSON 键。

`Manager.detectTunConflict` 组装 `Self`：

- `TunActive` = 现有 `selfTunLiveActive`
- `TunName` = live configs 的 `tun.device`（若有且为 string）
- `CorePID` = `m.store.Load().Core.PID`（core 未上报则为 0）

FakeBackend / 集成测试同步改 `Detection.MihomoProcesses` 类型。

### 5.3 Enable 后核对 live

`mutateTun` 在 `applyTun` 返回 nil 之后：

1. 再读 `liveTunEnable`。
2. 若请求 `enable==true` 且 `!(ok && live)`：把 settings 回滚到 `previousTun` 并 persist（沿用现有失败回滚）；best-effort 再 `applyTun(previousTun)` 把运行配置拉回（忽略这次二次 apply 的 live 核对，避免递归）；返回

   ```text
   code    = upstream_failure
   message = "TUN did not become live after apply"
   ```

3. 若请求 `enable==false`，不因 live 仍为 true 而失败。Disable 的不对称保持：拆自己的 desired 不得被别人的网卡或内核迟滞挡住。
4. `applyTun` 若同时做了 regenerate 与 PATCH，**PATCH 失败必须返回错误**，不得再被 `regenerated==true` 吞掉。Reload 已把文件打进内核后 PATCH 又失败，以 PATCH 为准并走同一套 settings 回滚。

不新增错误码。`CodeUpstreamFailure` 已有 CLI/TUI 映射。`mapTunApplyError` 继续把权限类失败映射为 `CodePermissionDenied`。

Force 只绕过信号 A 门控，**不**绕过 live 核对。Force 之后内核仍然开不起来（地址已被别人占用）时，用户应看到 apply 失败，而不是 Desired On / Live Off。

### 5.4 `last_error` 填充

`TunStatus.LastError` 字段已存在且 `omitempty`，只是生产路径从未赋值。

`Manager` 增加进程内 `tunLastError string`（不落盘，不进 `/v1` 以外的持久化）：

- apply / live 核对失败：写入净化后的 `APIError.Message`。
- Enable/Disable 成功且 live 与请求一致：清空。
- `buildTunStatus` 优先用这次调用传入的错误；空则用 `tunLastError`。
- 若 Desired On 且 live 明确为 false，且 `tunLastError` 仍空（例如进程重启后内存没了），填稳定文案 `live TUN is off`。不得把 configs 原文塞进去。

CLI `mihari tun status` 已打印 `LastError:`。TUI TUN 详情 `tunDetailText` 已有 `Last error` 行。本次只保证 daemon 填值，不改文案键。

### 5.5 产品语义（与用户已确认的处理方式一致）

- 同一时刻只应有一个 TUN + `auto-route` 所有者。
- 别人先占了 `198.18.0.1/30` 和默认路由时：非 force 的 Enable 应在门控处失败（信号 A）；若检测仍漏网，live 核对必须失败并回滚 Desired。
- 用户若继续用 Sparkle：在 Mihari 里 Disable TUN。
- 用户若改用 Mihari：先关 Sparkle TUN，再 Enable。
- 两个内核可以并存（不同 mixed-port），但不能两个都开 TUN。

### 5.6 安全与平台

- 展示名、进程基名、PID 可以进 conflict details；完整路径、命令行、订阅 URL、secret 不行。
- 枚举继续 best-effort：`Detect` 出错则 `Conflict==nil`，Enable 不因此失败。
- Windows 实现留在 `tundetect_windows.go`；分类纯函数可放同文件或 `classify_windows.go`，由 `_test.go` 无构建标签测试（纯字符串，不需要 windows 构建）。为让 `go test` 在非 Windows 上也能跑分类器，把 `isWindowsTunAdapter` 放进无构建标签的 `adapter_windows_names.go`（仅字符串，无 syscall）或直接放 `classify.go` 旁的 `windows_names.go`。
- 发布构建保持 `CGO_ENABLED=0`。

## 6. 测试要点

必须用 `t.TempDir()` / fake controller / FakeBackend，禁止公网、真实用户目录、真实 Sparkle/mihomo。

| 行为 | 包 | 断言 |
|---|---|---|
| `Meta Tunnel` + 别名 `mihomo` 为 TUN | `tundetect` | `isWindowsTunAdapter` true |
| `Wintun Userspace Tunnel` 仍为 TUN | `tundetect` | true |
| WLAN / TAP-Windows / 深信服 VNIC 不是 TUN | `tundetect` | false |
| 展示名 `mihomo (Meta Tunnel)` | `tundetect` | 格式函数单测 |
| 按 PID 扣除自身，保留另一个 pid | `tundetect` | `OtherMihomoProcesses` 只含 other |
| live 未起来时不删任何网卡 | `tundetect` | Sparkle 网卡留在 OtherTun |
| live + 已知 device 时只删匹配名 | `tundetect` | 自身 `Meta` 去掉，`mihomo (Meta Tunnel)` 留下 |
| 信号 A 出现则 Enable 门控 | `runtime` | `CodeTunConflict`，无 PATCH |
| 信号 B 单独不门控 | `runtime` | 现有测试改成结构化 Process |
| Enable 后 live 仍 false 则回滚 | `runtime` | Desired 回 false，返回 `upstream_failure`，settings 无 enable true |
| PATCH 失败即使 reload 成功也失败 | `runtime` | 回滚，不把 Desired 留下 |
| 检测错误不门控 | `runtime` | 保持现有 |
| status 在 Desired On / Live Off 带 last_error | `runtime` | `LastError` 非空 |
| 协议 JSON 仍无新键 | `protocol` | 现有 round-trip 保持；`last_error` 有值时出现 |

不在默认 `go test ./...` 里打真实网卡或真实服务。

## 7. Key Decisions

1. **同一主机一个 TUN 所有者。** 不靠换 `/30` 做成双栈共存；`auto-route` 默认路由只能有一条。
2. **信号 A 按 mihomo/Wintun 家族名字认网卡，不按「所有虚拟 NIC」。** 覆盖 `Meta Tunnel` / 别名 `mihomo`，排除 TAP 与企业 VPN。
3. **自身扣除按 PID 与网卡名，不按切片位置。** 修掉「把本实例标成 other」的现场。
4. **Enable 成功 = live 已变为 true。** HTTP 成功但 live 仍 false 必须回滚 Desired。Force 只绕过冲突门控。
5. **PATCH 失败不可被 regenerate 成功掩盖。** 现场 Desired/Live 分裂的直接原因之一。
6. **`last_error` 只填净化短句，沿用已有字段。** 不新开协议版本，不落盘。
7. **不把 GUI 进程名扩进信号 B。** 避免 Sparkle 仅开窗口、未开 TUN 时误报；TUN 以网卡为准。

## 8. 风险与回滚

- 名字规则可能漏掉尚未见到的隧道类型（例如只叫 `Clash` 的网卡）。漏检时 live 核对仍是第二道闸。新增针要加表驱动测试。
- 别名恰好叫 `meta` 的无关网卡会被当成 TUN。概率低；若出现再收紧到 `IfType==53` 且组合判断。
- live 核对依赖 controller。controller 不可用时 `liveTunEnable` 读不到，Enable 应按「未证实 live」失败并回滚，而不是当成成功。
- 回滚二次 `applyTun(previous)` 是 best-effort：失败只记 `last_error`，不得掩盖第一次的 `TUN did not become live`。

回滚本功能：还原 `tundetect` 分类与 `runtime/tun.go` 的 apply 语义即可，无持久化格式变更。

## 9. PR Plan

### PR 1 — 探测与 Classify 身份扣除

- 标题：`fix(tundetect): 识别 Meta Tunnel 并按 PID 扣除自身`
- 文件：`internal/tundetect/*`，以及 FakeBackend 调用方的类型适配（`internal/runtime/tun_test.go`、`internal/integration/sysproxy_tun_test.go`）
- 依赖：无
- 内容：Windows 名字分类、展示名格式、`Detection.Process`、`Classify(Self)`。门控与 apply 语义先不动，只让信号 A 在测试里能喂到现有 gate。

### PR 2 — Enable live 核对与 last_error

- 标题：`fix(tun): Enable 后核对 live 并回填 last_error`
- 文件：`internal/runtime/tun.go`、`internal/runtime/tun_test.go`、必要时 `internal/cli/tun_test.go`、`internal/tui/pages/system` 仅当详情行缺 LastError 断言
- 依赖：PR 1（`Self` / 结构化 Detection）
- 内容：PATCH 失败不可吞；live 不一致回滚；status 填 `last_error`。

可以在同一分支连续提交，按两个 commit 对应上述 PR 边界，便于审查。合并回 `main` 走一条 PR 也可以，但 commit 必须拆开。

## 10. Open Questions

无。产品选择（单所有者、不换地址共存、force 不跳过 live 核对）已在调查对话中确认。实现期若发现必须用 `IfType` 才能排除误报，作为 PR 1 内的测试驱动收紧，不升协议版本。
