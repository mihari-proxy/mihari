# Platform install and uninstall e2e Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在空的 GitHub-hosted runner 上，用本次检出构建的同一份程序走完离线安装、保留目录的卸载、再安装、以及 `--purge --yes`。

**Architecture:** 产品安装器不改。Unix 在调用现有 `scripts/install/install-aio.sh` 之前，由作业以 root 写下 `install-trust/manifest.json`，钉住这次整合包和这次程序。Windows 调用现有 `scripts/install/install-aio.ps1`，它不读这份清单。可在普通 CI 里跑的只有夹具打包、清单 JSON 和结果判定。真正注册服务的脚本只由新 workflow 调用，不进入 `go test ./...`。

**Tech Stack:** Go `cmd/mihari`，`CGO_ENABLED=0`，Python 3 标准库，GitHub Actions，现有安装脚本。

**Spec:** 本文件第 1 节，对应 [issue #317](https://github.com/mihari-proxy/mihari/issues/317) 在 2026-10-09 的正文。没有第二份设计文档。

## Global Constraints

- 工作区是 `.worktrees/feat-install-uninstall-e2e`，分支 `feat/install-uninstall-e2e`，基线 `origin/dev` `a80ff34650c4a1b2b6f3322c47b9275423b03eeb`。主检出留在 `dev`，不在那里提交。PR 基线是 `dev`。
- 指向 `dev` 的 PR 不改 `CHANGELOG.md`。
- 不改 `/v1` 协议、持久化格式、CLI flag、编译进程序的 `internal/app/install_trust.json`，也不改 README 的平台支持声明。
- 不改 `scripts/install/install-aio.sh`、`scripts/install/install-aio.ps1` 和 `scripts/install/root-apply.sh.in`。信任这次构建的方式是作业写下的清单，不是安装器里的新旁路。
- 版本字符串和 `MIHARI_VERSION` 都是 `v0.0.0-dev.0`。通道是 `dev`。两次安装用同一份程序字节。
- 三个平台都设置 `MIHARI_YES=1`。
- 不执行单独的 `mihari service reinstall`。不检查 #282 的更新残留。
- 不下载 mihomo、GeoIP、通道索引或 GitHub Release。Windows 整合包里的核心和 GeoIP 是本地占位文件，内容不作为通过条件。
- 发布构建保持 `CGO_ENABLED=0`。
- 新 workflow 出现在 pull request 上，不加入分支保护的必需检查。
- 默认 `go test ./...` 不编译、不执行这次服务安装。

---

## 1. 需求

这是一次**离线安装**。空 runner 上的问题只是：安装器能否做完。

程序在 runner 上从本次检出构建。ldflags 写入 `github.com/mihari-proxy/mihari/internal/buildinfo.Version=v0.0.0-dev.0`。同一标签换同一标签是 `ReplacementNone`，第二次安装可以继续。

Unix 每次安装前，以 root 写 `/usr/local/lib/mihari/install-trust/manifest.json`。顶层只有 `binaries` 和 `bundles`，各含一个 64 位小写十六进制摘要。正式用户的信任根不变。Windows 不写这个文件。

同一作业、同一份程序，按这个顺序做四步：

1. 离线安装。服务已登记并且在运行，进程是这份程序。PATH 命令指向它。成功输出保持现有英文。
2. `mihari service uninstall`。服务登记消失。程序目录和数据目录还在。
3. 再离线安装一次。
4. `mihari service uninstall --purge --yes`。服务消失。数据根、程序目录消失。字节与安装根程序一致的 PATH 命令文件被删除。

| 平台 | Runner | 服务 | 服务里的程序 | 数据 | PATH 命令 |
| --- | --- | --- | --- | --- | --- |
| Linux | `ubuntu-latest` | systemd `mihari.service` | `/usr/local/lib/mihari` | `/var/lib/mihari/data` | `/usr/local/bin/mihari` |
| macOS | `macos-latest` | `/Library/LaunchDaemons/mihari.plist` | `/usr/local/lib/mihari` | `/Library/Application Support/mihari/data` | `/usr/local/bin/mihari` |
| Windows | `windows-latest` | SCM `mihari` | `C:\Program Files\Mihari` | `%USERPROFILE%\.mihari` | `%LOCALAPPDATA%\Programs\mihari\mihari.exe` |

`--purge --yes` 在 Unix 上还删除数据目录所在的根：Linux `/var/lib/mihari`，macOS `/Library/Application Support/mihari`。

成功英文：

- Unix：`Installation complete.`，以及 `Run mihari to open the TUI.`
- macOS 在安装前向 stderr 打印 `macOS is currently unsupported. Support is incomplete and use is not recommended.`
- Windows：`All-in-one installation completed. Restart your terminal, then run mihari to get started.`
- 保留目录的卸载：`service uninstall ok`
- 完全卸载：`Mihari has been completely uninstalled`

失败、取消和超时之后，`always()` 清理必须卸掉本次服务并删掉本次写出的根。该步失败则 job 失败。已经不存在的服务和目录算清理成功。

linux/arm64、windows/arm64、darwin/amd64 仍只做现有无 CGO 交叉编译。

## 2. 文件

- Create: `scripts/test/platform_install_e2e.py` — 打包、清单、命令和结果判定。`run` 才会调用安装器。
- Create: `scripts/test/test_platform_install_e2e.py` — 不提权、不注册服务。
- Create: `.github/workflows/platform-install-e2e.yml` — 三个 runner 上调用 `run`，并用 `always()` 调用 `cleanup`。
- Modify: `.github/workflows/ci.yml` — 在现有 unit job 里增加一条 pytest。
- Create: `docs/platform-install-e2e.md` — 前置条件、入口和回滚。

Unix 归档成员只允许这些名字，且 `mihari` 必须在归档根上。多一个成员，`install_release_inputs_unix.go` 会返回 `unsupported install bundle entry`。不要放入 `data/bin/core-channel`。

```text
mihari
data/bin/mihomo
data/geoip/GeoLite2-Country.mmdb
data/geoip/GeoLite2-ASN.mmdb
```

Windows 目录是 `mihari.exe`、`data\bin\mihomo.exe` 和同样的两份 `.mmdb`。

## 3. 任务

### Task 1: 打包本地整合包

**Files:**

- Create: `scripts/test/platform_install_e2e.py`
- Test: `scripts/test/test_platform_install_e2e.py`

**Interfaces:**

- Consumes: 无。
- Produces: `VERSION = "v0.0.0-dev.0"`，`CHANNEL = "dev"`，`LDFLAG = "-X github.com/mihari-proxy/mihari/internal/buildinfo.Version=v0.0.0-dev.0"`。`pack_unix_bundle(mihari: bytes, core: bytes, country: bytes, asn: bytes) -> bytes`。`unix_members(blob: bytes) -> list[str]`。`write_windows_bundle(root: pathlib.Path, mihari: bytes, core: bytes, country: bytes, asn: bytes) -> None`。

- [x] **Step 1: 写失败测试**

测试文件开头先把本目录放进 `sys.path`，再按文件名导入。仓库里 `scripts/test` 不是包。

```python
import io
import sys
import tarfile
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import platform_install_e2e as e2e


def test_unix_bundle_has_only_the_installer_members():
    blob = e2e.pack_unix_bundle(b"mihari-bytes", b"core", b"country", b"asn")
    assert e2e.unix_members(blob) == [
        "mihari",
        "data/bin/mihomo",
        "data/geoip/GeoLite2-Country.mmdb",
        "data/geoip/GeoLite2-ASN.mmdb",
    ]
    with tarfile.open(fileobj=io.BytesIO(blob), mode="r:gz") as archive:
        mihari = archive.extractfile("mihari").read()
    assert mihari == b"mihari-bytes"


def test_windows_bundle_uses_exe_names(tmp_path: Path):
    e2e.write_windows_bundle(tmp_path, b"exe", b"core", b"country", b"asn")
    assert (tmp_path / "mihari.exe").read_bytes() == b"exe"
    assert (tmp_path / "data" / "bin" / "mihomo.exe").read_bytes() == b"core"
    assert (tmp_path / "data" / "geoip" / "GeoLite2-Country.mmdb").read_bytes() == b"country"
    assert (tmp_path / "data" / "geoip" / "GeoLite2-ASN.mmdb").read_bytes() == b"asn"
```

- [x] **Step 2: 确认失败**

Run: `python -m pytest scripts/test/test_platform_install_e2e.py::test_unix_bundle_has_only_the_installer_members -q`

Expected: FAIL，`ModuleNotFoundError` 或 `ImportError`。

- [x] **Step 3: 写打包函数**

`unix_members` 用 `tarfile.open(..., mode="r:gz")` 返回 `getnames()`。`pack_unix_bundle` 用 `tarfile.TarInfo`，`type` 为 `tarfile.REGTYPE`，不要前缀目录，不要符号链接。四个成员的内容就是四个参数。`write_windows_bundle` 创建上表里的四个相对路径并写入字节。

- [x] **Step 4: 确认通过**

Run: `python -m pytest scripts/test/test_platform_install_e2e.py -q`

Expected: PASS。

- [x] **Step 5: 提交**

```bash
git add scripts/test/platform_install_e2e.py scripts/test/test_platform_install_e2e.py
git commit -m "test: 锁定离线安装夹具的归档成员"
```

### Task 2: 清单只钉住这次构建

**Files:**

- Modify: `scripts/test/platform_install_e2e.py`
- Test: `scripts/test/test_platform_install_e2e.py`

**Interfaces:**

- Consumes: Task 1 的打包函数。
- Produces: `sha256_hex(payload: bytes) -> str`。`manifest_document(bundle_sha256: str, binary_sha256: str) -> dict`。`manifest_bytes(document: dict) -> bytes`，UTF-8，无 BOM。

- [x] **Step 1: 写失败测试**

```python
import hashlib
import json
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import platform_install_e2e as e2e


def test_manifest_pins_bundle_and_binary_only():
    mihari = b"mihari-bytes"
    blob = e2e.pack_unix_bundle(mihari, b"core", b"country", b"asn")
    document = e2e.manifest_document(e2e.sha256_hex(blob), e2e.sha256_hex(mihari))
    assert document == {
        "binaries": [hashlib.sha256(mihari).hexdigest()],
        "bundles": [hashlib.sha256(blob).hexdigest()],
    }
    parsed = json.loads(e2e.manifest_bytes(document).decode("utf-8"))
    assert list(parsed) == ["binaries", "bundles"]
    assert len(parsed["binaries"][0]) == 64
```

- [x] **Step 2: 确认失败**

Run: `python -m pytest scripts/test/test_platform_install_e2e.py::test_manifest_pins_bundle_and_binary_only -q`

Expected: FAIL，`ImportError`。

- [x] **Step 3: 实现**

`sha256_hex` 返回 `hashlib.sha256(payload).hexdigest()`。`manifest_document` 返回上列字典，摘要必须匹配 `^[0-9a-f]{64}$`，否则 `ValueError`。`manifest_bytes` 使用 `json.dumps(..., indent=2)` 加换行，键顺序固定为 `binaries`、`bundles`。

- [x] **Step 4: 确认通过**

Run: `python -m pytest scripts/test/test_platform_install_e2e.py -q`

Expected: PASS。

- [x] **Step 5: 提交**

```bash
git add scripts/test/platform_install_e2e.py scripts/test/test_platform_install_e2e.py
git commit -m "test: 锁定作业内 install-trust 清单"
```

### Task 3: 结果判定

**Files:**

- Modify: `scripts/test/platform_install_e2e.py`
- Test: `scripts/test/test_platform_install_e2e.py`

**Interfaces:**

- Consumes: `VERSION`。
- Produces: `assert_install_success(system: str, stdout: str, stderr: str) -> None`。`assert_service_version(stdout: str) -> None`。`assert_kept(system: str, service_installed: bool, paths_present: dict[str, bool]) -> None`。`assert_purged(system: str, service_installed: bool, paths_present: dict[str, bool]) -> None`。失败抛 `SystemExit`，消息里带上失败的键。

`paths_present` 的键：

- Linux / macOS：`program`、`data`、`base`、`path_command`
- Windows：`program`、`data`、`path_command`

- [x] **Step 1: 写失败测试**

```python
import sys
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parent))
import platform_install_e2e as e2e


def test_unix_success_text_and_macos_warning():
    e2e.assert_install_success("linux", "Installation complete.\nRun mihari to open the TUI.\n", "")
    e2e.assert_install_success(
        "darwin",
        "Installation complete.\nRun mihari to open the TUI.\n",
        "warning: macOS is currently unsupported. Support is incomplete and use is not recommended.\n",
    )
    with pytest.raises(SystemExit):
        e2e.assert_install_success("darwin", "Installation complete.\nRun mihari to open the TUI.\n", "")


def test_version_json_must_match_the_fixed_tag():
    e2e.assert_service_version('{"schema":"mihari/v1","version":"v0.0.0-dev.0"}\n')
    with pytest.raises(SystemExit):
        e2e.assert_service_version('{"schema":"mihari/v1","version":"dev"}\n')


def test_keep_leaves_directories_and_purge_removes_them():
    present = {"program": True, "data": True, "base": True, "path_command": True}
    e2e.assert_kept("linux", False, present)
    e2e.assert_purged("linux", False, {key: False for key in present})
    with pytest.raises(SystemExit):
        e2e.assert_kept("linux", True, present)
```

- [x] **Step 2: 确认失败**

Run: `python -m pytest scripts/test/test_platform_install_e2e.py::test_keep_leaves_directories_and_purge_removes_them -q`

Expected: FAIL，`ImportError`。

- [x] **Step 3: 实现判定**

`assert_install_success`：`linux` 和 `darwin` 的 stdout 必须同时包含两句 Unix 成功英文。`darwin` 的 stderr 还必须包含 macOS 警告全文。`windows` 的 stdout 必须包含 Windows 成功英文。

`assert_service_version` 解析 JSON，要求 `schema == "mihari/v1"` 且 `version == VERSION`。

`assert_kept` 要求 `service_installed is False`，并且 `paths_present` 的每个值都是 `True`。`assert_purged` 要求服务未安装，并且每个值都是 `False`。

- [x] **Step 4: 确认通过**

Run: `python -m pytest scripts/test/test_platform_install_e2e.py -q`

Expected: PASS。

- [x] **Step 5: 提交**

```bash
git add scripts/test/platform_install_e2e.py scripts/test/test_platform_install_e2e.py
git commit -m "test: 锁定安装和卸载的通过条件"
```

### Task 4: 把四步接成一个命令

**Files:**

- Modify: `scripts/test/platform_install_e2e.py`
- Test: `scripts/test/test_platform_install_e2e.py`

**Interfaces:**

- Consumes: Task 1 到 Task 3 的函数。
- Produces: `build_command(go: str, output: str) -> list[str]`。`unix_install_command(script: str, archive: str) -> list[str]`。`windows_install_command(script: str, bundle_dir: str) -> list[str]`。`service_command(binary: str, purge: bool) -> list[str]`。`main(argv: list[str]) -> None`，子命令是 `run` 和 `cleanup`。

- [x] **Step 1: 写失败测试**

```python
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import platform_install_e2e as e2e


def test_commands_use_the_fixed_version_and_existing_installers():
    assert e2e.build_command("go", "mihari") == [
        "go", "build", "-trimpath", "-ldflags", e2e.LDFLAG, "-o", "mihari", "./cmd/mihari",
    ]
    assert e2e.unix_install_command("scripts/install/install-aio.sh", "bundle.tar.gz") == [
        "sudo", "/usr/bin/env",
        "MIHARI_VERSION=v0.0.0-dev.0",
        "MIHARI_CHANNEL=dev",
        "MIHARI_YES=1",
        "scripts/install/install-aio.sh",
        "--channel", "dev",
        "bundle.tar.gz",
    ]
    assert e2e.windows_install_command("scripts/install/install-aio.ps1", r"C:\bundle") == [
        "powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass",
        "-File", "scripts/install/install-aio.ps1",
        "-BundleDir", r"C:\bundle",
        "-Channel", "dev",
    ]
    assert e2e.service_command("/usr/local/bin/mihari", False) == [
        "sudo", "/usr/local/bin/mihari", "service", "uninstall",
    ]
    assert e2e.service_command("/usr/local/bin/mihari", True) == [
        "sudo", "/usr/local/bin/mihari", "service", "uninstall", "--purge", "--yes",
    ]
```

- [x] **Step 2: 确认失败**

Run: `python -m pytest scripts/test/test_platform_install_e2e.py::test_commands_use_the_fixed_version_and_existing_installers -q`

Expected: FAIL，`ImportError`。

- [x] **Step 3: 实现命令和 `run`**

`build_command` 返回测试里的那一列。调用前设置 `CGO_ENABLED=0`。

`unix_install_command` 返回测试里的那一列。`sudo /usr/bin/env` 把三个变量传给现有 `install-aio.sh`，后面是 `--channel dev` 和归档路径。不要改安装脚本。

`windows_install_command` 是 `powershell.exe -NoProfile -ExecutionPolicy Bypass -File <script> -BundleDir <dir> -Channel dev`。调用前设置 `MIHARI_YES=1`。

`service_command` 在 Unix 上前缀 `sudo`。`purge` 为假时参数是 `service uninstall`。为真时追加 `--purge --yes`。

`run` 的顺序固定为：

1. `go build` 出本次程序。
2. 打包。Unix 写 `.tar.gz`；Windows 写目录。
3. Unix：以 root 创建 `/usr/local/lib/mihari/install-trust`，写入清单，文件和新建目录的属主是 root，权限不含组和其他的写位。然后执行安装。安装前再写一次清单，第二次安装前也再写一次。
4. 断言安装成功英文、`self version --json`、服务在运行、进程路径是服务里的程序、PATH 命令的摘要等于服务里的程序。
5. `service uninstall`。断言服务不在，程序目录、数据目录、Unix 的 base、PATH 命令都还在。
6. 再安装一次，并重复第 4 步。
7. `service uninstall --purge --yes`。断言服务不在，这些路径都不在。

Windows 的 PATH 判定读用户环境里的 `Path` 是否包含 `%LOCALAPPDATA%\Programs\mihari`，并比较该目录下 `mihari.exe` 与 `C:\Program Files\Mihari\mihari.exe` 的摘要。当前进程的 `PATH` 不会在安装后自动刷新，不要用它作通过条件。

服务在运行的判定：

- Linux：`systemctl is-active mihari` 是 `active`。`/proc/<MainPID>/exe` 解析到 `/usr/local/lib/mihari/mihari`。
- macOS：`launchctl print system/mihari` 成功，并且其中的程序路径是 `/usr/local/lib/mihari/mihari`。
- Windows：`Get-Service mihari` 的状态是 `Running`，`Win32_Service` 的 `PathName` 包含 `C:\Program Files\Mihari\mihari.exe`。

`cleanup` 只做删除。服务还在就执行 `service uninstall --purge --yes`。然后删除这些还存在的路径：`/usr/local/lib/mihari`、`/var/lib/mihari`、`/usr/local/bin/mihari`、`/Library/LaunchDaemons/mihari.plist`、`/Library/Application Support/mihari`、`C:\Program Files\Mihari`、`%USERPROFILE%\.mihari`、`%LOCALAPPDATA%\Programs\mihari\mihari.exe`。目标已不存在则继续。卸载或删除返回非零则退出 1。

`python scripts/test/platform_install_e2e.py` 不带 `run` 时不得调用安装器。pytest 只导入模块。

- [x] **Step 4: 确认通过**

Run: `python -m pytest scripts/test/test_platform_install_e2e.py -q`

Expected: PASS。测试进程没有 `sudo`、`powershell.exe` 或 `go build`。

- [x] **Step 5: 提交**

```bash
git add scripts/test/platform_install_e2e.py scripts/test/test_platform_install_e2e.py
git commit -m "feat: 串起同一程序的安装和两次卸载"
```

### Task 5: 独立 workflow 和说明

**Files:**

- Create: `.github/workflows/platform-install-e2e.yml`
- Modify: `.github/workflows/ci.yml`
- Create: `docs/platform-install-e2e.md`

**Interfaces:**

- Consumes: `python scripts/test/platform_install_e2e.py run` 和 `cleanup`。
- Produces: workflow 名 `platform-install-e2e`。三份 job 的 `runs-on` 分别是 `ubuntu-latest`、`macos-latest`、`windows-latest`。

- [x] **Step 1: 写 workflow**

```yaml
name: platform-install-e2e

on:
  pull_request:
  workflow_dispatch:

permissions:
  contents: read

jobs:
  install:
    name: install uninstall (${{ matrix.os }})
    strategy:
      fail-fast: false
      matrix:
        os: [ubuntu-latest, macos-latest, windows-latest]
    runs-on: ${{ matrix.os }}
    timeout-minutes: 45
    steps:
      - uses: actions/checkout@v7
        with:
          persist-credentials: false
      - uses: actions/setup-go@v7
        with:
          go-version-file: go.mod
      - uses: actions/setup-python@v6
        with:
          python-version: "3.x"
      - name: Install, reinstall, and purge
        shell: bash
        env:
          CGO_ENABLED: "0"
          GOTOOLCHAIN: local
          GOENV: "off"
        run: python scripts/test/platform_install_e2e.py run
      - name: Remove this job's service and roots
        if: always()
        shell: bash
        run: python scripts/test/platform_install_e2e.py cleanup
```

`ci.yml` 的 unit job 在 `Test isolated security runner without privileges` 之后加一步：

```yaml
      - name: Test platform install fixture plan
        run: python -m pytest scripts/test/test_platform_install_e2e.py -q
```

- [x] **Step 2: 写说明**

`docs/platform-install-e2e.md` 写这四项，不写新的产品行为：

- 前置条件：空的 hosted runner，Unix 有免密 `sudo`，Windows 作业已经是管理员。版本固定为 `v0.0.0-dev.0`，通道 `dev`。
- 入口：workflow `platform-install-e2e`，本地判定是 `python -m pytest scripts/test/test_platform_install_e2e.py -q`。本地 pytest 不注册服务。
- 回滚：`python scripts/test/platform_install_e2e.py cleanup`。它卸掉 `mihari` 服务并删除第 1 节列出的程序目录、数据根和 PATH 命令。
- 不在分支保护里把这个 workflow 设成必需检查。

- [x] **Step 3: 跑本地判定**

Run: `python -m pytest scripts/test/test_platform_install_e2e.py -q`

Expected: PASS。

再确认默认测试不会因为新文件去注册服务：

Run: `go test -count=1 ./internal/buildinfo`

Expected: PASS。仓库里没有新的 Go 测试文件。

- [x] **Step 4: 提交**

```bash
git add .github/workflows/platform-install-e2e.yml .github/workflows/ci.yml docs/platform-install-e2e.md
git commit -m "ci: 增加全平台安装与卸载端到端作业"
```

## 4. 不在本计划里

- 不把 workflow 加进必需状态检查。
- 不跑 `mihari service reinstall`，除非 Windows 安装脚本自己在发现已有服务时调用。第一次安装前服务不存在。第一次卸载之后服务也不存在，所以第二次安装仍是 `service install`。
- 不断言 `*.old-*` 或孤儿进程。
- 不替换占位核心为官方 mihomo。安装器接受受信任整合包里的 `data/bin/mihomo` 字节。通过条件是 `mihari` 服务进程，不是 mihomo 进程。
- 不在开发机、Ubuntu 虚拟机或长期 self-hosted runner 上执行 `run`。

## 5. 完成前核对

- 第 1 节的四步、三张路径表、英文输出、`MIHARI_YES=1`、清单和「不测更新残留」都能指到 Task 3、Task 4 或 Task 5。
- `git diff` 不含 `CHANGELOG.md`、`internal/app/install_trust.json` 和 `scripts/install/install-aio.sh`。
- 推送后看 `platform-install-e2e` 的三个 job。pytest 绿不能代替这三个 job。
