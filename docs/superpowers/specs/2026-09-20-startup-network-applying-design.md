# 启动期间的 Network Applying 状态

## 已确认的产品范围

- 只在 System → Network 的 System proxy、TUN 状态行末尾展示橙色旋转动画与 `Applying…` badge。
- Applying 表示正在应用保存的期望状态，目标可以是 On 或 Off。
- 仅有 Desired 与 Observed/Live 不一致，不构成显示条件。
- 完成、失败、取消后停止动画，保留实际状态和现有错误提示。短于一次界面采样的操作不人为延长动画。
- 动画不占用手动操作的 pending 状态，不抢焦点、不打开弹窗、不禁用页面或手动开关。
- 不扩展到 Overview，不增加自动修复、漂移检测、重试策略或 Auto fix failed 状态。

## 当前实现事实

- `Manager.Run` 在进入核心 supervisor 前调用一次 `ApplyDesiredSystemProxy`，失败记录 warning 后继续；该调用持有 maintenance gate。
- TUN 保存的期望值由配置生成器写入核心配置，`PrepareCoreStart` 准备及发布启动配置，核心启动及健康结果由 supervisor 观察回报。
- TUN 和系统代理的独立状态查询都取得 maintenance gate。直接往这些查询结果里添加标志，无法保证应用期间能读到标志。
- `/v1/status` 当前也可能在 `SetupRequired` 查询中等待相同 gate。因此仅增加 JSON 字段仍不足以实现可观察的 Applying。

## 已确认的协议字段

在现有认证本地 `GET /v1/status` 响应中兼容增加可选对象：

```json
{
  "startup_network": {
    "system_proxy_applying": true,
    "tun_applying": false
  }
}
```

这里只展示新增部分，原有响应字段保持原义。没有启动应用进行时可省略整个对象；旧 daemon 不返回该字段时，新 TUI 不推断或模拟 Applying。

这个对象是 daemon 持有的瞬时只读观察，不写入 settings 或持久化 snapshot，不推进业务 revision，不包含错误正文或凭据，不映射到 Web gateway。现有 CLI 的 JSON 状态输出可能兼容透传新增字段；文本输出、退出码和 mutation 请求保持原义。

用户已确认直接增加上述字段，保持 `/v1` 协议版本。

## 实现约束

1. 系统代理标志只包围 daemon 初始应用调用，正常返回、错误和取消路径均清理；更新恢复及手动操作不设置此启动标志。
2. TUN 标志只覆盖 daemon 本次初始核心启动尝试，且存在 Mihari 管理的 TUN 开关。初始成功、失败退避、degraded、停止或 Run 退出均结束标志；不把之后的重试、手动重启或安装核心显示为本次启动应用。
3. 缺少核心、待安装激活或核心修复阻塞时，没有实际启动应用，不显示持续旋转。
4. 进度观察独立于 mutation gate。实现时必须让 `/v1/status` 在应用期间及时返回，且不能通过伪造 `setup_required=false` 来绕过原有 readiness 语义。readiness 直接读取原子 restart-required 标志与已有核心状态，保留原有二进制可用性判断，并以阻塞 fake 验证。
5. TUI 用 daemon 标志驱动 badge，动画仅更新本地帧；状态轮询在后台执行。断连时停止动画并保留现有 Stale 语义，重新连接后以新 daemon 响应为准。
6. 初始 live 状态尚未读到时保留 Loading/Unknown，不合成 Off；有快照时保留原状态文字并在末尾追加 badge。
7. 启动应用期间 session 只轮询权威状态，推迟会等待 mutation gate 的完整快照和核心流连接；应用结束后恢复通常的轮询与流订阅。
8. 窄页优先为完整 badge 保留空间，过长的观测摘要截断；状态行详情保留完整信息。
9. 动画与用户动作的进度分别管理。现有服务端串行写入顺序、权限、冲突检查、revision 校验与事务回滚继续生效。

## 验证计划

采用 Red–Green–Refactor，优先添加以下可观察行为测试：

- 用 channel 控制启动应用暂停，证明本地状态接口能在它完成前返回 Applying，开启和关闭目标均覆盖。
- 系统代理正常完成、失败、取消均清除标志；TUN 初始核心启动成功、失败、缺失及停止边界均覆盖。
- 持续状态漂移、后续核心重试、手动开关及更新恢复不会误触发启动 badge。
- JSON 可选字段省略及旧响应兼容；已有字段、错误码、CLI 退出码保持契约。
- System 两行分别显示 badge；动画帧变化且保留状态文字；停止、断连和重连时无遗留动画。
- 动画期间方向键、页面导航及手动开关意图仍可处理，不设置全局 pending。
- 先运行相关包测试，再按修改范围执行集成测试、race、vet；只用 fake 和临时目录，不操作本机系统代理、TUN 或服务。

同步更新中英文 README。文档及代码工作在 `feat/startup-network-applying` 分支；用户现有 `.gitignore` 修改保留，不纳入本需求。

## 实施验证

- 已通过全部源码目录测试：`go test ./cmd/... ./internal/... ./scripts/...`。
- 已通过相同源码目录的 `go vet`。
- 已通过 runtime、control/server、整个 TUI 及 integration 的 race 检查；窄页布局调整后复验相关 TUI 包。
- 已通过 Windows/amd64、Linux/amd64、macOS/arm64 的 `CGO_ENABLED=0` 编译。
- 修改过的 Go 文件通过 gofmt，`git diff --check` 通过。
- `go test ./...` 与 `go vet ./...` 的目录遍历被已有 `bin/core-policy-checks/pytest-local-1` 权限阻塞，因此显式列出以上全部源码目录；未修改该临时产物目录或其权限。
- 本次验证使用 fake、临时目录和本地 IPC，未执行真实 mihomo 或系统服务的 testenv 验证。
