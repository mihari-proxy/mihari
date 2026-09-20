package core

import "context"

// LatestVersion checks release metadata without downloading or executing a core.
func (i Installer) LatestVersion(ctx context.Context, channel string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, i.checkTimeout())
	defer cancel()
	target, err := i.ResolveTarget(ctx, channel)
	if err != nil {
		return "", err
	}
	if channel == "alpha" {
		return "alpha-" + ParseAlphaSHA(target.asset.Name), nil
	}
	return target.tag, nil
}
