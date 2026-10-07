package core

import "testing"

func TestSelectAssetRejectsAmbiguousOrWrongTarget(t *testing.T) {

	for _, tt := range []struct {
		name, channel, goos, arch, tag string
		assets                         []string
	}{
		{"duplicate stable", "stable", "linux", "amd64", "v1.20.0", []string{"mihomo-linux-amd64-compatible-v1.20.0.gz", "mihomo-linux-amd64-compatible-v1.20.0.gz"}},
		{"duplicate alpha", "alpha", "linux", "amd64", "Prerelease-Alpha", []string{"mihomo-linux-amd64-alpha-abcdef1.gz", "mihomo-linux-amd64-alpha-abcdef2.gz"}},
		{"architecture suffix", "stable", "linux", "amd64", "v1.20.0", []string{"mihomo-linux-amd64evil-v1.20.0.gz"}},
		{"wrong version", "stable", "linux", "amd64", "v1.20.0", []string{"mihomo-linux-amd64-compatible-v1.19.30.gz"}},
		{"unsupported OS", "stable", "freebsd", "amd64", "v1.20.0", []string{"mihomo-freebsd-amd64-v1.20.0.gz"}},
		{"unsupported arch", "stable", "linux", "386", "v1.20.0", []string{"mihomo-linux-386-v1.20.0.gz"}},
		{"unknown channel", "nightly", "linux", "amd64", "v1.20.0", []string{"mihomo-linux-amd64-compatible-v1.20.0.gz"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			release := Release{TagName: tt.tag}
			for _, name := range tt.assets {
				release.Assets = append(release.Assets, Asset{Name: name})
			}
			if asset, err := SelectAsset(release, tt.goos, tt.arch, tt.channel); err == nil {
				t.Fatalf("unsafe or ambiguous selection accepted: %+v", asset)
			}
		})
	}
}

func TestSelectAssetStablePrefersCompatibleCPU(t *testing.T) {
	release := Release{TagName: "v1.20.0", Assets: []Asset{
		{Name: "mihomo-linux-amd64-v3-v1.20.0.gz"},
		{Name: "mihomo-linux-amd64-v1.20.0.gz"},
		{Name: "mihomo-linux-amd64-compatible-v1.20.0.gz"},
	}}
	asset, err := SelectAsset(release, "linux", "amd64", "stable")
	if err != nil || asset.Name != release.Assets[2].Name {
		t.Fatalf("asset=%+v err=%v", asset, err)
	}
}

func TestSelectAssetStableAllowsExactGoVariantFallback(t *testing.T) {
	release := Release{TagName: "v1.20.0", Assets: []Asset{{Name: "mihomo-linux-arm64-go120-v1.20.0.gz"}}}
	asset, err := SelectAsset(release, "linux", "arm64", "stable")
	if err != nil || asset.Name != release.Assets[0].Name {
		t.Fatalf("asset=%+v err=%v", asset, err)
	}
}
