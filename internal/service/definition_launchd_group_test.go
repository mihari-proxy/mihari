package service

import (
	"context"
	"encoding/binary"
	"strings"
	"syscall"
	"testing"
)

func fixtureLaunchdIdentity() ProcessIdentity {
	return ProcessIdentity{PID: 77, BootID: "boot-test", StartUnix: 1700000000, StartUsec: 42, Group: "darwin-pgid-v1:boot-test:77:1700000000:42"}
}

func TestLaunchdWaitOwnedTreeExit_DaemonExitDoesNotProveGroupExit(t *testing.T) {
	h := newLaunchdHarness(t, false, false, true)
	h.adapter.last = Definition{Status: StatusRunning, Process: fixtureLaunchdIdentity()}
	h.tree.alive, h.tree.empty = false, false
	if err := h.adapter.WaitOwnedTreeExit(context.Background()); err == nil {
		t.Fatal("daemon exit accepted while its process group still has members")
	}
	if len(h.tree.signals) != 0 {
		t.Fatal("lost leader identity authorized a signal")
	}
}

func TestLaunchdStopAuthority_UnloadedInspectCannotEraseBackup(t *testing.T) {
	h := newLaunchdHarness(t, false, false, true)
	binder, ok := any(h.adapter).(interface{ BindStopAuthority(Definition, string) })
	if !ok {
		t.Fatal("adapter cannot retain the durable stop authority")
	}
	binder.BindStopAuthority(Definition{Status: StatusRunning, Process: fixtureLaunchdIdentity()}, "boot-test")
	if _, err := h.adapter.InspectDefinition(context.Background()); err != nil {
		t.Fatal(err)
	}
	h.tree.empty = false
	if err := h.adapter.WaitOwnedTreeExit(context.Background()); err == nil {
		t.Fatal("fresh unloaded observation erased unresolved durable group")
	}
	h.tree.empty = true
	if err := h.adapter.WaitOwnedTreeExit(context.Background()); err != nil {
		t.Fatal("empty recorded group could not finish", err)
	}
}

func TestLaunchdStop_RejectsMissingGroupAuthority(t *testing.T) {
	for _, running := range []bool{false, true} {
		h := newLaunchdHarness(t, running, true, true)
		h.tree.identity = ProcessIdentity{PID: 77, BootID: "boot-test", StartUnix: 1700000000, StartUsec: 42}
		if err := h.adapter.DisableAutostartAndStop(context.Background()); err == nil {
			t.Fatal("legacy instance without process-group proof authorized bootout")
		}
		for _, call := range h.runner.calls {
			if containsArg(call, "bootout") {
				t.Fatal("legacy service was bootouted")
			}
		}
	}
}

func TestLaunchdStopReplay_UnloadedJobDoesNotProveEmptyGroup(t *testing.T) {
	for _, replay := range []bool{false, true} {
		h := newLaunchdHarness(t, false, false, true)
		h.adapter.last = Definition{Status: StatusRunning, Process: fixtureLaunchdIdentity()}
		h.tree.empty = false
		action := DefinitionAction{Kind: DefinitionActionStop, NewState: "unloaded"}
		var err error
		if replay {
			err = h.adapter.ReplayAction(context.Background(), action)
		} else {
			_, err = h.adapter.ObserveAction(context.Background(), action)
		}
		if err == nil {
			t.Fatal("unloaded job bypassed group exit during recovery")
		}
	}
}

func TestLaunchdStopReplay_BootoutMustJoinRemainingGroup(t *testing.T) {
	h := newLaunchdHarness(t, true, false, true)
	h.adapter.BindStopAuthority(Definition{Status: StatusRunning, Process: fixtureLaunchdIdentity()}, "boot-test")
	h.runner.handle(func(argv []string) bool { return containsArg(argv, "bootout") }, func([]string) (CommandResult, error) {
		h.loaded, h.running, h.tree.alive = false, false, false
		h.tree.empty = false
		return CommandResult{}, nil
	})
	if err := h.adapter.ReplayAction(context.Background(), DefinitionAction{Kind: DefinitionActionStop}); err == nil {
		t.Fatal("replayed bootout returned before its remaining group exited")
	}
}

func TestLaunchdStopAuthority_RejectsNewInstanceAfterBackup(t *testing.T) {
	for _, replay := range []bool{false, true} {
		h := newLaunchdHarness(t, true, true, true)
		h.adapter.BindStopAuthority(Definition{Status: StatusRunning, Process: fixtureLaunchdIdentity()}, "boot-test")
		h.tree.identity.StartUsec++
		h.tree.identity.Group = "darwin-pgid-v1:boot-test:77:1700000000:43"
		var err error
		if replay {
			err = h.adapter.ReplayAction(context.Background(), DefinitionAction{Kind: DefinitionActionStop})
		} else {
			err = h.adapter.DisableAutostartAndStop(context.Background())
		}
		if err == nil {
			t.Fatal("unrecorded replacement instance was stopped")
		}
		for _, call := range h.runner.calls {
			if containsArg(call, "bootout") {
				t.Fatal("replacement instance received bootout")
			}
		}
	}
}

func TestLaunchdStopAuthority_CrossBootDoesNotUseOldGroup(t *testing.T) {
	h := newLaunchdHarness(t, false, false, true)
	id := fixtureLaunchdIdentity()
	id.BootID, id.Group = "old-boot", "darwin-pgid-v1:old-boot:77:1700000000:42"
	h.adapter.BindStopAuthority(Definition{Status: StatusRunning, Process: id}, "old-boot")
	h.tree.empty = false
	if err := h.adapter.WaitOwnedTreeExit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(h.clock.sleeps) != 0 || len(h.tree.signals) != 0 {
		t.Fatal("cross-boot wait accessed old process group")
	}
	h.running, h.loaded = true, true
	if err := h.adapter.DisableAutostartAndStop(context.Background()); err == nil {
		t.Fatal("old boot proof authorized a current service")
	}
}

func TestDarwinGroupToken_RejectsMalformedOrMismatchedIdentity(t *testing.T) {
	id := fixtureLaunchdIdentity()
	if err := validateDarwinGroup(id); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"", "/system.slice/mihari.service", "darwin-pgid-v1:boot-test:077:1700000000:42", "darwin-pgid-v1:boot-test:77:1700000000:1000000", "darwin-pgid-v1:boot-test:1:1700000000:42", "darwin-pgid-v1:other:77:1700000000:42", "darwin-pgid-v1:boot-test:77:1700000001:42", strings.Repeat("a", 161)} {
		id.Group = token
		if err := validateDarwinGroup(id); err == nil {
			t.Fatal("unbound or noncanonical group accepted")
		}
	}
}

func fixtureDarwinArgs(argv ...string) []byte {
	raw := make([]byte, 4)
	binary.LittleEndian.PutUint32(raw, uint32(len(argv)))
	raw = append(raw, []byte("/opt/mihari/mihari\x00\x00")...)
	for _, arg := range argv {
		raw = append(raw, []byte(arg)...)
		raw = append(raw, 0)
	}
	return raw
}

func TestDarwinProcessIdentity_RequiresActualMarkedArguments(t *testing.T) {
	for _, marked := range []bool{false, true} {
		for _, leader := range []bool{false, true} {
			id := fixtureLaunchdIdentity()
			id.Group = ""
			argv := []string{"/opt/mihari/mihari", "daemon", "--system-service"}
			if marked {
				argv = append(argv, "--launchd-process-group")
			}
			raw := append(fixtureDarwinArgs(argv...), []byte("ENV=--launchd-process-group\x00")...)
			got, err := identifyLaunchdProcess(context.Background(), id.PID, func(context.Context, int) (darwinProcessObservation, error) {
				pgid := id.PID
				if !leader {
					pgid++
				}
				return darwinProcessObservation{identity: id, group: pgid}, nil
			}, func(int) ([]byte, error) { return raw, nil })
			if err != nil {
				t.Fatal(err)
			}
			if (got.Group != "") != (marked && leader) {
				t.Fatal("group proof did not depend on actual argv and live PGID")
			}
		}
	}
}

func TestDarwinProcessIdentity_RejectsChangeAcrossArgumentsRead(t *testing.T) {
	id := fixtureLaunchdIdentity()
	id.Group = ""
	reads := 0
	_, err := identifyLaunchdProcess(context.Background(), id.PID, func(context.Context, int) (darwinProcessObservation, error) {
		reads++
		got := id
		if reads > 1 {
			got.StartUsec++
		}
		return darwinProcessObservation{identity: got, group: id.PID}, nil
	}, func(int) ([]byte, error) {
		return fixtureDarwinArgs("/opt/mihari/mihari", "daemon", "--system-service", "--launchd-process-group"), nil
	})
	if err == nil {
		t.Fatal("argv from a different process generation accepted")
	}
}

func TestDarwinProcArgs_RejectsTruncationAndBounds(t *testing.T) {
	valid := fixtureDarwinArgs("/opt/mihari/mihari", "daemon", "--system-service", "--launchd-process-group")
	for _, raw := range [][]byte{nil, valid[:3], valid[:len(valid)-1], make([]byte, darwinProcArgsMax+1), {255, 255, 255, 255, 'x', 0}} {
		if _, err := parseDarwinProcArgs(raw); err == nil {
			t.Fatal("malformed process arguments accepted")
		}
	}
}

func TestDarwinGroupExit_OnlyESRCHProvesAbsence(t *testing.T) {
	for _, probeErr := range []error{nil, syscall.ESRCH, syscall.EPERM, syscall.EIO} {
		id := fixtureLaunchdIdentity()
		empty, err := observeLaunchdGroupExit(context.Background(), id.Group, func(context.Context) (string, error) { return id.BootID, nil }, func(context.Context, int) (darwinProcessObservation, error) { return darwinProcessObservation{}, nil }, func(pgid int) error {
			if pgid != -id.PID {
				t.Fatal("wrong PGID probed")
			}
			return probeErr
		})
		if empty != (probeErr == syscall.ESRCH) || (probeErr != nil && probeErr != syscall.ESRCH && err == nil) {
			t.Fatal("ambiguous group observation was accepted")
		}
	}
}

func TestDarwinGroupExit_DoesNotProbeReusedOrPreviousBootGroup(t *testing.T) {
	for _, crossBoot := range []bool{false, true} {
		id := fixtureLaunchdIdentity()
		empty, err := observeLaunchdGroupExit(context.Background(), id.Group, func(context.Context) (string, error) {
			if crossBoot {
				return "later-boot", nil
			}
			return id.BootID, nil
		}, func(context.Context, int) (darwinProcessObservation, error) {
			if crossBoot {
				t.Fatal("previous-boot PID inspected")
			}
			id.StartUsec++
			return darwinProcessObservation{identity: id, group: id.PID}, nil
		}, func(int) error { t.Fatal("unknown group probed"); return nil })
		if crossBoot {
			if err != nil || !empty {
				t.Fatal("previous-boot group was not retired", err)
			}
		} else if err == nil || empty {
			t.Fatal("reused group accepted")
		}
	}
}
