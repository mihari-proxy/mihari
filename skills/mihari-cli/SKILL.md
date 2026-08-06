---
name: mihari-cli
displayName: Mihari CLI (Manage mihomo proxy state)
version: 1.0.0
description: |
  用 mihari 命令管理本机 mihomo 的代理状态:查询全局状态、代理组与节点延迟、
  切换代理组、管理订阅、控制系统代理与 TUN、关闭连接。
  Trigger: 用户要求切换代理、测试节点延迟、查看/管理订阅、开启系统代理或 TUN 时使用。
---

# Mihari CLI

- **Author**: [Mihar1](https://github.com/Mihar1)
- **Organization**: [KinemaClawWorkspace](https://github.com/KinemaClawWorkspace)
- **GitHub**: https://github.com/LeeShunEE/mihari

## ⚠️ Before First Use | 首次使用必读

**首次使用此 skill 前，必须先读取 [references/ONBOARDING.md](references/ONBOARDING.md) 完成环境配置。**

- **首次配置** → 读取 references/ONBOARDING.md 完成全部步骤
- **环境不可用**（命令不存在、依赖缺失、连接失败）→ 读取 references/ONBOARDING.md Troubleshooting 排查修复
- **配置完成后** → 直接使用下方 Run Commands

## 概述

Mihari 是 mihomo 的本地管理器，所有操作通过本机 daemon 控制面执行（`mihari` 命令）。本 skill 指导 agent 用 CLI 管理代理状态：

- **查询**：全局状态、代理组与当前节点、节点延迟、订阅列表、活动连接
- **变更**：切换代理组、切换/刷新订阅、控制系统代理与 TUN、关闭连接

完整指令列表见 [references/commands.md](references/commands.md)（含输出格式与退出码说明）。

## Run Commands | 工作流程

所有操作遵循统一流程，细节（参数、输出解析、冲突处理）在 [references/commands.md](references/commands.md)：

1. **前置检查**：`mihari status` 确认 daemon 可用（退出码 3 = daemon 不可用，按 ONBOARDING 请用户启动）
2. **查询**：根据任务执行查询类指令（proxy groups / sub list / connections list 等），用 `--json` 解析
3. **变更**：变更类指令前先向用户确认（切换代理组、关闭连接、覆盖外部代理尤其需要）
4. **验证**：变更后重新查询确认生效（如 `proxy groups` 确认 Now 已切换）

**禁止模型执行**：`mihari daemon`、`mihari service *`、`mihari self update`、`--follow` 流式命令（阻塞或需提权），一律由用户自行执行。完整分级见 [references/commands.md](references/commands.md) 的「命令分级」章节。

### 开启系统代理 / TUN（特殊流程）

开启 sys proxy 或 TUN 之前**必须**先检查系统状态并处理冲突，流程见 [references/commands.md](references/commands.md) 的「系统代理与 TUN」章节。要点：

- `mihari sysproxy status` 显示外部代理占用（foreign）时，**先提示用户关闭运行中的代理软件**，再 enable；用户坚持覆盖时才用 `--force`（需说明会覆盖其他产品代理的风险）
- `mihari tun status` 确认托管状态；mihari 不检测其他软件的 TUN 占用，开启前需提示用户确认无其他 TUN 代理软件在运行

## Environment Variables

| 变量 | 用途 |
|------|------|
| `MIHARI_DATA` | 数据根目录覆盖（Windows 默认 `%USERPROFILE%\.mihari`） |
| `MIHARI_CONTROL_ENDPOINT` | 控制端点覆盖（命名管道 / Unix socket） |
| `MIHARI_CONTROL_CREDENTIAL` | 控制凭据覆盖 |
