package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/service"
)

func TestUninstaller_PreviewRejectsRelativeCommandDirectory(t *testing.T) {
	layout, data, program := uninstallTestLayout(t)
	writeUninstallFixture(t, data, "mihari.yaml")
	writeUninstallFixture(t, program, platform.InstalledBinaryName())
	service := &uninstallFakeService{status: service.StatusStopped}
	runner := newUninstallTestRunner(t, layout, uninstallCommandTestOptions(service))
	t.Setenv("MIHARI_BIN", filepath.Join("relative", "bin"))

	_, previewErr := runner.Preview(context.Background())
	if previewErr == nil || !strings.Contains(previewErr.Error(), filepath.Join("relative", "bin")) {
		t.Fatalf("Preview error = %v, want the relative MIHARI_BIN value", previewErr)
	}
	err := runner.Run(context.Background(), nil)
	if err == nil || service.uninstallCalls != 0 || !fileExists(data) || !fileExists(program) {
		t.Fatalf("Run error = %v, uninstall calls = %d, data = %t, program = %t; want refusal before service removal", err, service.uninstallCalls, fileExists(data), fileExists(program))
	}
}

func TestUninstaller_RunRemovesMatchingCommandBeforeService(t *testing.T) {
	layout, data, program := uninstallTestLayout(t)
	writeUninstallFixture(t, data, "mihari.yaml")
	writeProgramBytes(t, program, []byte("same-bytes"))
	binDir := t.TempDir()
	command := filepath.Join(binDir, platform.InstalledBinaryName())
	if err := os.WriteFile(command, []byte("same-bytes"), 0o700); err != nil {
		t.Fatal(err)
	}
	service := &uninstallFakeService{status: service.StatusStopped, onUninstall: func() {
		if fileExists(command) {
			t.Errorf("command file %s still existed when service uninstall started", command)
		}
	}}
	runner := newUninstallTestRunner(t, layout, uninstallCommandTestOptions(service))
	t.Setenv("MIHARI_BIN", binDir)

	targets, err := runner.Preview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !previewHasPath(targets, command) {
		t.Fatalf("Preview targets = %#v, want command file %s", targets, command)
	}
	var progress []string
	if err := runner.Run(context.Background(), func(message string) { progress = append(progress, message) }); err != nil {
		t.Fatal(err)
	}
	if fileExists(command) || fileExists(data) || fileExists(program) {
		t.Fatalf("command = %t, data = %t, program = %t", fileExists(command), fileExists(data), fileExists(program))
	}
	if !progressHas(progress, "Removed "+command) || !progressHas(progress, "Mihari has been completely uninstalled") {
		t.Fatalf("progress = %#v", progress)
	}
}

func TestUninstaller_RunForceRemovesMatchingCommandFile(t *testing.T) {
	layout, data, program := uninstallTestLayout(t)
	writeUninstallFixture(t, data, "mihari.yaml")
	writeProgramBytes(t, program, []byte("same-bytes"))
	binDir := t.TempDir()
	command := filepath.Join(binDir, platform.InstalledBinaryName())
	if err := os.WriteFile(command, []byte("same-bytes"), 0o700); err != nil {
		t.Fatal(err)
	}
	service := &uninstallFakeService{status: service.StatusStopped}
	runner := newUninstallTestRunner(t, layout, uninstallCommandTestOptions(service))
	t.Setenv("MIHARI_BIN", binDir)

	if err := runner.RunForce(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if fileExists(command) {
		t.Fatal("command file remained after forced uninstall")
	}
}

func TestUninstaller_RunLeavesMismatchedCommandAndFails(t *testing.T) {
	layout, data, program := uninstallTestLayout(t)
	writeUninstallFixture(t, data, "mihari.yaml")
	writeProgramBytes(t, program, []byte("installed"))
	binDir := t.TempDir()
	command := filepath.Join(binDir, platform.InstalledBinaryName())
	if err := os.WriteFile(command, []byte("different"), 0o700); err != nil {
		t.Fatal(err)
	}
	service := &uninstallFakeService{status: service.StatusStopped}
	runner := newUninstallTestRunner(t, layout, uninstallCommandTestOptions(service))
	t.Setenv("MIHARI_BIN", binDir)

	targets, err := runner.Preview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if previewHasPath(targets, command) {
		t.Fatalf("Preview listed a command file that will not be deleted: %#v", targets)
	}
	var progress []string
	err = runner.Run(context.Background(), func(message string) { progress = append(progress, message) })
	if err == nil || !strings.Contains(err.Error(), command) || strings.Contains(err.Error(), "Remove-Item") || fileExists(command) == false || fileExists(data) || fileExists(program) {
		t.Fatalf("Run error = %v, command = %t, data = %t, program = %t", err, fileExists(command), fileExists(data), fileExists(program))
	}
	if !progressHas(progress, command) || progressHas(progress, "Mihari has been completely uninstalled") {
		t.Fatalf("progress = %#v", progress)
	}
}

func TestUninstaller_RunSkipsMissingCommandFile(t *testing.T) {
	layout, data, program := uninstallTestLayout(t)
	writeUninstallFixture(t, data, "mihari.yaml")
	writeUninstallFixture(t, program, platform.InstalledBinaryName())
	binDir := t.TempDir()
	service := &uninstallFakeService{status: service.StatusStopped}
	runner := newUninstallTestRunner(t, layout, uninstallCommandTestOptions(service))
	t.Setenv("MIHARI_BIN", binDir)

	var progress []string
	if err := runner.Run(context.Background(), func(message string) { progress = append(progress, message) }); err != nil {
		t.Fatal(err)
	}
	if !progressHas(progress, "Mihari has been completely uninstalled") {
		t.Fatalf("progress = %#v", progress)
	}
}

func TestUninstaller_RunLeavesCommandDirectory(t *testing.T) {
	layout, data, program := uninstallTestLayout(t)
	writeUninstallFixture(t, data, "mihari.yaml")
	writeProgramBytes(t, program, []byte("installed"))
	binDir := t.TempDir()
	command := filepath.Join(binDir, platform.InstalledBinaryName())
	if err := os.Mkdir(command, 0o700); err != nil {
		t.Fatal(err)
	}
	service := &uninstallFakeService{status: service.StatusStopped}
	runner := newUninstallTestRunner(t, layout, uninstallCommandTestOptions(service))
	t.Setenv("MIHARI_BIN", binDir)

	err := runner.Run(context.Background(), nil)
	if err == nil || !fileExists(command) || fileExists(data) || fileExists(program) {
		t.Fatalf("Run error = %v, command dir = %t, data = %t, program = %t", err, fileExists(command), fileExists(data), fileExists(program))
	}
}

func TestUninstallCommandPath_UsesDefaultDirectoryWhenUnset(t *testing.T) {
	t.Setenv("MIHARI_BIN", "")
	wantDir := "/usr/local/bin"
	if runtime.GOOS == "windows" {
		root := t.TempDir()
		t.Setenv("LOCALAPPDATA", root)
		wantDir = filepath.Join(root, "Programs", "mihari")
	}
	got, err := uninstallCommandPath()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(wantDir, platform.InstalledBinaryName())
	if got != want {
		t.Fatalf("command path = %s, want %s", got, want)
	}
}

func TestUninstallCommandPath_RejectsNonAbsoluteDefaultDirectory(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the unix default command directory is absolute")
	}
	t.Setenv("MIHARI_BIN", "")
	t.Setenv("LOCALAPPDATA", "")
	_, err := uninstallCommandPath()
	if err == nil || !strings.Contains(err.Error(), filepath.Join("Programs", "mihari")) {
		t.Fatalf("error = %v, want the non-absolute default directory", err)
	}
}

func TestUninstaller_RunUnlinksMatchingCommandSymlink(t *testing.T) {
	layout, data, program := uninstallTestLayout(t)
	writeUninstallFixture(t, data, "mihari.yaml")
	payload := []byte("same-bytes")
	writeProgramBytes(t, program, payload)
	outside := filepath.Join(t.TempDir(), "outside-mihari")
	if err := os.WriteFile(outside, payload, 0o700); err != nil {
		t.Fatal(err)
	}
	binDir := t.TempDir()
	command := filepath.Join(binDir, platform.InstalledBinaryName())
	if err := os.Symlink(outside, command); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	service := &uninstallFakeService{status: service.StatusStopped}
	runner := newUninstallTestRunner(t, layout, uninstallCommandTestOptions(service))
	t.Setenv("MIHARI_BIN", binDir)

	if err := runner.Run(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if fileExists(command) {
		t.Fatal("command symlink remained")
	}
	got, err := os.ReadFile(outside)
	if err != nil || string(got) != string(payload) {
		t.Fatalf("outside target = %q, err = %v", got, err)
	}
}

func TestUninstaller_RunStopsWhenMatchingCommandCannotBeRemoved(t *testing.T) {
	layout, data, program := uninstallTestLayout(t)
	writeUninstallFixture(t, data, "mihari.yaml")
	payload := []byte("same-bytes")
	writeProgramBytes(t, program, payload)
	binDir := t.TempDir()
	command := filepath.Join(binDir, platform.InstalledBinaryName())
	if err := os.WriteFile(command, payload, 0o700); err != nil {
		t.Fatal(err)
	}
	service := &uninstallFakeService{status: service.StatusStopped}
	runner := newUninstallTestRunner(t, layout, uninstallCommandTestOptions(service))
	t.Setenv("MIHARI_BIN", binDir)
	restoreUninstallCommandRemove(t)
	commandRemoveFailureIsFatal = true
	removeUninstallCommandFile = func(string) error { return errors.New("unlink failed") }

	var progress []string
	err := runner.Run(context.Background(), func(message string) { progress = append(progress, message) })
	if err == nil || !strings.Contains(err.Error(), command) || service.uninstallCalls != 0 || !fileExists(command) || !fileExists(data) || !fileExists(program) {
		t.Fatalf("Run error = %v, calls = %d, command = %t, data = %t, program = %t", err, service.uninstallCalls, fileExists(command), fileExists(data), fileExists(program))
	}
	if progressHas(progress, "Mihari has been completely uninstalled") {
		t.Fatalf("progress = %#v", progress)
	}
}

func TestUninstaller_RunContinuesWhenCommandRemoveIsNotFatal(t *testing.T) {
	layout, data, program := uninstallTestLayout(t)
	writeUninstallFixture(t, data, "mihari.yaml")
	payload := []byte("same-bytes")
	writeProgramBytes(t, program, payload)
	binDir := t.TempDir()
	command := filepath.Join(binDir, platform.InstalledBinaryName())
	if err := os.WriteFile(command, payload, 0o700); err != nil {
		t.Fatal(err)
	}
	service := &uninstallFakeService{status: service.StatusStopped}
	runner := newUninstallTestRunner(t, layout, uninstallCommandTestOptions(service))
	t.Setenv("MIHARI_BIN", binDir)
	restoreUninstallCommandRemove(t)
	commandRemoveFailureIsFatal = false
	removeUninstallCommandFile = func(string) error { return errors.New("text file busy") }

	var progress []string
	err := runner.Run(context.Background(), func(message string) { progress = append(progress, message) })
	wantCmd := manualUninstallCommand(command)
	if err == nil || !strings.Contains(err.Error(), wantCmd) || strings.Contains(err.Error(), "separate copy") || service.uninstallCalls != 1 || !fileExists(command) || fileExists(data) || fileExists(program) {
		t.Fatalf("Run error = %v, calls = %d, command = %t, data = %t, program = %t", err, service.uninstallCalls, fileExists(command), fileExists(data), fileExists(program))
	}
	if !progressHas(progress, wantCmd) || progressHas(progress, "Mihari has been completely uninstalled") {
		t.Fatalf("progress = %#v", progress)
	}
}

func TestManualUninstallCommand_QuotesPathForPowerShell(t *testing.T) {
	got := manualUninstallCommand(`C:\mihari's\mihari.exe`)
	want := "Remove-Item -LiteralPath 'C:\\mihari''s\\mihari.exe' -Force"
	if got != want {
		t.Fatalf("command = %s, want %s", got, want)
	}
}

func TestUninstaller_RunLeavesCommandWhenInstalledProgramMissing(t *testing.T) {
	layout, data, program := uninstallTestLayout(t)
	writeUninstallFixture(t, data, "mihari.yaml")
	if err := os.MkdirAll(program, 0o700); err != nil {
		t.Fatal(err)
	}
	binDir := t.TempDir()
	command := filepath.Join(binDir, platform.InstalledBinaryName())
	if err := os.WriteFile(command, []byte("orphan"), 0o700); err != nil {
		t.Fatal(err)
	}
	service := &uninstallFakeService{status: service.StatusStopped}
	runner := newUninstallTestRunner(t, layout, uninstallCommandTestOptions(service))
	t.Setenv("MIHARI_BIN", binDir)

	targets, err := runner.Preview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if previewHasPath(targets, command) {
		t.Fatalf("Preview listed a command file that cannot be matched: %#v", targets)
	}
	err = runner.Run(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), command) || !fileExists(command) || fileExists(data) || fileExists(program) {
		t.Fatalf("Run error = %v, command = %t, data = %t, program = %t", err, fileExists(command), fileExists(data), fileExists(program))
	}
}

func uninstallCommandTestOptions(service *uninstallFakeService) UninstallerOptions {
	return UninstallerOptions{
		Service:     service,
		Elevated:    func() bool { return true },
		ProbeDaemon: func(context.Context) (bool, error) { return false, nil },
	}
}

func previewHasPath(targets []UninstallTarget, path string) bool {
	for _, target := range targets {
		if target.Path == path {
			return true
		}
	}
	return false
}

func progressHas(progress []string, text string) bool {
	for _, message := range progress {
		if strings.Contains(message, text) {
			return true
		}
	}
	return false
}

func writeProgramBytes(t *testing.T, program string, payload []byte) {
	t.Helper()
	if err := os.MkdirAll(program, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(program, platform.InstalledBinaryName()), payload, 0o700); err != nil {
		t.Fatal(err)
	}
}

func restoreUninstallCommandRemove(t *testing.T) {
	t.Helper()
	prevFatal := commandRemoveFailureIsFatal
	prevRemove := removeUninstallCommandFile
	t.Cleanup(func() {
		commandRemoveFailureIsFatal = prevFatal
		removeUninstallCommandFile = prevRemove
	})
}
