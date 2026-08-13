package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

type fakeInstalledServiceUpdater struct {
	installed bool
	err       error
	calls     int
}

func (f *fakeInstalledServiceUpdater) UpdateInstalledBinary() (bool, error) {
	f.calls++
	return f.installed, f.err
}

type statusResult struct {
	status protocol.Status
	err    error
}

type fakeDaemonVersionClient struct {
	results []statusResult
	calls   int
}

func (f *fakeDaemonVersionClient) Status(context.Context) (protocol.Status, error) {
	index := f.calls
	f.calls++
	if index >= len(f.results) {
		index = len(f.results) - 1
	}
	return f.results[index].status, f.results[index].err
}

func TestSelfUpdateServiceCompletionWaitsForNewDaemonVersion(t *testing.T) {
	service := &fakeInstalledServiceUpdater{installed: true}
	client := &fakeDaemonVersionClient{results: []statusResult{
		{err: errors.New("daemon restarting")},
		{status: protocol.Status{DaemonVersion: "v0.5.2"}},
		{status: protocol.Status{DaemonVersion: "v0.6.0"}},
	}}
	completion := NewSelfUpdateServiceCompletion(service, client)

	if err := completion.AfterReplace(context.Background(), "v0.6.0"); err != nil {
		t.Fatal(err)
	}
	if service.calls != 1 || client.calls != 3 {
		t.Fatalf("service calls=%d status calls=%d", service.calls, client.calls)
	}
}

func TestSelfUpdateServiceCompletionSkipsVersionCheckWhenNotInstalled(t *testing.T) {
	service := &fakeInstalledServiceUpdater{installed: false}
	client := &fakeDaemonVersionClient{}
	completion := NewSelfUpdateServiceCompletion(service, client)

	if err := completion.AfterReplace(context.Background(), "v0.6.0"); err != nil {
		t.Fatal(err)
	}
	if service.calls != 1 || client.calls != 0 {
		t.Fatalf("service calls=%d status calls=%d", service.calls, client.calls)
	}
}

func TestSelfUpdateServiceCompletionReturnsRedactedSyncWarning(t *testing.T) {
	service := &fakeInstalledServiceUpdater{installed: true, err: errors.New(`copy C:\Users\secret\mihari.exe failed`)}
	completion := NewSelfUpdateServiceCompletion(service, &fakeDaemonVersionClient{})

	err := completion.AfterReplace(context.Background(), "v0.6.0")
	var api protocol.APIError
	if !errors.As(err, &api) {
		t.Fatalf("err=%v", err)
	}
	if strings.Contains(api.Message, "secret") || strings.Contains(api.Message, `C:\Users`) {
		t.Fatalf("unsafe message=%q", api.Message)
	}
}

func TestSelfUpdateServiceCompletionPreservesSafeServiceWarning(t *testing.T) {
	warning := protocol.APIError{
		Code:    protocol.CodeDataFailure,
		Message: "Mihari updated, but the installed service binary could not be synchronized",
	}
	service := &fakeInstalledServiceUpdater{installed: true, err: warning}
	completion := NewSelfUpdateServiceCompletion(service, &fakeDaemonVersionClient{})

	err := completion.AfterReplace(context.Background(), "v0.6.0")
	var api protocol.APIError
	if !errors.As(err, &api) || api.Code != warning.Code || api.Message != warning.Message {
		t.Fatalf("err=%v", err)
	}
}

func TestSelfUpdateServiceCompletionCancellationReportsUnverifiedVersion(t *testing.T) {
	service := &fakeInstalledServiceUpdater{installed: true}
	client := &fakeDaemonVersionClient{results: []statusResult{{status: protocol.Status{DaemonVersion: "v0.5.2"}}}}
	completion := NewSelfUpdateServiceCompletion(service, client)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := completion.AfterReplace(ctx, "v0.6.0")
	var api protocol.APIError
	if !errors.As(err, &api) || api.Code != protocol.CodeInvalidState || !strings.Contains(api.Message, "v0.6.0") {
		t.Fatalf("err=%v", err)
	}
}

func TestSelfUpdateServiceCompletionReportsVersionMismatch(t *testing.T) {
	service := &fakeInstalledServiceUpdater{installed: true}
	client := &fakeDaemonVersionClient{results: []statusResult{{status: protocol.Status{DaemonVersion: "v0.5.2"}}}}
	completion := NewSelfUpdateServiceCompletion(service, client)
	completion.wait = func(context.Context) error { return context.DeadlineExceeded }

	err := completion.AfterReplace(context.Background(), "v0.6.0")
	var api protocol.APIError
	if !errors.As(err, &api) || api.Code != protocol.CodeInvalidState {
		t.Fatalf("err=%v", err)
	}
	if !strings.Contains(api.Message, "v0.6.0") || strings.Contains(api.Message, "v0.5.2") {
		t.Fatalf("message=%q", api.Message)
	}
}
