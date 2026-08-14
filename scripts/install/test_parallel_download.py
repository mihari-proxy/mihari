import glob
import os
from pathlib import Path
import re
import select
import socket
import subprocess
import tempfile
import threading
import time
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


SCRIPT_DIR = Path(__file__).resolve().parent
PAYLOAD = bytes(range(256)) * 8192


def downloader_timeout(os_name=os.name):
    # First powershell.exe + Add-Type + 4 runspaces on a cold windows-latest
    # runner exceeded the previous 30s budget (CI run 31770214283). Later
    # tests on the same job finished in 1-9s. Unix shells start quickly.
    if os_name == "nt":
        return 90
    return 30


class RangeServer(ThreadingHTTPServer):
    daemon_threads = True

    def __init__(self):
        super().__init__(("127.0.0.1", 0), RangeHandler)
        self.active = 0
        self.max_active = 0
        self.range_headers = []
        self.lock = threading.Lock()

    def reset(self):
        with self.lock:
            self.active = 0
            self.max_active = 0
            self.range_headers = []

    def handle_error(self, _request, _client_address):
        # Range probes intentionally dispose a one-byte response without
        # consuming the body; Windows may reset that keep-alive connection.
        pass


class RangeHandler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, _format, *args):
        pass

    def do_GET(self):
        range_header = self.headers.get("Range")
        with self.server.lock:
            self.server.range_headers.append(range_header)

        if self.path == "/ignore" or not range_header:
            self.send_response(200)
            self.send_header("Content-Length", str(len(PAYLOAD)))
            self.end_headers()
            self.wfile.write(PAYLOAD)
            return

        match = re.fullmatch(r"bytes=(\d+)-(\d+)", range_header)
        if not match:
            self.send_error(416)
            return
        start, end = (int(value) for value in match.groups())
        if start < 0 or end < start or end >= len(PAYLOAD):
            self.send_error(416)
            return

        if self.path == "/slow-probe" and start == 0 and end == 0:
            with self.server.lock:
                self.server.active += 1
                self.server.max_active = max(self.server.max_active, self.server.active)
            try:
                deadline = time.monotonic() + 10
                while time.monotonic() < deadline:
                    readable, _, _ = select.select([self.connection], [], [], 0.05)
                    if readable and not self.connection.recv(1, socket.MSG_PEEK):
                        return
                self.send_response(206)
                self.send_header("Content-Range", f"bytes 0-0/{len(PAYLOAD)}")
                self.send_header("Content-Length", "1")
                self.end_headers()
                self.wfile.write(PAYLOAD[:1])
            except (BrokenPipeError, ConnectionResetError):
                pass
            finally:
                with self.server.lock:
                    self.server.active -= 1
            return

        if self.path == "/bad" and start > 0:
            self.send_response(206)
            self.send_header("Content-Range", f"bytes {start}-{end}/{len(PAYLOAD)}")
            self.send_header("Content-Length", str(end - start))
            self.end_headers()
            self.wfile.write(PAYLOAD[start:end])
            return

        chunk = PAYLOAD[start : end + 1]
        with self.server.lock:
            self.server.active += 1
            self.server.max_active = max(self.server.max_active, self.server.active)
        try:
            self.send_response(206)
            header_name = "CONTENT-RANGE" if self.path == "/uppercase" else "Content-Range"
            self.send_header(header_name, f"bytes {start}-{end}/{len(PAYLOAD)}")
            self.send_header("Content-Length", str(len(chunk)))
            self.end_headers()
            if end > start:
                time.sleep(2 if self.path == "/slow" else 0.2)
            try:
                self.wfile.write(chunk)
            except (BrokenPipeError, ConnectionResetError):
                pass
        finally:
            with self.server.lock:
                self.server.active -= 1


class ParallelDownloadTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.server = RangeServer()
        cls.server_thread = threading.Thread(target=cls.server.serve_forever, daemon=True)
        cls.server_thread.start()
        cls.warmup_windows_downloader()

    @classmethod
    def warmup_windows_downloader(cls):
        if os.name != "nt":
            return
        script = SCRIPT_DIR / "install-aio-remote.ps1"
        command = (
            "$ErrorActionPreference='Stop'; "
            "$env:MIHARI_INSTALL_TEST_MODE='1'; "
            f". ([scriptblock]::Create([IO.File]::ReadAllText('{script}'))); "
            "Add-Type -AssemblyName System.Net.Http"
        )
        subprocess.run(
            ["powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", command],
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            errors="replace",
            timeout=60,
            check=False,
        )

    @classmethod
    def tearDownClass(cls):
        cls.server.shutdown()
        cls.server.server_close()
        cls.server_thread.join(timeout=5)

    def setUp(self):
        self.server.reset()
        self.temp_dir = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp_dir.cleanup)

    def downloader_command(self, url, destination):
        force_shell = os.environ.get("MIHARI_TEST_UNIX_SHELL") == "1"
        if os.name == "nt" and not force_shell:
            script = SCRIPT_DIR / "install-aio-remote.ps1"
            command = (
                "$ErrorActionPreference='Stop'; "
                "$env:MIHARI_INSTALL_TEST_MODE='1'; "
                f". ([scriptblock]::Create([IO.File]::ReadAllText('{script}'))); "
                f"Download-FileWithProgress -url '{url}' -dest '{destination}'"
            )
            return ["powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", command]

        script = (SCRIPT_DIR / "install-aio-remote.sh").as_posix()
        destination = Path(destination).as_posix()
        command = (
            "set -eu; MIHARI_INSTALL_TEST_MODE=1; export MIHARI_INSTALL_TEST_MODE; "
            f". '{script}'; download_file_with_progress '{url}' '{destination}'"
        )
        shell = os.environ.get("MIHARI_TEST_SHELL", "sh")
        return [shell, "-c", command]

    def run_downloader(self, endpoint, destination):
        url = f"http://127.0.0.1:{self.server.server_port}/{endpoint}"
        return subprocess.run(
            self.downloader_command(url, destination),
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            errors="replace",
            timeout=downloader_timeout(),
        )

    def test_windows_downloader_timeout_covers_cold_powershell_startup(self):
        self.assertGreaterEqual(downloader_timeout("nt"), 90)
        self.assertEqual(30, downloader_timeout("posix"))

    def assert_no_parts(self, destination):
        self.assertEqual([], glob.glob(str(destination) + ".parts-*"))

    def assert_no_download_artifacts(self, destination):
        self.assert_no_parts(destination)
        self.assertEqual([], glob.glob(str(destination) + ".probe-*"))

    def test_range_download_uses_four_concurrent_segments(self):
        destination = Path(self.temp_dir.name) / "bundle.bin"

        result = self.run_downloader("range", destination)

        self.assertEqual(0, result.returncode, result.stderr)
        self.assertEqual(PAYLOAD, destination.read_bytes())
        segment_requests = [header for header in self.server.range_headers if header != "bytes=0-0"]
        self.assertEqual(4, len(segment_requests))
        self.assertGreaterEqual(self.server.max_active, 2)
        self.assert_no_parts(destination)

    def test_server_without_range_support_falls_back_to_single_stream(self):
        destination = Path(self.temp_dir.name) / "bundle.bin"

        result = self.run_downloader("ignore", destination)

        self.assertEqual(0, result.returncode, result.stderr)
        self.assertEqual(PAYLOAD, destination.read_bytes())
        self.assertEqual(["bytes=0-0", None], self.server.range_headers)
        self.assert_no_parts(destination)

    def test_content_range_header_name_is_case_insensitive(self):
        destination = Path(self.temp_dir.name) / "bundle.bin"

        result = self.run_downloader("uppercase", destination)

        self.assertEqual(0, result.returncode, result.stderr)
        self.assertEqual(PAYLOAD, destination.read_bytes())
        segment_requests = [header for header in self.server.range_headers if header != "bytes=0-0"]
        self.assertEqual(4, len(segment_requests))

    @unittest.skipIf(os.name == "nt", "POSIX signal semantics are tested on Linux and macOS CI")
    def test_unix_cancellation_stops_segments_and_removes_files(self):
        destination = Path(self.temp_dir.name) / "bundle.bin"
        url = f"http://127.0.0.1:{self.server.server_port}/slow"
        process = subprocess.Popen(
            self.downloader_command(url, destination),
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            errors="replace",
        )
        deadline = time.monotonic() + 10
        while time.monotonic() < deadline:
            with self.server.lock:
                active = self.server.active
            if active >= 2:
                break
            time.sleep(0.05)
        else:
            process.kill()
            self.fail("parallel segment requests did not start")

        process.terminate()
        process.communicate(timeout=10)
        deadline = time.monotonic() + 3
        while time.monotonic() < deadline:
            with self.server.lock:
                active = self.server.active
            if active == 0:
                break
            time.sleep(0.05)

        self.assertEqual(0, active, "segment requests remained active after cancellation")
        self.assertFalse(destination.exists())
        self.assert_no_download_artifacts(destination)

    @unittest.skipIf(os.name == "nt", "POSIX signal semantics are tested on Linux and macOS CI")
    def test_unix_cancellation_stops_range_probe_and_removes_files(self):
        destination = Path(self.temp_dir.name) / "bundle.bin"
        url = f"http://127.0.0.1:{self.server.server_port}/slow-probe"
        process = subprocess.Popen(
            self.downloader_command(url, destination),
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            errors="replace",
        )
        deadline = time.monotonic() + 10
        while time.monotonic() < deadline:
            with self.server.lock:
                active = self.server.active
            if active == 1:
                break
            time.sleep(0.05)
        else:
            process.kill()
            self.fail("range probe did not start")

        process.terminate()
        try:
            process.communicate(timeout=3)
        except subprocess.TimeoutExpired:
            process.kill()
            process.communicate(timeout=3)
            self.fail("downloader did not stop while the range probe was active")
        deadline = time.monotonic() + 3
        while time.monotonic() < deadline:
            with self.server.lock:
                active = self.server.active
            if active == 0:
                break
            time.sleep(0.05)

        self.assertEqual(0, active, "range probe remained active after cancellation")
        self.assertFalse(destination.exists())
        self.assert_no_download_artifacts(destination)

    def test_invalid_segment_length_fails_without_leaving_files(self):
        destination = Path(self.temp_dir.name) / "bundle.bin"

        result = self.run_downloader("bad", destination)

        self.assertNotEqual(0, result.returncode)
        self.assertFalse(destination.exists())
        self.assert_no_parts(destination)


if __name__ == "__main__":
    unittest.main()
