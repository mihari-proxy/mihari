# Mihari 版本通道 main/dev 设计

日期：2026-08-24
状态：已批准（第三版：AList P2 已落地 + 审计 round-2 决议）
Issue：https://github.com/mihari-proxy/mihari/issues/125
基线：`origin/dev`（`2a7edec`，含 #128/#129/#130）
目标分支：`feat/125-mihari-version-channel`
PR 目标：`dev`（再晋级 `main`）
工作目录：`.worktrees/issue-125-mihari-channel`

## 第三版相对第二版的变更

1. **AList P2 已在 `dev` 落地。** 公开 dev index 为 `https://cloud.xn--30q18ry71c.com/p/public/mihari-release/mihari-dev/index.txt`。脚本 3 `--channel dev` 改为读该 index，不再失败退出。
2. **GitHub 在线安装仍必须解析 Releases。** AList `index.txt` 只服务脚本 3（AIO）。脚本 1 / CLI / TUI 自更新仍走 GitHub；`dev` 通道没有 `/releases/latest` 等价物，必须 list + 选最大 canonical `vX.Y.Z-dev.N`。
3. **通道 I/O 单独解析 `SUDO_USER`。** 不改全局 `platform.DefaultDataRoot`。root 写入时 chown sidecar 与本次新建的父目录；lookup 失败报错；禁止把原始 `SUDO_USER` 拼进路径。
4. **按脚本写死 argv。** Unix `--channel VALUE` 与 `--channel=VALUE`；未知/缺值在下载前失败。`install.ps1` 不加 `param()`。脚本 2 的 flag 与一个位置 `bundle_dir` 顺序无关。脚本 3 显式通道交接：`sh install-aio.sh --channel "$channel" "$workdir"`。
5. **GitHub list 分页：** 头与体分开；只跟 `rel="next"`；最多 5 页；不回退 `/releases/latest`。列表页 8 MiB；`/releases/latest` 与单 tag 仍 2 MiB。
6. **`self update --json` 增加 `"ahead": true|false`。**

## 1. 背景

#115 把发版拆成两条线：`main` 出正式 `vX.Y.Z`，`dev` 出 `vX.Y.Z-dev.N` prerelease。#126（P2）已在 `origin/dev` 接入独立 AList index 与 `retract-dev.yml`。消费端（安装脚本、CLI、TUI）还没跟上，这是本 issue。

当前代码（本 worktree / `origin/dev` @ `2a7edec` 已核对）：

- `internal/update.Check` / `Update` 只请求 `/releases/latest`，`Available` 为 `!sameTag`。tag 不同就会下载，包括把更新的 dev 构建装回更旧的正式 latest。
- `install.sh` / `install.ps1` 只有 `/releases/latest/download/` 或 `MIHARI_VERSION` 直链，不解析 JSON，没有通道参数。
- `install-aio-remote` 默认读稳定 AList `index.txt`，已支持 `MIHARI_INDEX_URL` 覆盖；解压后 `sh install-aio.sh "$workdir"`，不传通道。dev 根目录**没有** `install-aio-remote.sh/.ps1`。
- `install-aio.sh` 覆盖 `data/bin/core-channel`，不写 Mihari 通道，也不碰 `mihari.yaml`。
- System 页 Core 行是 mihomo 的 `stable` / `alpha`（`settings.core-channel`），与本 issue 无关。
- `platform.DefaultDataRoot()` 用 `MIHARI_DATA` 或 `os.UserHomeDir()+"/.mihari"`。`sudo` 后 `HOME`/`UserHomeDir` 变成 root。
- `config.AtomicWrite` 已处理 Windows `MoveFileEx`。自更新 CLI 必须提权（Unix `geteuid()==0`）。

发版现场：

| 通道 | GitHub | AList |
| --- | --- | --- |
| `main` | `/releases/latest` → 正式 `vX.Y.Z` | `/mihari-release/mihari/index.txt`，`latest` 为 `vX.Y.Z` |
| `dev` | prerelease，`make_latest=false`；无 latest-prerelease URL | `/mihari-release/mihari-dev/index.txt`，`latest` 只能是 `vX.Y.Z-dev.N`。历史 `v0.9.0-dev.2/.3` 不回溯；AList 从 P2 之后的新版本开始 |

通道名固定 **`main` / `dev`**，禁止叫 `stable`。

## 2. 目标

- 通道只有 `main`（默认）和 `dev`。没选过通道的用户，安装和更新与现在一样。
- GitHub 在线安装能装当前最新 canonical `vX.Y.Z-dev.N`，不经过 `/releases/latest`。
- AList 离线安装能按通道读对应 `index.txt`：`dev` 读公开 dev index，`main`/缺省仍读稳定 index。
- `MIHARI_VERSION` 钉 tag 优先于通道（脚本 1）。
- 四个安装脚本都接受 **`--channel main|dev`**（Windows 文件调用用 `-Channel`；`irm | iex` 用环境变量）。显式通道写入 sidecar，供之后的 `self update` / TUI 跟踪。
- README 快速开始用两个并列代码块：**main release 通道** 与 **dev release 通道**。GitHub 在线与 AList 离线各一套。
- CLI 和 TUI 能查看、切换通道，并按通道检查/更新。
- 切通道只改跟踪，不换二进制。失配是一等状态（available / up to date / ahead），不得把 dev 二进制显示成已经在 main 正式线上。
- 只升不降：通道 latest 新于当前才安装。
- 不改 `/v1`、不写 `mihari.yaml`、daemon 仍不替换 Mihari 二进制。
- 变更从 `dev` 合入，不打 `main`。

## 3. 非目标

- 不改 AList writer / `release-dev.yml` / `retract-dev.yml` / index 格式（#126 已完成）。
- 不把 `install-aio-remote.sh/.ps1` 上传到 `/mihari-release/mihari-dev/`（#126 已禁止；下载器始终从稳定根取）。
- 不为历史 `v0.9.0-dev.2/.3` 回溯写 AList。
- 不让 CLI/TUI 自更新走 AList。AList 版本目录只有 AIO 包，没有独立 `mihari-<os>-<arch>` 资产。
- 不引入滚动 GitHub tag `dev`。
- 不把 Mihari 通道做成第二个 Core 通道。
- 不改 `/v1` DTO；不把通道放进 daemon settings。
- 不按当前二进制 tag 或 `MIHARI_VERSION` 推断并反写通道。
- 切通道后不自动下载、不自动弹出更新确认。
- 不把 `SUDO_USER` 解析扩到全局 `DefaultDataRoot`（服务安装、control socket、settings 路径保持现状）。
- 不改 `CGO_ENABLED=0`、loopback controller、控制面 TCP、daemon 单写入者。
- 不新增 Go 模块（不为 semver 引入 `golang.org/x/mod`）。
- Unix 安装脚本不依赖 python3 / jq。
- 不测真实 GitHub、真实 AList、真实订阅、系统服务安装。

## 4. 方案比较

### 4.1 采用：sidecar + 扩展 `internal/update` + 脚本 `--channel` + 双源 latest

通道文件在数据目录。`internal/update` 负责读写、按通道取 **GitHub** latest、canonical 比较、Check/Update。CLI / TUI / 脚本 1 共用比较规则。脚本 3 不解析 GitHub：读 AList `index.txt` 的 `latest` 行。自更新本来就不走 daemon，通道跟更新器走，不跟 `settings.core-channel`。

### 4.2 不采用：写入 `mihari.yaml` / `/v1`

与「不要改 mihari.yaml / 能不改协议就不改」冲突。`KnownFields(true)` 会让旧 daemon 读不了新 settings。内核通道走 settings 是因为 core 安装由 daemon 提交。

### 4.3 不采用：滚动 tag `dev`

issue 禁止。

### 4.4 不采用：Unix 安装脚本依赖 python3

现有 `install.sh` 只拼直链。查 latest dev 是新能力，不是修旧缺陷；保持 POSIX。

### 4.5 不采用：自更新改走 AList

AList 没有独立二进制；把 AIO 解包进 `self update` 会把 Core/GeoIP 覆盖语义缠进来。墙内自更新仍是后续议题，不是本 issue。

## 5. 核心模型

### 5.1 通道与二进制

| 对象 | 含义 | 谁写 |
| --- | --- | --- |
| sidecar 通道 | 下次检查/更新跟踪哪条线 | 安装脚本（仅显式 `--channel` / `MIHARI_CHANNEL`）、`mihari self channel`、TUI 通道行 |
| 当前二进制 | `buildinfo.Version`，界面永远显示真实 tag | 安装或 `self update` / `Update Mihari` |

切通道 = 只写 sidecar。刚切完、二进制还没动，是预期状态。

缺 sidecar = `main`。

钉 `MIHARI_VERSION` 但不设通道：**不写、不改** sidecar。即使钉的是 `v0.9.0-dev.3`，下次仍跟 `main`；若当前已新于 main latest 则 ahead。

未指定通道的重装 / AIO 落地：**不动**已有 sidecar（既不写也不删）。

### 5.2 Canonical tag

与 `scripts/github_release_policy.py` / `scripts/release_policy.py` 对齐，禁止前导零：

- 正式：`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`
- dev：`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)-dev\.(0|[1-9][0-9]*)$`

其它一律不选：`dev`、`v0.9.0-dev`、`v0.9.0-dev.3-rc.1`、draft、滚动名。

比较（不引入新依赖）：

1. 比 `X.Y.Z` 数值。
2. 同 `X.Y.Z`：无 `-dev` 的正式版 **大于** 任意 `-dev.N`。
3. 双方都是 `-dev`：比 `N` 数值（`dev.10` > `dev.9`）。

因此 `v0.9.0-dev.3` > `v0.8.2`；`v0.9.0` > `v0.9.0-dev.3`。

### 5.3 判定顺序

对「当前版本」和「该通道 latest」：

1. `sameTag`（现有：去空白、忽略大小写、去掉一个前导 `v`）→ **up to date**。因此 `"0.8.2"` 与 `"v0.8.2"` 视为相同。
2. 否则当前无法解析为 §5.2 两种形式（本地默认 `"dev"`、脏字符串），且 latest 存在 → **available**，不能标 ahead。
3. 否则按 §5.2 比较：latest > current → **available**；current > latest → **ahead**；相等（已被 sameTag 覆盖）→ up to date。

### 5.4 三态

| 状态 | 例子 | TUI Update 行 | 更新 |
| --- | --- | --- | --- |
| available | 当前 `v0.8.2`，dev latest `v0.9.0-dev.3` | `v0.8.2 · v0.9.0-dev.3 available` | 允许安装 latest |
| up to date | 当前与通道 latest sameTag | `v0.8.2 · Up to date` | 不下载 |
| ahead | 当前 `v0.9.0-dev.3`，main latest `v0.8.2` | `v0.9.0-dev.3 · ahead of main v0.8.2` | 不下载、不打开安装确认 |
| ahead | 当前正式 `v0.9.0`，dev latest `v0.9.0-dev.3` | `v0.9.0 · ahead of dev v0.9.0-dev.3` | 同版本正式版大于 `-dev.N`，切到 `dev` 后要等 `v0.9.1-dev.1` 才 available |

ahead **不是** Failed，也 **不是** Up to date。

这会改变 **main 通道** 今天的「tag 不同就装」：比 `/releases/latest` 新的构建变为 ahead，不再被装回去。有意为之。

切到 `dev` 的确认文案要提到：若当前已是同系列或更高正式版，可能不会立刻看到可装的 nightly。

### 5.5 按通道取 latest（GitHub：Go + 脚本 1）

AList `index.txt` **不是** 这条路径。本小节只服务 `internal/update` 与脚本 1。

- `main`：现有 `GET /repos/{repo}/releases/latest`。不得改成列表再挑正式版。响应上限 **2 MiB**。
- `dev`：`GET /repos/{repo}/releases?per_page=100`，跟随 `Link: rel="next"` 最多 5 页。只收 `draft != true` 且 tag 为 canonical dev 的项，取比较最大者。列表顺序不当答案。零命中 → 错误，不回退 `/releases/latest`。
- 不要请求 `/releases/tags/dev`。
- Go 列出后用该条目的 assets；若分页条目没有完整 assets，再 `GET /releases/tags/{tag}`（单 tag 上限仍 2 MiB）。
- 未认证 API 通常不含 draft。Go 仍过滤 `draft`。安装脚本的 POSIX 抽取做不到按对象绑 `draft`，以 tag 全匹配 canonical 为准，并在注释/文档写明 draft 过滤为尽力而为。

**分页（Go 与脚本 1 相同契约）：**

1. 请求与响应的 **header 与 body 分开**（curl `-D` / wget `--server-response` / PowerShell `Invoke-WebRequest.Headers`；禁止把 header 混进 JSON 体）。
2. 只跟随参数恰好为 `rel="next"` 的 URI。忽略 `rel="last"` / `rel="prev"` / `rel="first"`。
3. 没有 `next` 或已满 5 页则停。header 缺失或无法解析 → 停在已读页；**不**改走 `/releases/latest`。
4. 列表页 body 上限 **8 MiB**（100 条含 `body` 字段会超过现有 2 MiB）。超限 → `CodeDataFailure` / 脚本非 0，不当截断 JSON 解析。
5. `/releases/latest` 与 `GET /releases/tags/{tag}` 保持 2 MiB。

## 6. 持久化与数据根

路径：`platform.Paths` 增加 `MihariChannel` = `{dataRoot}/mihari-channel`。不要写在二进制旁，不要写进 `bin/core-channel`。

格式：UTF-8，第一行 `main` 或 `dev`，以 `\n` 结尾。无 stamp 行。

读：

- 文件不存在 → `main`。
- 第一行 trim 后为 `main` / `dev` → 用之；其后忽略。
- 文件存在但第一行非法 → **错误，不当 main**。

写：Go 必须调用 `config.AtomicWrite(path, []byte(channel+"\n"), 0o600)`（Windows 走已有 `MoveFileEx`）。安装脚本：Unix 在数据目录 `mktemp` + `mv -f`，权限 `0600`；PowerShell 同目录临时文件 + `Move-Item -Force`，不宣称 POSIX `0600` ACL（用户默认 ACL 即可）。

### 6.1 提权后的通道 I/O（不改全局 `DefaultDataRoot`）

`self channel` / TUI 切通道通常不提权，写的是调用用户的 `~/.mihari`。`self update` / `Update Mihari` **必须**提权。Unix `sudo` 会把 `HOME` 变成 `/root`。若 LoadChannel 仍用 `UserHomeDir`，会读到不存在的 `/root/.mihari/mihari-channel`，按缺省当成 `main`，再次打 `/releases/latest`。

**只对 Mihari 通道文件**使用独立解析，不改 `platform.DefaultDataRoot()`。今天 `DefaultDataRoot` 的调用方包括 settings、token、core、GeoIP、logs、`installEnvVars`、Unix `control.sock` 回退。把 `SUDO_USER` 放进全局会改变 `sudo mihari service install` 钉死的数据根，超出本 issue。

通道路径解析（`LoadChannel`/`SaveChannel` 的调用方、安装脚本写 sidecar）：

1. `MIHARI_DATA` 非空 → `{MIHARI_DATA}/mihari-channel`。
2. 否则 Unix 且 euid 0 且 `SUDO_USER` 非空 → `{lookup(SUDO_USER).HomeDir}/.mihari/mihari-channel`。lookup 是可注入的「用户名 → 绝对 home」；**禁止** `filepath.Join("/home", SUDO_USER)` 或把原始用户名拼进路径。
3. 否则 `os.UserHomeDir()` + `/.mihari/mihari-channel`。

lookup 失败（无此用户、空 HomeDir、非绝对路径）→ **错误**，不回退 `/root`（那会重现原 bug）。`CGO_ENABLED=0` 的 `os/user.Lookup` 只读 `/etc/passwd`；LDAP/nss 用户应设 `MIHARI_DATA`。Windows 不读 `SUDO_USER`；UAC 一般保留 `%USERPROFILE%`。没有 `SUDO_USER` 的纯 root 登录：走 root 的 home，文档写明应设 `MIHARI_DATA`。

Unix euid 0 写入 `SUDO_USER` 树时：

- `AtomicWrite` / 脚本 `MkdirAll` 后，把 **本次新建的父目录** 和 sidecar 文件 `chown` 给 lookup 得到的 uid/gid。
- 已存在、属主不是该用户的 `~/.mihari` **不**递归 chown（历史 root 目录不在本 issue 修复）。
- 测试注入 lookup **和** euid，放在 `internal/platform`（Unix 文件 `_unix.go`），不要只在 `internal/update` 测。

脚本的 Core/GeoIP `DATA_DIR` 保持现状（`MIHARI_DATA` 或 `$HOME/.mihari`）。本期不把 sudo 数据根问题扩到 core overlay。脚本 1 的 sidecar 仍按上面规则写到用户树，不写 `/root/.mihari`。

服务安装已经把绝对 `MIHARI_DATA` 写进 unit（`installEnvVars`），本规则不改变服务模式。

## 7. `internal/update` API

导出唯一 sidecar I/O：

```text
LoadChannel(path) (channel string, err error)   // 缺文件返回 "main", nil；非法返回 error
SaveChannel(path, channel) error                // 只接受 main|dev
```

通道路径由 §6.1 解析后传入。`Check`/`Update` **不打开 sidecar**。`channel == ""` 或 `"main"` 走 `/releases/latest`。非法 channel 参数 → `CodeInvalidArgument`。CLI/TUI 必须先解析路径再 `LoadChannel`；非法文件 → 错误，不调用 Check，不当 main。

`CheckResult` 保持 `Current` / `Latest` / `Available`，新增 `Channel string`、`Ahead bool`。不变量：`Available` 与 `Ahead` 不能同时为 true。`Available` 仍是「打开安装确认 / 允许下载」的唯一条件。

`Result` 增加 `Channel`、`Ahead`。ahead 或 up to date 时 `Updated == false`；`Version` 为该通道 latest（与今天 skip-same-tag 填 latest 一致）。TUI 在 skip 路径必须复制 `Ahead`，不得写成只有 `Available: false`（否则会显示 Up to date）。

现有 `sameTag`、选资产、checksum、替换、`AfterReplace` 保持。编译会改这些接口，测试 fake 一并改：

- `internal/tui/pages/system.SelfUpdater`
- `internal/cli.SelfUpdater`
- `internal/cli/self_test.go`、`internal/tui/pages/system/model_test.go`、`internal/tui/model_test.go`

## 8. CLI

```text
mihari self channel          # 显示；缺文件打印 main
mihari self channel main
mihari self channel dev
mihari self update           # LoadChannel 后再 Update
```

- `self channel` 不提权。非法参数 → `CodeInvalidArgument` / `ExitUsage`（2）。非法 sidecar → `CodeDataFailure` / `ExitData`（9）。
- 切换只改文件，不下载。
- `--json` show：`{"schema":"mihari/v1","channel":"main"}`（CLI 信封，不是控制协议）。
- `--json` update：保留 `schema` / `version` / `updated`，增加 `channel` 与 `"ahead": true|false`。ahead 时 `updated: false` 且 `ahead: true`。up to date 时两者都为 false。
- `self update` 仍要提权。ahead / up to date：退出 0，不下载。
  - 成功替换：现有 `updated to %s`
  - up to date：现有 `already up to date (%s)`
  - ahead：`current %s is ahead of %s %s`（当前、通道、通道 latest）

## 9. TUI

Daemon 分区，在 `Update Mihari` **上方**增加 `Mihari channel` 行。不进 Core 分区。

```text
┌─ Daemon ──────────────────────────────────────────────┐
│   Daemon          ● Healthy  v0.8.2                   │
│   ...                                                 │
│   Mihari channel  main                                │
│ > Update Mihari   v0.8.2 · Up to date                 │
│   Run Setup                                           │
└──────────────────────────────────────────────────────┘
```

通道行：

- 值只有 `main` 或 `dev`。二进制身份不写在这一行。
- Enter 切到另一条。新 Action `switch-mihari-channel`（不要复用 `ActionSwitchCoreChannel`）。
- 必须列入 `RequiresConfirmation`、`knownAction`、`rowProgressForAction`；`RequiresDaemon == false`（默认 true，漏写则断连无法切换）。
- 不检查 `isElevated()`。
- → `dev`：跟踪预发布，可能不稳定；现在不替换二进制；若当前已是同系列或更高正式版，可能 ahead。
- → `main`：跟踪正式 latest；现在不替换二进制；当前若是 canonical dev，检查后可能 ahead。
- 成功后：清本行 pending、标 Done，**然后** `selfCheckGeneration++` 并 `checkMihariVersion`。现有 `checkMihariVersion` 在 `m.pending` 时直接 return nil，必须先 `clearRowPending`。旧 generation 结果丢弃。
- Core `stable`/`alpha` 的 Action、确认、chip、settings 完全不动。

Update 行：

- `mihariUpdateRow` 三路：`Available` / `Ahead` / else Up to date。ahead 的 Enter = 再检查，不发 `ActionUpdateMihari`。
- 自动检查与手动检查都先 `LoadChannel` 再 `Check(..., channel)`。读失败 → 通道行 Failed，Update 不猜 main。
- Checking / Updating / Failed chip 不变。ahead 不是 Failed。

增加与 `TestSelfUpdatePolicyIsConfirmedAndDaemonIndependent` 对等的 policy 测试。

## 10. 安装脚本

四个脚本都解析通道。优先级：

1. `--channel` / `-Channel`（非法值立即失败，不下载）。
2. 否则 `MIHARI_CHANNEL`。
3. 否则未指定（缺省行为，不写 sidecar）。

`--channel` 与 env 都存在时 **flag 赢**。

`MIHARI_VERSION` 非空时仍钉 tag 下载（脚本 1）；通道仍按上面规则决定是否写 sidecar。

### 10.0 各脚本 argv 契约

**`install.sh`**

- 今天无 argv 循环，本期加 `while`/`shift`。
- 接受 `--channel main|dev` 与 `--channel=main|dev`。
- `--channel` 缺操作数、未知 flag、多余位置参数 → 非 0，**任何下载之前**。
- `curl | bash` **必须** `bash -s -- --channel dev`；管道后的单词进不了脚本。

**`install.ps1`**

- **不加 `param()`。** `irm | iex` 把脚本当字符串执行，文件头 `param()` 会破坏现有一键命令。
- `irm | iex`：只用 `$env:MIHARI_CHANNEL`。
- `powershell -File install.ps1`：从 `$args` 认 `-Channel VALUE` / `-Channel:VALUE`（大小写不敏感，与 PowerShell 习惯一致）。不要为了 `-Channel` 引入 `param()`。

**`install-aio.sh`**

- flag 与**至多一个**位置参数 `bundle_dir`，顺序无关。
- 默认 `bundle_dir` 仍是脚本所在目录。
- 接受 `--channel` / `--channel=`；未知 flag、多个位置参数、缺值 → 失败。
- README 的 `sh install-aio.sh --channel dev` 必须把 `bundle_dir` 留在脚本目录，不能把 `--channel` 当成 `$1`。

**`install-aio.ps1`**

- 扩展现有 `param([string]$BundleDir, [string]$Channel)`。已是 `-File` / scriptblock 调用，不是 `irm | iex`。

**`install-aio-remote.sh`**

- 把今天忽略未知 token 的 `for arg` 换成 `shift` 循环。
- `--yes`/`-y` 与 `--channel` 可组合；未知 flag / `--channel` 缺值 → 失败。
- 通道解析完成、非法值失败，必须发生在 `fetch "$INDEX_URL"` 和 `MIHARI_BUNDLE_URL` 下载之前。

**`install-aio-remote.ps1`**

- 扩展现有 `param([switch]$Yes)`，增加 `[string]$Channel`。文档命令是 `& ([scriptblock]::Create((irm …)))`，**可以**传 `-Channel dev`。`irm | iex` 不是该脚本的文档入口。

**脚本 3 → 脚本 2 交接（唯一规范形式，测试钉死）：**

- 有显式通道：`sh "${workdir}/install-aio.sh" --channel "$channel" "$workdir"`
- 无显式通道：保持今天的 `sh "${workdir}/install-aio.sh" "$workdir"`
- PowerShell：显式时 `& … -Channel $channel -BundleDir $workdir`；否则只传 `-BundleDir`。

### 10.1 脚本 1：`install.sh` / `install.ps1`（GitHub，必须解析）

AList index **不用于**脚本 1。资产是独立 `mihari-<os>-<arch>`，只在 GitHub Releases。

1. `MIHARI_VERSION` 非空 → `/releases/download/<tag>/...`。
2. 否则通道 `dev` → GitHub list API，按 §5.5 选出最大 canonical `vX.Y.Z-dev.N`，再 `/releases/download/<tag>/...`。禁止回退 `/releases/latest`。
3. 否则（缺省或 `main`）→ 现有 `/releases/latest/download/...`。

dev 解析：

- Unix：curl/wget 按 §5.5 取 **紧凑 JSON**。抽取用空白可选模式，对齐 `install-aio-remote.sh` 已有的 `"version"[[:space:]]*:[[:space:]]*"..."`：

  `"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)"`

  再对捕获值做 §5.2 **全匹配**。夹具必须是紧凑 JSON（`"tag_name":"v0.9.0-dev.3"`），不能只测 pretty-print。
- Windows：`ConvertFrom-Json`，按对象读 `tag_name` / `draft`。
- 失败 → 非 0，提示 `MIHARI_VERSION=vX.Y.Z-dev.N`。不装正式 latest。

写 sidecar：仅当通道是显式 `main` 或 `dev`。数据根按 §6.1。Unix 用 `SUDO_USER` home + chown。下载失败不写。

### 10.2 脚本 2：`install-aio.sh` / `install-aio.ps1`（本期必做）

包内落地，零网络。`--channel` **只写 sidecar**，不改包内二进制、不改 `core-channel`、不改 GeoIP。

- 显式 `main` / `dev` → 写入 `{channelRoot}/mihari-channel`（channelRoot 按 §6.1；Core `DATA_DIR` 仍用现有 `$HOME`/`MIHARI_DATA`）。
- 未指定 → 不写不删。
- 「Never touches」清单保持：不碰 `mihari.yaml`、订阅、token、onboarding、logs、web。**允许**写新文件 `mihari-channel`。不要把 Mihari 通道塞进 `data/bin/core-channel`。

墙内 / 手动 GitHub prerelease AIO 的记住通道方式仍是：解压后 `sh install-aio.sh --channel dev`。

### 10.3 脚本 3：`install-aio-remote.sh` / `.ps1`（AList index，不解析 GitHub）

脚本 3 的 latest 来源是 AList `index.txt` 的 `latest` 行，**不要**为脚本 3 做 GitHub list/分页。index 格式与稳定通道字节级同构（`docs/distribution.md`）。

硬编码公开 URL（与 `docs/distribution.md` / #126 钉死的契约一致，永久不变）：

| 通道 | index |
| --- | --- |
| `main` / 未指定 | `https://cloud.xn--30q18ry71c.com/p/public/mihari-release/mihari/index.txt` |
| `dev` | `https://cloud.xn--30q18ry71c.com/p/public/mihari-release/mihari-dev/index.txt` |

下载器本身永远从**稳定根**取（dev 根没有安装器）：

```text
https://cloud.xn--30q18ry71c.com/p/public/mihari-release/mihari/install-aio-remote.sh
https://cloud.xn--30q18ry71c.com/p/public/mihari-release/mihari/install-aio-remote.ps1
```

**URL 选择：**

1. `MIHARI_BUNDLE_URL` 非空 → 跳过 index 与 sha256（现有信任自担）。通道仍按 §10 决定是否写 sidecar（经脚本 2）。**不再**因 `dev` 拒绝（P2 已落地；任意包标通道是操作者责任）。
2. 否则 `MIHARI_INDEX_URL` 非空 → 用之（现有覆盖，操作者可指向任意 index）。
3. 否则按通道选上表默认 URL。

显式 `--channel dev` 且未设 `MIHARI_INDEX_URL` 时，**禁止**读稳定 `…/mihari/index.txt`。

**latest 形状（仅显式通道）：**

- `--channel main`：`latest` 必须全匹配正式 canonical；若是 `-dev.N` → 失败，不下载。
- `--channel dev`：`latest` 必须全匹配 `vX.Y.Z-dev.N`；若是正式 `vX.Y.Z` → 失败，不下载。
- 未指定通道：保持今天的解析（不新增形状校验，避免改变默认一键命令）。

index 无 `latest`、空文件、或 P2 之后尚未发布任何 AList dev 版本：沿用现有「尚未发布完成」错误。脚本 3 **不**回退 GitHub（它是免 GitHub 路径）。提示可改用脚本 1 `--channel dev` 或等下一次 `release-dev` 写入 AList。

解析、确认、分段下载、sha256、解压流程不变。交接按 §10.0。

不修改稳定 AList `index.txt`，不改 `release.yml` / `release-dev.yml`。

## 11. README

`README.md` 与 `README.zh-CN.md` 快速开始 **Install** 从「一个默认块」改成两个并列标签。main 命令语义不变。离线小节同样两个标签。

**main release 通道**（GitHub）

```bash
curl -fsSL https://raw.githubusercontent.com/mihari-proxy/mihari/main/scripts/install/install.sh | bash
```

```powershell
irm https://raw.githubusercontent.com/mihari-proxy/mihari/main/scripts/install/install.ps1 | iex
```

**dev release 通道**（GitHub）

脚本从 **`dev` 分支**取（本 PR 先合入 `dev`；`main` raw 在晋级前没有 `--channel`）：

```bash
curl -fsSL https://raw.githubusercontent.com/mihari-proxy/mihari/dev/scripts/install/install.sh | bash -s -- --channel dev
```

```powershell
$env:MIHARI_CHANNEL = 'dev'
irm https://raw.githubusercontent.com/mihari-proxy/mihari/dev/scripts/install/install.ps1 | iex
```

**main release 通道**（AList / 离线）

```bash
curl -fsSL https://cloud.xn--30q18ry71c.com/p/public/mihari-release/mihari/install-aio-remote.sh | bash
```

```powershell
& ([scriptblock]::Create((irm https://cloud.xn--30q18ry71c.com/p/public/mihari-release/mihari/install-aio-remote.ps1)))
```

**dev release 通道**（AList / 离线）

下载器仍从稳定根取；`--channel dev` 让脚本读 `…/mihari-dev/index.txt` 并写 sidecar：

```bash
curl -fsSL https://cloud.xn--30q18ry71c.com/p/public/mihari-release/mihari/install-aio-remote.sh | bash -s -- --channel dev
```

```powershell
& ([scriptblock]::Create((irm https://cloud.xn--30q18ry71c.com/p/public/mihari-release/mihari/install-aio-remote.ps1))) -Channel dev
```

不要把 README 指向会 404 的 `…/mihari-dev/install-aio-remote.sh`。

`docs/distribution.md`：把「#125 之前用 `MIHARI_INDEX_URL` 覆盖」改成「脚本 3 `--channel` 选择默认 index；`MIHARI_INDEX_URL` 仍可覆盖」。`docs/commands.md`、`docs/architecture.md` 补最小说明：Mihari 通道与 Core 通道分开、sidecar 路径、AIO `--channel`、自更新仍走 GitHub。

## 12. 错误处理

- 网络 / 过大响应 / 非法 GitHub JSON / 403 rate limit：`CodeNetworkFailure` 或现有映射，message 不含 URL query、token。dev Check 最多 5 个 list GET，未认证限额 60/小时。
- GitHub dev 零 canonical 命中：`CodeDataFailure`。
- 脚本 3 显式通道但 `latest` 形状不匹配：非 0，不下载、不写 sidecar。
- 非法 sidecar：CLI `ExitData`；TUI 通道行 Failed；Update 不猜 main。
- 切通道与检查：generation 在写成功且 clear pending 之后递增。
- 安装脚本下载失败：不写 sidecar。
- 通道路径是目录、或 lookup 失败、或 root 留下不可写的 sidecar：按现有数据文件错误处理，不回退 main。

## 13. 测试

默认测试不访问公网。

`internal/update`：

- main 只打 `/releases/latest`；夹具里即使有更高 `vX.Y.Z-dev.N` 也不选。
- dev 过滤非 canonical / draft / 正式 tag；按比较最大而不是数组顺序；分页夹具（含 `rel="next"` 与故意给出 `rel="last"` 不得跟随）。
- 列表页超过 8 MiB → 干净错误，不截断解析。
- 三态，含 `v0.9.0-dev.3` vs main `v0.8.2`（ahead）、`v0.9.0` vs `v0.9.0-dev.3` 在 dev 上（ahead）、`"dev"` 当前（available 非 ahead）。
- `LoadChannel` 缺文件 = main；非法失败。`SaveChannel` 走 AtomicWrite 语义（至少在 Windows 覆盖第二次写入）。
- Update 在 ahead / up to date 不下载。
- Unix 通道路径：注入 euid 0 + `SUDO_USER` lookup，sidecar 不落在 root home；lookup 失败报错；不拼原始用户名。elevated 写入后 sidecar 属主为 lookup uid（可用 fake chown 记录断言）。
- `DefaultDataRoot()` 在 euid 0 + `SUDO_USER` 下**仍**是 elevated `UserHomeDir`（证明通道解析没有改全局）。

CLI / TUI：

- `self channel` 读/写/缺省/非法参数/非法文件。
- `self update` ahead 退出 0 且无资产请求；`--json` 含 `"ahead": true`。
- 通道行在 Update 上方；两套确认；Core 不受影响。
- 切通道成功后新检查；旧 generation 丢弃；pending 清掉后才能重查。
- ahead 行不发 `ActionUpdateMihari`。
- policy：`RequiresDaemon==false`、需要确认。

安装脚本：**不要**把脚本 1 的用例塞进 `test_parallel_download.py`（它只测脚本 3 分段下载，`MIHARI_INSTALL_TEST_MODE` 只存在于 aio-remote）。

新建脚本 1 / 脚本 2 / 脚本 3 通道测试夹具：抽 `resolve_channel` / `resolve_dev_tag` / `write_channel`（或 `MIHARI_INSTALL_TEST_MODE` 早退），对本地 GitHub JSON stub / 本地 index stub：

- 脚本 1 缺省 URL 仍是 `/releases/latest/download/`。
- 脚本 1 `--channel dev` 选最大 canonical，请求 `/releases/download/<tag>/...`；紧凑 JSON；跟 `rel="next"` 最多 5 页；不跟 `rel="last"`。
- `MIHARI_VERSION` 优先于通道。
- 非法通道失败；dev API 失败不回退 latest。
- 仅显式通道写 sidecar；钉版本不写；未指定不删已有文件。
- 脚本 1 Unix：`--channel VALUE` 与 `--channel=VALUE`；`curl | bash` 无 `-s` 时 flag 进不了脚本（文档覆盖，不一定测管道）。
- 脚本 2：`sh install-aio.sh --channel dev` 不把 `--channel` 当 bundle_dir；写 sidecar、不改 core-channel。
- 脚本 3 `--channel dev`：请求 **dev** index URL，零次稳定 index；`latest v0.9.0-dev.4` 通过形状校验；`latest v0.8.2` 失败且零包下载。
- 脚本 3 `--channel main`：稳定 index；把 `--channel main` 按规范形式传给脚本 2。
- 脚本 3 未指定：稳定 index，交接不含 `--channel`。
- 脚本 3 `MIHARI_INDEX_URL` 覆盖仍生效（即使 `--channel dev`）。
- `install.ps1` 无 `param(` 子串（防止破坏 `irm | iex`）。

## 14. 包与文件

| 区域 | 改动 |
| --- | --- |
| `internal/platform/` | `MihariChannel` 路径；Unix **通道专用** `SUDO_USER` 解析 + 注入 lookup/euid/chown；`DefaultDataRoot` 不变 |
| `internal/update/` | Load/SaveChannel、按通道取 GitHub release、比较、Check/Update、列表 8 MiB |
| `internal/cli/self.go` | `self channel`；`self update` 先 LoadChannel；JSON `ahead` |
| `internal/tui/pages/system/` | 通道行、三态、切完重查 |
| `internal/tui/ui/strings.go`、`actions.go`、policy 测试 | 新 Action |
| `scripts/install/install.sh`、`install.ps1` | `--channel` / `$args -Channel`、GitHub dev 解析、写 sidecar |
| `scripts/install/install-aio.sh`、`install-aio.ps1` | `--channel` / `-Channel` 写 sidecar |
| `scripts/install/install-aio-remote.sh`、`.ps1` | `--channel` 选 AList index；交接传 flag |
| 新的安装脚本测试 | 见 §13 |
| README.md、README.zh-CN.md | GitHub 与 AList 各两个 release 通道代码块 |
| distribution / commands / architecture | 最小说明 |

不改 `internal/control/protocol`、`internal/config/settings.go`、runtime 的 Core 通道路径、AList writer、release workflow。

## 15. Key Decisions

1. **通道与 Core 通道分开。** 名称 `main`/`dev`，独立 sidecar，独立 UI 行。
2. **不改 `/v1`，不写 `mihari.yaml`。** 自更新已是控制协议之外的例外。
3. **切通道不换二进制。** 失配三态；禁止冒充。
4. **不从 tag 推断通道。** 只有显式 flag/env 或 CLI/TUI 才写。未指定的 AIO 重装不覆盖已有 sidecar。
5. **只升不降。** main 用户也会从「不同 tag 就装」变成「更新才装」。
6. **通道 I/O 单独尊重 `SUDO_USER`。** 不改全局 `DefaultDataRoot`。lookup 失败报错；root 写入 chown sidecar 与本次新建目录。
7. **Check/Update 不打开文件。** 空通道 = main 只适用于显式传入的参数；非法 sidecar 由 LoadChannel 失败。
8. **Go 写 sidecar 用 `config.AtomicWrite`。** 普通 `os.Rename` 在 Windows 上不能覆盖第二次切换。
9. **脚本 `--channel` 是文档主写法。** Unix `bash -s -- --channel dev`；`install.ps1` 的 `irm | iex` 用环境变量且不加 `param()`。dev GitHub 安装脚本从 `dev` 分支 raw URL 取。
10. **双源 latest。** 脚本 1 / CLI / TUI：GitHub。脚本 3：AList `index.txt`。dev GitHub 必须 list 解析；dev AList 读 P2 已钉死的公开 URL。下载器始终来自稳定 AList 根。
11. **POSIX 抽取按紧凑 JSON、空白可选、tag 全匹配。** 分页只跟 `rel="next"`，最多 5 页，列表 8 MiB。
12. **`self update --json` 带 `ahead`。** 否则 JSON 客户端无法区分 ahead 与 up to date。
13. **PR 打向 `dev`。** 基线 `origin/dev` @ `2a7edec`。

## 16. Open Questions

无。AList P2 消费、GitHub 仍解析、以及 round-2 五项决议已写入正文。

## 17. PR Plan

一个 PR 合入 `feat/125-mihari-version-channel` → `dev`。通道文件、更新器、CLI、TUI、四个脚本、README 必须同时可见。

体积过大时按依赖拆（仍全部打 `dev`）：

1. `platform` 通道路径 + `internal/update` sidecar/比较/GitHub Check/Update
2. CLI `self channel` / `self update`
3. TUI System 页
4. 四个安装脚本 + README / distribution

不要先改脚本却让应用内更新仍只打 `/releases/latest`。不要改 AList 发布侧。
