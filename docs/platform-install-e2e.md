# 全平台安装与卸载端到端作业

这个作业只回答一件事：空的 GitHub-hosted runner 上，现有安装器能否装上本次检出构建的程序，再把它卸掉。它不改变产品的安装信任根，也不进入默认 `go test ./...`。

## 前置条件

- 使用空的 hosted runner。Unix 需要免密 `sudo`。Windows 作业用户已经是管理员。
- 程序版本固定为 `v0.0.0-dev.0`，通道是 `dev`。两次安装使用同一份程序。
- 三个平台都设置 `MIHARI_YES=1`。
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

## 分支保护

不要把 `platform-install-e2e` 加进分支保护的必需检查。三个 runner 的 job 才是安装是否做完的证据。pytest 通过不能代替它们。
