# Issue #204 执行计划

状态：用户已批准收缩后的设计和开发测试，随后授权提交、PR、CI/bot review 反馈修复，以及全部检查通过后的 bypass 合并与 dev 发版。真实环境操作仍不在范围内。

## 范围基线

实施 provider 发现、固定候选与专用测速、启动首次完整检查的同名提示、最多三次 provider 读取重试、全英文过期/错误/恢复反馈，以及全部 mihomo HTTP 原始诊断。

删除 Details.attempts 与专用刷新开始事件；页面不显示内部重试次数。启动首次成功检查完成后不再新增同名弹窗。不给普通节点发现附加 4 秒截止时间，不另修节点删除后的迟到结果。URL 回落维持现有语义，按实际需要复用。

## 执行顺序

1. Provider 路由：先用 fake controller 写失败回归；实现上游模型/方法、Manager 目录和候选解析、有界重试、控制响应 duplicate_names。验证全局优先、provider 排序、Compatible 排除、错误路径与取消。
2. HTTP 诊断：先验证当前日志丢失状态/正文；保留专用内部 HTTP cause，在日志出口输出原文并脱敏。覆盖 typed REST、gateway REST 与 WebSocket 握手、响应读取和解码失败，保持最终错误记录归属与透传字节。
3. TUI：先验证代理读取错误当前未传播；接通失败事件、保留快照/过期/恢复与首次失败空态；接通首次成功检查的一次性英文同名弹窗与名称去重。
4. 集成与文档：fake controller 跨层回归；更新用户说明与架构相关说明；记录旧诊断脚本的历史性质。
5. 验收：相关包、internal/integration、go test ./...、go vet ./...、gofmt 与 diff 检查；race 能力检查与六目标 CGO_ENABLED=0 构建。记录实际运行结果和环境限制。

## 交付记录

开发与最终复核完成。所有网络 fixture 使用本地 httptest 或 fake，不连接用户真实订阅或 mihomo。

### 已完成开发

- 增加 provider 读取与专用 healthcheck；Manager 保留原始全局映射作为路由依据，按已批准的固定规则选择同名候选，补齐列表元数据。
- 完整目录读取和原始全局读取使用不同的 Manager 方法；控制服务明确要求完整目录，不能悄悄退化成不含 provider 的成功响应。唯一公开增量是可选 duplicate_names。
- provider 读取对瞬时失败最多尝试三次，退避可取消，遵守 Retry-After 与总预算；缺失 providers 对象属于无效响应，不当成空目录成功。
- 原始 HTTP cause 只进入内部诊断；typed REST、gateway REST 与两条握手路径保留状态、操作、阶段和原因。原有最终 owner 去重保持，重试失败各记一次 WARN，最终失败由出口记录。
- gateway 观察器只读取转发方已消费的字节，保留响应状态/正文，记录读取与关闭失败。成功正文不进入日志。关闭失败不改变已完成的成功结果。
- TUI 接通最终错误事件，继续读取本轮其他资源；成功快照/测速值保留并标记过期，首次失败显示错误空态，恢复不清除不相关的选择错误。窄终端换行保留关键原因。
- 同名弹窗限定启动首次成功检查，避让已有弹窗/安装/导出界面，净化外部名称的终端控制符。组内同名卡片去重。

### Red–Green 证据

| 回归 | 初始失败 | 修复结果 |
| --- | --- | --- |
| Provider-only 测速 | 普通路由返回失败，专用路由未被调用 | 选择排序首个 provider，返回 fixture 42ms |
| HTTP 状态与原文 | 日志缺少 404、报错正文和操作类别 | 保留原始诊断；公开错误不含原始正文 |
| Gateway 503 | 零条失败记录 | 一条最终记录且透传字节不变 |
| TUI 错误事件 | 原因丢失，后续 Rules 未读取 | 错误传到页面，后续资源继续 |
| HTTP 200 截断读取 | 只尝试一次且错误提示成为 HTTP 200 | 按读取失败重试，不误用成功状态描述原因 |
| 短凭据出现在 Close 错误 | 原始短凭据进入诊断 | 脱敏后记录 WARN，业务仍成功 |
| 同名弹窗终端控制符 | 名称保留控制符 | 净化后显示 |
| 窄终端错误文案 | HTTP 状态被截掉 | 自动换行，保留关键原因 |
| providers 字段缺失 | 被接受为空目录成功 | 明确 data_failure，稳定格式错误不重试 |

### 验证与限制

- 最终代码已通过 `go test ./...`、`go test -race ./...` 和 `go vet ./...`；全仓测试包含跨层 provider 测速与错误诊断集成回归。
- 最终代码已通过 `CGO_ENABLED=0` 的 Windows、Linux、macOS × amd64、arm64 六目标构建。
- `git diff --check` 通过；全部修改和新增的 Go 文件经 `gofmt -l` 检查，无待格式化文件。
- golangci-lint 2.12.2 的全仓 Windows 检查仅剩原版本已有的 internal/subscription/legacy_resources.go:52 未使用常量告警。该常量在 Unix 文件中使用，HEAD 原版本即存在；本轮未修改该文件，不扩大修复范围。
- typed HTTP 错误正文内部保留最多 64 KiB，日志保持既有 4096 字节诊断上限；凭据脱敏，截断明确标识。当前 websocket 依赖在失败握手后最多提供 1024 字节正文，达到该边界或已知未完整读取时标记 handshake body truncated。
- 原诊断脚本保持历史复现性质，新增 README 明确其不能作为修复后验收。正式回归位于 internal/integration/provider_delay_test.go 及相关包。
- 构建产物位于已忽略的 bin/issue204。上述验证在首次提交前完成；后续 PR/CI/bot review 与 dev 发版记录另行补充。未运行真实环境验证。

