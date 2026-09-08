package service

import (
	"context"
	"strings"
	"testing"
	"time"
)

type launchdHarness struct {
	adapter  *LaunchdAdapter
	runner   *fakeRunner
	files    *memFS
	tree     *fakeTree
	clock    *fakeClock
	hook     *recordingHook
	loaded   bool
	running  bool
	disabled bool
}

func newLaunchdHarness(t *testing.T, running, enabled bool, installPlist bool) *launchdHarness {
	t.Helper()
	files := newMemFS()
	if installPlist {
		if err := files.Write(context.Background(), trustedPlistFile(t)); err != nil {
			t.Fatal(err)
		}
	}
	hook := &recordingHook{}
	tree := &fakeTree{alive: running, empty: !running, identity: fixtureLaunchdIdentity()}
	clock := &fakeClock{now: time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)}
	runner := &fakeRunner{}
	h := &launchdHarness{
		runner:   runner,
		files:    files,
		tree:     tree,
		clock:    clock,
		hook:     hook,
		loaded:   running,
		running:  running,
		disabled: !enabled,
	}
	h.adapter = newLaunchdAdapter(launchdConfig{
		Runner: runner,
		Files:  files,
		Tree:   tree,
		Clock:  clock,
		Hook:   hook.wrap,
		Paths:  DefaultLaunchdPaths(),
	})
	h.installLaunchdHandlers()
	return h
}

func (t *fakeTree) BootIdentity(context.Context) (string, error) { return "boot-test", nil }

func (h *launchdHarness) installLaunchdHandlers() {
	h.runner.handle(func(argv []string) bool {
		return argvHasPrefix(argv, []string{defaultLaunchctl, "print-disabled", "system"})
	}, func([]string) (CommandResult, error) {
		value := "false"
		if h.disabled {
			value = "true"
		}
		body := "{\n\t\"mihari\" => " + value + "\n}\n"
		return CommandResult{Stdout: []byte(body)}, nil
	})
	h.runner.handle(func(argv []string) bool {
		return argvHasPrefix(argv, []string{defaultLaunchctl, "print", "system/mihari"})
	}, func([]string) (CommandResult, error) {
		if !h.loaded {
			return CommandResult{ExitCode: 1, Stderr: []byte("Could not find service \"system/mihari\".\n")}, nil
		}
		state := "active"
		pid := "0"
		if h.running {
			state = "running"
			pid = "77"
		}
		body := "system/mihari = {\n\tstate = " + state + "\n\tpid = " + pid + "\n}\n"
		return CommandResult{Stdout: []byte(body)}, nil
	})
	h.runner.handle(func(argv []string) bool {
		return argvHasPrefix(argv, []string{defaultLaunchctl, "disable", "system/mihari"})
	}, func([]string) (CommandResult, error) {
		h.disabled = true
		return CommandResult{}, nil
	})
	h.runner.handle(func(argv []string) bool {
		return argvHasPrefix(argv, []string{defaultLaunchctl, "enable", "system/mihari"})
	}, func([]string) (CommandResult, error) {
		h.disabled = false
		return CommandResult{}, nil
	})
	h.runner.handle(func(argv []string) bool {
		return argvHasPrefix(argv, []string{defaultLaunchctl, "bootout", "system/mihari"})
	}, func([]string) (CommandResult, error) {
		h.loaded = false
		h.running = false
		h.tree.alive = false
		h.tree.empty = true
		return CommandResult{}, nil
	})
	h.runner.handle(func(argv []string) bool {
		return argvHasPrefix(argv, []string{defaultLaunchctl, "bootstrap", "system", defaultPlistPath})
	}, func([]string) (CommandResult, error) {
		h.loaded = true
		h.running = true
		h.tree.alive = true
		h.tree.empty = false
		return CommandResult{}, nil
	})
}

func TestLaunchdInspect_FourStates(t *testing.T) {
	for _, state := range definitionStates {
		t.Run(stateName(state.running, state.enabled), func(t *testing.T) {
			h := newLaunchdHarness(t, state.running, state.enabled, true)
			if !state.running {
				h.loaded = false
			}
			got, err := h.adapter.InspectDefinition(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if got.Running != state.running || got.Enabled != state.enabled {
				t.Fatalf("running=%v enabled=%v got running=%v enabled=%v status=%s", state.running, state.enabled, got.Running, got.Enabled, got.Status)
			}
			if got.Binary != "/usr/local/lib/mihari/mihari" {
				t.Fatalf("binary=%q args=%v", got.Binary, got.Args)
			}
		})
	}
}

func TestLaunchdInspect_PlistExistsUnloadedIsStopped(t *testing.T) {
	h := newLaunchdHarness(t, false, true, true)
	h.loaded = false
	got, err := h.adapter.InspectDefinition(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusStopped || got.Running {
		t.Fatalf("status=%s running=%v", got.Status, got.Running)
	}
}

func TestLaunchdInspect_MissingPlistAndJobIsNotInstalled(t *testing.T) {
	h := newLaunchdHarness(t, false, false, false)
	h.loaded = false
	got, err := h.adapter.InspectDefinition(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusNotInstalled {
		t.Fatalf("status=%s", got.Status)
	}
}

func TestLaunchdInspect_RejectsSymlinkPlist(t *testing.T) {
	for _, target := range []string{defaultDevNull, "/other/mihari.plist"} {
		h := newLaunchdHarness(t, false, false, true)
		h.files.links[defaultPlistPath] = target
		delete(h.files.files, defaultPlistPath)
		if _, err := h.adapter.InspectDefinition(context.Background()); err == nil {
			t.Fatal("symlink plist was accepted as empty regular bytes")
		} else {
			requireInvalidState(t, err)
		}
		if len(h.hook.kinds) != 0 {
			t.Fatal("unsupported definition caused mutation")
		}
	}
}

func TestLaunchdDisableAutostartAndStop_PersistentDisableThenBootout(t *testing.T) {
	for _, state := range definitionStates {
		t.Run(stateName(state.running, state.enabled), func(t *testing.T) {
			h := newLaunchdHarness(t, state.running, state.enabled, true)
			if !state.running {
				h.loaded = false
				h.adapter.BindStopAuthority(Definition{Status: StatusRunning, Process: fixtureLaunchdIdentity()}, "boot-test")
			}
			if err := h.adapter.DisableAutostartAndStop(context.Background()); err != nil {
				t.Fatal(err)
			}
			if !h.disabled {
				t.Fatal("persistent disable missing")
			}
			disableAt, printAt, bootoutAt := -1, -1, -1
			for i, argv := range h.runner.calls {
				joined := strings.Join(argv, " ")
				if strings.Contains(joined, "gui/") {
					t.Fatalf("used gui domain: %v", argv)
				}
				if argvHasPrefix(argv, []string{defaultLaunchctl, "disable", "system/mihari"}) && disableAt < 0 {
					disableAt = i
				}
				if argvHasPrefix(argv, []string{defaultLaunchctl, "print-disabled", "system"}) && disableAt >= 0 && printAt < 0 {
					printAt = i
				}
				if argvHasPrefix(argv, []string{defaultLaunchctl, "bootout", "system/mihari"}) {
					bootoutAt = i
				}
			}
			if disableAt < 0 || printAt < 0 || !(disableAt < printAt) || (state.running && (bootoutAt < 0 || printAt >= bootoutAt)) {
				t.Fatalf("want disable, verify, bootout; calls=%v", h.runner.calls)
			}
			if !containsKind(h.hook.kinds, DefinitionActionDisabled) || !containsKind(h.hook.kinds, DefinitionActionStop) {
				t.Fatalf("hook batched or skipped: %v", h.hook.kinds)
			}
		})
	}
}

func TestLaunchdWriteDefinition_FourStates(t *testing.T) {
	for _, state := range definitionStates {
		t.Run(stateName(state.running, state.enabled), func(t *testing.T) {
			h := newLaunchdHarness(t, false, false, false)
			def := Definition{
				Enabled: state.enabled,
				Running: state.running,
				Files:   []DefinitionFile{trustedPlistFile(t)},
			}
			if err := h.adapter.WriteDefinition(context.Background(), def); err != nil {
				t.Fatal(err)
			}
			if _, err := h.files.Read(context.Background(), defaultPlistPath); err != nil {
				t.Fatal(err)
			}
			bootstrapped := false
			for _, argv := range h.runner.calls {
				if containsArg(argv, "bootstrap") {
					bootstrapped = true
				}
				if strings.Contains(strings.Join(argv, " "), "gui/") {
					t.Fatalf("gui domain: %v", argv)
				}
			}
			if state.running {
				if err := h.adapter.Start(context.Background()); err != nil {
					t.Fatal(err)
				}
				if !h.loaded || !h.running {
					t.Fatal("expected bootstrap for running target")
				}
				if state.enabled == h.disabled {
					t.Fatalf("enabled=%v disabled=%v", state.enabled, h.disabled)
				}
				if !state.enabled {
					assertLaunchdRunningDisabledSequence(t, h.runner.calls)
				}
			} else if bootstrapped {
				t.Fatal("stopped target bootstrapped")
			}
			if !containsKind(h.hook.kinds, DefinitionActionDefinition) {
				t.Fatalf("definition not journaled: %v", h.hook.kinds)
			}
		})
	}
}

func TestLaunchdStart_RunningDisabledDoesNotLeaveEnabled(t *testing.T) {
	h := newLaunchdHarness(t, false, false, true)
	h.adapter.last = Definition{Enabled: false, Running: true, Files: []DefinitionFile{trustedPlistFile(t)}}
	if err := h.adapter.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertLaunchdRunningDisabledSequence(t, h.runner.calls)
	if !h.disabled || !h.running {
		t.Fatalf("running=%v disabled=%v", h.running, h.disabled)
	}
}

func TestLaunchdInspect_RunningRecordsStartIdentity(t *testing.T) {
	h := newLaunchdHarness(t, true, true, true)
	got, err := h.adapter.InspectDefinition(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Process.PID != 77 || got.Process.StartUnix == 0 || got.Process.BootID == "" {
		t.Fatalf("incomplete identity after inspect: %+v", got.Process)
	}
}

func TestLaunchdWaitOwnedTreeExit_IncompleteIdentityInvalidState(t *testing.T) {
	h := newLaunchdHarness(t, true, true, true)
	h.adapter.last = Definition{Process: ProcessIdentity{PID: 77}, Running: true}
	h.tree.alive = true
	if err := h.adapter.WaitOwnedTreeExit(context.Background()); err == nil {
		t.Fatal("incomplete identity treated as exited")
	} else {
		requireInvalidState(t, err)
	}
	if len(h.tree.signals) != 0 {
		t.Fatalf("signaled without identity: %v", h.tree.signals)
	}
}

func TestLaunchdWaitOwnedTreeExit_UsesGroupAbsenceWithoutSignals(t *testing.T) {
	h := newLaunchdHarness(t, true, true, true)
	if _, err := h.adapter.InspectDefinition(context.Background()); err != nil {
		t.Fatal(err)
	}
	h.tree.alive = true
	h.tree.empty = true
	if err := h.adapter.WaitOwnedTreeExit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(h.tree.signals) != 0 {
		t.Fatalf("signals=%v", h.tree.signals)
	}
}

func TestLaunchdDisableAutostartAndStop_SnapshotsIdentityBeforeBootout(t *testing.T) {
	h := newLaunchdHarness(t, true, true, true)
	if err := h.adapter.DisableAutostartAndStop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if h.adapter.last.Process.PID != 77 || incompleteProcessIdentity(h.adapter.last.Process) {
		t.Fatalf("disable did not snapshot identity: %+v", h.adapter.last.Process)
	}
}

func TestLaunchdBootout_NonZeroUnknownIsInvalidState(t *testing.T) {
	h := newLaunchdHarness(t, true, true, true)
	h.runner.handle(func(argv []string) bool {
		return argvHasPrefix(argv, []string{defaultLaunchctl, "bootout", "system/mihari"})
	}, func([]string) (CommandResult, error) {
		return CommandResult{ExitCode: 1, Stderr: []byte("bootout failed: unexpected\n")}, nil
	})
	if err := h.adapter.DisableAutostartAndStop(context.Background()); err == nil {
		t.Fatal("non-zero bootout succeeded")
	} else {
		requireInvalidState(t, err)
	}
}

func TestLaunchdStart_EnabledRequiresRunning(t *testing.T) {
	h := newLaunchdHarness(t, false, true, true)
	h.adapter.last = Definition{Enabled: true, Running: false, Files: []DefinitionFile{trustedPlistFile(t)}}
	h.runner.handle(func(argv []string) bool {
		return containsArg(argv, "bootstrap")
	}, func([]string) (CommandResult, error) {
		h.loaded = false
		h.running = false
		return CommandResult{}, nil
	})
	if err := h.adapter.Start(context.Background()); err == nil {
		t.Fatal("enabled start without running job succeeded")
	} else {
		requireInvalidState(t, err)
	}
}

func TestLaunchdInspect_MissingToolIsInvalidState(t *testing.T) {
	h := newLaunchdHarness(t, false, true, true)
	h.runner.handlers = nil
	h.runner.handle(func([]string) bool { return true }, func([]string) (CommandResult, error) {
		return CommandResult{}, invalidServiceState("service manager is unavailable")
	})
	_, err := h.adapter.InspectDefinition(context.Background())
	if err == nil {
		t.Fatal("missing tool succeeded")
	}
	requireInvalidState(t, err)
}

func TestLaunchdInspect_TrustedSnapshotParses(t *testing.T) {
	parsed, err := parseLaunchdPlist(loadDefinitionTestdata(t, "mihari.plist"))
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Binary != "/usr/local/lib/mihari/mihari" || strings.Join(parsed.Args, " ") != "daemon" {
		t.Fatalf("parsed %+v", parsed)
	}
}

func TestLaunchdPaths_SystemDomainOnly(t *testing.T) {
	paths := DefaultLaunchdPaths()
	if paths.Domain != "system" || strings.Contains(paths.Plist, "LaunchAgents") {
		t.Fatalf("%+v", paths)
	}
}

func assertLaunchdRunningDisabledSequence(t *testing.T, calls [][]string) {
	t.Helper()
	enableAt, bootstrapAt, disableAt := -1, -1, -1
	for i, argv := range calls {
		switch {
		case argvHasPrefix(argv, []string{defaultLaunchctl, "enable", "system/mihari"}) && enableAt < 0:
			enableAt = i
		case containsArg(argv, "bootstrap") && bootstrapAt < 0:
			bootstrapAt = i
		case argvHasPrefix(argv, []string{defaultLaunchctl, "disable", "system/mihari"}):
			disableAt = i
		}
	}
	if enableAt < 0 || bootstrapAt < 0 || disableAt < 0 || !(enableAt < bootstrapAt && bootstrapAt < disableAt) {
		t.Fatalf("want enable, bootstrap, disable; calls=%v", calls)
	}
}
