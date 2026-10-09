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
