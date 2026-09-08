#!/usr/bin/env python3
"""One owned process group: wait for durable parent intent before spawning tests."""
import os
import signal
import subprocess
import sys
import time


def members():
    rows = subprocess.check_output(["/bin/ps", "-axo", "pid=,pgid="], text=True)
    live=[]
    for row in rows.splitlines():
        parts=row.split()
        if len(parts)!=2 or int(parts[1])!=os.getpgrp() or int(parts[0])==os.getpid():
            continue
        try:
            os.kill(int(parts[0]),0)
            live.append(int(parts[0]))
        except ProcessLookupError:
            pass
    return live


def main():
    descriptor = int(sys.argv[1])
    if os.read(descriptor, 1) != b"G":
        return 2
    os.close(descriptor)
    interrupted = False
    def stop(_signum, _frame):
        nonlocal interrupted
        interrupted = True
        signal.signal(signal.SIGTERM, signal.SIG_IGN)
        signal.signal(signal.SIGINT, signal.SIG_IGN)
        os.killpg(os.getpgrp(), signal.SIGTERM)
    signal.signal(signal.SIGTERM, stop)
    signal.signal(signal.SIGINT, stop)
    command = sys.argv[2:]
    if command and command[0].startswith("--owner="):
        command = command[1:]
    child = subprocess.Popen(command)
    code = child.wait()
    # Keep the group leader (and its recorded start identity) alive until every
    # descendant has exited. This permits identity-checked cleanup after a Go
    # fatal/timeout while its helper is still active.
    leftovers = members()
    if leftovers:
        stop(signal.SIGTERM, None)
        deadline = time.monotonic()+8
        while members() and time.monotonic() < deadline:
            time.sleep(0.05)
        # Retain the identity-bearing group leader while an uncooperative
        # descendant exists. The root deadline fails cleanup and retains the
        # ledger/accounts/anchor; the ephemeral VM bounds final destruction.
        # Exiting here would erase the parent's authority to this live group.
        while members():
            time.sleep(0.1)
    return 143 if interrupted else code


if __name__ == "__main__":
    sys.exit(main())
