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
curl -fsSL https://raw.githubusercontent.com/mihari-proxy/mihari/main/install.sh | bash

# Windows (PowerShell)
& ([scriptblock]::Create((irm https://raw.githubusercontent.com/mihari-proxy/mihari/main/install.ps1)))
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

脚本3 下载器的执行流程：

1. 下载根目录 `index.txt`（固定公开直链，永久有效），解析出最新版本号与本平台的整合包公开直链 + sha256；
2. 询问确认（`--yes` / `-y` 可跳过；stdin 非 tty 时读 `/dev/tty`）；
3. 下载整合包 → `Downloads/mihari-aio/` → sha256 校验；
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
| `MIHARI_INDEX_URL` | 公开直链（见脚本默认值） | index.txt 公开直链（脚本3） |
| `MIHARI_BUNDLE_URL` | 空 | 显式指定整合包 URL，**跳过 index 与 sha256 校验**（信任自担） |

---

## 二、AList 网盘目录结构

base_path 默认 `/mihari-release/mihari`（AList fs/API 路径，通过 GitHub 变量 `ALIST_BASE_PATH` 配置）：

```
/mihari-release/mihari/             base_path（fs/API 路径）
├── index.txt                       路由表（公开直链）
├── install-aio-remote.sh / .ps1    脚本3 下载器（内含固定公开 index 直链，每次发布覆盖）
├── v0.3.0/                         不可变版本目录
│   ├── mihari-all-in-one-{linux,darwin,windows}-{amd64,arm64}.tar.gz / .zip
│   ├── SHA256SUMS.txt              本版本 6 个整合包的 sha256
│   └── COMPLETE                    完整标记（内部语义）
├── v0.2.0/
└── v0.1.0/
```

> **AList 拓扑 quirk**：fs/API 路径（上传、list、get）用 `/mihari-release/mihari/…`，但**公开下载 URL** 需在 `/p` 后加 `/public` 挂载点前缀，即 `https://cloud.xn--30q18ry71c.com/p/public/mihari-release/mihari/…`（`alist_client.public_url` 自动处理）。若日后恢复 `/mihari` 挂载点，此 quirk 消失：base_path 改回 `/mihari`、`public_url` 去掉 `/public` 中缀。

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

### 发布顺序（零失败窗口）

release workflow 的 AList 步骤顺序保证用户永远拿不到半成品：

1. 先把整个版本目录上传完整（6 包 + SHA256SUMS.txt），**最后传 `COMPLETE`**；
2. 已完整的目录（`COMPLETE` 存在）**整个跳过**——重跑幂等，不覆盖任何文件；
3. **最后一行才覆盖 `index.txt`**——发布过程中用户读到的是旧 index → 下载旧版本目录，无校验失败窗口。

唯一残留风险是 `index.txt` 单文件覆盖中断 → 用户解析失败，重试即可。

---

## 三、版本保留策略

`MIHARI_KEEP_VERSIONS`（GitHub 变量，默认 `5`）控制保留数量：

- 保留 = **index 当前指向的 1 个**（不计入名额）+ **其余最新的 N-1 个**；
- 无 `COMPLETE` 的半成品目录（发版中断残留）**优先删除，不占名额**；
- `index.txt` 读取失败 → 重试 1 次 → 仍失败则本次跳过、不删任何版本、打警告（发布不因此失败）。

---

## 四、版本撤回（致命错误）

独立 workflow `.github/workflows/retract.yml` 手动触发，**彻底删除**坏版本：

1. `workflow_dispatch` 输入 `version`（纯 semver 闸门）+ `confirm`（布尔双保险）；
2. 读 `index.txt` 判断撤回版本是否为当前 latest；
3. 删除 AList 版本目录 `<base_path>/<version>/`；
4. **仅当撤回的是 latest** 才重建 `index.txt`：latest 改为现存最高且完整（含 `COMPLETE`）的版本（sha256 从该目录的 `SHA256SUMS.txt` 读取）；无完整版本 → `index.txt` 置空；
5. `gh release delete <version> --yes --cleanup-tag`（删 release + 资产 + tag，允许修复后同版本号重发）。

幂等：对已撤回的版本重跑不报错。

### 边界（务必知晓）

撤回**只移除分发渠道，已安装用户不可回收**。修复版发布前，已装用户主动 `self-update` 会先降到次高版本，再随修复版回升——最终靠**快速发布修复版**（`vN+1 > vN` 自更新覆盖坏版本）自愈。

---

## 五、CI 配置（前置依赖）

AList 分发步骤在 release workflow 中以 `if: env.ALIST_URL != ''` 包裹——**未配置时跳过，不阻塞 GitHub 发布**。启用国内分发需配置：

**GitHub Secrets：**

| Secret | 用途 |
|--------|------|
| `ALIST_URL` | AList 站点地址（如 `https://alist.example.com`） |
| `ALIST_USERNAME` | 登录用户名 |
| `ALIST_PASSWORD` | 登录密码 |

**GitHub Variables（可选）：**

| Variable | 默认值 | 用途 |
|----------|--------|------|
| `ALIST_BASE_PATH` | `/mihari-release/mihari` | 网盘 base_path（fs/API 路径） |
| `MIHARI_KEEP_VERSIONS` | `5` | 保留版本数 |

> **前置：关闭签名**。AList 存储必须关闭「签名」（per-storage Sign 关），公开直链才不会 401。
