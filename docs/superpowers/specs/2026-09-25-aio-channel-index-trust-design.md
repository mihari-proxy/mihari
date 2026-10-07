# 整合包以通道索引完成空机器安装

日期：2026-09-25。决定见 [ADR 0007](../../adr/0007-aio-channel-index-trust.md)。词汇见仓库根目录 `CONTEXT.md`。

## 问题

`install-aio-remote.sh --channel dev` 在空机器上会在整合包下载完成之后失败，错误是 `helper release metadata exceeds limit`。root 脚本不使用包内的 `mihari`，而是请求 `https://api.github.com/repos/mihari-proxy/mihari/releases?per_page=100`。该响应当前约 1.93 MiB，脚本上限是 1 MiB。

包内的 `mihari` 与官方单文件是同一份字节。空机器安装当前通道的整合包不需要第二份程序，也不需要 GitHub。

## 行为

**远程安装**（`install-aio-remote.sh`，`bootstrap_mode=online`，带整合包）：

1. 本机 `/usr/local/lib/mihari/mihari` 若通过既有 root 路径链，且 `service apply --help` 同时含 `--yes` 和 `--expected-preview`，则它是安装器。选择它不联网。整合包未被 install-trust 的 `bundles` 或 `binaries` 接受时，随后的安装事务仍请求固定通道索引。
2. 否则 root 只请求该通道的固定通道索引：
   - `main`：`https://cloud.xn--30q18ry71c.com/p/public/mihari-release/mihari/index.txt`
   - `dev`：`https://cloud.xn--30q18ry71c.com/p/public/mihari-release/mihari-dev/index.txt`
3. root 先把整合包复制进自己的临时目录，再计算副本的 sha256。索引的 `latest` 必须等于本次 tag，本平台行的 sha256 必须等于该副本。然后从该副本抽出 `mihari`。帮助输出含上述两个参数时，这份副本既是安装器也是候选。
4. 摘要不一致、索引不可用、或包内程序缺少确认参数：拒绝。不回退到 GitHub release 列表，不下载第二份二进制。
5. 随后的安装事务对这份整合包使用同一通道索引。不再下载 GitHub `SHA256SUMS.txt`。

**离线安装**（`install-aio.sh`，默认 `bootstrap_mode=offline`）：

没有合格安装器时不拨号，只接受安装根下 `install-trust/manifest.json` 的 `binaries` 或 `bundles` 中已经钉住的 sha256；钉住的是整合包或其中的 `mihari` 均可。核对的是 root 临时目录里的副本。通过且程序具备确认参数后，由包内程序执行安装。已有合格安装器时由它执行；整合包未被这些摘要接受时，安装事务仍会请求通道索引。否则拒绝，沿用现有“需要事先准备可信安装器”的失败。

**不在本次范围：**

- `install.sh` 继续从 GitHub 解析并下载单文件。它在空机器上安装 dev 时仍可能因 1 MiB 的 release 列表上限失败。
- Windows 安装脚本、dev/stable 的 AList 发布流程不改。公开的 `install-aio-remote.sh` 仍只在下一次 stable 发布时上传。
- 不修改 `mihari.install-request/v1`。调用方传入的 `bundle_sha256`、`MIHARI_INDEX_URL` 和 `MIHARI_BUNDLE_URL` 都不能充当信任根。root 环境已被 `env -i` 清空，索引地址必须是脚本中的字面量。

## 安装事务

`prepareNativeReleaseInputs` 在 install-trust 已接受整合包摘要或候选 `mihari` 摘要时不联网。候选摘要被接受时，归档里名为 `mihari` 的成员仍须与候选字节一致；该钉一并授权同一归档中的 core 与 geo。两者都未接受且请求带有整合包时，改为拉取对应通道的固定索引并核对 tag 与摘要；禁止再请求 `github.com` 上的 `SHA256SUMS.txt`。没有整合包的请求仍走现有 GitHub 单文件校验。

离线脚本只在 install-trust 已接受摘要之后才执行包内程序，因此该次 Go 调用不会因为缺少摘要而去拉索引。
