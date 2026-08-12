# Mihari Test Governance Implementation Roadmap

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 分阶段补齐 Mihari 高风险测试盲区，并建立跨平台、覆盖率与 fuzz 的持续治理能力。

**Architecture:** 路线按四个可独立交付的子计划执行。先保护稳定契约与 WebSocket，再覆盖 mutation 和平台生命周期；随后增加 CI 可观测性与相对基线门禁；最后引入 bounded fuzz。每个阶段都能独立通过现有默认测试，不依赖后续阶段。

**Tech Stack:** Go 1.26.5、`testing`、`httptest`、`github.com/coder/websocket`、GitHub Actions、Go coverage profile、Go native fuzzing。

## Global Constraints

- 实施前必须获得用户对相应阶段的明确授权，并更新根 `AGENTS.md` 当前“不新增或修改测试代码、CI、Git hooks”的治理限制。
- 不修改 `/v1` DTO、错误码、JSON envelope、CLI 退出码或持久化格式。
- 不新增第三方测试、mock、覆盖率或 fuzz 依赖。
- 默认测试不访问公网、真实订阅、真实用户目录、真实 mihomo 或系统服务。
- 所有网络、文件、listener、goroutine 与子进程均由测试拥有并清理。
- 行为变更使用 Red–Green–Refactor；覆盖既有行为时先记录目标函数的 0% 覆盖基线，不制造虚假失败断言。
- 所有 Go 修改执行 `gofmt`；验证从目标包扩大到 `go test -race ./...`、`go vet ./...`。
- 只在非 `main` 分支实施；仅在用户明确要求时创建 commit。

---

## Execution Order

1. [阶段 A：控制协议与 WebSocket](2026-08-12-test-governance-phase-a-control-websocket.md)
2. [阶段 B：Mutation 与平台生命周期](2026-08-12-test-governance-phase-b-mutation-platform.md)
3. [阶段 C：CI 与覆盖率治理](2026-08-12-test-governance-phase-c-ci-coverage.md)
4. [阶段 D：Bounded Fuzz](2026-08-12-test-governance-phase-d-fuzz.md)

阶段 A、B 修改测试和少量可测试性边界；阶段 C 修改 CI 并新增无依赖覆盖率工具；阶段 D 新增 fuzz target 与独立 workflow。每个阶段单独审核、单独授权、单独交付。

## Governance Gate

- [ ] **Step 1: 获得阶段实施授权**

记录用户明确批准的阶段，不把设计文档批准解释为代码或 CI 修改授权。

- [ ] **Step 2: 更新根治理状态**

修改 `AGENTS.md` 第 10 节，将绝对禁止测试/CI 修改改为与获批阶段一致的限定授权，同时保留“不迁移目录、不新增 Git hooks、不预设固定总覆盖率”的约束。

- [ ] **Step 3: 检查独立分支与工作区**

```console
git branch --show-current
git status --short
```

Expected: 当前分支不是 `main`，不存在会被本阶段覆盖的用户改动。

- [ ] **Step 4: 建立干净基线**

```console
go test -count=1 ./...
go test -count=1 -race ./...
go vet ./...
```

Expected: 全部通过；若失败，停止实施并报告基线问题。

