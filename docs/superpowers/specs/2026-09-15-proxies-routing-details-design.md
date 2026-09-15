# Proxies 定位文案与 Routing 视觉细节设计

日期：2026-09-15

状态：设计已实施，本地验收完成；后续跟进 PR、CI 与可用 bot review。

分支：`fix/proxies-routing-details`

工作目录：`.worktrees/fix-proxies-routing-details`

基线：`origin/dev` @ `f36563139805eebb1e46544512968ba9305bbf18`

执行计划：[Proxies 视觉细节执行计划](../plans/2026-09-15-proxies-routing-details.md)

## 1. 目标与范围

调整 Proxies 页的定位入口文案、Routing 标签和值的颜色，以及操作提示的出现时机和位置。用户通过逐项问答和截图确认了本文规则。

本文更新 [Proxies 顶部控制区与 GLOBAL 定位](2026-09-14-proxies-routing-layout-design.md) 中“值使用紫色、操作提示优先占位、整行一起反色”的视觉取舍。Routing 卡片结构、模式切换、GLOBAL 展开与滚动行为仍沿用既有实现。

变更限于 TUI 展示、对应测试、快照和使用说明。无新增依赖，无协议、CLI/JSON、持久化格式、安全边界或平台支持变化；不改变 daemon 单写入者和 mutation 路径。

## 2. Locate Selected

- Proxies 每个代理组（含 GLOBAL）的 `[Locate]` 改为 `Locate Selected`，大小写和空格固定。
- 移除的是文字两侧的方括号；不增加按钮框、其他括号或装饰边框。
- 保持紧跟当前节点名，沿用两空格间隔，不挪到最右侧。
- 保持现有焦点交互：组头按 → 聚焦定位入口，← 返回组头，Enter 展开并定位到当前选中的候选卡片。
- 定位入口自身获得焦点时仍使用现有反色；组头反色不延伸到未聚焦的定位入口。
- 当前选择为空或不在候选列表时仍置灰，可聚焦但 Enter 不执行定位。保留的 `Last selected` 数据仍可用于定位。
- 显示截断只影响文字，不修改节点身份；定位时继续使用最新快照中的完整名称，不提交代理选择请求。

示意（仅表达文字与顺序）：

```text
› ▾  Now: [US] Haruka 0x  Locate Selected
```

## 3. Routing 配色与反色的具体含义

`Routing` 标题保留既有紫色；边框保留既有灰色。标签仍为 `Mode` 和大写 `GLOBAL`，值使用与代理组 `Now:` 后节点名一致的绿色（当前主题 `Success` / `ColorSuccess`，256 色索引 78）。

“按截图反色”必须实现为下列分段效果，不能仅以“整行高亮”代替：

| 区域 | 未选中 | 选中且内容区获得键盘焦点 |
| --- | --- | --- |
| 左侧焦点标记、标签和标签至值之间的间隔 | 深色背景；标签白字；不显示 `›` | 浅灰底黑字，显示 `›` |
| 模式值 `Rule` / `Global` / `Direct` 或 GLOBAL 显示值 | 深色背景、绿字 | 绿底黑字 |
| 值后的圆点与操作提示 | 不显示 | 深色背景、浅灰字，不参与反色 |
| 状态说明 | 按既有状态规则显示，浅灰字 | 浅灰字，独立于标签和值的反色区域 |

选中效果对应用户截图中 `Now:` 的浅灰底黑字，以及 `[US] Haruka 0x` 的绿底黑字。绿色背景覆盖值本身；值结束后恢复普通背景，再渲染提示。标签、值和提示之间不能因 ANSI 重置或外层样式包裹而串色。

白色标签不能继续套用灰色 `Muted`。绿色复用当前 `Now` 样式；浅灰说明复用现有说明样式。颜色调整局限于本页，不全局修改 `RowFocus`、`Title` 或其他页面的主题。

只有实际获得内容区焦点的 Routing 行显示 `›` 和反色。焦点移到另一行、代理组或侧栏后，相应行恢复未选中样式。

## 4. 操作提示的位置与出现条件

正常可操作且空间足够时，当前焦点行显示：

```text
› Mode     Rule · Press Enter to Change
  GLOBAL   [US] Haruka 0x
```

焦点移到 GLOBAL 后：

```text
  Mode     Rule
› GLOBAL   [US] Haruka 0x · Press Enter to Select
```

- 提示紧跟值，固定后缀分别为 ` · Press Enter to Change`、` · Press Enter to Select`。
- 圆点两侧各一个空格；不为了右对齐插入大段空格。
- 只有对应行获得键盘焦点且内容区处于聚焦状态时，才可显示该行的操作提示。
- 非焦点行只显示标签和值；焦点移至代理组或侧栏时，两条正常操作提示均隐藏。
- 保持 Enter 原有行为：Mode 打开模式选择弹窗，GLOBAL 展开并定位现有 GLOBAL 候选组。本文不修改快捷键、页脚提示或弹窗交互。

## 5. 状态说明优先于操作提示

状态说明不受“仅焦点显示”限制。沿用既有状态来源、判定顺序和说明位置，不因本次视觉修改改变可操作性判断。

| 行与状态 | 常驻说明 | 操作提示 |
| --- | --- | --- |
| Mode：`State == pending` | `Saved · pending` | 隐藏 |
| Mode：状态未知或尚未确认，且未命中 pending | `Live state unavailable` | 隐藏 |
| GLOBAL：候选快照不满足当前性检查 | `Waiting for candidates` | 隐藏 |
| 正常状态 | 无上述状态说明 | 仅当前焦点行、空间足够时显示 |

继续保留 `Loading…`、`Not selected`、`DIRECT · direct connection` 等现有值投影。`status.Message` 非空时仍在 Routing 边框内追加说明行，不因失焦隐藏。

“常驻”表示不依赖键盘焦点，不要求极窄终端完整容纳任意长状态文本；状态说明继续遵循既有可见宽度截断策略。状态说明与正常操作提示必须分开处理，不能把两者一起在失焦时清空。

## 6. 窄屏与长名称

### 6.1 Routing

- 先计算不含正常操作提示时的标签和值，并按可用显示宽度截断过长值。
- 只有当前行的标签、完整显示值与完整操作后缀能够同时容纳时，才显示操作提示。
- 空间不足则隐藏整个操作后缀，包括圆点；不为容纳提示进一步压缩值，不显示半截 `Press Enter`。
- 状态说明沿用既有优先占位与有界截断；此规则只替换正常操作提示的宽度策略。
- 卡片保持与下方组卡片对齐，常规情况下两条内容行加上下边框共四行。操作提示不换行，不改变列表视口高度。

### 6.2 代理组头

- 优先预留完整 `Locate Selected`（15 个显示列）及必要间隔，再分配 `Now:` / `Last selected:` 和节点名的空间。
- 节点名过长时按显示列截断；更窄时按既有策略压缩标签，仍优先保留定位文案。
- 沿用现有 30、58、80、160 列组头回归范围；极端不足以容纳固定控件的宽度继续受页面整体尺寸和裁切约束，不承诺物理上无法容纳的完整文字。
- 中文、emoji 和 ANSI 样式不按字节数计宽；截断后不溢出边框、不损坏字符、不改变定位对象。

## 7. 实现落点

| 文件 | 预期工作 |
| --- | --- |
| `internal/tui/pages/proxies/locate.go` | `renderGroupHeader` 改定位文案，核对长文案宽度预算 |
| `internal/tui/pages/proxies/routing.go` | `routingHeader` 拆分标签、值与说明样式；区分操作提示和状态说明；按焦点与宽度显示内联提示 |
| `internal/tui/pages/proxies/locate_layout_test.go` | 新文案、完整控件、相邻位置、焦点与禁用样式回归 |
| `internal/tui/pages/proxies/routing_layout_test.go` | 配色、分段反色、提示时机、长值和状态说明回归 |
| `internal/tui/golden_test.go` 与相关 `testdata` | 核对并更新受影响的 Proxies / Routing 文本布局快照 |
| `README.md`、`README.zh-CN.md` | 同步 Locate Selected 文案及 Routing 焦点提示说明 |

优先复用既有页面渲染和主题。只有确有重复且能简化理解时才抽取本页小函数，不创建通用主题框架。

## 8. 验收标准

- [x] 所有代理组显示 `Locate Selected`，无旧 `[Locate]`；定位行为和禁用条件保持有效。
- [x] Mode / GLOBAL 未选中时白字，值绿字，Routing 标题原色。
- [x] 选中时标签浅灰底黑字、值绿底黑字，提示保持深底浅灰字；失焦正确还原。
- [x] 两种 Enter 提示文字准确、紧跟值、仅当前焦点行显示；空间不足时完整隐藏。
- [x] pending、unknown、候选等待和额外状态消息在失焦时仍可见。
- [x] 窄屏与中英文、emoji 长名称不溢出；定位仍使用完整节点身份。
- [x] 普通文本快照与 ANSI 样式验证共同覆盖设计；仅文本快照通过不能证明反色正确。
- [x] 相关 TUI 测试、race、vet 和无 CGO 构建按执行计划记录真实结果。

以上本地验收已完成，实际命令与证据见执行计划；远端 CI / bot review 结果另行记录，不以本地通过代替。
