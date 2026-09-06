# Root 配置策略 v1

本文记录 T06 的固定源码合同、完整类型化生成边界与实现裁定。默认接线归 T18；T06 只提供可注入接口，不能提前启用系统模式。独立安全审核是任务交付 gate，声明计数和测试通过均不能代替该审核。

策略唯一标识为 `mihari.root-config/v1/mihomo-v1.19.30`，由 `subscription.RootPolicyID` 导出。核心源码固定到 [ac017cdd246ce8bd547653d927e7bf77d7ee73d5](https://github.com/MetaCubeX/mihomo/tree/ac017cdd246ce8bd547653d927e7bf77d7ee73d5)。仅 Linux/macOS 的 amd64/arm64 组合属于该策略；T07 另外验证四个核心制品的可信身份。配置检查不能代替制品认证。

## 类型与错误边界

注册表编译进 Go，不从下载数据或测试 JSON 加载。私有 `policyValue` 用不同标记保存字符串、布尔、带符号整数、无符号整数、有限浮点数、列表和有序对象。原始 `yaml.Node` 仅供严格解码；输出每个节点与映射键均重新创建。动态键只能出现在显式注册的数据映射中，不能扩展对象字段。

输入只有一个 YAML 文档，订阅字节上限沿用 16 MiB。未知键、重复键、规范化碰撞、错误类型、合并键、别名、锚点及自定义标签拒绝。引用类型的 nullable 语义逐字段注册，不能把任意 null 当作空对象。支持的别名必须与固定解码器对应；规范化不能让两个不同拼写覆盖同一字段。

`PolicyError` 只携带安全的注册字段路径及既有错误码。动态键、URL、密码、名称和资源内容不进入错误。未知核心组合使用 `invalid_state`；不能安全处理的数据使用 `data_failure`。取消通过 context 返回。

## Manager 所有的能力

mixed/controller 地址必须是带非零端口的明确 loopback IP，controller secret 必须存在。只从 Manager settings 生成这些值。订阅的 TUN 块不能授予能力；Manager 的 TUN 输入只接受当前生成器已使用的 `enable` 与 `stack` 类型。未知 settings.Tun 字段也不能被通用映射复制。

用户提供的替代 controller、任意 listener、外部 UI 路径、原生 provider 下载、一般 override expression、任意文件证书回退与持久化协议能力不能交给 root 核心。固定系统 resolver/hosts/CA 读取和 socket 路由标记属于已审计网络功能，不能推广为任意路径或调用者环境读取。

`find-process-mode` 的 strict/always/off 保留固定 OS 进程元数据发现，用于 PROCESS 网络匹配。规则内进程名或路径只是匹配数据，不能传给文件打开或执行接口。`global-client-fingerprint` 在该版本 [只产生弃用告警](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/config/config.go#L756)，登记为已知弃用字符串后省略；不能声称它仍设置 TLS 指纹。

## Provider 身份与两阶段准备

Provider 身份是 `(subscription ID, generation, kind, name)`，不包含最终代理名称。版本 1 的哈希输入是 Go `encoding/json.Marshal` 按声明顺序编码的对象：

```json
{"schema":"mihari.provider-identity/v1","subscription_id":"00000000000000000000000000000001","generation":1,"kind":"proxy","name":"example"}
```

不带尾随换行；保留 Go JSON 对控制字符、HTML 字符和 U+2028/U+2029 的转义。输出为完整 SHA256 的 64 位小写十六进制。名称不作路径拼接；相同名字在不同订阅、generation、kind 下不共享身份。

`PolicyInput.Resources` 的 HTTP provider 键是目标身份哈希；file 源按 `SourceResourceID` 提供已验证对象内容，inline 内容来自声明本身。三种源的目标都使用同一身份规则。输出文件只可能为 core-home 相对 `providers/<id>.yaml` 或 `.txt`。file 源的 `path` 是已注册的源对象 ID，绝不是本机路径。资源映射还接受下文四个保留 Geo ID；未知、额外或未注册资源不能通过扩展映射获得文件读取能力。

`ProviderSpec.SourceResourceID` 保留源到目标的引用。T08 从本次订阅候选可访问的私有 catalog/可信资源图验证源对象存在和身份，复制到目标 ResourceID，并在提交时重查源/目标/内容哈希。语法正确不代表对象可信，策略自身仍验证完整内容。T15 显式迁移旧资源映射；缺失不能降级为空 provider。

HTTP 的 `Header map[string][]string` 保留合法重复值并复制；T08 必须消费它。`MaxBytes int64` 是实际下载限制，不能超过单源 16 MiB。固定上游 [vehicle.go](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/component/resource/vehicle.go#L155) 的非正 size-limit 表示无上游限制；本策略始终仍施加 R4 的硬上限。下载复用订阅 direct/proxy/auto；自定义 provider.proxy 不能被静默忽略或伪装生效。总数最多 256，provider 内容合计最多 256 MiB。

`Inspect` 与 `Build` 共享解析与语义处理。Inspect 只返回 Providers/Geo 要求，没有可执行 YAML；允许尚未准备的源资源用于发现，已提供及 inline 内容仍必须验证。T08 先发现并准备 provider 闭包，再准备 Geo，最后 Build 强制完整资源。各阶段使用同一订阅与 generation 快照，提交重查。不存在公开 skip-validation 选项。

## Provider 变换与正则

固定核心 [provider.go:390–468](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/adapter/provider/provider.go#L390) 先按原始名称过滤/去重，再应用 override，最后 ParseProxy。一般 `override-expr` 可修改任意字段，明确拒绝。其他简单能力字段覆盖先物化，再验证所有节点能力字段。

仅名称的 `override.proxy-name.pattern/target`、additional-prefix/suffix，以及 filter/exclude 字段可在固定 file provider 内保留严格类型。名称变换只能修改 `name`，不能构造资源路径、命令、监听器或新的配置字段。资源身份不依赖变换结果，任何后续路径生成同样不得使用代理名称。

代理组 filter/exclude 也只选择已验证节点名称，见 [groupbase.go](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/adapter/outboundgroup/groupbase.go#L64)。保留 regexp2 的 .NET 语法，不用 Go RE2 冒充。受限候选核心的 `-t` 检查正则、重复名称及网络引用语义；策略必须独立证明所有保留字段和变换不能产生文件/执行/监听能力。该分层没有新增 regexp2 依赖，也不声称普通测试已执行真实核心。

## Inline 凭据与网络字符串

TLS certificate/private-key 只有实际 `tls.X509KeyPair` 成功才允许，因为固定 [ca/keypair.go](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/component/ca/keypair.go#L28) 解析失败会回退本机文件。CA 必须由标准库成功解析，不能仅靠 PEM 标记。

SSH 与 TLS 不同：[ssh.go](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/adapter/outbound/ssh.go#L124) 在解析前选 inline 分支，解析失败直接返回，不再回退文件。策略验证单一受支持 PRIVATE KEY PEM 块及结构/大小；加密 OpenSSH 的密码学有效性可留给受限核心，不引入 SSH 库依赖。仅搜索 PRIVATE KEY 子串不能构成策略验证。

实际 SSH 依赖是 [metacubex/ssh v0.1.0](https://github.com/MetaCubeX/ssh/blob/v0.1.0/keys.go)，不是相邻依赖 x/crypto v0.33.0。前期来源归属已按 import/go.mod 更正。RSA/EC/PKCS8 使用标准库解析，DSA 使用完整 ASN.1 结构；OpenSSH 验证单密钥、长度字段、已注册算法、未加密字段和 padding，bcrypt/aes256-ctr/cbc 密文只作内联结构证明。严格单 PEM 块会拒绝上游容忍的前导垃圾、多块与尾随内容。

SSH host-key 的 ParseAuthorizedKey 保留 options/comments/多行语法；外层算法文本被忽略，类型来自二进制首个 SSH string。options 即使含 command 也只被解析并丢弃，最终只比较 key.Marshal。策略保留这些文本、已知公钥/证书类型及有界 base64/SSH 外层结构，内部算法/证书密码学检查归同一可信候选核心。普通测试使用真正上游明文/加密 PEM fixture，附 BSD 许可，不执行 SSH 或 mihomo。

Mieru [验证与构造](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/adapter/outbound/mieru.go#L206) 使用完整且区分大小写的 protobuf 枚举键 MULTIPLEXING_DEFAULT/OFF/LOW/MIDDLE/HIGH 和 HANDSHAKE_DEFAULT/STANDARD/NO_WAIT，不接受文档简写为隐式别名。port 与 port-range 必须互斥且存在，范围 1..65535；策略拒绝 fmt.Sscanf 容忍的尾随垃圾及符号/空白歧义。traffic-pattern 经有限 wire schema 验证后重新编码；Base64 自身不构成语义证明。Client.Store 是内存配置，不产生持久化路径。

AnyTLS 的 [settings frame](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/transport/anytls/session/session.go#L86) 固定开销 56 字节，长度写为 uint16，因此 client-metadata 最多 65479 个 UTF-8 原字节。[StringMap](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/transport/anytls/util/string_map.go#L9) 使用 LF 分隔，拒绝 metadata 内 LF，保留单独 CR。idle 两字段检查有符号秒数乘法，保留原生 <=5 秒默认 30 秒语义；min-idle-session 仅懒惰计数比较，保留负值。ShadowTLS/Restls/JLS 同时激活时拒绝；ECH 可并存。Restls 保留缺省脚本、ASCII 空格、空项和省略数字为零的实际语法，不替换随机算法。

TrustTunnel [pool](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/transport/trusttunnel/client.go#L251) 的三个 count 字段也是懒惰比较，全部零才默认 max-connections=8/min-streams=5。所给 ALPN 在 TCP 时须含 h2，在 QUIC 时须含 h3，省略按原生默认。共享 [QUIC controller](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/transport/tuic/common/congestion.go#L18) 的空/未知选择保留原生无操作；BBR profile 未知回退 standard。仅 QUIC 和 BBR 同时有效时检查 cwnd×1280 的有符号表示范围，允许负值和 1；不声称覆盖全部 BBR 反馈、MTU、浮点或累计计数的运行时行为。

TLSMirror 是 HTTP 载荷构造；Restls 是 TLS record shaping DSL；AmneziaWG 是 WireGuard 网络参数/标签。不能因为字段含 script 字样而整体拒绝。HTTP method 接受完整 token grammar，header 验证并保留合法多值。算术转换和分配必须避免溢出，不能改写已固定的 transition/cycle 算法或随意禁用 h2 并发。

AWG 字符串禁止 CR/LF 跨 UAPI 配置边界注入，并按 legacy/v3 完整语法解析和生成。固定 ClientBind 不因 UAPI listen_port 产生任意监听，不以未经证实的监听漏洞作为拒绝理由。Restls 按实际可达最大长度验证：TLS 1.2 GCM 可达时 16364，其余 16372；随机长度取最大可达值。数值来源是 record 缓冲区扣除协议开销，不是任意兼容上限。

## DNS、hosts 与 sniffer

DNS nameserver 字符串按固定 scheme 和 fragment grammar 处理，不能把 URI 当作无语义字符串。允许固定 system resolver、精确 `dhcp://system` alias；实际 `dhcp://interface` 会建立 DHCP UDP68 listener，因此拒绝。`ts` DNS 引用需要有效 outbound 资源；Tailscale/ZeroTier 自带持久化/本地 planet 能力而不支持时，报告资源图错误，不能错误归因于 DNS URI 自身文件能力。

有序 DNS policy 的连续 domain trie 分段保持原顺序。动态 hosts 的地址、列表、别名与 `lan` sentinel 分别类型化，LAN 不在策略中枚举主机接口。拒绝别名循环与规范化碰撞。sniffer 的旧字段优先级、nullable override 和 TLS/HTTP/QUIC 判别器按固定消费者保留；端口范围禁止原生 uint64→uint16 截断。

hosts 与 DNS policy 的 string-or-string-list 是显式 union，fresh 输出统一为列表；地址重复项保留原有随机选择权重。hosts 别名按 [NewHostValueByDomain](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/component/resolver/host.go#L110) 仅去除首尾点并要求至少两段，不把任意未匹配别名误作文件路径。[trie](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/component/trie/domain.go#L30) 的 literal、单标签星号、任意层级点通配优先级决定别名边；迭代遍历及三色图检查拒绝完整闭包中的循环。`+.domain` 展开与 apex/点通配的节点碰撞拒绝，这是消除原生 map 迭代覆盖歧义的输入限制。

[DNS 解析](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/config/config.go#L1401) 保留省略字段与显式空列表的区别、proxy bootstrap 条件，以及仅 fake-IP 模式有效的池/过滤/TTL。地址池需至少三位 host bits，因为起点为 network+4 且必须小于最后地址。声明的 IPv6 前缀始终接受稳定类型/结构校验；原生 parseIPV6 还会依据全局 ipv6 和 OS probe 清空 v6 range，策略不探测开发主机。DNS listen 只接受 loopback 数字地址或 native [preResolve](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/adapter/inbound/listen.go#L84) 特判的 exact localhost；空地址/数字0保留禁用语义，不通过 DNS 查主机名。mark 只允许一种32位 socket bit pattern 的 signed/unsigned 表达；非 Linux 固定消费者忽略该选项。

[DNS cache](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/dns/resolver.go#L488) 的0默认4096；只有 exact arc 使用 ARC，其余字符串原生退回 LRU。负容量属于 lazy/unbounded 分支而非 eager make。正 ARC 容量检查实际 [2*c](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/common/arc/arc.go#L192) 的 signed 表示范围；不虚构内存预算或保证物理可用性。有序 DNS policy 每逢 Geo/provider matcher 会结束当前普通域名 trie；输出保持该分段和空列表优先匹配，规范化碰撞仅在同一 trie 内拒绝。

[Sniffer](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/config/config.go#L1754) 非空 sniff map 替代 legacy sniffing/port-whitelist；被替代值仍须是已声明 string lists，但未知原生 dormant 字符串不假装生效。空 map 时旧字段正常激活；enable 本身不添加任何默认协议。端口使用 [range-list](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/common/utils/ranges.go#L33) 语法，支持反序、端点方括号、空字符串和空白0，拒绝 uint64→uint16 溢出，不套用另一个 combined parser 的星号或28条限制。捕获流量元数据不生成新监听或本机路径；Geo/provider 选择器必须进入完整资源闭包。

## Geo 资源

Root 专属 Manager TUN 输入只接受现有 runtime.buildManagedTun 生成的 enable/stack；空配置明确 enable=false。stack 按 [native enum](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/constant/tun.go#L23) case-fold，显式空字符串不合法。订阅 RawTun48 个字段均按声明类型解析后丢弃，不能携带 device/file-descriptor/routes 进入受管配置。Settings 历史 map 的其他 key 在 root seam 拒绝，不改变 Windows/非 root 的原持久化和 Generate 行为。

根 authentication 是网络凭据数据；skip-auth/lan masks 是 typed CIDR 且不能覆盖 Manager allow-lan=false。Geo/profile 用户输入只做类型校验后由 Manager 禁用原生写入。global-client-fingerprint 仅有 [弃用告警](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/config/config.go#L755)，experimental.fingerprints 和 clash-for-android 字段无此 pin 的实际消费，均省略。experimental 的 [GSO/ECN](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/hub/executor/executor.go#L214) 布尔值只设置固定网络特性变量，IP4P 只切换远端地址转换，不允许调用者选择任意环境变量。NTP 的 [非正 interval](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/ntp/ntp/service.go#L36) 在乘分钟前停止，port0保留原生网络数据表示；write-to-system=true始终拒绝。

规则由 [ParseRulePayload](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/rules/common/base.go#L47) 的实际逗号/ASCII空格语义解析成私有判别 AST，顺序保留，按已验证字段重新编码。IP-CIDR/IP-SUFFIX 保存解析的地址和位数而不抹掉后缀匹配所需 host bits；端口/UID/DSCP 使用实际范围语法并拒绝原生窄整数截断。IP-ASN 是任意字符串的相等比较，不能误缩成数字-only。DOMAIN/PROCESS 的 regexp2 仅匹配元数据，不替换配置字段；完整 .NET 语法留同一可信核心验证，策略不以 RE2 仿冒。process path 仅匹配进程元数据，不打开文件或执行命令。

[复合规则](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/rules/logic/logic.go#L88) 按实际括号选区构造 AST：普通 AND/OR/NOT 跳过完整外层括号，SUB-RULE 先有隐含外层；非选中分隔垃圾规范化掉。AND/OR 允许零子节点，NOT/SUB-RULE 恰一子，选中 MATCH/SUB-RULE 禁止。括号扫描连 regex 内平衡括号也计数，不引入另一套转义规则。策略单次扫描建立选区索引并迭代解析/生成，不设无来源深度上限。

[sub-rules 全图](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/config/config.go#L1029) 允许前向引用、共享后继、空列表和非标识符名字；所有已声明列表包括 unused 列表都验证。每个 SUB-RULE 必须指向真实 sub-rule，明确收紧顶层“命中同名 proxy 就跳过缺引用检查”的原生漏洞。迭代三色图检查拒绝循环，不影响 DAG。原生 verify 无 memo，且 [matchSubRules](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/rules/logic/logic.go#L179) 对成功嵌套 SUB-RULE 重复递归，可产生指数工作；策略的有限图证明不声称解决该可信核心运行时成本。资源闭包必须逐个遍历全部子规则列表，不能只收集 SUB-RULE 条件的 ProviderNames。

Geo 使用独立的 `GeoResourceKind` 和 `GeoResourceSpec{Kind,SHA256,Bytes}`。四种 kind 的目标只能是 `Country.mmdb`、`ASN.mmdb`、`GeoIP.dat`、`GeoSite.dat`。GeoResourceID 的唯一编码为 `mihari.geo/<kind>/v1`，属于编译保留空间，与 provider 的 64hex 身份互斥。core-home 只保留单一 canonical 文件别名，不能让旧 geoip.db/geoip.metadb 抢占 Country 数据。

资源闭包包括主规则、sub-rules、递归逻辑、所有 classical provider、DNS policy/fallback/fake-ip 与 sniffer 的 Geo 选择器。DNS/sniffer 即使 disabled 也可能在解析期触发初始化。DAT 的隐含 CN 分类及实际 selector/attributes 都须验证，不能把存在或 hash 相等当作结构有效。

MMDB 复用已有 maxminddb/v2 的 Verify 并检查 country/ASN 数据语义；DAT 使用有界的固定 protobuf wire schema，完整检查 framing、wire type、UTF-8、CIDR、regexp、oneof、分类碰撞与选择器。相同私有 wire 工具只复用于 Mieru traffic-pattern 的实际固定 schema；不复制通用 protobuf 库，不引入新依赖，未知字段明确拒绝并记录版本兼容成本。输出 bytes 复制，digest 从实 bytes 计算；来源认证归 T08 的编译可信 catalog。

Country/ASN 沿用既有锁定资产。初版 DAT 来源固定 MetaCubeX artifact commit `b3a0635a5ff10e63d300a050aa38edf7f138ef2f`，GeoIP 17,120,329 bytes / SHA256 `4149e607530f91da697bad4696f8c59f0a475af38e69405e4124438c9886c721`；GeoSite 4,242,906 bytes / SHA256 `7104fc19469298564947d42c320a1d5442416f1f72648bf516314d719594338c`。T08 实际认证下载；T06 的 hash 计算不授予可信来源。上游 orphan artifact 可能消失，失败保留旧部署或已验证缓存，禁止 fallback latest。没有修改 release lock、发布镜像或授权任意更新源。

四个 geox-url 显式空且 geo-auto-update=false 只属纵深。固定 [Geo 初始化](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/component/geodata/init.go#L39) 在缺失/损坏时仍可能删除文件并调用原生下载；auto false 只挡启动 scheduler，不能挡手动 controller Geo 更新。必须在 `-t` 前准备全部有效资源，T08 同时阻止绕过 Manager 的 native mutation。

## 首批源码消费点

根级 mode/log/process 枚举分别由 [tunnel/mode.go](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/tunnel/mode.go)、[log/level.go](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/log/level.go) 和 [find_process_mode.go](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/component/process/find_process_mode.go) 消费。根 keepalive 秒转换发生于 [executor.go:406](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/hub/executor/executor.go#L406)，因此限制到可表示的 time.Duration，保留负值 sentinel。

四种代理组共同字段与协议分派在 [outboundgroup/parser.go](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/adapter/outboundgroup/parser.go#L25)。interval 是秒、timeout 是毫秒，见 [healthcheck.go](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/adapter/provider/healthcheck.go#L210)。expected-status 使用 [ranges.go](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/common/utils/ranges.go)，保留逗号/斜杠、反向范围、空值与星号，限制原生 28 段并拒绝窄整数截断。selector.default-selected、url-test.tolerance、load-balance.strategy 只能属于对应分支。

### Additional implemented pinned network consumers

The following entries record delivered implementation decisions and indexed registry evidence. Independent review gates and default activation remain pending.

- Hysteria v1: [adapter precedence and rate/window defaults](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/adapter/outbound/hysteria.go#L192). Policy materializes the effective protocol and removes obfs-protocol. Effective faketcp is rejected because its Linux transport calls iptables Exists/Append on OUTPUT; udp/wechat-video remain. Native Speed runs before positive speed aliases; negative bandwidth aliases are rejected and positive signed multiplication is checked. Both window defaults inspect ReceiveWindow==0, so an explicit dormant stream window is preserved. Local rate checks do not bound peer ServerHello.RecvBPS or all runtime congestion arithmetic.
- Hysteria2: [exact sing-quic client](https://github.com/metacubex/sing-quic/blob/38b0e9295f51d1e96e7564b1d804c2e3a56c8610/hysteria2/client.go) uses active Gecko0 defaults512/1200 and1<=min<=max<=2048, initial QUIC windows require62-bit representation while maximum windows are natively clamped. Realm is an injected HTTP/STUN/NAT client, with no arbitrary local server or filename. Server URL is HTTP(S), token/password HTTP values, nested TLS credentials inline-validated even inactive. Port-list range syntax follows the pinned parser including slash/comma/brackets/reversal but rejects narrowing overflow; maximum28 ranges is native, not an invented limit. Handshake nonpositive timeout follows caller context; signed seconds are checked before conversion. BBR fallback is always reachable; local sendrate bounds only its local signed conversion.
- Hysteria2 UDP MTU: [packet.go](https://github.com/metacubex/sing-quic/blob/38b0e9295f51d1e96e7564b1d804c2e3a56c8610/hysteria2/packet.go#L77) computes H=8+varintLen(destinationBytes)+destinationBytes. For positive payload a fragmentation step requires effectiveM>H. The policy preserves0=>1197, rejects nonzero<=12 as a weak necessary sanity check, and preserves larger positive signed integers.13 is only a generic a:0 example (H12); normal [::1]:1 and192.0.2.1:53 instead need17 and22 respectively for a one-byte step. There is no universal configuration-only proof: IPv6 zones and generic FQDN callers are not globally bounded, a DatagramTooLarge retry substitutes an unchecked peer/PMTU value, and small capacities can overflow uint8 fragment counts. Default1197 does not solve those trusted-core runtime limitations. No272/57/73 global bound or all-payload claim is made.
- Snell: [constructor](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/adapter/outbound/snell.go#L171) supports versions1..4 with0 default1 and5 alias4; UDP requires3+. Reuse is always enabled for2, conditional for4, otherwise dormant. The obfs mapping is a closed typed union of HTTP, TLS, ShadowTLS, Restls and JLS field families; absent native host defaults remain effective. [Simple TLS-obfs](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/transport/simple-obfs/tls.go#L78) first chunks writes at16384 and emits uint16(212+dataBytes+hostBytes), deriving host<=48939 bytes. This is an entire first-chunk framing bound, not a DNS name bound; HTTP obfs does not inherit it.
- Trojan: [constructor](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/adapter/outbound/trojan.go#L283) hashes a string password, supports ws/grpc and native TCP fallback, and excludes simultaneously active Reality/ShadowTLS/Restls/JLS. Its optional SS wrapper uses the [legacy PickCipher registry](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/transport/shadowsocks/core/cipher.go#L111), not the separate SS2 registry. All31 canonical/alias cipher names are tested with native case folding; disabled fields are typed no-ops. [WS](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/transport/vmess/websocket.go#L333) parses only path/query onto an existing carrier, so a file-looking URI is data. Early-data limits are lazy byte-buffer reads, not count-sized allocation. [gRPC](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/transport/gun/gun.go#L325) service names become HTTP paths including leading-slash custom paths; user-agent/header and signed ping conversion are validated.
- TUIC: [constructor](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/adapter/outbound/tuic.go#L132) selects v4 with nonempty token, otherwise v5 with native arbitrary UUID-to-nil fallback. It performs packet+27 before frame clamp1400 and int64(count)+int64(ceil(float64(count)/10)); both actual expressions are checked. Native v5 datagram fragmentation needs frame>=28, while v4, QUIC relay and UOT do not run that path. This is positive fragment representation, not all-datagram fidelity: uint8 runtime fragment-count and congestion feedback limits remain. ECH options are parsed/stored in this adapter but the current dial path does not consume that stored config; no ECH handshake effectiveness is claimed.

- VMess uses the [pinned constructor](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/adapter/outbound/vmess.go): identity is an arbitrary string mapped by the actual sing-vmess client, not UUID-only; signed alterId selects legacy only when positive. MKCP TTI0 defaults50 then1..1000 and capacities default5/20 then1..4095 prevent the exact uint32 multiplication/division errors. Mekya h2 pool n>=2 eagerly allocates an interface slice and HTTP objects in its constructor, including during core validation; n<2 uses one. The checked 64-bit slice backing representation is not a memory budget or allocation-success guarantee. TLSMirror is a network HTTP request/transition DSL; duplicate header values, extension methods, cycles and signed weights whose running sum fits int32 are retained. Policy does not instantiate transports or run the core.
- SS uses [sing-shadowsocks2 v0.2.7](https://github.com/metacubex/sing-shadowsocks2/tree/7f844b0df8db54b1658884fb8c15cb7da5778f18), all39 case-sensitive methods. Its 2022 AES key chains are capped at1972 AES128 or1971 AES256 keys by the official normal32768-byte buffer TCP header equation; this is not a global MTU or peer-runtime guarantee. The actual UDP WriteTo path allocates payload plus exact overhead. Kcptun zero defaults precede range checks: conn1..65535, native smux frame/buffer limits, checked keepalive seconds and retransmission multiplier. Its negative keepalive reaches time.NewTicker in the actual smux session and is rejected. Effective nonpositive FEC counts disable initial transmit FEC; positive counts require D<=255 and P<=256-D, allowing256 total without claiming ReedSolomon itself is limited to256. Named manual mode retains tuning; native unrecognized plugin no-op names are explicitly rejected, a documented compatibility cost.
- MASQUE [constructor and branches](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/adapter/outbound/masque.go#L101) parse base64 SEC1 EC private and PKIX ECDSA public keys in memory. Policy performs those actual stdlib parses. name-cert-verify is a known unused placeholder. h2 does not run QUIC congestion; H3 uses packet1242 for BBR/meta_v2, while meta_v1's first product uses1280. L4 mode does not consume local prefixes, MTU or connect-IP URI; these remain typed dormant fields. Connect-IP URI is restricted to a literal HTTP(S) request URL without template variables. Ordinary H3 uses prefix parsing and checked uint32 MTU; no filename comes from a proxy name or key.
- The shared [IPStack selector](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/adapter/outbound/wireguard.go#L143) normalizes auto/gvisor/mips and cubic/reno/bbr/bbr3. The official with_gvisor artifact selects gVisor for auto, with fixed internal cubic behavior. [mipstack config](https://github.com/metacubex/mipstack/blob/b7038299fe13b267b76f12231a941e677c7c1030/config.go#L114) supplies68..65535 MTU, IPv6 minimum1280 and local-address/broadcast checks. [sing-wireguard stack](https://github.com/metacubex/sing-wireguard/blob/110eac03c3f0954ad5275c95ba027125ec20a0fd/device_stack.go#L49) creates internal NICs/routes and returns nil File; these are userspace network stacks, not OS interface or filesystem capabilities. Policy does not claim all runtime allocation or peer-address safety from numeric representation alone.
- OpenVPN [typed config](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/transport/openvpn/config.go#L227) consumes inline CA, certificate/key, static256-byte keys and tls-crypt-v2 PEM with more than256 decoded bytes. Policy validates CA and optional key pair using stdlib, exact mutually exclusive key modes, native proto/dev/cipher/auth normalization and checked seconds. Dev tun selects only the preceding userspace stack. Data-ciphers and fallback are arbitrary typed remote negotiation strings; unknown values may fail negotiation, without becoming local capabilities. comp-lzo only yes/adaptive enables native compression; other strings remain disabled. OpenVPN's udp option is a known no-op because its Base always enables UDP.
- OpenVPN [peer-info and wire strings](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/transport/openvpn/keymethod.go#L269) are a separately registered dynamic string-to-string schema. IV_VER overrides the built-in version; supplied IV_PROTO and IV_CIPHERS are ignored; supplied IV_LZO is ignored only when compression is yes. Other entries sort into remote key=value lines; LF/NUL remain native network string data, not local parsed options. Fresh output preserves these inputs for the pinned consumer. Duplicate YAML keys, non-string keys/values and nested maps reject without revealing the key. The final aggregated peer-info, username, password and options strings each truncate natively to65534 bytes before uint16 framing; this is neither a per-entry limit nor a guarantee that every metadata value reaches the peer. Server-pushed OpenVPN prefixes are unavailable during policy Build, so their runtime validity remains the trusted core's responsibility.

- WireGuard [configuration generation](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/adapter/outbound/wireguard.go#L589) emits only decoded fixed-size keys and validated explicit-peer CIDRs; top-level allowed-ips is an unused typed field. Explicit peers replace the top-level key branch, though top-level reserved still gets its native length check. Refresh seconds and uint16 keepalive/port conversions are checked. The three actual device constructors eagerly start worker goroutines. Their encryption queue reference count reaches W+P+2, so positive workers require W<=2147483645-P and P<=65536; zero keeps native CPU default and negative workers reject. P counts distinct decoded effective peer keys excluding self. Fresh zero private state keeps zero self through FromMaybeZeroHex/SetPrivateKey's equality return; other private keys use standard X25519 clamping/public derivation, checked by the [RFC7748 section6.1 fixed vector](https://www.rfc-editor.org/rfc/rfc7748.txt). This bound proves counter representation, not affordability of eager workers or a running tunnel.
- VLESS [constructor](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/adapter/outbound/vless.go#L461) accepts arbitrary identity mapping, ignores flows shorter than16 bytes and accepts longer values only when their first16 bytes equal xtls-rprx-vision. Unknown network names use TCP fallback, including names used for different transports by VMess; no VMess MKCP constraint is imported. Legacy ws-headers is typed but unused at this pin. Its [encryption factory](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/transport/vless/encryption/factory.go#L12) supports native/xorpub/random and1rtt/0rtt mixed32/1184-byte chains, not merely none. MLKEM requires each of768 packed12-bit coefficients below3329. Padding preserves native token byte partition, extra components, probability and reversed endpoint semantics, with checked65553 sum; gap milliseconds are checked for duration representation. Wholehello has dynamic allocation, not a32KiB cap. Low-order public-key and peer/runtime cryptographic failures are not claimed solved.
- XHTTP's [25+6+20 field declarations](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/adapter/outbound/vless.go#L91) form closed typed schemas. Download pointer absence/null inherits; explicit values replace, including false, empty arrays/maps and individual TLS pair members. JLS and Reality require explicit empty required leaves to clear their inherited modes: an empty object fails the native structure decoder. Other all-optional mode objects can clear with an empty object. Every effective TLS pair is parsed in policy before core fallback could interpret it as paths. H3 requires TLS and excludes special wrappers independently for each leg. XHTTP paths/query fields are network request data on supplied carriers.
- XHTTP [range/session handling](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/transport/xhttp/config.go#L320) retains inclusive ranges, plus signs, zero/default chunk normalization, each named preset and literal ASCII table. Policy computes nominal room with saturation at2^31 and bounded work; duplicate characters still count natively. The core repeats an eager big.Int calculation over its whole configured range, and session/padding generation allocates configured sizes; representation checks do not promise physical allocation success. MaxInt custom-length endpoints reject the actual loop wrap. Tokenish validates the actual float ceil conversion and possible150-byte growth. Active header/cookie placements validate their corresponding output grammar; stream-one/no-op branches are distinguished. XMUX counts are lazy soft targets, with only actual int32 and duration conversions bounded; packet-up requires positive post-range minimum to consume input. There is no invented128/1024 ceiling.
- AWG [legacy and v3 options](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/adapter/outbound/wireguard.go#L102) remain full network data families. Only version3 selects v3; other integers use legacy. H1–H4 alone accept the explicitly registered string-or-uint32 YAML union and emit new decimal strings for integers. Unknown fields, floats, booleans and integer overflow do not coerce. Raw UAPI values reject CR/LF; header-protection keys go through base64 decoding and native hex serialization. Complete known tags are parsed and canonicalized; unlike the native permissive parsers, policy explicitly rejects nonsemantic text outside the tag sequence. This spelling restriction is a compatibility cost, not a claim that native parsers reject it.
- AWG [legacy postconfig](https://github.com/MetaCubeX/amneziawg-go/blob/736a78668832cc477359d7e2d508ef06496d48d6/device_v1/device.go#L619) uses Unix B65535, nonnegative domain checks, active equal junk endpoints incremented before bounds, and distinct148+s1/92+s2/64+s3/32+s4, each below B. Its jc and special generator backing24*(g+jc) are checked without inventing a small count limit. [v3 junk and padding](https://github.com/MetaCubeX/amneziawg-go/blob/736a78668832cc477359d7e2d508ef06496d48d6/device/send.go#L344) has uint32 junk/uint16 sizes, active junk ordering only, no legacy length-collision ban, and s4<=65519 for the actual fixed-array header offset. That last equality is structural, not a full-MTU tunnel positive. Nonzero header-protection keys require every s>=12. Reject-after-time Hi is bounded by the actual signed seconds*3 operation. Legacy/v3 batch junk generation retains allocated packets and can exhaust resources well below representation limits; peer/runtime and allocation availability remain trusted-core limitations, not guarantees made by these controls.


## 生成接口和后续任务合同

`GenerateWithPolicy(context.Context, PolicyInput, PolicyBuilder) (PolicyOutput, error)` 原样传递原始 YAML、订阅身份、settings、资源及 context。`PolicyBuilder` 只有 `Build(context.Context, PolicyInput) (PolicyOutput, error)` 一个方法。nil builder 返回 `invalid_state`，不能回退 legacy；策略错误和输出直接返回。此入口不先调用 `ParseDocument` 或 map 重编码，以免丢失重复键等原始输入信息。旧 `ParseDocument`/`Generate` 语义与默认调用链保留。T18 负责唯一入口切换，并覆盖旧 runtime PatchConfigs fallback。

内部 Go 合同如下；它不是新的 CLI/JSON 或持久化格式：

| 类型 | 精确字段 |
| --- | --- |
| `PolicyInput` | `YAML []byte`；`SubscriptionID string`；`Generation uint64`；`CoreTag, OS, Arch string`；`Settings config.Settings`；`Resources map[string][]byte` |
| `PolicyOutput` | `YAML []byte`；`Providers []ProviderSpec`；`Geo []GeoResourceSpec` |
| `ProviderSpec` | `SubscriptionID string`；`Generation uint64`；`Kind, Name, Format, Behavior, URL, ResourceID string`；`SourceResourceID string`；`Interval time.Duration`；`Inline []byte`；`Header map[string][]string`；`MaxBytes int64` |
| `PolicyRequirements` | `Providers []ProviderSpec`；`Geo []GeoResourceKind` |
| `GeoResourceSpec` | `Kind GeoResourceKind`；`SHA256 string`；`Bytes []byte` |

`Inline` 在完整 Build 中是验证并重新生成后的内容，不是任意原始 bytes；Inspect 对未准备源可能只返回源说明。源内容、输出内容及 headers 都复制，调用方不得在并发 Build/Inspect 期间修改输入 map/bytes。策略本身不做网络、文件写入、进程或主机探测，也不持有 context。T08 必须持有同一快照、完成准备、最终 Build、校验候选、原子提交及身份重检；T07 提供同一候选核心的来源证明。没有绕过最终完整检查的公开参数。

## Provider 下载、内容与刷新合同

HTTP interval 缺省 3600 秒；显式值必须为 60..9223372036 秒。原生 0/负值不启动自动循环，与受管调度语义不同。非正或省略 `size-limit` 仍采用 16 MiB，正值取该值与 16 MiB 的较小者。T08 使用 30 秒超时、最多五次重定向、读取 cap+1 后明确拒绝超限；不能继承原生固定 20 秒与静默前缀截断。每次刷新必须把当前根配置与新的完整 provider bytes 重新经过整图检查，不能只校验单个下载文件。

`header` 是真实 HTTP token 名到有序字符串列表的映射，允许 TAB/UTF-8，拒绝其它控制字符及 DEL，并拒绝规范化重名。`Host` 保留普通 Header 项语义，不能在受管下载时偷偷变为 `req.Host`；userinfo/Authorization/User-Agent 等标准请求处理也不能被宣称永远原样写出。file/inline 的 URL、interval、header、下载 proxy 等原生不消费字段严格解码后省略，不伪装生效。HTTP 非空自定义 proxy 与订阅 direct/proxy/auto 合同冲突而拒绝。非空 age-secret-key 不是已批准的输入编码，拒绝；不新增解密下载能力。

health-check 独立于下载 interval。对象若提供则 enable 必须明确；enable 且 URL 精确非空时，interval0 交原生默认 300 秒，有效正秒需可表示。inactive interval 可保留负值。timeout0 交原生 5000ms，所有分支非零值必须在 ±9223372036854ms；负值的原生立即取消语义保留。group/manual 检查仍可能消费 disabled health-check 的 timeout，因此不能省略其数值检查或套用下载 30 秒上限。

proxy payload 使用闭合 YAML 文档和统一节点 schema。rule payload 支持 domain、ipcidr、classical 的 YAML/text；MRS 不属于本合同可安全解码格式。完整文档拒绝未知/重复键以及同时出现 payload/rules，即使一方为空；不会沿原生逐行吞错保留未经验证内容。输出 YAML 恰有一个 payload 头，每项完整 quote/escape 为一物理行，使固定核心逐行消费者不会丢失内嵌换行、引号、注释字符或长 regex。file/HTTP 非空 path-in-bundle 即使已有有效缓存也拒绝，因为存在未受管归档读取/缓存写入 fallback；inline 中该字段为 typed no-op 并省略。

12 个 simple override 只覆盖各协议实际声明的字段，未声明字段是 no-op；未知原节点键仍然拒绝。能力校验针对有效覆盖值，全部节点包括过滤后不会使用的节点都要经过此检查。fresh provider 内容保留原始名称、源列表顺序和重复项，让固定 file parser 执行原始过滤、去重与多 filter 名称变换顺序；不在 policy 中假执行 regexp2。simple 字段已物化后从最终 override 删除，只保留 `proxy-name`、`additional-prefix`、`additional-suffix`。

## 代理与规则对象图

内置名称仅 DIRECT、REJECT、REJECT-DROP、COMPATIBLE、PASS、PASS-RULE，GLOBAL 在原生构造末尾产生。root proxy GLOBAL 的旧对象、自动 GLOBAL selector 与已捕获成员用不同内部 identity 表示，最终名字 lookup 则遵原生覆盖顺序；不全面禁止合法 GLOBAL 名称。节点名字允许已证的空/NUL/Unicode 普通数据；Mieru 因其构造器要求非空单独拒绝空名。group 名必须非空但允许 NUL。重复和引用使用精确字符串，错误仅输出安全占位符，任何名字都不能变为资源路径。

保留原生 raw group DAG 检查，并构建有效节点 dialer/group 成员联合图。include-all-providers 替换 use，include-all-proxies 追加显式 proxies；default-selected 只是选择偏好，不要求存在。empty-fallback 总先验证为非 group 节点，即使不会触发。direct/dns/reject/rematch 的原始 dialer 字段没有实际 dialer 边；group 自身 dialer/interface/mark 是已知无效字段。rematch name 是标签，缺失 target-sub-rule 保留原生 fallback，不伪装 mandatory 引用。active NTP 非空 dialer 必须在最终 namespace；disabled/非正 interval 不产生边。

联合图拒绝已证明可实现的循环，包括可选成员循环和 provider 成员到使用组的 dialer 回边。exclude-type 的确定移除和过滤后 fallback 重加入分别处理。未知 regexp2 结果不能冒充必然可选边；未知前置成员可能遮蔽后置同名 selector 项，该遮蔽信息必须保留。单个本地 Compatible provider 的 positive filter 不生效，不因 backtick 文本误归 unknown。任意 regexp2、多 provider 排序和名称变换仍有静态 unknown 边界：本策略不能宣称任意过滤配置都不存在运行时递归。可信核心验证也不是该边界的通用证明。

## Geo、DNS 与资源预算补充

`geodata-mode` 缺省明确输出 false，不继承核心前次 reload 全局状态；显式 true 保留。Inspect/Build 使用相同有效值和完整引用闭包。固定 Geo kind/ID/path 为：

| kind | ID | core-home 文件 |
| --- | --- | --- |
| country-mmdb | mihari.geo/country-mmdb/v1 | Country.mmdb |
| asn-mmdb | mihari.geo/asn-mmdb/v1 | ASN.mmdb |
| geoip-dat | mihari.geo/geoip-dat/v1 | GeoIP.dat |
| geosite-dat | mihari.geo/geosite-dat/v1 | GeoSite.dat |

Geo 独立于 provider 预算：每资产最多 128 MiB，四个唯一 kind 累计最多 512 MiB，checked 累加，不提前分配总量。provider 仍保持每源 16 MiB、总 256 MiB、最多 256 项；输入与生成资源都需限界，不能借转换放大逃过检查。额外保留 Geo ID 也须真实验证，未知 kind/ID 拒绝。SHA256 来自实际输出 bytes，只是摘要，不构成来源认证。

DNS fragment 裸 selector 保留已解码精确字符串，包括引用已注册 NUL 名字的情况；未命中 proxy 时保留原生 interface fallback，不强加不存在的必须引用规则。`ts`/`tailscale` 则依实际 resolver 能力检查。WireGuard、MASQUE、OpenVPN remote-dns-resolve 且 dns 非空时，对所有 root/provider 节点做该能力检查；注入 adapter 后普通裸 selector 不制造额外 proxy 边。固定 OS resolver/hosts/CA 读取获准，任意 DHCP interface listener、文件路径或调用者环境选择没有因此获准。

## 认证编码与证据阅读方式

SOCKS5 的 [认证序列化](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/transport/socks5/socks5.go#L241) 把字符串长度直接写为 uint8。仅 username 非空的活跃分支要求两项各 ≤255 个 UTF-8 字节；空 username 时 password 保持休眠数据。Gost 的 [原生请求检查](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/transport/gost/relay.go#L231) 已拒绝两项 >255，策略提前一致拒绝。没有另加字符限制或非空密码要求。HTTP Basic 动态 base64 不套 255 限制。这是输入表示完整性与提前错误，不是资源预算或文件能力发现。

机器可读证据在 `internal/subscription/testdata/rootpolicy/registry.json`：declaration_index 对账固定 1,039 条声明，fields 对应明确绑定；compiled_shapes 独立列出共享数组元素、动态键和容器形状，不冒充独立上游字段。fixture_catalog 引用已存在的具名测试与实际表值。精确 fresh-value、仅 presence、复合分支/错误 parity、父能力拒绝及共享解码机制分别标明；含某字段的组合输入不能升级成该字段所有运行分支已执行。Go 编译 schema 是权威注册表，此 JSON 不能增加支持字段。

共享机制只证明其实际范围。例如 Gost forward/mux 的声明与消费者分别是目标地址选择和固定 smux 客户端，bool 类型通过同一 decoder；不能把共享 bool fixture 称为实际运行 Gost 链路。所有协议的构造 baseline 只验证真正供给的字段及 fresh 输出，不执行核心构造或握手。明确的兼容成本包括严格 YAML tags/null、空 netip 文本不自动变零值、uint32 routing-mark 域、规范化碰撞拒绝、有限 protobuf 未知字段拒绝，以及原生忽略垃圾的若干 grammar 规范化。

表述为表示检查的数值界不保证物理内存可分配、eager core -t 构造成本、peer 反馈算术或全运行时鲁棒性。已知 Hysteria2 UDP 重试、低容量 fragment count、MKCP congestion wrap、Mekya/XHTTP eager 分配、WireGuard workers/AWG junk 批量分配、复合规则重复递归等限制保留在对应段落。测试不访问公网、不运行真实 mihomo；后续受限核心校验和部署事务不能被本策略单元测试替代。

MASQUE [active resolver](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/adapter/outbound/masque.go#L252) 与 OpenVPN [resolver initialization](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/adapter/outbound/openvpn.go#L350) 均纳入同一 Build/Inspect 能力闭包，包括被 provider filter 排除的已声明节点。普通 UDP/system 与注入 adapter 下的裸 selector 保持原语义；此校验不执行隧道或 DNS 请求。

## 受管资源准备与 provider WAL（T08 内部检查点）

`NewResourcePreparer` 依次执行 Inspect、受管 provider 下载/授权私有对象复用、包含 provider 内容的 Geo 闭包检查、可信 Geo 内容认证和最终 Build。源 bytes 和最终资源分别进入随机私有 staging；输出的 provider 身份沿用 T06。候选 `SourceResourceID` 只能命中同一 store 从 daemon 当前合法配置构造的封闭 `ResourceGraph`，不能凭 ID 语法或任意 bytes map 取得授权。`PreparedResources.Recheck` 复查来源、旧目标及候选 inode/hash；Manager 仍须在 mutation 内复查订阅身份、generation 与 configGeneration。

Geo 来源由编译目录固定：Country/ASN 保留 release-inputs.lock 的 Loyalsoldier commit `69986b5d098c8d723a2c4d56317bc10cd5669c02` 与原 SHA256；GeoIP/GeoSite DAT 使用 MetaCubeX/meta-rules-dat commit `b3a0635a5ff10e63d300a050aa38edf7f138ef2f`，精确大小分别 17,120,329 / 4,242,906 bytes，SHA256 分别 `4149e607530f91da697bad4696f8c59f0a475af38e69405e4124438c9886c721` / `7104fc19469298564947d42c320a1d5442416f1f72648bf516314d719594338c`。DAT snapshot 可能失去上游可用性；新下载失败须保留旧部署，不能回退 mutable latest。固定摘要是所选数据身份，不能表述为上游构建证明；T06 的结构和 selector 校验仍执行。

Unix `NewProviderStore` 只借用 root0700 的 TrustedRoot；每次操作重新检查根、持有目录和文件身份，以0600私有文件、同步和身份绑定的原子替换实现 IO。`mihari.provider-commit/v1` 固定在 `staging/providers/commit.json`，包含完整 provider 身份、旧/新/备份对象摘要与 boot 身份、私有事务 marker、prepared/intent/done 和恢复 intent/done。prepared 先于备份，intent 先于替换，done 晚于成功 reload 与新文件复查；未完成提交恢复旧资源，durable done 保留新资源，未知身份保留 journal 并失败关闭。相同 boot 要求 inode 身份一致；跨 boot 仅在重新验证私有根/事务 marker 后按精确摘要解释。启动恢复最后清理拥有 marker 的私有候选，未知对象不被猜测删除。

此检查点提供资源准备、单 provider 事务和恢复基础，尚未接入默认运行入口。固定 H 的 Geo/多资源交换、停止核心后的批量 WAL/激活与完整回滚、Manager 入口/调度/退出以及离线启动装配仍由后续 T08 检查点完成；不得逐个提交 Geo 文件代替该激活事务。Store 恢复必须在任何 worker、核心恢复或执行之前完成；活跃事务内部使用自己的恢复路径，不能调用会清扫其他候选的启动 Recover。root 原生构造与完整 store IO 正例只在显式隔离 CI、可信 TMPDIR 中运行，普通平台测试或交叉编译不能替代这一验收。
