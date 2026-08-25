package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/elevate"
	"github.com/mihari-proxy/mihari/internal/update"
)

type fakeSelfUpdater struct {
	calls  int
	result update.Result
	err    error
}

func (f *fakeSelfUpdater) Update(context.Context, string, string, string) (update.Result, error) {
	f.calls++
	return f.result, f.err
}

func TestSelfUpdateRequiresElevation(t *testing.T) {
	prev := elevate.Check
	t.Cleanup(func() { elevate.Check = prev })
	elevate.Check = func() bool { return false }
	fake := &fakeSelfUpdater{}
	exit := Execute(context.Background(), []string{"self", "update", "--json"}, &bytes.Buffer{}, &bytes.Buffer{}, Dependencies{SelfUpdater: fake})
	if exit != ExitPermission || fake.calls != 0 {
		t.Fatalf("exit=%d calls=%d", exit, fake.calls)
	}
}

func TestSelfUpdateWhenElevated(t *testing.T) {
	prev := elevate.Check
	t.Cleanup(func() { elevate.Check = prev })
	elevate.Check = func() bool { return true }
	fake := &fakeSelfUpdater{result: update.Result{Version: "v2.0.0", Updated: true}}
	stdout := &bytes.Buffer{}
	exit := Execute(context.Background(), []string{"self", "update", "--json"}, stdout, &bytes.Buffer{}, Dependencies{SelfUpdater: fake})
	if exit != ExitOK || fake.calls != 1 || !strings.Contains(stdout.String(), `"updated":true`) {
		t.Fatalf("exit=%d stdout=%q calls=%d", exit, stdout, fake.calls)
	}
}

func TestSelfVersionJSON(t *testing.T) {
	stdout := &bytes.Buffer{}
	exit := Execute(context.Background(), []string{"self", "version", "--json"}, stdout, &bytes.Buffer{}, Dependencies{})
	if exit != ExitOK || !strings.Contains(stdout.String(), `"version"`) {
		t.Fatalf("exit=%d stdout=%q", exit, stdout)
	}
}
