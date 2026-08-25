package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/elevate"
	"github.com/mihari-proxy/mihari/internal/update"
)

type fakeSelfUpdater struct {
	calls       int
	lastChannel string
	result      update.Result
	err         error
}

func (f *fakeSelfUpdater) Update(_ context.Context, _, _, channel string) (update.Result, error) {
	f.calls++
	f.lastChannel = channel
	return f.result, f.err
}

func TestSelfChannelShowDefaultsMain(t *testing.T) {
	t.Setenv("MIHARI_DATA", t.TempDir())
	stdout := &bytes.Buffer{}
	exit := Execute(context.Background(), []string{"self", "channel", "--json"}, stdout, &bytes.Buffer{}, Dependencies{})
	if exit != ExitOK || !strings.Contains(stdout.String(), `"channel":"main"`) {
		t.Fatalf("exit=%d stdout=%q", exit, stdout)
	}
}

func TestSelfChannelSetDev(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MIHARI_DATA", root)
	if Execute(context.Background(), []string{"self", "channel", "dev"}, &bytes.Buffer{}, &bytes.Buffer{}, Dependencies{}) != ExitOK {
		t.Fatal("set")
	}
	raw, _ := os.ReadFile(filepath.Join(root, "mihari-channel"))
	if string(raw) != "dev\n" {
		t.Fatalf("raw=%q", raw)
	}
}

func TestSelfChannelRejectsStable(t *testing.T) {
	t.Setenv("MIHARI_DATA", t.TempDir())
	if Execute(context.Background(), []string{"self", "channel", "stable"}, &bytes.Buffer{}, &bytes.Buffer{}, Dependencies{}) != ExitUsage {
		t.Fatal("want usage")
	}
}

func TestSelfChannelInvalidFile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MIHARI_DATA", root)
	_ = os.WriteFile(filepath.Join(root, "mihari-channel"), []byte("stable\n"), 0o600)
	if Execute(context.Background(), []string{"self", "channel"}, &bytes.Buffer{}, &bytes.Buffer{}, Dependencies{}) != ExitData {
		t.Fatal("want data")
	}
}

func TestSelfChannelDoesNotRequireElevation(t *testing.T) {
	t.Setenv("MIHARI_DATA", t.TempDir())
	prev := elevate.Check
	t.Cleanup(func() { elevate.Check = prev })
	elevate.Check = func() bool { return false }
	if Execute(context.Background(), []string{"self", "channel", "dev"}, &bytes.Buffer{}, &bytes.Buffer{}, Dependencies{}) != ExitOK {
		t.Fatal("channel should not require elevation")
	}
}

func TestSelfUpdateJSONIncludesAhead(t *testing.T) {
	prev := elevate.Check
	t.Cleanup(func() { elevate.Check = prev })
	elevate.Check = func() bool { return true }
	root := t.TempDir()
	t.Setenv("MIHARI_DATA", root)
	_ = os.WriteFile(filepath.Join(root, "mihari-channel"), []byte("dev\n"), 0o600)
	fake := &fakeSelfUpdater{result: update.Result{Version: "v0.8.2", Updated: false, Ahead: true, Channel: "dev"}}
	stdout := &bytes.Buffer{}
	exit := Execute(context.Background(), []string{"self", "update", "--json"}, stdout, &bytes.Buffer{}, Dependencies{SelfUpdater: fake})
	if exit != ExitOK || fake.calls != 1 || fake.lastChannel != "dev" || !strings.Contains(stdout.String(), `"ahead":true`) {
		t.Fatalf("exit=%d stdout=%q calls=%d channel=%q", exit, stdout, fake.calls, fake.lastChannel)
	}
}

func TestSelfUpdateAheadText(t *testing.T) {
	prev := elevate.Check
	t.Cleanup(func() { elevate.Check = prev })
	elevate.Check = func() bool { return true }
	root := t.TempDir()
	t.Setenv("MIHARI_DATA", root)
	_ = os.WriteFile(filepath.Join(root, "mihari-channel"), []byte("main\n"), 0o600)
	fake := &fakeSelfUpdater{result: update.Result{Version: "v0.8.2", Updated: false, Ahead: true, Channel: "main"}}
	stdout := &bytes.Buffer{}
	exit := Execute(context.Background(), []string{"self", "update"}, stdout, &bytes.Buffer{}, Dependencies{SelfUpdater: fake})
	if exit != ExitOK || !strings.Contains(stdout.String(), "current ") || !strings.Contains(stdout.String(), "is ahead of main v0.8.2") {
		t.Fatalf("exit=%d stdout=%q", exit, stdout)
	}
}

func TestSelfUpdateInvalidFileDoesNotCallUpdater(t *testing.T) {
	prev := elevate.Check
	t.Cleanup(func() { elevate.Check = prev })
	elevate.Check = func() bool { return true }
	root := t.TempDir()
	t.Setenv("MIHARI_DATA", root)
	_ = os.WriteFile(filepath.Join(root, "mihari-channel"), []byte("stable\n"), 0o600)
	fake := &fakeSelfUpdater{}
	if Execute(context.Background(), []string{"self", "update"}, &bytes.Buffer{}, &bytes.Buffer{}, Dependencies{SelfUpdater: fake}) != ExitData {
		t.Fatal("want data")
	}
	if fake.calls != 0 {
		t.Fatalf("calls=%d", fake.calls)
	}
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
