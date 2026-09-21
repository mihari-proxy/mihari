//go:build windows

package app

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/service"
	"github.com/mihari-proxy/mihari/internal/update"
)

// TestUpdateDaemonFixture is a temporary executable with no business files or real service registration.
func TestUpdateDaemonFixture(t *testing.T) {
	if os.Getenv("MIHARI_APP_UPDATE_DAEMON_FIXTURE") != "1" {
		return
	}
	if _, err := fmt.Fprintln(os.Stdout, "ready"); err != nil {
		t.Fatal(err)
	}
	<-time.NewTimer(15 * time.Second).C
}

type fixtureApplicationClient struct {
	status       protocol.Status
	identity     platform.WindowsProcessIdentityValue
	prepareError error
	events       *[]string
}

func (c *fixtureApplicationClient) Status(context.Context) (protocol.Status, error) {
	return c.status, nil
}
func (c *fixtureApplicationClient) PrepareApplicationUpdate(_ context.Context, id string) (protocol.ApplicationUpdatePrepared, error) {
	*c.events = append(*c.events, "prepare")
	return protocol.ApplicationUpdatePrepared{OperationID: id, PID: c.identity.PID, CreationFiletime: c.identity.CreationFiletime, ImagePath: c.identity.ImagePath, SID: c.identity.SID}, c.prepareError
}
func (c *fixtureApplicationClient) ReleaseApplicationUpdate(context.Context, string) error {
	*c.events = append(*c.events, "release")
	return nil
}

type fixtureApplicationService struct {
	state  service.StatusKind
	events *[]string
	stop   func() error
}

func (s *fixtureApplicationService) Status() (service.StatusKind, error) { return s.state, nil }
func (s *fixtureApplicationService) Stop() error {
	*s.events = append(*s.events, "stop-service")
	if err := s.stop(); err != nil {
		return err
	}
	s.state = service.StatusStopped
	return nil
}
func (s *fixtureApplicationService) Start() error {
	*s.events = append(*s.events, "start-service")
	s.state = service.StatusRunning
	return nil
}

// TestWindowsUpdateMaintenance_DrainsBeforeStopping binds only an isolated fake daemon's copied executable.
func TestWindowsUpdateMaintenance_DrainsBeforeStopping(t *testing.T) {
	for _, scenario := range []string{"manual", "service", "drain-timeout", "old-daemon"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			fs, err := platform.NewPrivateFS(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := fs.Close(); err != nil {
					t.Error(err)
				}
			}()
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			body, err := os.ReadFile(exe)
			if err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(dir, "mihari.exe")
			if err = os.WriteFile(target, body, 0700); err != nil {
				t.Fatal(err)
			}
			child := exec.Command(target, "-test.run=^TestUpdateDaemonFixture$")
			child.Env = append(os.Environ(), "MIHARI_APP_UPDATE_DAEMON_FIXTURE=1")
			output, err := child.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			child.Stderr = io.Discard
			if err = child.Start(); err != nil {
				t.Fatal(err)
			}
			waited := false
			defer func() {
				if !waited {
					_ = child.Process.Kill()
					_ = child.Wait()
				}
			}()
			reader := bufio.NewReader(output)
			if line, e := reader.ReadString('\n'); e != nil || line != "ready\n" {
				t.Fatalf("fixture not ready: %q %v", line, e)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			process, err := platform.OpenWindowsProcessIdentity(ctx, uint32(child.Process.Pid))
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := process.Close(); err != nil {
					t.Error(err)
				}
			}()
			events := []string{}
			client := &fixtureApplicationClient{status: protocol.Status{PID: child.Process.Pid, Capabilities: []string{protocol.ApplicationUpdateCapability}}, identity: process.Identity(), events: &events}
			svc := &fixtureApplicationService{state: service.StatusNotInstalled, events: &events, stop: func() error { return process.Terminate(ctx) }}
			if scenario == "service" {
				svc.state = service.StatusRunning
			}
			if scenario == "drain-timeout" {
				client.prepareError = context.DeadlineExceeded
			}
			if scenario == "old-daemon" {
				client.status.Capabilities = nil
			}
			coordinator := &WindowsUpdateMaintenance{Client: client, Service: svc, ManualDaemonStopped: func() { events = append(events, "manual-stopped") }}
			coordinator.openTree = func(context.Context, string, *platform.WindowsProcessIdentity) (updateRuntimeTree, error) {
				return fixtureUpdateTree{process}, nil
			}
			lease, err := coordinator.Acquire(ctx, update.PreparedUpdate{TargetPath: target})
			if scenario == "drain-timeout" || scenario == "old-daemon" {
				if err == nil {
					_ = lease.Close()
					t.Fatal("unsafe update accepted")
				}
				if scenario == "drain-timeout" && !errors.Is(err, context.DeadlineExceeded) {
					t.Fatal(err)
				}
				if exited, e := process.Exited(ctx); e != nil || exited {
					t.Fatalf("preparation failure killed daemon: %v %v", exited, e)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if exited, e := process.Exited(ctx); e != nil || !exited {
				t.Fatalf("old daemon survived: %v %v", exited, e)
			}
			_ = child.Wait()
			waited = true
			if err = lease.Close(); err != nil {
				t.Fatal(err)
			}
			want := []string{"prepare", "manual-stopped", "release"}
			if scenario == "service" {
				want = []string{"prepare", "stop-service", "release", "start-service"}
			}
			if !reflect.DeepEqual(events, want) {
				t.Fatalf("events %v want %v", events, want)
			}
		})
	}
}

type fixtureUpdateTree struct {
	process *platform.WindowsProcessIdentity
}

func (t fixtureUpdateTree) Terminate(ctx context.Context) error { return t.process.Terminate(ctx) }
func (t fixtureUpdateTree) WaitExit(ctx context.Context) error  { return t.process.WaitExit(ctx) }
func (t fixtureUpdateTree) Close() error                        { return nil } // The test owns its observation handle.
