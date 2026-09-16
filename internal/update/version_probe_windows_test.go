package update

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/mihari-proxy/mihari/internal/platform"
	"golang.org/x/sys/windows"
)

func TestVersionProbe_WindowsUserWritableBinary(t *testing.T) {
	file := platform.ReplacementFile{Path: filepath.Join(t.TempDir(), "mihari.exe"), FileID: "installed", SHA256: strings.Repeat("a", 64), Exists: true}
	probe := &userVersionProbeFake{file: file}
	probe.file.MayExecute = true // this user's ACL is trusted only after reduction
	observe := func(context.Context, string) (platform.ReplacementFile, error) { return file, nil }
	fallback := func(ctx context.Context, target ReplacementTarget) (ReplacementTarget, error) {
		return observeUserReplacementTargetWith(ctx, target, func() (userVersionProbe, error) { return probe, nil })
	}
	got, err := observeReplacementTargetWithFallback(context.Background(), "binary", file.Path, nil, observe, fallback)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != "v1.2.3" || got.FileID != file.FileID || got.SHA256 != file.SHA256 || !got.Exists {
		t.Fatalf("user-writable binary observation=%+v", got)
	}
	if probe.runs != 1 || probe.observations != 2 || probe.closes != 1 {
		t.Fatalf("runs=%d observations=%d closes=%d", probe.runs, probe.observations, probe.closes)
	}
}

func TestVersionProbe_WindowsUserRunner(t *testing.T) {
	// Hosted Windows runners may have UAC disabled and no filtered token.
	// Only that host limitation skips the native test; policy tests always run.
	if windows.GetCurrentProcessToken().IsElevated() {
		linked, err := windows.GetCurrentProcessToken().GetLinkedToken()
		if err != nil {
			t.Skip("host has no filtered UAC token")
		}
		if err := linked.Close(); err != nil {
			t.Fatal(err)
		}
	}
	probe, err := platform.OpenWindowsUserVersionProbe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := probe.Close(); err != nil {
			t.Error(err)
		}
	})
	exe := filepath.Join(t.TempDir(), "user version probe.exe")
	cmd := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-o", exe, "./testdata/userversionprobe")
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN="+runtime.Version())
	if raw, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fixture: %v\n%s", err, raw)
	}
	t.Setenv("MIHARI_PROBE_TEST_SECRET", "must-not-be-inherited")
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	attributes := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), InheritHandle: 1}
	sentinel, err := windows.CreateEvent(&attributes, 1, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := windows.CloseHandle(sentinel); err != nil {
			t.Error(err)
		}
	})
	for _, mode := range []string{"version", "stdout-overflow", "stderr-overflow", "wait"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "unexpected-handle"), []byte(strconv.FormatUint(uint64(sentinel), 10)), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "expected-user"), []byte(user.User.Sid.String()), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "probe-mode"), []byte(mode), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			raw, err := (userVersionRunner{probe}).RunVersion(ctx, exe, dir)
			if state, waitErr := windows.WaitForSingleObject(sentinel, 0); waitErr != nil || state != uint32(windows.WAIT_TIMEOUT) {
				t.Fatalf("unrelated inheritable handle leaked: state=%d error=%v", state, waitErr)
			}
			switch mode {
			case "version":
				if err != nil || decodeProbedVersion(raw) != "v1.2.3" {
					t.Fatalf("non-admin version query=%s error=%v", raw, err)
				}
			case "wait":
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("timeout=%v", err)
				}
			default:
				if !errors.Is(err, errVersionProbeLimit) {
					t.Fatalf("overflow=%v", err)
				}
			}
		})
	}
}

func TestVersionProbe_WindowsInstalledBinary(t *testing.T) {
	if windows.GetCurrentProcessToken().IsElevated() {
		linked, err := windows.GetCurrentProcessToken().GetLinkedToken()
		if err != nil {
			t.Skip("host has no filtered UAC token")
		}
		if err := linked.Close(); err != nil {
			t.Fatal(err)
		}
	}
	// Exercise the real CLI and full path/token observation, not just a runner
	// fake. All version-process state is redirected to its temporary environment.
	exe := filepath.Join(t.TempDir(), "installed mihari.exe")
	cmd := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-ldflags", "-X github.com/mihari-proxy/mihari/internal/buildinfo.Version=v1.2.3", "-o", exe, "../../cmd/mihari")
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN="+runtime.Version(), "CGO_ENABLED=0")
	if raw, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build installed fixture: %v\n%s", err, raw)
	}
	probe, err := platform.OpenWindowsUserVersionProbe()
	if err != nil {
		t.Fatal(err)
	}
	file, observeErr := probe.Observe(context.Background(), exe)
	if err := errors.Join(observeErr, probe.Close()); err != nil {
		t.Fatal(err)
	}
	if !file.MayExecute {
		t.Skip("temporary directory ancestry permits another user to write; version execution is correctly refused")
	}
	got, err := ObserveReplacementTarget(context.Background(), "binary", exe, nil)
	if err != nil || got.Version != "v1.2.3" || !got.Exists || got.FileID == "" || got.SHA256 == "" || got.probeErr != nil {
		t.Fatalf("installed binary version=%q exists=%v error=%v probe error=%v", got.Version, got.Exists, err, got.probeErr)
	}
}

type userVersionProbeFake struct {
	file                       platform.ReplacementFile
	runs, observations, closes int
	beforeObserve              func(int, *platform.ReplacementFile)
	runErr, closeErr           error
	beforeRun                  func(*exec.Cmd)
}

func (p *userVersionProbeFake) Observe(context.Context, string) (platform.ReplacementFile, error) {
	p.observations++
	file := p.file
	if p.beforeObserve != nil {
		p.beforeObserve(p.observations, &file)
	}
	return file, nil
}

func (p *userVersionProbeFake) Run(_ context.Context, cmd *exec.Cmd) error {
	p.runs++
	if p.beforeRun != nil {
		p.beforeRun(cmd)
	}
	if p.runErr != nil {
		return p.runErr
	}
	_, err := cmd.Stdout.Write([]byte(`{"schema":"mihari/v1","version":"v1.2.3"}`))
	return err
}

func (p *userVersionProbeFake) Close() error {
	p.closes++
	return p.closeErr
}

func TestVersionProbe_WindowsUserProbeRefusesUnsafeOrChangedFiles(t *testing.T) {
	for _, tc := range []struct {
		name      string
		change    func(int, *platform.ReplacementFile)
		wantRuns  int
		wantError bool
	}{
		{"other user can write", func(_ int, f *platform.ReplacementFile) { f.MayExecute = false }, 0, false},
		{"changed before execution", func(_ int, f *platform.ReplacementFile) { f.FileID = "other" }, 0, true},
		{"changed during execution", func(n int, f *platform.ReplacementFile) {
			if n == 2 {
				f.SHA256 = strings.Repeat("b", 64)
			}
		}, 1, true},
		{"trust revoked", func(n int, f *platform.ReplacementFile) {
			if n == 2 {
				f.MayExecute = false
			}
		}, 1, true},
		{"removed", func(_ int, f *platform.ReplacementFile) { f.Exists = false }, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file := platform.ReplacementFile{Path: filepath.Join(t.TempDir(), "mihari.exe"), FileID: "installed", SHA256: strings.Repeat("a", 64), Exists: true, MayExecute: true}
			probe := &userVersionProbeFake{file: file, beforeObserve: tc.change}
			target := ReplacementTarget{Roles: []string{"binary"}, Path: file.Path, FileID: file.FileID, SHA256: file.SHA256, Exists: true}
			got, err := observeUserReplacementTargetWith(context.Background(), target, func() (userVersionProbe, error) { return probe, nil })
			if (err != nil) != tc.wantError || got.Version != "" || probe.runs != tc.wantRuns || probe.closes != 1 {
				t.Fatalf("version=%q error=%v runs=%d closes=%d", got.Version, err, probe.runs, probe.closes)
			}
		})
	}
}

func TestVersionProbe_WindowsTokenFailureStaysUnknown(t *testing.T) {
	target := ReplacementTarget{Roles: []string{"binary"}, Path: filepath.Join(t.TempDir(), "mihari.exe"), FileID: "installed", SHA256: strings.Repeat("a", 64), Exists: true}
	denied := errors.New("no filtered token")
	got, err := observeUserReplacementTargetWith(context.Background(), target, func() (userVersionProbe, error) { return nil, denied })
	if err != nil || got.Version != "" || got.FileID != target.FileID || !errors.Is(got.probeErr, denied) {
		t.Fatalf("observation=%+v error=%v cause=%v", got, err, got.probeErr)
	}
}

func TestVersionProbe_WindowsUserProbeFailureAndCleanup(t *testing.T) {
	for _, mode := range []string{"start failed", "canceled", "output overflow", "close failed"} {
		t.Run(mode, func(t *testing.T) {
			file := platform.ReplacementFile{Path: filepath.Join(t.TempDir(), "mihari.exe"), FileID: "installed", SHA256: strings.Repeat("a", 64), Exists: true, MayExecute: true}
			probe := &userVersionProbeFake{file: file}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			failure := errors.New(mode)
			switch mode {
			case "start failed":
				probe.runErr = failure
			case "canceled":
				probe.beforeRun = func(*exec.Cmd) { cancel() }
			case "output overflow":
				probe.beforeRun = func(cmd *exec.Cmd) { _, _ = cmd.Stdout.Write([]byte(strings.Repeat("x", versionProbeLimit+1))) }
			case "close failed":
				probe.closeErr = failure
			}
			target := ReplacementTarget{Roles: []string{"binary"}, Path: file.Path, FileID: file.FileID, SHA256: file.SHA256, Exists: true}
			got, err := observeUserReplacementTargetWith(ctx, target, func() (userVersionProbe, error) { return probe, nil })
			if probe.runs != 1 || probe.closes != 1 {
				t.Fatalf("runs=%d closes=%d", probe.runs, probe.closes)
			}
			switch mode {
			case "canceled":
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("error=%v", err)
				}
			case "close failed":
				if !errors.Is(err, failure) {
					t.Fatalf("error=%v", err)
				}
			case "start failed":
				if err != nil || got.Version != "" || !errors.Is(got.probeErr, failure) {
					t.Fatalf("error=%v cause=%v", err, got.probeErr)
				}
			case "output overflow":
				if err != nil || got.Version != "" || !errors.Is(got.probeErr, errVersionProbeLimit) {
					t.Fatalf("error=%v cause=%v", err, got.probeErr)
				}
			}
		})
	}
}
