# Mihari 三期代码质量提升设计

Date: 2026-08-14  
Status: Draft  
Branch: `docs/code-quality-phase-3`  
Baseline: `f78f1d8` (Phase 2 merged as #74)  
Roadmap: `docs/superpowers/plans/2026-08-13-code-quality-roadmap.md` §6  
Audit: scheduler/Web gateway 错误被 `_ =` 丢弃；`tundetect` 覆盖薄弱

## 1. 目标

让 Web gateway 与 scheduler 的非取消失败可被测试观察到，且不停止 mihomo supervisor。为 `tundetect.FakeBackend` 的错误路径补回归。不安装真实系统服务、不改系统代理/TUN、不改 `/v1` DTO。

## 2. 范围

1. `Manager.Run` 对 `webGateway.Serve` 与 `runScheduler` 的返回值：`context.Canceled` / `DeadlineExceeded` 忽略；其他错误交给可注入的 `onBackgroundError(component, err)`。
2. 默认 sink 为 no-op（保持现有“不停止 supervisor”）。测试注入 sink。
3. `tundetect.FakeBackend`：`Detect` 在 `Err != nil` 时返回该错误并递增 `DetectCalls`（已实现）；补测试钉住该行为。
4. 审查表写入审计/roadmap：service/supervisor 已有注入测试；sysproxy FakeBackend 错误已有测试；本阶段不新增真实 OS 服务测试。

## 3. 非目标

- 不把后台错误写入 `state.Snapshot` 或 `/v1`（避免协议变更）。
- 不新建事件总线。
- 不执行 testenv 真实服务安装。
- 不改 `go.mod`、公开 CLI/JSON。

## 4. 设计

```go
func isBackgroundCancel(err error) bool {
	return err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func (m *Manager) reportBackground(component string, err error) {
	if isBackgroundCancel(err) || m.onBackgroundError == nil {
		return
	}
	m.onBackgroundError(component, err)
}
```

`Options` 增加未导出测试字段不可行（Options 是导出装配）。使用：

```go
// Options.OnBackgroundError 可选。生产不设置。错误文本不得含 secret。
OnBackgroundError func(component string, err error)
```

这是可选回调，不是协议字段。生产 `app.Open` 可不设置。测试设置。

Serve 循环：

```go
err := m.webGateway.Serve(ctx)
m.reportBackground("web-gateway", err)
```

scheduler 同理，component `"scheduler"`。

Supervisor 仍因自身错误返回；gateway/scheduler 失败不改变 `Run` 返回值。

错误消息使用 `err.Error()` 原样交给回调；回调实现不得记录订阅 URL。默认无回调即丢弃，与今日行为一致，但测试能证明分类。

## 5. 测试

通过公共 `Manager.Run`：

- `TestManagerReportsWebGatewayError`：fake Serve 返回 `errors.New("listen failed")`，supervisor 立即返回；回调收到 `web-gateway` + 该错误。
- `TestManagerIgnoresWebGatewayCancellation`：Serve 返回 `context.Canceled`；回调不被调用。
- `TestManagerReportsSchedulerError`：`RunScheduler` 返回非取消错误；回调 `scheduler`。
- `TestManagerIgnoresSchedulerCancellation`：返回 `ctx.Err()` 在 cancel 后；回调不触发。
- 既有 `TestManagerCancelsOwnedSchedulerWhenSupervisorStops` 仍通过。

`tundetect`:

- `TestFakeBackend_PropagatesErrors`：设置 `Err`，`Detect` 返回该错误且 `DetectCalls==1`。

## 6. Key Decisions

| 决策 | 理由 |
|---|---|
| 可选回调而非 Snapshot 字段 | 遵守“不改 /v1 / 不公开新 snapshot 字段” |
| 忽略 cancel/deadline | 正常关机不是故障 |
| 不停止 supervisor | 保持既有隔离 |

## 7. Alternatives

1. 写入 Snapshot.LastError：可能泄漏到 status API。拒绝。
2. 引入 slog 全局 logger：难测且扩大范围。拒绝。

## 8. Security

回调不得接收订阅 URL 或 controller secret。本阶段错误来自 listen/scheduler 包装字符串。

## 9. PR Plan

一个 PR：`fix: 上报 gateway 与 scheduler 非取消错误`。本目标授权 squash merge（仓库禁止 merge commit）。

## 10. Open Questions

无。
