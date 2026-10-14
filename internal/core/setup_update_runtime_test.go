package core_test

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/core"
	"github.com/mihari-proxy/mihari/internal/onboarding"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
	"github.com/mihari-proxy/mihari/internal/supervisor"
)

type setupChangingInstaller struct {
	*core.TestTrustedFixture
	afterPrepare func(context.Context) error
}

func (i *setupChangingInstaller) Prepare(ctx context.Context, r core.InstallRequest) (core.PreparedCore, error) {
	candidate, err := i.PrepareUpdate(ctx, r)
	if err != nil {
		return nil, err
	}
	if err := i.afterPrepare(ctx); err != nil {
		candidate.Cleanup()
		return nil, err
	}
	return candidate, nil
}

func TestSetupStoppedCore_RevalidatesFreshInputsWithSelectedCandidate(t *testing.T) {
	for _, reject := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "rejected"}[reject], func(t *testing.T) {
			service, err := onboarding.Open(onboarding.Options{StatePath: filepath.Join(t.TempDir(), "onboarding.json"), InitialSetupRequired: true})
			if err != nil {
				t.Fatal(err)
			}
			installer := &setupChangingInstaller{}
			starter := &capabilityStarter{seamStarter: &seamStarter{children: make(chan *seamChild, 8)}}
			sup := supervisor.New(supervisor.Options{Starter: starter})
			manager, f, _, _ := seamManager(t, func(o *runtimeapi.Options) { o.Supervisor = sup; o.Installer = installer; o.Onboarding = service })
			installer.TestTrustedFixture = f
			starter.fixture = f
			changed, validations := false, 0
			var installedValidationBinary string
			f.Execute = func(_ context.Context, c core.CoreCommand) ([]byte, error) {
				if !changed && len(c.Args) > 0 && c.Args[0] == "-t" && installedValidationBinary == "" {
					installedValidationBinary = c.Binary
				}
				if changed && len(c.Args) > 0 && c.Args[0] == "-t" {
					if installedValidationBinary == "" || c.Binary == installedValidationBinary {
						t.Fatalf("fresh validation did not use selected candidate: installed=%q validation=%q", installedValidationBinary, c.Binary)
					}
					validations++
					if !strings.Contains(string(f.CommandConfig(c)), "19700") {
						t.Fatalf("revalidated stale committed config: %s", f.CommandConfig(c))
					}
					if reject {
						return nil, errors.New("synthetic new candidate rejection")
					}
				}
				return []byte("Mihomo v1.19.30"), nil
			}
			installer.afterPrepare = func(ctx context.Context) error {
				port := "127.0.0.1:19700"
				_, err := manager.UpdateOnboarding(ctx, runtimeapi.Operation{ID: "other-client"}, onboarding.Update{MixedAddr: &port})
				changed = true
				return err
			}
			old := f.Binary()
			_, err = manager.Install(t.Context(), runtimeapi.Operation{ID: "setup", ExecutionMode: protocol.ExecutionModeSetup})
			if validations != 1 || starter.starts.Load() != 0 {
				t.Fatalf("validations=%d starts=%d err=%v", validations, starter.starts.Load(), err)
			}
			if reject {
				if err == nil || !bytes.Equal(old, f.Binary()) {
					t.Fatalf("invalid candidate published: %v", err)
				}
			} else if err != nil || bytes.Equal(old, f.Binary()) {
				t.Fatalf("target not installed: %v", err)
			}
		})
	}
}
