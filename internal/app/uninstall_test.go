package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/service"
)

func TestUninstaller_RunServiceFailureLeavesFiles(t *testing.T) {
	layout, data, program := uninstallTestLayout(t)
	writeUninstallFixture(t, data, "mihari.yaml")
	writeUninstallFixture(t, program, "mihari")
	service := &uninstallFakeService{uninstallErr: errors.New("service removal failed")}
	runner := newUninstallTestRunner(t, layout, UninstallerOptions{
		Service:     service,
		Elevated:    func() bool { return true },
		ProbeDaemon: func(context.Context) (bool, error) { return false, nil },
	})

	err := runner.Run(context.Background(), nil)
	if !errors.Is(err, service.uninstallErr) {
		t.Fatalf("Run error = %v, want service error", err)
	}
	if !fileExists(data) || !fileExists(program) {
		t.Fatalf("files were removed after service failure: data=%t program=%t", fileExists(data), fileExists(program))
	}
}

func TestUninstaller_RunUnknownFilesDoNotTouchService(t *testing.T) {
	layout, data, _ := uninstallTestLayout(t)
	writeUninstallFixture(t, data, "unknown.txt")
	service := &uninstallFakeService{}
	runner := newUninstallTestRunner(t, layout, UninstallerOptions{
		Service:     service,
		Elevated:    func() bool { return true },
		ProbeDaemon: func(context.Context) (bool, error) { return false, nil },
	})

	err := runner.Run(context.Background(), nil)
	var checkErr *UninstallFileError
	if !errors.As(err, &checkErr) || service.uninstallCalls != 0 {
		t.Fatalf("Run error = %v, uninstall calls = %d; want file refusal before service action", err, service.uninstallCalls)
	}
}

func TestUninstaller_RunRemovesRecognizedRootsAfterDaemonStops(t *testing.T) {
	layout, data, program := uninstallTestLayout(t)
	writeUninstallFixture(t, data, "mihari.yaml")
	writeUninstallFixture(t, program, "mihari")
	service := &uninstallFakeService{status: service.StatusStopped}
	runner := newUninstallTestRunner(t, layout, UninstallerOptions{
		Service:     service,
		Elevated:    func() bool { return true },
		ProbeDaemon: func(context.Context) (bool, error) { return false, nil },
	})

	var progress []string
	if err := runner.Run(context.Background(), func(message string) { progress = append(progress, message) }); err != nil {
		t.Fatal(err)
	}
	if service.uninstallCalls != 1 {
		t.Fatalf("uninstall calls = %d, want 1", service.uninstallCalls)
	}
	if fileExists(data) || fileExists(program) {
		t.Fatalf("recognized roots remain: data=%t program=%t", fileExists(data), fileExists(program))
	}
	if len(progress) == 0 {
		t.Fatal("expected progress messages")
	}
}

func TestUninstaller_RunAllowsAbsentRootsOnRetry(t *testing.T) {
	layout, _, _ := uninstallTestLayout(t)
	service := &uninstallFakeService{status: service.StatusNotInstalled}
	runner := newUninstallTestRunner(t, layout, UninstallerOptions{
		Service:     service,
		Elevated:    func() bool { return true },
		ProbeDaemon: func(context.Context) (bool, error) { return false, nil },
	})

	if err := runner.Run(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if service.uninstallCalls != 0 {
		t.Fatalf("uninstall calls = %d, want 0 for an already absent service", service.uninstallCalls)
	}
}

func TestUninstaller_RunSkipsAlreadyAbsentServiceOnPartialRetry(t *testing.T) {
	layout, _, program := uninstallTestLayout(t)
	writeUninstallFixture(t, program, "mihari")
	missingServiceErr := errors.New("service is not installed")
	service := &uninstallFakeService{status: service.StatusNotInstalled, uninstallErr: missingServiceErr}
	runner := newUninstallTestRunner(t, layout, UninstallerOptions{
		Service:     service,
		Elevated:    func() bool { return true },
		ProbeDaemon: func(context.Context) (bool, error) { return false, nil },
	})

	if err := runner.Run(context.Background(), nil); err != nil {
		t.Fatalf("Run error = %v, want absent-service retry to remove program", err)
	}
	if service.uninstallCalls != 0 {
		t.Fatalf("uninstall calls = %d, want 0 when status already says not installed", service.uninstallCalls)
	}
	if fileExists(program) {
		t.Fatal("program remained after absent-service retry")
	}
}

func TestUninstaller_RunUnknownServiceStatusLeavesFiles(t *testing.T) {
	layout, data, program := uninstallTestLayout(t)
	writeUninstallFixture(t, data, "mihari.yaml")
	writeUninstallFixture(t, program, "mihari")
	service := &uninstallFakeService{status: service.StatusUnknown}
	runner := newUninstallTestRunner(t, layout, UninstallerOptions{
		Service:     service,
		Elevated:    func() bool { return true },
		ProbeDaemon: func(context.Context) (bool, error) { return false, nil },
	})

	err := runner.Run(context.Background(), nil)
	if err == nil || !fileExists(data) || !fileExists(program) {
		t.Fatalf("Run error = %v, data exists = %t, program exists = %t; want status refusal without deletion", err, fileExists(data), fileExists(program))
	}
}

func TestUninstaller_RunUnknownDaemonLeavesFiles(t *testing.T) {
	layout, data, program := uninstallTestLayout(t)
	writeUninstallFixture(t, data, "mihari.yaml")
	writeUninstallFixture(t, program, "mihari")
	service := &uninstallFakeService{status: service.StatusStopped}
	probeErr := errors.New("control probe failed")
	runner := newUninstallTestRunner(t, layout, UninstallerOptions{
		Service:     service,
		Elevated:    func() bool { return true },
		ProbeDaemon: func(context.Context) (bool, error) { return false, probeErr },
	})

	err := runner.Run(context.Background(), nil)
	if !errors.Is(err, probeErr) || !fileExists(data) || !fileExists(program) {
		t.Fatalf("Run error = %v, data exists = %t, program exists = %t; want probe refusal without deletion", err, fileExists(data), fileExists(program))
	}
}

func TestUninstaller_RunSecondCheckRefusesNewUnknownEntry(t *testing.T) {
	layout, data, program := uninstallTestLayout(t)
	writeUninstallFixture(t, data, "mihari.yaml")
	writeUninstallFixture(t, program, "mihari")
	service := &uninstallFakeService{status: service.StatusStopped, onUninstall: func() { writeUninstallFixture(t, data, "appeared-after-stop.txt") }}
	runner := newUninstallTestRunner(t, layout, UninstallerOptions{
		Service:     service,
		Elevated:    func() bool { return true },
		ProbeDaemon: func(context.Context) (bool, error) { return false, nil },
	})

	err := runner.Run(context.Background(), nil)
	var checkErr *UninstallFileError
	if !errors.As(err, &checkErr) || checkErr.RelativePath != "appeared-after-stop.txt" {
		t.Fatalf("Run error = %v, want second-check refusal", err)
	}
	if !fileExists(data) || !fileExists(program) {
		t.Fatalf("files were removed after second-check refusal: data=%t program=%t", fileExists(data), fileExists(program))
	}
}

func TestUninstaller_RunRemovalFailureStopsBeforeProgram(t *testing.T) {
	layout, data, program := uninstallTestLayout(t)
	writeUninstallFixture(t, data, "mihari.yaml")
	writeUninstallFixture(t, program, "mihari")
	service := &uninstallFakeService{status: service.StatusStopped}
	removeErr := errors.New("data removal failed")
	runner := newUninstallTestRunner(t, layout, UninstallerOptions{
		Service:     service,
		Elevated:    func() bool { return true },
		ProbeDaemon: func(context.Context) (bool, error) { return false, nil },
		Remove: func(path string) error {
			if filepath.Clean(path) == filepath.Clean(data) {
				return removeErr
			}
			return os.RemoveAll(path)
		},
	})

	err := runner.Run(context.Background(), nil)
	if !errors.Is(err, removeErr) || !strings.Contains(err.Error(), data) || !fileExists(program) {
		t.Fatalf("Run error = %v, program exists = %t; want failed data removal before program phase", err, fileExists(program))
	}
}

func TestUninstaller_RunReturnsCancellationWhileWaitingForDaemon(t *testing.T) {
	layout, data, _ := uninstallTestLayout(t)
	writeUninstallFixture(t, data, "mihari.yaml")
	service := &uninstallFakeService{status: service.StatusStopped}
	ctx, cancel := context.WithCancel(context.Background())
	runner := newUninstallTestRunner(t, layout, UninstallerOptions{
		Service:      service,
		Elevated:     func() bool { return true },
		ProbeDaemon:  func(context.Context) (bool, error) { cancel(); return true, nil },
		PollInterval: time.Hour,
	})

	err := runner.Run(ctx, nil)
	if !errors.Is(err, context.Canceled) || !fileExists(data) {
		t.Fatalf("Run error = %v, data exists = %t; want cancellation without removal", err, fileExists(data))
	}
}

func TestUninstaller_PreviewAllowsUnrecognizedFiles(t *testing.T) {
	layout, data, program := uninstallTestLayout(t)
	writeUninstallFixture(t, data, "unknown.txt")
	writeUninstallFixture(t, program, "mihari")
	runner := newUninstallTestRunner(t, layout, UninstallerOptions{})

	targets, err := runner.Preview(context.Background())
	if err != nil {
		t.Fatalf("Preview error = %v, want success when only folder contents are unrecognized", err)
	}
	if len(targets) < 2 || targets[0].Path != data || targets[1].Path != program {
		t.Fatalf("Preview targets = %#v, want data and program roots", targets)
	}
}

func TestUninstaller_RunForceRemovesUnrecognizedFiles(t *testing.T) {
	layout, data, program := uninstallTestLayout(t)
	writeUninstallFixture(t, data, "unknown.txt")
	writeUninstallFixture(t, program, "mihari")
	service := &uninstallFakeService{status: service.StatusStopped}
	runner := newUninstallTestRunner(t, layout, UninstallerOptions{
		Service:     service,
		Elevated:    func() bool { return true },
		ProbeDaemon: func(context.Context) (bool, error) { return false, nil },
	})

	if err := runner.RunForce(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if service.uninstallCalls != 1 {
		t.Fatalf("uninstall calls = %d, want 1", service.uninstallCalls)
	}
	if fileExists(data) || fileExists(program) {
		t.Fatalf("forced roots remain: data=%t program=%t", fileExists(data), fileExists(program))
	}
}

func TestUninstaller_RunForceStillRefusesSymlinkRoot(t *testing.T) {
	layout, data, program := uninstallTestLayout(t)
	writeUninstallFixture(t, program, "mihari")
	realData := filepath.Join(t.TempDir(), "real-data")
	if err := os.MkdirAll(realData, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(data); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realData, data); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	service := &uninstallFakeService{status: service.StatusStopped}
	runner := newUninstallTestRunner(t, layout, UninstallerOptions{
		Service:     service,
		Elevated:    func() bool { return true },
		ProbeDaemon: func(context.Context) (bool, error) { return false, nil },
	})

	err := runner.RunForce(context.Background(), nil)
	var checkErr *UninstallFileError
	if !errors.As(err, &checkErr) || service.uninstallCalls != 0 || !fileExists(realData) {
		t.Fatalf("RunForce error = %v, uninstall calls = %d; want symlink root refusal without deletion", err, service.uninstallCalls)
	}
}

func TestUninstaller_PreviewPrivateLayoutIncludesCapturedWindowsControlTarget(t *testing.T) {
	layout, data, program := uninstallTestLayout(t)
	writeUninstallFixture(t, data, "mihari.yaml")
	writeUninstallFixture(t, program, "mihari")
	control := filepath.Join(t.TempDir(), "install-control")
	if err := os.MkdirAll(control, 0o700); err != nil {
		t.Fatal(err)
	}
	runner := newUninstaller(layout, UninstallerOptions{}, func() (string, error) { return control, nil })

	targets, err := runner.Preview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 3 || targets[0] != (UninstallTarget{Path: data, Kind: "data"}) || targets[1] != (UninstallTarget{Path: program, Kind: "program"}) || targets[2] != (UninstallTarget{Path: control, Kind: "control"}) {
		t.Fatalf("Preview targets = %#v, want private data, program, and captured control roots", targets)
	}
}

func TestUninstaller_RunRemovesCapturedControlWithoutPrivateBaseSibling(t *testing.T) {
	layout, data, program := uninstallTestLayout(t)
	root := filepath.Dir(data)
	control := filepath.Join(root, "system-base", "install-control")
	sibling := filepath.Join(root, "system-base", "control.token")
	writeUninstallFixture(t, data, "mihari.yaml")
	writeUninstallFixture(t, program, "mihari")
	writeUninstallFixture(t, control, "state.json")
	writeUninstallFixture(t, root, "system-base/control.token")
	service := &uninstallFakeService{status: service.StatusStopped}
	runner := newUninstaller(layout, UninstallerOptions{
		Service:     service,
		Elevated:    func() bool { return true },
		ProbeDaemon: func(context.Context) (bool, error) { return false, nil },
	}, func() (string, error) { return control, nil })

	if err := runner.Run(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if fileExists(control) || !fileExists(sibling) {
		t.Fatalf("control exists = %t, unrelated sibling exists = %t", fileExists(control), fileExists(sibling))
	}
}

func uninstallTestLayout(t *testing.T) (platform.ResolvedLayout, string, string) {
	t.Helper()
	root := t.TempDir()
	data := filepath.Join(root, "data")
	program := filepath.Join(root, "program")
	layout := platform.ResolvedLayout{
		Mode:        platform.PrivateMode,
		BaseDir:     data,
		Data:        platform.NewPaths(data),
		ClientLogs:  platform.NewPaths(data),
		InstallRoot: program,
	}
	return layout, data, program
}

func newUninstallTestRunner(t *testing.T, layout platform.ResolvedLayout, opts UninstallerOptions) *Uninstaller {
	t.Helper()
	control := filepath.Join(t.TempDir(), "install-control")
	if err := os.MkdirAll(control, 0o700); err != nil {
		t.Fatal(err)
	}
	return newUninstaller(layout, opts, func() (string, error) { return control, nil })
}

func fileExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

type uninstallFakeService struct {
	status         service.StatusKind
	statusErr      error
	uninstallErr   error
	uninstallCalls int
	onUninstall    func()
}

func (f *uninstallFakeService) Uninstall() error {
	f.uninstallCalls++
	if f.onUninstall != nil {
		f.onUninstall()
	}
	return f.uninstallErr
}

func (f *uninstallFakeService) Status() (service.StatusKind, error) {
	if f.statusErr != nil {
		return service.StatusUnknown, f.statusErr
	}
	return f.status, nil
}
