package subscription

import "context"

type geoArtifact struct {
	url, hash string
	size      int64
}

func (a geoArtifact) matches(b []byte) bool {
	return len(b) > 0 && len(b) <= maxGeoResourceBytes && (a.size == 0 || int64(len(b)) == a.size) && providerDigest(b) == a.hash
}

// These fixed identities were approved from the retained artifact catalog.
// Country/ASN preserve the existing release lock. DAT orphan commits may become
// unavailable; failure retains old resources and never selects a mutable fallback.
func trustedGeoArtifact(kind GeoResourceKind) (geoArtifact, error) {
	switch kind {
	case GeoCountryMMDB:
		return geoArtifact{url: "https://raw.githubusercontent.com/Loyalsoldier/geoip/69986b5d098c8d723a2c4d56317bc10cd5669c02/GeoLite2-Country.mmdb", hash: "26a2c3c3791b36303a1c70bac18320c4e6bd40950286224a38f2756c0f7d0ca2"}, nil
	case GeoASNMMDB:
		return geoArtifact{url: "https://raw.githubusercontent.com/Loyalsoldier/geoip/69986b5d098c8d723a2c4d56317bc10cd5669c02/GeoLite2-ASN.mmdb", hash: "82abcabdf4d0ecb34da45e4f0f9bc30bf933cfbfec446b89a2215fae5b1fdbdc"}, nil
	case GeoIPDAT:
		return geoArtifact{url: "https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/b3a0635a5ff10e63d300a050aa38edf7f138ef2f/geoip.dat", hash: "4149e607530f91da697bad4696f8c59f0a475af38e69405e4124438c9886c721", size: 17120329}, nil
	case GeoSiteDAT:
		return geoArtifact{url: "https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/b3a0635a5ff10e63d300a050aa38edf7f138ef2f/geosite.dat", hash: "7104fc19469298564947d42c320a1d5442416f1f72648bf516314d719594338c", size: 4242906}, nil
	default:
		return geoArtifact{}, dataError("unknown Geo artifact")
	}
}

// DownloadGeo retrieves a pinned Geo artifact with a separate 128 MiB budget.
func (d *Downloader) DownloadGeo(ctx context.Context, kind GeoResourceKind, mode string) ([]byte, error) {
	a, err := trustedGeoArtifact(kind)
	if err != nil {
		return nil, err
	}
	limit := int64(maxGeoResourceBytes)
	if a.size > 0 && a.size < limit {
		limit = a.size
	}
	b, err := d.downloadManaged(ctx, ProviderSpec{URL: a.url}, mode, limit)
	if err != nil {
		return nil, err
	}
	if !a.matches(b) {
		return nil, dataError("untrusted Geo artifact")
	}
	return b, nil
}
