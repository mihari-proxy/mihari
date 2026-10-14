package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/core"
)

type revalidatingSetupCandidate struct {
	*fakeCandidate
	revalidate func(context.Context, []byte) error
}

func (c *revalidatingSetupCandidate) Revalidate(ctx context.Context, content []byte) error {
	return c.revalidate(ctx, content)
}

func TestSetupCore_LegacyCandidateHasAtMostTwoAdditionalValidations(t *testing.T) {
	for _, keepChanging := range []bool{false, true} {
		t.Run(fmt.Sprint(keepChanging), func(t *testing.T) {
			var manager *Manager
			validations, commits, downloads := 0, 0, 0
			change := func(ctx context.Context) error {
				_, err := manager.updateSettings(ctx, func(s *config.Settings) error {
					s.MixedAddr = fmt.Sprintf("127.0.0.1:%d", 19600+validations)
					return nil
				})
				return err
			}
			candidate := &revalidatingSetupCandidate{fakeCandidate: &fakeCandidate{version: "v1.19.0", commit: func() (core.InstallResult, error) {
				commits++
				return core.InstallResult{Version: "v1.19.0", Updated: true}, nil
			}}, revalidate: func(ctx context.Context, content []byte) error {
				if !strings.Contains(string(content), fmt.Sprint(19600+validations)) {
					t.Fatalf("candidate validation used stale config: %s", content)
				}
				validations++
				if keepChanging {
					return change(ctx)
				}
				return nil
			}}
			settings := config.Defaults()
			settings.ControllerSecret = strings.Repeat("ab", 32)
			manager = newTestManager(Options{Settings: settings, Supervisor: &fakeSupervisor{}, Installer: &fakeInstaller{prepare: func(ctx context.Context, _ core.InstallRequest) (PreparedCore, error) {
				downloads++
				return candidate, change(ctx)
			}}})
			_, err := manager.Install(t.Context(), Operation{ID: "install", ExecutionMode: protocol.ExecutionModeSetup})
			if downloads != 1 || !candidate.cleaned.Load() {
				t.Fatalf("downloads=%d cleaned=%v", downloads, candidate.cleaned.Load())
			}
			if keepChanging {
				if err == nil || validations != 2 || commits != 0 {
					t.Fatalf("err=%v validations=%d commits=%d", err, validations, commits)
				}
			} else if err != nil || validations != 1 || commits != 1 {
				t.Fatalf("err=%v validations=%d commits=%d", err, validations, commits)
			}
		})
	}
}

func TestSetupCore_WaitCancellationDoesNotDisturbOtherOwner(t *testing.T) {
	manager := newTestManager(Options{})
	reservation := &coreUpdateReservation{done: make(chan struct{})}
	manager.coreUpdate.Store(reservation)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := manager.lockSetupMutation(ctx, Operation{ExecutionMode: protocol.ExecutionModeSetup})
	if !errors.Is(err, context.Canceled) || manager.coreUpdate.Load() != reservation {
		t.Fatalf("err=%v owner=%v", err, manager.coreUpdate.Load())
	}
	manager.coreUpdate.Store(nil)
	close(reservation.done)
}
