"""Generate standalone installers from the single privileged bridge template.

Run this at development/build time. Distributed installers never source it.
"""
import argparse
from pathlib import Path

SCRIPTS = ("install.sh", "install-aio.sh", "install-aio-remote.sh")
BEGIN = "# BEGIN ROOT APPLY"
END = "# END ROOT APPLY"

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--check", action="store_true")
    parser.add_argument("--directory", type=Path, default=Path(__file__).parent)
    args = parser.parse_args()
    template = (args.directory / "root-apply.sh.in").read_text(encoding="utf-8").rstrip("\n")
    if not template.startswith(BEGIN + "\n") or not template.endswith("\n" + END):
        raise ValueError("invalid root apply template boundaries")
    drift = False
    for name in SCRIPTS:
        path = args.directory / name
        current = path.read_text(encoding="utf-8")
        if current.count(BEGIN) != 1 or current.count(END) != 1:
            raise ValueError(f"{name}: invalid root apply block boundaries")
        start = current.index(BEGIN)
        stop = current.index(END, start) + len(END)
        generated = current[:start] + template + current[stop:]
        if current != generated:
            if args.check:
                print(f"{name}: root apply block differs; run generate_root_apply.py")
                drift = True
            else:
                path.write_bytes(generated.encode("utf-8"))
    return 1 if drift else 0

if __name__ == "__main__":
    raise SystemExit(main())
