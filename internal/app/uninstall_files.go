package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// UninstallTarget is one resolved root whose generated entries may be removed.
type UninstallTarget struct {
	Path string
	Kind string
}

// UninstallFileError reports an entry that cannot be safely recognized.
type UninstallFileError struct {
	Kind         string
	Path         string
	RelativePath string
	Reason       string
}

func (e *UninstallFileError) Error() string {
	if e.Reason != "" {
		return fmt.Sprintf("unrecognized %s in %s: %s", e.Reason, e.Kind, e.RelativePath)
	}
	return fmt.Sprintf("unrecognized entry in %s: %s", e.Kind, e.RelativePath)
}

// CheckUninstallFiles verifies that every existing entry below each target has
// a recognized Mihari-generated name. It never changes the filesystem.
func CheckUninstallFiles(ctx context.Context, targets []UninstallTarget) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, target := range targets {
		if !validUninstallTargetKind(target.Kind) {
			return fmt.Errorf("unknown uninstall target kind %q", target.Kind)
		}
		info, err := os.Lstat(target.Path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect uninstall %s root: %w", target.Kind, err)
		}
		if err := refuseInvalidUninstallRoot(target, info); err != nil {
			return err
		}
		if err := checkUninstallDirectory(ctx, target, target.Path, ""); err != nil {
			return err
		}
	}
	return nil
}

func inspectUninstallRoots(ctx context.Context, targets []UninstallTarget) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, target := range targets {
		if !validUninstallTargetKind(target.Kind) {
			return fmt.Errorf("unknown uninstall target kind %q", target.Kind)
		}
		info, err := os.Lstat(target.Path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect uninstall %s root: %w", target.Kind, err)
		}
		if err := refuseInvalidUninstallRoot(target, info); err != nil {
			return err
		}
	}
	return nil
}

func refuseInvalidUninstallRoot(target UninstallTarget, info os.FileInfo) error {
	if reason := uninstallRootLinkReason(info); reason != "" {
		return newUninstallFileError(target, ".", reason)
	}
	if !info.IsDir() {
		return newUninstallFileError(target, ".", "non-directory root")
	}
	return nil
}

func checkUninstallDirectory(ctx context.Context, target UninstallTarget, path, relative string) error {
	entries, err := os.ReadDir(path)
	if err != nil {
		return fmt.Errorf("read uninstall %s directory: %w", target.Kind, err)
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		entryPath := filepath.Join(path, entry.Name())
		info, err := os.Lstat(entryPath)
		if err != nil {
			return fmt.Errorf("inspect uninstall %s entry: %w", target.Kind, err)
		}
		rel := entry.Name()
		if relative != "" {
			rel = relative + "/" + entry.Name()
		}
		if reason := uninstallRootLinkReason(info); reason != "" {
			return newUninstallFileError(target, rel, reason)
		}
		if !allowedUninstallEntry(target.Kind, rel, info.IsDir()) {
			return newUninstallFileError(target, rel, "")
		}
		if info.IsDir() {
			if err := checkUninstallDirectory(ctx, target, entryPath, rel); err != nil {
				return err
			}
		}
	}
	return nil
}

func newUninstallFileError(target UninstallTarget, relative, reason string) *UninstallFileError {
	return &UninstallFileError{Kind: target.Kind, Path: target.Path, RelativePath: relative, Reason: reason}
}

func validUninstallTargetKind(kind string) bool {
	switch kind {
	case "data", "base", "logs", "program", "control":
		return true
	default:
		return false
	}
}

func allowedUninstallEntry(kind, relative string, isDir bool) bool {
	switch kind {
	case "data":
		return allowedDataEntry(relative, isDir)
	case "base":
		return allowedBaseEntry(relative, isDir)
	case "logs":
		return allowedLogsEntry(relative, isDir)
	case "program":
		return !isDir && (relative == "mihari" || relative == "mihari.exe" || relative == ".mihari-binary.lock")
	case "control":
		return allowedControlEntry(relative, isDir)
	default:
		return false
	}
}

func allowedBaseEntry(relative string, isDir bool) bool {
	if relative == "transactions" {
		return isDir
	}
	if after, ok := strings.CutPrefix(relative, "transactions/"); ok {
		return allowedInstallJournalEntry(after, isDir)
	}
	if relative == "data" {
		return isDir
	}
	if after, ok := strings.CutPrefix(relative, "data/"); ok {
		return allowedDataEntry(after, isDir)
	}
	if relative == "install-control" {
		return isDir
	}
	if after, ok := strings.CutPrefix(relative, "install-control/"); ok {
		return allowedControlEntry(after, isDir)
	}
	return !isDir && (relative == "install-transaction.json" || relative == "control.sock" || relative == "control.token" || relative == "mihari-channel" || relative == "install.lock" || endpointNameFile(relative) || endpointLockFile(relative))
}

func allowedInstallJournalEntry(relative string, isDir bool) bool {
	parts := strings.Split(relative, "/")
	if len(parts) == 1 {
		return isDir && lowercaseHex(parts[0], 32)
	}
	if len(parts) != 2 || isDir || !lowercaseHex(parts[0], 32) {
		return false
	}
	switch parts[1] {
	case "transaction-id", "unit", "unit-bootstrap", "ready.json", "validation-launch.json":
		return true
	default:
		return false
	}
}

func allowedControlEntry(relative string, isDir bool) bool {
	if isDir || strings.Contains(relative, "/") {
		return false
	}
	switch relative {
	case "state.json", "previous-state.json", "operation.lock", "startup.lock":
		return true
	default:
		return false
	}
}

func allowedDataEntry(relative string, isDir bool) bool {
	parts := strings.Split(relative, "/")
	if dataScratchFile(parts, isDir) {
		return true
	}
	if len(parts) == 1 {
		return allowedDataRootEntry(parts[0], isDir)
	}
	switch parts[0] {
	case "bin":
		return allowedBinEntry(parts[1:], isDir)
	case "runtime":
		return allowedRuntimeEntry(parts[1:], isDir)
	case "subscriptions":
		return allowedSubscriptionsEntry(parts[1:], isDir)
	case "geoip":
		return allowedGeoIPEntry(parts[1:], isDir)
	case "preferences":
		return len(parts) == 2 && !isDir && (parts[1] == "tui.json" || atomicTemp(parts[1], "tui.json"))
	case "providers":
		return len(parts) == 2 && !isDir && providerFile(parts[1])
	case "web":
		return allowedWebEntry(parts[1:], isDir)
	case "ruleset":
		return allowedRulesetEntry(parts[1:], isDir)
	case "logs", "logs-export":
		return allowedLogsEntry(relative, isDir)
	case "staging":
		return allowedStagingEntry(parts[1:], isDir)
	case "locks":
		return len(parts) == 2 && !isDir && parts[1] == "install-data-id"
	case "install-control":
		return allowedControlEntry(strings.Join(parts[1:], "/"), isDir)
	default:
		return false
	}
}

func dataScratchFile(parts []string, isDir bool) bool {
	if isDir || len(parts) == 0 || !lowercaseHexRange(strings.TrimPrefix(parts[len(parts)-1], ".mihari-"), 1, 16) || !strings.HasPrefix(parts[len(parts)-1], ".mihari-") {
		return false
	}
	if len(parts) == 1 {
		return true
	}
	return allowedDataEntry(strings.Join(parts[:len(parts)-1], "/"), true)
}

func allowedDataRootEntry(name string, isDir bool) bool {
	if isDir {
		switch name {
		case "bin", "runtime", "subscriptions", "geoip", "preferences", "providers", "web", "logs", "logs-export", "staging", "locks", "install-control", "ruleset":
			return true
		default:
			return false
		}
	}
	if endpointNameFile(name) || endpointLockFile(name) {
		return true
	}
	switch name {
	case "mihari.yaml", "onboarding.json", "control.token", "daemon.lock", "mihari.yaml.lock", "control.sock", "mihari-channel", "install.lock":
		return true
	default:
		return coreHomeFile(name) || mihomoWorkingFile(name) || atomicTemp(name, "mihari.yaml") || atomicTemp(name, "onboarding.json")
	}
}

func allowedBinEntry(parts []string, isDir bool) bool {
	if len(parts) != 1 || isDir {
		return false
	}
	name := parts[0]
	return name == "mihomo" || name == "mihomo.exe" || name == "mihomo.provenance.json" || name == "core-channel" || canonicalPositiveSuffix(name, "mihomo.exe.old-")
}

func allowedRuntimeEntry(parts []string, isDir bool) bool {
	if len(parts) == 1 {
		return (isDir && parts[0] == "core-home") || (!isDir && (parts[0] == "config.yaml" || atomicTemp(parts[0], "config.yaml")))
	}
	if parts[0] != "core-home" {
		return false
	}
	return allowedCoreHomeEntry(parts[1:], isDir)
}

func allowedCoreHomeEntry(parts []string, isDir bool) bool {
	if len(parts) == 1 {
		if isDir {
			return parts[0] == "providers" || parts[0] == "ruleset"
		}
		return coreHomeFile(parts[0]) || mihomoWorkingFile(parts[0])
	}
	switch parts[0] {
	case "providers":
		return len(parts) == 2 && !isDir && providerFile(parts[1])
	case "ruleset":
		return allowedRulesetEntry(parts[1:], isDir)
	default:
		return false
	}
}

func allowedSubscriptionsEntry(parts []string, isDir bool) bool {
	if len(parts) == 1 {
		return (isDir && parts[0] == "cache") || (!isDir && (parts[0] == "catalog.yaml" || atomicTemp(parts[0], "catalog.yaml")))
	}
	return len(parts) == 2 && parts[0] == "cache" && !isDir && subscriptionCacheFile(parts[1])
}

func allowedGeoIPEntry(parts []string, isDir bool) bool {
	if len(parts) != 1 || isDir {
		return false
	}
	return parts[0] == "GeoLite2-Country.mmdb" || parts[0] == "GeoLite2-Country.mmdb.previous" || parts[0] == "GeoLite2-ASN.mmdb" || parts[0] == "GeoLite2-ASN.mmdb.previous"
}

func allowedWebEntry(parts []string, isDir bool) bool {
	if len(parts) == 0 {
		return false
	}
	if len(parts) == 1 {
		if isDir {
			return panelIDDir(parts[0])
		}
		return parts[0] == "active.json" || parts[0] == "credential" || atomicTemp(parts[0], "active.json")
	}
	if !panelIDDir(parts[0]) {
		return false
	}
	for _, part := range parts[1:] {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func panelIDDir(name string) bool {
	return name == "zashboard" || name == "metacubexd"
}

func allowedRulesetEntry(parts []string, isDir bool) bool {
	return len(parts) == 1 && !isDir && parts[0] != "" && parts[0] != "." && parts[0] != ".."
}

func allowedLogsEntry(relative string, isDir bool) bool {
	parts := strings.Split(relative, "/")
	if len(parts) == 1 {
		return isDir && (parts[0] == "logs" || parts[0] == "logs-export")
	}
	if len(parts) != 2 || isDir {
		return false
	}
	switch parts[0] {
	case "logs":
		return logFile(parts[1])
	case "logs-export":
		return logExportFile(parts[1])
	default:
		return false
	}
}

func allowedStagingEntry(parts []string, isDir bool) bool {
	if len(parts) == 1 {
		return (isDir && (parts[0] == "core" || parts[0] == "providers" || parts[0] == "geoip" || parts[0] == "panels" || parts[0] == "subscriptions")) || (!isDir && (tempName(parts[0], ".mihomo-download-", "") || tempName(parts[0], ".mihomo-candidate-", "")))
	}
	switch parts[0] {
	case "core":
		return allowedCoreStaging(parts[1:], isDir)
	case "providers":
		return allowedProviderStaging(parts[1:], isDir)
	case "geoip":
		return len(parts) == 2 && !isDir && tempName(parts[1], ".candidate-", ".mmdb")
	case "panels":
		return len(parts) == 2 && ((isDir && panelStagingDirectory(parts[1])) || (!isDir && panelStagingZIP(parts[1])))
	case "subscriptions":
		return allowedSubscriptionStaging(parts[1:], isDir)
	default:
		return false
	}
}

func allowedSubscriptionStaging(parts []string, isDir bool) bool {
	if len(parts) != 1 || isDir {
		return false
	}
	name := parts[0]
	if !strings.HasPrefix(name, "config-") || !strings.HasSuffix(name, ".yaml") {
		return false
	}
	return canonicalUint32Decimal(strings.TrimSuffix(strings.TrimPrefix(name, "config-"), ".yaml"))
}

func allowedCoreStaging(parts []string, isDir bool) bool {
	if len(parts) == 1 {
		return (!isDir && parts[0] == "provenance-commit.json") || (isDir && lowercaseHex(parts[0], 32))
	}
	if len(parts) != 2 || !lowercaseHex(parts[0], 32) || isDir {
		return false
	}
	switch parts[1] {
	case "candidate-binary", "candidate-receipt.json", "backup-binary", "backup-receipt.json", "restore-binary", "restore-receipt.json", "quarantine-binary", "quarantine-receipt.json", "transaction-id":
		return true
	default:
		return false
	}
}

func allowedProviderStaging(parts []string, isDir bool) bool {
	if len(parts) == 1 {
		return (!isDir && (parts[0] == "commit.json" || parts[0] == "activation.json")) || (isDir && lowercaseHex(parts[0], 32))
	}
	if len(parts) != 2 || !lowercaseHex(parts[0], 32) || isDir {
		return false
	}
	return parts[1] == "candidate" || parts[1] == "source" || parts[1] == "transaction-id"
}

func endpointNameFile(name string) bool {
	return name == ".mihari-endpoint-name.control.sock"
}

func endpointLockFile(name string) bool {
	return strings.HasPrefix(name, ".mihari-endpoint-") && strings.HasSuffix(name, ".lock") && lowercaseHex(strings.TrimSuffix(strings.TrimPrefix(name, ".mihari-endpoint-"), ".lock"), 64)
}

func atomicTemp(name, base string) bool {
	return tempName(name, "."+base+".tmp-", "")
}

func coreHomeFile(name string) bool {
	for _, base := range []string{"Country.mmdb", "ASN.mmdb", "GeoIP.dat", "GeoSite.dat"} {
		if name == base || strings.HasPrefix(name, base+".old-") && lowercaseHex(strings.TrimPrefix(name, base+".old-"), 32) {
			return true
		}
	}
	return false
}

func mihomoWorkingFile(name string) bool {
	switch name {
	case "cache.db", "cache.db-wal", "cache.db-shm", "geoip.metadb":
		return true
	default:
		return false
	}
}

func providerFile(name string) bool {
	if before, after, ok := strings.Cut(name, ".old-"); ok {
		if !lowercaseHex(after, 32) {
			return false
		}
		name = before
	}
	for _, suffix := range []string{".yaml", ".txt"} {
		if strings.HasSuffix(name, suffix) && lowercaseHex(strings.TrimSuffix(name, suffix), 64) {
			return true
		}
	}
	return false
}

func subscriptionCacheFile(name string) bool {
	if strings.HasSuffix(name, ".yaml") && lowercaseHex(strings.TrimSuffix(name, ".yaml"), 32) {
		return true
	}
	prefix, suffix, ok := strings.Cut(name, ".yaml.tmp-")
	return ok && strings.HasPrefix(prefix, ".") && lowercaseHex(strings.TrimPrefix(prefix, "."), 32) && canonicalUint32Decimal(suffix)
}

func logFile(name string) bool {
	for _, base := range []string{"mihari-daemon.log", "mihomo.log", "mihari-tui.log"} {
		if name == base || name == base+".lock" || canonicalPositiveSuffix(name, base+".") {
			return true
		}
	}
	return false
}

func logExportFile(name string) bool {
	if !strings.HasPrefix(name, "mihari-logs-") || !strings.HasSuffix(name, ".zip") {
		return false
	}
	stem := strings.TrimSuffix(strings.TrimPrefix(name, "mihari-logs-"), ".zip")
	_, suffix, ok := splitLogExportStamp(stem)
	if !ok {
		return false
	}
	return suffix == "" || canonicalPositiveDecimal(suffix)
}

func splitLogExportStamp(stem string) (stamp, suffix string, ok bool) {
	for _, size := range []int{20, 21} {
		if len(stem) < size || !logExportStamp(stem[:size]) {
			continue
		}
		rest := stem[size:]
		if rest == "" {
			return stem[:size], "", true
		}
		if strings.HasPrefix(rest, "-") {
			return stem[:size], rest[1:], true
		}
	}
	return "", "", false
}

func logExportStamp(stem string) bool {
	switch len(stem) {
	case 20:
		return stem[8] == '-' && (stem[15] == '+' || stem[15] == '-') && decimal(stem[:8]) && decimal(stem[9:15]) && decimal(stem[16:20])
	case 21:
		return stem[8] == '-' && stem[15] == '-' && (stem[16] == '+' || stem[16] == '-') && decimal(stem[:8]) && decimal(stem[9:15]) && decimal(stem[17:21])
	default:
		return false
	}
}

func panelStagingDirectory(name string) bool {
	if strings.HasSuffix(name, "-previous-panel") {
		name = strings.TrimSuffix(name, "-previous-panel")
	} else if strings.HasSuffix(name, "-previous") {
		name = strings.TrimSuffix(name, "-previous")
	}
	return panelStagingCandidate(name)
}

func panelStagingZIP(name string) bool {
	return strings.HasPrefix(name, ".") && strings.HasSuffix(name, ".zip") && panelStagingCandidate(strings.TrimSuffix(strings.TrimPrefix(name, "."), ".zip"))
}

func panelStagingCandidate(name string) bool {
	separator := strings.LastIndexByte(name, '-')
	if separator <= 0 || !canonicalUint32Decimal(name[separator+1:]) {
		return false
	}
	return sanitizedPanelBuild(name[:separator])
}

func sanitizedPanelBuild(name string) bool {
	for _, panelID := range []string{"zashboard", "metacubexd"} {
		build, ok := strings.CutPrefix(name, panelID+"-")
		if !ok || build == "" {
			continue
		}
		for _, r := range build {
			if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-' || r == '_' {
				continue
			}
			return false
		}
		return true
	}
	return false
}

func canonicalPositiveSuffix(name, prefix string) bool {
	return strings.HasPrefix(name, prefix) && canonicalPositiveDecimal(strings.TrimPrefix(name, prefix))
}

func canonicalPositiveDecimal(value string) bool {
	return len(value) > 0 && value[0] != '0' && decimal(value)
}

func tempName(name, prefix, suffix string) bool {
	if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
		return false
	}
	value := strings.TrimSuffix(strings.TrimPrefix(name, prefix), suffix)
	return canonicalUint32Decimal(value)
}

func canonicalUint32Decimal(value string) bool {
	if value == "" || len(value) > 1 && value[0] == '0' {
		return false
	}
	_, err := strconv.ParseUint(value, 10, 32)
	return err == nil
}

func decimal(value string) bool {
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return value != ""
}

func lowercaseHex(value string, length int) bool {
	return lowercaseHexRange(value, length, length)
}

func lowercaseHexRange(value string, minLength, maxLength int) bool {
	if len(value) < minLength || len(value) > maxLength {
		return false
	}
	for _, r := range value {
		if r >= '0' && r <= '9' || r >= 'a' && r <= 'f' {
			continue
		}
		return false
	}
	return true
}
