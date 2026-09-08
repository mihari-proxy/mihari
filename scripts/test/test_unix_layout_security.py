"""Ordinary tests: no root, accounts, mounts, services, or user data."""
import json
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))
import unix_security as security

PREFIX = "github.com/mihari-proxy/mihari/"
MANDATORY = {
    "internal/platform": ["TestSecurityTrustedRootPositive", "TestSecurityCreationACL", "TestSecurityDirectoryIdentity", "TestSecurityBindMountDenied"],
    "internal/control/transport": ["TestSecurityPeerOwner"],
    "internal/integration": ["TestSecurityTwoUIDControl", "TestSecurityPrivateDataDenied", "TestSecurityOtherUserLogsDenied"],
}


def valid_events():
    events = []
    for package, names in MANDATORY.items():
        for name in names:
            events.extend([{"Action": "run", "Package": PREFIX+package, "Test": name}, {"Action": "pass", "Package": PREFIX+package, "Test": name}])
    for uid in [51731, 51739]:
        proof = {"euid": uid, "gid": 20, "authenticated": True, "private_denied": True, "other_denied": True, "own_log": True}
        events.append({"Action": "output", "Package": PREFIX+"internal/integration", "Test": "TestSecurityTwoUIDControl", "Output": "security_child="+json.dumps(proof)+"\n"})
    events.append({"Action": "output", "Package": PREFIX+"internal/platform", "Test": "TestSecurityTrustedRootPositive", "Output": 'security_root={"uid":0,"mode":457,"dev":2,"ino":3,"mount":"2:3@4"}\n'})
    return events


def test_complete_native_evidence_passes():
    result = security.verify(valid_events(), "linux", [51731, 51739])
    assert result["passed"], result


def test_darwin_requires_real_shared_group_and_launchd_evidence():
    required = set(security.required("darwin", supplemental=True))
    for package, name in [
        ("internal/platform", "TestDarwinGroupHasPeers_IsolatedProcesses"),
        ("internal/platform", "TestDarwinWaitChildExit_PreservesZombieAndIgnoresStop"),
        ("internal/supervisor", "TestDarwinSharedChild_DescendantsAndSignalOwnership"),
        ("internal/service", "TestDarwinLaunchdIdentity_ActualArgumentsAndGroup"),
        ("internal/integration", "TestSecurityLaunchdBootoutDrainsSharedProcessGroup"),
    ]:
        assert (PREFIX+package, name) in required
        assert (PREFIX+package, name) not in security.required("linux", supplemental=True)


def test_native_failure_coordinates_do_not_publish_test_output():
    events = valid_events()
    events.append({"Action": "output", "Package": PREFIX+"internal/platform", "Test": "TestSecurityCreationACL", "Output": "    unix_security_test.go:42: secret=must-remain-private /private/path\n"})
    result = security.verify(events, "linux", [51731, 51739])
    assert result["source_locations"] == [PREFIX+"internal/platform:TestSecurityCreationACL:unix_security_test.go:42"]
    assert "must-remain-private" not in json.dumps(result)
    assert "/private/path" not in json.dumps(result)


def test_native_crash_failure_exports_only_allowlisted_case_coordinates():
    events = valid_events()
    for name in ("TestSecurityNativeCrashMatrix/fresh-system/reverse-recovery/03-service-remove-after-effect",
                 "TestSecurityNativeCrashMatrix/secret-token/reverse-recovery/03-service-remove-after-effect"):
        events.append({"Action": "fail", "Package": PREFIX+"internal/app", "Test": name})
    result = security.verify(events, "linux", [51731, 51739])
    assert result["crash_failures"] == ["fresh-system/reverse/03/after-effect"]
    assert "secret-token" not in json.dumps(result)

import copy
import pytest


@pytest.mark.parametrize("mutation", ["skip", "fail", "missing", "duplicate", "wrong_package", "subtest_only", "all_skip", "false_proof", "echo_only", "go_failure"])
def test_rejects_incomplete_or_forged_success(mutation):
    events = copy.deepcopy(valid_events())
    status = 0
    if mutation in ("skip", "fail"):
        events[1]["Action"] = mutation
    elif mutation == "missing":
        del events[1]
    elif mutation == "duplicate":
        events.append(events[1].copy())
    elif mutation == "wrong_package":
        events[1]["Package"] = PREFIX+"wrong"
    elif mutation == "subtest_only":
        events[1]["Test"] += "/child"
    elif mutation == "all_skip":
        for event in events:
            if event["Action"] == "pass":
                event["Action"] = "skip"
    elif mutation == "false_proof":
        events[-2]["Output"] = events[-2]["Output"].replace('"authenticated": true', '"authenticated": false')
    elif mutation == "echo_only":
        events = [e for e in events if e["Action"] != "output"]
    else:
        status = 1
    assert not security.verify(events, "linux", [51731, 51739], status)["passed"]


class FakeHost:
    def __init__(self, fail=None):
        self.fail = fail
        self.calls = []
        self.anchor_exists = True
        self.archive_exists = False
    def archive(self):
        self.calls.append("archive")
        if self.fail == "archive":
            raise OSError("injected archive failure")
        self.archive_exists = True
    def cleanup(self, stage):
        self.calls.append(stage)
        if self.fail == stage:
            raise OSError("injected cleanup failure")
        if stage == "anchor":
            assert self.archive_exists
            self.anchor_exists = False


@pytest.mark.parametrize("initial", [0, 1, 143])
def test_cleanup_preserves_test_and_term_failures(initial):
    host = FakeHost()
    result = security.finish(host, {"failures": [], "cleanup": {}}, initial)
    assert result["exit_status"] == (0 if initial == 0 else 1)
    assert host.calls == ["archive", "processes", "mounts", "accounts", "anchor"]
    assert host.archive_exists and not host.anchor_exists


@pytest.mark.parametrize("message,reason", [
    ("cannot inspect isolated launchd job", "launchd-job-query"),
    ("isolated launchd job remains loaded", "launchd-job-still-loaded"),
    ("isolated launchd group was not published", "launchd-group-unpublished"),
    ("isolated launchd group remains", "launchd-group-still-present"),
])
def test_launchd_cleanup_failure_has_safe_reason(message, reason):
    class FailedLaunchdHost(FakeHost):
        def cleanup(self, stage):
            self.calls.append(stage)
            raise OSError(message)
    host = FailedLaunchdHost()
    result = security.finish(host, {"failures": [], "cleanup": {}}, 0)
    assert result["cleanup_errors"]["processes"].get("reason") == reason
    assert host.calls == ["archive", "processes"]
    assert result["exit_status"] == 1 and host.anchor_exists
    assert message not in json.dumps(result)


def test_linux_cleanup_uses_available_mount_inventory_command(tmp_path, monkeypatch):
    import types
    import unix_security_host as host_module
    host = object.__new__(host_module.Host)
    host.root = tmp_path/"anchor"
    host.results = tmp_path/"results"
    host.ledger = {"mounts": []}
    monkeypatch.setattr(host_module, "load_run", lambda *args, **kwargs: {})
    monkeypatch.setattr(host_module, "sys", types.SimpleNamespace(platform="linux"))
    def inventory(command, **kwargs):
        if command != ["/bin/mount"]:
            raise FileNotFoundError(command[0])
        return "fixture-free mount inventory\n"
    monkeypatch.setattr(host_module.subprocess, "check_output", inventory)
    host.cleanup("mounts")


@pytest.mark.parametrize("case", ["gone", "published-during-bootout", "unpublished", "live-group", "query-error", "foreign-label", "foreign-plist", "invalid-pgid"])
def test_darwin_always_cleanup_joins_recorded_launchd_group(tmp_path, monkeypatch, case):
    import types
    import unix_security_host as module
    root = tmp_path/"anchor"
    jobs = root/"launchd-jobs"
    jobs.mkdir(parents=True)
    nonce, run_id = "1234567890abcdef", "0123456789ab"
    record_path = jobs/(nonce+".json")
    label = "com.mihari.security."+run_id+".shared-group."+nonce
    record = {"schema": "mihari.security-launchd-job/v1", "label": label,
              "plist": str(root/("launchd-bootout-"+nonce)/"job.plist"), "pgid": 4321}
    if case in ("published-during-bootout", "unpublished"):
        record["pgid"] = 0
    elif case == "foreign-label":
        record["label"] = "com.mihari.daemon"
    elif case == "foreign-plist":
        record["plist"] = "/Library/LaunchDaemons/com.mihari.daemon.plist"
    elif case == "invalid-pgid":
        record["pgid"] = True
    record_path.write_text(json.dumps(record))
    host = object.__new__(module.Host)
    host.root, host.results = root, tmp_path/"results"
    host.run, host.ledger = {"run_id": run_id}, {"processes": []}
    monkeypatch.setattr(module, "load_run", lambda *args, **kwargs: host.run)
    monkeypatch.setattr(module, "sys", types.SimpleNamespace(platform="darwin"))
    monkeypatch.setattr(module, "trusted_chain", lambda path: None)
    monkeypatch.setattr(module, "identity", lambda path: {"uid": 0, "mode": 0o700})
    monkeypatch.setattr(module, "read_private", lambda path: json.loads(Path(path).read_text()))
    monkeypatch.setattr(module.time, "sleep", lambda duration: None)
    ticks = iter(range(1000))
    monkeypatch.setattr(module.time, "monotonic", lambda: next(ticks))
    commands, groups = [], []
    loaded = True
    def command(argv, **kwargs):
        nonlocal loaded
        commands.append(argv)
        assert argv[0] == "/bin/launchctl" and argv[-1] == "system/"+label
        if argv[1] == "print":
            return types.SimpleNamespace(returncode=5 if case == "query-error" else (0 if loaded else 113))
        assert argv[1] == "bootout"
        loaded = False
        if case == "published-during-bootout":
            record["pgid"] = 4321
            record_path.write_text(json.dumps(record))
        return types.SimpleNamespace(returncode=0)
    def probe(pgid, sig):
        groups.append((pgid, sig))
        assert pgid == 4321 and sig == 0
        if case != "live-group":
            raise ProcessLookupError()
    monkeypatch.setattr(module.subprocess, "run", command)
    monkeypatch.setattr(module.os, "killpg", probe, raising=False)
    if case in ("gone", "published-during-bootout"):
        host.cleanup("processes")
        assert groups == [(4321, 0)]
        assert any(argv[1] == "bootout" for argv in commands)
        assert record_path.exists()  # always recovery remains repeatable
    else:
        with pytest.raises(OSError):
            host.cleanup("processes")
        if case in ("foreign-label", "foreign-plist", "invalid-pgid"):
            assert commands == [] and groups == []
        if case == "query-error":
            assert len(commands) == 1 and groups == []
        if case == "unpublished":
            assert groups == [] and record_path.exists()
            record["pgid"] = 4321  # a late publisher is visible on always retry
            record_path.write_text(json.dumps(record))
            host.cleanup("processes")
            assert groups == [(4321, 0)]


@pytest.mark.parametrize("failure", ["processes", "mounts", "accounts", "anchor", "archive"])
def test_failure_survives_always_retry(failure):
    host = FakeHost(failure)
    result = security.finish(host, {"failures": [], "cleanup": {}}, 0)
    assert result["exit_status"] == 1
    assert host.anchor_exists
    if failure == "archive":
        assert host.calls == ["archive"]
    host.fail = None
    result = security.finish(host, result, 0)
    assert result["exit_status"] == 1  # successful recovery cannot erase failure
    assert all(value == "pass" for value in result["cleanup"].values())
    assert host.archive_exists and not host.anchor_exists

@pytest.mark.parametrize("failure", [None, "go", "skip", "term", "archive", "processes", "mounts", "accounts", "anchor"])
def test_real_runner_wrapper_preserves_every_failure(tmp_path, monkeypatch, failure):
    """Exercise run_tests/trap/verifier, replacing only its privileged host edges."""
    import argparse
    import hashlib
    import unix_security_host as host_module
    root=tmp_path/"anchor"
    results=tmp_path/"results"
    (root/"shared").mkdir(parents=True)
    results.mkdir()
    (root/"shared"/"supervisor.py").write_bytes(b"fake")
    run={"root":str(root),"results":str(results),"run_id":"0123456789ab","root_identity":{"dev":1,"ino":2},"uids":[51731,51739],"accounts":[{"uid":51731,"gid":20},{"uid":51739,"gid":20}]}
    manifest={"binaries":[],"tools":{}}
    for package in sorted(set(security.COMMON)|set(security.SUPPLEMENTAL)):
        name=package.replace("/","-")+".test"
        (root/"shared"/name).write_bytes(b"fake")
        manifest["binaries"].append({"package":PREFIX+package,"name":name,"identity":{"dev":1},"sha256":hashlib.sha256(b"fake").hexdigest()})
    tool=tmp_path/"tool"
    tool.write_bytes(b"fake")
    for key in ["go","python"]:
        manifest["tools"][key]={"path":str(tool),"size":4,"sha256":hashlib.sha256(b"fake").hexdigest()}
        monkeypatch.setenv("MIHARI_SECURITY_"+key.upper(),str(tool))
    monkeypatch.setattr(host_module,"load_run",lambda *a,**k:run)
    monkeypatch.setattr(host_module,"require_private_namespace",lambda:None)
    monkeypatch.setattr(host_module,"identity",lambda *a:{"dev":1})
    monkeypatch.setattr(host_module,"process_identity",lambda *a:"owned-process")
    monkeypatch.setattr(host_module,"read_private",lambda *a:manifest)
    monkeypatch.setattr(host_module,"sync_dir",lambda *a:None)
    monkeypatch.setattr(host_module.os,"readlink",lambda p:"private" if "self" in p else "host")
    monkeypatch.setattr(host_module.os,"killpg",lambda *a:None,raising=False)
    import types
    monkeypatch.setattr(host_module,"sys",types.SimpleNamespace(platform="linux"))
    monkeypatch.setattr(host_module,"atomic_json",lambda p,value,mode=0o600:Path(p).write_text(json.dumps(value)))
    class WrapperHost(FakeHost):
        def __init__(self,ignored):
            super().__init__(failure)
            self.root=root;self.results=results
            self.ledger={"processes":[],"intents":[],"objects":[{"path":str(root/"shared"/"supervisor.py"),"identity":{"dev":1},"sha256":hashlib.sha256(b"fake").hexdigest()}]}
        def save(self):
            pass
    instance=WrapperHost(run)
    monkeypatch.setattr(host_module,"Host",lambda ignored:instance)
    class Process:
        pid=123
        def __init__(self,argv,**kwargs):
            kwargs["stderr"].write(b"launcher diagnostic outside the test event protocol\n")
            if "user" in kwargs:
                user_root = results/"users"/"a"
                for name in ("GOCACHE", "GOMODCACHE", "GOPATH", "TEST_TELEMETRY_DIR"):
                    assert Path(kwargs["env"][name]).is_relative_to(user_root), name+" is not writable by the ordinary test UID"
            package=argv[argv.index("-p")+1]
            pattern=next(x for x in argv if x.startswith("-test.run="))
            names=pattern.split("^(" if "^(" in pattern else "^",1)[1].removesuffix(")$").split("|")
            stream=kwargs["stdout"]
            for name in names:
                for action in ["run","skip" if failure=="skip" and name=="TestSecurityPeerOwner" else "pass"]:
                    stream.write((json.dumps({"Action":action,"Package":package,"Test":name})+"\n").encode())
                if name=="TestUnixProcess_LocalFailureExitContracts":
                    for row in ["invalid-layout","daemon-lock","channel-lock","unsafe-channel-root","channel-IO"]:
                        stream.write((json.dumps({"Action":"pass","Package":package,"Test":name+"/"+row})+"\n").encode())
                if name=="TestUnixSecurity_FullAssembly":
                    for uid in run["uids"]:
                        proof={"EUID":uid,"GID":20,"Authenticated":True,"SettingsDenied":True,"OtherUserDenied":True,"V2Export":True}
                        stream.write((json.dumps({"Action":"output","Package":package,"Test":name,"Output":"unix_assembly_result="+json.dumps(proof)})+"\n").encode())
            for event in valid_events():
                if event["Action"]=="output" and event["Package"]==package:
                    stream.write((json.dumps(event)+"\n").encode())
        def wait(self,timeout=None):
            if failure=="term":
                raise InterruptedError(15)
            return 1 if failure=="go" else 0
    monkeypatch.setattr(host_module.subprocess,"Popen",Process)
    args=argparse.Namespace(root=str(root),report=str(results/"result.json"),uid_a=51731,uid_b=51739,source=str(tmp_path))
    status=host_module.run_tests(args)
    report=json.loads((results/"result.json").read_text())
    assert status==(0 if failure is None else 1),report
    assert instance.archive_exists or failure=="archive"
    assert report["exit_status"]==status


@pytest.mark.skipif(sys.platform=="win32",reason="POSIX ordinary process-group test")
@pytest.mark.parametrize("code",[0,7])
def test_supervisor_preserves_actual_child_exit(code):
    import os
    import subprocess
    read,write=os.pipe()
    process=subprocess.Popen([sys.executable,str(Path(__file__).with_name("unix_security_supervisor.py")),str(read),sys.executable,"-c",f"raise SystemExit({code})"],pass_fds=(read,),start_new_session=True)
    os.close(read)
    os.write(write,b"G");os.close(write)
    assert process.wait(timeout=10)==code

def test_supplemental_rejects_wrong_five_cli_rows():
    events=valid_events()
    for package,names in security.SUPPLEMENTAL.items():
        for name in names:
            events.extend([{"Action":"run","Package":PREFIX+package,"Test":name},{"Action":"pass","Package":PREFIX+package,"Test":name}])
    for index in range(5):
        events.append({"Action":"pass","Package":PREFIX+"cmd/mihari","Test":"TestUnixProcess_LocalFailureExitContracts/wrong"+str(index)})
    for uid in [51731,51739]:
        proof={"EUID":uid,"GID":20,"Authenticated":True,"SettingsDenied":True,"OtherUserDenied":True,"V2Export":True}
        events.append({"Action":"output","Package":PREFIX+"cmd/mihari","Test":"TestUnixSecurity_FullAssembly","Output":"unix_assembly_result="+json.dumps(proof)})
    assert not security.verify(events,"linux",[51731,51739],supplemental=True)["passed"]

def test_supervisor_retains_leader_past_grace_until_descendants_join(monkeypatch):
    import types
    import unix_security_supervisor as supervisor
    calls=[]
    remaining=iter([[91],[91],[91],[91],[]])
    def members():
        calls.append("members")
        return next(remaining)
    monkeypatch.setattr(supervisor,"members",members)
    monkeypatch.setattr(supervisor,"sys",types.SimpleNamespace(argv=["supervisor","3","fake"]))
    monkeypatch.setattr(supervisor.os,"read",lambda *a:b"G")
    monkeypatch.setattr(supervisor.os,"close",lambda *a:None)
    monkeypatch.setattr(supervisor.os,"killpg",lambda *a:None,raising=False)
    monkeypatch.setattr(supervisor.os,"getpgrp",lambda:90,raising=False)
    monkeypatch.setattr(supervisor.signal,"signal",lambda *a:None)
    monkeypatch.setattr(supervisor.subprocess,"Popen",lambda *a:types.SimpleNamespace(wait=lambda:0))
    ticks=iter([0,20])
    monkeypatch.setattr(supervisor.time,"monotonic",lambda:next(ticks))
    monkeypatch.setattr(supervisor.time,"sleep",lambda *a:None)
    assert supervisor.main()==143
    assert len(calls)==5


@pytest.mark.skipif(sys.platform=="win32",reason="POSIX ordinary signal/gate fixture")
@pytest.mark.parametrize("case",["gate-eof","term"])
def test_supervisor_gate_and_actual_term_join(tmp_path,case):
    import os,signal,subprocess,time
    ready,joined=tmp_path/"ready",tmp_path/"joined"
    program=("import os,signal,time;from pathlib import Path;"
             f"signal.signal(signal.SIGTERM,lambda s,f:(Path({str(joined)!r}).write_text(str(os.getpid())),exit(0)));"
             f"Path({str(ready)!r}).write_text(str(os.getpid()));time.sleep(30)")
    read,write=os.pipe()
    process=None
    try:
        process=subprocess.Popen([sys.executable,str(Path(__file__).with_name("unix_security_supervisor.py")),str(read),sys.executable,"-c",program],pass_fds=(read,),start_new_session=True)
        os.close(read);read=None
        if case=="term":os.write(write,b"G")
        os.close(write);write=None
        if case=="term":
            deadline=time.monotonic()+5
            while not ready.exists():
                assert process.poll() is None and time.monotonic()<deadline
                time.sleep(.02)
            os.kill(process.pid,signal.SIGTERM)
        assert process.wait(timeout=12)==(143 if case=="term" else 2)
        if case=="term":assert ready.read_text()==joined.read_text()
    finally:
        for descriptor in (read,write):
            if descriptor is not None:os.close(descriptor)
        if process is not None and process.poll() is None:
            os.killpg(process.pid,signal.SIGKILL);process.wait(timeout=5)

@pytest.mark.parametrize("failure",[None,"exit","term","ledger"])
def test_account_preparation_owns_and_joins_process_before_recovery(tmp_path,monkeypatch,failure):
    import unix_security_host as host
    events=[]
    ledger={"processes":[]}
    class Process:
        pid=91
        done=False
        def wait(self,timeout):
            events.append("wait")
            if failure=="term" and len([e for e in events if e=="wait"])==1:raise InterruptedError(15)
            self.done=True
            return 7 if failure=="exit" else 0
        def poll(self):return 0 if self.done else None
    process=Process()
    monkeypatch.setattr(host.subprocess,"Popen",lambda *a,**kw:process)
    monkeypatch.setattr(host,"process_identity",lambda pid:"actual-start")
    monkeypatch.setattr(host.os,"pipe",lambda:(30,31))
    monkeypatch.setattr(host.os,"close",lambda *a:None)
    monkeypatch.setattr(host.os,"write",lambda *a:events.append("gate"))
    monkeypatch.setattr(host.os,"killpg",lambda *a:events.append("terminate"),raising=False)
    monkeypatch.setattr(host.signal,"signal",lambda *a:None)
    def persist(*args):
        events.append("identity" if ledger["processes"][0].get("pid") else "intent")
        if failure=="ledger" and events[-1]=="identity":raise OSError("disk sync")
    monkeypatch.setattr(host,"atomic_json",persist)
    if failure:
        with pytest.raises((OSError,host.subprocess.CalledProcessError)):
            host.owned_prepare_command(["fake-account"],tmp_path,tmp_path,ledger)
    else:host.owned_prepare_command(["fake-account"],tmp_path,tmp_path,ledger)
    assert events[:2]==["intent","identity"]
    assert process.done
    if failure=="ledger":assert "gate" not in events and "terminate" in events
    if failure=="term":assert "terminate" in events and events.count("wait")==2

def test_real_always_entry_retries_without_erasing_prior_failure(tmp_path,monkeypatch):
    import unix_security_host as host
    root,results=tmp_path/"anchor",tmp_path/"results"
    root.mkdir();results.mkdir()
    report=results/"result.json"
    report.write_text(json.dumps({"schema":"mihari.unix-security-result/v1","run_id":"0123456789ab","passed":False,"failures":["test-or-signal"],"cleanup":{}}))
    run={"run_id":"0123456789ab","root":str(root),"results":str(results)}
    instance=FakeHost("accounts")
    monkeypatch.setattr(host,"guard",lambda:None)
    monkeypatch.setattr(host,"load_run",lambda *a,**kw:run)
    monkeypatch.setattr(host,"Host",lambda *a:instance)
    monkeypatch.setattr(host,"atomic_json",lambda p,v,mode:Path(p).write_text(json.dumps(v)))
    monkeypatch.setattr(host.sys,"argv",["host","recover","--root",str(root),"--report",str(report)])
    assert host.main()==1
    assert instance.anchor_exists
    instance.fail=None
    assert host.main()==1
    final=json.loads(report.read_text())
    assert final["failures"]==["accounts","test-or-signal"]
    assert all(value=="pass" for value in final["cleanup"].values())
    assert not final["passed"] and instance.archive_exists and not instance.anchor_exists
@pytest.mark.parametrize("boundary",["record-term","preflight","results-create","anchor-create","results-identity","anchor-identity","anchor-marker","results-marker","after-anchor-marker","after-results-marker","outputs","term"])
def test_preparation_failures_preserve_identity_bound_route(tmp_path,monkeypatch,boundary):
    import argparse,builtins,os
    import unix_security_host as host
    root,results=tmp_path/"anchor",tmp_path/"results"
    outputs=tmp_path/"outputs"
    monkeypatch.setattr(host,"guard",lambda:None)
    monkeypatch.setattr(host,"paths",lambda ignored:(root,results))
    monkeypatch.setattr(host,"trusted_chain",lambda *a:None)
    monkeypatch.setattr(host,"sync_dir",lambda *a:None)
    monkeypatch.setattr(host.os,"O_NOFOLLOW",0,raising=False)
    monkeypatch.setattr(host,"read_private",lambda p:json.loads(Path(p).read_text()))
    real_identity=host.identity
    def native_identity(path):
        found=real_identity(path)
        found["uid"]=0
        if Path(path)==root:found["mode"]=0o711
        if Path(path)==results:found["mode"]=0o755
        return found
    monkeypatch.setattr(host,"identity",native_identity)
    accounts=[{"name":"mh0123456789ab0","uid":51731,"gid":20},{"name":"mh0123456789ab1","uid":51739,"gid":20}]
    def preflight(args):
        assert not root.exists() and not results.exists()
        if boundary=="preflight":raise PermissionError("two fresh UIDs unavailable")
        return [51731,51739],accounts,{}
    monkeypatch.setattr(host,"preparation_preflight",preflight)
    monkeypatch.setattr(host,"prepare_resources",lambda *a:None)
    triggered=False
    real_os_open=os.open
    def record_open(path,*a,**kw):
        nonlocal triggered
        fd=real_os_open(path,*a,**kw)
        if boundary=="record-term" and Path(path)==host.preparation_path(root) and not triggered:
            triggered=True
            try:host.signal.getsignal(host.signal.SIGTERM)(host.signal.SIGTERM,None)
            except BaseException:
                os.close(fd)
                raise
        return fd
    monkeypatch.setattr(os,"open",record_open)
    real_mkdir=Path.mkdir
    def mkdir(path,*a,**kw):
        nonlocal triggered
        if not triggered and ((boundary=="results-create" and path==results) or (boundary=="anchor-create" and path==root)):
            triggered=True;raise OSError("directory creation")
        return real_mkdir(path,*a,**kw)
    monkeypatch.setattr(Path,"mkdir",mkdir)
    real_open=builtins.open
    def output_open(path,*a,**kw):
        nonlocal triggered
        if Path(path)==outputs and boundary=="outputs" and not triggered:
            triggered=True;raise OSError("output publication")
        return real_open(path,*a,**kw)
    monkeypatch.setattr(builtins,"open",output_open)
    def persist(path,value,mode=0o600):
        nonlocal triggered
        match=(boundary=="results-identity" and Path(path)==host.preparation_path(root) and value["run"]["results_identity"] is not None and value["run"]["root_identity"] is None) or (boundary in ("anchor-identity","term") and Path(path)==host.preparation_path(root) and value["run"]["root_identity"] is not None) or (boundary=="anchor-marker" and Path(path)==root/host.MARKER) or (boundary=="results-marker" and Path(path)==results/host.MARKER)
        if match and not triggered:
            triggered=True
            if boundary=="term":raise InterruptedError(15)
            raise OSError("injected preparation boundary")
        Path(path).write_text(json.dumps(value))
        if not triggered and ((boundary=="after-anchor-marker" and Path(path)==root/host.MARKER) or (boundary=="after-results-marker" and Path(path)==results/host.MARKER)):
            triggered=True;raise OSError("marker sync after publication")
    monkeypatch.setattr(host,"atomic_json",persist)
    args=argparse.Namespace(run_id="0123456789ab",outputs=str(outputs),build=str(tmp_path))
    with pytest.raises(OSError):host.prepare(args)
    report=json.loads((results/"result.json").read_text())
    assert report["failures"]==["prepare"] and not report["passed"]
    record=host.load_preparation(root,results)
    assert record["run"]["results_identity"]==host.identity(results)
    assert root.exists()==(boundary not in ("record-term","preflight","results-create","anchor-create","results-identity"))
    assert host.load_run(root,results,allow_missing_root=True)==record["run"]
    if root.exists():
        old=host.identity(root)
        monkeypatch.setattr(host,"identity",lambda p:{**old,"ino":old["ino"]+1} if Path(p)==root else native_identity(p))
        with pytest.raises(PermissionError):host.load_preparation(root,results)
        monkeypatch.setattr(host,"identity",native_identity)
    # Exercise the actual recovery/archive/results-cleanup dispatcher. Only
    # native account/mount observations are faked; all owned files are temp files.
    monkeypatch.setattr(host,"account",lambda name:None)
    monkeypatch.setattr(host.subprocess,"check_output",lambda *a,**kw:"")
    monkeypatch.setattr(host.sys,"argv",["host","recover","--root",str(root),"--report",str(results/"result.json")])
    assert host.main()==1
    assert not root.exists() and (results/"cleanup-archive.json").exists()
    assert host.preparation_path(root).exists()  # survives until successful upload
    assert host.main()==1  # a second always attempt cannot erase preparation failure
    final=json.loads((results/"result.json").read_text())
    assert final["failures"]==["prepare"] and not final["passed"]
    monkeypatch.setattr(host.sys,"argv",["host","clean-results","--root",str(root),"--report",str(results/"result.json")])
    assert host.main()==0
    assert not results.exists() and not host.preparation_path(root).exists()


def test_preparation_term_is_deferred_until_identity_persistence():
    import signal
    import unix_security_host as host
    sequence=[]
    previous=signal.getsignal(signal.SIGTERM)
    with pytest.raises(InterruptedError):
        with host.preparation_signal_boundary():
            sequence.append("created")
            signal.getsignal(signal.SIGTERM)(signal.SIGTERM,None)
            sequence.append("identity-synced")
    assert sequence==["created","identity-synced"]
    assert signal.getsignal(signal.SIGTERM)==previous


def test_workflow_routes_recovery_before_privileged_preparation():
    workflow=Path(__file__).parents[2]/".github/workflows/unix-layout-security.yml"
    text=workflow.read_text()
    assert text.index("id: route")<text.index("id: fixture")
    assert "steps.route.outputs.results" in text and "steps.fixture.outputs.results" not in text


@pytest.mark.parametrize("case", ["deleted", "retry", "still-present", "query-failure", "replaced"])
def test_darwin_account_cleanup_checks_directory_record_not_cached_lookup(tmp_path, monkeypatch, case):
    import types
    import unix_security_host as host
    entry = {"name": "mh0123456789ab0", "uid": 51731, "gid": 20}
    instance = host.Host.__new__(host.Host)
    instance.root, instance.results = tmp_path/"anchor", tmp_path/"results"
    instance.ledger = {"accounts": [entry], "intents": [{"kind": "directory-account", "name": entry["name"], "generated_uid": "OWNED-GUID"}]}
    monkeypatch.setattr(host, "load_run", lambda *a, **kw: {})
    monkeypatch.setattr(host, "sys", types.SimpleNamespace(platform="darwin"))
    # getpwnam can retain the deleted record; Directory Service is authoritative.
    monkeypatch.setattr(host, "account", lambda name: entry.copy())
    present = case != "retry"
    deleted = []
    def command(args, **kwargs):
        nonlocal present
        assert args[:2] == ["/usr/bin/dscl", "."]
        if args[2:] == ["-list", "/Users"]:
            if case == "query-failure":
                raise host.subprocess.CalledProcessError(1, args)
            return types.SimpleNamespace(returncode=0, stdout="root\n"+(entry["name"]+"\n" if present else ""))
        if args[2:] == ["-read", "/Users/"+entry["name"], "GeneratedUID"]:
            return types.SimpleNamespace(returncode=0 if present else 1, stdout="GeneratedUID: "+("OTHER-GUID" if case == "replaced" else "OWNED-GUID"))
        assert args[2:] == ["-delete", "/Users/"+entry["name"]]
        deleted.append(entry["name"])
        present = case == "still-present"
        return types.SimpleNamespace(returncode=0, stdout="")
    monkeypatch.setattr(host.subprocess, "run", command)
    if case in ("still-present", "query-failure", "replaced"):
        with pytest.raises((OSError, host.subprocess.CalledProcessError)):
            instance.cleanup("accounts")
    else:
        instance.cleanup("accounts")
    assert deleted == ([entry["name"]] if case in ("deleted", "still-present") else [])
