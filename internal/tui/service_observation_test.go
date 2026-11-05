package tui

import (
	"context"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/observation"
	"github.com/mihari-proxy/mihari/internal/service"
	systempage "github.com/mihari-proxy/mihari/internal/tui/pages/system"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
	"sync/atomic"
	"testing"
)

type observationServiceFake struct{ calls atomic.Int32 }

func (*observationServiceFake) Install() error   { return nil }
func (*observationServiceFake) Uninstall() error { return nil }
func (*observationServiceFake) Reinstall() error { return nil }
func (*observationServiceFake) Start() error     { return nil }
func (*observationServiceFake) Stop() error      { return nil }
func (*observationServiceFake) Restart() error   { return nil }
func (c *observationServiceFake) Status() (service.StatusKind, error) {
	c.calls.Add(1)
	return service.StatusRunning, nil
}
func TestServiceObservation_SharedByRootAndSystem(t *testing.T) {
	backend := &observationServiceFake{}
	m := NewModel()
	m.SetServiceController(backend)
	defer m.closeServiceObservations()
	m.applyPreferences(protocol.TUIPreferences{Performance: &protocol.PerformancePreferences{ObservationReuseSeconds: 3}})
	m.loadRootServiceStatus()()
	m.pages[ui.PageSystem].(*systempage.Model).LoadStatus()()
	if backend.calls.Load() != 1 {
		t.Fatal("root/System did not share service observation")
	}
}
func TestServiceObservation_RefreshAndMutationInvalidate(t *testing.T) {
	backend := &observationServiceFake{}
	c := &observedServiceController{ServiceController: backend}
	defer c.Close()
	c.setPolicy(protocol.PerformancePreferences{ObservationReuseSeconds: 3})
	for range 2 {
		if _, err := c.Status(); err != nil {
			t.Fatal(err)
		}
	}
	if backend.calls.Load() != 1 {
		t.Fatal("service status repeated")
	}
	if _, err := c.StatusContext(observation.WithFresh(context.Background())); err != nil {
		t.Fatal(err)
	}
	if backend.calls.Load() != 2 {
		t.Fatal("service explicit refresh repeated cache")
	}
	if err := c.Restart(); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Status(); err != nil {
		t.Fatal(err)
	}
	if backend.calls.Load() != 3 {
		t.Fatal("service mutation did not invalidate")
	}
}
