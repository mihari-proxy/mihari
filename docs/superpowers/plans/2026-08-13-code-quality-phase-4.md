# Mihari 四期实施计划

**Spec:** `docs/superpowers/specs/2026-08-13-code-quality-phase-4-design.md`

## Task 1：waitListenAddr + benchmarks

Red：先写 `TestWaitListenAddrTimesOut` 用 stub gateway 永不 bind，断言超时且不依赖固定成功路径的 sleep。

Green：实现 `waitListenAddr`，替换 server_test.go 两处循环。

加 `BenchmarkSafeName`、`BenchmarkExtractZipNestedIndex`。

```
go test -count=1 ./internal/web ./internal/panel/archive
go test -bench='BenchmarkSafeName|BenchmarkExtractZipNestedIndex' -benchtime=20x ./internal/panel/archive
git commit -s -m "test: 用条件等待替换 web gateway bind sleep"
```

## Task 2：文档

写入 coverage 决策、季度模板、roadmap Phase 4。不改 coverage-gate 比较逻辑。

`git commit -s -m "docs: 记录覆盖率门禁数据不足"`

## Task 3：PR squash merge
