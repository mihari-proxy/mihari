package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

type systemdHarness struct {
	adapter *SystemdAdapter
	runner  *fakeRunner
	files   *memFS
	tree    *fakeTree
	clock   *fakeClock
	hook    *recordingHook
	show    string
}

func newSystemdHarness(t *testing.T, running, enabled bool, unit DefinitionFile, dropins ...DefinitionFile) *systemdHarness {
	t.Helper()
	files := newMemFS()
	if err := files.Write(context.Background(), unit); err != nil {
		t.Fatal(err)
	}
	dropinPaths := make([]string, 0, len(dropins))
	for _, dropin := range dropins {
		if err := files.Write(context.Background(), dropin); err != nil {
			t.Fatal(err)
		}
		dropinPaths = append(dropinPaths, dropin.Path)
	}
	if enabled {
		if err := files.Write(context.Background(), DefinitionFile{
			Path: DefaultSystemdPaths().WantsLink,
			Kind: "link",
			Mode: 0o777,
		}); err != nil {
			t.Fatal(err)
		}
		files.links[DefaultSystemdPaths().WantsLink] = defaultSystemdUnitFile
		delete(files.files, DefaultSystemdPaths().WantsLink)
	}
	hook := &recordingHook{}
	tree := &fakeTree{empty: !running, alive: running}
	clock := &fakeClock{now: time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)}
	runner := &fakeRunner{}
	h := &systemdHarness{
		runner: runner,
		files:  files,
		tree:   tree,
		clock:  clock,
		hook:   hook,
		show:   systemdShowOutput(running, enabled, strings.Join(dropinPaths, " ")),
	}
	h.adapter = newSystemdAdapter(systemdConfig{
		Runner: runner,
		Files:  files,
		Tree:   tree,
		Clock:  clock,
		Hook:   hook.wrap,
		Paths:  DefaultSystemdPaths(),
	})
	h.installSystemdHandlers()
	return h
}

func systemdShowOutput(running, enabled bool, dropins string) string {
	active := "inactive"
	sub := "dead"
	pid := "0"
	group := ""
	if running {
		active = "active"
		sub = "running"
		pid = "4242"
		group = "/system.slice/mihari.service"
	}
	unitFile := "disabled"
	if enabled {
		unitFile = "enabled"
	}
	return strings.Join([]string{
		"LoadState=loaded",
		"ActiveState=" + active,
		"SubState=" + sub,
		"MainPID=" + pid,
		"ControlGroup=" + group,
		"FragmentPath=" + defaultSystemdUnitFile,
		"DropInPaths=" + dropins,
		"UnitFileState=" + unitFile,
	}, "\n") + "\n"
}

func systemdMaskedShow() string {
	return strings.Join([]string{
		"LoadState=masked",
		"ActiveState=inactive",
		"SubState=dead",
		"MainPID=0",
		"ControlGroup=",
		"FragmentPath=/dev/null",
		"DropInPaths=",
		"UnitFileState=masked",
	}, "\n") + "\n"
}

func TestSystemdInspect_AutoRestartIsInstalledButNotRunning(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(stateName(false, enabled), func(t *testing.T) {
			h := newSystemdHarness(t, false, enabled, trustedUnitFile(t))
			h.show = strings.NewReplacer("ActiveState=inactive", "ActiveState=activating", "SubState=dead", "SubState=auto-restart").Replace(h.show)
			got, err := h.adapter.InspectDefinition(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != StatusStopped || got.Running || got.Enabled != enabled || got.Binary != "/usr/local/lib/mihari/mihari" {
				t.Fatalf("unexpected restart-wait definition: %+v", got)
			}
			if len(h.hook.kinds) != 0 {
				t.Fatal("inspection mutated the service")
			}
		})
	}
}

func TestSystemdInspect_RejectsOtherActivatingStates(t *testing.T) {
	for _, state := range []struct{ sub, pid string }{
		{"start", "0"}, {"start-pre", "0"}, {"unknown", "0"},
		{"auto-restart", "4242"}, {"auto-restart", "invalid"},
	} {
		t.Run(state.sub+"-"+state.pid, func(t *testing.T) {
			h := newSystemdHarness(t, false, true, trustedUnitFile(t))
			h.show = strings.NewReplacer("ActiveState=inactive", "ActiveState=activating", "SubState=dead", "SubState="+state.sub, "MainPID=0", "MainPID="+state.pid).Replace(h.show)
			_, err := h.adapter.InspectDefinition(context.Background())
			if err == nil {
				t.Fatal("accepted an unverified activating state")
			}
			requireInvalidState(t, err)
		})
	}
}

func TestSystemdDisableAutostartAndStop_AutoRestart(t *testing.T) {
	h := newSystemdHarness(t, false, true, trustedUnitFile(t))
	h.show = strings.NewReplacer("ActiveState=inactive", "ActiveState=activating", "SubState=dead", "SubState=auto-restart").Replace(h.show)
	if err := h.adapter.DisableAutostartAndStop(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertSystemdStopSequence(t, h.runner.calls)
	if !h.files.masked(defaultSystemdUnitFile) {
		t.Fatal("restart loop was not masked")
	}
	if _, err := h.files.ReadLink(context.Background(), DefaultSystemdPaths().WantsLink); !os.IsNotExist(err) {
		t.Fatalf("autostart link remains: %v", err)
	}
	if err := h.adapter.WaitOwnedTreeExit(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestSystemdInspect_RejectsSymlinkDropins(t *testing.T) {
	for _, target := range []string{defaultDevNull, "/other/config.conf"} {
		dropin := trustedDropinFile(t)
		h := newSystemdHarness(t, false, false, trustedUnitFile(t), dropin)
		h.files.links[dropin.Path] = target
		delete(h.files.files, dropin.Path)
		if _, err := h.adapter.InspectDefinition(context.Background()); err == nil {
			t.Fatal("symlink drop-in was accepted as empty regular bytes")
		} else {
			requireInvalidState(t, err)
		}
		if len(h.hook.kinds) != 0 {
			t.Fatal("unsupported definition caused mutation")
		}
	}
}

func (h *systemdHarness) installSystemdHandlers() {
	h.runner.handle(func(argv []string) bool {
		return argvHasPrefix(argv, []string{defaultSystemctl, "--system", "show"})
	}, func([]string) (CommandResult, error) {
		if h.files.masked(defaultSystemdUnitFile) && h.runner.reloaded {
			return CommandResult{Stdout: []byte(systemdMaskedShow())}, nil
		}
		return CommandResult{Stdout: []byte(h.show)}, nil
	})
	h.runner.handle(func(argv []string) bool {
		return argvHasPrefix(argv, []string{defaultSystemctl, "--system", "daemon-reload"})
	}, func([]string) (CommandResult, error) {
		return CommandResult{}, nil
	})
	h.runner.handle(func(argv []string) bool {
		return argvHasPrefix(argv, []string{defaultSystemctl, "--system", "stop"})
	}, func([]string) (CommandResult, error) {
		h.tree.empty = true
		h.tree.alive = false
		return CommandResult{}, nil
	})
	h.runner.handle(func(argv []string) bool {
		return argvHasPrefix(argv, []string{defaultSystemctl, "--system", "start"})
	}, func([]string) (CommandResult, error) {
		h.tree.empty = false
		h.tree.alive = true
		return CommandResult{}, nil
	})
	h.runner.handle(func(argv []string) bool {
		return argvHasPrefix(argv, []string{defaultSystemctl, "--system", "is-enabled"})
	}, func([]string) (CommandResult, error) {
		if strings.Contains(h.show, "UnitFileState=enabled") {
			return CommandResult{ExitCode: 0, Stdout: []byte("enabled\n")}, nil
		}
		return CommandResult{ExitCode: 1, Stdout: []byte("disabled\n")}, nil
	})
}

func TestSystemdInspect_FourStates(t *testing.T) {
	unit := trustedUnitFile(t)
	for _, state := range definitionStates {
		t.Run(stateName(state.running, state.enabled), func(t *testing.T) {
			h := newSystemdHarness(t, state.running, state.enabled, unit)
			got, err := h.adapter.InspectDefinition(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if got.Running != state.running || got.Enabled != state.enabled {
				t.Fatalf("running=%v enabled=%v got running=%v enabled=%v status=%s", state.running, state.enabled, got.Running, got.Enabled, got.Status)
			}
			if got.Binary != "/usr/local/lib/mihari/mihari" || strings.Join(got.Args, " ") != "daemon --system-service" {
				t.Fatalf("exec %+v %v", got.Binary, got.Args)
			}
			if got.Status != definitionStatus(true, state.running) {
				t.Fatalf("status=%s", got.Status)
			}
		})
	}
}

func TestSystemdDisableAutostartAndStop_MasksEtcThenReloadsThenStops(t *testing.T) {
	for _, state := range definitionStates {
		t.Run(stateName(state.running, state.enabled), func(t *testing.T) {
			h := newSystemdHarness(t, state.running, state.enabled, trustedUnitFile(t))
			if err := h.adapter.DisableAutostartAndStop(context.Background()); err != nil {
				t.Fatal(err)
			}
			if !h.files.masked(defaultSystemdUnitFile) {
				t.Fatal("did not mask /etc unit")
			}
			if h.files.links[defaultSystemdUnitFile] != defaultDevNull {
				t.Fatalf("mask target=%q", h.files.links[defaultSystemdUnitFile])
			}
			if !h.runner.reloaded {
				t.Fatal("daemon-reload missing")
			}
			assertSystemdStopSequence(t, h.runner.calls)
			if !containsKind(h.hook.kinds, DefinitionActionMask) || !containsKind(h.hook.kinds, DefinitionActionStop) {
				t.Fatalf("hook batched or skipped: %v", h.hook.kinds)
			}
			if state.enabled && !containsKind(h.hook.kinds, DefinitionActionDisable) {
				t.Fatalf("enable link not journaled: %v", h.hook.kinds)
			}
			if len(h.hook.kinds) < 2 {
				t.Fatalf("expected separate intent/done steps, got %v", h.hook.kinds)
			}
			if err := h.adapter.WaitOwnedTreeExit(context.Background()); err != nil {
				t.Fatal(err)
			}
			if !h.tree.empty {
				t.Fatal("cgroup not empty")
			}
		})
	}
}

func TestSystemdInspect_RejectsUnknownDropinStrangeExecAndMultipleCommands(t *testing.T) {
	ctx := context.Background()
	unit := trustedUnitFile(t)

	t.Run("unknown-dropin", func(t *testing.T) {
		dropin := DefinitionFile{
			Path:  "/usr/lib/systemd/system/mihari.service.d/other.conf",
			Bytes: []byte("[Service]\nLimitNOFILE=1\n"),
			Kind:  "dropin",
			Mode:  0o644,
		}
		h := newSystemdHarness(t, false, true, unit, dropin)
		_, err := h.adapter.InspectDefinition(ctx)
		if err == nil {
			t.Fatal("accepted unknown drop-in")
		}
		requireInvalidState(t, err)
	})

	t.Run("strange-exec", func(t *testing.T) {
		strange := unit
		strange.Bytes = []byte("[Service]\nExecStart=/bin/sh -c /usr/local/lib/mihari/mihari daemon\n")
		h := newSystemdHarness(t, false, true, strange)
		_, err := h.adapter.InspectDefinition(ctx)
		if err == nil {
			t.Fatal("accepted strange ExecStart")
		}
		requireInvalidState(t, err)
	})

	t.Run("multiple-commands", func(t *testing.T) {
		multiple := unit
		multiple.Bytes = []byte("[Service]\nExecStart=/usr/local/lib/mihari/mihari daemon\nExecStart=/usr/bin/true\n")
		h := newSystemdHarness(t, false, true, multiple)
		_, err := h.adapter.InspectDefinition(ctx)
		if err == nil {
			t.Fatal("accepted multiple ExecStart")
		}
		requireInvalidState(t, err)
	})
}

func TestSystemdInspect_TrustedDropinAllowed(t *testing.T) {
	h := newSystemdHarness(t, true, true, trustedUnitFile(t), trustedDropinFile(t))
	got, err := h.adapter.InspectDefinition(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Binary != "/usr/local/lib/mihari/mihari" {
		t.Fatalf("binary=%q", got.Binary)
	}
	if len(got.Files) < 2 {
		t.Fatalf("expected unit and drop-in, got %d files", len(got.Files))
	}
}

func TestSystemdInspect_GlobalServiceDropinNotOwned(t *testing.T) {
	for _, tc := range []struct {
		name string
		path string
	}{
		{"fedora", "/usr/lib/systemd/system/service.d/10-timeout-abort.conf"},
		{"custom-global-root", "/srv/systemd/system/service.d/10-custom.conf"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			global := DefinitionFile{
				Path:  tc.path,
				Bytes: []byte("[Service]\nTimeoutStopFailureMode=abort\n"),
				Kind:  "dropin",
				Mode:  0o644,
			}
			h := newSystemdHarness(t, false, true, trustedUnitFile(t), global)
			got, err := h.adapter.InspectDefinition(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if got.Binary != "/usr/local/lib/mihari/mihari" {
				t.Fatalf("binary=%q", got.Binary)
			}
			for _, file := range got.Files {
				if file.Path == global.Path {
					t.Fatalf("global drop-in was treated as owned: %s", file.Path)
				}
			}
		})
	}
}

func TestSystemdInspect_GlobalServiceDropinRejectsExec(t *testing.T) {
	for _, key := range []string{"ExecStart", "ExecStartPre", "ExecStartPost", "ExecStop", "ExecStopPost", "ExecReload", "ExecCondition"} {
		t.Run(key, func(t *testing.T) {
			global := DefinitionFile{
				Path:  "/etc/systemd/system/service.d/99-exec.conf",
				Bytes: []byte("[Service]\n" + key + "=/usr/bin/true\n"),
				Kind:  "dropin",
				Mode:  0o644,
			}
			h := newSystemdHarness(t, false, true, trustedUnitFile(t), global)
			_, err := h.adapter.InspectDefinition(context.Background())
			if err == nil {
				t.Fatal("accepted an execution command in a global drop-in")
			}
			requireInvalidState(t, err)
			if len(h.hook.kinds) != 0 {
				t.Fatal("unsupported global drop-in caused mutation")
			}
		})
	}
}

func TestSystemdInspect_GlobalServiceDropinRejectsSpacedExec(t *testing.T) {
	global := DefinitionFile{
		Path:  "/usr/lib/systemd/system/service.d/10-exec.conf",
		Bytes: []byte("[Service]\nExecStart =\nExecStart =/opt/wrapper/mihari daemon\n"),
		Kind:  "dropin",
		Mode:  0o644,
	}
	h := newSystemdHarness(t, false, true, trustedUnitFile(t), global)
	_, err := h.adapter.InspectDefinition(context.Background())
	if err == nil {
		t.Fatal("accepted a global drop-in whose ExecStart key has space before '='")
	}
	requireInvalidState(t, err)
	if len(h.hook.kinds) != 0 {
		t.Fatal("spaced global exec caused mutation")
	}
}

func TestSystemdInspect_GlobalServiceDropinRejectsCommentContinuation(t *testing.T) {
	for _, marker := range []string{"#", ";"} {
		t.Run(marker, func(t *testing.T) {
			global := DefinitionFile{
				Path:  "/usr/lib/systemd/system/service.d/10-exec.conf",
				Bytes: []byte("[Service]\n" + marker + " comment \\\nExecStartPre=/usr/bin/true\n"),
				Kind:  "dropin",
				Mode:  0o644,
			}
			h := newSystemdHarness(t, false, true, trustedUnitFile(t), global)
			_, err := h.adapter.InspectDefinition(context.Background())
			if err == nil {
				t.Fatal("accepted an execution command hidden by a comment continuation")
			}
			requireInvalidState(t, err)
			if len(h.hook.kinds) != 0 {
				t.Fatal("comment continuation caused mutation")
			}
		})
	}
}

func TestSystemdInspect_DropinRejectsExecAfterNonContinuation(t *testing.T) {
	for _, scope := range []string{"global", "owned"} {
		t.Run(scope, func(t *testing.T) {
			for _, tc := range []struct {
				name   string
				suffix string
			}{
				{"double-backslash", `\\`},
				{"four-backslashes", `\\\\`},
				{"backslash-space", "\\ "},
				{"backslash-tab", "\\\t"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					dropin := trustedDropinFile(t)
					if scope == "global" {
						dropin.Path = "/usr/lib/systemd/system/service.d/99-exec.conf"
					}
					dropin.Bytes = []byte("[Service]\nEnvironment=REVIEW=value" + tc.suffix + "\nExecStartPre=/usr/bin/false\n")
					h := newSystemdHarness(t, false, true, trustedUnitFile(t), dropin)
					_, err := h.adapter.InspectDefinition(context.Background())
					if err == nil {
						t.Fatal("accepted an execution command after a line that does not continue")
					}
					requireInvalidState(t, err)
					if len(h.hook.kinds) != 0 {
						t.Fatal("unsupported drop-in caused mutation")
					}
				})
			}
		})
	}
}

func TestSystemdInspect_GlobalServiceDropinContinuationAllowed(t *testing.T) {
	global := DefinitionFile{
		Path:  "/usr/lib/systemd/system/service.d/10-global.conf",
		Bytes: []byte("[Service]\nEnvironment=FIRST=one\\\n# comment\nSECOND=two\nTimeoutStopFailureMode=abort\n"),
		Kind:  "dropin",
		Mode:  0o644,
	}
	h := newSystemdHarness(t, false, true, trustedUnitFile(t), global)
	got, err := h.adapter.InspectDefinition(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Binary != "/usr/local/lib/mihari/mihari" {
		t.Fatalf("binary=%q", got.Binary)
	}
	if len(got.Files) != 1 || got.Files[0].Path != defaultSystemdUnitFile {
		t.Fatalf("expected only the owned unit, got %+v", got.Files)
	}
	if len(h.hook.kinds) != 0 {
		t.Fatal("global drop-in inspection caused mutation")
	}
}

func TestSystemdInspect_OwnedDropinRejectsHiddenExec(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "spaced-exec", body: "[Service]\nExecStop =/usr/bin/true\n"},
		{name: "comment-continuation", body: "[Service]\n# comment \\\nExecStartPre=/usr/bin/true\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			owned := trustedDropinFile(t)
			owned.Bytes = []byte(tc.body)
			h := newSystemdHarness(t, false, true, trustedUnitFile(t), owned)
			_, err := h.adapter.InspectDefinition(context.Background())
			if err == nil {
				t.Fatal("accepted a hidden execution command in an owned drop-in")
			}
			requireInvalidState(t, err)
		})
	}
}

func substituteFixtureOnce(raw, old, replacement string) (string, error) {
	if got := strings.Count(raw, old); got != 1 {
		return "", fmt.Errorf("fixture text %q matched %d times, want 1", old, got)
	}
	return strings.Replace(raw, old, replacement, 1), nil
}

func TestSubstituteFixtureOnce_RequiresOneMatch(t *testing.T) {
	if _, err := substituteFixtureOnce("ExecStart=/bin/mihari", "ExecStartMissing=", "x"); err == nil {
		t.Fatal("accepted a fixture substitution that matched nothing")
	}
	got, err := substituteFixtureOnce("ExecStart=/bin/mihari", "ExecStart=", "ExecStart =")
	if err != nil {
		t.Fatal(err)
	}
	if got != "ExecStart =/bin/mihari" {
		t.Fatalf("substituted %q", got)
	}
	if _, err := substituteFixtureOnce("ExecStart=/a\nExecStart=/b", "ExecStart=", "x"); err == nil {
		t.Fatal("accepted a fixture substitution that matched twice")
	}
}

func TestSystemdInspect_SpacedExecStartStillIdentifiesUnit(t *testing.T) {
	unit := trustedUnitFile(t)
	replaced, err := substituteFixtureOnce(string(unit.Bytes), "ExecStart=", "ExecStart =")
	if err != nil {
		t.Fatal(err)
	}
	unit.Bytes = []byte(replaced)
	h := newSystemdHarness(t, false, true, unit)
	got, err := h.adapter.InspectDefinition(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Binary != "/usr/local/lib/mihari/mihari" || strings.Join(got.Args, " ") != "daemon --system-service" {
		t.Fatalf("spaced ExecStart parsed as %+v", got)
	}
}

func TestSystemdInspect_CommentInsideContinuationStillIdentifiesUnit(t *testing.T) {
	unit := trustedUnitFile(t)
	replaced, err := substituteFixtureOnce(string(unit.Bytes),
		`ExecStart="/usr/local/lib/mihari/mihari" daemon --system-service`,
		"ExecStart=\"/usr/local/lib/mihari/mihari\" \\\n# comment\n  daemon --system-service")
	if err != nil {
		t.Fatal(err)
	}
	unit.Bytes = []byte(replaced)
	h := newSystemdHarness(t, false, true, unit)
	got, err := h.adapter.InspectDefinition(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Binary != "/usr/local/lib/mihari/mihari" || strings.Join(got.Args, " ") != "daemon --system-service" {
		t.Fatalf("continued ExecStart parsed as %+v", got)
	}
}

func TestSystemdInspect_GlobalServiceDropinReadFailurePreservesCause(t *testing.T) {
	for _, tc := range []struct {
		name  string
		cause error
	}{
		{"permission-denied", os.ErrPermission},
		{"io-failure", errors.New("fixture global drop-in read failure")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			global := DefinitionFile{
				Path:  "/usr/lib/systemd/system/service.d/10-global.conf",
				Bytes: []byte("[Service]\nTimeoutStopFailureMode=abort\n"),
				Kind:  "dropin",
				Mode:  0o644,
			}
			h := newSystemdHarness(t, false, true, trustedUnitFile(t), global)
			h.files.readErrors = map[string]error{
				global.Path: &os.PathError{Op: "open", Path: global.Path, Err: tc.cause},
			}
			_, err := h.adapter.InspectDefinition(context.Background())
			if err == nil {
				t.Fatal("accepted a global drop-in that could not be read")
			}
			requireInvalidState(t, err)
			if !errors.Is(err, tc.cause) {
				t.Fatalf("lost global drop-in read cause: %v", err)
			}
			if len(h.hook.kinds) != 0 {
				t.Fatal("global drop-in read failure caused mutation")
			}
		})
	}
}

func TestSystemdInspect_GlobalServiceDropinRejectsMissingOrInvalid(t *testing.T) {
	for _, condition := range []string{"missing", "symlink", "mask", "malformed", "exec-reset"} {
		t.Run(condition, func(t *testing.T) {
			global := DefinitionFile{
				Path:  "/usr/lib/systemd/system/service.d/10-global.conf",
				Bytes: []byte("[Service]\nTimeoutStopFailureMode=abort\n"),
				Kind:  "dropin",
				Mode:  0o644,
			}
			h := newSystemdHarness(t, false, true, trustedUnitFile(t), global)
			switch condition {
			case "missing":
				delete(h.files.files, global.Path)
			case "symlink", "mask":
				delete(h.files.files, global.Path)
				if condition == "mask" {
					h.files.links[global.Path] = defaultDevNull
				} else {
					h.files.links[global.Path] = "/other/global.conf"
				}
			case "malformed":
				global.Bytes = []byte("[Service\n")
				h.files.files[global.Path] = global
			case "exec-reset":
				global.Bytes = []byte("[Service]\nExecStart=\nExecStart=/opt/wrapper/mihari daemon\n")
				h.files.files[global.Path] = global
			}
			_, err := h.adapter.InspectDefinition(context.Background())
			if err == nil {
				t.Fatal("accepted an unreadable or unsupported global drop-in")
			}
			requireInvalidState(t, err)
		})
	}
}

func TestSystemdInspect_GlobalServiceDropinKeepsOwnedValidation(t *testing.T) {
	for _, condition := range []string{"supported", "exec", "foreign-path"} {
		t.Run(condition, func(t *testing.T) {
			global := DefinitionFile{
				Path:  "/srv/systemd/system/service.d/10-global.conf",
				Bytes: []byte("[Service]\nTimeoutStopFailureMode=abort\n"),
				Kind:  "dropin",
				Mode:  0o644,
			}
			owned := trustedDropinFile(t)
			switch condition {
			case "exec":
				owned.Bytes = []byte("[Service]\nExecStop=/usr/bin/true\n")
			case "foreign-path":
				owned.Path = "/usr/lib/systemd/system/mihari.service.d/other.conf"
			}
			h := newSystemdHarness(t, false, true, trustedUnitFile(t), global, owned)
			got, err := h.adapter.InspectDefinition(context.Background())
			if condition != "supported" {
				if err == nil {
					t.Fatal("global drop-in bypassed unit-specific validation")
				}
				requireInvalidState(t, err)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Files) != 2 || got.Files[0].Path != defaultSystemdUnitFile || got.Files[1].Path != owned.Path {
				t.Fatalf("expected only unit and owned drop-in, got %+v", got.Files)
			}
		})
	}
}

func TestSystemdInspect_MissingToolIsInvalidState(t *testing.T) {
	h := newSystemdHarness(t, false, false, trustedUnitFile(t))
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

func TestSystemdWaitOwnedTreeExit_SignalsVerifiedCgroupAfterTimeout(t *testing.T) {
	h := newSystemdHarness(t, true, true, trustedUnitFile(t))
	h.tree.empty = false
	h.tree.emptyAfter = 2
	def, err := h.adapter.InspectDefinition(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if def.Process.Group != "/system.slice/mihari.service" {
		t.Fatalf("group=%q", def.Process.Group)
	}
	if err := h.adapter.WaitOwnedTreeExit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(h.tree.signals) != 2 || h.tree.signals[0] != "TERM" || h.tree.signals[1] != "KILL" {
		t.Fatalf("signals=%v", h.tree.signals)
	}
	for _, group := range h.tree.groups {
		if group != "/system.slice/mihari.service" {
			t.Fatalf("signaled %q", group)
		}
	}
	if sleepSum(h.clock.sleeps) < stopWait+termWait {
		t.Fatalf("timeout too short: %v", h.clock.sleeps)
	}
}

func TestSystemdInspect_RunningRecordsBootAndStartIdentity(t *testing.T) {
	h := newSystemdHarness(t, true, true, trustedUnitFile(t))
	got, err := h.adapter.InspectDefinition(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Process.PID != 4242 || got.Process.StartUnix == 0 || got.Process.BootID == "" || got.Process.Group == "" {
		t.Fatalf("incomplete identity after inspect: %+v", got.Process)
	}
}

func TestSystemdWaitOwnedTreeExit_IncompleteIdentityInvalidState(t *testing.T) {
	h := newSystemdHarness(t, true, true, trustedUnitFile(t))
	h.adapter.last = Definition{
		Running: true,
		Process: ProcessIdentity{PID: 4242, Group: "/system.slice/mihari.service"},
	}
	if err := h.adapter.WaitOwnedTreeExit(context.Background()); err == nil {
		t.Fatal("incomplete identity treated as exited")
	} else {
		requireInvalidState(t, err)
	}
	if len(h.tree.signals) != 0 {
		t.Fatalf("signaled without identity: %v", h.tree.signals)
	}
}

func TestSystemdDisableAutostartAndStop_ReloadIsSeparateHook(t *testing.T) {
	h := newSystemdHarness(t, true, true, trustedUnitFile(t))
	if err := h.adapter.DisableAutostartAndStop(context.Background()); err != nil {
		t.Fatal(err)
	}
	maskAt, reloadAt, stopAt := -1, -1, -1
	for i, kind := range h.hook.kinds {
		switch kind {
		case DefinitionActionMask:
			if maskAt < 0 {
				maskAt = i
			}
		case DefinitionActionReload:
			if reloadAt < 0 {
				reloadAt = i
			}
		case DefinitionActionStop:
			stopAt = i
		}
	}
	if maskAt < 0 || reloadAt < 0 || stopAt < 0 || !(maskAt < reloadAt && reloadAt < stopAt) {
		t.Fatalf("want mask, reload, stop hooks; got %v", h.hook.kinds)
	}
}

func TestSystemdWriteDefinition_ReloadIsHooked(t *testing.T) {
	h := newSystemdHarness(t, false, false, trustedUnitFile(t))
	def := Definition{Enabled: true, Files: []DefinitionFile{trustedUnitFile(t)}}
	if err := h.adapter.WriteDefinition(context.Background(), def); err != nil {
		t.Fatal(err)
	}
	if !containsKind(h.hook.kinds, DefinitionActionReload) {
		t.Fatalf("reload not journaled: %v", h.hook.kinds)
	}
}

func TestSystemdStop_NonZeroExitIsInvalidState(t *testing.T) {
	h := newSystemdHarness(t, true, false, trustedUnitFile(t))
	h.runner.handle(func(argv []string) bool {
		return argvHasPrefix(argv, []string{defaultSystemctl, "--system", "stop"})
	}, func([]string) (CommandResult, error) {
		return CommandResult{ExitCode: 1, Stderr: []byte("stop failed\n")}, nil
	})
	if err := h.adapter.DisableAutostartAndStop(context.Background()); err == nil {
		t.Fatal("non-zero stop succeeded")
	} else {
		requireInvalidState(t, err)
	}
}

func TestSystemdStop_RequiresInactiveShow(t *testing.T) {
	h := newSystemdHarness(t, true, false, trustedUnitFile(t))
	stopped := false
	h.runner.handle(func(argv []string) bool {
		return argvHasPrefix(argv, []string{defaultSystemctl, "--system", "stop"})
	}, func([]string) (CommandResult, error) {
		stopped = true
		return CommandResult{}, nil
	})
	h.runner.handle(func(argv []string) bool {
		return argvHasPrefix(argv, []string{defaultSystemctl, "--system", "show"})
	}, func([]string) (CommandResult, error) {
		if stopped {
			return CommandResult{Stdout: []byte(systemdShowOutput(true, false, ""))}, nil
		}
		if h.files.masked(defaultSystemdUnitFile) && h.runner.reloaded {
			return CommandResult{Stdout: []byte(systemdMaskedShow())}, nil
		}
		return CommandResult{Stdout: []byte(h.show)}, nil
	})
	if err := h.adapter.DisableAutostartAndStop(context.Background()); err == nil {
		t.Fatal("stop without inactive show succeeded")
	} else {
		requireInvalidState(t, err)
	}
}

func TestSystemdStart_RequiresRunning(t *testing.T) {
	h := newSystemdHarness(t, false, true, trustedUnitFile(t))
	if err := h.adapter.Start(context.Background()); err == nil {
		t.Fatal("start without running unit succeeded")
	} else {
		requireInvalidState(t, err)
	}
}

func TestCgroupEmpty_DirWithoutEventsIsInvalidState(t *testing.T) {
	empty, err := classifyCgroupEmpty(false, os.ErrNotExist, nil)
	if err != nil || !empty {
		t.Fatalf("missing dir: empty=%v err=%v", empty, err)
	}
	_, err = classifyCgroupEmpty(true, os.ErrNotExist, nil)
	if err == nil {
		t.Fatal("dir without cgroup.events treated as empty")
	}
	requireInvalidState(t, err)
	empty, err = classifyCgroupEmpty(true, nil, []byte("populated=0\n"))
	if err != nil || !empty {
		t.Fatalf("populated=0: empty=%v err=%v", empty, err)
	}
}

func TestSystemdWriteDefinition_DoesNotStart(t *testing.T) {
	h := newSystemdHarness(t, false, false, trustedUnitFile(t))
	def := Definition{
		Enabled: true,
		Running: false,
		Files:   []DefinitionFile{trustedUnitFile(t)},
	}
	if err := h.adapter.WriteDefinition(context.Background(), def); err != nil {
		t.Fatal(err)
	}
	for _, argv := range h.runner.calls {
		if containsArg(argv, "start") {
			t.Fatalf("install started service: %v", argv)
		}
	}
	if !containsKind(h.hook.kinds, DefinitionActionDefinition) {
		t.Fatalf("definition not journaled: %v", h.hook.kinds)
	}
}

func assertSystemdStopSequence(t *testing.T, calls [][]string) {
	t.Helper()
	reloadAt, showAt, stopAt := -1, -1, -1
	for i, argv := range calls {
		switch {
		case containsArg(argv, "daemon-reload"):
			if reloadAt < 0 {
				reloadAt = i
			}
		case containsArg(argv, "show") && reloadAt >= 0 && showAt < 0:
			showAt = i
		case containsArg(argv, "stop"):
			stopAt = i
		}
		if containsArg(argv, "--runtime") {
			t.Fatalf("used runtime mask: %v", argv)
		}
		if argv[0] != defaultSystemctl {
			t.Fatalf("non-absolute or unexpected tool %v", argv)
		}
	}
	if reloadAt < 0 || showAt < 0 || stopAt < 0 || !(reloadAt < showAt && showAt < stopAt) {
		t.Fatalf("want mask reload, verify, stop; calls=%v", calls)
	}
}

func stateName(running, enabled bool) string {
	return strings.Join([]string{
		map[bool]string{true: "running", false: "stopped"}[running],
		map[bool]string{true: "enabled", false: "disabled"}[enabled],
	}, "+")
}

func containsKind(kinds []string, want string) bool {
	for _, kind := range kinds {
		if kind == want {
			return true
		}
	}
	return false
}

func sleepSum(values []time.Duration) time.Duration {
	var total time.Duration
	for _, value := range values {
		total += value
	}
	return total
}

func TestSystemdInspect_TrustedSnapshotParses(t *testing.T) {
	parsed, err := parseSystemdUnit(loadDefinitionTestdata(t, "mihari.service"))
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Binary != "/usr/local/lib/mihari/mihari" || strings.Join(parsed.Args, " ") != "daemon --system-service" {
		t.Fatalf("parsed %+v", parsed)
	}
	if _, err := parseSystemdUnit(loadDefinitionTestdata(t, "10-mihari.conf")); err != nil {
		t.Fatal(err)
	}
}

func TestSystemdInspect_MaskedUnitRetainsRemovalSnapshot(t *testing.T) {
	h := newSystemdHarness(t, false, false, trustedUnitFile(t))
	h.show = systemdMaskedShow()
	if err := h.files.Mask(context.Background(), defaultSystemdUnitFile, defaultDevNull); err != nil {
		t.Fatal(err)
	}
	def, err := h.adapter.InspectDefinition(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !def.Masked || len(def.Links) != 1 || def.Links[0].Path != defaultSystemdUnitFile || def.Links[0].Target != defaultDevNull {
		t.Fatalf("masked unit missing from definition removal snapshot: %+v", def)
	}
}

func TestSystemdDisableAutostartAndStop_PreservesExistingMaskBarrier(t *testing.T) {
	h := newSystemdHarness(t, false, true, trustedUnitFile(t))
	h.show = systemdMaskedShow()
	if err := h.files.Mask(context.Background(), defaultSystemdUnitFile, defaultDevNull); err != nil {
		t.Fatal(err)
	}
	h.adapter.hook = func(ctx context.Context, action DefinitionAction, apply func(context.Context) error) error {
		if err := apply(ctx); err != nil {
			return err
		}
		if !h.files.masked(defaultSystemdUnitFile) {
			t.Fatalf("existing mask barrier removed during %s", action.Kind)
		}
		return nil
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := h.adapter.DisableAutostartAndStop(context.Background()); err != nil {
			t.Fatal(err)
		}
		if _, present := h.files.links[DefaultSystemdPaths().WantsLink]; present {
			t.Fatal("autostart link was not disabled")
		}
	}
}

func TestSystemdWriteDefinition_ReconstructsMaskedTarget(t *testing.T) {
	h := newSystemdHarness(t, false, false, trustedUnitFile(t))
	if err := h.files.Remove(context.Background(), defaultSystemdUnitFile); err != nil {
		t.Fatal(err)
	}
	def := Definition{Status: StatusStopped, Masked: true, Links: []DefinitionLink{{Path: defaultSystemdUnitFile, Target: defaultDevNull}}}
	if err := h.adapter.WriteDefinition(context.Background(), def); err != nil {
		t.Fatal(err)
	}
	if !h.files.masked(defaultSystemdUnitFile) {
		t.Fatal("masked target definition was not reconstructed")
	}
}

func TestSystemdPaths_NotRuntime(t *testing.T) {
	paths := DefaultSystemdPaths()
	if strings.Contains(paths.UnitFile, "/run/") || !strings.HasPrefix(paths.UnitFile, "/etc/") {
		t.Fatalf("unit file %q", paths.UnitFile)
	}
}
