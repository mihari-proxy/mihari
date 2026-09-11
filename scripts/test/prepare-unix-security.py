#!/usr/bin/env python3
"""Unprivileged native package compilation and immutable artifact inventory."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
from unix_security import PREFIX, COMMON, SUPPLEMENTAL


def main():
    parser=argparse.ArgumentParser()
    parser.add_argument("--out",required=True)
    args=parser.parse_args()
    if hasattr(os,"geteuid") and os.geteuid()==0:
        raise SystemExit("precache must run without root")
    output=Path(args.out).resolve()
    output.mkdir(mode=0o700)
    environment=os.environ.copy()
    environment.update(GOTOOLCHAIN="local",GOENV="off",CGO_ENABLED="0")
    go=Path(subprocess.check_output(["which","go"],text=True).strip()).resolve()
    subprocess.run([str(go),"mod","download"],env=environment,check=True)
    version=subprocess.check_output([str(go),"version"],env=environment,text=True).strip()
    if "go1.26.5" not in version:
        raise SystemExit("unexpected pinned Go toolchain")
    manifest={"schema":"mihari.unix-security-binaries/v1","toolchain":version,"revision":subprocess.check_output(["git","rev-parse","HEAD"],text=True).strip(),"os":sys.platform,"arch":subprocess.check_output([str(go),"env","GOARCH"],env=environment,text=True).strip(),"tags":"unix_security","binaries":[],"tools":{}}
    for package in sorted(set(COMMON)|set(SUPPLEMENTAL)):
        name=package.replace("/","-")+".test"
        subprocess.run([str(go),"test","-c","-tags=unix_security","-o",str(output/name),"./"+package],env=environment,check=True)
        data=(output/name).read_bytes()
        manifest["binaries"].append({"package":PREFIX+package,"name":name,"size":len(data),"sha256":hashlib.sha256(data).hexdigest()})
    for key,path in {"go":go,"python":Path(sys.executable).resolve()}.items():
        data=path.read_bytes()
        manifest["tools"][key]={"path":str(path),"size":len(data),"sha256":hashlib.sha256(data).hexdigest()}
    (output/"manifest.json").write_text(json.dumps(manifest,sort_keys=True)+"\n")
    if "GITHUB_OUTPUT" in os.environ:
        with open(os.environ["GITHUB_OUTPUT"],"a") as stream:
            stream.write(f"go={go}\npython={Path(sys.executable).resolve()}\n")


if __name__=="__main__":
    main()
