# Issue #242：Proxies Locate 执行方案

日期：2026-09-15。
状态：实现与本地验证进行中；用户已授权提交、推送、创建 PR，并每 10 分钟检查 CI 与 bot review，处理反馈直到通过。

## 目标与工作位置

依据已确认的[设计](../specs/2026-09-15-current-proxy-locate-design.md)，在每个代理组的当前选择名称右侧增加 `[Locate]`。按钮 Enter 展开当前组、聚焦直接选中的候选卡片并滚动到可见位置。

- Worktree：`C:/Users/Kinema/Documents/modular_dev/mihari/.worktrees/feat-242-current-proxy`
- 分支：`feat/242-current-proxy`
- 建立基线：`origin/dev @ cb08ec2`
- Go/toolchain：沿用 `go.mod` 的 Go 1.26.0 / go1.26.5。
- 所有命令在上述 worktree 执行。保留工作区已有的设计与计划文档。

## 约束

- 变更限于 TUI 页面、相关帮助、测试与文档。定位不调用选择、测速或其他 mutation，不新建网络请求或 goroutine。
- 复用现有组快照、主题、显示宽度工具和 `ensureFocusVisible`；不新增依赖或调整版本。
- 不改变控制协议、CLI/JSON、持久化格式、daemon 单写入者边界和跨平台范围。
- `CHANGELOG.md` 不在本功能范围。提交、推送及 PR 按后续用户授权执行，PR 目标为 `dev`，合并须经用户确认。
- 行为变更逐项执行 Red–Green–Refactor；先确认测试因缺少行为而失败，再实现。仅更新相关快照，不批量接受不明差异。

## 代码依据与实现选择

| 位置 | 当前行为 | 本次处理 |
| --- | --- | --- |
| `internal/tui/pages/proxies/navigation.go` | `FocusID` 仅含 Group/Node；标题参与纵向导航 | 增加标题按钮的焦点标识，保持既有组/节点零值语义 |
| `internal/tui/pages/proxies/model.go` | 标题 Enter 折叠；节点 Enter 调用选择；渲染提供焦点行范围 | 在标题分支处理 Locate，计算名称与按钮宽度，映射按钮的可见范围 |
| `internal/tui/pages/proxies/snapshot.go` | 刷新失败保留旧组、设置 loadError 并失效 groupsFresh | 沿用现有快照行为，通过测试验证旧目标仍可定位 |
| `internal/tui/pages/proxies/routing.go` | 顶部 GLOBAL Enter 聚焦组标题；FooterHints 按上下文生成 | 保留 GLOBAL 入口语义，为 Locate 焦点提供准确提示 |
| `internal/tui/ui/keymap.go` | Catalog 维护帮助与底栏绑定 | 更新 Proxies 的方向键及 Enter 说明 |
| `internal/tui/golden_test.go` | `-update` 更新指定 golden 测试 | 仅重生成本次影响的 Proxies/Routing 快照 |

建议在 `FocusID` 增加 `Locate bool`：标题为 `{Group: name}`，按钮为 `{Group: name, Locate: true}`，节点仍为 `{Group: name, Node: node}`。约束 `Locate=true` 时 Node 必须为空；原有 pending map 中节点身份仍使用默认 false。不给纵向列表插入额外一项：按钮 ↑/↓ 先按所在组标题归一化，再走原来的纵向导航。

目标判断共用一个页面内的小函数：按完整 Group 找组，拒绝空 Now，并在该组 Nodes 中按完整 Name 查找。渲染可用性和 Enter 执行都复用该判断。执行时重新读取页面当前快照，不缓存按钮最初聚焦时的目标。

## Task 1：基线与回归入口

**读取/核对：** `.github/CONTRIBUTING.md`、`AGENTS.md`、`README.md`、本设计，以及目标目录中新增的就近规则。

- [x] 检查分支、HEAD、工作区，确认仅在指定 worktree 工作；不自动清理或重建分支。
- [x] 运行目标包基线，记录已有失败并与本任务区分：

```powershell
git branch --show-current
git status --short
go test ./internal/tui/pages/proxies ./internal/tui/ui ./internal/tui -count=1
```

- [x] 复用 `updateProxyKey`、`fakeClient` 和现有 Routing fixture。新增 `internal/tui/pages/proxies/locate_test.go` 集中放定位行为测试。

## Task 2：标题按钮导航与定位

**修改：** `navigation.go`、`model.go`、`navigation_test.go`。
**新增：** `locate_test.go`。

- [x] Red：新增 `TestLocate_EnterFocusesCurrentCandidate`。构造两个以上候选，Now 指向非首项；从标题发送 Right、Enter，断言展开、焦点为 Now 对应节点、Now 未改、无异步命令、fakeClient 未收到选择或测速。
- [x] Red：新增 `TestLocate_HeaderButtonNavigation`，通过左右键和后续 Enter 的外部行为证明两个焦点目标不同；新增 `TestLocate_ButtonVerticalNavigation` 和 `TestLocate_EscapeReturnsToRail`。
- [x] 将 `TestNavigation_GroupAndNodeArrowRules` 的标题 Right no-op 断言更新为新目标行为，保留节点网格导航及标题 Left/Enter 的既有断言。

```powershell
go test ./internal/tui/pages/proxies -run 'TestLocate_|TestNavigation_GroupAndNodeArrowRules' -count=1
```

**预期 Red 原因：** 当前 Right 留在标题，随后 Enter 仅展开组，未聚焦 Now。测试必须编译成功；首批测试使用现有字段和按键验证，不依赖尚未新增的字段编译失败。

- [x] Green：扩展焦点标识；标题 Right 进入 Locate、Locate Left 回标题；Locate Enter 先校验目标，成功后设置展开、节点焦点并调用 `ensureFocusVisible()`，返回 nil 命令。缺失目标时保持状态。
- [x] 按钮 Up/Down 使用标题的纵向移动；节点 Left/Up 返回普通标题；FocusFirst 与顶部 GLOBAL 跳转产生普通标题焦点。
- [x] 保留 Esc、Ctrl+T 的既有处理；按钮上的 t 不产生节点测速。节点上的 Enter 保持原来的选择行为。
- [x] 运行目标测试至绿，再运行整个 Proxies 包。只在绿色测试保护下整理页面内小函数。

## Task 3：目标边界、旧快照与刷新

**修改：** `locate_test.go`；仅在测试证明必要时调整 `model.go` 的 `SetGroups`/焦点归一化。

- [x] Red → Green：`TestLocate_CollapsedGroupExpands`，折叠组直接定位。
- [x] Red → Green：`TestLocate_NestedGroupTargetsDirectCandidate`，Now 为策略组时停在本组对应卡片，不递归跳其他组。
- [x] Red → Green：`TestLocate_MissingTargetIsDisabled`，覆盖空 Now、非空但不在 Nodes、空候选；按钮可聚焦，Enter 不展开、不改变焦点、不发请求。
- [x] Red → Green：`TestLocate_StaleSnapshotRemainsUsable`，先用 `ObserveSnapshot` 注入成功快照，再注入失败；断言 `Last selected`、旧目标保留、Locate 可用，即使 `groupsFresh=false`。
- [x] Red → Green：`TestLocate_UsesLatestSnapshotOnEnter`，按钮聚焦后直接调用 SetGroups 更新 Now，再 Enter，必须定位更新后的目标。
- [x] Red → Green：`TestLocate_RefreshPreservesFocus`，定位后改变 Now，焦点留在原卡片；删除聚焦卡片则回本组标题；删除组/清空列表沿用页面的安全回退，不残留无效 Locate 状态。
- [x] 覆盖按钮聚焦期间目标出现、消失与恢复，确保禁用状态每次由当前快照计算。

每个新增行为先运行对应测试并确认正确失败，再补最小实现；既有实现已满足的用例记录为通过的回归保护，不人为制造失败。

```powershell
go test ./internal/tui/pages/proxies -run 'TestLocate_|TestSnapshot_' -count=1
```

## Task 4：按钮渲染、滚动与 GLOBAL

**修改：** `model.go`、`locate_test.go`。
**新增：** `internal/tui/pages/proxies/locate_layout_test.go`。
**复用回归：** `routing_layout_test.go`。

- [x] Red：`TestLocateHeader_PreservesButtonWithLongName`，使用普通/中文/emoji 长名称，覆盖 Now 与 Last selected 两种标签，页面宽度 30、58、80、160。
- [x] Red：`TestLocateHeader_FocusAndDisabledStyles`，按钮与标题焦点可区分；失去内容焦点后不高亮；禁用仍保持可识别的灰色状态及焦点位置。
- [x] Green：按 `SectionTextWidth(FullSectionInner(width))` 获取预算，预留 `[Locate]`、间隔及焦点标记，剩余用于标签与名称；用现有显示宽度截断工具处理名称，再组合样式。短名称后紧跟按钮，不填充到整行右端。
- [x] 极窄视口遵循现有最小布局边界：名称预算取非负值，必要时再缩短标签，避免按钮被 section 的整行截断吞掉；不为该功能扩大页面的终端支持范围。
- [x] 将“焦点属于该组标题行”和“焦点属于普通标题控件”分开判断，使 Locate 也能返回正确的标题行范围，而标题和按钮只高亮当前目标。
- [x] Red → Green：`TestLocate_ScrollRevealsCurrentCard`，构造目标位于长列表中部/末尾的单列与多列 fixture。验证视口中的目标卡片及其上下边框完整可见、总高度受限；定位后方向键仍可继续移动。
- [x] 覆盖 Routing 固定区扣高、stale 错误区占行、终端 resize，以及视口低于卡片高度时沿用既有滚动边界；不能仅检查目标名字，因为标题也可能显示同名。
- [x] Red → Green：`TestLocate_GLOBALCandidate`，下方 GLOBAL 组按钮定位 GLOBAL.Now；现有 `TestRouting_GLOBALJump*` 保持顶部入口聚焦组标题的断言。

```powershell
go test ./internal/tui/pages/proxies -run 'TestLocate|TestRouting_GLOBALJump|TestNavigation_Scroll' -count=1
go test ./internal/tui/pages/proxies -count=1
```

## Task 5：帮助、说明与渲染快照

**修改：** `internal/tui/ui/keymap.go`、`keymap_test.go`、按需 `strings.go`；`internal/tui/pages/proxies/routing.go` 的 `FooterHints`；`internal/tui/help_test.go`；`README.md`、`README.zh-CN.md`；相关 golden fixture。

- [x] Red → Green：帮助说明标题 →/← 在 Locate 与标题间移动，Enter 可执行定位；覆盖现有帮助 Catalog 与当前页过滤测试。
- [x] Red → Green：Locate 焦点的底栏明确显示 `Enter locate`，标题/节点/Routing 原有提示依其上下文保留。窄底栏继续遵循现有 FitFooter 规则。
- [x] 在 README 的 Proxies 段加入简短双语操作说明，包含折叠组自动展开、旧数据仍可定位及不可用按钮的条件。TUI 文案使用英文。
- [x] 先运行现有快照测试查看差异，再仅更新受影响快照：

```powershell
go test ./internal/tui -run 'TestGolden(ProxiesFull|RoutingMode)$' -count=1
go test ./internal/tui -run 'TestGolden(ProxiesFull|RoutingMode)$' -count=1 -args -update
go test ./internal/tui -run 'TestGolden(ProxiesFull|RoutingMode)$' -count=1
```

- [x] 检查 `internal/tui/testdata/full/proxies.golden` 及实际产生差异的 Routing golden。确认仅包含按钮、名称截断、相关焦点或提示变化；不更新其他页面以掩盖失败。
- [x] 运行 TUI 全部测试，确认 page 与 shell 的按键转发和帮助集成。

## Task 6：格式、回归与构建验证

- [ ] 对本次改动的 Go 文件执行 gofmt；检查 diff 中无无关格式变化。
- [ ] 以下命令按顺序执行，失败时定位原因，修改后只重复必要范围：

```powershell
go test ./internal/tui/... -count=1
go test -race ./internal/tui/... -count=1
go test ./internal/integration -count=1
go test ./...
go vet ./...
gofmt -l internal/tui
git diff --check
```

- [ ] Race 使用本机可用的 race 工具链，与 CGO-free 发布构建分开。若缺少 C 工具链，记录未验证原因并由对应 CI 补齐，不将 CGO=0 下的 race 失败当成功。
- [ ] 用临时进程环境执行 `CGO_ENABLED=0` 的六目标编译。每个构建独立检查退出码，结束后恢复原 GOOS/GOARCH/CGO_ENABLED；产物放入既有忽略的 bin 目录或外部临时目录，不提交。

| GOOS | GOARCH | 建议产物 |
| --- | --- | --- |
| windows | amd64 | `bin/mihari-windows-amd64.exe` |
| windows | arm64 | `bin/mihari-windows-arm64.exe` |
| linux | amd64 | `bin/mihari-linux-amd64` |
| linux | arm64 | `bin/mihari-linux-arm64` |
| darwin | amd64 | `bin/mihari-darwin-amd64` |
| darwin | arm64 | `bin/mihari-darwin-arm64` |

每个矩阵项设置上述环境后，使用 `go build -trimpath -o <建议产物> ./cmd/mihari`；编译成功不代表在目标 OS 上运行测试成功。平台运行验证由现有 CI 补齐。

- [ ] 不启动真实 mihomo、不连接真实订阅、不安装或重启系统服务。UI 行为用本地 fixture 和渲染结果验证。

## 完成标准与交付

- [ ] 七项已确认设计都有对应行为测试或明确的既有回归测试，特别是定位没有 mutation、stale 可用及刷新不抢焦点。
- [ ] 渲染快照已逐项审查，长名称保留按钮，GLOBAL 现有入口行为保持。
- [ ] 说明和帮助与最终交互一致；格式、测试、vet、构建的实际结果与未验证项均已记录。
- [ ] `git status --short`、准确的文件清单与 diff 已复核，包含本功能设计/计划，无 CHANGELOG 或构建产物。
- [ ] 交付变更摘要、测试证据与剩余限制。只有用户进一步授权时才创建带 DCO 的 Conventional Commit、推送和面向 dev 的 PR；不直接合并。

## 实施记录（2026-09-15）

- Tasks 1–5 已完成。折叠/展开定位合并在 TestLocate_EnterFocusesCurrentCandidate 子测试；GLOBAL 定位由 TestLocate_ScrollRevealsCurrentCard 的 Routing fixture 覆盖；额外覆盖按钮焦点跨帮助弹窗恢复和极矮视口。
- 已观察到导航/定位、按钮渲染/焦点范围、空快照焦点清理、帮助/底栏测试因缺少行为正确失败，随后最小实现使对应测试通过。
- 已通过：go test ./...（含 integration）、最终 Proxies 包 race、go vet ./...、golangci-lint（全仓与最终 TUI）、六目标 CGO_ENABLED=0 编译、gofmt 检查。
- Unix 布局安全检查：74 passed、4 skipped；本机未执行真实账户/挂载测试。
- 全仓 go test -race ./... 已启动；首次提交时订阅包仍在运行，最终结果随 PR 验证记录更新。六目标编译不代表目标 OS 运行测试，平台运行由 CI 验证。
- 普通源文件 diff --check 通过；full/proxies.golden 保留现有渲染测试要求的行尾填充，按该格式审查其差异。
- 用户已明确授权提交、推送、创建 PR，以及每 10 分钟检查 CI/bot review 并修复反馈。PR 最终结果是远端验收依据；合并等待用户确认。
