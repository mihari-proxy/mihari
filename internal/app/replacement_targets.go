package app

import (
	"context"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/service"
	"github.com/mihari-proxy/mihari/internal/update"
)

// ObserveReplacement discovers the executable and the actual service staging destination.
func (c *SelfUpdateServiceCompletion) ObserveReplacement(ctx context.Context, binary string) (update.ReplacementSnapshot, error) {
	view, err := c.service.ObserveReplacementService(ctx)
	if err != nil {
		return update.ReplacementSnapshot{}, err
	}
	snapshot := update.ReplacementSnapshot{ServiceDefinitionSHA256: view.DefinitionSHA256}
	target, err := update.ObserveReplacementTarget(ctx, "binary", binary, nil)
	if err != nil {
		return snapshot, err
	}
	snapshot.Targets = append(snapshot.Targets, target)
	if view.Registered {
		target, err = update.ObserveReplacementTarget(ctx, "service", view.BinaryPath, nil)
		if err != nil {
			return snapshot, err
		}
		snapshot.Targets = append(snapshot.Targets, target)
	}
	after, err := c.service.ObserveReplacementService(ctx)
	if err != nil {
		return snapshot, err
	}
	if after != view {
		return snapshot, protocol.APIError{Code: protocol.CodeInvalidState, Message: "service installation changed; start again"}
	}
	return snapshot, nil
}

func serviceReplacementChanged() error {
	return protocol.APIError{Code: protocol.CodeInvalidState, Message: "Mihari updated, but the service installation changed; start again"}
}

// AfterPreparedReplace consumes the service portion of the confirmed preview.
func (c *SelfUpdateServiceCompletion) AfterPreparedReplace(ctx context.Context, p update.PreparedUpdate) error {
	check := func(ctx context.Context) error { return c.recheckPreparedService(ctx, p) }
	if err := check(ctx); err != nil {
		return c.completeReplacement(ctx, p.Version, true, err)
	}
	installed, err := c.service.UpdateInstalledBinaryChecked(ctx, service.ServiceReplacementChecks{BeforeStop: check, BeforeStage: check})
	return c.completeReplacement(ctx, p.Version, installed, err)
}

func (c *SelfUpdateServiceCompletion) recheckPreparedService(ctx context.Context, p update.PreparedUpdate) error {
	view, err := c.service.ObserveReplacementService(ctx)
	if err != nil {
		return err
	}
	if view.DefinitionSHA256 != p.Preview.Snapshot.ServiceDefinitionSHA256 {
		return serviceReplacementChanged()
	}
	main, err := platform.ObserveReplacementFile(ctx, p.TargetPath)
	if err != nil {
		return err
	}
	if !main.Exists || main.SHA256 != p.SHA256 {
		return serviceReplacementChanged()
	}
	serviceSeen := false
	for _, target := range p.Preview.Snapshot.Targets {
		now, err := platform.ObserveReplacementFile(ctx, target.Path)
		if err != nil {
			return err
		}
		isService := false
		for _, role := range target.Roles {
			if role == "service" {
				isService = true
			}
		}
		if isService {
			serviceSeen = true
			if !view.Registered {
				return serviceReplacementChanged()
			}
			destination, err := platform.ObserveReplacementFile(ctx, view.BinaryPath)
			if err != nil {
				return err
			}
			if destination.Path != now.Path || destination.FileID != now.FileID || destination.Exists != now.Exists {
				return serviceReplacementChanged()
			}
		}
		// Only the explicitly replaced main directory entry (or its canonical alias)
		// may have our new identity and candidate contents. Other copies retain theirs.
		if target.Path == main.Path && now.FileID == main.FileID && now.SHA256 == p.SHA256 {
			continue
		}
		if now.Exists != target.Exists || now.FileID != target.FileID || now.SHA256 != target.SHA256 {
			return serviceReplacementChanged()
		}
	}
	if serviceSeen != view.Registered {
		return serviceReplacementChanged()
	}
	return nil
}
