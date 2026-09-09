# Unix base-dir implementation status

> **2026-09-09 状态更新：** 本文 T06–T08 关于完整 RootConfigPolicy、字段白名单及新建受管 provider 资源图的实现状态已由[移除方案](2026-09-09-remove-root-config-policy.md)替代；TUN 仅覆盖 `tun.enable`。Unix 布局、核心身份校验、安装事务与历史 provider/resource WAL 恢复约束继续保留。下文是 2026-09-08 的实施历史快照，不再作为当前配置生成合同；现行操作语义见 [Unix 布局与恢复](../../unix-layout.md)。

Updated: 2026-09-08. Feature branch: `feat/system-data-root`.

T01–T18 development gates are accepted. T19 now supplies the isolated native runner, required fixtures, CI and maintained documentation; fix1 addresses the initial review’s scenario-inventory, live-child recovery and early-preparation ownership findings and awaits scoped re-review; its local verification and outstanding hosted acceptance are recorded in [the implementation evidence](../reviews/2026-09-08-unix-base-dir-implementation-evidence.md). This is implementation status, not a hosted security PASS or merge authorization.

The reviewed [R4 design](../specs/2026-09-05-unix-base-dir-design.md) and [R3 plan](2026-09-05-unix-base-dir-implementation-plan.md) remain immutable. Their planning-time unchecked boxes do not describe the current implementation. Operational behavior is documented in [Unix layout and recovery](../../unix-layout.md).

| Tasks | Delivered behavior | Acceptance boundary |
| --- | --- | --- |
| T01–T05 | Captured layout, trusted roots/leases, socket discovery and per-request credentials | Native owner/ACL/mount/peer/two-UID evidence required |
| T06–T08 | Typed root policy, trusted core provenance, provider resources and activation/recovery before business stores load | Accepted task fixes; final broad review must retain cross-store recovery and inactive-provider observations |
| T09–T11 | Fixed machine diagnostic snapshots, ZIP v2, UID-local TUI logging/export and cancellation/join | Windows/private ZIP v1 compatibility preserved |
| T12–T17 | Native install journal/actions, platform definition adapters, migration/recovery, validation and unified Unix installer | Fake OS manager avoids real service installation; native filesystem/process matrix is a separate required job |
| T18 | System defaults, process assembly, private service activation, source discovery and locked channel metadata | Accepted fix1 includes actual signal/join and five CLI exit-contract rows |
| T19 | Guarded ephemeral native workflow; exact evidence verifier; Linux bind and Darwin ABI fixtures; native forward/reverse action faults and authenticated subprocess tests; docs | Implementation/local checks delivered; independent task/broad review and exact published-head hosted CI/Bot results remain controller-owned |

The former T08 open-defect list was resolved through its accepted fix/review rounds. It is not an accepted product limitation. Final review still checks the current cross-package consumers, including inactive catalog/cache durability and platform service inspection failure handling.

No real service/core/subscription operation, user-data migration, merge, or privileged workstation account/mount test was performed. The planned current hosted macOS target is macOS 26 arm64; the eventual job must record its actual OS/architecture. Native runtime is pending, and that target cannot prove macOS 12 amd64 runtime compatibility. Cross-compilation is recorded separately.

Publication must preserve one integrated feature PR to `dev`, inspect all required ordinary jobs and both native jobs plus `unix-layout-security`, and inspect actual Bot findings tied to the final head. A skipped Bot review, a prior-head PASS, or a successful artifact upload does not close these gates.

The integrated publication is [PR 211](https://github.com/mihari-proxy/mihari/pull/211). Its corrective review rounds and actual hosted failures are recorded in the [implementation evidence](../reviews/2026-09-08-unix-base-dir-implementation-evidence.md#pr-211-corrective-review-evidence). Native execution has now run on both hosted targets: at `a9e1c79`, all mandatory cases except FullAssembly and all cleanup stages passed. FullAssembly's export fixture and subsequent review fixes still require a completed run on their published head. The PR description records the latest head, checks and bot verdicts; this historical status does not claim those outstanding gates passed.
