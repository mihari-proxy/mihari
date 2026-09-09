package app

import (
	"context"
	"github.com/mihari-proxy/mihari/internal/update"
)

func runInstallReplacement(ctx context.Context, preview update.ReplacementPreview, consent update.ReplacementConsent, recheck func(context.Context) error, apply func(context.Context) (InstallResult, error)) (InstallResult, error) {
	if err := update.ValidateReplacementConsent(preview, consent); err != nil {
		return InstallResult{}, err
	}
	if err := recheck(ctx); err != nil {
		return InstallResult{}, err
	}
	return apply(ctx)
}

// installReplacementGuard carries only current-call confirmation and observations.
type installReplacementGuard struct {
	preview update.ReplacementPreview
	consent update.ReplacementConsent
	recheck func(context.Context) error
}

func (g *installReplacementGuard) run(ctx context.Context, apply func(context.Context) (InstallResult, error)) (InstallResult, error) {
	if g == nil {
		return apply(ctx)
	}
	return runInstallReplacement(ctx, g.preview, g.consent, g.recheck, apply)
}
