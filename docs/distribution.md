# 分发方案

mihari 面向两类用户分发：

- **海外 / 可访问 GitHub 的用户**：在线安装，直接从 GitHub Releases 拉取二进制（脚本1）。
- **墙内 / 无 GitHub 访问的用户**：离线安装，从自建 AList 网盘拉取 all-in-one 整合包（脚本3 → 脚本2）。

本文档聚焦「运维/用户操作面」。详细的架构论证与设计取舍见 all-in-one 分发设计稿（仓库外规划文档）。

---

## 一、用户安装入口

### 1. 在线安装（脚本1，需 GitHub 访问）

```bash
# Linux / macOS
curl -fsSL https://raw.githubusercontent.com/mihari-proxy/mihari/main/scripts/install/install.sh | bash

# Windows (PowerShell)
& ([scriptblock]::Create((irm https://raw.githubusercontent.com/mihari-proxy/mihari/main/scripts/install/install.ps1)))
```

脚本1 仅安装 mihari 二进制，核心（mihomo）与 GeoIP 在首次运行时联网下载。

### 2. 离线安装（脚本3 下载器，免 GitHub）

一条命令，全程不触碰 GitHub：

```bash
# Linux / macOS
curl -fsSL https://cloud.xn--30q18ry71c.com/p/public/mihari-release/mihari/install-aio-remote.sh | bash

# Windows (PowerShell)
& ([scriptblock]::Create((irm https://cloud.xn--30q18ry71c.com/p/public/mihari-release/mihari/install-aio-remote.ps1)))
```

> 命令中的网址是 AList 网盘的固定公开直链（签名已关闭），永久不变，复制即用。

GitHub dev prerelease `v0.9.0-dev.2` 已发布并完成精确 14 个 assets 的公开验收，当时未写 AList。独立 dev AList 根目录为 `/mihari-release/mihari-dev`，公开 index 为 `https://cloud.xn--30q18ry71c.com/p/public/mihari-release/mihari-dev/index.txt`；稳定安装入口仍读取 `/mihari-release/mihari/index.txt`，不会指向 dev。历史 `v0.9.0-dev.2` 不回溯到 AList。

脚本3 下载器的执行流程：

1. 下载根目录 `index.txt`（固定公开直链，永久有效），解析出最新版本号与本平台的整合包公开直链 + sha256；
2. 询问确认（`--yes` / `-y` 可跳过；stdin 非 tty 时读 `/dev/tty`）；
3. 下载整合包 → `Downloads/mihari-aio/`：源站支持 HTTP Range 时默认使用 4 个并发分段，否则自动回退单流；随后执行 sha256 校验；
4. 解压到 `Downloads/mihari-aio/`；
5. 调用包内的本地安装器（脚本2），传入 bundle 目录；
6. 提示重启终端，运行 `mihari`。

### 3. 手动安装某个整合包

无需脚本3，直接下载某版本的整合包解压后运行包内安装器：

```bash
# 下载 mihari-all-in-one-linux-amd64.tar.gz（从 AList 版本目录）
tar -xzf mihari-all-in-one-linux-amd64.tar.gz
sh install-aio.sh        # Windows: powershell -File install-aio.ps1
```

环境变量可覆盖默认安装位置：

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `MIHARI_BIN` | `/usr/local/bin`（Linux/macOS）<br>`%LOCALAPPDATA%\Programs\mihari`（Windows） | mihari 二进制安装目录 |
| `MIHARI_DATA` | `$HOME/.mihari`（Linux/macOS）<br>`%USERPROFILE%\.mihari`（Windows） | 数据根目录（核心 + GeoIP 落地处） |
| `MIHARI_INDEX_URL` | 公开直链（见脚本默认值） | index.txt 公开直链（脚本3）；默认仍是稳定 `/mihari-release/mihari/index.txt` |
| `MIHARI_BUNDLE_URL` | 空 | 显式指定整合包 URL，**跳过 index 与 sha256 校验**（信任自担） |

脚本 3 `--channel` 选择默认 index：缺省/`main` 读稳定 `…/mihari/index.txt`，`--channel dev` 读公开 `…/mihari-dev/index.txt`。`MIHARI_INDEX_URL` 仍可覆盖（即使同时传 `--channel dev`）。下载器本身仍从稳定根目录获取（dev 根不放置 `install-aio-remote.sh` / `.ps1`）。操作者仍可用 `$env:MIHARI_INDEX_URL=` 指向任意 index。

```bash
# Linux / macOS：稳定下载器 + --channel 选择默认 index
curl -fsSL https://cloud.xn--30q18ry71c.com/p/public/mihari-release/mihari/install-aio-remote.sh | bash -s -- --channel dev
```

```powershell
# Windows (PowerShell)：稳定下载器 + -Channel 选择默认 index
& ([scriptblock]::Create((irm https://cloud.xn--30q18ry71c.com/p/public/mihari-release/mihari/install-aio-remote.ps1))) -Channel dev
```

---

## 二、核心通道与 sidecar

`scripts/tools/build-all-in-one` 不解析滚动 tag、latest release 或 GeoIP 可变分支。它要求显式传入仓库内已审核的 `scripts/release/release-inputs.lock.json`，并只下载 lock 中精确记录且有 SHA-256 的六个平台 mihomo 资产和两份 GeoIP 数据。当前 checked-in lock 使用 **stable** 内核；预置通道由 lock 的 `mihomo.channel` 决定，而不是由 bundler 在发版时动态选择。

维护者只在独立的 release-prep PR 中更新 lock：

```sh
go run ./scripts/tools/resolve-release-inputs --channel stable --out scripts/release/release-inputs.lock.json
```

GitHub API 限流时可设置 `GITHUB_TOKEN` 环境变量。必须审核生成的 diff 后再合并；稳定和 dev release workflow 都只消费 lock，绝不运行解析器。实际打包命令必须显式传入 lock，例如：

```sh
go run ./scripts/tools/build-all-in-one \
  --lock scripts/release/release-inputs.lock.json \
  --mihari-dir dist --out bundles \
  --platforms "linux/amd64,linux/arm64,darwin/amd64,darwin/arm64,windows/amd64,windows/arm64"
```

`--out` 必须是专用的受管 bundle 目录；允许使用当前工作目录下的专用子目录（例如 `./bundles`），但输出目录不得等于或包含当前工作目录，也不得等于或包含 lock 文件。它与 `--mihari-dir`、`--scripts-dir` 两个输入目录之间还必须双向不重叠：输出不能包含输入，输入也不能包含输出。目录内不得混放非 bundle 文件。bundler 在临时目录完成全部构建与校验后才整体提交输出。lock 只用于构建，不会进入 bundle，也不会作为 GitHub Release/AList 资产上传，因此固定的 14-asset 发布契约不变。

Mihari 原始二进制由 Go 1.26.5 以 `-buildvcs=false -trimpath` 构建，避免同一 commit 在 tag 创建前后因 VCS/module 元数据变化而改变字节。相同 commit、版本、toolchain、lock 和仓库内脚本的重试应逐字节复现全部 14 个 assets。仅 dev release workflow 会在已有同版本 Release 时做 existing-asset checksum preflight，并在字节冲突时于 mutation 前 fail closed；stable release workflow 不提供同等 preflight，仍遵循现有 stable Action 发布与 AList 事务契约。

无论 lock 中的 `mihomo.channel` 是 `stable` 还是 `alpha`，bundle 都会写入 sidecar `data/bin/core-channel`（UTF-8 文本）：

```
<stable|alpha>
<stamp>
```

第 1 行为 `stable` 或 `alpha`；第 2 行为非空 stamp（通道 + 二进制指纹，例如 `stable-v1.19.29` 或 `alpha-e183c58`）。缺行、非法通道或 stamp 为空视为无效，守护进程忽略、不改 settings。

`install-aio.sh` / `install-aio.ps1` 覆盖 `data/bin/mihomo` 时，若 bundle 带 sidecar 则一并覆盖到 `$MIHARI_DATA/bin/core-channel`，**仍不修改** `mihari.yaml`。守护进程在启动与 setup 快路径按 stamp 应用 sidecar：与已记录的 `core-channel-bundle` 相同则不改 `core-channel`（保护用户后来在 System 页切换的通道）；stamp 变化才把打包通道写入 settings。

settings 新增可选字段 `core-channel` 与 `core-channel-bundle`（schema 仍为 `mihari.settings/v1`）。加载使用 `KnownFields(true)`：无这些字段的旧文件可由新 daemon 读取（空通道视为 `stable`）；**含这些字段的新 settings 文件无法被旧 daemon 加载**。

---

## 三、AList 网盘目录结构

AList base path 不是可配置入口：策略层将 stable 固定为 `/mihari-release/mihari`，将 dev 固定为 `/mihari-release/mihari-dev`；传入其他路径会被拒绝。

```
stable（仅稳定通道）：

/mihari-release/mihari/             base_path（fs/API 路径）
├── index.txt                       路由表（公开直链）
├── install-aio-remote.sh / .ps1    稳定通道脚本3（内含稳定 index 直链）
├── v0.3.0/                         不可变版本目录
│   ├── mihari-all-in-one-{linux,darwin,windows}-{amd64,arm64}.tar.gz / .zip
│   ├── SHA256SUMS.txt              本版本 6 个整合包的 sha256
│   ├── BUILDINFO                   新发布版本的 version + 40 位 commit 身份
│   └── COMPLETE                    完整标记（内部语义）
├── v0.2.0/
└── v0.1.0/

dev（仅预发布通道；无安装器）：

/mihari-release/mihari-dev/         base_path（fs/API 路径）
├── index.txt                       路由表；latest 只能是 vX.Y.Z-dev.N
└── vX.Y.Z-dev.N/                   不可变版本目录（无 install-aio-remote.*）
    ├── mihari-all-in-one-{linux,darwin,windows}-{amd64,arm64}.tar.gz / .zip
    ├── SHA256SUMS.txt
    ├── BUILDINFO
    └── COMPLETE
```

稳定公开 index：`https://cloud.xn--30q18ry71c.com/p/public/mihari-release/mihari/index.txt`。公开 dev index：`https://cloud.xn--30q18ry71c.com/p/public/mihari-release/mihari-dev/index.txt`。dev 根不放置 `install-aio-remote.sh` / `.ps1`。已发布的 GitHub dev prerelease 与后续 dev AList 写入均不改变稳定 `index.txt` 或 `/releases/latest`。

> **AList 拓扑 quirk（读路径 / 下载）**：**公开下载 URL** 需在 `/p` 后加 `/public` 挂载点前缀，即 `https://cloud.xn--30q18ry71c.com/p/public/mihari-release/mihari/…`（`alist_client.public_url` 自动处理）。
>
> **AList 拓扑 quirk（fs API 路径）**：**所有** fs API（get/list/put/mkdir/remove）都把传入路径当**相对存储根** `/mihari-release` 解析、再拼一次前缀——`/mihari-release/mihari/X` 会被当成 `/mihari-release/mihari-release/mihari/X`（写落到那里、读也从那里查）。而 `/p/public` 下载用的是逻辑虚拟绝对路径。`alist_client._fs_path` 对**所有 fs 操作**（读+写）去掉首段 `/mihari-release`，让读、写、公开下载三者落到同一处。`upload`/`upload_text` 还会检查 AList body `code`（AList 永远返 HTTP 200，真失败此前被静默吞掉），写失败即报错。
>
> 若日后恢复 `/mihari` 挂载点（fs API 与 `/p` 用同一路径），这两个 quirk 同时消失：base_path 改回 `/mihari`，`public_url` 去掉 `/public` 中缀，`_fs_path` 改为原样返回。

### index.txt 格式

```
latest v0.3.0
linux-amd64   <公开直链>  <sha256>
linux-arm64   <公开直链>  <sha256>
darwin-amd64  <公开直链>  <sha256>
darwin-arm64  <公开直链>  <sha256>
windows-amd64 <公开直链>  <sha256>
windows-arm64 <公开直链>  <sha256>
```

每行 `<key> <rest...>`：`latest` 行的 key 后是版本号；平台行的 key 是 `<goos>-<goarch>`，后跟公开直链与 sha256。脚本3 按此解析。

### 发布顺序与非原子 index 窗口

release workflow 的 AList 步骤顺序保证用户永远拿不到半成品：

1. 上传前以及 index 提交前各扫描一次通道基线：当前合法 `latest` 与所有可逐字节验证的完整目录共同决定最高版本；候选版本低于该基线时 fail closed，不允许 stable latest 回退；
2. 先把整个版本目录上传完整（6 包 + `SHA256SUMS.txt` + `BUILDINFO`），逐字节回读验证后，**最后传 `COMPLETE`**；历史 stable 目录可没有 `BUILDINFO`，新目录必须包含它；
3. 已有 `COMPLETE` 的目录不会直接跳过：先验证 `COMPLETE`、`BUILDINFO`（新目录）、checksum manifest 和 6 个 bundle 的远端字节；完全一致才复用且不覆盖，冲突或无法完整验证时 fail closed；
4. **最后才覆盖 `index.txt`**——发布过程中用户读到的是旧 index，继续下载旧版本目录。

`index.txt` 仍是单文件提交点。Shared writer 在事务前要求权威实时内容等于调用方观察到的原值并保存备份，然后只执行一次 PUT 和一次权威 readback：

- 回读等于目标内容：提交成功；
- 回读仍等于原值：以 index unchanged 失败，不在 writer 内再次 PUT，必须从头重跑完整 release/retract workflow；
- 回读出现不同于目标和原值的第三方值：立即停止，保留可能来自并发操作的新现场；
- 回读失败或结果不确定：立即停止，因为目标写入可能已经生效，不能猜测远端状态。

第三方值和不确定 readback 均转入人工恢复，不得触发自动 rollback。AList 不提供 compare-and-swap（CAS）或原子 rename，因此事务前检查与单次 PUT 之间仍有竞态，覆盖期间也可能出现短暂解析窗口，不能声称彻底原子或零失败窗口。

Stable release 与 retract workflow 共用 channel concurrency（`mihari-stable-alist`），避免这两类 Actions writer 互相并行；dev 发布与 `retract-dev.yml` 使用独立并发组 `mihari-dev-alist`，不得复用稳定锁。它们不能约束 workflow 外的管理员操作。执行人工 `regenerate-index` 或 artifact 恢复前，必须确认相关 release/retract workflow 均未运行，且两条并发组都空闲；从读取现场、判断 metadata、写入到最终回读的整个期间，都必须禁止其他人工或自动 writer。`regenerate-index.py` 默认 `--channel stable`，写入 `/mihari-release/mihari/index.txt`；修复 dev index 必须显式 `--channel dev`。

Writer 在首次 mutation 前把原状态写入 runner 的通道备份目录：`index.txt` 保存原始字节，`metadata.json` 保存 `existed`、`channel`、`path` 和 index 字节的 `sha256`。仅当 AList mutation 失败或 mutation 期间取消时，release/retract workflow 才会将两者作为 `stable-index-backup-<run_id>-<attempt>` workflow artifact 上传并保留 3 天。AList mutation 已成功后若下游步骤失败，workflow 不会上传该 artifact，因为远端 index 已经验证提交成功，无需恢复旧 index。人工恢复时必须从对应 run 下载 artifact，先验证 `channel=stable`、固定 `path=/mihari-release/mihari/index.txt` 及 `sha256`：`existed=false` 表示原对象不存在，应在确认没有合法并发更新后删除该 index；`existed=true` 且 `index.txt` 为空表示恢复为空文件；`existed=true` 且非空则逐字节恢复其内容。恢复后再次下载并逐字节核对；runner 本地 `$RUNNER_TEMP` 不能作为运行结束后的恢复入口。

dev 发布与 `retract-dev.yml` 另有两类 artifact，同样仅在 AList mutation 失败或 mutation 期间取消时上传并保留 3 天：`dev-index-backup-<run_id>-<attempt>` 的 metadata 为 `channel=dev` 且 `path=/mihari-release/mihari-dev/index.txt`；隔离失败另有 `stable-index-isolation-<run_id>-<attempt>`（`channel=stable` / `path=/mihari-release/mihari/index.txt`）。两类 artifact 不得交叉恢复：禁止用 dev backup 写稳定 index，也禁止用 isolation snapshot 写 dev index。恢复稳定 index 的唯一允许来源是 `stable-index-isolation-*` 或稳定通道自己的 `stable-index-backup-*`。

---

## 四、版本保留策略

`MIHARI_KEEP_VERSIONS`（GitHub 变量，默认 `5`）控制保留数量：

- 保留 = **index 当前指向的 1 个**（不计入名额）+ **其余最新的 N-1 个**；
- 无 `COMPLETE` 的半成品目录（发版中断残留）**优先删除，不占名额**；
- `index.txt` 读取失败 → 重试 1 次 → 仍失败则本次跳过、不删任何版本、打警告（发布不因此失败）。

---

## 五、版本撤回（致命错误）

独立 workflow `.github/workflows/retract.yml` 手动触发，永久移除坏版本的 GitHub Release、assets 与 AList 分发数据，但保留 canonical stable tag。dev 通道使用独立的 `.github/workflows/retract-dev.yml`：只操作 `/mihari-release/mihari-dev/<version>/` 与 dev `index.txt`，删除 GitHub prerelease 及其 assets，保留 canonical `vX.Y.Z-dev.N` tag；不得修改稳定 `index.txt`、稳定版本目录或 `/releases/latest`。当 AList 已配置时，dev 撤回会只读 snapshot/compare 稳定 index 做隔离检查，这不是写入。

稳定撤回步骤：

1. 在 GitHub Actions 选择 `main` 分支/ref 后运行；`workflow_dispatch` 输入 `version`（纯 semver 闸门）+ `confirm`（布尔双保险）；
2. 读 `index.txt` 判断撤回版本是否为当前 latest；
3. **仅当撤回的是 latest**，先排除目标目录，重建 `index.txt`：latest 改为现存最高且完整（含 `COMPLETE`）的版本（sha256 从该目录的 `SHA256SUMS.txt` 读取）；无完整版本 → `index.txt` 置空。写入后必须回读验证成功，才进入删除；
4. 永久删除 AList 版本目录 `<base_path>/<version>/`；撤回非 latest 时 index 保持原始字节不变；
5. `gh release delete <version> --yes` 永久删除 GitHub Release 及其 assets，但保留 canonical stable tag；tag 继续受覆盖 `refs/tags/v*` 的 active tag ruleset 保护，不为撤回配置删除 bypass。

若 latest 的替代或空 index 已验证、但目录删除失败，index 保持已切换状态，不回退；目标目录成为不再被 index 引用的遗留目录，同一撤回重跑会继续删除它。幂等：对已撤回的版本重跑不报错。

### 边界（务必知晓）

撤回**只移除分发渠道，已安装用户不可回收**。canonical stable tag 保留且不可同版本重切；修复必须使用更高版本号。修复版发布前，已装用户主动 `self-update` 会先降到次高版本，再随修复版回升——最终靠**快速发布修复版**（`vN+1 > vN` 自更新覆盖坏版本）自愈。

---

## 六、CI 配置（前置依赖）

AList 是否启用只由 `ALIST_URL` 决定：URL 缺失时，release workflow 走 GitHub-only skip，不进入 AList mutation，也不阻塞 GitHub 发布；URL 存在时必须进入 mutation。此时 `ALIST_USERNAME` 与 `ALIST_PASSWORD` 都是必需凭据，任一缺失都会由客户端 fail closed 并使 workflow 失败，不能静默跳过。启用国内分发需配置：

**GitHub Secrets：**

| Secret | 用途 |
|--------|------|
| `ALIST_URL` | AList 站点地址（如 `https://alist.example.com`）；是否存在是唯一启用开关 |
| `ALIST_USERNAME` | 登录用户名；`ALIST_URL` 存在时必填 |
| `ALIST_PASSWORD` | 登录密码；`ALIST_URL` 存在时必填 |

**GitHub Variables（可选）：**

| Variable | 默认值 | 用途 |
|----------|--------|------|
| `MIHARI_KEEP_VERSIONS` | `5` | 保留版本数 |

Stable/dev base path 由发布策略固定，不通过 GitHub Variable 覆盖；传入与通道不匹配的路径会在任何 AList mutation 前失败。

> **前置：关闭签名**。AList 存储必须关闭「签名」（per-storage Sign 关），公开直链才不会 401。
