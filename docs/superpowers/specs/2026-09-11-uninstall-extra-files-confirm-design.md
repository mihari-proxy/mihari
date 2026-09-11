# 彻底卸载：列出文件夹并确认两次

状态：用户已批准简化范围。本文覆盖同日「额外文件摘要」稿。不改 `/v1`。未授权真实卸载。

关联：`2026-09-10-issue-194-uninstall-design.md`。固定根、删除顺序、默认 Cancel、英文文案、不跟随链接删除、不回滚，仍然有效。本文取消 TUI 对目录内容的白名单预检。

## 问题

白名单预检把未建模的生成文件（例如 `bin/core-channel`）和外来文件都变成 TUI Failed。继续扩清单或做「额外文件摘要」都太重。用户要自己看将被清空的文件夹，决定是否整目录删除。

## 范围

做：

- TUI Preview **不扫目录内容**。确认框列出将删除的目标根路径。必须确认两次，两次都默认 Cancel。
- 两次都确认后，退出 TUI，卸服务，再 `RemoveAll` 这些根（根内额外文件一并删除）。
- CLI `service uninstall --purge --yes` 仍走白名单失败闭环；补上源码可证明的 `bin/core-channel`。
- CLI 增加 `--force`：必须同时有 `--purge --yes`，才跳过内容检查并整根删除。TUI 两次确认等价于这个开关。
- 目标根本身若是符号链接、junction/重解析点、或不是目录，仍立刻失败。不扫根内文件。

不做：

- 额外文件摘要、AcceptUnrecognized、把未知项收集进 Preview
- 让 `--yes` 单独覆盖外来文件
- 新 daemon 协议、事务/身份/恢复、改 `CHANGELOG.md`

## TUI

Maintenance 一行不变。Enter 后：

1. Preview 解析固定根（缺的跳过）。根本身非法则该行 Failed，展示真实错误，不开确认框。
2. 第一张确认框：Object 为将删除的文件夹路径；说明这些文件夹会被整个清空。默认 Cancel。
3. 第二张确认框：同一组路径；说明不可撤销，服务和这些文件夹会被永久删除。默认 Cancel。Enter/Esc 仍取消。
4. 两次都主动选 Uninstall 后，退出 alternate screen，现有 cleanup，再 `RunForce`。

按钮仍是 Cancel / Uninstall。其它 System section 不动。

## CLI

- `service uninstall`：只卸服务，不变。
- `--purge --yes`：检查文件；未识别则失败，不卸服务、不删。需要 `bin/core-channel`。
- `--force` 没有 `--purge`：非法参数。
- `--purge --force` 没有 `--yes`：仍是 `--purge requires --yes`。
- `--purge --yes --force`：跳过内容检查，整根删除。进度和 JSON envelope 与现有 purge 成功路径相同。

## App 契约

`Preview(ctx) ([]UninstallTarget, error)` 只解析根并检查根形态，不调用内容白名单。

`Run(ctx, progress)` 仍在停服务前后做 `CheckUninstallFiles`。

`RunForce(ctx, progress)` 不做内容检查；删除前仍拒绝符号链接/非目录根。顺序不变：data/logs → program → 其余。

## 测试

TDD。临时目录与 fake。

- 识别树含 `bin/core-channel` 时检查通过
- `Preview` 在根内有未知文件时仍成功并返回这些根
- `Run` 遇到未知文件不卸服务、不删
- `RunForce` 删除含未知文件的根；根是 symlink/reparse/非目录时仍失败
- TUI 第一次确认后出现第二次确认，两次默认 Cancel；取消第二次不 `Run`
- CLI `--purge --yes` 仍调 `Run`；`--force` 约束；`--purge --yes --force` 调 `RunForce`
