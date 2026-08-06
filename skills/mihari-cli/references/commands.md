# 指令列表

> 用 `mihari <command>` 在 shell 中执行。查询与变更命令都支持人类可读输出与 `--json`（稳定 JSON envelope + 退出码，机器解析场景优先用 `--json`）。

## 退出码

| 码 | 含义 |
|----|------|
| 0 | 成功 |
| 3 | daemon 不可用（请用户启动，见 ONBOARDING.md Step 3） |
| 其他非 0 | 失败（权限、冲突、参数错误等；`--json` 时 stderr 输出错误 envelope） |

## 命令分级（模型使用）

> 按影响程度分级：**A 级可自主执行；B 级执行前必须向用户确认；C 级仅用户明确点名时执行；D 级禁止模型执行**，需要时告知用户自行操作。

### A 级 · 只读查询（可自主执行）

`mihari status`、`mihari core status`、`mihari proxy groups`、`mihari proxy test <GROUP>`、`mihari rules list`、`mihari connections list`、`mihari sub list`、`mihari sub show <ID>`、`mihari sysproxy status`、`mihari tun status`、`mihari panel list`、`mihari self version`、`mihari service status`、`mihari traffic`、`mihari logs`（后两者**不带** `--follow`，输出单条快照后退出）

### B 级 · 变更运行状态（先向用户确认）

`mihari proxy select <GROUP> <PROXY>`、`mihari sub add/use/enable/disable/set/refresh`、`mihari connections close <ID>`、`mihari core restart`、`mihari sysproxy enable/disable`、`mihari tun enable/disable`

### C 级 · 破坏性或强副作用（仅用户明确点名）

`mihari core install/update`、`mihari connections close-all --yes`、`mihari sub remove <ID> --yes`、`mihari sysproxy enable --force`、`mihari panel install/update/rollback/uninstall/reinstall`、`mihari panel open`（会在用户桌面弹出浏览器）

### D 级 · 禁止执行（由用户自行操作）

`mihari daemon`（阻塞前台进程）、`mihari service install/uninstall/reinstall/start/stop/restart`（系统服务生命周期，需提权）、`mihari self update`（替换 mihari 自身二进制）、`mihari traffic --follow` / `mihari logs --follow`（无限流阻塞 shell）

## 查询类

| 场景 | 命令 | 关键输出 |
|------|------|----------|
| 全局状态 | `mihari status` | `Health: ok`；`--json` 含 capabilities 列表 |
| 核心状态 | `mihari core status` | running / version |
| 代理组与当前节点 | `mihari proxy groups` | 每行 `名称\t类型\t当前节点` |
| 节点延迟测试 | `mihari proxy test <GROUP>` | 每行 `节点名\tN ms` |
| 订阅列表 | `mihari sub list` | 订阅与 active 标记；`--json` 含 ID |
| 活动连接 | `mihari connections list` | ID / host / 流量；`--json` 含 ID 用于关闭 |
| 系统代理状态 | `mihari sysproxy status` | 是否启用、是否外部持有（foreign） |
| TUN 状态 | `mihari tun status` | mihari 托管的 TUN 状态 |
| Web 面板 | `mihari panel list` | 已安装面板与激活状态 |

## 变更类

> 执行变更前先向用户确认；破坏性或大范围操作（close-all、sub remove、rollback、--force 覆盖）尤其需要用户明确同意。

| 场景 | 命令 |
|------|------|
| 切换代理组节点 | `mihari proxy select <GROUP> <PROXY>` |
| 添加订阅 | `mihari sub add <NAME> <URL>` |
| 激活订阅 | `mihari sub use <ID>` |
| 刷新订阅 | `mihari sub refresh <ID>` |
| 关闭单个连接 | `mihari connections close <ID>` |
| 关闭全部连接 | `mihari connections close-all --yes` |
| 重启核心 | `mihari core restart` |
| 安装 / 更新核心 | `mihari core install` / `mihari core update`（C 级，仅用户明确点名） |

> 其余变更（`connections close-all`、`sub remove`、`sysproxy enable --force`、`panel` 系列、`self update`）均为 C/D 级，见上方分级清单，模型不得主动发起。

## 系统代理与 TUN（特殊流程）

> **开启之前必须检查系统状态**。若本机正在运行其他代理软件（Clash、Clash Verge、v2rayN 等），先提示用户关闭，再执行开启；只有用户明确要求覆盖时才继续。

### 系统代理

```bash
mihari sysproxy status   # 检查:Foreign=true 表示被外部产品占用
```

- `Foreign=false` → 直接开启：

```bash
mihari sysproxy enable
```

- `Foreign=true` → **提示用户关闭运行中的代理软件**。用户坚持覆盖时才用（会接管其他产品设置的代理）：

```bash
mihari sysproxy enable --force
```

- 关闭（只清除 Mihari 持有的代理，不会关闭外部代理）：

```bash
mihari sysproxy disable
```

### TUN

```bash
mihari tun status        # 检查 Mihari 托管状态(未实现外部占用检测)
```

- **开启前提示用户确认**没有其他软件启用 TUN（Clash 系软件开启 TUN 时会与 mihomo TUN 冲突），用户确认后：

```bash
mihari tun enable
mihari tun disable
```

- TUN 可能需要管理员 / root 权限或服务安装（视 OS 而定）。

## 验证变更

| 变更 | 验证命令 |
|------|----------|
| 切换代理组 | `mihari proxy groups` → Now 已变 |
| 订阅操作 | `mihari sub list` → active 标记正确 |
| 系统代理 | `mihari sysproxy status` → 已启用 |
| TUN | `mihari tun status` → 托管状态已变 |
