# Prerelease AList Index Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 把已有的 `--channel dev` 策略接到 `release-dev.yml` / 新的 `retract-dev.yml`，在 `/mihari-release/mihari-dev/index.txt` 写出独立 prerelease `latest`，且永不改写稳定 `index.txt` 或 GitHub `/releases/latest`。

**Architecture:** 脚本层先补 `_fs_path`-正确的通道根探测、topology-aware Fake、隔离守卫和 CI 测试列表（PR 1，此时文档仍写「P2 不可用」）。然后在**同一个** Actions/文档 PR（PR 2）里接线发布与撤回，避免 `dev` 上出现没有 retract 的 AList writer。

**Tech Stack:** Python 3.12、pytest、GitHub Actions YAML、现有 `scripts/release-alist.py` / `retract-alist.py` / `alist_client.py`。

---

## Global Constraints

- 只在 worktree 分支 `feat/126-prerelease-alist-index` 工作（跟踪 `origin/dev`）。禁止提交 `main` / `dev`。
- 规范：`docs/superpowers/specs/2026-08-24-prerelease-alist-index-design.md`（已 Approved）。YAML 步骤以该文档为准，不要即兴改顺序。GitHub delete 脚本例外：设计文档里是注释，必须从 `.github/workflows/retract.yml` 第 122–139 行复制真实 lookup + 404 分支（见 Task 10）。
- Issue：https://github.com/mihari-proxy/mihari/issues/126
- 已拍板：不回溯 `v0.9.0-dev.2/.3`；notes 追加 `MIHARI_INDEX_URL`；共用 `MIHARI_KEEP_VERSIONS`；`regenerate-index --channel` 进 PR 1；自动 mkdir `mihari-dev`。
- **生产行为变更**走 Red–Green–Refactor。Task 1–2 是表征钉（characterization pin），不是 TDD，不要写成 red-green。
- 只提交绿灯。禁止把已知失败的测试当成完成的 Task 提交。红灯测试写完、确认 FAIL 后停住，并入随后的绿灯 commit。
- 不打公网、不连真实 AList、不改 `/v1` / daemon / TUI / README 快速开始。
- 不上传 `install-aio-remote.*` 到 dev 根。不把 `mihari-stable-alist` 写进 dev workflow。不改默认安装器 URL。
- Conventional Commits，摘要中文。下面标了 commit message 的步骤才允许 `git commit`。
- Windows 上 pytest / go 命令在 worktree 根目录执行。

## File Structure

**PR 1（脚本 + CI 列表）**

- Modify: `scripts/alist_client.py` — `storage_root_entries`
- Modify: `scripts/release-alist.py` — `ensure_channel_root`，仅 `channel == "dev"` 在 `publish()` 开头调用
- Modify: `scripts/retract-alist.py` — 同一探测；有 `mihari`、无 `mihari-dev` → AList no-op
- Create: `scripts/alist_channel_guard.py`
- Modify: `scripts/regenerate-index.py` — `--channel` + help/notice；`regenerate_index(alist, base_path, channel="stable")`
- Create: `scripts/test_alist_topology_fake.py` — topology-aware Fake（**内部键一律 fs 路径**）
- Create: `scripts/test_alist_channel_guard.py`
- Modify: `scripts/test_alist_client.py`, `test_release_alist.py`, `test_retract_alist.py`, `test_regenerate_index.py`, `test_release_workflow.py`（只改 `RELEASE_SAFETY_TESTS`）
- Modify: `.github/workflows/ci.yml` — pytest 命令与 constant 同步

**PR 2（发布 + 撤回 + 文档一次翻转）**

- Modify: `.github/workflows/release-dev.yml`
- Create: `.github/workflows/retract-dev.yml`
- Modify: `scripts/test_release_workflow.py`（YAML 断言在写 YAML **之前**；文档断言留到 Task 11）
- Modify: `docs/RELEASE.md`, `docs/distribution.md`, `CHANGELOG.md`

---

### Task 1: 表征钉 `_fs_path("/mihari-release/mihari-dev")` 与 `"/"`

**性质：** characterization pin，不是 TDD。现实现已正确；本 Task 只加回归断言。不要为了「走一遍 red-green」去改生产代码。可单独绿灯提交，也可暂不 commit、并入 Task 4 的 `feat` commit。

**Files:**
- Modify: `scripts/test_alist_client.py` — `test_fs_path_strips_mount_segment`
- Modify: `scripts/alist_client.py` — **仅当断言失败才改**；预期现实现已正确

**Step 1: 先跑现有测试确认基线**

Run: `python -m pytest scripts/test_alist_client.py::test_fs_path_strips_mount_segment -q`

Expected: PASS

**Step 2: 追加表征断言**

在 `test_fs_path_strips_mount_segment` 末尾追加：

```python
    assert alist._fs_path("/mihari-release/mihari-dev") == "/mihari-dev"
    assert alist._fs_path("/mihari-release/mihari-dev/index.txt") == "/mihari-dev/index.txt"
    assert alist._fs_path("/") == "/"
```

保留现有 `assert alist._fs_path("/mihari-release") == "/mihari-release"`。

**Step 3: 再跑**

Run: `python -m pytest scripts/test_alist_client.py::test_fs_path_strips_mount_segment -q`

Expected: PASS（`sep <= 0` 时原样返回 `"/"`, 两段路径丢掉首段得 `/mihari-dev`）。若 FAIL，按 `sep <= 0: return path` / `"/" + rest[sep+1:]` 修，不要改成 list `/mihari-release`。

**Step 4: 可选绿灯 commit**（不要写成 red-green）

```bash
git add scripts/test_alist_client.py scripts/alist_client.py
git commit -m "test: 钉死 dev 通道 AList fs 路径映射"
```

---

### Task 2: 表征钉 `list_dir("/")` 的 fs JSON `path` 必须是 `"/"`

**性质：** characterization pin，不是 TDD。`list_dir` 已发送 `"path": self._fs_path(path)`，`LIST_PAGE_SIZE = 200`。可单独绿灯提交，也可并入 Task 4。

**Files:**
- Modify: `scripts/test_alist_client.py`
- Modify: `scripts/alist_client.py` — 仅当断言失败才改

**Step 1: 写表征测试**

追加：

```python
def test_list_dir_root_sends_slash_fs_path():
    alist = AList.__new__(AList)
    captured = {}

    class Session:
        def post(self, url, timeout=None, json=None):
            captured["url"] = url
            captured["json"] = json
            return list_response(
                content=[{"name": "mihari", "is_dir": True}],
                total=1,
                page=1,
                per_page=200,
                has_more=False,
                pages_total=1,
            )

    alist.base = "https://cloud.example.com"
    alist.session = Session()
    entries = alist.list_dir("/")
    assert captured["url"].endswith("/api/fs/list")
    assert captured["json"]["path"] == "/"
    assert entries == [{"name": "mihari", "is_dir": True}]
```

`list_response` 的 `per_page` 默认已是 200，与 `LIST_PAGE_SIZE` 对齐即可。

**Step 2: 跑测试**

Run: `python -m pytest scripts/test_alist_client.py::test_list_dir_root_sends_slash_fs_path -q`

Expected: PASS。FAIL 则修 `_fs_path`，禁止为了这个测试去 list `/mihari-release`。

**Step 3: 可选绿灯 commit**

```bash
git add scripts/test_alist_client.py
git commit -m "test: 校验存储根 list 使用 fs 路径斜杠"
```

---

### Task 3: Topology-aware Fake + 红灯 first-publish（不 commit）

**Files:**
- Create: `scripts/test_alist_topology_fake.py`
- Modify: `scripts/test_release_alist.py`

本 Task **写 Fake 与失败测试、确认 FAIL、停止。不要 `git commit`。** Fake、红灯测试与 `ensure_channel_root` 一并进入 Task 4 的绿灯 commit。

**Step 1: 实现 Fake（测试辅助，不是生产代码）**

约定（锁定，不要另写「逻辑路径存储」版本）：

1. 内部键是 **fs** 路径。起始 `dirs = {"/", "/mihari"}`。**不要**预置 `/mihari-release` 或 `/mihari-dev`。
2. `list_dir` / `mkdir` / `exists` / `content` / `upload` / `upload_text` 接收逻辑路径，用真实 `AList._fs_path` 转成 fs 再查表。
3. `mkdir` 的父目录检查在 fs 空间：`/mihari-dev` 的父是 `"/"`。禁止 `mkdir` 出 fs `"/"`。
4. 测试断言用 `alist.content(logical)`，**禁止** `logical in alist.files`（`files` 的键是 fs）。
5. 每次操作记录 `(op, logical, fs)`；测试断言 `fs != "/mihari-release"`。
6. `content()` 必须先 `self.exists(path)` 再读，与生产 `AList.content` 相同。`exists()` 通过 `list_dir(parent)` 实现，因此 `exists("/mihari-release/mihari-dev")` 会 list 逻辑父 `/mihari-release`（fs `/mihari-release`）——生产代码不得走这条路径。

`scripts/test_alist_topology_fake.py`：

```python
"""AList fake that honors AList._fs_path and raises on missing fs directories."""
from pathlib import Path

from alist_client import AList, AListError


class TopologyFake:
    """In-memory AList keyed by fs paths from the real AList._fs_path."""

    def __init__(self):
        self.files = {}
        self.dirs = {"/", "/mihari"}
        self.ops = []
        self.uploaded = []

    def _fs(self, path):
        return AList._fs_path(self, path)

    def _record(self, op, logical):
        fs = self._fs(logical)
        self.ops.append((op, logical, fs))
        return fs

    def mkdir(self, path):
        fs = self._record("mkdir", path)
        if fs == "/":
            raise AListError("alist write failed")
        parent = fs.rsplit("/", 1)[0] or "/"
        if parent not in self.dirs:
            raise AListError("alist write failed")
        self.dirs.add(fs)

    def list_dir(self, path):
        fs = self._record("list_dir", path)
        if fs not in self.dirs:
            raise AListError("invalid directory listing")
        prefix = fs.rstrip("/") + "/"
        names = set()
        for item in list(self.dirs) + list(self.files):
            if item == fs:
                continue
            if item.startswith(prefix):
                names.add(item[len(prefix):].split("/", 1)[0])
        entries = []
        for name in names:
            child = "/" + name if fs == "/" else prefix + name
            entries.append({"name": name, "is_dir": child in self.dirs})
        return entries

    def exists(self, path):
        self._record("exists", path)
        parent, name = path.rsplit("/", 1)
        if not name:
            raise AListError("invalid object path")
        return any(entry["name"] == name for entry in self.list_dir(parent or "/"))

    def upload(self, local, remote):
        fs = self._record("upload", remote)
        self.files[fs] = Path(local).read_bytes()
        self.uploaded.append(remote)

    def upload_text(self, text, remote):
        fs = self._record("upload_text", remote)
        self.files[fs] = text.encode()
        self.uploaded.append(remote)

    def content(self, path):
        self._record("content", path)
        if not self.exists(path):
            return None
        value = self.files.get(self._fs(path))
        return value.decode() if value is not None else None

    def read_bytes(self, path, **_kwargs):
        fs = self._record("read_bytes", path)
        return self.files[fs]

    def remove(self, base, names):
        self._record("remove", base)
        for name in names:
            logical = base.rstrip("/") + "/" + name
            fs = self._fs(logical)
            self.files = {
                key: value
                for key, value in self.files.items()
                if key != fs and not key.startswith(fs + "/")
            }
            self.dirs = {
                key for key in self.dirs if key != fs and not key.startswith(fs + "/")
            }

    def public_url(self, path):
        return "https://example.invalid" + path
```

缺失 fs 目录时 `list_dir` **raise `AListError`**（相对现有 Fake 返回 `[]` 的关键差异）。不得在空的旧 `FakeAList` 上写 first-publish——旧 Fake 对缺失目录返回 `[]`，会假绿。

**Step 2: 红灯测试**

`scripts/test_release_alist.py`：

```python
from test_alist_topology_fake import TopologyFake


def test_publish_dev_bootstraps_missing_channel_root(tmp_path):
    alist = TopologyFake()
    args = SimpleNamespace(
        version="v1.2.3-dev.1",
        dist_dir=make_dist(tmp_path),
        repo_root=tmp_path,
        base_path="/mihari-release/mihari-dev",
        commit_sha="a" * 40,
        channel="dev",
        keep_versions=5,
    )
    release.publish(alist, args)
    assert any(
        op == "mkdir" and logical == "/mihari-release/mihari-dev" and fs == "/mihari-dev"
        for op, logical, fs in alist.ops
    )
    assert not any(fs == "/mihari-release" for _, _, fs in alist.ops)
    index = f"{args.base_path}/index.txt"
    assert alist.content(index) is not None
    assert "latest v1.2.3-dev.1" in alist.content(index)
    assert index not in alist.files
```

**Step 3: 跑测试确认因缺少 `ensure_channel_root` 而失败**

Run: `python -m pytest scripts/test_release_alist.py::test_publish_dev_bootstraps_missing_channel_root -q`

Expected: FAIL，`SystemExit` / `unable to inspect release baseline`（`content(index)` → `exists` → `list_dir` 缺失 fs `/mihari-dev` raise）。

**Step 4: 不要 commit。** 进入 Task 4。

---

### Task 4: `storage_root_entries` + `ensure_channel_root`，first-publish 变绿

**Files:**
- Modify: `scripts/alist_client.py`
- Modify: `scripts/release-alist.py`
- Modify: `scripts/test_release_alist.py`
- Create: `scripts/test_alist_topology_fake.py`（来自 Task 3，本 Task 才进 commit）
- Modify: `scripts/test_alist_client.py`（可选：直接测 `storage_root_entries`）

**Step 1: 再加红灯测试（全部走 `TopologyFake`）**

```python
def test_ensure_channel_root_fails_closed_without_mihari_sibling(tmp_path):
    alist = TopologyFake()
    alist.dirs.discard("/mihari")
    args = SimpleNamespace(
        version="v1.2.3-dev.1",
        dist_dir=make_dist(tmp_path),
        repo_root=tmp_path,
        base_path="/mihari-release/mihari-dev",
        commit_sha="a" * 40,
        channel="dev",
        keep_versions=5,
    )
    with pytest.raises(SystemExit):
        release.publish(alist, args)
    assert not any(op == "mkdir" for op, _, _ in alist.ops)
    assert not any(fs == "/mihari-release" for _, _, fs in alist.ops)


def test_ensure_channel_root_is_noop_when_mihari_dev_already_a_directory(tmp_path):
    alist = TopologyFake()
    alist.dirs.add("/mihari-dev")
    release.ensure_channel_root(alist, "/mihari-release/mihari-dev", "dev")
    assert not any(op == "mkdir" for op, _, _ in alist.ops)
    args = SimpleNamespace(
        version="v1.2.3-dev.1",
        dist_dir=make_dist(tmp_path),
        repo_root=tmp_path,
        base_path="/mihari-release/mihari-dev",
        commit_sha="a" * 40,
        channel="dev",
        keep_versions=5,
    )
    release.publish(alist, args)
    assert not any(
        op == "mkdir" and logical == "/mihari-release/mihari-dev"
        for op, logical, _ in alist.ops
    )
    assert alist.content(f"{args.base_path}/index.txt") is not None
    assert not any(fs == "/mihari-release" for _, _, fs in alist.ops)


def test_ensure_channel_root_fails_when_mihari_dev_is_not_a_directory(tmp_path):
    alist = TopologyFake()
    alist.files["/mihari-dev"] = b"not-a-directory"
    args = SimpleNamespace(
        version="v1.2.3-dev.1",
        dist_dir=make_dist(tmp_path),
        repo_root=tmp_path,
        base_path="/mihari-release/mihari-dev",
        commit_sha="a" * 40,
        channel="dev",
        keep_versions=5,
    )
    with pytest.raises(SystemExit):
        release.publish(alist, args)
    assert not any(op == "mkdir" for op, _, _ in alist.ops)
    assert not any(fs == "/mihari-release" for _, _, fs in alist.ops)


def test_stable_publish_does_not_list_storage_root_or_mkdir_dev(tmp_path):
    alist = TopologyFake()
    repo_root = tmp_path / "repo"
    write_root_installers(repo_root)
    args = SimpleNamespace(
        version="v1.2.3",
        dist_dir=make_dist(tmp_path),
        repo_root=repo_root,
        base_path="/mihari-release/mihari",
        commit_sha="a" * 40,
        channel="stable",
        keep_versions=5,
    )
    release.publish(alist, args)
    assert not any(op == "list_dir" and logical == "/" for op, logical, _ in alist.ops)
    assert not any(op == "mkdir" and "mihari-dev" in logical for op, logical, _ in alist.ops)
    assert not any(fs == "/mihari-release" for _, _, fs in alist.ops)
    assert alist.content("/mihari-release/mihari/index.txt") is not None
    assert "/mihari-release/mihari/index.txt" not in alist.files


def test_ensure_channel_root_skips_stable_without_listing():
    alist = TopologyFake()
    release.ensure_channel_root(alist, "/mihari-release/mihari", "stable")
    assert alist.ops == []
```

`test_stable_publish_does_not_list_storage_root_or_mkdir_dev` **必须调用 `release.publish()`**，不得用 `ensure_channel_root(..., "stable")` 代替。`write_root_installers` 已在同文件后部定义。helper 单测 `test_ensure_channel_root_skips_stable_without_listing` 是附加项，不是替代。

**Step 2: 跑，确认 FAIL**

Run: `python -m pytest scripts/test_release_alist.py::test_publish_dev_bootstraps_missing_channel_root scripts/test_release_alist.py::test_ensure_channel_root_fails_closed_without_mihari_sibling scripts/test_release_alist.py::test_ensure_channel_root_is_noop_when_mihari_dev_already_a_directory scripts/test_release_alist.py::test_ensure_channel_root_fails_when_mihari_dev_is_not_a_directory scripts/test_release_alist.py::test_stable_publish_does_not_list_storage_root_or_mkdir_dev scripts/test_release_alist.py::test_ensure_channel_root_skips_stable_without_listing -q`

Expected: FAIL（尚无 `ensure_channel_root` / `storage_root_entries`）。

**Step 3: 最小实现**

`scripts/alist_client.py`（`fail` 已在同文件）：

```python
def storage_root_entries(alist):
    try:
        entries = alist.list_dir("/")
    except Exception:
        fail("unable to inspect release root")
    if not isinstance(entries, list):
        fail("unable to inspect release root")
    mihari = [e for e in entries if isinstance(e, dict) and e.get("name") == "mihari"]
    if len(mihari) != 1 or mihari[0].get("is_dir") is not True:
        fail("unable to inspect release root")
    return entries
```

`scripts/release-alist.py` 的 import 行改为包含 `storage_root_entries`：

```python
from alist_client import AListError, DEFAULT_BASE_PATH, DEFAULT_KEEP_VERSIONS, PLATFORMS, bundle_name, connect, fail, info, sha256_file, storage_root_entries
```

```python
def ensure_channel_root(alist, base_path, channel):
    if channel != "dev":
        return
    entries = storage_root_entries(alist)
    matches = [e for e in entries if isinstance(e, dict) and e.get("name") == "mihari-dev"]
    if not matches:
        alist.mkdir(base_path)
        return
    if len(matches) != 1 or matches[0].get("is_dir") is not True:
        fail("channel base path is not a directory")


def publish(alist, args):
    if args.channel == "dev":
        ensure_channel_root(alist, args.base_path, args.channel)
    ensure_monotonic_version(...)
    # 其余保持不变
```

`list_dir("/")` 的 JSON path 已由 Task 2 钉死。禁止 `list_dir("/mihari-release")`。

**Step 4: 给仍走旧 `FakeAList` 的 `publish(channel="dev")` 测试补存储根**

旧 Fake 的 `list_dir("/")` 按**第一段**切名。逻辑路径 `/mihari-release/mihari-dev/...` 会让 `"/" ` 列出 `name="mihari-release"`，于是 `storage_root_entries` fail-closed。探测本身就是存储根行为变化，旧测试必须认识 `"/"`。

在 `scripts/test_release_alist.py` 的 `FakeAList` 旁加入：

```python
def seed_storage_root(fake, *, has_dev_channel=True):
    # Old Fake lists "/" by first logical/fs segment. These two fs-shaped
    # siblings make storage_root_entries see mihari [+ mihari-dev]
    # without preloading the forbidden logical path /mihari-release.
    fake.dirs.add("/mihari")
    if has_dev_channel:
        fake.dirs.add("/mihari-dev")
```

对每一个仍走旧 Fake、且调用 `release.publish(..., channel="dev")` 的测试，在 `publish()` 前调用 `seed_storage_root(alist)`（publish 缺 `/mihari-dev` 时会 mkdir 逻辑 base，空 Fake 可接受；为少一次 mkdir 噪音，publish 测试也 seed 两个 sibling）：

- `test_publish_dev_never_uploads_root_installers`
- `test_publish_rechecks_index_at_commit_point_and_preserves_newer_index`
- `test_post_index_retention_list_failure_skips_safely_without_leaking_details`

`LateListFailureAList`：**不要**把 `path == "/"` 计入 `list_calls`，并且 seed：

```python
        def list_dir(self, path):
            if path == "/":
                return super().list_dir(path)
            self.list_calls += 1
            if self.list_calls > 2:
                raise RuntimeError("https://cloud.invalid/list?token=retention-secret body")
            return super().list_dir(path)
```

只调 `upload_version_dir` / `ensure_monotonic_version` / `prune_versions` / `write_index_reliably` 的测试不走 `publish()`，不必 seed。

**Step 5: 跑新测试 + 全文件**

Run: `python -m pytest scripts/test_release_alist.py::test_publish_dev_bootstraps_missing_channel_root scripts/test_release_alist.py::test_ensure_channel_root_fails_closed_without_mihari_sibling scripts/test_release_alist.py::test_ensure_channel_root_is_noop_when_mihari_dev_already_a_directory scripts/test_release_alist.py::test_ensure_channel_root_fails_when_mihari_dev_is_not_a_directory scripts/test_release_alist.py::test_stable_publish_does_not_list_storage_root_or_mkdir_dev scripts/test_release_alist.py::test_ensure_channel_root_skips_stable_without_listing scripts/test_release_alist.py::test_publish_dev_never_uploads_root_installers -q`

Expected: PASS

Run: `python -m pytest scripts/test_release_alist.py -q`

Expected: PASS

**Step 6: Commit（含 Task 3 的 Fake 与 first-publish 测试）**

```bash
git add scripts/alist_client.py scripts/release-alist.py scripts/test_release_alist.py scripts/test_alist_topology_fake.py scripts/test_alist_client.py
git commit -m "feat: 仅在 dev 通道自动创建 AList 根目录"
```

---

### Task 5: retract 在已确认存储根上缺失 `mihari-dev` 时 AList no-op

**Files:**
- Modify: `scripts/retract-alist.py`
- Modify: `scripts/test_retract_alist.py`

**Step 1: 红灯测试（新行为走 `TopologyFake`）**

在 `scripts/test_retract_alist.py` 增加 `from test_alist_topology_fake import TopologyFake` 以及 `from alist_client import AListError`。

```python
def test_dev_retract_noops_when_storage_root_has_mihari_but_no_dev_channel():
    fake = TopologyFake()
    retract_mod.retract(fake, "/mihari-release/mihari-dev", "v1.0.0-dev.1", "dev", "a" * 40)
    assert not any(
        op in {"remove", "upload", "upload_text", "mkdir"} for op, _, _ in fake.ops
    )
    assert fake.files == {}
    assert fake.dirs == {"/", "/mihari"}
    assert not any(fs == "/mihari-release" for _, _, fs in fake.ops)


def test_dev_retract_fails_closed_when_storage_root_lacks_mihari():
    fake = TopologyFake()
    fake.dirs.discard("/mihari")
    with pytest.raises(SystemExit):
        retract_mod.retract(fake, "/mihari-release/mihari-dev", "v1.0.0-dev.1", "dev", "a" * 40)
    assert not any(op in {"remove", "upload", "upload_text", "mkdir"} for op, _, _ in fake.ops)


def test_dev_retract_fails_closed_when_storage_root_list_raises():
    fake = TopologyFake()
    original = fake.list_dir

    def list_dir(path):
        if path == "/":
            raise AListError("invalid directory listing")
        return original(path)

    fake.list_dir = list_dir
    with pytest.raises(SystemExit):
        retract_mod.retract(fake, "/mihari-release/mihari-dev", "v1.0.0-dev.1", "dev", "a" * 40)
    assert not any(op in {"remove", "upload", "upload_text"} for op, _, _ in fake.ops)


def test_dev_retract_uses_existing_logic_when_channel_root_exists(tmp_path, monkeypatch):
    monkeypatch.setenv("RUNNER_TEMP", str(tmp_path))
    fake = TopologyFake()
    fake.dirs.add("/mihari-dev")
    _add_topology_complete(fake, "/mihari-release/mihari-dev", "v1.0.0-dev.1")
    _add_topology_complete(fake, "/mihari-release/mihari-dev", "v1.1.0-dev.1")
    fake.upload_text("latest v1.1.0-dev.1\n", "/mihari-release/mihari-dev/index.txt")
    retract_mod.retract(
        fake, "/mihari-release/mihari-dev", "v1.1.0-dev.1", "dev", "a" * 40
    )
    body = fake.content("/mihari-release/mihari-dev/index.txt")
    assert body is not None and body.startswith("latest v1.0.0-dev.1\n")
    assert not fake.exists("/mihari-release/mihari-dev/v1.1.0-dev.1")
    assert not any(fs == "/mihari-release" for _, _, fs in fake.ops)
```

`_add_topology_complete` 用 Fake 的逻辑路径 API（`mkdir` / `upload_text`）写入，让键落在 fs 空间；不要把逻辑路径塞进 `fake.files`。可按 `test_retract_alist.add_complete` 的 COMPLETE / BUILDINFO / 六包 / SHA256SUMS 形状移植。

**Step 2: 跑，确认 FAIL**

Run: `python -m pytest scripts/test_retract_alist.py::test_dev_retract_noops_when_storage_root_has_mihari_but_no_dev_channel scripts/test_retract_alist.py::test_dev_retract_fails_closed_when_storage_root_lacks_mihari scripts/test_retract_alist.py::test_dev_retract_fails_closed_when_storage_root_list_raises scripts/test_retract_alist.py::test_dev_retract_uses_existing_logic_when_channel_root_exists -q`

Expected: FAIL（当前 `directory_exists` → `exists(base/version)` → `list_dir(parent)` 在缺失根上 fail `"unable to inspect retraction target"`，不是成功 no-op；list `"/"` 尚未成为 fail-closed 探测）。

**Step 3: 实现**

`scripts/retract-alist.py` 的 import 行必须加上 `storage_root_entries`：

```python
from alist_client import DEFAULT_BASE_PATH, PLATFORMS, bundle_name, connect, fail, info, storage_root_entries
```

在 `retract()` 里 `validate_inputs` 之后、`directory_exists` 之前：

```python
    if channel == "dev":
        entries = storage_root_entries(alist)
        matches = [e for e in entries if isinstance(e, dict) and e.get("name") == "mihari-dev"]
        if not matches:
            return
        if len(matches) != 1 or matches[0].get("is_dir") is not True:
            fail("channel base path is not a directory")
```

`storage_root_entries` 失败（无 `mihari`、listing 不是 list、传输/`AListError`）已经 `fail("unable to inspect release root")`，不要当成 no-op。

**Step 4: 给仍走旧 `Fake` 的 `retract(..., "dev")` 测试补存储根**

在 `scripts/test_retract_alist.py` 的 `Fake` 旁加入与 Task 4 **相同**的 `seed_storage_root`。

对每一个调用 `retract_mod.retract(..., "dev")` 且应继续走现有 retract 语义的旧 Fake 测试，在调用前 `seed_storage_root(fake, has_dev_channel=True)`：

- `test_retract_refuses_noncanonical_checksum_manifest_without_mutation`
- `test_dev_retract_rebuilds_highest_remaining_complete`
- `test_dev_retract_refuses_mismatched_identity`
- `test_retract_missing_dev_directory_is_idempotent`
- `test_latest_retraction_switches_verified_index_before_removing_target`
- `test_latest_retraction_keeps_target_when_index_commit_fails`
- `test_remove_failure_after_index_commit_preserves_new_index_and_rerun_removes_orphan`
- `test_last_latest_retraction_commits_empty_index_before_removing_target`
- `test_latest_retraction_aborts_when_remaining_candidate_cannot_be_read`
- `test_latest_retraction_aborts_on_malformed_directory_listing`
- `test_non_latest_retraction_leaves_index_bytes_unchanged`
- `test_non_latest_retraction_fails_closed_when_index_changes_concurrently`
- `test_missing_target_referenced_by_index_fails_closed`
- `test_missing_target_not_referenced_by_index_is_idempotent`

`test_retract_missing_dev_directory_is_idempotent` 保持「**版本目录缺失、通道根存在**」：必须 seed **两个** sibling（`has_dev_channel=True`）。不要把它和新的「存储根有 `mihari`、无 `mihari-dev`」no-op 混成一个测试。seed 后断言改为：`mutations(fake) == []`，且 `"/mihari"` / `"/mihari-dev"` 仍在 `fake.dirs`（不要再要求 `fake.dirs == set()`）。

`test_latest_retraction_aborts_on_malformed_directory_listing` 当前用 `fake.list_dir = lambda _path: ...` 覆盖**所有**路径。probe 之后 `list_dir("/")` 会先被畸形 listing 打成 fail-closed，测不到 `highest_complete`。改为 seed 后只对非根路径返回畸形条目：

```python
    seed_storage_root(fake, has_dev_channel=True)
    original = fake.list_dir
    fake.list_dir = (
        lambda path: original(path) if path == "/" else [{"name": 123, "is_dir": True}]
    )
```

只调 `validate_inputs` / `verified_directory` 的测试不必 seed。

**Step 5: 跑相关 retract 测试**

Run: `python -m pytest scripts/test_retract_alist.py -q`

Expected: PASS

**Step 6: Commit**

```bash
git add scripts/retract-alist.py scripts/test_retract_alist.py
git commit -m "feat: 缺失 dev AList 根时撤回改为空操作"
```

---

### Task 6: `alist_channel_guard.py` snapshot / compare

**Files:**
- Create: `scripts/alist_channel_guard.py`
- Create: `scripts/test_alist_channel_guard.py`

**Step 1: 红灯测试（写完整，不要留 `...`）**

```python
import hashlib
import json
from pathlib import Path

import pytest

import alist_channel_guard as guard

STABLE_INDEX = "/mihari-release/mihari/index.txt"


class GuardFake:
    def __init__(self, body=None):
        self.body = body

    def content(self, path):
        assert path == STABLE_INDEX
        return self.body


def test_snapshot_missing_index_records_existed_false(tmp_path):
    output = tmp_path / "stable-isolation"
    guard.snapshot(GuardFake(None), STABLE_INDEX, output)
    assert (output / "index.txt").read_bytes() == b""
    metadata = json.loads((output / "metadata.json").read_text(encoding="utf-8"))
    assert metadata["channel"] == "stable"
    assert metadata["existed"] is False
    assert metadata["path"] == STABLE_INDEX
    assert metadata["sha256"] == hashlib.sha256(b"").hexdigest()


def test_compare_accepts_unchanged_bytes(tmp_path):
    output = tmp_path / "stable-isolation"
    body = "latest v1.2.3\n"
    guard.snapshot(GuardFake(body), STABLE_INDEX, output)
    guard.compare(GuardFake(body), STABLE_INDEX, output)


def test_compare_rejects_changed_index_without_logging_body(tmp_path, capsys):
    output = tmp_path / "stable-isolation"
    guard.snapshot(GuardFake("latest v1.2.3\n"), STABLE_INDEX, output)
    with pytest.raises(SystemExit):
        guard.compare(GuardFake("latest v9.0.0\n"), STABLE_INDEX, output)
    err = capsys.readouterr().err
    assert "foreign channel index changed during this mutation" in err
    assert "latest " not in err
    assert "v9.0.0" not in err


def test_guard_rejects_non_stable_index_path(tmp_path):
    output = tmp_path / "stable-isolation"
    fake = GuardFake("latest v1.2.3\n")
    for path in (
        "/mihari-release/mihari-dev/index.txt",
        "/mihari-release/mihari/other.txt",
        "/mihari/index.txt",
        "/",
    ):
        with pytest.raises(SystemExit):
            guard.snapshot(fake, path, output)
        with pytest.raises(SystemExit):
            guard.compare(fake, path, output)
```

**Step 2: 跑 FAIL**（模块不存在）

Run: `python -m pytest scripts/test_alist_channel_guard.py -q`

Expected: FAIL（`ModuleNotFoundError` 或导入失败）

**Step 3: 按设计文档实现 CLI**

- `snapshot --path --output-dir`
- `compare --path --expected-dir`
- path 必须**精确等于** `/mihari-release/mihari/index.txt`，否则 `fail`
- 单元测试注入 Fake：`snapshot(alist, path, output_dir)` / `compare(alist, path, expected_dir)`；`main()` 再 `connect()`
- snapshot：`content` 为 `None` → 空 `index.txt`，`metadata.json` 含 `channel=stable`、`existed=false`、`path`、空字节 sha256；`metadata.json` 按设计 `sort_keys=True` 并追加 `\n`
- compare 不相等：`fail("foreign channel index changed during this mutation")`，stderr 不得含 index 正文

**Step 4: 跑 PASS**

Run: `python -m pytest scripts/test_alist_channel_guard.py -q`

Expected: PASS

**Step 5: Commit**

```bash
git add scripts/alist_channel_guard.py scripts/test_alist_channel_guard.py
git commit -m "feat: 增加稳定 index 隔离快照与比对"
```

---

### Task 7: `regenerate-index.py --channel`

**Files:**
- Modify: `scripts/regenerate-index.py`
- Modify: `scripts/test_regenerate_index.py`

**Step 1: 红灯测试**

现有 `test_regenerate_index_uses_highest_verified_stable_release_and_reliable_writer` 继续用两参数 `regenerate_index(fake, base)`，签名必须是 `regenerate_index(alist, base_path, channel="stable")`。

追加：

```python
def test_regenerate_index_rebuilds_dev_index_for_highest_complete(tmp_path, monkeypatch):
    monkeypatch.setenv("RUNNER_TEMP", str(tmp_path))
    fake = FakeAList()
    base = "/mihari-release/mihari-dev"
    add_complete(fake, base, "v1.2.3-dev.1")
    add_complete(fake, base, "v1.2.3-dev.2")
    regenerate.regenerate_index(fake, base, channel="dev")
    body = fake.files[f"{base}/index.txt"].decode()
    assert body.startswith("latest v1.2.3-dev.2\n")
    assert len(body.splitlines()) == 1 + len(PLATFORMS)


def test_regenerate_index_rejects_dev_channel_with_stable_path():
    fake = FakeAList()
    with pytest.raises(SystemExit):
        regenerate.regenerate_index(fake, "/mihari-release/mihari", channel="dev")
    assert fake.uploads == []


def test_regenerate_index_help_names_stable_default_path(monkeypatch, capsys):
    monkeypatch.setattr("sys.argv", ["regenerate-index.py", "--help"])
    with pytest.raises(SystemExit) as exc:
        regenerate.main()
    assert exc.value.code == 0
    out = capsys.readouterr().out
    assert "stable" in out
    assert "/mihari-release/mihari" in out


def test_main_channel_dev_without_base_path_targets_dev_root(monkeypatch, capsys):
    captured = {}

    def fake_connect():
        return object()

    def fake_regenerate(alist, base_path, channel="stable"):
        captured["base_path"] = base_path
        captured["channel"] = channel

    monkeypatch.setattr(regenerate, "connect", fake_connect)
    monkeypatch.setattr(regenerate, "regenerate_index", fake_regenerate)
    monkeypatch.setattr("sys.argv", ["regenerate-index.py", "--channel", "dev"])
    regenerate.main()
    assert captured == {
        "base_path": "/mihari-release/mihari-dev",
        "channel": "dev",
    }
    logged = capsys.readouterr()
    text = logged.out + logged.err
    assert "regenerating dev index at /mihari-release/mihari-dev/index.txt" in text
```

`info(...)` 必须在 `connect()` / `regenerate_index` / 任何 PUT **之前**。`info()` 走 stdout（`::notice::`）。

默认不传 `--channel` 仍 stable：现有两参数测试覆盖 `regenerate_index`；CLI 默认由 `--help` 与 `expected_base_path(args.channel)` 钉死。

**Step 2: 跑 FAIL**

Run: `python -m pytest scripts/test_regenerate_index.py -q`

Expected: FAIL（尚无 `channel` 参数 / `--channel`）

**Step 3: 实现**

```python
# 在现有 `from release_policy import validate_base_path` 上追加 expected_base_path
from release_policy import expected_base_path, validate_base_path


def regenerate_index(alist, base_path, channel="stable"):
    """Rebuild the channel index through the shared reliable writer."""
    try:
        validate_base_path(channel, base_path)
    except ValueError as error:
        fail(str(error))
    retract = _load_retract()
    index_path = f"{base_path}/index.txt"
    previous = retract.read_index(alist, index_path)
    try:
        latest = retract.highest_complete(
            alist, base_path, excluded=None, channel=channel
        )
    except retract.RemoteScanError:
        fail("unable to inspect release versions")
    if latest is None:
        fail("no COMPLETE version dir found — nothing to rebuild index from")
    info(f"highest complete version: {latest}")
    try:
        body = retract.index_body(alist, base_path, latest, channel)
    except retract.RemoteScanError:
        fail("unable to verify release directory")
    retract.write_index_reliably(
        alist, index_path, body, previous, channel=channel
    )


def main():
    parser = argparse.ArgumentParser(
        description="Regenerate index.txt for the current AList topology."
    )
    parser.add_argument(
        "--channel",
        choices=("stable", "dev"),
        default="stable",
        help="Index channel to rebuild (default: stable, writes /mihari-release/mihari/index.txt)",
    )
    parser.add_argument("--base-path", default=None)
    args = parser.parse_args()
    channel = args.channel
    base_path = args.base_path or expected_base_path(channel)
    info(f"regenerating {channel} index at {base_path}/index.txt")
    alist = connect()
    regenerate_index(alist, base_path, channel)
    info("index.txt regenerated with current public_url() links")
```

默认 `--base-path` 今天是 `DEFAULT_BASE_PATH`（stable）。改成 `None` 后，CLI 省略 base-path 时用 `expected_base_path(channel)`。显式传错路径仍由 `validate_base_path` 拒绝。`--channel dev` 且不传 `--base-path` 必须打到 `/mihari-release/mihari-dev`。

**Step 4: PASS + Commit**

Run: `python -m pytest scripts/test_regenerate_index.py -q`

Expected: PASS

```bash
git add scripts/regenerate-index.py scripts/test_regenerate_index.py
git commit -m "feat: regenerate-index 支持显式 dev 通道"
```

---

### Task 8: 把新测试送进 CI 列表

**Files:**
- Modify: `scripts/test_release_workflow.py` — `RELEASE_SAFETY_TESTS`
- Modify: `.github/workflows/ci.yml` — unit job pytest 命令

**Step 1: 改 constant（先改测试侧，跑 CI 锁定测试应红）**

`RELEASE_SAFETY_TESTS` 改为（注意末尾 `-q` 仍在字符串里）：

```
scripts/test_release_policy.py scripts/test_github_release_policy.py scripts/test_release_workflow.py scripts/test_alist_client.py scripts/test_alist_index.py scripts/test_release_alist.py scripts/test_retract_alist.py scripts/test_regenerate_index.py scripts/test_alist_channel_guard.py -q
```

**Step 2: 跑**

Run: `python -m pytest scripts/test_release_workflow.py::test_ci_runs_release_safety_suite_from_pinned_requirements_on_all_integration_branches -q`

Expected: FAIL（`ci.yml` 仍是旧命令）

**Step 3: 同步 `ci.yml` 那一行 `run:` 与 constant **逐字相同**（含 `-q`）。

**Step 4: PASS 全量 release-safety**

Run: `python -m pytest scripts/test_release_policy.py scripts/test_github_release_policy.py scripts/test_release_workflow.py scripts/test_alist_client.py scripts/test_alist_index.py scripts/test_release_alist.py scripts/test_retract_alist.py scripts/test_regenerate_index.py scripts/test_alist_channel_guard.py -q`

Expected: PASS。`test_release_documents_record_verified_dev_github_release_and_unavailable_dev_alist` 此时必须仍绿（尚未建 `retract-dev.yml`，文档仍写不写 AList）。

**Step 5: Commit（PR 1 收尾）**

```bash
git add scripts/test_release_workflow.py .github/workflows/ci.yml
git commit -m "test: 将通道守卫测试列入 CI 发版安全套件"
```

---

### Task 9: 先写失败的 YAML 测试，再接 `release-dev.yml`

**Files:**
- Modify: `scripts/test_release_workflow.py` — **先写测试，此时两个 YAML 都还没改/还不存在**
- Modify: `.github/workflows/release-dev.yml` — 测试红了之后才写

YAML **完整步骤、并发组、compare-first exit、两条 artifact、notes marker** 复制设计文档「`release-dev.yml` 接线」一节。不要发明第二种退出结构。

**本 Task 与 Task 10–11 共用一次绿灯 commit。写完测试与 `release-dev.yml` 后不要 commit。不要改 `test_release_documents_*`。**

**Step 1: 在任一 YAML 改动之前写入失败测试**

在 `scripts/test_release_workflow.py` 增加：

```python
RETRACT_DEV_WORKFLOW = Path(__file__).resolve().parents[1] / ".github" / "workflows" / "retract-dev.yml"
```

把 `test_release_workflow_is_github_only_and_limits_tag_peeling` **拆开**：删除 `ALIST_` / `mihari-dev` 禁令，保留 peel 深度断言（函数可改名为 `test_release_workflow_limits_tag_peeling`，或留原名但去掉 github-only 断言）。文档测试一字不改。

然后新增（函数名锁定）：

```python
def test_dev_publish_and_retract_use_independent_alist_lock():
    release_dev = yaml.safe_load(WORKFLOW.read_text(encoding="utf-8"))
    retract_dev = yaml.safe_load(RETRACT_DEV_WORKFLOW.read_text(encoding="utf-8"))
    expected = {"group": "mihari-dev-alist", "cancel-in-progress": False}
    assert release_dev["jobs"]["publish"].get("concurrency") == expected
    assert retract_dev["jobs"]["retract"].get("concurrency") == expected
    assert "mihari-stable-alist" not in WORKFLOW.read_text(encoding="utf-8")
    assert "mihari-stable-alist" not in RETRACT_DEV_WORKFLOW.read_text(encoding="utf-8")
    assert release_dev.get("concurrency", {}).get("group") == "dev-release-${{ inputs.version }}"


def test_dev_alist_mutation_uses_compare_first_exit():
    cases = (
        (WORKFLOW, "publish", "Publish to AList drive", "release-alist.py", "publish_status"),
        (RETRACT_DEV_WORKFLOW, "retract", "Retract from AList drive", "retract-alist.py", "retract_status"),
    )
    for path, job, step_name, writer, status_var in cases:
        document = yaml.safe_load(path.read_text(encoding="utf-8"))
        run = _workflow_step(document, job, name=step_name)["run"]
        assert "set +e" in run
        assert run.index(writer) < run.index("alist_channel_guard.py compare")
        compare_exit = 'if [ "${compare_status}" -ne 0 ]; then exit "${compare_status}"; fi'
        assert compare_exit in run
        assert run.index(compare_exit) < run.index(f'exit "${{{status_var}}}"')


def test_dev_retract_peel_expected_sha_is_release_sha_not_job_sha():
    document = yaml.safe_load(RETRACT_DEV_WORKFLOW.read_text(encoding="utf-8"))
    peel = _workflow_step(document, "retract", name="Peel canonical tag for identity SHA")
    run = peel["run"]
    assert "github_release_policy.py tag-chain" in run
    assert '--expected-sha "${RELEASE_SHA}"' in run or '--expected-sha "${tag_sha}"' in run
    assert '--expected-sha "${SHA}"' not in run
    mutation = _workflow_step(document, "retract", name="Retract from AList drive")
    assert '--commit-sha "${RELEASE_SHA}"' in mutation["run"]
    assert '--commit-sha "${SHA}"' not in mutation["run"]


def test_dev_alist_isolation_artifacts_are_separate_from_writer_backup():
    expected_if = (
        "env.ALIST_CONFIGURED == 'true' && "
        "((failure() && steps.alist_mutation.outcome == 'failure') || "
        "(cancelled() && steps.alist_mutation.outcome == 'cancelled'))"
    )
    for path, job in ((WORKFLOW, "publish"), (RETRACT_DEV_WORKFLOW, "retract")):
        document = yaml.safe_load(path.read_text(encoding="utf-8"))
        text = path.read_text(encoding="utf-8")
        steps = document["jobs"][job]["steps"]
        dev_backup = next(
            step for step in steps
            if str(step.get("with", {}).get("name", "")).startswith("dev-index-backup-")
        )
        isolation = next(
            step for step in steps
            if str(step.get("with", {}).get("name", "")).startswith("stable-index-isolation-")
        )
        assert dev_backup["if"] == expected_if
        assert isolation["if"] == expected_if
        assert dev_backup["with"]["path"] == "${{ runner.temp }}/mihari-index-backup/dev/**"
        assert isolation["with"]["path"] == "${{ runner.temp }}/mihari-index-backup/stable-isolation/**"
        assert isolation["with"]["path"] != "${{ runner.temp }}/mihari-index-backup/stable/**"
        assert "${{ runner.temp }}/mihari-index-backup/stable/**" not in text


def test_dev_retract_resolve_outputs_sha_and_alist_runs_before_github_delete():
    document = yaml.safe_load(RETRACT_DEV_WORKFLOW.read_text(encoding="utf-8"))
    resolve = document["jobs"]["resolve"]
    retract = document["jobs"]["retract"]
    assert resolve["outputs"]["sha"] == "${{ steps.source.outputs.sha }}"
    assert "refs/heads/dev" in retract["if"]
    checkout = next(step for step in retract["steps"] if step.get("uses") == "actions/checkout@v7")
    assert checkout["with"]["ref"] == "${{ needs.resolve.outputs.sha }}"
    names = [step.get("name") for step in retract["steps"]]
    assert names.index("Retract from AList drive") < names.index("Delete GitHub prerelease")
```

再补设计表里其余可执行断言（同样在写 YAML 之前）：

```python
def test_dev_alist_writers_pin_channel_base_path_and_commit_sha():
    publish = WORKFLOW.read_text(encoding="utf-8")
    retract = RETRACT_DEV_WORKFLOW.read_text(encoding="utf-8")
    assert "--channel dev" in publish
    assert "--base-path /mihari-release/mihari-dev" in publish
    assert '--commit-sha "${SHA}"' in publish
    assert "--channel dev" in retract
    assert "--base-path /mihari-release/mihari-dev" in retract


def test_dev_publish_mutates_alist_after_final_github_verify():
    workflow = WORKFLOW.read_text(encoding="utf-8")
    assert workflow.index("Final verify prerelease and stable latest") < workflow.index(
        "Publish to AList drive"
    )


def test_dev_release_notes_append_index_url_with_stable_root_downloaders():
    workflow = WORKFLOW.read_text(encoding="utf-8")
    assert "<!-- aio-install-dev -->" in workflow
    assert "mihari-dev/index.txt" in workflow
    assert "mihari-release/mihari/install-aio-remote.sh" in workflow


def test_dev_retract_github_delete_is_idempotent_and_retains_canonical_tag():
    document = yaml.safe_load(RETRACT_DEV_WORKFLOW.read_text(encoding="utf-8"))
    delete = _workflow_step(document, "retract", name="Delete GitHub prerelease")
    run = delete["run"]
    assert "gh release delete" in run
    assert "--cleanup-tag" not in run
    assert "canonical dev tag retained" in run
    gate = next(
        step["run"]
        for step in document["jobs"]["retract"]["steps"]
        if str(step.get("name", "")).startswith("Gate")
    )
    assert 'echo "${VERSION}"' not in gate


def test_alist_runtime_dependencies_are_pinned_after_checkout_in_dev_workflows():
    release_dev = yaml.safe_load(WORKFLOW.read_text(encoding="utf-8"))
    retract_dev = yaml.safe_load(RETRACT_DEV_WORKFLOW.read_text(encoding="utf-8"))
    for workflow, job_name, install_step_name in (
        (release_dev, "publish", "Publish to AList drive"),
        (retract_dev, "retract", "Retract from AList drive"),
    ):
        steps = workflow["jobs"][job_name]["steps"]
        checkout_index = next(
            index for index, step in enumerate(steps) if step.get("uses") == "actions/checkout@v7"
        )
        setup_index = next(
            index for index, step in enumerate(steps) if step.get("uses") == "actions/setup-python@v7"
        )
        install_index = next(
            index for index, step in enumerate(steps) if step.get("name") == install_step_name
        )
        install = steps[install_index]["run"]
        assert checkout_index < setup_index < install_index
        assert steps[setup_index]["with"] == {"python-version": "3.12"}
        assert (
            "python -m pip install --disable-pip-version-check -r scripts/requirements-release.txt"
            in install
        )
        assert "pip install requests" not in install
```

关键钉死字符串（实现 YAML 时对照，测试覆盖）：

- `jobs.publish.concurrency.group: mihari-dev-alist`，`cancel-in-progress: false`（YAML 布尔 `false` → Python `False`）
- 文件级 `dev-release-${{ inputs.version }}` 保留
- 不含 `mihari-stable-alist`
- `ALIST_CONFIGURED: ${{ secrets.ALIST_URL != '' }}`
- secrets 只出现在 `id: alist_mutation`
- mutation 在 `Final verify prerelease and stable latest` **之后**
- `release-alist.py --channel dev --base-path /mihari-release/mihari-dev --commit-sha "${SHA}"`
- `if [ "${compare_status}" -ne 0 ]; then exit "${compare_status}"; fi` 然后 `exit "${publish_status}"`
- artifacts: `dev-index-backup-` → `${{ runner.temp }}/mihari-index-backup/dev/**`；`stable-index-isolation-` → `${{ runner.temp }}/mihari-index-backup/stable-isolation/**`（**不是** `.../stable/**`）
- notes：`<!-- aio-install-dev -->` 与 `MIHARI_INDEX_URL=.../mihari-dev/index.txt`，下载器仍是稳定根 `install-aio-remote.sh`
- 仍 `-buildvcs=false -trimpath`；`make_latest=false`

**Step 2: 跑，确认因 YAML 未接线 / `retract-dev.yml` 不存在而失败**

Run: `python -m pytest scripts/test_release_workflow.py::test_dev_publish_and_retract_use_independent_alist_lock scripts/test_release_workflow.py::test_dev_alist_mutation_uses_compare_first_exit scripts/test_release_workflow.py::test_dev_retract_peel_expected_sha_is_release_sha_not_job_sha scripts/test_release_workflow.py::test_dev_alist_isolation_artifacts_are_separate_from_writer_backup scripts/test_release_workflow.py::test_dev_retract_resolve_outputs_sha_and_alist_runs_before_github_delete scripts/test_release_workflow.py::test_dev_alist_writers_pin_channel_base_path_and_commit_sha scripts/test_release_workflow.py::test_dev_publish_mutates_alist_after_final_github_verify scripts/test_release_workflow.py::test_dev_release_notes_append_index_url_with_stable_root_downloaders scripts/test_release_workflow.py::test_dev_retract_github_delete_is_idempotent_and_retains_canonical_tag scripts/test_release_workflow.py::test_alist_runtime_dependencies_are_pinned_after_checkout_in_dev_workflows -q`

Expected: FAIL（`release-dev.yml` 无 `ALIST_`；`retract-dev.yml` 不存在 → `FileNotFoundError`）。

文档测试此时必须仍绿：

Run: `python -m pytest scripts/test_release_workflow.py::test_release_documents_record_verified_dev_github_release_and_unavailable_dev_alist -q`

Expected: PASS

**Step 3: 按设计复制 `release-dev.yml` 接线**（此时还不要创建 `retract-dev.yml`）

**Step 4: 再跑 Step 2 的测试**

Expected: 依赖 `release-dev.yml` 的断言变绿或接近绿；**所有读取 `retract-dev.yml` 的测试仍 FAIL**。不要为了让 retract 测试假绿而去 skip。

**Step 5: 不要 commit。** 进入 Task 10。

---

### Task 10: 新增 `retract-dev.yml`

**Files:**
- Create: `.github/workflows/retract-dev.yml`
- Modify: `scripts/test_release_workflow.py`（仅当 Task 9 测试需微调断言字符串；不要在本 Task 才第一次写测试）

**Step 1: 先跑 Task 9 的 YAML 测试，确认 retract-dev 断言仍红**

Run: `python -m pytest scripts/test_release_workflow.py::test_dev_publish_and_retract_use_independent_alist_lock scripts/test_release_workflow.py::test_dev_alist_mutation_uses_compare_first_exit scripts/test_release_workflow.py::test_dev_retract_peel_expected_sha_is_release_sha_not_job_sha scripts/test_release_workflow.py::test_dev_alist_isolation_artifacts_are_separate_from_writer_backup scripts/test_release_workflow.py::test_dev_retract_resolve_outputs_sha_and_alist_runs_before_github_delete scripts/test_release_workflow.py::test_dev_retract_github_delete_is_idempotent_and_retains_canonical_tag -q`

Expected: FAIL（`retract-dev.yml` 仍不存在，或尚未写入 peel / delete 块）。

**Step 2: 按设计文档创建完整 job 图**

必须包含：

- `jobs.resolve.outputs.sha: ${{ steps.source.outputs.sha }}`
- `retract.if`: `github.ref == 'refs/heads/dev' && needs.resolve.outputs.sha != ''`
- checkout `needs.resolve.outputs.sha`
- confirm 布尔；错误路径不 echo `${VERSION}`
- Peel：`--expected-sha "${RELEASE_SHA}"`（peel 出的 commit）。**仅 peel 步骤文本**不得出现 `--expected-sha "${SHA}"`（用 `_workflow_step(..., name="Peel canonical tag for identity SHA")` 切片；全文件 grep 会被 `release-dev.yml` 的合法 `"${SHA}"` 和 retract job env `SHA:` 污染）
- `--commit-sha "${RELEASE_SHA}"` 给 `retract-alist.py`
- AList mutation **字面量上**出现在 `Delete GitHub prerelease` 之前
- 与发布相同的 snapshot/compare-first、两条 artifact、`mihari-dev-alist`
- 事后再校验 `/releases/latest`

**GitHub delete 不要留设计文档里的注释。** 从 `.github/workflows/retract.yml` 第 122–139 行复制 lookup + HTTP 404 分支，把 stable 字样改成 dev，且 `gh release delete "${VERSION}" --yes` **无** `--cleanup-tag`：

```yaml
      - name: Delete GitHub prerelease
        env:
          GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
        run: |
          release_lookup_error="$(mktemp)"
          trap 'rm -f "${release_lookup_error}"' EXIT
          set +e
          gh api "repos/${GITHUB_REPOSITORY}/releases/tags/${VERSION}" \
            >/dev/null 2>"${release_lookup_error}"
          release_lookup_status=$?
          set -e

          if [ "${release_lookup_status}" -eq 0 ]; then
            gh release delete "${VERSION}" --yes
            echo "Permanently removed GitHub prerelease/assets for ${VERSION}; canonical dev tag retained."
          elif grep -Eq '(^|[^0-9])HTTP 404([^0-9]|$)' "${release_lookup_error}"; then
            echo "GitHub prerelease ${VERSION} not found (already retracted?) — nothing to delete; canonical dev tag retained."
          else
            echo "::error::unable to determine whether the GitHub release exists"
            exit 1
          fi
```

**Step 3: YAML 测试变绿；文档测试保持「P2 不可用」**

Run: `python -m pytest scripts/test_release_workflow.py::test_dev_publish_and_retract_use_independent_alist_lock scripts/test_release_workflow.py::test_dev_alist_mutation_uses_compare_first_exit scripts/test_release_workflow.py::test_dev_retract_peel_expected_sha_is_release_sha_not_job_sha scripts/test_release_workflow.py::test_dev_alist_isolation_artifacts_are_separate_from_writer_backup scripts/test_release_workflow.py::test_dev_retract_resolve_outputs_sha_and_alist_runs_before_github_delete scripts/test_release_workflow.py::test_dev_alist_writers_pin_channel_base_path_and_commit_sha scripts/test_release_workflow.py::test_dev_publish_mutates_alist_after_final_github_verify scripts/test_release_workflow.py::test_dev_release_notes_append_index_url_with_stable_root_downloaders scripts/test_release_workflow.py::test_dev_retract_github_delete_is_idempotent_and_retains_canonical_tag scripts/test_release_workflow.py::test_alist_runtime_dependencies_are_pinned_after_checkout_in_dev_workflows scripts/test_release_workflow.py::test_release_documents_record_verified_dev_github_release_and_unavailable_dev_alist -q`

Expected: 全部 YAML 接线测试 PASS；`test_release_documents_*` 仍 PASS（文档尚未翻转）。

**Step 4: 不要 commit。** 进入 Task 11。

---

### Task 11: 翻转 workflow 文档断言与文档

**Files:**
- Modify: `scripts/test_release_workflow.py`
- Modify: `docs/RELEASE.md`
- Modify: `docs/distribution.md`
- Modify: `CHANGELOG.md`

**Step 1: 只在本 Task 改文档测试**（先改测试，跑红，再改文档）

`test_release_documents_record_verified_dev_github_release_and_unavailable_dev_alist` 目标：

- 两份文档仍含 `v0.9.0-dev.2`
- **必须**出现 `retract-dev.yml`
- 删除对「不写 AList」「当前不创建或操作该目录」的强制
- 改为断言独立 `/mihari-release/mihari-dev`、公开 index URL、`mihari-dev-alist`、稳定入口不变
- 保留「不得把稳定安装入口指向 dev」

**Step 2: 跑 FAIL**

Run: `python -m pytest scripts/test_release_workflow.py::test_release_documents_record_verified_dev_github_release_and_unavailable_dev_alist -q`

Expected: FAIL（文档仍写 P2 不可用）

**Step 3: 改 `docs/RELEASE.md` / `docs/distribution.md` 直到测试绿**

要点见设计「文档更新要点」。CHANGELOG `[Unreleased] Added`：

```
- 为 prerelease 通道增加独立 AList index（`/mihari-release/mihari-dev`）与 `retract-dev` 撤回入口（#126）。
```

不要改 README 快速开始。

**Step 4: 全量安全套件**

Run: `python -m pytest scripts/test_release_policy.py scripts/test_github_release_policy.py scripts/test_release_workflow.py scripts/test_alist_client.py scripts/test_alist_index.py scripts/test_release_alist.py scripts/test_retract_alist.py scripts/test_regenerate_index.py scripts/test_alist_channel_guard.py -q`

Expected: PASS

**Step 5: Commit PR 2（Task 9–11 唯一绿灯 commit）**

```bash
git add .github/workflows/release-dev.yml .github/workflows/retract-dev.yml scripts/test_release_workflow.py docs/RELEASE.md docs/distribution.md CHANGELOG.md
git commit -m "feat: 为 prerelease 通道接入独立 AList index 与 retract-dev"
```

---

### Task 12: 收尾验证

**Step 1:** `gofmt` 未改 Go 文件则跳过。

**Step 2:** `git diff origin/dev -- README.md README.zh-CN.md scripts/install/install-aio-remote.sh scripts/install/install-aio-remote.ps1 .github/workflows/release.yml .github/workflows/retract.yml`

Expected: 空。

**Step 3:** 确认 `release-dev.yml` / `retract-dev.yml` 不含 `mihari-stable-alist`，含 `mihari-dev-alist`。隔离 artifact 路径是 `stable-isolation/**` 不是 `stable/**`。

**Step 4:** 不要在本计划里 dispatch 真实 `release-dev`。首次真实发布需用户授权，且 version 必须是新的 `vX.Y.Z-dev.N`。

---

## Execution notes

- PR 1 = Task 1–8。Task 1–2 是表征钉（可单独绿灯 commit 或并入 Task 4）。Task 3 不 commit。Task 4 是 Fake + `ensure_channel_root` 的绿灯 commit。
- PR 2 = Task 9–11，**禁止**只合发布 YAML 不合 `retract-dev.yml`。Task 9 先写失败 YAML 测试；Task 10 先跑这些测试（retract-dev 仍红）再写 YAML；文档测试只在 Task 11 翻转。三次任务一次 commit。
- 实现时对照 `@docs/superpowers/specs/2026-08-24-prerelease-alist-index-design.md` 的 YAML；本计划不重复整份 workflow。GitHub delete 以 `retract.yml` 122–139 为准，不以设计注释为准。
