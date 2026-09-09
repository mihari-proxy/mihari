package core_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/core"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
	"github.com/mihari-proxy/mihari/internal/subscription"
)

type tunFaultController struct {
	*seamController
	cancel             context.CancelFunc
	rejectConfirmation bool
}

func (c *tunFaultController) Reload(ctx context.Context, path string, force bool) error {
	if err := c.seamController.Reload(ctx, path, force); err != nil {
		return err
	}
	// The real controller exposes a disabled live TUN even when YAML omits it.
	if c.tun == nil {
		c.tun = map[string]any{"enable": false}
	}
	if c.reloads == 1 && c.cancel != nil {
		c.cancel()
	}
	return nil
}
func (c *tunFaultController) Configs(ctx context.Context) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c.reloads == 1 && c.rejectConfirmation {
		return nil, errors.New("synthetic confirmation failure")
	}
	return c.seamController.Configs(ctx)
}
func TestRootManager_TunFailureCompensatesPersistentSettingsAndConfig(t *testing.T) {
	for _, failure := range []string{"validation", "save", "reload", "confirmation", "cancellation", "rollback-settings"} {
		t.Run(failure, func(t *testing.T) {
			settingsPath := filepath.Join(t.TempDir(), "mihari.yaml")
			var controller *tunFaultController
			saves := 0
			m, f, _, _ := seamManager(t, func(o *runtimeapi.Options) {
				if err := config.Save(settingsPath, o.Settings); err != nil {
					t.Fatal(err)
				}
				o.SettingsPath = settingsPath
				o.SaveSettings = func(path string, s config.Settings) (config.CommitResult, error) {
					saves++
					if failure == "save" || failure == "rollback-settings" && saves == 2 {
						return config.CommitResult{}, errors.New("synthetic persistence failure")
					}
					return config.SaveWithCommit(path, s)
				}
				controller = &tunFaultController{seamController: o.Controller.(*seamController)}
				o.Controller = controller
			})
			before := f.Content()
			settingsBefore, err := os.ReadFile(settingsPath)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch failure {
			case "validation":
				f.Execute = func(context.Context, core.CoreCommand) ([]byte, error) { return nil, errors.New("synthetic rejection") }
			case "reload":
				controller.failReloads = 1
			case "confirmation", "rollback-settings":
				controller.rejectConfirmation = true
			case "cancellation":
				controller.cancel = cancel
			}
			_, err = m.EnableTun(ctx, runtimeapi.Operation{ID: "tun-" + failure, Source: "test"}, true)
			if err == nil {
				t.Fatal("failure accepted")
			}
			after, readErr := os.ReadFile(settingsPath)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if !bytes.Equal(before, f.Content()) || controller.patches != 0 {
				t.Fatal("failed TUN changed config or used PATCH")
			}
			if failure == "rollback-settings" {
				if bytes.Equal(after, settingsBefore) {
					t.Fatal("fixture failed to retain uncompensated persistence")
				}
				_, err = m.DisableTun(context.Background(), runtimeapi.Operation{ID: "refused", Source: "test"})
				assertCode(t, err, protocol.CodeInvalidState)
			} else if !bytes.Equal(after, settingsBefore) {
				t.Fatal("persistent settings not compensated")
			}
			if failure == "validation" && saves != 0 {
				t.Fatal("validation failure wrote settings")
			}
			if failure == "save" && controller.reloads != 0 {
				t.Fatal("save failure published configuration")
			}
		})
	}
}

func TestRootManager_DisablingLastSubscriptionDoesNotInjectLegacyTunFields(t *testing.T) {
	var settings config.Settings
	m, f, _, service := seamManager(t, func(o *runtimeapi.Options) {
		o.Settings.Tun = map[string]any{"enable": false, "stack": "legacy", "device": "legacy-device", "x-extra": true}
		settings = o.Settings.Clone()
	})
	id := cachedProfile(t, service)
	if _, err := m.UseSubscription(context.Background(), runtimeapi.Operation{ID: "use-before-disable", Source: "test"}, id); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetSubscriptionEnabled(context.Background(), runtimeapi.Operation{ID: "disable-last", Source: "test"}, id, false); err != nil {
		t.Fatal(err)
	}
	document, err := subscription.ParseDocument(f.Content())
	if err != nil {
		t.Fatal(err)
	}
	tun, ok := document["tun"].(subscription.Document)
	if !ok || len(tun) != 1 || tun["enable"] != false {
		t.Fatalf("bootstrap injected legacy TUN fields: %#v", tun)
	}
	if settings.Tun["stack"] != "legacy" || settings.Tun["device"] != "legacy-device" {
		t.Fatal("settings input changed")
	}
}

func TestRootManager_DisableFailureRestoresEnabledTunWithoutDegrading(t *testing.T) {
	var controller *tunFaultController
	var settings config.Settings
	m, f, _, _ := seamManager(t, func(o *runtimeapi.Options) {
		o.Settings.Tun = map[string]any{"enable": true}
		settings = o.Settings.Clone()
		controller = &tunFaultController{seamController: o.Controller.(*seamController), rejectConfirmation: true}
		controller.tun = map[string]any{"enable": true, "stack": "gvisor", "x-extra": map[string]any{"preserved": true}}
		o.Controller = controller
	})
	previous, err := subscription.Generate(subscription.Document{"proxies": []any{}, "tun": controller.tun}, nil, settings)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Trusted.InitializeConfig(context.Background(), previous); err != nil {
		t.Fatal(err)
	}
	_, err = m.DisableTun(context.Background(), runtimeapi.Operation{ID: "disable-confirmation-failed", Source: "test"})
	assertCode(t, err, protocol.CodeUpstreamFailure)
	status, err := m.TunStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !status.DesiredEnable || status.LiveEnable == nil || !*status.LiveEnable || !bytes.Equal(previous, f.Content()) || controller.patches != 0 {
		t.Fatal("enabled TUN was not restored")
	}
	controller.rejectConfirmation = false
	if _, err = m.DisableTun(context.Background(), runtimeapi.Operation{ID: "disable-after-recovery", Source: "test"}); err != nil {
		t.Fatal("confirmed rollback incorrectly refused subsequent mutation", err)
	}
}
