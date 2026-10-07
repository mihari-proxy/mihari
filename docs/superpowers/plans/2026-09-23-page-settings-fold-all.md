# Page Settings 默认全部展开与全部折叠实施方案

日期：2026-09-23。

设计依据：[Page Settings 默认全部展开与全部折叠](../specs/2026-09-23-page-settings-fold-all-design.md)。

工作区：`.worktrees/dev-20260923`；功能分支：`feat/page-settings-fold-all`；目标分支：`dev`。

## 范围与实现

1. `newPageSettings` 打开时把侧栏八个页签全部展开，焦点和目录仍停在来源页标题。
2. `]` / `[` 在保存开始前的四个焦点区生效：分别把每个页签设为展开或折叠。`]` 不改焦点。`[` 在焦点落在设置项时把 field 收回到该节标题。
3. 底栏两套提示加入 `] all` 与 `[ all`。全局帮助目录不增加这两个键。
4. 更新 `README.md`、`README.zh-CN.md` 和 Page Settings 渲染快照。不改 2026-09-20 规格，不改 `CHANGELOG.md`。

## Red–Green–Refactor 与验收

- [x] 先写通过真实 `Model.Update` 按键打开弹窗的失败测试，覆盖默认全展开、四区 `]` / `[`、保存中忽略、光标回标题、单节切换、左侧 Enter、两套底栏，以及小于 72×22 时只剩 Esc。
- [x] 用最小改动实现上述行为，并更新因此失效的初始展开断言和渲染快照。
- [x] 运行 `go test -race ./...`、`go vet ./...`、`gofmt -l cmd internal`。

## 执行记录

- 失败测试先因「打开时只有来源页展开、`]` / `[` 无效、底栏没有 fold 提示」失败，实现后 `TestPageSettings_` 与 `TestGoldenPageSettings` 通过。
- 2026-09-23：`gofmt -l cmd internal` 无输出，`go vet ./...` 与 `go test -race -count=1 ./...` 退出码 0。
