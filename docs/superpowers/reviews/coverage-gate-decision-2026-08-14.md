# Coverage gate decision (2026-08-14)

## Sample window

Only `main` workflow runs **after** AQ-04 (`[]string{}` CLI tests, merged in #73) count toward the same-toolchain `-coverpkg=./...` observe series.

| SHA | PR | Notes |
|---|---|---|
| `a603423` | #73 | first post-AQ-04 main CI |
| `f78f1d8` | #74 | second |
| `4b09376` | #75 | third (may still be completing at doc write time) |

**N = 2–3** successful comparable `main` coverage jobs. Relative regression gate requires ≥10. Fixed percentage threshold requires ≥30.

## Decision

**Do not enable** `scripts/coverage-gate compare` or any percentage fail threshold. CI remains `coverage-gate report` (observe only). Revisit after 10 successful post-AQ-04 `main` coverage runs.
