package core_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/mihomo"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
)

type timeoutTrustedController struct {
	*seamController
	mode, selected string
}

func (c *timeoutTrustedController) Configs(context.Context) (map[string]any, error) {
	return map[string]any{"mode": c.mode}, nil
}
func (c *timeoutTrustedController) PatchConfigs(_ context.Context, patch map[string]any) error {
	c.mode, _ = patch["mode"].(string)
	return nil
}
func (c *timeoutTrustedController) Proxies(context.Context) (mihomo.Proxies, error) {
	return mihomo.Proxies{Proxies: map[string]mihomo.Proxy{"GLOBAL": {Name: "GLOBAL", Type: "Selector", Now: c.selected, All: []string{"DIRECT", "Node B"}}}}, nil
}
func (c *timeoutTrustedController) SelectProxy(_ context.Context, _, name string) error {
	c.selected = name
	return nil
}

func TestSubscriptionTimeout_TrustedCancellationRestoresPreviousState(t *testing.T) {
	for _, routing := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain", true: "routing"}[routing], func(t *testing.T) {
			var controller *timeoutTrustedController
			m, fixture, c, service := seamManager(t, func(o *runtimeapi.Options) {
				controller = &timeoutTrustedController{seamController: o.Controller.(*seamController), mode: "global", selected: "Node B"}
				o.Controller = controller
				if routing {
					o.Settings.SetRoutingMode("global")
				}
			})
			profile, err := service.Add("fixture", "http://fixture.invalid/sub", "")
			if err != nil {
				t.Fatal(err)
			}
			before := fixture.Content()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			c.reload = func(recovery context.Context) error {
				calls++
				if calls == 1 {
					cancel()
					return ctx.Err()
				}
				if recovery.Err() != nil {
					t.Error("trusted recovery inherited cancellation")
					return recovery.Err()
				}
				deadline, ok := recovery.Deadline()
				budget := 10 * time.Second
				if calls == 3 {
					budget = 15 * time.Second
				}
				if !ok || time.Until(deadline) > budget || time.Until(deadline) < budget-time.Second {
					t.Error("trusted recovery missing bounded deadline")
				}
				return nil
			}
			_, err = m.RefreshSubscription(ctx, runtimeapi.Operation{ID: "cancel-refresh"}, profile.ID)
			var api protocol.APIError
			if !errors.Is(err, context.Canceled) || !errors.As(err, &api) || api.Details["degraded"] == true {
				t.Fatalf("trusted compensation failed: %v", err)
			}
			want := 2
			if routing {
				want = 3
			}
			if calls != want || !bytes.Equal(before, fixture.Content()) || service.Snapshot().Profiles[0].Generation != 0 {
				t.Fatalf("trusted rollback mismatch, reloads=%d", calls)
			}
			if routing && (controller.mode != "global" || controller.selected != "Node B") {
				t.Fatal("trusted routing not restored")
			}
		})
	}
}
