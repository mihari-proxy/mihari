# 全平台安装与卸载端到端作业

这个作业只回答一件事：空的 GitHub-hosted runner 上，现有安装器能否装上本次检出构建的程序，再把它卸掉。它不改变产品的安装信任根，也不进入默认 `go test ./...`。

## 前置条件

- 使用空的 hosted runner。Unix 需要免密 `sudo`。Windows 作业用户已经是管理员。
- 程序版本固定为 `v0.0.0-dev.0`，通道是 `dev`。两次安装使用同一份程序。
- GeoIP 使用仓库 `internal/app/testdata/migration-mmdb` 中已有的合成 MMDB（许可证与来源见该目录 README）；不下载真实地理数据。核心仍是占位文件，作业验证 Mihari 服务，不验证代理功能。
- Windows 的保留数据卸载从 PATH 命令执行；完整卸载从临时目录执行同一份构建程序，避免正在运行的映像锁住待删除文件。
- 三个平台都设置 `MIHARI_YES=1`。
- Unix 仅在 `GITHUB_ACTIONS=true` 且 `RUNNER_ENVIRONMENT=github-hosted` 时执行安装。作业打印默认 PATH 目录 `/usr/local/bin` 的原始权限，然后只将该目录本身设为 UID/GID 0、0755，满足安装器的信任要求；不递归修改其中的工具，不改变 `/usr`、`/usr/local`，不放宽产品信任校验。
- Linux 跑在 `ubuntu-latest`，服务是 systemd `mihari.service`。macOS 跑在 `macos-latest`，服务是 `/Library/LaunchDaemons/mihari.plist`。Windows 跑在 `windows-latest`，服务是 SCM `mihari`。

## 入口

- 真正注册服务的入口是 workflow `platform-install-e2e`。它在 pull request 和 `workflow_dispatch` 上运行。
- 本地判定是 `python -m pytest scripts/test/test_platform_install_e2e.py -q`。这条 pytest 不注册服务。
- 不要在开发机、Ubuntu 虚拟机或长期 self-hosted runner 上执行 `python scripts/test/platform_install_e2e.py run`。

## 回滚

失败、取消和超时之后，workflow 的 `always()` 步骤会执行：

```console
python scripts/test/platform_install_e2e.py cleanup
```

它在服务还在时执行 `service uninstall --purge --yes`，并删除本次写出的程序目录、数据根和 PATH 命令。目标已经不存在则继续。卸载或删除返回非零时，这个步骤失败。

Windows 清理先把已安装程序复制到临时目录，再从该副本执行完整卸载；等待进程退出后删除副本。不会忽略 PATH 命令删除失败或把部分卸载判定为成功。

## 分支保护

不要把 `platform-install-e2e` 加进分支保护的必需检查。三个 runner 的 job 才是安装是否做完的证据。pytest 通过不能代替它们。
