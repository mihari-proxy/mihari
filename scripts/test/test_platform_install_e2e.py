import hashlib
import io
import json
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
