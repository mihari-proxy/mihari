# Design Document Review: Mihari 版本通道 main/dev (round 2)

Reviewed: `docs/superpowers/specs/2026-08-24-mihari-version-channel-design.md`
Worktree: `.worktrees/issue-125-mihari-channel`
HEAD: `cd180e3` (`feat/125-mihari-version-channel`), matches `origin/dev`; `origin/main` is `9eb47d9` (diverged). Uncommitted files are the design plus the round-1 review.
Issue: https://github.com/mihari-proxy/mihari/issues/125
Prior review: `docs/superpowers/reviews/2026-08-24-mihari-version-channel-design-review.md`

### Summary

Needs revision. Round-1 criticals are closed on the failure modes they named (elevated *read* of the sidecar, POSIX compact JSON), and the product additions (AIO `--channel`, README dual blocks, PR → `dev`) are specified and match #125. Two majors still block a safe implement-from-the-doc pass: Unix script/PowerShell argv contracts, and the write-side of the new `SUDO_USER` data-root rule (global `DefaultDataRoot` blast radius plus root-owned `0600` sidecar).

### Issue 1: `DefaultDataRoot` + `SUDO_USER` specifies the read path, not the write path
- **Severity**: major
- **Section**: §6.1, §10.1–10.2, §12, Key Decision 6, §13 Unix 数据根
- **Description**: §6.1 correctly closes the round-1 critical: elevated `self update` must not resolve `/root/.mihari/mihari-channel`, treat a missing file as `main`, and hit `/releases/latest`. The chosen rule is `MIHARI_DATA` → Unix euid 0 + `SUDO_USER` passwd home → `UserHomeDir`, “落在 `platform.DefaultDataRoot` 的 Unix 实现上”, with “安装脚本、`LoadChannel`/`SaveChannel`、daemon 数据根一致”.

  That over-unifies, and the write side is missing.

  1. **Global `DefaultDataRoot` is not channel-only.** Today `DefaultDataRoot()` is `MIHARI_DATA` else `os.UserHomeDir()+"/.mihari"` (`internal/platform/paths.go`). Callers include `DefaultPaths()` (settings, token, core, GeoIP, logs), `AbsoluteDataRoot()` → `installEnvVars()` (`internal/service/service.go`), and the Unix control-socket fallback `filepath.Join(platform.DefaultDataRoot(), "control.sock")` (`internal/control/transport/unix.go`, used when `XDG_RUNTIME_DIR` is unset — typical under `sudo`). Putting `SUDO_USER` here changes every elevated CLI/TUI/daemon start, not just sidecar I/O.

  2. **“本规则不改变服务模式” is false for first-time `sudo mihari service install`.** `installEnvVars` pins whatever `AbsoluteDataRoot()` returns *at install time*. `install.sh` / `install-aio.sh` already do `$SUDO … service install` with no `MIHARI_DATA`. Under Linux `sudo` that is currently the elevated process’s `UserHomeDir` (`/root/.mihari`). After §6.1 it becomes the invoking user’s home. That may be the *intended* architecture (`docs/architecture.md` already claims the unit shares the desktop tree), but it is a behavior change, not a no-op. Already-installed units keep their pinned env; new installs do not.

  3. **Root writer + `0600` sidecar locks the user out.** `SaveChannel` must call `config.AtomicWrite(..., 0o600)`. `AtomicWrite` does `MkdirAll(dir, 0700)` then writes a new file as the current euid (`internal/config/atomic.go`). Channel switch is *not* elevated (TUI “不检查 `isElevated()`”; `self channel` “不提权”), but Update Mihari / `self update` **are**, and an elevated TUI can still hit the channel row. `curl | sudo bash -s -- --channel dev` runs the whole installer as root. Result: `{user home}/.mihari/mihari-channel` owned by root, mode `0600`. Unprivileged `LoadChannel` then gets a permission error → CLI `ExitData`, TUI channel row Failed, Update “不猜 main”. That is a worse user-visible failure than a missing file (which defaults to `main`). Scripts: Unix `mktemp` + `mv -f` + `0600` also do not `chown` to `SUDO_USER`.

  4. **Lookup failure and injection are unnamed.** There is no `os/user` usage in this tree today. `user.Lookup` with `CGO_ENABLED=0` reads `/etc/passwd` only (LDAP/`nss` users miss). If lookup fails, does the resolver fall through to `UserHomeDir` (the original bug) or error? Tests “注入 lookup” is right; the production function must take a *username* and return an absolute home or error. Concatenating `SUDO_USER` into a path (`/home/$SUDO_USER`) is a traversal/injection bug the tests would then be tempted to rely on.

  5. **Script `DATA_DIR` vs sidecar.** `install-aio.sh` uses `DATA_DIR="${MIHARI_DATA:-$HOME/.mihari}"` for core/GeoIP. If only the new sidecar write follows §6.1 while overlay stays `$HOME`, `sudo sh install-aio.sh --channel dev` splits core into `/root/.mihari` and the channel file into the user’s tree.

- **Suggestion**: Either (A) keep `DefaultDataRoot` as it is and resolve `SUDO_USER` only for Mihari-channel I/O (CLI/TUI `LoadChannel`/`SaveChannel` and install-script sidecar writes), or (B) keep the global change but document it as an intentional sudo data-root fix, with tests for `sudo service install` pinning the invoking user. In both cases: on Unix euid 0 writes into a `SUDO_USER` tree, `chown` the sidecar (and any newly created `~/.mihari`) to that user; if passwd lookup fails, error (do not silently use `/root`); never path-join the raw `SUDO_USER` string; put the Unix branch in `_unix.go`; inject lookup *and* euid in `internal/platform` tests (not only `internal/update`). Apply the same resolver to script `DATA_DIR`, not only the channel file.
- **Status**: open

### Issue 2: `--channel` argv grammar is not implementable without breaking existing call styles
- **Severity**: major
- **Section**: §10, §10.1–10.3, §11 README, Key Decision 9
- **Description**: The product wants `--channel` as “文档主写法”, and the rebuilt spec names the right README forms. It does not specify how three Unix scripts that *today have incompatible parsers* should accept those forms, and it invites a PowerShell `param()` that would break the existing Windows one-liner.

  Verified current parsers:

  - `install.sh` has **no argv loop**. Download URL is only `MIHARI_VERSION` vs `/releases/latest/download/`. `curl | bash` does not put words after `|` into the script unless the README form `bash -s -- --channel dev` is used.
  - `install-aio.sh` is `bundle_dir="${1:-$(cd "$(dirname "$0")" && pwd)}"` (line 20). README’s `sh install-aio.sh --channel dev` would set `bundle_dir=--channel` and fail the “mihari binary not found” check. Remote today is `sh "${workdir}/install-aio.sh" "$workdir"` (`install-aio-remote.sh:264`). Both styles must keep working.
  - `install-aio-remote.sh` uses `for arg in "$@"; do case … --yes|-y) … *) ;; esac` (lines 23–28). Unknown tokens are **ignored**. `--channel dev` is two tokens; this loop cannot consume a value, and a naive `case --channel)` without `shift` will not fail `dev` before `fetch "$INDEX_URL"`.
  - `install.ps1` has **no `param()`** and is documented as `irm | iex`. `install-aio.ps1` already has `param([string]$BundleDir = $PSScriptRoot)`. `install-aio-remote.ps1` has `param([switch]$Yes)` and hands off with `& ([scriptblock]::Create(…)) -BundleDir $workdir` (line 287) — a different invocation than `irm | iex`.

  Unspecified, but load-bearing:

  - `--channel VALUE` vs `--channel=VALUE`; `--channel` with no operand; extra positionals; unknown flags (today remote ignores them; illegal *values* must fail).
  - Canonical script-3 → script-2 handoff: `sh install-aio.sh --channel main "$workdir"` vs `"$workdir" --channel main`. Pick one (or require both) and test it.
  - `install.ps1`: adding `param([string]$Channel)` — the obvious way to accept `-Channel` — is invalid at the start of an `iex` string and would break the *existing* `irm …/install.ps1 | iex` one-liner for everyone, not just channel users. Env-only for `irm | iex` is stated; the `-File` path needs `$args` (or equivalent) **without** introducing `param()`.
  - `install-aio-remote.ps1` *can* grow `-Channel` on its existing `param()` because it is invoked as a scriptblock, not `irm | iex`. The design does not draw that distinction, so an implementer may “make install.ps1 match”.

- **Suggestion**: Write an argv contract per script and lock it with fixtures:

  - `install.sh`: `while`/`shift`; accept `--channel main|dev` and `--channel=…`; unknown flags and missing values → non-zero before any download. Document that `curl | bash` **requires** `bash -s --`.
  - `install-aio.sh`: flags and at most one positional `bundle_dir`, order-independent; default dir remains the script directory. Script 3 passes one canonical form, e.g. `sh install-aio.sh --channel "$channel" "$workdir"` when the channel is explicit, else keep today’s `sh install-aio.sh "$workdir"`.
  - `install-aio-remote.sh`: replace the `for arg` loop with `shift` so `--yes` and `--channel` compose; `--channel dev` / `MIHARI_CHANNEL=dev` exit before `fetch` of `index.txt` or `MIHARI_BUNDLE_URL`.
  - `install.ps1`: **no `param()`**; `$env:MIHARI_CHANNEL` for `irm | iex`; `-Channel` only via `$args` when launched with `-File`.
  - `install-aio.ps1` / `install-aio-remote.ps1`: extend existing `param()`; remote handoff adds `-Channel` next to `-BundleDir` when explicit.
- **Status**: open

### Issue 3: POSIX/PowerShell `Link: rel="next"` parser is still unspecified
- **Severity**: minor
- **Section**: §5.5, §10.1
- **Description**: Round-1 Issue 6 (Go paginates, scripts did not) is fixed as *policy*: both walk `per_page=100` and at most 5 `Link: rel="next"` pages. The scripts still have no recipe. `install.sh` `fetch()` is `curl -fsSL` / `wget -qO-` (body only). GitHub sends `Link: <url>; rel="next", <url>; rel="last"`. A naive grep of `http` from that header can follow `rel="last"` or stall. `install-aio-remote.sh` already parses response headers with `tr`/`awk` for `Content-Range`; that is the pattern to name, plus PowerShell `Invoke-WebRequest.Headers['Link']`. wget vs curl header capture differs.
- **Suggestion**: Specify: capture headers separately from the body; take only the URI whose param is `rel="next"`; stop when absent or after 5 pages; keep the 2 MiB body cap per page; on header/parse failure do not fall back to `/releases/latest`. One compact-JSON multi-page fixture for script 1 is enough.
- **Status**: open

### Issue 4: 2 MiB cap was sized for one `/releases/latest` document, not a 100-item list page
- **Severity**: minor
- **Section**: §5.5, §12
- **Description**: `maxReleaseResponseSize = 2 << 20` (`internal/update/self.go`) applies to a single latest-release JSON. `GET /repos/…/releases?per_page=100` returns full objects including `body`. A page of 100 notes can exceed 2 MiB; Check/Update on `dev` then fail closed with `CodeDataFailure` even when canonical tags exist. Scripts told to “单页仍受现有 2 MiB 限制” inherit the same trap. GitHub list responses cannot omit `body`.
- **Suggestion**: Use a larger cap for list pages (and the same cap in the scripts), or a smaller `per_page` with the 5-page budget unchanged. Add a fixture whose first page is over-size and expect a clean error, not a truncated JSON parse.
- **Status**: open

### Issue 5: `--json` `self update` still cannot distinguish ahead from up to date
- **Severity**: nit
- **Section**: §8
- **Description**: Text output has a dedicated ahead line. JSON “保留 `schema` / `version` / `updated`，增加 `channel`。ahead 时 `updated: false`” — the same shape as skip-same-tag today (`internal/cli/self.go`). TUI gets `CheckResult.Ahead` / `Result.Ahead`. A JSON client cannot tell ahead from up to date without reimplementing §5.2.
- **Suggestion**: Add `"ahead": true|false` beside `updated`, or state that JSON callers must compare tags themselves.
- **Status**: open

### Strengths
- Round-1 criticals on compact JSON and elevated *read* of the sidecar are actually written into the spec: whitespace-optional `"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)"`, full-match of the no-leading-zero regex, compact fixtures, draft filtering documented as best-effort for POSIX; `SUDO_USER` passwd home (not `$HOME`) for Unix euid 0.
- #125 hard constraints still hold: default `main`, no rolling `dev` tag, names are not `stable`, Core `settings.core-channel` / `bin/core-channel` two-line stamp untouched, no `/v1` DTO, no AList P2, daemon still does not replace the Mihari binary, `CGO_ENABLED=0` / no TCP control plane unchanged.
- AIO is in scope this issue: script 2 `--channel` only writes `mihari-channel`; script 3 `--channel dev` fails *before* stable `index.txt` / `MIHARI_BUNDLE_URL` download; handoff gains `--channel` when explicit; README must not advertise `/mihari-release/mihari-dev`.
- README dual labeled blocks are specific enough to implement, including `dev` raw URLs (because this PR lands on `dev` first) and `$env:MIHARI_CHANNEL` for `irm | iex` vs Unix `bash -s -- --channel dev`.
- Check/Update never open the sidecar; `LoadChannel` / `SaveChannel` are the only file API; `""`/`"main"` → `/releases/latest` only as an *argument* meaning, not as a guess on illegal files. `config.AtomicWrite(..., 0o600)` is named; Windows `MoveFileEx` is the existing helper.
- Three-state model is now implementable: eval order sameTag → unparseable-current → canonical compare; `v0.9.0` vs `v0.9.0-dev.3` on `dev` is ahead; skip path must copy `Ahead`; `mihariUpdateRow` is three-way; ahead Enter rechecks and does not emit `ActionUpdateMihari`.
- TUI wiring is explicit (`RequiresDaemon == false`, `RequiresConfirmation`, `knownAction`, `rowProgressForAction`, clear pending before `selfCheckGeneration++`), with a policy-test twin of `TestSelfUpdatePolicyIsConfirmedAndDaemonIndependent`.
- Install tests are a new harness, not `test_parallel_download.py` / `MIHARI_INSTALL_TEST_MODE` on script 1. Unspecified reinstall leaves sidecar. `MIHARI_VERSION` pin does not write channel. PR target is `dev`; worktree HEAD is `origin/dev`.

### Previously raised issues (round 1)

| # | Title | Status |
| --- | --- | --- |
| 1 | Elevated CLI/TUI self-update misses user sidecar on Unix | **addressed** for the original miss-sidecar → `/releases/latest` failure (resolver in §6.1). Write-side ownership, global `DefaultDataRoot` blast radius, and lookup failure are new Issue 1. |
| 2 | POSIX grep of GitHub compact JSON | **addressed**. Optional whitespace, full-match, compact fixture, draft = best-effort. |
| 3 | Install tests cannot land on `test_parallel_download.py` | **addressed**. New script 1/2 harness; script 3 `dev` zero-download case named. |
| 4 | Windows overwrite needs `config.AtomicWrite` | **addressed**. Helper named; scripts `mktemp`/`mv -f` and `Move-Item -Force`; no POSIX `0600` ACL claim on Windows. |
| 5 | Check/Update channel param vs sidecar ownership | **addressed**. Never open the file; illegal sidecar errors before Check. |
| 6 | Script vs Go pagination | **partially addressed**. Same 5×100 policy; Link-header *parser* still missing (this round Issue 3). |
| 7 | `Available` / Ahead TUI blast radius | **addressed**. Three-way row, `Result.Ahead`, skip-path copy, tests listed. |
| 8 | TUI action wiring / pending / policy test | **addressed**. |
| 9 | `v0.9.0` vs `v0.9.0-dev.3`, sameTag order, unparseable current | **addressed**. Table + eval order + → `dev` confirmation copy. |
| 10 | One-PR plan vs test surface | **addressed**. Four-PR stack is the written fallback, still all into `dev`. |
| 11 | Error codes, 403, root-owned dir | **partially addressed**. `CodeDataFailure` / `CodeNetworkFailure` / 403 named. Root-owned *sidecar file* after elevated write is this round Issue 1. |
| 12 | Optional `--channel` vs `install.sh` having no argv loop | **addressed** as a product requirement; the grammar itself is this round Issue 2. |

Verified against this worktree (not taken from the doc): `internal/update/self.go` (`Available: !sameTag`, single `/releases/latest`, no `Draft`/`Ahead`, 2 MiB cap); `internal/cli/self.go` + `exit.go`; `internal/platform/paths.go` (no `MihariChannel`, `defaultDataRoot` = `UserHomeDir`); `internal/config/atomic.go` + `atomic_windows.go`; `internal/config/settings.go` `ApplyCoreChannelSidecar` (two-line `stable`/`alpha` stamp); `internal/tui/actions.go` + `policy_test.go`; System `rows()` / `mihariUpdateRow` / `checkMihariVersion` (`m.pending` early return) / skip overwrite of `CheckResult`; `internal/service/service.go` `installEnvVars`; `internal/control/transport/unix.go` socket fallback; `internal/elevate` (no auto-relaunch); `scripts/install/install.sh` (no argv, no data dir); `install.ps1` (no `param()`, `irm \| iex`); `install-aio.sh` (`$1` = bundle_dir, `DATA_DIR=$HOME/.mihari`, copies `bin/core-channel`); `install-aio.ps1` (`param(-BundleDir)`); `install-aio-remote.sh` (`for` `--yes` only, compact `"version"` sed, handoff `sh install-aio.sh "$workdir"`); `install-aio-remote.ps1` (`param(-Yes)`, handoff `-BundleDir`); `scripts/release_policy.py` (canonical regexes); `scripts/github_release_policy.py` (stable-only regex); `README.md` / `README.zh-CN.md` (single unlabeled Install block; offline AList `install-aio-remote` only).
