package tui

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/observation"
	"github.com/mihari-proxy/mihari/internal/service"
	systempage "github.com/mihari-proxy/mihari/internal/tui/pages/system"
)

type observedServiceController struct {
	generation atomic.Uint64
	systempage.ServiceController
	policy atomic.Pointer[protocol.PerformancePreferences]
	cache  observation.Cache[protocol.PerformancePreferences, service.StatusKind]
}

func (c *observedServiceController) setPolicy(value protocol.PerformancePreferences) {
	if old := c.policy.Load(); old != nil && *old == value {
		return
	}
	c.generation.Add(1)
	c.policy.Store(&value)
	c.cache.Invalidate()
}
func (c *observedServiceController) Status() (service.StatusKind, error) {
	return c.StatusContext(context.Background())
}
func (c *observedServiceController) StatusContext(ctx context.Context) (service.StatusKind, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for {
		policy := protocol.PerformancePreferences{}
		if p := c.policy.Load(); p != nil {
			policy = *p
		}
		value, err := c.cache.Get(ctx, policy, time.Duration(policy.ObservationReuseSeconds)*time.Second, func(worker context.Context) (service.StatusKind, error) {
			if backend, ok := c.ServiceController.(interface {
				StatusContext(context.Context) (service.StatusKind, error)
			}); ok {
				return backend.StatusContext(worker)
			}
			return c.ServiceController.Status()
		})
		if errors.Is(err, observation.ErrInvalidated) {
			continue
		}
		if p := c.policy.Load(); p != nil && *p != policy {
			continue
		}
		return value, err
	}
}
func (c *observedServiceController) mutate(action func() error) error {
	c.generation.Add(1)
	defer c.generation.Add(1)
	c.cache.Invalidate()
	defer c.cache.Invalidate()
	return action()
}
func (c *observedServiceController) Install() error   { return c.mutate(c.ServiceController.Install) }
func (c *observedServiceController) Uninstall() error { return c.mutate(c.ServiceController.Uninstall) }
func (c *observedServiceController) Reinstall() error { return c.mutate(c.ServiceController.Reinstall) }
func (c *observedServiceController) Start() error     { return c.mutate(c.ServiceController.Start) }
func (c *observedServiceController) Stop() error      { return c.mutate(c.ServiceController.Stop) }
func (c *observedServiceController) Restart() error   { return c.mutate(c.ServiceController.Restart) }
func (c *observedServiceController) Close()           { c.cache.Close() }

func readServiceStatus(ctx context.Context, ctrl systempage.ServiceController) (service.StatusKind, error) {
	if backend, ok := ctrl.(interface {
		StatusContext(context.Context) (service.StatusKind, error)
	}); ok {
		return backend.StatusContext(ctx)
	}
	return ctrl.Status()
}
func (model *Model) closeServiceObservations() {
	if ctrl, ok := model.serviceCtrl.(*observedServiceController); ok {
		ctrl.Close()
	}
}

// ObservationGeneration retires queued status messages after local changes.
func (c *observedServiceController) ObservationGeneration() uint64 { return c.generation.Load() }
