"""Host adapter for the explicitly isolated ephemeral native security workflow.

The ordinary tests import unix_security (policy) without importing Unix account APIs.
Every durable resource intent is written to the independent results ledger first.
"""
import argparse
from contextlib import contextmanager
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import signal
import stat
import subprocess
import sys
import time
import secrets

from unix_security import PREFIX, COMMON, SUPPLEMENTAL, DARWIN_SUPPLEMENTAL, finish, verify

MARKER = ".mihari-security-owner.json"
SCHEMA = "mihari.unix-security-owner/v1"
PACKAGE_TIMEOUT_SECONDS = 400
APP_TIMEOUT_SECONDS = 900
EXECUTION_TIMEOUT_SECONDS = 1200


def atomic_json(path, value, mode=0o600):
    path = Path(path)
    temporary = path.with_name(path.name+".new-"+secrets.token_hex(8))
    fd = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, mode)
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as stream:
            json.dump(value, stream, sort_keys=True)
            stream.write("\n")
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, path)
        os.chmod(path, mode, follow_symlinks=False)
        sync_dir(path.parent)
    finally:
        if temporary.exists():
            temporary.unlink()


def sync_dir(path):
    fd = os.open(path, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def identity(path):
    item = os.lstat(path)
    return {"dev": item.st_dev, "ino": item.st_ino, "uid": item.st_uid, "mode": stat.S_IMODE(item.st_mode)}


def no_acl(path):
    if sys.platform == "linux":
        for name in ("system.posix_acl_access", "system.posix_acl_default"):
            try:
                if os.getxattr(path, name, follow_symlinks=False):
                    raise PermissionError("ACL on trusted ancestor")
            except OSError as error:
                if error.errno != 61:  # ENODATA; unsupported ACL checks fail closed
                    raise
    else:
        output = subprocess.check_output(["/bin/ls", "-lde", str(path)], text=True)
        if re.search(r"^\s*\d+:", output, re.MULTILINE):
            raise PermissionError("ACL on trusted ancestor")


def trusted_chain(path):
    path = Path(path)
    if not path.is_absolute() or str(path) != os.path.normpath(str(path)):
        raise PermissionError("canonical absolute path required")
    for item in [Path("/"), *reversed(path.parents[:-1]), path]:
        st = os.lstat(item)
        if not stat.S_ISDIR(st.st_mode) or st.st_uid != 0 or stat.S_IMODE(st.st_mode) & 0o022:
            raise PermissionError("untrusted root ancestor")
        no_acl(item)


def guard():
    if sys.platform not in ("linux", "darwin") or os.geteuid() != 0 or os.environ.get("CI") != "true" or os.environ.get("MIHARI_ISOLATED_SECURITY_CI") != "1" or os.environ.get("GITHUB_ACTIONS") != "true" or os.environ.get("RUNNER_ENVIRONMENT") != "github-hosted":
        raise PermissionError("explicit ephemeral hosted security CI required")


def paths(run_id):
    if not re.fullmatch(r"[a-f0-9]{12}", run_id):
        raise PermissionError("invalid run ID")
    if sys.platform == "linux":
        return Path("/var/lib/mihari-security-"+run_id), Path("/var/lib/mihari-security-results-"+run_id)
    return Path("/Library/MihariSecurity-"+run_id), Path("/Library/MihariSecurityResults-"+run_id)


def read_private(path):
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    try:
        st = os.fstat(fd)
        if not stat.S_ISREG(st.st_mode) or st.st_uid != 0 or stat.S_IMODE(st.st_mode) != 0o600 or st.st_nlink != 1 or st.st_size > 1<<20:
            raise PermissionError("invalid private marker/ledger")
        with os.fdopen(fd, "r", encoding="utf-8", closefd=False) as stream:
            return json.load(stream)
    finally:
        os.close(fd)


def _load_run(root, results, uids=None, allow_missing_root=False):
    guard()
    root, results = Path(root), Path(results)
    trusted_chain(results)
    marker = read_private(results/MARKER)
    if marker.get("schema") != SCHEMA or marker.get("role") != "results" or tuple(map(str, paths(marker.get("run_id", "")))) != (str(root), str(results)):
        raise PermissionError("result marker scope mismatch")
    run = marker["run"]
    if identity(results) != run["results_identity"] or (uids is not None and run["uids"] != uids):
        raise PermissionError("result identity mismatch")
    if root.exists():
        trusted_chain(root)
        other = read_private(root/MARKER)
        if identity(root) != run["root_identity"] or other != {"schema": SCHEMA, "role": "anchor", "run": run, "run_id": run["run_id"]}:
            raise PermissionError("anchor identity mismatch")
    elif not allow_missing_root:
        raise PermissionError("missing marked anchor")
    return run


def load_run(root,results,uids=None,allow_missing_root=False):
    try:
        return _load_run(root,results,uids,allow_missing_root)
    except (OSError,ValueError,KeyError):
        if not allow_missing_root:
            raise
        guard()
        record=load_preparation(Path(root),Path(results))
        if record.get("ready"):
            raise PermissionError("completed preparation markers invalid")
        return record["run"]


def account(name):
    import pwd
    try:
        item = pwd.getpwnam(name)
        return {"name": item.pw_name, "uid": item.pw_uid, "gid": item.pw_gid}
    except KeyError:
        return None


def darwin_account_present(name):
    # Query the local directory node, not getpwnam's potentially stale cache.
    # A failed query is never evidence that a record was deleted.
    found = subprocess.run(["/usr/bin/dscl", ".", "-list", "/Users"], capture_output=True, text=True, check=True)
    return name in found.stdout.splitlines()


def free_uids():
    import pwd
    used = {entry.pw_uid for entry in pwd.getpwall()}
    return [uid for uid in range(50000, 60000) if uid not in used][:2]


def owned_prepare_command(command, root, results, ledger):
    """Gate account mutations behind a synced process identity, including TERM."""
    intent={"operation":command[0],"nonce":str(time.monotonic_ns())}
    ledger["processes"].append(intent)
    atomic_json(results/"cleanup-ledger.json",ledger)
    read_gate,write_gate=os.pipe()
    process=None
    def interrupted(signum,_frame):
        raise InterruptedError(signum)
    previous=signal.signal(signal.SIGTERM,interrupted)
    try:
        process=subprocess.Popen([str(Path(sys.executable).resolve()),str(root/"shared"/"supervisor.py"),str(read_gate),"--owner="+intent["nonce"],*command],start_new_session=True,pass_fds=(read_gate,))
        intent.update(pid=process.pid,identity=process_identity(process.pid))
        atomic_json(results/"cleanup-ledger.json",ledger)
        os.write(write_gate,b"G")
        code=process.wait(timeout=30)
        if code:
            raise subprocess.CalledProcessError(code,command)
    finally:
        signal.signal(signal.SIGTERM,signal.SIG_IGN)
        os.close(read_gate);os.close(write_gate)
        try:
            if process is not None and process.poll() is None:
                os.killpg(process.pid,signal.SIGTERM)
                process.wait(timeout=10)
        finally:
            signal.signal(signal.SIGTERM,previous)


def preparation_path(root):
    return Path(str(root)+".prepare.json")


def preparation_preflight(args):
    uids=free_uids()
    if len(uids)!=2:
        raise PermissionError("two fresh UIDs unavailable")
    import grp
    gid=20 if sys.platform=="darwin" else grp.getgrnam("nogroup").gr_gid
    if gid<=0:
        raise PermissionError("unprivileged primary group required")
    accounts=[]
    for index,uid in enumerate(uids):
        name="mh"+args.run_id+str(index)
        if uid<=0 or account(name) is not None:
            raise PermissionError("fixture account exists")
        accounts.append({"name":name,"uid":uid,"gid":gid})
    manifest = json.loads((Path(args.build)/"manifest.json").read_text())
    expected={PREFIX+p:p.replace("/","-")+".test" for p in set(COMMON)|set(SUPPLEMENTAL)}
    artifacts=manifest.get("binaries",[])
    if manifest.get("schema")!="mihari.unix-security-binaries/v1" or manifest.get("os")!=sys.platform or manifest.get("tags")!="unix_security" or len(artifacts)!=len(expected):
        raise PermissionError("native manifest scope mismatch")
    seen=set()
    for artifact in artifacts:
        package=artifact.get("package")
        if package in seen or package not in expected or artifact.get("name")!=expected[package] or not re.fullmatch(r"[a-f0-9]{64}",artifact.get("sha256","")) or type(artifact.get("size")) is not int or not 0<artifact["size"]<=256<<20:
            raise PermissionError("native manifest artifact mismatch")
        seen.add(package)
    # Read and hash before any privileged directory/account mutation as well.
    for artifact in artifacts:
        data=(Path(args.build)/artifact["name"]).read_bytes()
        if len(data)!=artifact["size"] or hashlib.sha256(data).hexdigest()!=artifact["sha256"]:
            raise PermissionError("precompiled test binary changed")
    return uids,accounts,manifest


def save_preparation(record):
    atomic_json(preparation_path(record["run"]["root"]),record)


def load_preparation(root,results):
    trusted_chain(Path(root).parent)
    record=read_private(preparation_path(root))
    run=record["run"]
    if set(record)!={"schema","ready","run","ledger","parent_identity"} or record.get("schema")!="mihari.unix-security-preparation/v1" or type(record.get("ready")) is not bool or tuple(map(str,paths(run.get("run_id",""))))!=(str(root),str(results)) or run.get("root")!=str(root) or run.get("results")!=str(results):
        raise PermissionError("preparation route mismatch")
    if identity(Path(root).parent)!=record["parent_identity"] or set(run)!={"run_id","root","results","root_identity","results_identity","uids","accounts"}:
        raise PermissionError("preparation parent/schema mismatch")
    if len(run["uids"]) not in (0,2) or run["uids"]!=[a["uid"] for a in run["accounts"]] or len(set(run["uids"]))!=len(run["uids"]):
        raise PermissionError("preparation account inventory mismatch")
    for index,entry in enumerate(run["accounts"]):
        if set(entry)!={"name","uid","gid"} or entry["name"]!="mh"+run["run_id"]+str(index) or type(entry["uid"]) is not int or entry["uid"]<=0 or type(entry["gid"]) is not int or entry["gid"]<=0:
            raise PermissionError("preparation account identity mismatch")
    ledger=record["ledger"]
    if ledger.get("schema")!="mihari.unix-security-ledger/v1" or any(ledger.get(k)!=run[k] for k in ("run_id","root","results","accounts")):
        raise PermissionError("preparation ledger mismatch")
    for key,path in (("root_identity",root),("results_identity",results)):
        wanted=run.get(key)
        if Path(path).exists():
            if wanted is None or wanted.get("uid")!=0 or wanted.get("mode")!=(0o711 if key=="root_identity" else 0o755) or identity(path)!=wanted:
                raise PermissionError("unrecorded or replaced preparation directory")
            trusted_chain(path)
    return record


@contextmanager
def preparation_signal_boundary():
    pending=[]
    def defer_signal(signum,_frame):
        pending.append(signum)
    previous=signal.signal(signal.SIGTERM,defer_signal)
    try:
        yield
    finally:
        signal.signal(signal.SIGTERM,previous)
    if pending:
        raise InterruptedError(pending[0])


def preparation_directory(record,key,mode):
    run=record["run"]
    path=Path(run["root" if key=="root_identity" else "results"])
    with preparation_signal_boundary():
        path.mkdir(mode=mode)
        run[key]=identity(path)  # also retained if subsequent persistence fails
        save_preparation(record)
        sync_dir(path.parent)


def preparation_report(record):
    run=record["run"]
    results=Path(run["results"])
    if not results.exists():
        preparation_directory(record,"results_identity",0o755)
    save_preparation(record)
    load_preparation(Path(run["root"]),results)
    atomic_json(results/"cleanup-ledger.json",record["ledger"])
    atomic_json(results/"result.json",{"schema":"mihari.unix-security-result/v1","os":sys.platform,"run_id":run["run_id"],"root_identity":run["root_identity"],"uids":run["uids"],"passed":False,"failures":["prepare"],"checks":{},"cleanup":{},"exit_status":1},0o644)


def prepare(args):
    def interrupted(signum,_frame):
        raise InterruptedError(signum)
    previous=signal.signal(signal.SIGTERM,interrupted)
    try:
        return _prepare(args)
    finally:
        signal.signal(signal.SIGTERM,previous)


def _prepare(args):
    guard()
    root,results=paths(args.run_id)
    trusted_chain(root.parent)
    if root.exists() or results.exists() or preparation_path(root).exists():
        raise PermissionError("run resources already exist")
    preflight_error=None
    try:
        uids,accounts,manifest=preparation_preflight(args)
    except (OSError,ValueError,KeyError,subprocess.SubprocessError) as error:
        preflight_error=error
        uids,accounts,manifest=[],[],None
    ledger={"schema":"mihari.unix-security-ledger/v1","run_id":args.run_id,"root":str(root),"results":str(results),"accounts":accounts,"processes":[],"mounts":[],"objects":[],"intents":[{"kind":"anchor","path":str(root)}]}
    run={"run_id":args.run_id,"root":str(root),"results":str(results),"root_identity":None,"results_identity":None,"uids":uids,"accounts":accounts}
    record={"schema":"mihari.unix-security-preparation/v1","ready":False,"run":run,"ledger":ledger,"parent_identity":identity(root.parent)}
    # This independent private record precedes both directories. The workflow
    # already published the deterministic route in its preceding unprivileged step.
    created=False
    try:
        with preparation_signal_boundary():
            fd=os.open(preparation_path(root),os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o600)
            created=True
            with os.fdopen(fd,"w",encoding="utf-8") as stream:
                json.dump(record,stream);stream.flush();os.fsync(stream.fileno())
            sync_dir(root.parent)
        if preflight_error is not None:
            raise preflight_error
        preparation_directory(record,"results_identity",0o755)
        atomic_json(results/"cleanup-ledger.json",ledger)
        preparation_directory(record,"root_identity",0o711)
        for folder,role in [(root,"anchor"),(results,"results")]:
            atomic_json(folder/MARKER,{"schema":SCHEMA,"role":role,"run":run,"run_id":args.run_id})
        with open(args.outputs,"a",encoding="utf-8") as stream:
            for key,value in {"root":root,"results":results,"uid_a":uids[0],"uid_b":uids[1]}.items():
                stream.write(f"{key}={value}\n")
            stream.flush()
        prepare_resources(args,root,results,run,ledger,manifest)
        record["ready"]=True
        save_preparation(record)
    except BaseException:
        if not created:
            raise  # exclusive creation failed; no owned record/resources exist
        # Preserve evidence; this is not permission to remove unmarked objects.
        previous=signal.signal(signal.SIGTERM,signal.SIG_IGN)
        try:
            preparation_report(record)
        finally:
            signal.signal(signal.SIGTERM,previous)
        raise


def prepare_resources(args,root,results,run,ledger,manifest):
    import uuid
    gid=run["accounts"][0]["gid"]
    ledger["intents"].append({"kind":"directory","path":str(root/"shared")})
    atomic_json(results/"cleanup-ledger.json",ledger)
    (root/"shared").mkdir(mode=0o755)
    supervisor = Path(__file__).with_name("unix_security_supervisor.py").read_bytes()
    destination = root/"shared"/"supervisor.py"
    ledger["intents"].append({"kind":"supervisor","path":str(destination),"sha256":hashlib.sha256(supervisor).hexdigest()})
    atomic_json(results/"cleanup-ledger.json",ledger)
    fd = os.open(destination, os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW, 0o755)
    with os.fdopen(fd,"wb") as stream:
        stream.write(supervisor);stream.flush();os.fsync(stream.fileno())
    sync_dir(root/"shared")
    ledger["objects"].append({"path":str(destination),"identity":identity(destination),"sha256":hashlib.sha256(supervisor).hexdigest()})
    atomic_json(results/"cleanup-ledger.json",ledger)
    for entry in ledger["accounts"]:
        name, uid = entry["name"], entry["uid"]
        if sys.platform == "linux":
            owned_prepare_command(["/usr/sbin/useradd", "--system", "--no-create-home", "--no-user-group", "--gid", str(gid), "--uid", str(uid), "--shell", "/usr/sbin/nologin", name], root, results, ledger)
        else:
            generated = str(uuid.uuid4()).upper()
            ledger["intents"].append({"kind": "directory-account", "name": name, "generated_uid": generated})
            atomic_json(results/"cleanup-ledger.json", ledger)
            record = "/Users/"+name
            for fields in [["GeneratedUID", generated], ["UniqueID", str(uid)], ["PrimaryGroupID", "20"], ["UserShell", "/usr/bin/false"], ["NFSHomeDirectory", "/var/empty"]]:
                owned_prepare_command(["/usr/bin/dscl", ".", "-create", record, *fields], root, results, ledger)
        if account(name) != entry:
            raise PermissionError("created account identity mismatch")
    for name, mode in [("tmp", 0o700), ("install", 0o755), ("go-cache",0o700), ("go-modules",0o700), ("go-path",0o700), ("telemetry",0o755)]:
        ledger["intents"].append({"kind": "directory", "path": str(root/name)})
        atomic_json(results/"cleanup-ledger.json", ledger)
        (root/name).mkdir(mode=mode)
    ledger["intents"].append({"kind":"telemetry-mode","path":str(root/"telemetry"/"mode")})
    atomic_json(results/"cleanup-ledger.json",ledger)
    with open(root/"telemetry"/"mode","x") as stream:
        stream.write("off\n");stream.flush();os.fsync(stream.fileno())
    os.chmod(root/"telemetry"/"mode",0o644)
    sync_dir(root/"telemetry")
    # User log capabilities open their entire ancestry read-only. Their private
    # homes therefore live under the independently marked 0755 results root,
    # outside the deliberately search-only 0711 machine anchor.
    ledger["intents"].append({"kind":"directory","path":str(results/"users")})
    atomic_json(results/"cleanup-ledger.json",ledger)
    (results/"users").mkdir(mode=0o755)
    for label, owner in zip(("a", "b"), ledger["accounts"]):
        user_root = results/"users"/label
        ledger["intents"].append({"kind":"user-directory","path":str(user_root),"uid":owner["uid"],"gid":owner["gid"]})
        atomic_json(results/"cleanup-ledger.json",ledger)
        user_root.mkdir(mode=0o700)
        os.chown(user_root, owner["uid"], owner["gid"])
    for artifact in manifest["binaries"]:
        source = Path(args.build)/artifact["name"]
        data = source.read_bytes()
        if hashlib.sha256(data).hexdigest() != artifact["sha256"] or len(data) != artifact["size"]:
            raise PermissionError("precompiled test binary changed")
        destination = root/"shared"/artifact["name"]
        ledger["intents"].append({"kind": "binary", "path": str(destination), "sha256": artifact["sha256"]})
        atomic_json(results/"cleanup-ledger.json", ledger)
        fd = os.open(destination, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o755)
        with os.fdopen(fd, "wb") as stream:
            stream.write(data)
            stream.flush()
            os.fsync(stream.fileno())
        if hashlib.sha256(destination.read_bytes()).hexdigest() != artifact["sha256"]:
            raise PermissionError("staged executable checksum mismatch")
        artifact["identity"] = identity(destination)
    atomic_json(results/"binaries.json", manifest)
    atomic_json(results/"cleanup-ledger.json", ledger)



def release_launchd_descendants(directory):
    """Release only the validated fixture's private, non-signaling barrier."""
    trusted_chain(directory)
    observed = identity(directory)
    if observed["uid"] != 0 or observed["mode"] != 0o700:
        raise PermissionError("invalid launchd fixture directory")
    barrier = directory/"release-descendants"
    try:
        current = read_private(barrier)
    except FileNotFoundError:
        atomic_json(barrier, "release")
        return
    if current != "release":
        raise PermissionError("invalid launchd fixture release")


def cleanup_launchd_jobs(root, run_id):
    """Recover only this isolated run's durably recorded launchd fixtures."""
    if not re.fullmatch(r"[a-f0-9]{12}", run_id):
        raise PermissionError("invalid launchd fixture run")
    directory = root/"launchd-jobs"
    try:
        os.lstat(directory)
    except FileNotFoundError:
        return
    trusted_chain(directory)
    observed = identity(directory)
    if observed["uid"] != 0 or observed["mode"] != 0o700:
        raise PermissionError("invalid launchd fixture directory")
    for path in sorted(directory.iterdir()):
        if not re.fullmatch(r"[a-f0-9]{16}\.json", path.name):
            raise PermissionError("unknown launchd fixture record")
        nonce = path.stem
        label = "com.mihari.security."+run_id+".shared-group."+nonce
        plist = str(root/("launchd-bootout-"+nonce)/"job.plist")
        def read_record():
            record = read_private(path)
            if (not isinstance(record, dict) or set(record) != {"schema", "label", "plist", "pgid"}
                    or record["schema"] != "mihari.security-launchd-job/v1"
                    or record["label"] != label or record["plist"] != plist
                    or type(record["pgid"]) is not int
                    or record["pgid"] not in (0,) and not 1 < record["pgid"] <= 2147483647):
                raise PermissionError("invalid launchd fixture record")
            return record
        def launchctl(*args):
            return subprocess.run(["/bin/launchctl", *args, "system/"+label],
                                  capture_output=True, check=False, timeout=8,
                                  env={"PATH": "/usr/bin:/bin", "LANG": "C", "LC_ALL": "C"}).returncode
        def absent():
            status = launchctl("print")
            if status not in (0, 113):
                raise OSError("cannot inspect isolated launchd job")
            return status == 113
        before = read_record()
        if not absent():
            # A concurrent natural exit can make bootout fail. Its return value
            # is never the absence proof; the bounded print check below is.
            launchctl("bootout")
            deadline = time.monotonic()+10
            while not absent():
                if time.monotonic() >= deadline:
                    raise OSError("isolated launchd job remains loaded")
                time.sleep(0.1)
        # The helper publishes its PGID before starting descendants. Re-read
        # after unload to cover a publication racing the first record read.
        after = read_record()
        if before["pgid"] and after["pgid"] != before["pgid"]:
            raise PermissionError("launchd fixture group identity changed")
        if not after["pgid"]:
            # print-not-found does not join a helper that may still publish.
            # Keep the anchor for a later retry, even if bootstrap itself failed.
            raise OSError("isolated launchd group was not published")
        # The Go test may die before its own t.Cleanup. Release the exact
        # private helper barrier before waiting; never signal a historic PGID.
        release_launchd_descendants(Path(after["plist"]).parent)
        deadline = time.monotonic()+10
        while True:
            try:
                os.killpg(after["pgid"], 0)
            except ProcessLookupError:
                break
            # Never signal a historical group; unknown or reused groups
            # retain the marked anchor and fail cleanup.
            if time.monotonic() >= deadline:
                raise OSError("isolated launchd group remains")
            time.sleep(0.1)


class Host:
    def __init__(self, run):
        self.run = run
        self.root = Path(run["root"])
        self.results = Path(run["results"])
        try:
            self.ledger = read_private(self.results/"cleanup-ledger.json")
        except FileNotFoundError:
            self.ledger = load_preparation(self.root,self.results)["ledger"]
    def save(self):
        atomic_json(self.results/"cleanup-ledger.json", self.ledger)
    def archive(self):
        load_run(self.root, self.results, allow_missing_root=True)
        mount_record = self.root/"mount-intent.json"
        if mount_record.exists():
            record = read_private(mount_record)
            if record.get("source") != str(self.root/"rootpolicy-bind"/"source") or record.get("target") != str(self.root/"rootpolicy-bind"/"mount"):
                raise PermissionError("mount intent escaped anchor")
            self.ledger["mounts"] = [record]
            self.save()
        # Independent ledger exists before mutation; archive a synced immutable
        # copy before the anchor can be removed. Raw events are never uploaded.
        atomic_json(self.results/"cleanup-archive.json", self.ledger)
        for name in ("events.jsonl", "launcher.stderr"):
            if not (self.root/name).exists():
                continue
            data = (self.root/name).read_bytes()
            destination = self.results/name
            fd = os.open(destination, os.O_WRONLY | os.O_CREAT | os.O_TRUNC | os.O_NOFOLLOW, 0o600)
            with os.fdopen(fd, "wb") as stream:
                stream.write(data)
                stream.flush()
                os.fsync(stream.fileno())
        sync_dir(self.results)
    def cleanup(self, stage):
        load_run(self.root, self.results, allow_missing_root=True)
        if stage == "processes":
            for entry in self.ledger["processes"]:
                pid = entry.get("pid")
                if not pid:
                    continue
                observed = process_identity(pid)
                if observed is None:
                    continue
                if observed != entry["identity"]:
                    raise OSError("process identity changed")
                os.killpg(pid, signal.SIGTERM)
                for _ in range(50):
                    if process_identity(pid) is None:
                        break
                    time.sleep(0.1)
                else:
                    raise OSError("owned process did not terminate")
            if sys.platform == "darwin":
                cleanup_launchd_jobs(self.root, self.run["run_id"])
        elif stage == "mounts":
            # Linux bind mounts live exclusively in a verified private namespace.
            # The owning process is joined first; namespace destruction removes
            # them. Refuse any host-visible mount inside the anchor.
            for entry in self.ledger.get("mounts", []):
                if sys.platform != "linux" or os.readlink("/proc/self/ns/mnt") != entry["namespace"]:
                    continue  # owning private namespace was destroyed after join
                target, source = Path(entry["target"]), Path(entry["source"])
                if not target.exists():
                    continue
                current = identity(target)
                if current == entry["source_identity"]:
                    if identity(source) != entry["source_identity"]:
                        raise PermissionError("mount source identity changed")
                    subprocess.run(["/bin/umount", str(target)], check=True)
                elif current != entry["mount_identity"]:
                    raise PermissionError("mount target identity changed")
                if identity(target) != entry["mount_identity"]:
                    raise PermissionError("mount cleanup did not restore target")
            output = subprocess.check_output(["/bin/mount" if sys.platform == "linux" else "/sbin/mount"], text=True)
            if str(self.root)+"/" in output:
                raise OSError("fixture mount remains visible")
        elif stage == "accounts":
            for entry in self.ledger["accounts"]:
                actual = account(entry["name"])
                if sys.platform == "darwin":
                    intent = next((i for i in self.ledger["intents"] if i.get("kind")=="directory-account" and i["name"]==entry["name"]), None)
                    if intent is None:
                        if actual is None:
                            continue  # preparation never attempted this account
                        raise PermissionError("unattempted account unexpectedly exists")
                    if actual is not None and actual != entry:
                        raise OSError("account identity changed")
                    if not darwin_account_present(entry["name"]):
                        continue  # an earlier cleanup already removed this record
                    found = subprocess.run(["/usr/bin/dscl", ".", "-read", "/Users/"+entry["name"], "GeneratedUID"],capture_output=True,text=True,check=False)
                    if found.returncode or found.stdout.strip() != "GeneratedUID: "+intent["generated_uid"]:
                        raise OSError("directory-services account identity changed")
                elif actual is None:
                    continue
                if actual is not None and actual != entry:
                    raise OSError("account identity changed")
                if sys.platform == "linux":
                    subprocess.run(["/usr/sbin/userdel", entry["name"]], check=True)
                else:
                    subprocess.run(["/usr/bin/dscl", ".", "-delete", "/Users/"+entry["name"]], check=True)
                remains = darwin_account_present(entry["name"]) if sys.platform == "darwin" else account(entry["name"]) is not None
                if remains:
                    raise OSError("account cleanup incomplete")
        elif stage == "anchor" and self.root.exists():
            if identity(self.root) != self.run["root_identity"]:
                raise OSError("anchor identity changed")
            shutil.rmtree(self.root)
            sync_dir(self.root.parent)
            if self.root.exists():
                raise OSError("anchor cleanup incomplete")


def process_identity(pid):
    result = subprocess.run(["/bin/ps", "-ww", "-p", str(pid), "-o", "uid=,lstart=,args="], capture_output=True, text=True, check=False)
    if result.returncode == 1 and not result.stdout.strip():
        return None
    if result.returncode or not result.stdout.strip():
        raise OSError("cannot verify process identity")
    return result.stdout.strip()


def require_private_namespace():
    if os.readlink("/proc/self/ns/mnt")==os.readlink("/proc/1/ns/mnt"):
        raise PermissionError("private mount namespace required")
    rows=Path("/proc/self/mountinfo").read_text().splitlines()
    if not rows:
        raise PermissionError("mount propagation unavailable")
    for row in rows:
        fields=row.split(" - ",1)[0].split()
        if len(fields)<6 or any(f.startswith(("shared:","master:","propagate_from:")) for f in fields[6:]):
            raise PermissionError("mount namespace propagation is not private")


def run_tests(args):
    run = load_run(args.root, str(Path(args.report).parent), [args.uid_a, args.uid_b])
    if Path(args.report) != Path(run["results"])/"result.json":
        raise PermissionError("fixed report basename required")
    host = Host(run)
    report = {"schema": "mihari.unix-security-result/v1", "os": sys.platform, "run_id": run["run_id"], "root_identity": run["root_identity"], "uids": run["uids"], "gids": [a["gid"] for a in run["accounts"]], "failures": [], "cleanup": {}, "passed": False}
    status, active = 0, None
    deadline = time.monotonic()+EXECUTION_TIMEOUT_SECONDS
    def terminate(signum, _frame):
        raise InterruptedError(signum)
    old_term = signal.signal(signal.SIGTERM, terminate)
    old_int = signal.signal(signal.SIGINT, terminate)
    try:
        go = Path(os.environ["MIHARI_SECURITY_GO"])
        if not go.is_absolute() or not go.is_file():
            raise PermissionError("fixed absolute Go tool required")
        manifest = read_private(host.results/"binaries.json")
        for key in ("go","python"):
            tool=manifest["tools"][key]
            selected=os.environ["MIHARI_SECURITY_"+key.upper()]
            if selected != tool["path"] or not Path(selected).is_absolute():
                raise PermissionError("tool path changed")
            data=Path(selected).read_bytes()
            if len(data)!=tool["size"] or hashlib.sha256(data).hexdigest()!=tool["sha256"]:
                raise PermissionError("fixed tool executable changed")
        script=host.root/"shared"/"supervisor.py"
        staged=next((entry for entry in host.ledger["objects"] if entry["path"]==str(script)),None)
        if staged is None or identity(script)!=staged["identity"] or hashlib.sha256(script.read_bytes()).hexdigest()!=staged["sha256"]:
            raise PermissionError("staged supervisor identity changed")
        environment = {"PATH": "/usr/bin:/bin:/usr/sbin:/sbin", "CI": "true", "GITHUB_ACTIONS": "true", "RUNNER_ENVIRONMENT": "github-hosted", "MIHARI_ISOLATED_SECURITY_CI": "1", "MIHARI_SECURITY_ROOT": str(host.root), "MIHARI_SECURITY_RESULTS": str(host.results), "MIHARI_SECURITY_UID_A": str(args.uid_a), "MIHARI_SECURITY_UID_B": str(args.uid_b), "MIHARI_NATIVE_INSTALL_TEST": "1", "TMPDIR": str(host.root/"tmp"), "GOTOOLCHAIN": "local", "GOENV": "off", "GOPROXY": "off", "GOSUMDB": "off"}
        environment.update(GOCACHE=str(host.root/"go-cache"),GOMODCACHE=str(host.root/"go-modules"),GOPATH=str(host.root/"go-path"),TEST_TELEMETRY_DIR=str(host.root/"telemetry"))
        if sys.platform == "linux":
            require_private_namespace()
            environment["MIHARI_SECURITY_MOUNT_NAMESPACE"] = "isolated"
        with open(host.root/"events.jsonl", "xb", buffering=0) as events, open(host.root/"launcher.stderr", "xb", buffering=0) as diagnostics:
            os.chmod(host.root/"events.jsonl", 0o600)
            os.chmod(host.root/"launcher.stderr", 0o600)
            # Full assembly owns fresh B, then retain its completed fixture under
            # an identity-checked new name. No deletion/marker edit is involved.
            phases = [("cmd/mihari", ["TestUnixSecurity_FullAssembly"], False)]
            for package in ["internal/platform", "internal/control/transport", "internal/integration", "internal/app", "internal/service", "internal/core", "internal/supervisor"]:
                names = list(COMMON.get(package, []))+list(SUPPLEMENTAL.get(package, []))
                if sys.platform == "darwin":
                    names.extend(DARWIN_SUPPLEMENTAL.get(package, []))
                if package == "internal/platform":
                    names.append("TestSecurityBindMountDenied" if sys.platform == "linux" else "TestSecurityDarwinACLABI")
                phases.append((package, names, False))
            phases.append(("cmd/mihari", ["TestProcess_SIGTERMJoinsCleanup", "TestProcess_SetupPreservesClassifiedError", "TestUnixProcess_LocalFailureExitContracts"], True))
            for index, (package, names, ordinary) in enumerate(phases):
                artifact = next(item for item in manifest["binaries"] if item["package"] == PREFIX+package)
                binary = host.root/"shared"/artifact["name"]
                if identity(binary) != artifact["identity"] or hashlib.sha256(binary.read_bytes()).hexdigest() != artifact["sha256"]:
                    raise PermissionError("staged executable identity changed")
                child_env = environment.copy()
                credential = {}
                if ordinary:
                    owner = run["accounts"][0]
                    child_env.pop("MIHARI_SECURITY_ROOT")
                    child_env.pop("MIHARI_ISOLATED_SECURITY_CI")
                    user_root = host.results/"users"/"a"
                    child_env["TMPDIR"] = str(user_root)
                    child_env.update(GOCACHE=str(user_root/"go-cache"), GOMODCACHE=str(user_root/"go-modules"), GOPATH=str(user_root/"go-path"), TEST_TELEMETRY_DIR=str(user_root/"telemetry"))
                    credential = {"user": owner["uid"], "group": owner["gid"], "extra_groups": []}
                package_timeout = APP_TIMEOUT_SECONDS if package == "internal/app" else PACKAGE_TIMEOUT_SECONDS
                command = [str(go), "tool", "test2json", "-t", "-p", PREFIX+package, str(binary), "-test.v=test2json", "-test.count=1", "-test.timeout="+str(max(1,min(package_timeout,int(deadline-time.monotonic()))))+"s", "-test.run=^("+"|".join(names)+")$"]
                intent = {"package": PREFIX+package, "nonce": str(time.monotonic_ns())}
                host.ledger["processes"].append(intent)
                host.save()
                gate_read, gate_write = os.pipe()
                supervisor = [os.environ["MIHARI_SECURITY_PYTHON"], str(host.root/"shared"/"supervisor.py"), str(gate_read), "--owner="+intent["nonce"], *command]
                try:
                    active = subprocess.Popen(supervisor, cwd=str(host.root/"shared") if ordinary else str(Path(args.source)/package), env=child_env, stdout=events, stderr=diagnostics, start_new_session=True, pass_fds=(gate_read,), **credential)
                    intent.update(pid=active.pid, identity=process_identity(active.pid))
                    host.save()  # supervisor cannot spawn a test until this sync
                    os.write(gate_write,b"G")
                finally:
                    os.close(gate_read);os.close(gate_write)
                code = active.wait(timeout=max(1, deadline-time.monotonic()))
                active = None
                status |= int(code != 0)
                if index == 0:
                    base = host.root/"system"
                    if base.exists():
                        observed = identity(base)
                        host.ledger["intents"].append({"kind": "retain-phase", "path": str(base), "identity": observed})
                        host.save()
                        if identity(base) != observed or (host.root/"assembly-system").exists():
                            raise PermissionError("phase fixture identity changed")
                        base.rename(host.root/"assembly-system")
                        sync_dir(host.root)
            os.fsync(events.fileno())
            os.fsync(diagnostics.fileno())
        with open(host.root/"events.jsonl", encoding="utf-8") as stream:
            parsed = [json.loads(line) for line in stream if line.strip()]
        verdict = verify(parsed, sys.platform, run["uids"], status, supplemental=True)
        report.update(verdict)
        status |= int(not verdict["passed"])
    except (OSError, ValueError, KeyError, subprocess.SubprocessError) as error:
        status = 1
        report["failures"].append(type(error).__name__)
    finally:
        signal.signal(signal.SIGTERM, signal.SIG_IGN)
        signal.signal(signal.SIGINT, signal.SIG_IGN)
        if active is not None:
            try:
                os.killpg(active.pid, signal.SIGTERM)
                active.wait(timeout=10)
            except (OSError, subprocess.SubprocessError):
                status = 1
        report = finish(host, report, status)
        atomic_json(args.report, report, 0o644)
        signal.signal(signal.SIGTERM, old_term)
        signal.signal(signal.SIGINT, old_int)
    return report["exit_status"]


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("action", choices=("prepare", "run", "recover", "clean-results"))
    parser.add_argument("--root")
    parser.add_argument("--uid-a", type=int)
    parser.add_argument("--uid-b", type=int)
    parser.add_argument("--report")
    parser.add_argument("--run-id")
    parser.add_argument("--build")
    parser.add_argument("--outputs")
    parser.add_argument("--source", default=os.getcwd())
    args = parser.parse_args()
    try:
        guard()
        if args.action == "prepare":
            prepare(args)
            return 0
        if not args.root or not args.report:
            parser.error("root and report required")
        if args.action == "run":
            if args.uid_a is None or args.uid_b is None:
                parser.error("two UID arguments required")
            return run_tests(args)
        run = load_run(args.root, str(Path(args.report).parent), allow_missing_root=True)
        if Path(args.report) != Path(run["results"])/"result.json":
            raise PermissionError("fixed report basename required")
        if args.action == "recover":
            path = Path(args.report)
            report = json.loads(path.read_text()) if path.exists() else {"schema": "mihari.unix-security-result/v1", "run_id": run["run_id"], "passed": False, "failures": ["interrupted-before-report"], "cleanup": {}}
            report = finish(Host(run), report, 0)
            atomic_json(path, report, 0o644)
            return report["exit_status"]
        if Path(args.root).exists():
            raise PermissionError("anchor remains; preserve results for recovery")
        preparation=preparation_path(args.root)
        if preparation.exists():
            load_preparation(Path(args.root),Path(args.report).parent)
        shutil.rmtree(Path(args.report).parent)
        if preparation.exists():
            preparation.unlink()
        sync_dir(Path(args.report).parent.parent)
        return 0
    except (OSError, ValueError, KeyError, subprocess.SubprocessError):
        print("isolated security runner refused or failed; private ledger retained", file=sys.stderr)
        return 2


if __name__ == "__main__":
    sys.exit(main())
