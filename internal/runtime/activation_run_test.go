package runtime

import (
	"context"
	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/sysproxy"
	"sync/atomic"
	"testing"
)

type activationGateway struct{ calls *atomic.Int32 }

func (g activationGateway) Serve(context.Context) error { g.calls.Add(1); return nil }
func (activationGateway) SessionCount() int             { return 0 }
func (activationGateway) ListenAddr() string            { return "" }

func TestActivation_RunBlocksAllWorkers(t *testing.T) {
	for _, phase := range []string{"prepared", "definition_committed", "activation_committed", "complete"} {
		t.Run(phase, func(t *testing.T) {
			var core, scheduler, web, binary atomic.Int32
			proxy := &sysproxy.FakeBackend{}
			settings := config.Defaults()
			settings.SystemProxyDesired = true
			m := newTestManager(Options{ActivationPhase: phase, SysProxy: proxy, Settings: settings,
				Supervisor:   &fakeSupervisor{run: func(context.Context) error { core.Add(1); return nil }},
				RunScheduler: func(context.Context) error { scheduler.Add(1); return nil },
				WebGateway:   activationGateway{&web},
				BinaryExists: func() bool { binary.Add(1); return true },
			})
			err := m.Run(context.Background())
			allowed := phase == "activation_committed" || phase == "complete"
			if !allowed && (err == nil || core.Load()+scheduler.Load()+web.Load()+binary.Load() != 0 || proxy.EnableCalls+proxy.DisableCalls+proxy.GetCalls != 0) {
				t.Fatalf("pending startup escaped gate: err=%v core=%d scheduler=%d web=%d binary=%d", err, core.Load(), scheduler.Load(), web.Load(), binary.Load())
			}
			if allowed && (err != nil || core.Load() != 1 || scheduler.Load() != 1 || web.Load() != 1 || proxy.EnableCalls != 1) {
				t.Fatalf("activated workers missing: %v %d/%d/%d", err, core.Load(), scheduler.Load(), web.Load())
			}
		})
	}
}
