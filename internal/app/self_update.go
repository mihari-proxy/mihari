package app

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

const (
	selfUpdateServiceTimeout = 15 * time.Second
	selfUpdatePollInterval   = 100 * time.Millisecond
)

// InstalledServiceUpdater synchronizes and restarts an installed Mihari OS
// service. The boolean result reports whether a service installation exists.
type InstalledServiceUpdater interface {
	UpdateInstalledBinary() (bool, error)
}

// DaemonVersionClient reads the daemon version through the local control API.
type DaemonVersionClient interface {
	Status(context.Context) (protocol.Status, error)
}

// SelfUpdateServiceCompletion coordinates the post-replacement service update
// and verifies that the restarted daemon reports the new Mihari version.
type SelfUpdateServiceCompletion struct {
	service InstalledServiceUpdater
	client  DaemonVersionClient
	timeout time.Duration
	wait    func(context.Context) error
}

// NewSelfUpdateServiceCompletion returns a post-replacement service coordinator.
func NewSelfUpdateServiceCompletion(service InstalledServiceUpdater, client DaemonVersionClient) *SelfUpdateServiceCompletion {
	return &SelfUpdateServiceCompletion{
		service: service,
		client:  client,
		timeout: selfUpdateServiceTimeout,
		wait: func(ctx context.Context) error {
			timer := time.NewTimer(selfUpdatePollInterval)
			defer timer.Stop()
			select {
			case <-timer.C:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	}
}

// AfterReplace updates an installed service and waits until its daemon reports
// version. A missing service is a successful no-op.
func (c *SelfUpdateServiceCompletion) AfterReplace(ctx context.Context, version string) error {
	installed, err := c.service.UpdateInstalledBinary()
	if err != nil {
		var apiError protocol.APIError
		if errors.As(err, &apiError) {
			return apiError
		}
		return protocol.APIError{
			Code:    protocol.CodeInvalidState,
			Message: "Mihari updated, but the installed service could not be synchronized",
		}
	}
	if !installed {
		return nil
	}

	verifyCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	for {
		status, statusErr := c.client.Status(verifyCtx)
		if statusErr == nil && sameVersion(status.DaemonVersion, version) {
			return nil
		}
		if err := c.wait(verifyCtx); err != nil {
			return protocol.APIError{
				Code:    protocol.CodeInvalidState,
				Message: "Mihari updated and synchronized the installed service, but the daemon did not report version " + version,
			}
		}
	}
}

func sameVersion(left, right string) bool {
	normalize := func(value string) string {
		return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(value)), "v")
	}
	return normalize(left) == normalize(right)
}
