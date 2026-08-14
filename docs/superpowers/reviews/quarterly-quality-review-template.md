# Quarterly / release quality review template

Date:  
Baseline SHA:  
Reviewer:

## 1. Open risks
- [ ] AQ backlog status
- [ ] New fail-open paths?

## 2. Linter exclusions
- [ ] Each `.golangci.yml` exclusion still justified?

## 3. Dependencies / vulns
- [ ] `govulncheck` observe job RESULT=
- [ ] Any required-gate promotion?

## 4. Coverage
- [ ] Count of post-AQ-04 `main` coverage successes: N=
- [ ] Relative gate (≥10)? enabled / **not enabled**
- [ ] Fixed threshold (≥30)? enabled / **not enabled**

## 5. Benchmarks
- [ ] `BenchmarkSafeName` / `BenchmarkExtractZipNestedIndex` trend

## 6. Large files
- [ ] TUI/Web/runtime hotspots still acceptable?

## Outcome
Actions / follow-ups:
