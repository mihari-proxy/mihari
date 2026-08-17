# TUN 冲突探测与 Live 校验 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Windows 认出 Sparkle/mihomo 的 `Meta Tunnel` 网卡；按 PID/网卡名扣除自身；Enable 必须看到 live 为 true，否则回滚并填 `last_error`。

**Architecture:** `tundetect` 继续只观察。Windows 用纯字符串分类器认 Wintun/mihomo 家族网卡。`Classify` 改为接收 `Self{TunActive,TunName,CorePID}`。`runtime.mutateTun` 在 apply 后读 `liveTunEnable`，不一致则回滚 Desired；PATCH 错误不再被 regenerate 成功吞掉。

**Tech Stack:** Go 1.26，`golang.org/x/sys/windows`（已有），标准 `go test`。`CGO_ENABLED=0`。

**设计文档:** `docs/superpowers/specs/2026-08-17-tun-conflict-detect-and-live-design.md`

**Issue:** https://github.com/mihari-proxy/mihari/issues/91

**工作目录（worktree）:** 必须在 `.worktrees/fix-tun-conflict-detect-and-live`（分支 `fix/tun-conflict-detect-and-live`）。禁止修改主工作区 `main`。

## Global Constraints

- 禁止在 `main` 上直接改代码或提交。
- `/v1` 只允许加法：`TunStatus.last_error` 已存在；不新增错误码；`CodeTunConflict` 的 message 不变。
- 不杀外部进程，不换 `device`/`inet4-address` 做双 TUN 共存，不扫描 `Sparkle.exe`。
- 错误文案不得包含 token、controller secret、订阅 URL、完整路径。
- Windows 分类纯函数必须能在非 Windows 上 `go test`。
- 平台 syscall 必须留在 `_windows.go` / `_linux.go` / `_darwin.go`。
- 测试不得访问公网、真实用户目录或已安装的 mihomo/Sparkle。
- 每个行为先写失败测试，再写最小实现。
- 修改过的 Go 文件必须 `gofmt`。
- Conventional Commits：类型英文、摘要中文。一个 task 一个 commit。提交加 `-s`（DCO）。

---

### Task 1: Windows 网卡名字分类器

**Files:**
- Create: `internal/tundetect/windows_names.go`
- Create: `internal/tundetect/windows_names_test.go`
- Modify: `internal/tundetect/tundetect_windows.go`（`isWintun` 改委托，展示名格式）

**Interfaces:**
- Consumes: 无
- Produces: `isWindowsTunAdapter(desc, friendly string) bool`；`formatAdapterName(desc, friendly string) string`

- [ ] **Step 1: 写失败测试**

`internal/tundetect/windows_names_test.go`：

```go
package tundetect

import "testing"

func TestIsWindowsTunAdapter(t *testing.T) {
	tests := []struct {
		name     string
		desc     string
		friendly string
		want     bool
	}{
		{"legacy wintun desc", "Wintun Userspace Tunnel", "Ethernet 2", true},
		{"sparkle meta tunnel", "Meta Tunnel", "mihomo", true},
		{"friendly mihomo only", "Some Vendor NIC", "mihomo", true},
		{"friendly Meta exact", "Virtual", "Meta", true},
		{"wireguard tunnel", "WireGuard Tunnel", "OfficeNet", true},
		{"wlan", "Intel(R) Wi-Fi 6 AX201 160MHz", "WLAN", false},
		{"tap windows", "TAP-Windows Adapter V9", "以太网 8", false},
		{"sangfor", "Sangfor SSL VPN CS Support System VNIC", "以太网 7", false},
		{"netease tap", "Netease UU TAP-Win32 Adapter V9.21", "以太网 6", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isWindowsTunAdapter(tt.desc, tt.friendly); got != tt.want {
				t.Fatalf("got=%v want=%v", got, tt.want)
			}
		})
	}
}

func TestFormatAdapterName(t *testing.T) {
	if got := formatAdapterName("Meta Tunnel", "mihomo"); got != "mihomo (Meta Tunnel)" {
		t.Fatalf("got=%q", got)
	}
	if got := formatAdapterName("Wintun Userspace Tunnel", "Wintun Userspace Tunnel"); got != "Wintun Userspace Tunnel" {
		t.Fatalf("got=%q", got)
	}
	if got := formatAdapterName("Wintun Userspace Tunnel", ""); got != "Wintun Userspace Tunnel" {
		t.Fatalf("got=%q", got)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

```powershell
Set-Location D:\modular_dev\mihari\.worktrees\fix-tun-conflict-detect-and-live
go test ./internal/tundetect -run "TestIsWindowsTunAdapter|TestFormatAdapterName" -count=1
```

预期：编译失败，`isWindowsTunAdapter` / `formatAdapterName` 未定义。

- [ ] **Step 3: 最小实现**

`internal/tundetect/windows_names.go`（无构建标签）：

```go
package tundetect

import "strings"

func isWindowsTunAdapter(desc, friendly string) bool {
	haystack := strings.ToLower(desc + " " + friendly)
	for _, needle := range []string{"wintun", "meta tunnel", "wireguard"} {
		if strings.Contains(haystack, needle) {
			return true
		}
	}
	name := strings.ToLower(strings.TrimSpace(friendly))
	if name == "" {
		name = strings.ToLower(strings.TrimSpace(desc))
	}
	return name == "mihomo" || name == "meta"
}

func formatAdapterName(desc, friendly string) string {
	friendly = strings.TrimSpace(friendly)
	desc = strings.TrimSpace(desc)
	switch {
	case friendly == "":
		return desc
	case desc == "" || strings.EqualFold(friendly, desc):
		return friendly
	default:
		return friendly + " (" + desc + ")"
	}
}
```

`tundetect_windows.go`：`isWintun` 改为 `return isWindowsTunAdapter(desc, friendly)`；枚举里用 `formatAdapterName`。

- [ ] **Step 4: 跑测试确认通过**

同 Step 2。预期：PASS。再跑 `go test ./internal/tundetect -count=1`。

- [ ] **Step 5: Commit**

```powershell
Set-Location D:\modular_dev\mihari\.worktrees\fix-tun-conflict-detect-and-live
git add internal/tundetect/windows_names.go internal/tundetect/windows_names_test.go internal/tundetect/tundetect_windows.go
git commit -s -m "fix(tundetect): 识别 Meta Tunnel 与 mihomo 网卡名"
```

---

### Task 2: Detection.Process 与 Classify(Self)

**Files:**
- Modify: `internal/tundetect/tundetect.go`
- Modify: `internal/tundetect/classify.go`
- Modify: `internal/tundetect/classify_test.go`
- Modify: `internal/tundetect/tundetect_windows.go`（进程改为 `Process`）
- Modify: `internal/tundetect/tundetect_linux.go`
- Modify: `internal/tundetect/tundetect_darwin.go`
- Modify: `internal/runtime/tun_test.go`（Detection 字面量）
- Modify: `internal/integration/sysproxy_tun_test.go`（若构造了 MihomoProcesses）

**Interfaces:**
- Consumes: Task 1 的 `formatAdapterName`（网卡匹配时比较 alias）
- Produces:

```go
type Process struct {
    Name string
    PID  int
}

type Self struct {
    TunActive bool
    TunName   string
    CorePID   int
}

func Classify(d Detection, self Self) *protocol.TunConflict
func formatProcess(p Process) string // "name (pid)"
func adapterNameMatch(listed, tunName string) bool
```

- [ ] **Step 1: 写失败测试**

重写 `classify_test.go` 的表，旧的 `Classify(d, bool)` 与 `[]string` 进程必须编不过。新增用例：

```go
func TestClassify_SubtractsByPIDNotPosition(t *testing.T) {
	got := Classify(Detection{
		MihomoProcesses: []Process{
			{Name: "mihomo-alpha.exe", PID: 11220},
			{Name: "mihomo.exe", PID: 13400},
		},
	}, Self{CorePID: 13400})
	if got == nil || len(got.OtherMihomoProcesses) != 1 || got.OtherMihomoProcesses[0] != "mihomo-alpha.exe (11220)" {
		t.Fatalf("got=%#v", got)
	}
}

func TestClassify_DoesNotDropAdapterWhenSelfInactive(t *testing.T) {
	got := Classify(Detection{
		TunInterfaces: []string{"mihomo (Meta Tunnel)"},
	}, Self{TunActive: false, CorePID: 13400})
	if got == nil || len(got.OtherTunInterfaces) != 1 || got.OtherTunInterfaces[0] != "mihomo (Meta Tunnel)" {
		t.Fatalf("got=%#v", got)
	}
}

func TestClassify_SubtractsAdapterByName(t *testing.T) {
	got := Classify(Detection{
		TunInterfaces: []string{"Meta", "mihomo (Meta Tunnel)"},
	}, Self{TunActive: true, TunName: "Meta"})
	if got == nil || len(got.OtherTunInterfaces) != 1 || got.OtherTunInterfaces[0] != "mihomo (Meta Tunnel)" {
		t.Fatalf("got=%#v", got)
	}
}

func TestClassify_UnknownLiveNameDropsOne(t *testing.T) {
	got := Classify(Detection{
		TunInterfaces: []string{"Meta"},
	}, Self{TunActive: true, TunName: ""})
	if got != nil {
		t.Fatalf("got=%#v", got)
	}
}

func TestClassify_UnknownPIDKeepsAllProcesses(t *testing.T) {
	got := Classify(Detection{
		MihomoProcesses: []Process{{Name: "mihomo.exe", PID: 13400}},
	}, Self{})
	if got == nil || len(got.OtherMihomoProcesses) != 1 {
		t.Fatalf("got=%#v", got)
	}
}
```

把原表驱动改成 `self Self` + `[]Process`。原先「single mihomo is self, subtracts to nil」改为 `Self{CorePID: 1}` 且进程 PID=1。

- [ ] **Step 2: 跑测试确认失败**

```powershell
Set-Location D:\modular_dev\mihari\.worktrees\fix-tun-conflict-detect-and-live
go test ./internal/tundetect -count=1
```

预期：编译失败（`Classify` 仍是 `bool`，`MihomoProcesses` 仍是 `[]string`）。

- [ ] **Step 3: 最小实现**

`tundetect.go` 增加 `Process`、`Self`，`Detection.MihomoProcesses []Process`。

`classify.go`：

- 网卡：`TunActive && TunName!=""` 则按 `adapterNameMatch` 删一项；`TunActive && TunName==""` 则删 `[0]`；否则不删。
- 进程：`CorePID!=0` 则删 PID 相等的一项；否则不删。
- 输出进程用 `formatProcess`。

`adapterNameMatch(listed, tunName)`：忽略大小写；`listed==tunName`，或 `listed` 为 `tunName (...)`，或 `listed` 以 ` (tunName)` 结尾。

三端 enumerate 函数改返回 `[]Process`。Windows：`PID: int(entry.ProcessID)`。Linux：`strconv.Atoi(e.Name())`。Darwin：解析 `ps` 的 pid 列。

更新 `runtime/tun_test.go`：

```go
Detection: tundetect.Detection{MihomoProcesses: []tundetect.Process{{Name: "mihomo", PID: 123}, {Name: "mihomo", PID: 456}}},
```

- [ ] **Step 4: 跑测试确认通过**

```powershell
go test ./internal/tundetect ./internal/runtime ./internal/integration -count=1
```

预期：PASS。

- [ ] **Step 5: Commit**

```powershell
git add internal/tundetect internal/runtime/tun_test.go internal/integration
git commit -s -m "fix(tundetect): 按 PID 与网卡名扣除自身"
```

---

### Task 3: runtime 把 Self 接到 Classify

**Files:**
- Modify: `internal/runtime/tun.go`（`detectTunConflict` / `selfTunLiveActive`）
- Modify: `internal/runtime/tun_test.go`（`TestEnableTunSubtractsSelfWhenLiveActive` 补 device + PID）

**Interfaces:**
- Consumes: `tundetect.Self`、`tundetect.Classify`
- Produces: `selfFromLive(ctx) tundetect.Self`；`liveTunDevice(configs) string`

- [ ] **Step 1: 写失败测试**

在 `TestEnableTunSubtractsSelfWhenLiveActive` 旁追加：

```go
func TestEnableTunKeepsForeignAdapterWhenLiveDeviceDiffers(t *testing.T) {
	controller := &fakeController{configs: map[string]any{
		"tun": map[string]any{"enable": true, "device": "Meta", "stack": "gVisor"},
	}}
	manager := newTunManagerWithDetect(t, controller, defaultTunSettings(map[string]any{
		"enable": true, "stack": "gVisor",
	}), &tundetect.FakeBackend{
		Detection: tundetect.Detection{TunInterfaces: []string{"Meta", "mihomo (Meta Tunnel)"}},
	})
	_, err := manager.EnableTun(context.Background(), Operation{ID: "tun-foreign", Source: "test"}, false)
	var apiError protocol.APIError
	if !errors.As(err, &apiError) || apiError.Code != protocol.CodeTunConflict {
		t.Fatalf("err=%v", err)
	}
	names, _ := apiError.Details["other_tun_interfaces"].([]string)
	if len(names) != 1 || names[0] != "mihomo (Meta Tunnel)" {
		t.Fatalf("details=%#v", apiError.Details)
	}
	if controller.patchCalls != 0 {
		t.Fatalf("patchCalls=%d", controller.patchCalls)
	}
}
```

该测试在 Task 2 之后、本 task 实现前应失败：`detectTunConflict` 仍用 `Classify(d, bool)`，live 时会盲删第一张 `Meta`，剩下 Sparkle 网卡——等等，那其实已经会门控。

更精确：若实现仍用 bool 盲删，检测顺序 `["mihomo (Meta Tunnel)", "Meta"]` 时会删错（删 Sparkle，留下自己），Enable **不会**门控。用这个顺序写测试：

```go
Detection: tundetect.Detection{TunInterfaces: []string{"mihomo (Meta Tunnel)", "Meta"}},
```

盲删会删 Sparkle，Enable 错误成功。按名扣除会留下 Sparkle，Enable 应 `CodeTunConflict`。

- [ ] **Step 2: 跑测试确认失败**

```powershell
go test ./internal/runtime -run TestEnableTunKeepsForeignAdapterWhenLiveDeviceDiffers -count=1
```

预期：FAIL，`err=nil`（盲删删错网卡）。

- [ ] **Step 3: 最小实现**

```go
func (m *Manager) detectTunConflict(ctx context.Context) *protocol.TunConflict {
	if m.tunDetect == nil {
		return nil
	}
	detection, err := m.tunDetect.Detect(ctx)
	if err != nil {
		return nil
	}
	return tundetect.Classify(detection, m.selfFromLive(ctx))
}

func (m *Manager) selfFromLive(ctx context.Context) tundetect.Self {
	self := tundetect.Self{CorePID: m.store.Load().Core.PID}
	if m.controller == nil || ctx.Err() != nil {
		return self
	}
	configs, err := m.controller.Configs(ctx)
	if err != nil {
		return self
	}
	if live, ok := liveTunEnable(configs); ok && live {
		self.TunActive = true
		self.TunName = liveTunDevice(configs)
	}
	return self
}

func liveTunDevice(configs map[string]any) string {
	raw, _ := configs["tun"].(map[string]any)
	device, _ := raw["device"].(string)
	return strings.TrimSpace(device)
}
```

删除只返回 bool 的 `selfTunLiveActive`，或改成对 `selfFromLive(ctx).TunActive` 的包装（无其它调用则可删）。

`newTunManagerWithDetect` 如需 PID，在 Options 里给 Store 预置 `Core: {PID: 13400}` 不是本用例必需。

- [ ] **Step 4: 跑测试确认通过**

```powershell
go test ./internal/runtime -count=1
```

预期：PASS。

- [ ] **Step 5: Commit**

```powershell
git add internal/runtime/tun.go internal/runtime/tun_test.go
git commit -s -m "fix(runtime): Classify 使用 live device 与 core PID"
```

---

### Task 4: PATCH 失败不可被 regenerate 吞掉

**Files:**
- Modify: `internal/runtime/tun.go`（`applyTun`）
- Modify: `internal/runtime/tun_test.go`
- Modify: `internal/runtime/manager_test.go`（`fakeController.Reload`）

**Interfaces:**
- Consumes: 现有 `applyTun`、`configReloader`
- Produces: `applyTun` 在 `patchErr != nil` 时必须返回 `patchErr`

- [ ] **Step 1: 写失败测试**

给 `fakeController` 增加：

```go
reloads   int
reloadErr error

func (c *fakeController) Reload(context.Context, string, bool) error {
	c.reloads++
	return c.reloadErr
}
```

新测试（用 `subscription.Open` + 空 catalog，走 bootstrap regenerate）：

```go
func TestApplyTunReturnsPatchErrorEvenIfReloadSucceeded(t *testing.T) {
	root := t.TempDir()
	controller := &fakeController{
		configs: map[string]any{},
		patchConfigs: func(context.Context, map[string]any) error {
			return protocol.APIError{Code: protocol.CodeUpstreamFailure, Message: "patch tun rejected"}
		},
	}
	subs, err := subscription.Open(subscription.ServiceOptions{
		CatalogPath: filepath.Join(root, "catalog.yaml"),
		CacheDir:    filepath.Join(root, "cache"),
		ProxyAddr:   "127.0.0.1:9190",
	})
	if err != nil {
		t.Fatal(err)
	}
	settings := defaultTunSettings(nil)
	settingsPath := filepath.Join(root, "settings.yaml")
	if err := config.Save(settingsPath, settings); err != nil {
		t.Fatal(err)
	}
	manager := newTestManager(Options{
		Controller:    controller,
		SettingsPath:  settingsPath,
		Settings:      settings,
		Subscriptions: subs,
		RuntimeConfig: filepath.Join(root, "runtime", "config.yaml"),
		StagingDir:    filepath.Join(root, "staging"),
	})
	_, err = manager.EnableTun(context.Background(), Operation{ID: "tun-patch-fail", Source: "test"}, false)
	if err == nil {
		t.Fatal("expected patch error")
	}
	loaded, loadErr := config.Load(settingsPath)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if tunDesiredEnable(loaded.Tun) {
		t.Fatalf("desired must roll back, tun=%#v", loaded.Tun)
	}
}
```

当前 `applyTun` 在 reload 成功后返回 nil，本测试会失败在 `err==nil` 或 Desired 仍为 true。

- [ ] **Step 2: 跑测试确认失败**

```powershell
go test ./internal/runtime -run TestApplyTunReturnsPatchErrorEvenIfReloadSucceeded -count=1
```

预期：FAIL，`err==nil` 或 Desired 未回滚。

- [ ] **Step 3: 最小实现**

`applyTun` 末尾改为：

```go
if patchErr != nil {
	return patchErr
}
if regenerated || patched {
	return nil
}
if regenerateErr != nil {
	return regenerateErr
}
return protocol.APIError{Code: protocol.CodeInvalidState, Message: "mihomo controller is unavailable"}
```

- [ ] **Step 4: 跑测试确认通过**

```powershell
go test ./internal/runtime -count=1
```

预期：PASS。

- [ ] **Step 5: Commit**

```powershell
git add internal/runtime/tun.go internal/runtime/tun_test.go internal/runtime/manager_test.go
git commit -s -m "fix(tun): regenerate 成功后仍返回 PATCH 错误"
```

---

### Task 5: Enable 后核对 live 并回滚

**Files:**
- Modify: `internal/runtime/tun.go`
- Modify: `internal/runtime/tun_test.go`

**Interfaces:**
- Consumes: `liveTunEnable`、`applyTun`、现有 settings 回滚
- Produces: Enable 在 live 非 true 时返回 `CodeUpstreamFailure` / `"TUN did not become live after apply"`，Desired 回滚

- [ ] **Step 1: 写失败测试**

```go
func TestEnableTunRollsBackWhenLiveStaysOff(t *testing.T) {
	controller := &fakeController{
		configs: map[string]any{"tun": map[string]any{"enable": false, "stack": "gVisor"}},
		patchConfigs: func(context.Context, map[string]any) error {
			return nil
		},
	}
	manager := newTunManager(t, controller, defaultTunSettings(nil))
	_, err := manager.EnableTun(context.Background(), Operation{ID: "tun-live-off", Source: "test"}, false)
	var apiError protocol.APIError
	if !errors.As(err, &apiError) || apiError.Code != protocol.CodeUpstreamFailure {
		t.Fatalf("err=%v", err)
	}
	if apiError.Message != "TUN did not become live after apply" {
		t.Fatalf("message=%q", apiError.Message)
	}
	assertPersistedTun(t, manager.settingsPath, false, "") // 见 Step 3：previous 为 nil 时 settings.Tun 应回到空/非 desired
}

func TestEnableTunForceStillRequiresLive(t *testing.T) {
	controller := &fakeController{
		configs: map[string]any{"tun": map[string]any{"enable": false}},
		patchConfigs: func(context.Context, map[string]any) error { return nil },
	}
	manager := newTunManagerWithDetect(t, controller, defaultTunSettings(nil), &tundetect.FakeBackend{
		Detection: tundetect.Detection{TunInterfaces: []string{"mihomo (Meta Tunnel)"}},
	})
	_, err := manager.EnableTun(context.Background(), Operation{ID: "tun-force-live", Source: "test"}, true)
	var apiError protocol.APIError
	if !errors.As(err, &apiError) || apiError.Code != protocol.CodeUpstreamFailure {
		t.Fatalf("force must not skip live check, err=%v", err)
	}
}

func TestEnableTunFailsWhenConfigsUnreadableAfterApply(t *testing.T) {
	controller := &fakeController{
		configs:    map[string]any{},
		configsErr: protocol.APIError{Code: protocol.CodeUpstreamFailure, Message: "mihomo controller is unavailable"},
	}
	// patchConfigs 默认会写 configs，但随后 Configs 返回 err
	manager := newTunManager(t, controller, defaultTunSettings(nil))
	_, err := manager.EnableTun(context.Background(), Operation{ID: "tun-unread", Source: "test"}, false)
	if err == nil {
		t.Fatal("unread live must not count as success")
	}
}
```

`assertPersistedTun` 在 Tun 被回滚成 nil 时会 `Fatal("expected persisted tun block")`。本用例 previous 为 nil，回滚后允许 `loaded.Tun==nil` 或 `!tunDesiredEnable`。测试里不要调用 `assertPersistedTun`；改成：

```go
loaded, err := config.Load(manager.settingsPath)
if err != nil {
	t.Fatal(err)
}
if tunDesiredEnable(loaded.Tun) {
	t.Fatalf("desired rolled back? tun=%#v", loaded.Tun)
}
```

- [ ] **Step 2: 跑测试确认失败**

```powershell
go test ./internal/runtime -run "TestEnableTunRollsBackWhenLiveStaysOff|TestEnableTunForceStillRequiresLive|TestEnableTunFailsWhenConfigsUnreadableAfterApply" -count=1
```

预期：FAIL，`err==nil`（apply 被当成成功）。

- [ ] **Step 3: 最小实现**

在 `mutateTun` 里 `applyTun` 成功之后、`coordinator.Do` 之前：

```go
if enable {
	live, ok := false, false
	if m.controller != nil && ctx.Err() == nil {
		if configs, cfgErr := m.controller.Configs(ctx); cfgErr == nil {
			live, ok = liveTunEnable(configs)
		}
	}
	if !(ok && live) {
		m.settingsMu.Lock()
		m.settings.Tun = previousTun
		_ = m.persistSettings()
		m.settingsMu.Unlock()
		if applyBackErr := m.applyTun(ctx, previousTun); applyBackErr != nil {
			_ = applyBackErr // best-effort restore; 第一次失败仍返回
		}
		return protocol.TunStatus{}, protocol.APIError{
			Code:    protocol.CodeUpstreamFailure,
			Message: "TUN did not become live after apply",
		}
	}
}
```

`applyTun(nil)`：`buildManagedTun` 不在这条回滚路径；直接把 `previousTun`（可能为 nil）交给 `applyTun`。`applyTun` 对 nil 的 PATCH 应为 `{"tun": nil}` 或跳过？当前 `PatchConfigs({"tun": nil})` 可能不合法。

回滚二次 apply 规则（必须写死）：

- `previousTun == nil` 或 `len==0`：PATCH `{"tun": {"enable": false, "stack": defaultTunStack}}` 会改变「未托管」语义。改为：只 persist 回空 Tun，**不要** PATCH 一个伪造的 managed 块。二次 apply 仅当 `len(previousTun)>0`。
- `previousTun` 非空：`applyTun(ctx, previousTun)`。

`applyTun` 里 `nextTun==nil` 时跳过 PATCH。

- [ ] **Step 4: 跑测试确认通过**

```powershell
go test ./internal/runtime -count=1
```

预期：PASS。现有 `TestEnableTunPersistsAndPatches` 仍过（fake 会把 patch 写进 configs）。

- [ ] **Step 5: Commit**

```powershell
git add internal/runtime/tun.go internal/runtime/tun_test.go
git commit -s -m "fix(tun): Enable 后核对 live 失败则回滚"
```

---

### Task 6: 填充 TunStatus.last_error

**Files:**
- Modify: `internal/runtime/tun.go`
- Modify: `internal/runtime/tun_test.go`
- Modify: `internal/runtime/manager.go`（若 `tunLastError` 放在 Manager 结构体上）

**Interfaces:**
- Consumes: Task 5 的失败路径
- Produces: `Manager.tunLastError string`；`buildTunStatus` 非空 `LastError`

- [ ] **Step 1: 写失败测试**

```go
func TestTunStatusLastErrorWhenDesiredOnLiveOff(t *testing.T) {
	live := false
	controller := &fakeController{configs: map[string]any{
		"tun": map[string]any{"enable": live, "stack": "gVisor"},
	}}
	manager := newTunManager(t, controller, defaultTunSettings(map[string]any{
		"enable": true, "stack": "gVisor",
	}))
	status, err := manager.TunStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.LastError != "live TUN is off" {
		t.Fatalf("LastError=%q", status.LastError)
	}
}

func TestEnableTunFailurePersistsLastErrorOnStatus(t *testing.T) {
	controller := &fakeController{
		configs:      map[string]any{"tun": map[string]any{"enable": false}},
		patchConfigs: func(context.Context, map[string]any) error { return nil },
	}
	manager := newTunManager(t, controller, defaultTunSettings(nil))
	_, _ = manager.EnableTun(context.Background(), Operation{ID: "tun-err", Source: "test"}, false)
	status, err := manager.TunStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.LastError != "TUN did not become live after apply" {
		t.Fatalf("LastError=%q", status.LastError)
	}
	if status.DesiredEnable {
		t.Fatal("desired should stay off after failed enable")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

```powershell
go test ./internal/runtime -run "TestTunStatusLastErrorWhenDesiredOnLiveOff|TestEnableTunFailurePersistsLastErrorOnStatus" -count=1
```

预期：FAIL，`LastError` 为空。

- [ ] **Step 3: 最小实现**

`Manager` 增加 `tunLastError string`（与 `settingsMu` 一起保护，或单独字段仅在 lock 内写）。

- 成功 mutate 且 live 核对通过：`tunLastError=""`。
- live 核对失败 / apply 失败：`tunLastError=mapped.Message`。
- `buildTunStatus`：

```go
if lastError == "" {
	lastError = m.tunLastError
}
if lastError == "" && status.DesiredEnable && status.LiveEnable != nil && !*status.LiveEnable {
	lastError = "live TUN is off"
}
status.LastError = lastError
```

- [ ] **Step 4: 跑测试确认通过**

```powershell
go test ./internal/runtime ./internal/cli ./internal/tui/pages/system ./internal/control/protocol -count=1
gofmt -l internal/tundetect internal/runtime
```

预期：测试 PASS；`gofmt -l` 无输出。

- [ ] **Step 5: Commit**

```powershell
git add internal/runtime
git commit -s -m "fix(tun): Desired/Live 漂移时填充 last_error"
```

---

### Task 7: 交叉验证

**Files:** 本任务不改生产代码，除非 `gofmt` / 编译缺口。

**Interfaces:**
- Consumes: Task 1–6
- Produces: 验证记录（命令已实际跑过）

- [ ] **Step 1: 包测试**

```powershell
Set-Location D:\modular_dev\mihari\.worktrees\fix-tun-conflict-detect-and-live
go test ./internal/tundetect ./internal/runtime ./internal/cli ./internal/tui/pages/system ./internal/control/protocol ./internal/integration -count=1
```

预期：PASS。

- [ ] **Step 2: vet 与格式**

```powershell
go vet ./internal/tundetect ./internal/runtime
gofmt -l internal/tundetect internal/runtime
```

预期：vet 无输出；gofmt 无输出。

- [ ] **Step 3: 无 CGO 编译当前平台**

```powershell
$env:CGO_ENABLED = '0'
go build -o bin/mihari-check.exe ./cmd/mihari
```

预期：成功。不要提交 `bin/`。

- [ ] **Step 4: 不在本 task 提交**，除非 Step 1–3 发现必须修的格式问题。有修则：

```powershell
git add internal
git commit -s -m "style(tun): 格式化冲突探测改动"
```

---

## Spec coverage

| 规格条款 | Task |
|---|---|
| `Meta Tunnel` / 别名 `mihomo` 为信号 A | 1 |
| 展示名 `friendly (desc)` | 1 |
| 按 PID 扣自身进程 | 2 |
| live 未起来不删别人网卡 | 2 |
| 按 device 名扣自身网卡 | 2、3 |
| 盲删顺序导致漏门控 | 3 |
| PATCH 失败不可吞 | 4 |
| Enable 后 live 核对 + 回滚 | 5 |
| Force 不跳过 live 核对 | 5 |
| controller 读不到不算成功 | 5 |
| `last_error` | 6 |
| 不改 `/v1` 键、不杀进程、不双 TUN | 全局约束 |

## Type consistency

- `tundetect.Process` / `tundetect.Self` / `Classify(Detection, Self)` 在 Task 2 定义，Task 3 使用。
- 协议仍是 `[]string` `other_mihomo_processes`，格式 `name (pid)`。
- live 失败文案固定为 `TUN did not become live after apply`；漂移兜底为 `live TUN is off`。
