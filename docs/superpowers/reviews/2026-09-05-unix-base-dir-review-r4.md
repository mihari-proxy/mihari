# Unix Base Dir 设计独立评审 R4

- 结论：**PASS — 技术设计评审通过**。
- 未关闭的 Blocking / Critical / Major：**0**。
- 日期：2026-09-05；代码基线：`2d00f61e720fa27f115dea52f7b4a95cc35a599f`。
- 审阅规格：`docs/superpowers/specs/2026-09-05-unix-base-dir-design.md`，R4，306 行。
- **规格 SHA256：`3A97A33DBB1B1CA5122F2A25F8545FD5A1EBD73B5DA5A160047749FF8A942F3E`。**

本结论来自完整重读 R4、对照当前实现和 R1–R3 发现后的架构、安全及可实施性审查，仅适用于上述哈希的 Unix 设计。没有通过取消普通用户连接、迁移、日志导出或显式便携实例等原目标来规避问题。

## R3 最后问题的关闭证据

| 项目 | 结果 | R4 证据 |
| --- | --- | --- |
| UBD-R3-01：launchd 跨 reboot 禁启 | 关闭 | 217 行：durable disable intent → 持久 disable 并核验 → bootout → 等待已验证进程退出；plist/disabled/bootout/bootstrap 各自进入 WAL，恢复收敛前不放行旧 job |
| UBD-R3-01：stopped 与 enabled 独立恢复 | 关闭 | 219–228 行：四种状态表；两个 stopped 分支不 bootstrap，Inspect 从可信 plist 与 unloaded job 识别 stopped；running+disabled 采用受控 enable/bootstrap/恢复 disabled 并核验，失败不伪报成功 |
| 安装锁借用、不重入 | 关闭 | 124、241 行：外层 acquire 一次并拥有 OwnedInstallLease，RecoverLocked/ApplyLocked/StartLocked 只借用；不同 installer 报忙，内部不重入公共 service 方法 |
| pending 与 activation 的启动边界 | 关闭 | 241、249–257 行：pending 明确限定 activation 前；activation_committed/complete 允许目标 daemon，残余动作由 root 恢复者只向 target 收敛，不能回旧树 |

## R1–R2 全部历史阻塞的最终核对

| 主题及历史 ID | R4 关闭依据 |
| --- | --- |
| root 输入与共享权限（R1-01、R2-06） | 140–170 行：权限矩阵、系统模式 typed RootConfigPolicy、未知字段/版本拒绝、受管文件与环境边界；provider 刷新归属 daemon，下载在锁外、提交复检、失败回滚及 journal；可信内置 policy/hash 与 core provenance |
| Unix 文件能力与 ACL/mount（R1-02、R2-04） | 88–120 行：owner/mode、句柄身份、系统祖先与 application root 分离；creation parent 无继承授权、regular 写入前验证；Linux/Darwin 各自 mount/ACL 原语、不支持环境的明确失败，未授权修改系统祖先 |
| 显式模式与锁域（R1-03、R2-02） | 37–69、122–138、214、241–257 行：B/D/U 与私有 P 完整模式表、无 IO 解析、相对覆盖一次固定、两类实例锁和 peer 校验；默认服务全局锁与便携实例分派；binary-only 更新不触碰 B |
| credential、错误与共用接口（R1-08） | 67–86、132–138、289 行：Windows 原适配器保留；Unix 每请求 provider、停止后轮换、不重放 mutation、稳定 APIError/退出码；capability 转移和资源关闭所有者明确 |
| 快照、导出与 wire（R1-04、R2-05） | 172–202 行：独立 capability/endpoint、固定来源、精确 base64 payload/hash、限额、终帧与正常 EOF；多来源合并/v2 manifest/仅非空 entry；本地用户进程 no-replace 发布，离线显式 TUI-only |
| 安装、服务和自更新（R1-07、R2-03） | 204–233 行：统一 app 用例与 service apply；候选 hash 不作为自声明信任根；Linux `/etc` 持久 mask、macOS 持久 disable；Prepare、PATH/受管 binary 提交及运行/启用状态恢复 |
| journal、验证和激活（R1-05、R2-01/02） | 235–259 行：逐动作 intent/done，包括逆向恢复；跨 boot 重新获取可信身份；create/retain 分离；私有租约验证子进程；durable activation 先于业务启动，之后仅 target 权威 |
| 迁移来源和完整性（R1-06） | 168、261–285 行：逐类白名单、活动 cache/provider/资源引用保留、资源重建先于停机；受管 writer 停止、恶意 source owner 按不可信数据处理；源保留、不二次导入、不把日志继续写入误判为业务迁移失败 |

规格已为后续实施提供可检查的行为、失败和恢复合同。typed 字段注册表的具体代码、平台适配器方法体及故障注入 fixture 属于批准后的实现工作，不再作为缺少设计的理由。实现必须遵守本 spec 的拒绝边界，不能通过未知字段放行、放宽 ACL 或忽略 journal 错误自行改变已审查模型。

## 实际验证与结论边界

- 再次读取完整规格并核验 SHA256 与派发值一致。
- R3 已对保留到 R4 的五帧 wire fixture 独立解析和实算：payload 54 bytes，加 LF 为 55 bytes；daemon SHA256 为 `1625f1821f85ab2dc68c7da55c4fbe769637b7752174c7d5be8a83cd8d388a48`；空源 hash、source/complete 字节总量一致。R4 对该合同与 fixture 未作语义改变。
- 评审仅新增 review 文档；没有修改生产代码、提交、推送、执行真实迁移或系统服务操作。
- 未声称 Unix ACL、挂载、launchd/systemd、provider、journal 或六目标构建已经通过实现测试。第 12 节列出的平台运行、负向安全、race/vet、故障注入和 Windows 兼容检查仍是实施验收要求。

**技术 PASS 不等于产品或实施批准。** 用户仍需审阅并批准 A 分区、全体本机控制与诊断读取权、RootConfigPolicy 的高级配置/核心版本限制、公开快照和安装入口、新增持久化格式、文件系统限制及迁移兼容边界。Windows 重设计与完整跨平台交接验收不在此次 Unix PASS 的完成声明内。只有获得该批准后，才进入 TDD 实施计划和生产实现。
