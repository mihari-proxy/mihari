"""Strict evidence and lifecycle policy; this module performs no host operations."""
import json
import re

PREFIX = "github.com/mihari-proxy/mihari/"
COMMON = {
    "internal/platform": ["TestSecurityTrustedRootPositive", "TestSecurityCreationACL", "TestSecurityDirectoryIdentity"],
    "internal/control/transport": ["TestSecurityPeerOwner"],
    "internal/integration": ["TestSecurityTwoUIDControl", "TestSecurityPrivateDataDenied", "TestSecurityOtherUserLogsDenied"],
}
SUPPLEMENTAL = {
    "internal/supervisor": [
        "TestSupervisor_DescendantFailureBlocksMaintenanceAndRestart",
        "TestSupervisor_UnexpectedExitChecksDescendantsBeforeIdleMaintenance",
        "TestSupervisor_MaintenanceWaitsForDescendantsAfterLeaderExit",
    ],
    "internal/core": ["TestSecurityConfigStage_BindCancellationRemovesWrittenFile"],
    "internal/app": [
        "TestNativeInstallReplacement_StandaloneUpgradeWithoutYes",
        "TestNativeInstallReplacement_OfflineDigestCannotClaimAnotherTag",
        "TestNativeInstallReplacement_OfflineVersionBinding",
        "TestNativeInstallReplacement_OfflineProbeTimeoutCleansStage",
        "TestNativeInstallReplacement_UntrustedCandidateIsNotProbed",
        "TestNativeInstallEffects_FilePublicationAndActualBackup",
        "TestNativeInstallEffects_PrivatePublicationRetainsLockIdentity",
        "TestNativeInstallSession_PrivateMetadataBindsRecoveryAuthority",
        "TestNativeBinaryStageCleanup_CrashAndIdentityMismatch",
        "TestNativeInstallBoundary_LifecycleRetainsDataAuthority",
        "TestNativeInstallBoundary_AbsentPathMigrationStagesBothBinaries",
        "TestNativeInstallBoundary_BootstrapResidueMigration",
        "TestNativeInstallBoundary_ServiceIdentityRecovery",
        "TestNativeInstallBoundary_SourceRecoveryRetriesAfterRestore",
        "TestNativeInstallBoundary_UninstallAlreadyMaskedUnit",
        "TestNativeInstallBoundary_StopAlreadyMaskedUnitRecovery",
        "TestNativeInstallState_ForegroundDataPreparationKeepsAuthority",
        "TestNativeInstallSource_RejectsOverlapBeforeStaging",
        "TestNativeInstallState_BootstrapBackupSurvivesPreparationCrash",
        "TestNativeInstallState_BootstrapRollbackRemovesUnreferencedCandidate",
        "TestNativeInstallState_CreateIdentityCoversWholeTreeAndParts",
        "TestNativeLaunchdRecovery_RetainsUnloadedGroupFromBackup",
        "TestNativeInstallState_RejectsBackupBootMismatch",
        "TestInstallRollforward_RequiresRecordedGroupExitBeforeEffects",
        "TestInstallRollforward_RebindsAfterSameSessionBootstrapIntent",
        "TestRecoveryStopAuthority_DoesNotReuseOldGroupForLaterGeneration",
        "TestReadOnlyMigrationSource_OversizeHasMigrationClassification",
        "TestUnixLocalOperation_CancellationRemainsCancellation",
        "TestUnixLocalOperation_PreservesClassifiedErrorAndCause",
        "TestSecurityPrivateServiceActivation", "TestSecurityNativeCrashMatrix", "TestSecurityValidationProcess",
    ],
    "internal/service": [
        "TestNativeDefinitionStore_RestoresOriginalBytesAndMode",
        "TestNativeDefinitionStore_ReadLinkRejectsUnrestorableTarget",
    ],
    "internal/platform": [
        "TestReadOnlySource_UserWritableAncestry",
        "TestReadOnlySource_RejectsLinksAndAnchorReplacement",
        "TestReadOnlySource_RejectsDescendantReplacementDuringRead",
        "TestReadOnlySource_StatRetainsIdentityAndBounds",
        "TestTrustedRoot_ServiceExchangeCommittedOnSyncFailure",
        "TestTrustedRoot_WriteFileReportsPublishedIdentityOnSyncFailure",
        "TestTrustedRoot_WriteFilePublishedIdentityCannotRemoveReplacement",
        "TestTrustedRoot_WriteFileCollisionHasNoPublishedIdentity",
    ],
    "cmd/mihari": ["TestUnixSecurity_FullAssembly", "TestProcess_SIGTERMJoinsCleanup", "TestProcess_SetupPreservesClassifiedError", "TestUnixProcess_LocalFailureExitContracts"],
}

DARWIN_SUPPLEMENTAL = {
    "internal/platform": [
        "TestDarwinGroupHasPeers_IsolatedProcesses",
        "TestDarwinWaitChildExit_PreservesZombieAndIgnoresStop",
    ],
    "internal/supervisor": ["TestDarwinSharedChild_DescendantsAndSignalOwnership"],
    "internal/service": ["TestDarwinLaunchdIdentity_ActualArgumentsAndGroup"],
    "internal/integration": [
        "TestSecurityLaunchdBootoutRequiresSharedProcessGroupExit",
        "TestSecurityLaunchdCleanupRejectsUnpublishedProcessGroup",
        "TestSecurityLaunchdCleanupPropagatesGroupProof",
    ],
}


def required(target_os, supplemental=False):
    if target_os not in ("linux", "darwin"):
        raise ValueError("unsupported security OS")
    result = [(PREFIX+p, n) for p, names in COMMON.items() for n in names]
    result.append((PREFIX+"internal/platform", "TestSecurityBindMountDenied" if target_os == "linux" else "TestSecurityDarwinACLABI"))
    if supplemental:
        result.extend((PREFIX+p, n) for p, names in SUPPLEMENTAL.items() for n in names)
        if target_os == "darwin":
            result.extend((PREFIX+p, n) for p, names in DARWIN_SUPPLEMENTAL.items() for n in names)
    return result


def verify(events, target_os, uids, go_status=0, supplemental=False):
    keys = required(target_os, supplemental)
    terminals = {key: [] for key in keys}
    starts = {key: 0 for key in keys}
    errors, children, roots, assembly = [], [], [], []
    failure_locations = set()
    crash_failures = set()
    local_rows = {}
    for event in events:
        key = (event.get("Package"), event.get("Test"))
        action = event.get("Action")
        if key in terminals:
            if action in ("pass", "fail", "skip"):
                terminals[key].append(action)
            if action == "run":
                starts[key] += 1
        if action == "fail":
            errors.append("Go failure")
            if key[0] == PREFIX+"internal/app" and isinstance(key[1], str):
                case = re.fullmatch(r"TestSecurityNativeCrashMatrix/(fresh-system|fresh-private|migration-path|aio-resources-path|update-running-enabled|update-running-disabled|update-stopped-enabled|update-stopped-disabled)/(reverse-recovery/)?([0-9]{2,3})-[a-z-]+-(before-intent|after-intent|after-effect|after-done)", key[1])
                if case:
                    crash_failures.add(case[1]+("/reverse/" if case[2] else "/forward/")+case[3]+"/"+case[4])
        if key[0] == PREFIX+"cmd/mihari" and isinstance(key[1], str) and key[1].startswith("TestUnixProcess_LocalFailureExitContracts/"):
            if action in ("pass", "fail", "skip"):
                local_rows.setdefault(key[1], []).append(action)
        if action != "output":
            continue
        # Publish source coordinates only, never raw test/child output.
        parent_test = key[1].split("/", 1)[0] if isinstance(key[1], str) else ""
        if (key[0], parent_test) in terminals:
            for match in re.finditer(r"(?m)^\s*([A-Za-z_][A-Za-z0-9_]{0,100}_test\.go):([0-9]{1,6}):", event.get("Output", "")):
                failure_locations.add(key[0]+":"+parent_test+":"+match[1]+":"+match[2])
        for label, collection, permitted in [
            ("security_child=", children, (PREFIX+"internal/integration", "TestSecurityTwoUIDControl")),
            ("security_root=", roots, (PREFIX+"internal/platform", "TestSecurityTrustedRootPositive")),
            ("unix_assembly_result=", assembly, (PREFIX+"cmd/mihari", "TestUnixSecurity_FullAssembly")),
        ]:
            output = event.get("Output", "")
            if key == permitted and label in output:
                try:
                    collection.append(json.loads(output.split(label, 1)[1].strip()))
                except (ValueError, TypeError):
                    errors.append("invalid evidence record")
    checks = {package+":"+name: "pass" if terminals[(package, name)] == ["pass"] and starts[(package, name)] == 1 else "fail" for package, name in keys}
    if any(value != "pass" for value in checks.values()):
        errors.append("missing, duplicate, skipped or failed required test")
    if go_status != 0:
        errors.append("Go process exited unsuccessfully")
    if len(uids) != 2 or len(set(uids)) != 2 or any(type(uid) is not int or uid <= 0 for uid in uids):
        errors.append("invalid user identities")
    if len(children) != 2 or sorted(p.get("euid", 0) for p in children) != sorted(uids) or any(type(p.get("gid")) is not int or not all(p.get(k) is True for k in ("authenticated", "private_denied", "other_denied", "own_log")) for p in children):
        errors.append("missing actual two-user authentication and denial proof")
    if len(roots) != 1 or any(p.get("uid") != 0 or p.get("mode") != 0o711 or not all(type(p.get(k)) is int and p[k] > 0 for k in ("dev", "ino")) or not isinstance(p.get("mount"), str) or not p["mount"] for p in roots):
        errors.append("missing native root identity proof")
    if supplemental:
        expected_rows = {"TestUnixProcess_LocalFailureExitContracts/"+name for name in ("invalid-layout", "daemon-lock", "channel-lock", "unsafe-channel-root", "channel-IO")}
        if set(local_rows) != expected_rows or any(v != ["pass"] for v in local_rows.values()):
            errors.append("local process matrix requires five non-skipped rows")
        if len(assembly) != 2 or sorted(p.get("EUID", 0) for p in assembly) != sorted(uids) or any(not all(p.get(k) is True for k in ("Authenticated", "SettingsDenied", "OtherUserDenied", "V2Export")) for p in assembly):
            errors.append("missing full assembly two-user proof")
    return {"passed": not errors, "checks": checks, "errors": sorted(set(errors)), "source_locations": sorted(failure_locations), "crash_failures": sorted(crash_failures)}


def finish(host, report, status):
    if status:
        report["failures"].append("test-or-signal")
    try:
        host.archive()
    except Exception:
        report["failures"].append("archive")
        report["cleanup"]["archive"] = "fail"
        report["exit_status"] = 1
        report["passed"] = False
        return report
    report["cleanup"]["archive"] = "pass"
    for stage in ("processes", "mounts", "accounts", "anchor"):
        try:
            host.cleanup(stage)
            report["cleanup"][stage] = "pass"
        except Exception as error:
            report["cleanup"][stage] = "fail"
            report["failures"].append(stage)
            # Exception classes/errno are safe diagnostics; messages and raw
            # test output can contain fixture paths or credentials.
            report.setdefault("cleanup_errors", {})[stage] = {"type": type(error).__name__, "errno": getattr(error, "errno", None)}
            known_reasons = {
                "directory-services account identity changed": "directory-account-identity",
                "account identity changed": "account-identity",
                "account cleanup incomplete": "account-still-present",
                "fixture mount remains visible": "mount-still-present",
                "mount target identity changed": "mount-target-identity",
                "mount source identity changed": "mount-source-identity",
                "cannot inspect isolated launchd job": "launchd-job-query",
                "isolated launchd job remains loaded": "launchd-job-still-loaded",
                "isolated launchd group was not published": "launchd-group-unpublished",
                "isolated launchd group remains": "launchd-group-still-present",
            }
            if str(error) in known_reasons:
                report["cleanup_errors"][stage]["reason"] = known_reasons[str(error)]
            # A live process/mount/account makes recursive anchor removal unsafe.
            break
    report["failures"] = sorted(set(report["failures"]))
    report["exit_status"] = int(bool(report["failures"]))
    report["passed"] = report.get("passed", False) and not report["exit_status"]
    return report
