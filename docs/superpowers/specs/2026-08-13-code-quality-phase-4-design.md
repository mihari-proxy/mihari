# Mihari 四期代码质量提升设计

Date: 2026-08-14  
Branch: `docs/code-quality-phase-4`  
Baseline: `4b09376`  
Roadmap: §7

## 1. 目标

在行为稳定后做**小范围**可维护性交付：去掉一处脆弱 `time.Sleep` 轮询、为 archive 路径与小 ZIP 增加 benchmark、记录 coverage 样本数并**不启用**数字门禁。不拆大 TUI 文件。

## 2. 范围

1. `internal/web/server_test.go` 两处 `ListenAddr` 轮询：`time.Sleep(10ms)` 改为 `waitListenAddr(gateway, deadline)`，用短 ticker/`time.After` 等到地址非空，超时失败。生产代码不变。
2. `internal/panel/archive`：`BenchmarkSafeName`、`BenchmarkExtractZipNestedIndex`（固定小 fixture，`b.ReportAllocs`）。
3. `docs/superpowers/reviews/coverage-gate-decision-2026-08-14.md`：统计 AQ-04 修复后 `main` coverage job 成功次数（#73、#74、#75 至多 3 次同口径样本），结论：**不启用**相对回归门禁（需 ≥10）也不启用固定阈值（需 ≥30）。
4. `docs/superpowers/reviews/quarterly-quality-review-template.md`：复审模板（开放风险、linter 豁免、漏洞、覆盖率波动、benchmark、超大文件）。
5. Roadmap Phase 4 状态 `实施中` → merge 后由本 PR 标 `已验收` 并写明 coverage gate 未启用。

## 3. 非目标

- 不启用 coverage 百分比或相对门禁。
- 不一次性拆 TUI/Web/runtime 大文件。
- 不改 `go.mod`、`/v1`、settings schema。
- 不为 CRUD 凑 benchmark。

## 4. Coverage 样本

AQ-04 之后、同 `-coverpkg=./...` 的 `main` workflow：#73、#74、#75（#75 的 main 跑可能仍在进行）。**N < 10**。禁止改 `scripts/coverage-gate` 的 compare 门禁。

## 5. Key Decisions

| 决策 | 理由 |
|---|---|
| 只改测试里的 bind 等待 | 生产无固定 sleep；supervisor 已用 fake waiter |
| 不拆大文件 | 无独立行为边界计划，避免无关重构 |
| 不启用 coverage gate | 数据前置条件不满足 |

## 6. Alternatives

1. 启用 1% 相对门禁：违反 10 次样本规则。拒绝。
2. 拆分 tui/model.go：超出本 PR 可审查范围。拒绝。

## 7. PR Plan

一个 PR：`chore: 增加 archive benchmark 并记录覆盖率门禁未启用`。授权 squash merge。

## 8. Open Questions

无。
