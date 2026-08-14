# Mihari 三期实施计划

**Goal:** 上报 Web gateway / scheduler 非取消错误，并钉住 tundetect FakeBackend 错误路径。

**Spec:** `docs/superpowers/specs/2026-08-13-code-quality-phase-3-design.md`

## Task 1：background error 分类

**Files:** `internal/runtime/manager.go`, `internal/runtime/manager_test.go`, `internal/runtime/options` (same file as Options)

### Step 1 Red

在 `Options` 增加 `OnBackgroundError func(string, error)`。

测试（公共 `Manager.Run`）：

- Serve 返回 `listen failed` → 回调 `web-gateway`
- Serve 返回 `context.Canceled` → 无回调
- RunScheduler 返回 `refresh failed` → `scheduler`
- RunScheduler 返回 `ctx.Err()` 在 cancel 后 → 无回调

Supervisor fake 立即返回 error 以结束 Run（与既有 cancel 测试相同）。

```
go test -count=1 -run '^TestManager(Reports|Ignores)' ./internal/runtime
```

Expected: FAIL（尚未接线）。

### Step 2 Green

`reportBackground` + Serve/scheduler 调用。

```
go test -count=1 ./internal/runtime
```

### Step 3 commit

`fix: 上报 gateway 与 scheduler 非取消错误`

## Task 2：tundetect fake + 文档

**Files:** `internal/tundetect/fake_test.go`（新建）, 审计/roadmap/本设计与计划

```
go test -count=1 ./internal/tundetect
git commit -s -m "test: 覆盖 tundetect FakeBackend 错误路径"
```

## Task 3：PR squash merge

`go test -count=1 ./internal/runtime ./internal/tundetect`
Push `docs/code-quality-phase-3`，PR，required checks 绿后 `gh pr merge --squash --admin`。
