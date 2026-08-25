# Design Document Review: Mihari 版本通道 main/dev

Reviewed: `docs/superpowers/specs/2026-08-24-mihari-version-channel-design.md`
Worktree: `.worktrees/issue-125-mihari-channel` (same commit as `origin/main` plus the uncommitted design doc)
Issue: https://github.com/mihari-proxy/mihari/issues/125

### Summary

Needs revision. The product shape is right and matches #125 (sidecar, `main`/`dev`, no `/v1`, no Core mixing, no rolling `dev` tag, no AList P2, daemon still does not replace the Mihari binary). Two critical gaps would recreate the failure the issue exists to prevent: Unix elevation resolving the sidecar under `/root`, and POSIX grep of GitHub compact JSON that will not actually find `tag_name`. Several major implementation contracts are also underspecified.

### Issue 1: Elevated CLI/TUI self-update will miss the user sidecar on Unix
- **Severity**: critical
- **Section**: §6 Persistence, §8 CLI, §10 install scripts (“Unix 用调用用户的 HOME 写，不要 sudo 写到 `/root`”)
- **Description**: Channel state lives at `{dataRoot}/mihari-channel` via `platform.Paths` / `DefaultDataRoot()` (`MIHARI_DATA` else `os.UserHomeDir()+"/.mihari"`). That matches today’s data layout. The design correctly tells *install scripts* not to sudo-write `/root`, but `mihari self update` and TUI `Update Mihari` **already require elevation** (`elevate.RequireElevated`; Unix is `geteuid()==0`; TUI message is “re-run Mihari from an elevated shell”). Mihari does not auto-relaunch with sudo/UAC.

  On a default Unix install the binary is `/usr/local/bin/mihari` (needs root to replace) while data is `~/.mihari`. The intended flow is therefore:

  1. unprivileged `mihari self channel dev` / TUI channel row writes `$HOME/.mihari/mihari-channel`;
  2. `sudo mihari self update` or an elevated TUI reads `DefaultDataRoot()`.

  `sudo` typically sets `HOME=/root` and resets the environment, so step 2 sees a missing sidecar, treats that as `main` (§5.1), and follows `/releases/latest`. That is exactly “装了 dev 下次又回到 latest” from §17.

  Windows UAC usually keeps `%USERPROFILE%`, so this is primarily a Unix bug. Service install already pins `MIHARI_DATA` into the unit (`internal/service/service.go` `installEnvVars`); CLI/TUI do not read that unit. The design never specifies a shared data-root rule for *elevated* Go code.

- **Suggestion**: Define one resolver used by install scripts, `self channel`, TUI, and `Check`/`Update` sidecar I/O, for example: `MIHARI_DATA` if set (including through sudo) → else if euid 0 and `SUDO_USER` is set, that user’s home `/.mihari` → else `UserHomeDir`. Cover `sudo mihari self update` after an unprivileged `self channel dev` in tests (inject the resolver; do not touch a real home). Document that users who elevate without `SUDO_USER` must export `MIHARI_DATA`.
- **Status**: open

### Issue 2: POSIX grep of GitHub JSON as specified will not find canonical-dev tags
- **Severity**: critical
- **Section**: §4.4, §10 “Unix：curl/wget 取 JSON，POSIX `grep`/`sed` 抽出 `"tag_name": "vX.Y.Z-dev.N"`”
- **Description**: GitHub’s REST body is compact JSON: `"tag_name":"v0.9.0-dev.3"` with **no space** after `:`. The quoted pattern requires a space and will match zero tags on a real `/releases` response, so `MIHARI_CHANNEL=dev` always fails closed. The repo already knows compact JSON: `install-aio-remote.sh` parses `mihari self version --json` with `"version"[[:space:]]*:[[:space:]]*"\([^"]*\)"`.

  Even with optional whitespace, naive grep cannot bind `tag_name` to the same object’s `draft` field, and `"tag_name"` can appear inside escaped `body`/`name` strings (`\"tag_name\":...`). Unauthenticated API omits drafts, so the `draft != true` rule in §5.3 is mostly a no-op for install.sh unless a token is present; it is still not implementable with the specified grep.

- **Suggestion**: Specify a whitespace-optional extractor aligned with the existing `install-aio-remote.sh` sed, require the tag to *full-match* the §5.2 regex (reject `v0.9.0-dev.3-rc.1`), and treat draft filtering as best-effort for unauthenticated curl (document it). Add a fixture of *compact* GitHub list JSON, not pretty-printed. If draft-safe parsing is required, that is a different design (python3 was rejected; a small awk object scanner or “Go prints the tag, scripts only download” would need to be chosen explicitly).
- **Status**: open

### Issue 3: Install-script tests cannot be added to the existing `MIHARI_INSTALL_TEST_MODE` harness
- **Severity**: major
- **Section**: §13 “安装脚本（现有 `MIHARI_INSTALL_TEST_MODE` / 脚本测试）”
- **Description**: `MIHARI_INSTALL_TEST_MODE` exists only in `scripts/install/install-aio-remote.sh` (early `return 0` after defining the downloader) and `install-aio-remote.ps1`. `scripts/install/test_parallel_download.py` only sources those files and calls `download_file_with_progress` / `Download-FileWithProgress` against a local range server. It does not exercise `install.sh` / `install.ps1` URL construction, GitHub list parsing, sidecar writes, `MIHARI_VERSION` precedence, or illegal-channel failure.

  `install.sh` is a linear script with no functions and no test-mode seam. The five cases listed in §13 have nowhere to land without a new harness.

- **Suggestion**: Add an explicit install-script test plan: either (a) extract `resolve_tag` / `write_channel` functions and a `MIHARI_INSTALL_TEST_MODE=1` early-return in `install.sh` / `install.ps1` (mirroring AIO remote), or (b) a new Python/unittest file that runs the script against `httptest`-style local GitHub JSON and a fake `dl()`. Do not claim coverage via `test_parallel_download.py`. Keep tests off the public internet.
- **Status**: open

### Issue 4: “同目录临时文件 + 原子替换” is not enough on Windows
- **Severity**: major
- **Section**: §6 “写：同目录临时文件 + 原子替换，权限 `0600`”
- **Description**: The repo already has the correct helper: `config.AtomicWrite` uses a same-dir temp file, `Chmod(mode)`, `Sync`, then `replaceFile`. On Windows that is `MoveFileEx(..., MOVEFILE_REPLACE_EXISTING|MOVEFILE_WRITE_THROUGH)` (`internal/config/atomic_windows.go`). Plain `os.Rename` cannot replace an existing destination on Windows. Switching `main`↔`dev` overwrites the sidecar, so a naive rename will fail after the first write.

  The design never names `config.AtomicWrite`. An implementer copying `internal/core/replace_windows.go` (stash-rename) or Unix `os.Rename` will hit this.

- **Suggestion**: Require Go writers to call `config.AtomicWrite(path, []byte(channel+"\n"), 0o600)`. For install scripts, specify Unix `mktemp` in `$DATA_DIR` plus `mv -f`, and PowerShell `Move-Item -Force` from a same-directory temp; do not claim POSIX `0600` ACLs on Windows (user-only default ACL is the equivalent).
- **Status**: open

### Issue 5: Check/Update channel parameter vs sidecar ownership is a footgun
- **Severity**: major
- **Section**: §7 “在现有 `Check` / `Update` 上增加通道参数；空通道与 `"main"` 相同”; §8 “`self update` 读 sidecar”; §9 “检查必须带当前 sidecar 通道”
- **Description**: Today `Check(ctx, currentVersion)` and `Update(ctx, binaryPath, currentVersion)` always hit `/releases/latest` (`internal/update/self.go`). Adding a channel argument is a compile break for:

  - `internal/tui/pages/system.SelfUpdater`
  - `internal/cli.SelfUpdater` (Update only)
  - fakes in `internal/cli/self_test.go`, `internal/tui/pages/system/model_test.go`, `internal/tui/model_test.go`

  That is fine if owned. What is not specified: who opens the sidecar. “空通道与 main 相同” means a caller that forgets to load the file silently tracks main — including TUI `Load()` auto-check (`checkMihariVersion` currently calls `Check(ctx, currentVersion)` with no extra args). §12 says “Update 在通道读失败时不要按 main 猜测”, which contradicts passing `""`.

- **Suggestion**: Export `LoadChannel(path) (string, error)` / `SaveChannel(path, channel) error` as the only sidecar I/O. `Check`/`Update` take an explicit channel and **never** open the file. Empty or `"main"` select `/releases/latest`. CLI/TUI must `LoadChannel` first; illegal file → error, do not call Check. TUI `checkMihariVersion` loads sidecar (or uses the value already shown on the channel row) before Check.
- **Status**: open

### Issue 6: Unix install script and Go updater can pick different dev tags
- **Severity**: major
- **Section**: §5.3 (Go: `per_page=100`, follow `Link` up to 5 pages, compare not list order); §10 (scripts: “curl/wget 取 JSON … 选出最大 canonical”, no pagination)
- **Description**: There is no Link-header pagination in the repo today (`latestRelease` is a single GET `/releases/latest`). Go will grow a 5-page walk. The install script is not told to follow `Link`. GitHub lists newest-first, but a burst of official releases can push the latest canonical-dev off page 1. Then `MIHARI_CHANNEL=dev` install fails “no prerelease” while in-app `self update` on `dev` succeeds (or the reverse if the script takes array order).

  POSIX grep also cannot implement “`draft != true` and tag is canonical-dev, take compare-max” as one pass over objects.

- **Suggestion**: Make the script follow the same `per_page=100` + max-5-pages `Link: rel="next"` policy, or explicitly accept first-page-only and document the `MIHARI_VERSION=` fallback. Specify how to parse GitHub’s `Link` header in POSIX/PowerShell. In Go, add `Draft`/`Prerelease` to the list DTO; after selecting a tag, either use that list item’s assets or `GET /releases/tags/{tag}` so Update still has checksums. Keep `maxReleaseResponseSize` (2 MiB) in mind for a 100-asset page.
- **Status**: open

### Issue 7: `CheckResult.Available` blast radius is larger than the new bool
- **Severity**: major
- **Section**: §5.4, §7, §9 Update row; current `internal/update/self.go` and TUI
- **Description**: Current semantics (verified):

  ```go
  Available: !sameTag(currentVersion, release.TagName)
  ```

  `Update` downloads whenever tags differ — including a newer local build vs older `/releases/latest`. TUI `mihariUpdateRow` maps `Available` → `vX · vY available` and **any** `!Available` → `vX · Up to date` (`UpdateMihariUpToDate = "Up to date"`). Enter opens install confirm only when `Available` (`model.go` around the `rowMihariUpdate` case). After a skip, TUI overwrites:

  ```go
  m.selfCheckResult = update.CheckResult{Current: m.currentVersion, Latest: typed.result.Version, Available: false}
  ```

  Adding `Ahead` without changing that row and that skip path will render ahead as “Up to date”, violating §5.4 (“ahead **不是** Up to date”). `Result` gains `Channel` but not `Ahead`; CLI must still distinguish `already up to date (%s)` vs `current %s is ahead of %s %s` after `Updated == false`.

  Existing tests that stay valid: `TestSelfUpdaterCheckReportsAvailability` (`v1.0.0` vs `v1.1.0` / same tag), TUI available/up-to-date render tests. Missing today: current > latest (today those would be Available and would download).

- **Suggestion**: Keep `Available` as the sole “open install confirm / download” bit (as written). Change `mihariUpdateRow` to a three-way switch on `Available` / `Ahead` / else. Set `Ahead` on the skip path (or add `Ahead` to `update.Result` and copy it). Export the compare helper so CLI can classify skip without re-fetching. Extend `TestSelfUpdaterCheckReportsAvailability` with `v0.9.0-dev.3` vs main `v0.8.2` (ahead) and unparseable `"dev"` (available, not ahead).
- **Status**: open

### Issue 8: TUI action wiring details that will silently mis-gate the new row
- **Severity**: minor
- **Section**: §9; `internal/tui/actions.go`, `rowProgressForAction`, `policy_test.go`
- **Description**: `RequiresDaemon` defaults to **true**. `UpdateMihari` is explicitly false; `SwitchCoreChannel` is true (daemon mutates core). A new `switch-mihari-channel` that is omitted from the false list will show Stale when disconnected and cannot switch. `knownAction` / `RequiresConfirmation` / `rowProgressForAction` must all learn the new action or confirmation never maps to the Mihari channel row (empty `rowID` skips `beginRowPending`). `TestSelfUpdatePolicyIsConfirmedAndDaemonIndependent` and `TestCoreChannelPolicyRequiresConfirmationAndDaemon` are the pattern; §13 does not require a policy test.

  Elevation: design says the channel row needs no admin. That matches writing user data, and matches *not* copying `updateMihari()`’s `isElevated()` check. Do not reuse `ActionSwitchCoreChannel`.

  System page has a single `m.pending` flag: Enter is ignored while a check is in flight, and `checkMihariVersion` no-ops if pending. After a channel write, pending must be cleared **before** bumping `selfCheckGeneration` and re-checking, or the recheck is dropped.
- **Suggestion**: Add a policy test twin of `TestSelfUpdatePolicy`. List the new action in `RequiresConfirmation`, `knownAction`, `RequiresDaemon==false`, and `rowProgressForAction`. Sequence: channel action completes → clear pending / mark Done → generation++ → `checkMihariVersion`.
- **Status**: open

### Issue 9: Just-switched and unparseable cases still have a hole
- **Severity**: minor
- **Section**: §5.2, §5.5
- **Description**: Specified and correct: missing sidecar = main; pin `MIHARI_VERSION=v0.9.0-dev.3` without `MIHARI_CHANNEL` does not write and stays on main (ahead vs `v0.8.2`); switch to `dev` with older `v0.8.2` is available; switch to `main` with `v0.9.0-dev.3` is ahead; `"dev"` / dirty current cannot be ahead and is available if latest exists. `buildinfo.Version = "dev"` is accurate.

  Not spelled out: current official `v0.9.0` vs channel `dev` latest `v0.9.0-dev.3`. Compare rules make this **ahead** (release > `-dev.N` of the same X.Y.Z), so a user who switches to `dev` after that stable ships cannot install a same-series nightly until `v0.9.1-dev.1`. That is consistent with 只升不降 but surprising; TUI impact copy for → `dev` does not mention it.

  `sameTag` is case-insensitive and strips `v`; the canonical regex is case-sensitive and requires a leading `v`. Order of sameTag vs unparseable vs compare is only implied (“`sameTag` 仍用于 up to date”). `"0.8.2"` vs `"v0.8.2"` is sameTag (up to date) but would be unparseable if compare ran first.

- **Suggestion**: State evaluation order: sameTag → up to date; else if current unparseable and latest present → available; else canonical compare. Add the `v0.9.0` vs `v0.9.0-dev.3` on `dev` row to the three-state table. Optionally tighten the regex to `scripts/release_policy.py` (`0|[1-9][0-9]*`, no leading zeros).
- **Status**: open

### Issue 10: One-PR plan is optimistic relative to the real test surface
- **Severity**: minor
- **Section**: §13, §17
- **Description**: The split order (update core → CLI → TUI → scripts/docs) is correct and should be kept if the PR is too large. A single PR is only realistic if install tests get a **new** harness (Issue 3) and TUI work stays to System page + policy/strings. `internal/tui/pages/system/model_test.go` is already the self-update/core-channel suite; ahead render, generation invalidation, and a second confirmation path will grow it a lot. That is not a reason to land scripts before the updater.
- **Suggestion**: Keep the one-PR preference as a product invariant, but treat a four-PR stack in the written order as the fallback, not “if volume is large maybe split”.
- **Status**: open

### Issue 11: A few error and permission paths are unnamed
- **Severity**: minor
- **Section**: §12, §8
- **Description**: Illegal sidecar: TUI channel row Failed, Update must not guess main — good. CLI “非 0” does not pick an error code; `CodeDataFailure` → `ExitData` (9) is the closest existing mapping (`internal/cli/exit.go`). Invalid *argument* to `self channel foo` is correctly `CodeInvalidArgument` / `ExitUsage`.

  Also unnamed: GitHub 403 rate limit (dev Check can be 5 GETs vs today’s 1; unauthenticated cap is 60/hour), sidecar path is a directory, `MIHARI_DATA` not writable after a previous root-owned `~/.mihari`, install.sh `curl | sudo bash` (whole script is root; `SUDO_USER` lookup must match Issue 1).

  Windows install data dir is already `%USERPROFILE%\.mihari` while the binary is `%LOCALAPPDATA%\Programs\mihari`; putting the sidecar in the data root is consistent. Do not write it next to `mihari.exe`.
- **Suggestion**: Map illegal sidecar to `CodeDataFailure`. Cap list response size the same as Go. Mention 403 as `CodeNetworkFailure` without echoing URLs/tokens (already the self-update pattern).
- **Status**: open

### Issue 12: `--channel` flag note does not match `install.sh`
- **Severity**: nit
- **Section**: §10 “脚本若已有 argv 循环，可顺手接受 `--channel`”
- **Description**: `install.sh` / `install.ps1` have no argv loop. `install-aio-remote.sh` loops only for `--yes|-y`. Optional flag work should not be folded into script 1 “while we are here”.
- **Status**: open

### Strengths
- Product constraints from #125 are honored: default `main`, no rolling tag `dev`, names are not `stable`, Core `settings.core-channel` / `bin/core-channel` stamp file stay untouched, `/v1` unchanged, AIO remote `dev` fails instead of reading stable `index.txt`, daemon still does not replace the Mihari binary.
- Sidecar ownership next to self-update (not `mihari.yaml`) is the right boundary: `KnownFields(true)` would break old daemons, and self-update is already a documented exception to “clients only talk through the control protocol”.
- Three-state check (available / up to date / ahead) plus “do not infer channel from the current tag” actually prevent the silent downgrade that `!sameTag` would perform after switching back to `main`.
- Reusing `/releases/latest` for `main` (GitHub already ignores prereleases) and refusing `/releases/tags/dev` matches both GitHub and `internal/update/self.go` today.
- TUI placement (Daemon section, above Update Mihari, not in Core), independent Action/confirmation/chip, `RequiresDaemon == false` like `Update Mihari`, and generation bump after a successful switch match existing System page machinery (`selfCheckGeneration` is already in `checkMihariVersion`).
- No new Go module, no python3/jq on Unix install, no public-internet tests, CGO/TCP/controller invariants called out as non-goals.
- Canonical compare without `golang.org/x/mod` is feasible; `scripts/release_policy.py` already parses the same two tag shapes (stricter than the design regex).
- `MIHARI_VERSION` precedence and “pin without `MIHARI_CHANNEL` does not write sidecar” are precise and testable.

Verified against this worktree (not taken from the doc): `internal/update/self.go` (`Available: !sameTag`, single `/releases/latest`, no Draft field, 2 MiB cap); `internal/cli/self.go` + `exit.go`; `internal/platform/paths.go` (`DefaultDataRoot`, no `MihariChannel` yet, `EnsureDirs` 0700); `internal/config/atomic.go` + `atomic_windows.go`; `internal/config/settings.go` (`core-channel` / `ApplyCoreChannelSidecar` two-line stamp); `internal/tui/actions.go` + System `rows()` / `mihariUpdateRow` / `confirmSwitchCoreChannel`; `cmd/mihari/main.go` self-updater wiring; `internal/buildinfo.Version = "dev"`; `scripts/install/install.sh` (latest URL or `MIHARI_VERSION`, sudo only for bin/service, **no data dir**); `install.ps1` (binary under `LOCALAPPDATA\Programs\mihari`); `install-aio.sh` (`DATA_DIR="${MIHARI_DATA:-$HOME/.mihari"}`, copies `bin/core-channel`); `install-aio-remote.sh` (`MIHARI_INSTALL_TEST_MODE` early return); `scripts/install/test_parallel_download.py` (AIO downloader only).
