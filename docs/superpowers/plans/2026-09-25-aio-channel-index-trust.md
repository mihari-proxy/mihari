# 整合包以通道索引完成空机器安装

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 空机器安装整合包时用包内的 Mihari 执行安装，信任根是固定通道索引的 sha256，安装过程不连接 GitHub。

**Architecture:** 通道索引的解析放在无构建标签的 Go 函数里，Unix 安装事务在带整合包且 install-trust 未覆盖时调用它。POSIX root 脚本先把整合包复制进 root 临时目录再核对；在线读固定索引，离线只读 install-trust。`install.sh` 仍走 GitHub。

**Tech Stack:** Go（`internal/app`）、POSIX sh（`scripts/install/root-apply.sh.in`）、pytest。

**Spec:** [docs/superpowers/specs/2026-09-25-aio-channel-index-trust-design.md](../specs/2026-09-25-aio-channel-index-trust-design.md)

## Global Constraints

- 不修改 `CHANGELOG.md`，不新增 install-request 字段，不改 Windows 脚本和 `scripts/release/release-alist.py`。
- 固定索引只允许这两个 HTTPS 字面量：稳定 `https://cloud.xn--30q18ry71c.com/p/public/mihari-release/mihari/index.txt`，dev `https://cloud.xn--30q18ry71c.com/p/public/mihari-release/mihari-dev/index.txt`。
- 索引正文上限 65536 字节。`latest` 必须等于请求 tag。平台行 sha256 为 64 位小写十六进制。
- root 先复制整合包到临时目录，再对该副本做摘要和抽文件。不执行用户目录里的路径。
- 不在 `main` 或 `dev` 上提交。本计划的步骤不包含 commit；由用户另行要求。
- 工作区：`.worktrees/dev-20260925`，分支 `work/dev-20260925`。

---

### Task 1: 通道索引解析

**Files:**
- Create: `internal/app/channel_index.go`
- Test: `internal/app/channel_index_test.go`

**Interfaces:**
- Consumes: 无。
- Produces: `func channelIndexURL(channel string) (string, error)`；`func parseChannelIndex(text, channel, goos, goarch string) (latest, sum string, err error)`。`channel` 只接受 `main` 与 `dev`。重复键、缺 `latest`、缺本平台行、非规范 tag、非 64 位小写 sha256 都返回错误。

- [ ] **Step 1: 写失败测试**

覆盖：`main`/`dev` 的 URL；一份含 `latest v1.2.3` 与 `linux-amd64 https://example.invalid/a <64hex>` 的索引能解析出 tag 和摘要；dev tag `v1.2.3-dev.4` 仅在 `dev` 通道接受；重复 `latest`、错误平台摘要、超过 65536 字节的正文失败。

- [ ] **Step 2: 运行**

`go test -count=1 -run TestChannelIndex ./internal/app`

期望：编译失败或断言失败，因为函数尚不存在。

- [ ] **Step 3: 实现解析**

只做纯函数。不在这一步发网络请求。行格式与 `install-aio-remote.sh` 的 index 解析一致：`<key> <rest>`，忽略空行和以 `#`、`//` 开头的行。

- [ ] **Step 4: 再跑 Step 2，期望通过**

### Task 2: 整合包不再向 GitHub 要摘要

**Files:**
- Modify: `internal/app/install_release_inputs_unix.go`（`prepareNativeReleaseInputs` 中 bundle 分支，约 67–82 行）
- Modify: `internal/app/channel_index.go`（增加带 `*http.Client` 的获取函数）
- Test: `internal/app/install_core_policy_unix_test.go`
- Test: `internal/app/channel_index_test.go`（获取函数若放在无标签文件，则测试也无 unix 标签）

**Interfaces:**
- Consumes: Task 1 的 `channelIndexURL` 与 `parseChannelIndex`。
- Produces: `func fetchChannelIndex(ctx context.Context, client *http.Client, channel, goos, goarch string) (latest, sum string, err error)`。只请求 `channelIndexURL` 的结果，限制 65536 字节，非 200 即失败。

- [ ] **Step 1: 写失败测试**

在 `install_core_policy_unix_test.go` 增加 `TestNativeCoreInputs_BundleUsesChannelIndexNotGitHub`：不写 install-trust 摘要，HTTP transport 对 `github.com` 调用 `t.Error`，对 dev 索引 URL 返回

```text
latest v1.2.3-dev.1
linux-amd64 https://example.invalid/bundle <归档的 sha256>
```

请求带该归档、其中 `mihari` 与 `Binary` 文件字节相同、`ReleaseTag` 为 `v1.2.3-dev.1`、`Channel` 为 `dev`。期望 `prepareNativeReleaseInputs` 成功，且没有 GitHub 请求。

再增加摘要不符、`latest` 与 `ReleaseTag` 不符两个子测试，期望失败且错误不是成功。

保留已有 `TestNativeCoreInputs_AcceptsBundledVersionWithoutAdditionalDownload`：install-trust 已钉住摘要时 transport 仍须零请求。

- [ ] **Step 2: 运行**

`go test -count=1 -run 'TestNativeCoreInputs_' ./internal/app`

此测试文件有 `linux || darwin` 标签。Windows 上会显示没有匹配测试；以 Linux 结果为准。解析测试在 Windows 上可以先跑 Task 1。

- [ ] **Step 3: 改 bundle 授权**

`req.Bundle != ""` 且 `acceptsBundle` 为假时调用 `fetchChannelIndex`，不调用 `official.Checksum`。索引 `latest` 必须等于 `req.ReleaseTag`，摘要必须等于归档字节的 sha256。通过后把该摘要写入 `inputs.trust.bundle`。归档内 `mihari` 与候选文件摘要不一致时保持现有 `bundle binary does not match verified candidate`。

没有 `req.Bundle` 时，二进制摘要仍走 `update.OfficialReleaseSource.Checksum`。

- [ ] **Step 4: 再跑 Step 2。Linux 上两条测试都通过**

### Task 3: root 脚本区分整合包与 GitHub 单文件

**Files:**
- Modify: `scripts/install/root-apply.sh.in`（`if [ -z "$entry" ]` 之前，约 96 行）
- Modify: 由 `python scripts/install/generate_root_apply.py` 再生 `install.sh`、`install-aio.sh`、`install-aio-remote.sh`
- Test: `scripts/install/test_aio_channel_index.py`

**Interfaces:**
- Consumes: 位置参数里已有的 `tag`、`channel`、`bundle`、`bootstrap_mode`、`install_root`。
- Produces: bundle 非空且还没有合格的已安装程序时，`entry` 与 `candidate` 都指向 root 临时目录中的 `mihari`，`bundle` 指向同一目录中的归档副本。bundle 为空时，原 GitHub helper 块保持原样。

- [ ] **Step 1: 写失败测试**

新 pytest 在 POSIX 上用假 `curl` 驱动从 `root-apply.sh.in` 抽出的在线分支（Windows 上 `pytest.skip`，与 `test_replacement_confirmation.py` 相同）：

- `channel=dev`、`bootstrap_mode=online`、本地归档的 sha256 与假索引一致、抽出的 `mihari` 是一个 `service apply --help` 印出 `--yes` 和 `--expected-preview` 的脚本。期望退出码 0，且 curl 记录里没有 `api.github.com` 或 `github.com`。
- 索引 sha256 与归档不符时期望失败，stderr 不含 `helper release metadata exceeds limit`，也不出现第二份下载。
- `bootstrap_mode=offline` 且没有 manifest 时失败，curl 不被调用。
- `bootstrap_mode=offline`，把归档 sha256 写入一个通过 root 所有权检查的 `install-trust/manifest.json` 的 `bundles` 数组时成功，curl 不被调用。所有权检查在非 root 的测试里把“路径属主”换成可注入的判定有困难；测试改为提取摘要匹配函数，用固定 manifest 文本证明只接受 `binaries`/`bundles` 里的 64 位小写摘要，超 1 MiB 的 manifest 被拒绝。真正的路径属主沿用 `trusted_entry` 的同一循环，不在单测里冒充 root。
- 读取生成后的 `install.sh`，断言 `per_page=100` 仍在、且位于 bundle 为空的分支之后。读取 `install-aio-remote.sh`，断言 dev 固定索引 URL 存在，且 bundle 非空时不会先请求 `releases?per_page=100`。

- [ ] **Step 2: 运行**

`python -m pytest scripts/install/test_aio_channel_index.py -q`

期望失败。

- [ ] **Step 3: 改模板并再生**

在 `if [ -z "$entry" ]` 的 GitHub 块之前插入 bundle 分支。在线分支的索引 URL 用上面两个字面量，按 `channel` 选择。`root_fetch` 之后检查 `wc -c` 不超过 65536。`latest` 与 `$tag` 不同则失败。离线分支只读 `${install_root:-/usr/local/lib/mihari}/install-trust/manifest.json`，上限 `1048576`，并且该文件到 `/` 的路径链满足与 `trusted_entry` 相同的 root、非符号链接、不可被其他用户写入。匹配 `bundles` 中的归档摘要或 `binaries` 中的 `mihari` 摘要即可。缺少确认参数时失败，文案使用现有 “lacks replacement confirmation support”，不要再设置 `helper_url`。

然后运行 `python scripts/install/generate_root_apply.py`。不要手改三个生成结果。

- [ ] **Step 4: 再跑 Step 2，并跑**

`python -m pytest scripts/install/test_root_apply_generated.py scripts/install/test_aio_channel_index.py -q`

### Task 4: 发行说明

**Files:**
- Modify: `docs/distribution.md`（约第 99 行，“Unix 安装 helper” 段）

- [ ] 把该段改成与 spec 一致：已安装的合格程序优先；否则在线整合包只认固定通道索引并执行包内程序；离线不联网，只认 install-trust 或已安装程序。写明 `install.sh` 仍使用 GitHub，公开一键脚本要等 stable 发布才更新。

- [ ] `python -m pytest scripts/release/tests/test_release_workflow.py -q -k test_release_documents_record_verified_dev_github_release_and_unavailable_dev_alist`

该测试锁定公开索引和 `install-aio-remote.sh` 的 URL 仍在 `docs/distribution.md`，不锁定 helper 段落的原文。改写时保留这些 URL。

### Task 5: 验证

- [ ] `gofmt -l internal/app/channel_index.go internal/app/channel_index_test.go internal/app/install_release_inputs_unix.go`
- [ ] `go test -count=1 -run 'TestChannelIndex|TestNativeCoreInputs_' ./internal/app`
- [ ] `python -m pytest scripts/install/test_aio_channel_index.py scripts/install/test_root_apply_generated.py -q`
- [ ] Linux 上补跑 `go test -count=1 ./internal/app` 中带 unix 标签的安装测试。Windows 上说明这些标签测试未在本机执行。

不把本次改动写入 `CHANGELOG.md`。不推送，不打开 PR，除非用户要求。
