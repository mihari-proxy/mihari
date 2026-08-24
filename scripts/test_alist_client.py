"""Regression tests for the alist_client shared constants/helpers.

Guards the PLATFORMS shape that release-alist.py and retract-alist.py consume
via `for goos, goarch in PLATFORMS`. The first v0.2.0 release crashed at
release-alist.py:47 (ValueError: too many values to unpack) because PLATFORMS
was a list of "goos/goarch" strings — unpacking an 11-char string into two
names yields too many values. These tests pin PLATFORMS to (goos, goarch)
pairs so neither the publish nor the retract flow can regress.
"""
import importlib.util
from pathlib import Path

import pytest
import requests

import alist_client
from alist_client import AList, PLATFORMS, bundle_name, connect


class JSONResponse:
    def __init__(self, body, status_error=None):
        self.body = body
        self.status_error = status_error

    def raise_for_status(self):
        if self.status_error is not None:
            raise self.status_error

    def json(self):
        return self.body


class PostSession:
    def __init__(self, response):
        self.response = response

    def post(self, *_args, **_kwargs):
        return self.response


def list_response(content, total, page, per_page=200, has_more=False, pages_total=1):
    return JSONResponse(
        {
            "code": 200,
            "data": {
                "content": content,
                "total": total,
                "page": page,
                "per_page": per_page,
                "has_more": has_more,
                "pages_total": pages_total,
            },
        }
    )


class PagedSession:
    def __init__(self, responses):
        self.responses = list(responses)
        self.calls = []

    def post(self, url, **kwargs):
        self.calls.append((url, kwargs["json"]["page"], kwargs["json"]["per_page"]))
        return self.responses.pop(0)


def test_read_bytes_closes_response_after_success():
    alist = AList.__new__(AList)

    class Response:
        closed = False

        def raise_for_status(self):
            pass

        def iter_content(self, size):
            assert size == 1024 * 1024
            yield b"abcdef"

        def close(self):
            self.closed = True

    response = Response()

    class Session:
        def get(self, url, timeout, stream):
            assert timeout == 7
            assert stream is True
            return response

    alist.session = Session()
    alist.public_url = lambda path: "https://example.invalid" + path
    assert alist.read_bytes("/x", max_bytes=6, timeout=7) == b"abcdef"
    assert response.closed is True


def test_read_bytes_closes_response_when_status_is_error():
    alist = AList.__new__(AList)

    class Response:
        closed = False

        def raise_for_status(self):
            raise requests.HTTPError("remote failure")

        def close(self):
            self.closed = True

    response = Response()
    alist.session = type("Session", (), {"get": lambda *_args, **_kwargs: response})()
    alist.public_url = lambda path: "https://example.invalid" + path

    with pytest.raises(alist_client.AListError) as error:
        alist.read_bytes("/x")
    assert str(error.value) == "alist read failed"
    assert response.closed is True


def test_read_bytes_sanitizes_request_start_failure():
    alist = AList.__new__(AList)

    class Session:
        def get(self, *_args, **_kwargs):
            raise requests.Timeout(
                "https://cloud.invalid/file?token=read-secret response-body"
            )

    alist.session = Session()
    alist.public_url = lambda path: "https://example.invalid" + path

    with pytest.raises(alist_client.AListError) as error:
        alist.read_bytes("/x")

    assert str(error.value) == "alist read failed"
    assert "read-secret" not in str(error.value)
    assert "response-body" not in str(error.value)


def test_read_bytes_closes_response_when_object_exceeds_limit():
    alist = AList.__new__(AList)

    class Response:
        closed = False

        def raise_for_status(self):
            pass

        def iter_content(self, _size):
            yield b"abcdef"

        def close(self):
            self.closed = True

    response = Response()
    alist.session = type("Session", (), {"get": lambda *_args, **_kwargs: response})()
    alist.public_url = lambda path: "https://example.invalid" + path

    with pytest.raises(ValueError):
        alist.read_bytes("/x", max_bytes=5, timeout=7)
    assert response.closed is True


def test_content_uses_text_limit_and_strict_utf8():
    alist = AList.__new__(AList)
    captured = {}
    alist.exists = lambda _path: True

    def read_bytes(path, max_bytes):
        captured["path"] = path
        captured["max_bytes"] = max_bytes
        return b"latest v1.2.3\n"

    alist.read_bytes = read_bytes
    assert alist.content("/index.txt") == "latest v1.2.3\n"
    assert captured == {"path": "/index.txt", "max_bytes": alist_client.MAX_TEXT_BYTES}


def test_content_rejects_invalid_utf8_without_body_leak():
    alist = AList.__new__(AList)
    alist.exists = lambda _path: True
    alist.read_bytes = lambda _path, max_bytes: b"secret-index-body\xff"

    with pytest.raises(UnicodeDecodeError) as error:
        alist.content("/index.txt")
    assert "secret-index-body" not in str(error.value)


def test_exists_and_content_use_parent_listing_when_fs_get_reports_missing_as_500():
    alist = AList.__new__(AList)
    alist.base = "https://cloud.invalid"

    class Session:
        def __init__(self):
            self.paths = []

        def post(self, url, **_kwargs):
            self.paths.append(url)
            if url.endswith("/api/fs/get"):
                return JSONResponse({"code": 500, "message": "object not found"})
            return list_response([], total=0, page=1, pages_total=0)

        def get(self, *_args, **_kwargs):
            raise AssertionError("public download must not run for an absent object")

    alist.session = Session()

    path = "/mihari-release/mihari/index.txt"
    assert alist.exists(path) is False
    assert alist.content(path) is None
    assert all(not url.endswith("/api/fs/get") for url in alist.session.paths)


def test_list_dir_reads_every_page_and_preserves_second_page_entries():
    first = [{"name": f"v1.0.{index}", "is_dir": True} for index in range(200)]
    highest = {"name": "v9.0.0", "is_dir": True}
    alist = AList.__new__(AList)
    alist.base = "https://cloud.invalid"
    alist.session = PagedSession(
        [
            list_response(first, 201, 1, has_more=True, pages_total=2),
            list_response([highest], 201, 2, has_more=False, pages_total=2),
        ]
    )

    entries = alist.list_dir("/mihari-release/mihari")

    assert len(entries) == 201
    assert entries[-1] == highest
    assert [(page, per_page) for _, page, per_page in alist.session.calls] == [
        (1, 200),
        (2, 200),
    ]


@pytest.mark.parametrize(
    "responses",
    [
        [list_response([], 1, 1, has_more=False, pages_total=1)],
        [list_response([], 0, 2, has_more=False, pages_total=0)],
        [list_response([], 0, 1, per_page=30, has_more=False, pages_total=0)],
        [list_response([], 201, 1, has_more=False, pages_total=2)],
        [
            list_response(
                [{"name": f"entry-{index}", "is_dir": False} for index in range(199)],
                201,
                1,
                has_more=True,
                pages_total=2,
            ),
            list_response(
                [
                    {"name": "entry-199", "is_dir": False},
                    {"name": "entry-200", "is_dir": False},
                ],
                201,
                2,
                has_more=False,
                pages_total=2,
            ),
        ],
        [
            list_response(
                [{"name": f"entry-{index}", "is_dir": False} for index in range(200)],
                201,
                1,
                has_more=True,
                pages_total=2,
            ),
            list_response(
                [{"name": "entry-0", "is_dir": False}],
                201,
                2,
                has_more=False,
                pages_total=2,
            ),
        ],
    ],
)
def test_list_dir_rejects_pagination_ambiguity(responses):
    alist = AList.__new__(AList)
    alist.base = "https://cloud.invalid"
    alist.session = PagedSession(responses)

    with pytest.raises(alist_client.AListError, match="invalid directory listing"):
        alist.list_dir("/mihari-release/mihari")


@pytest.mark.parametrize("code", [401, 403, 500])
def test_exists_rejects_non_success_body_without_leaking_remote_details(code):
    alist = AList.__new__(AList)
    alist.base = "https://cloud.invalid"
    alist.session = PostSession(
        JSONResponse({"code": code, "message": "https://cloud.invalid/?token=secret response-body"})
    )

    with pytest.raises(RuntimeError) as error:
        alist.exists("/mihari-release/mihari/index.txt")

    assert type(error.value).__name__ == "AListError"
    assert "secret" not in str(error.value)
    assert "response-body" not in str(error.value)


@pytest.mark.parametrize(
    "operation",
    [
        lambda alist: alist.mkdir("/mihari-release/mihari/v1.2.3"),
        lambda alist: alist.remove("/mihari-release/mihari", ["v1.2.3"]),
    ],
)
def test_metadata_mutations_reject_non_success_body_as_normal_exception(operation):
    alist = AList.__new__(AList)
    alist.base = "https://cloud.invalid"
    alist.session = PostSession(JSONResponse({"code": 500, "message": "sensitive backend detail"}))

    with pytest.raises(RuntimeError, match="alist operation failed") as error:
        operation(alist)
    assert type(error.value).__name__ == "AListError"


def test_upload_write_failure_is_a_retryable_normal_exception():
    alist = AList.__new__(AList)
    response = JSONResponse({"code": 500, "message": "sensitive backend detail"})

    with pytest.raises(RuntimeError, match="alist write failed") as error:
        alist._check_write(response, "/mihari-release/mihari/index.txt")
    assert type(error.value).__name__ == "AListError"


def test_binary_upload_transport_error_is_typed_and_sanitized(tmp_path):
    local = tmp_path / "bundle.tar.gz"
    local.write_bytes(b"release bytes")
    alist = AList.__new__(AList)
    alist.base = "https://cloud.invalid"

    class Session:
        def put(self, *_args, **_kwargs):
            raise requests.ConnectionError(
                "https://cloud.invalid/upload?token=binary-secret response-body"
            )

    alist.session = Session()

    with pytest.raises(alist_client.AListError) as error:
        alist.upload(local, "/mihari-release/mihari/v1.2.3/bundle.tar.gz")

    assert str(error.value) == "alist write failed"
    assert "binary-secret" not in str(error.value)
    assert "response-body" not in str(error.value)


def test_text_upload_transport_error_is_typed_and_sanitized():
    alist = AList.__new__(AList)
    alist.base = "https://cloud.invalid"

    class Session:
        def put(self, *_args, **_kwargs):
            raise requests.Timeout(
                "https://cloud.invalid/upload?token=text-secret response-body"
            )

    alist.session = Session()

    with pytest.raises(alist_client.AListError) as error:
        alist.upload_text(
            "latest v1.2.3\n", "/mihari-release/mihari/index.txt"
        )

    assert str(error.value) == "alist write failed"
    assert "text-secret" not in str(error.value)
    assert "response-body" not in str(error.value)


@pytest.mark.parametrize(
    "body",
    [
        {"code": 200},
        {"code": 200, "data": None},
        {"code": 200, "data": {}},
        {"code": 200, "data": {"content": "not-a-list"}},
    ],
)
def test_list_dir_rejects_malformed_success_payload(body):
    alist = AList.__new__(AList)
    alist.base = "https://cloud.invalid"
    alist.session = PostSession(JSONResponse(body))

    with pytest.raises(RuntimeError, match="invalid directory listing") as error:
        alist.list_dir("/mihari-release/mihari")
    assert type(error.value).__name__ == "AListError"


def test_list_dir_accepts_explicit_null_as_an_empty_directory():
    alist = AList.__new__(AList)
    alist.base = "https://cloud.invalid"
    alist.session = PostSession(
        list_response(None, total=0, page=1, pages_total=0)
    )

    assert alist.list_dir("/mihari-release/mihari") == []


def current_alist_list_response(content, total):
    return JSONResponse(
        {
            "code": 200,
            "data": {
                "content": content,
                "header": None,
                "provider": "local",
                "readme": "",
                "total": total,
                "write": True,
            },
        }
    )


def test_list_dir_accepts_current_alist_payload_without_pagination_fields():
    alist = AList.__new__(AList)
    alist.base = "https://cloud.invalid"
    alist.session = PostSession(
        current_alist_list_response(
            [
                {"name": "mihari", "is_dir": True},
            ],
            1,
        )
    )

    entries = alist.list_dir("/")

    assert [(entry["name"], entry["is_dir"]) for entry in entries] == [("mihari", True)]


def test_list_dir_rejects_unpaged_payload_when_total_does_not_match_content():
    alist = AList.__new__(AList)
    alist.base = "https://cloud.invalid"
    alist.session = PostSession(
        current_alist_list_response(
            [{"name": "mihari", "is_dir": True}],
            2,
        )
    )

    with pytest.raises(alist_client.AListError, match="invalid directory listing"):
        alist.list_dir("/")


def test_connect_translates_login_failure_at_command_boundary(monkeypatch, capsys):
    monkeypatch.setenv("ALIST_URL", "https://cloud.invalid")
    monkeypatch.setenv("ALIST_USERNAME", "release-user")
    monkeypatch.setenv("ALIST_PASSWORD", "not-logged")

    def fail_login(*_args):
        raise alist_client.AListError(
            "https://cloud.invalid/login?token=login-secret response-body"
        )

    monkeypatch.setattr(alist_client, "AList", fail_login)

    with pytest.raises(SystemExit):
        connect()

    captured = capsys.readouterr()
    assert "login-secret" not in captured.err
    assert "response-body" not in captured.err


def test_platforms_unpack_as_goos_goarch_pairs():
    # The exact unpacking the publish/retract loops perform — must not raise.
    names = [bundle_name(goos, goarch) for goos, goarch in PLATFORMS]
    assert len(names) == len(PLATFORMS)


def test_platforms_are_two_tuples_not_strings():
    # The bug: a "linux/amd64" string unpacks into >2 values. Reject that shape.
    for entry in PLATFORMS:
        assert not isinstance(entry, str), f"PLATFORMS entry must be a pair, got string {entry!r}"
        goos, goarch = entry
        assert isinstance(goos, str) and isinstance(goarch, str)


def test_platforms_cover_six_targets():
    assert len(PLATFORMS) == 6


def test_bundle_name_formats():
    assert bundle_name("linux", "amd64") == "mihari-all-in-one-linux-amd64.tar.gz"
    assert bundle_name("darwin", "arm64") == "mihari-all-in-one-darwin-arm64.tar.gz"
    assert bundle_name("windows", "arm64") == "mihari-all-in-one-windows-arm64.zip"


def test_public_url_is_signless_proxy_route():
    # AList.__init__ would hit the network (_login); bypass it and set only the
    # attribute public_url reads.
    alist = AList.__new__(AList)
    alist.base = "https://cloud.example.com"
    # public_url injects "/public" between /p and the fs path (AList topology quirk).
    assert alist.public_url("/mihari-release/mihari/index.txt") == "https://cloud.example.com/p/public/mihari-release/mihari/index.txt"
    bundle = alist.public_url("/mihari-release/mihari/v0.3.0/mihari-all-in-one-linux-amd64.tar.gz")
    assert "?sign=" not in bundle
    assert bundle == "https://cloud.example.com/p/public/mihari-release/mihari/v0.3.0/mihari-all-in-one-linux-amd64.tar.gz"


def test_fs_path_strips_mount_segment():
    # AList fs-path quirk: EVERY fs API op (get/list/put/mkdir/remove) resolves
    # its path relative to the storage root (/mihari-release), prepending it
    # again — so /mihari-release/mihari/X must be passed as /mihari/X or it
    # reads/writes the doubled location. _fs_path drops the first segment for
    # ALL fs ops (read and write) so they agree with /p/public downloads.
    alist = AList.__new__(AList)
    assert alist._fs_path("/mihari-release/mihari/v0.3.0/mihari-all-in-one-linux-amd64.tar.gz") == "/mihari/v0.3.0/mihari-all-in-one-linux-amd64.tar.gz"
    assert alist._fs_path("/mihari-release/mihari/index.txt") == "/mihari/index.txt"
    # fs/remove's `dir` / fs:list of base_path is the bare base_path.
    assert alist._fs_path("/mihari-release/mihari") == "/mihari"
    # No leading segment to strip → returned unchanged (no crash on odd shapes).
    assert alist._fs_path("/mihari-release") == "/mihari-release"
    assert alist._fs_path("/mihari-release/mihari-dev") == "/mihari-dev"
    assert alist._fs_path("/mihari-release/mihari-dev/index.txt") == "/mihari-dev/index.txt"
    assert alist._fs_path("/") == "/"


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


def _load_release_alist():
    # release-alist.py has a hyphen in its name, so import it manually.
    path = Path(__file__).with_name("release-alist.py")
    spec = importlib.util.spec_from_file_location("release_alist", path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def test_build_index_emits_public_urls_no_sign():
    release = _load_release_alist()

    class FakeAList:
        base = "https://cloud.example.com"

        def exists(self, path):
            return True

        def public_url(self, path):
            return f"{self.base}/p/public{path}"

    # Stub sha256_file so build_index needs no real bundle files on disk.
    release.sha256_file = lambda _path: "deadbeef" * 8

    body, index_url = release.build_index(FakeAList(), "dist", "/mihari-release/mihari", "v0.3.0")
    lines = body.splitlines()
    assert lines[0] == "latest v0.3.0"
    assert len(lines) == 1 + len(PLATFORMS)
    for line in lines[1:]:
        platform, url, digest = line.split()
        assert url.startswith("https://cloud.example.com/p/public/mihari-release/mihari/v0.3.0/")
        assert "?sign=" not in url
        assert digest == "deadbeef" * 8
    assert index_url == "https://cloud.example.com/p/public/mihari-release/mihari/index.txt"


def test_install_scripts_hardcode_public_index_url():
    install_dir = Path(__file__).resolve().parent / "install"
    for name in ("install-aio-remote.sh", "install-aio-remote.ps1"):
        text = (install_dir / name).read_text(encoding="utf-8")
        assert "__MIHARI_INDEX_URL__" not in text, f"{name} still has the CI placeholder"
        assert "https://cloud.xn--30q18ry71c.com/p/public/mihari-release/mihari/index.txt" in text, (
            f"{name} lacks the fixed public index URL"
        )
