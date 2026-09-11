#!/usr/bin/env python3
"""Render only sanitized native evidence; raw events remain root-private."""
import argparse
import json
import os
import sys
from pathlib import Path
from unix_security import verify


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--events", required=True)
    parser.add_argument("--root", required=True)
    parser.add_argument("--uid-a", required=True, type=int)
    parser.add_argument("--uid-b", required=True, type=int)
    parser.add_argument("--out", required=True)
    parser.add_argument("--go-status", required=True, type=int)
    args = parser.parse_args()
    from unix_security_host import load_run, atomic_json
    run = load_run(args.root, str(Path(args.out).parent), [args.uid_a, args.uid_b])
    if Path(args.out) != Path(run["results"])/"result.json" or Path(args.events) != Path(args.root)/"events.jsonl":
        raise PermissionError("fixed report/event paths required")
    with open(args.events, encoding="utf-8") as stream:
        events = [json.loads(line) for line in stream if line.strip()]
    result = verify(events, sys.platform, [args.uid_a, args.uid_b], args.go_status, supplemental=True)
    result.update(schema="mihari.unix-security-result/v1", os=sys.platform, run_id=run["run_id"], root_identity=run["root_identity"], uids=run["uids"], cleanup={}, failures=[])
    atomic_json(Path(args.out), result, 0o600)
    return 0 if result["passed"] else 1


if __name__ == "__main__":
    sys.exit(main())
