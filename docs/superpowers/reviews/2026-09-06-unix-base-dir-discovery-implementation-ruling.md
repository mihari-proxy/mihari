# Unix base dir 只读发现算法实施裁定

日期：2026-09-06。对应设计 R4 §5–6、执行计划 R3 T04/T05/T19。状态：算法经独立审核认可，代码与原生验收尚未完成。R4/R3 原始文件与既有审核哈希保留，本记录明确列出实施算法差异。

## 问题与边界

普通用户必须能经 root0711 B 读取 root0644 C、连接控制 socket，不能要求目录读取权限。Linux O_PATH 不支持 Fgetxattr；macOS12 没有 O_SEARCH，而当前 Go1.26.5 仍支持 macOS12。不能因此修改 B 权限、略过 ACL、增加辅助服务、提高最低版本，或让有效系统布局一律失败。

裁定只涉及无写入能力的发现路径。writer root、锁、创建、发布、安装与迁移仍逐段持有目录 fd；只读能力不得转换成 writer，也不得修复发现的权限或创建凭据。root 及可改变系统挂载的管理员仍属 R4 信任范围；私有实例不防御自己的 UID。peer UID 必须在传送任何认证字节之前核验。

## Linux

逐段使用 O_PATH、DIRECTORY、NOFOLLOW、CLOEXEC 并保留句柄。ACL 查询可通过内部构造的 `/proc/<当前数字pid>/fd/<仍持有的数字fd>`，只跟随最后一个已经验证的内核 fd 引用。验证 procfs 类型、挂载身份、组件 owner/权限/类型与名字身份；目标 fd 不得在查询期间关闭或复用；查询前后与目标 Fstat 身份比较。选择器只限固定 access/default ACL，完整解析并保留现有失败分类。

这是严格限定的内核句柄元数据桥。禁止 readlink 后重开其文本目标、调用者传入 proc 路径、普通应用 symlink、仅以 mode 替代 ACL，或把不支持查询当作无 ACL。没有新内核版本要求。

## Darwin，包括 macOS12

保留从根获取的可信、可读祖先 fd；对无法读取但可搜索的目录尾路径，逐组件使用相对该 fd 的 Fstatat 与 Getattrlistat，NOFOLLOW 只负责当前末组件。每个前缀必须先证明不受不可信 UID 修改，才可成为后续 syscall 的中间前缀。

组合元数据需包含并交叉核验目录类型、owner、mode、64位身份、完整 ACL 和真实挂载 fsid；严格校验返回属性集、顺序、边界和 ACL 相对偏移。禁止父目录写/添加/删除子项与目标自身 DELETE、WRITE_SECURITY、TAKE_OWNERSHIP 等授权；不能仅检查父目录权限。B 仍无扩展 ACL且 root0711。仅允许 R4 的三个 OS 别名，并重新验证确切目标。

XNU 对合法无 ACL 对象不设置 EXTENDED_SECURITY 返回位，因此该位缺失本身不是无 ACL 的证据。此时执行绑定同一对象的独立 ACL-only 查询：不请求 RETURNED_ATTRS、不启用 PACK_INVAL_ATTRS、启用 REPORT_FULLSIZE，并保留 NOFOLLOW 与前后身份检查。XNU8019 在这一模式下会对 unsupported va_acl 返回 EINVAL；只有成功且严格合法的零长度引用才可证明受支持查询得到无 ACL。错误、截断、非法引用、身份变化均拒绝；组合查询无 ACL 位而独立查询得到非空 ACL 视为观察变化，不接受为无 ACL。其他必需属性和 REALFSID 的返回位仍必须齐全。

未持有目录自身 fd 的尾路径使用“核心身份、owner/mode/type 与 ACL”的严格同 syscall 查询，再与主组合查询的真实挂载身份核对；不只凭两次 stat 相等推定同一对象。已持有目标 fd 时可采用 ACL-only Fgetattrlist。ACL-only 的标准空结果为 total12、相对 offset8、length0；组合查询按自身固定布局严格检查引用，零长度不豁免边界检查。

挂载使用 ATTR_CMNEXT_REALFSID，与普通 dev/ino 分开处理。匹配可读祖先的 Fstatfs，或以完整 Getfsstat 快照按真实 fsid 唯一匹配；保留 local APFS/HFS、owner语义启用等既有检查。容量饱和时有界增长；缺失、重复、截断、查询失败或相关元数据变化均拒绝。允许选定应用锚点之前的合法 OS 挂载跨越，锚点之后拒绝嵌套挂载。`f_owner` 是挂载元数据，只用于相关快照一致性，不新增 root/私有 UID 白名单。

最终 C 使用相对可信 fd 和已验证前缀的 Openat，NOFOLLOW、NONBLOCK，取得其自身 fd 后完成 regular/nlink/owner/mode/ACL/mount/尺寸检查，再清除 NONBLOCK 并有界读取。返回前复核目录与文件名字身份。E 的路径连接仍遵循 R4 已有 socket 合同并核验 peer。

**与 R4 字面算法的差异：** 无法获取可读 fd 的 Darwin 尾组件会在后续相对 syscall 中重新解析，不是每段永久 pin 住。安全依据是不可信主体没有修改已接受前缀的权限，不是两次 stat 能防止 ABA。对受信任管理员并发变更的 pinning 保证较弱，发现变化仍拒绝。若未来要求防御 root 或同 UID 攻击，必须重新设计；本裁定不作这种承诺。

## 审核与验证义务

算法独立审核结论为 APPROVE_PROPOSED_RULING；不是代码审核或原生 PASS。T04/T05 须实现纯 ABI/parser、ACL、挂载快照、逐前缀 trace、无写入/无重开、资源关闭和零认证泄漏测试；T19 保留原生 Darwin、搜索但不可读目录、root0711/0644跨 UID、别名、挂载、权限与并发验收。macOS12 兼容路径需要实际原生证据，不能用 Windows 交叉编译或较新 macOS 的通过声称旧版已测。

源码依据：[Go1.26 Darwin 支持范围](https://go.dev/doc/go1.26#darwin)、[macOS12 XNU 路径与属性实现](https://github.com/apple-oss-distributions/xnu/blob/xnu-8019.80.24/bsd/vfs/vfs_attrlist.c)、[XNU fstatfs/getfsstat 共同字段来源](https://github.com/apple-oss-distributions/xnu/blob/xnu-8019.80.24/bsd/vfs/vfs_syscalls.c)、[Linux proc fd 引用](https://github.com/torvalds/linux/blob/v6.12/fs/proc/base.c)。这些支持算法推论，不能替代原生测试结果。
