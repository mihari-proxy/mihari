"""Root apply must trust an AIO bundle from the fixed index or install-trust.

Windows cannot run the POSIX fixture. A skip there is not a pass; run this
file on a POSIX host.
"""
import gzip
import hashlib
import io
import os
from pathlib import Path
import shlex
import subprocess
import tarfile

import pytest

INSTALL = Path(__file__).parent
DEV_INDEX = "https://cloud.xn--30q18ry71c.com/p/public/mihari-release/mihari-dev/index.txt"
MAIN_INDEX = "https://cloud.xn--30q18ry71c.com/p/public/mihari-release/mihari/index.txt"
OFFLINE_HELPER = (
    "Offline installation requires a trusted helper with replacement confirmation support. "
    "Prepare a current Mihari installation before installing this offline candidate."
)
CONFIRMATION = "lacks replacement confirmation support"

pytestmark = pytest.mark.skipif(os.name != "posix", reason="POSIX shell")


def sha256(data):
    return hashlib.sha256(data).hexdigest()


def shell_script(lines):
    return "\n".join(lines) + "\n"


CAPABLE = shell_script([
    "#!/bin/sh",
    'printf "%s\\n" "$0" >> "$MIHARI_ENTRY_LOG"',
    'if [ "$3" = "--help" ]; then',
    '  printf "%s\\n" "--yes" "--expected-preview"',
    "  exit 0",
    "fi",
    "exit 0",
])
INCAPABLE = shell_script([
    "#!/bin/sh",
    'printf "%s\\n" "$0" >> "$MIHARI_ENTRY_LOG"',
    'if [ "$3" = "--help" ]; then',
    '  printf "%s\\n" "--json"',
    "  exit 0",
    "fi",
    "exit 0",
])


def write_bundle(path, script):
    data = script.encode()
    extra = b"alpha\n"
    with path.open("wb") as raw:
        with gzip.GzipFile(filename="", fileobj=raw, mode="wb", mtime=0) as compressed:
            with tarfile.open(fileobj=compressed, mode="w") as archive:
                for name, body, mode in (("mihari", data, 0o700), ("data/bin/core-channel", extra, 0o644)):
                    info = tarfile.TarInfo(name)
                    info.size = len(body)
                    info.mode = mode
                    info.mtime = 0
                    info.uid = 0
                    info.gid = 0
                    archive.addfile(info, io.BytesIO(body))
    return sha256(path.read_bytes())


def index_text(tag, digest, platform="linux-amd64"):
    return f"latest {tag}\n{platform} https://example.invalid/bundle {digest}\n"


def extract_function(source, name):
    marker = name + "() {"
    start = source.find(marker)
    if start < 0:
        pytest.fail(f"missing {name}")
    depth = 0
    quote = ""
    index = source.find("{", start)
    while index < len(source):
        char = source[index]
        if quote:
            if char == "\\" and quote == '"':
                index += 2
                continue
            if char == quote:
                quote = ""
            index += 1
            continue
        if char in "'\"":
            quote = char
            index += 1
            continue
        if char == "{":
            depth += 1
        elif char == "}":
            depth -= 1
            if depth == 0:
                return source[start:index + 1]
        index += 1
    pytest.fail(f"unterminated {name}")


def flow_slice(source):
    start = source.index("entry=/usr/local/lib/mihari/mihari\n")
    end = source.index("# BEGIN REQUEST JSON", start)
    return source[start:end]


def root_block(name):
    text = (INSTALL / name).read_text(encoding="utf-8")
    return text.split("# BEGIN ROOT APPLY\n", 1)[1].split("\n# END ROOT APPLY", 1)[0]


def fake_curl(tmp_path, body, status=200):
    log = tmp_path / "curl.log"
    index = tmp_path / "index-body"
    index.write_bytes(body)
    program = tmp_path / "curl"
    program.write_text(shell_script([
        "#!/bin/sh",
        'printf "%s\\n" "$*" >> ' + shlex.quote(str(log)),
        "out=",
        "header=",
        "prev=",
        "url=",
        'for arg in "$@"; do',
        '  if [ "$prev" = -o ] || [ "$prev" = --output ]; then out=$arg; fi',
        '  if [ "$prev" = --dump-header ]; then header=$arg; fi',
        '  case "$arg" in http://*|https://*) url=$arg ;; esac',
        "  prev=$arg",
        "done",
        'case "$url" in',
        "  " + DEV_INDEX + "|" + MAIN_INDEX + ")",
        '    if [ -n "$header" ]; then printf "HTTP/1.1 ' + str(status) + '\\r\\n" > "$header"; fi',
        '    if [ -z "$out" ] || [ "$out" = - ]; then cat ' + shlex.quote(str(index)) + "; else cat " + shlex.quote(str(index)) + ' > "$out"; fi',
        "    exit 0",
        "    ;;",
        "  *) exit 1 ;;",
        "esac",
    ]))
    program.chmod(0o755)
    return program, log


def run_flow(tmp_path, *, mode, index, script=CAPABLE, channel="dev", tag="v1.2.3-dev.1", install_root=None, index_status=200, bundle_path=None):
    source = (INSTALL / "root-apply.sh.in").read_text(encoding="utf-8")
    archive = tmp_path / "caller-archive.tar.gz"
    digest = write_bundle(archive, script)
    original = tmp_path / "caller-mihari"
    original.write_text(shell_script(["#!/bin/sh", "printf caller >&2", "exit 9"]))
    original.chmod(0o755)
    stage = tmp_path / "stage"
    stage.mkdir(exist_ok=True)
    entry_log = tmp_path / "entry.log"
    check_log = tmp_path / "checksum.log"
    for stale in (entry_log, check_log, tmp_path / "curl.log"):
        stale.unlink(missing_ok=True)
    curl, curl_log = fake_curl(tmp_path, index.encode(), status=index_status)
    functions = "\n".join(extract_function(source, name) for name in ("checksum", "root_fetch", "root_fetch_index"))
    functions = functions.replace("/usr/bin/curl", shlex.quote(str(curl)))
    functions = functions.replace(
        "checksum() {",
        "checksum() {\n  printf '%s\\n' \"$1\" >> " + shlex.quote(str(check_log)),
        1,
    )
    root = str(tmp_path / "install-root") if install_root is None else str(install_root)
    command = "\n".join([
        "set -eu",
        "fail() { printf '%s\\n' \"$1\" >&2; exit 1; }",
        "stage=" + shlex.quote(str(stage)),
        "os=linux",
        "arch=amd64",
        "tag=" + shlex.quote(tag),
        "channel=" + shlex.quote(channel),
        "candidate=" + shlex.quote(str(original)),
        "bundle=" + shlex.quote(str(archive if bundle_path is None else bundle_path)),
        "bootstrap_mode=" + shlex.quote(mode),
        "install_root=" + shlex.quote(root),
        "explicit_yes=0",
        functions,
        "trusted_entry() { return 1; }",
        flow_slice(source),
        "printf 'ENTRY=%s\\nCANDIDATE=%s\\nBUNDLE=%s\\n' \"$entry\" \"$candidate\" \"$bundle\"",
    ])
    result = subprocess.run(
        ["sh", "-c", command],
        env=dict(os.environ, MIHARI_ENTRY_LOG=str(entry_log)),
        capture_output=True, text=True, timeout=20,
    )
    curl_text = curl_log.read_text(encoding="utf-8") if curl_log.exists() else ""
    checks = check_log.read_text(encoding="utf-8") if check_log.exists() else ""
    entries = entry_log.read_text(encoding="utf-8") if entry_log.exists() else ""
    return result, curl_text, checks, entries, archive, digest, stage


def assert_no_github(curl_text):
    assert "api.github.com" not in curl_text
    assert "github.com" not in curl_text


@pytest.mark.parametrize(("channel", "tag", "url"), [
    ("dev", "v1.2.3-dev.1", DEV_INDEX),
    ("main", "v1.2.3", MAIN_INDEX),
])
def test_online_bundle_uses_channel_index_not_github(tmp_path, channel, tag, url):
    archive = tmp_path / "preview.tar.gz"
    digest = write_bundle(archive, CAPABLE)
    result, curl_text, checks, entries, caller, _, stage = run_flow(
        tmp_path, mode="online", index=index_text(tag, digest), channel=channel, tag=tag,
    )
    assert result.returncode == 0, result.stderr
    assert curl_text.count("https://") == 1
    assert url in curl_text
    assert_no_github(curl_text)
    lines = dict(line.split("=", 1) for line in result.stdout.splitlines() if "=" in line)
    staged_mihari = stage / "mihari"
    staged_bundle = stage / "bundle"
    assert lines["ENTRY"] == lines["CANDIDATE"] == str(staged_mihari)
    assert lines["BUNDLE"] == str(staged_bundle)
    assert lines["BUNDLE"] != str(caller)
    assert staged_bundle.read_bytes() == caller.read_bytes()
    assert checks.splitlines()[0] == str(staged_bundle)
    assert str(caller) not in checks
    assert entries.splitlines()
    assert all(line == str(staged_mihari) for line in entries.splitlines())
    assert str(tmp_path / "caller-mihari") not in entries
    assert "caller" not in result.stderr


def test_online_index_checksum_mismatch_does_not_download_again(tmp_path):
    result, curl_text, _, _, _, _, _ = run_flow(
        tmp_path, mode="online", index=index_text("v1.2.3-dev.1", "0" * 64),
    )
    assert result.returncode != 0
    assert "helper release metadata exceeds limit" not in result.stderr
    assert curl_text.count("https://") == 1
    assert DEV_INDEX in curl_text
    assert_no_github(curl_text)


def test_online_index_latest_mismatch_fails(tmp_path):
    archive = tmp_path / "preview.tar.gz"
    digest = write_bundle(archive, CAPABLE)
    result, curl_text, _, _, _, _, _ = run_flow(
        tmp_path, mode="online", index=index_text("v1.2.3-dev.2", digest), tag="v1.2.3-dev.1",
    )
    assert result.returncode != 0
    assert "latest" in result.stderr
    assert "helper release metadata exceeds limit" not in result.stderr
    assert curl_text.count("https://") == 1
    assert_no_github(curl_text)


def test_online_index_size_boundary(tmp_path):
    archive = tmp_path / "preview.tar.gz"
    digest = write_bundle(archive, CAPABLE)
    base = index_text("v1.2.3-dev.1", digest).encode()
    pad = 65536 - len(base)
    exact = base + (b"#\n" * (pad // 2)) + (b"#" * (pad % 2))
    over = exact + b"#"
    assert len(exact) == 65536 and len(over) == 65537
    ok, curl_text, _, _, _, _, _ = run_flow(tmp_path, mode="online", index=exact.decode())
    assert ok.returncode == 0, ok.stderr
    assert curl_text.count("https://") == 1
    bad, bad_curl, _, _, _, _, _ = run_flow(tmp_path, mode="online", index=over.decode())
    assert bad.returncode != 0
    assert "helper release metadata exceeds limit" not in bad.stderr
    assert bad_curl.count("https://") == 1
    assert_no_github(bad_curl)


def test_online_bundle_without_confirmation_flags_does_not_set_helper(tmp_path):
    archive = tmp_path / "preview.tar.gz"
    digest = write_bundle(archive, INCAPABLE)
    result, curl_text, _, _, _, _, _ = run_flow(
        tmp_path, mode="online", index=index_text("v1.2.3-dev.1", digest), script=INCAPABLE,
    )
    assert result.returncode != 0
    assert CONFIRMATION in result.stderr
    assert "helper_url" not in result.stderr
    assert curl_text.count("https://") == 1
    assert_no_github(curl_text)


def test_online_index_redirect_is_not_a_trust_root(tmp_path):
    archive = tmp_path / "preview.tar.gz"
    digest = write_bundle(archive, CAPABLE)
    result, curl_text, _, _, _, _, _ = run_flow(
        tmp_path, mode="online", index=index_text("v1.2.3-dev.1", digest), index_status=302,
    )
    assert result.returncode != 0
    assert "cannot fetch channel index" in result.stderr
    assert "helper release metadata exceeds limit" not in result.stderr
    assert curl_text.count("https://") == 1
    assert "--location" not in curl_text
    assert_no_github(curl_text)


def test_relative_install_root_fails_closed(tmp_path):
    result, curl_text, _, _, _, _, _ = run_flow(
        tmp_path, mode="offline", index="", install_root="relative-root",
    )
    assert result.returncode != 0
    assert "invalid install root" in result.stderr
    assert curl_text == ""


def test_symlink_bundle_is_not_staged(tmp_path):
    archive = tmp_path / "real.tar.gz"
    write_bundle(archive, CAPABLE)
    link = tmp_path / "link.tar.gz"
    link.symlink_to(archive)
    result, curl_text, _, _, _, _, _ = run_flow(
        tmp_path, mode="online", index="", bundle_path=link,
    )
    assert result.returncode != 0
    assert "cannot stage install bundle" in result.stderr
    assert curl_text == ""


def test_oversize_bundle_is_not_staged(tmp_path):
    huge = tmp_path / "huge.tar.gz"
    with huge.open("wb") as raw:
        raw.truncate(1073741824 + 1)
    result, curl_text, _, _, _, _, _ = run_flow(
        tmp_path, mode="online", index="", bundle_path=huge,
    )
    assert result.returncode != 0
    assert "install bundle exceeds limit" in result.stderr
    assert curl_text == ""


def test_offline_without_manifest_does_not_call_curl(tmp_path):
    result, curl_text, _, _, _, _, _ = run_flow(tmp_path, mode="offline", index="")
    assert result.returncode != 0
    assert curl_text == ""
    assert OFFLINE_HELPER in result.stderr
    assert_no_github(result.stderr)


def test_offline_user_owned_manifest_does_not_authorize_bundle(tmp_path):
    archive = tmp_path / "preview.tar.gz"
    digest = write_bundle(archive, CAPABLE)
    root = tmp_path / "owned-root"
    trust = root / "install-trust"
    trust.mkdir(parents=True)
    (trust / "manifest.json").write_text(
        '{"binaries":[],"bundles":["' + digest + '"]}\n', encoding="utf-8",
    )
    result, curl_text, _, _, _, _, _ = run_flow(
        tmp_path, mode="offline", index="", install_root=root,
    )
    assert result.returncode != 0
    assert curl_text == ""
    assert OFFLINE_HELPER in result.stderr


def test_manifest_pin_accepts_only_lowercase_bundle_or_binary_digests(tmp_path):
    source = (INSTALL / "root-apply.sh.in").read_text(encoding="utf-8")
    function = extract_function(source, "manifest_pins_digest")
    archive = "a" * 64
    binary = "b" * 64
    other = "c" * 64

    def pins(text, archive_sha=archive, binary_sha=binary):
        path = tmp_path / "manifest.json"
        path.write_bytes(text.encode())
        command = "\n".join([
            "set -eu",
            function,
            "status=0",
            "manifest_pins_digest " + shlex.quote(str(path)) + " " + shlex.quote(archive_sha) + " " + shlex.quote(binary_sha) + " || status=$?",
            "printf '%s\\n' \"$status\"",
        ])
        result = subprocess.run(["sh", "-c", command], capture_output=True, text=True, timeout=20)
        assert result.returncode == 0, result.stderr
        return int(result.stdout.strip())

    assert pins('{"binaries":[],"bundles":["' + archive + '"]}') == 0
    assert pins('{"binaries":["' + binary + '"],"bundles":[]}') == 0
    assert pins('{\n  "bundles": [\n    "' + archive + '"\n  ],\n  "binaries": []\n}\n') == 0
    assert pins('{"core":["' + archive + '"],"binaries":[],"bundles":[]}') == 1
    assert pins('{"geo":["' + binary + '"],"binaries":[],"bundles":[]}') == 1
    assert pins('{"panels":{"zashboard/1":"' + archive + '"},"binaries":[],"bundles":[]}') == 1
    assert pins('{"binaries":["' + archive.upper() + '"],"bundles":[]}', binary_sha=archive) == 1
    assert pins('{"bundles":["' + archive[:-1] + '"]}') == 1
    assert pins('{"binaries":["' + other + '"],"bundles":["' + other + '"]}') == 1
    body = '{"binaries":[],"bundles":["' + archive + '"]}'
    exact = body + "\n" + (" " * (1048576 - len(body) - 1))
    over = exact + " "
    assert len(exact.encode()) == 1048576
    assert len(over.encode()) == 1048577
    assert pins(exact) == 0
    assert pins(over) == 1


def test_offline_manifest_reuses_trusted_entry_loop():
    source = (INSTALL / "root-apply.sh.in").read_text(encoding="utf-8")
    assert source.count('while [ "$entry_path" != / ]') == 1
    assert 'root_path_trusted "$manifest"' in source
    assert "${install_root:-/usr/local/lib/mihari}/install-trust/manifest.json" in source
    assert "root_path_trusted" in extract_function(source, "trusted_entry")
    loop = extract_function(source, "root_path_trusted")
    assert 'while [ "$entry_path" != / ]' in loop
    assert '[ "$owner" = 0 ]' in loop
    assert '*) return 1 ;;' in loop
    pin = source.index('manifest_pins_digest "$manifest" "$archive_sha" ""')
    extract = source.index('stage_bundle_mihari "$stage/bundle" "$stage/mihari"')
    assert pin < extract
    assert "manifest_pins_digest" in source


def test_generated_install_sh_keeps_github_helper_after_empty_bundle():
    text = (INSTALL / "install.sh").read_text(encoding="utf-8")
    root = root_block("install.sh")
    assert "per_page=100" in text
    assert '[ -z "${bundle:-}" ]' in root
    empty = root.index('[ -z "${bundle:-}" ]')
    assert root.index("per_page=100", empty) > empty


def test_generated_remote_root_indexes_bundle_before_release_list():
    root = root_block("install-aio-remote.sh")
    assert DEV_INDEX in root
    assert MAIN_INDEX in root
    bundle = root.index('[ -n "${bundle:-}" ]')
    empty = root.index('[ -z "${bundle:-}" ]')
    release_list = root.index("releases?per_page=100")
    assert bundle < empty < release_list
