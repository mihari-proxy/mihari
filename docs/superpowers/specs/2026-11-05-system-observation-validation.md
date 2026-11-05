## Ubuntu VM validation — 2026-11-05

Compared PR #369 baseline b48ad7e with this change on the same Ubuntu VM, one root TUI on Overview, 140×40 terminal. Each foreground CPU sample ran for 60 seconds after an 8-second warm-up. 100% CPU denotes one logical CPU. This is one diagnostic sample, not a benchmark guarantee.

| Measurement | Baseline | Optimized defaults |
| --- | ---: | ---: |
| daemon CPU | 4.28% | 2.85% |
| TUI CPU | 11.83% | 14.37% |
| mihomo CPU | 0.83% | 1.08% |
| /proc/*/comm reads over 8 seconds | 2,504 | 1,292 |
| controller owner fd readlinks over 8 seconds | 8,317 | 0 |

The daemon sample decreased by about 33%; TUI CPU did not improve in this run. Other TUI polling and credential-validation work remains in #367 and #368. Filesystem counts were collected separately with strace, outside the CPU interval. Post-install idle CPU was sampled immediately after restart and is not compared with the settled baseline.

The candidate was installed through the existing offline-pinned installer transaction. Candidate Linux amd64 SHA256: 6d27ad081144863482c6b97e93017c893d9ed0f899e28a8ff314a2d0dcb8c0e3. It retains the dev.7 version string solely for the test installer; the hash distinguishes the candidate from the baseline.

Verified daemon/core health, groups, rules, connections, subscriptions, panels, TUN, egress, system proxy, service status, traffic stream, and ordinary-user IPC access. The real TUI saved owner detection on with reuse 5, then off with reuse 0; original preferences were restored. Pages 2–8 rendered and the TUI exited normally.

Local validation: full Go tests and race tests, vet, golangci-lint, six CGO-free target builds, Linux native runs of the six affected test binaries, and Unix layout security tests (74 passed, 4 skipped). Linux test binaries were cross-compiled on Windows and executed under WSL from their package directories.
