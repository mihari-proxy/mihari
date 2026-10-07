# Issue #291 弹窗设计原型

这是仅供选择布局的临时原型，不是生产 TUI，不读取网卡或调用 daemon。所有设备、地址、应用结果均为内存中的模拟数据。

直接打开 `egress-variants.html`，或从仓库根执行：

```console
python -m http.server 8291 --bind 127.0.0.1 --directory internal/tui/pages/system/prototype-egress
```

打开 `http://127.0.0.1:8291/egress-variants.html?variant=B`。

- `variant=A`：紧凑列表，详情置于列表下方。
- `variant=B`：用户选定方向的精修版；左侧双行列表、右侧详情，区分 Saved 与 Not applied；窄窗口上下排列。
- `variant=C`：选择与应用分两步。
- 可选 `scenario=missing`：原来保存的网卡不可用。
- 可选 `scenario=failure`：模拟应用失败并保留候选。
- 可选 `compact=true`：限制为窄窗口布局。

默认选中并聚焦已保存网卡，未改动时 Apply 不可用。可点击网卡、用上下键浏览，再 Apply；不可用网卡仍可保存。B 的 Automatic 固定在上方，网卡列表超出可见区域时跟随选中项滚动；上下键到首尾停止。列表内 Tab 跳到操作区，Shift+Tab 从 Cancel 返回候选；Cancel 放弃候选。聚焦底部切换器后用左右键或点击按钮切换方案。

用户已批准 B 精修版及方向键滚动交互，作为生产 TUI 的布局与交互依据。生产实现位于相邻的 `egress.go`，独立使用本地控制协议；本原型始终仅使用模拟数据。原型设计阶段未创建提交或发布到 Issue，后续开发及 PR 验证见[实施计划](../../../../../docs/superpowers/plans/2026-09-21-egress-interface-implementation.md)。
