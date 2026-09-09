# Issue #193 / PR #222 bot review triage

审核基线：Cubic 对 `268a971` 的23项反馈；主代理及3位独立subagent基于 `9f7c4ff` 核实。严格保持已批准scope，没有确认必须扩大范围的致命P0/P1。评论ID可在PR中定位；本记录不代表bot主动撤回评论。

| 评论ID | 判定与处理 |
| --- | --- |
| 3968946914 | P1不成立：CLI原基线即在服务同步失败时返回错误，不输出成功JSON；内部Result.Updated保留，app错误明确“Mihari updated, but…”。补完整Execute回归固定现有单错误JSON与部分成功文案，不改变公开契约。 |
| 3968946919 | P1不成立：离线候选处于root可信父链下随机0700 stage，执行前检查完整链信任，之后检查身份/摘要。所述替换需要已具备root写权限。 |
| 3968946931 | 不采纳扩大写入：Windows本次stage实际覆盖canonical文件，服务定义绑定且daemon版本复核会报告不一致；不改写任意SCM路径。 |
| 3968946944 | 有效P2内部不变量；空PreparationKey应拒绝。现有生产者始终填key，未证实可触发的P1。已最小修复并独立复审PASS；原生验收以最新CI为准。 |
| 3968946994 | 有效P2：下载前服务状态过时；改为依据确认预览/边界状态走现有替换流程。已最小修复并独立复审PASS；原生验收以最新CI为准。 |
| 3968947009 | 不成立：AIO reinstall按现有InstallRoot stage并重新注册，旧ImagePath文件不是本次覆盖对象。由旧路径推导安装根会改变既有语义。 |
| 3968947017 | 不成立为本任务P1：Unix实际发布持有TrustedRoot/lease并复核链；Windows新增跨整个发布期父能力超出批准非CAS边界。 |
| 3968947029 | 超出scope：普通安装原本不额外同步不同的已注册服务binary；增加此写入属于新安装行为。 |
| 3968947040 | 超出scope：批准要求确认早于AIO数据写入、首次受管写入前复核；绑定每个core/GeoIP文件及父路径会扩大为多文件安全治理。 |
| 3968947054 | 未证实缺陷：所定位Lstat分支尚未读取size；实际Stat先限长，后续IO失败也拒绝。无既定错误分类优先契约或实际损害复现。 |
| 3968947066 | 不采纳：index latest与平台archive字段独立，不能作为可信candidate版本。unknown是已批准策略。 |
| 3968947072 | 有效P2：SCM查无服务后补取消检查。已最小修复并独立复审PASS；原生验收以最新CI为准。 |
| 3968947084 | 超出scope：Unix使用可信发布路径；Windows最后检查到rename的外部并发窗口已明确保留，不新增安全发布框架。候选按digest绑定，非inode。 |
| 3968947096 | 不成立为产品缺陷：计划记录本任务实际worktree与验证环境；它不是通用安装脚本。其他checkout可替换执行起点，不重写计划为新工具。 |
| 3968947116 | 不成立：离页不取消Check，PageSystem结果仍派发到非活动页并清除pending。 |
| 3968947130 | 有效P2：成功检查回调后再次检查ctx取消，防止继续Stop/stage。已最小修复并独立复审PASS；原生验收以最新CI为准。 |
| 3968947145 | P3显示建议保留：Preparing已有明确状态，不将spinner动画加入本轮必要修复。 |
| 3968947162 | P3重构建议保留：不为重复的两处归一化逻辑做非必要重构。 |
| 3968947180 | P3测试补强建议保留：未发现指纹不稳定的行为问题；已有确定性结构JSON。 |
| 3968947189 | 采纳范围内断言补足：直接检查调用后LASTEXITCODE恢复为83。 |
| 3968947203 | P3测试补强建议保留：已有错误路径与单JSON契约验证，未发现具体分类错误。 |
| 3968947214 | 采纳文档状态澄清：R1 M1/M2补已解决，保留历史证据。 |
| 3968947232 | 保持准确状态：部分完整原生命令仍未通过，最终复合checkbox不能仅依据本地历史结果勾选；逐项结果在§17记录。 |

首轮原生CI问题另行修复：PS5.1模块环境、服务参数展开、PTY读取；不是以上bot首轮重复发现的已解决项。仍需最新head CI通过。CodeRabbit因组织标签配置跳过，Cubic/Pullfrog状态按十分钟轮询记录；不擅自向bot发消息或改审核配置。

## Pullfrog review 5155149810

- distribution ahead：原代码与版本矩阵已保留stable ahead；补一句明确修正历史不准确文字，不改变版本选择。
- service apply JSON warning默认CI覆盖：原完整Execute因真实UID限制跳过属实。增加无root的命令级测试，复用现有euid注入和真实Warn/error转换，断言风险文案、Code/Details及无混入输出；完整Execute测试保留，不增加公开测试接口。已在非root隔离副本实际执行通过。
- app旧fake忽略checks：该fake针对历史AfterReplace；新的replacement_targets测试使用实际checked路径及独立fake，service层另有Stop/stage时序回归。未发现漏实现，保留现有职责。
- architecture长行与执行历史精简：仅维护风格建议，保留本任务审核/验证追踪，不做非必要文档重组。
- awk parser说明：现有代码已有受限结构说明，设计/计划保留完整约束，不增加解析框架。

Pullfrog以COMMENTED结束，Cubic检查也已结束；CodeRabbit跳过。以上为本地核实与处理结论，不冒称各bot无评论或主动撤回。
