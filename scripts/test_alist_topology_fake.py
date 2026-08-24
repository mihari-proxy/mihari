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
        try:
            return any(entry["name"] == name for entry in self.list_dir(parent or "/"))
        except AListError:
            return False

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
