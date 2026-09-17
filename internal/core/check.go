package core

import "context"

// LatestVersion checks release metadata without downloading or executing a core.
func (i Installer) LatestVersion(ctx context.Context, channel string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, i.checkTimeout())
	defer cancel()
	release, err := i.LatestRelease(ctx, channel)
	if err != nil {
		return "", err
	}
	asset, err := SelectAsset(release, i.targetOS(), i.targetArch(), channel)
	if err != nil {
		return "", err
	}
	if channel == "alpha" {
		return "alpha-" + ParseAlphaSHA(asset.Name), nil
	}
	return release.TagName, nil
}
