# Mihari CLI Onboarding

> 本文档指导 AI Agent 完成首次环境配置。按顺序执行，遇到问题时参考 Troubleshooting。
>
> **范围**：本流程只做到「订阅可用」。**不开启 TUN 和系统代理**——如需开启，配置完成后按 [commands.md](commands.md) 的「系统代理与 TUN」章节执行（开启前必须先检查系统状态）。

## Prerequisites | 前置条件

- 本机已安装 mihari 二进制（Windows / Linux / macOS 均可）
- 用户可提供订阅地址（不可猜测、不可代填，必须询问用户）

## Step 1: 检测 mihari 是否已安装

### 检测

```bash
mihari self version
```

### 安装

- 命令不存在时，先询问用户是否安装，再按平台执行：

```sh
# Linux / macOS
curl -fsSL https://raw.githubusercontent.com/mihari-proxy/mihari/main/scripts/install/install.sh | bash

# Windows (PowerShell，需要管理员)
irm https://raw.githubusercontent.com/mihari-proxy/mihari/main/scripts/install/install.ps1 | iex
```

- 安装后可能需要新开终端使 `mihari` 进入 PATH。

### 验证

```bash
mihari self version
```

## Step 2: 检测 daemon 是否运行

### 检测

```bash
mihari status
```

### 处理

- **退出码 0** 且 `Health: ok` → daemon 可用，跳到 Step 4（已有订阅时先跑 Step 3 的 `sub list` 确认）。
- **退出码 3**（daemon 不可用）→ 继续 Step 3 启动 daemon。
- 其他错误 → 见 Troubleshooting。

## Step 3: 启动 daemon

> **模型不执行阻塞或提权命令**：`mihari daemon` 会占用终端直至退出，`service` 操作需要管理员 / root 权限。以下命令一律**由用户在自己的终端执行**，模型只负责检测就绪状态。

### 处理（按优先级尝试）

1. 已注册为 OS 服务（`mihari service status` 非 not_installed）时，请用户执行：

```bash
mihari service start
```

2. 未注册服务时，向用户说明两种方式，由用户选择并自行执行：

```bash
# 前台运行（调试 / 一次性，会占用终端）
mihari daemon

# 或注册为服务（需要管理员 / root 权限，会常驻后台）
mihari service install
mihari service start
```

### 验证

用户启动后，模型轮询检测（未就绪时等待数秒重试）：

```bash
mihari status
```

退出码 0 且 `Health: ok` 即就绪。

## Step 4: 添加订阅（需要用户提供地址）

> **必须询问用户提供订阅地址，不得猜测、不得代填**。若用户暂时不提供，说明 daemon 已可用，等用户提供后再继续。

### 检测

```bash
mihari sub list
```

若已有订阅且状态正常，可询问用户是否直接使用现有订阅，跳过添加。

### 添加（用户提供地址后）

```bash
mihari sub add 订阅名称 <用户提供的URL>
mihari sub list          # 确认添加成功，记录 ID
mihari sub use <ID>      # 激活订阅
```

### 验证

```bash
mihari sub list          # 确认 active 标记
mihari proxy groups      # 确认代理组已生成
```

## Step 5: 最终检查

```bash
mihari status
mihari sub list
mihari proxy groups
```

全部正常即配置完成。**不要在本流程中开启 TUN 或系统代理**；如用户要求开启，转 [commands.md](commands.md) 的「系统代理与 TUN」章节，开启前先检查系统状态。

## Troubleshooting | 故障排除

| 错误 | 原因 | 解决方案 |
| --- | --- | --- |
| `mihari: command not found` | mihari 未安装或不在 PATH | 按 Step 1 安装；安装后新开终端 |
| `mihari status` 退出码 3 | daemon 未运行 | 按 Step 3 请用户启动（service start 或 mihari daemon） |
| `mihari service start` 报权限错误 | 服务控制需要管理员 / root | 请用户以管理员 / root 身份重跑 |
| `mihari sub add` 网络失败 | 订阅地址不可达或格式错误 | 与用户核对 URL 后重试 |
| `sub use` 后 `proxy groups` 为空 | 订阅内容无效或未刷新 | `mihari sub refresh <ID>` 后重试 |
| 安装脚本要求提权 | 注册 OS 服务需要管理员 / root | 按系统提示完成 UAC / sudo 授权 |
