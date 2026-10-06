//go:build linux || darwin

package app

import (
	"context"
	"errors"
	"net/http"
	"runtime"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/panel/archive"
	"github.com/mihari-proxy/mihari/internal/update"
)

type nativeReleaseInputs struct {
	offlineBinary bool
	binary, core  []byte
	resources     map[string][]byte
	trust         migrationTrust
	source        migrationCapability
}

func (i *nativeReleaseInputs) Close() error {
	if i.source != nil {
		return i.source.Close()
	}
	return nil
}
func prepareNativeReleaseInputs(ctx context.Context, req InstallRequest, sourcePath, offlineRoot string, client *http.Client) (_ *nativeReleaseInputs, err error) {
	inputs := &nativeReleaseInputs{resources: map[string][]byte{}}
	defer func() {
		if err != nil {
			err = errors.Join(err, inputs.Close())
		}
	}()
	inputs.trust, err = loadUnixOfflineTrust(ctx, offlineRoot)
	if err != nil {
		return nil, err
	}
	inputs.binary, err = readHostFile(req.Binary, migrationBinaryMax)
	if err != nil {
		return nil, err
	}
	hash := sha256HexBytes(inputs.binary)
	official := update.OfficialReleaseSource{Client: client}
	inputs.offlineBinary = inputs.trust.acceptsBinary(hash)
	// A request that carries a bundle must not ask GitHub for a digest. The
	// candidate is authorized only when it matches mihari inside an archive
	// that install-trust or the fixed channel index already accepted.
	if req.Bundle == "" && !inputs.offlineBinary {
		want, err := official.Checksum(ctx, req.ReleaseTag, "mihari-"+runtime.GOOS+"-"+runtime.GOARCH)
		if err != nil {
			return nil, err
		}
		if hash != want {
			return nil, migrateData("install binary checksum mismatch")
		}
		inputs.trust.binary[hash] = struct{}{}
	}
	if req.ArtifactSHA256 != "" && req.ArtifactSHA256 != hash {
		return nil, migrateData("install binary checksum mismatch")
	}
	if sourcePath != "" {
		inputs.source, err = openReadOnlyMigrationRoot(ctx, sourcePath)
		if err != nil {
			return nil, err
		}

	}
	if req.Bundle != "" {
		raw, err := readHostFile(req.Bundle, migrationBundleComp)
		if err != nil {
			return nil, err
		}
		bundleHash := sha256HexBytes(raw)
		// A binaries pin or a bundle pin is already offline authority. Only an
		// unpinned bundle consults the fixed channel index. The mihari member
		// must still match the candidate before this archive is trusted.
		if !inputs.trust.acceptsBundle(bundleHash) && !inputs.offlineBinary {
			latest, sum, err := fetchChannelIndex(ctx, client, req.Channel, runtime.GOOS, runtime.GOARCH)
			if err != nil {
				return nil, err
			}
			if latest != req.ReleaseTag {
				return nil, migrateData("channel index latest does not match release tag")
			}
			if sum != bundleHash {
				return nil, migrateData("install bundle checksum mismatch")
			}
		}
		if req.BundleSHA256 != "" && req.BundleSHA256 != bundleHash {
			return nil, migrateData("install bundle checksum mismatch")
		}
		matchedBundleBinary := false
		err = archive.ExtractTarGzipBytes(raw, archive.Limits{MaxFile: migrationBinaryMax, MaxTotal: migrationBundleExpand, MaxEntries: migrationBundleFiles, MaxDepth: migrationMaxDepth}, nil, func(name string, body []byte) error {
			switch name {
			case "mihari":
				if sha256HexBytes(body) != hash {
					return migrateData("bundle binary does not match verified candidate")
				}
				matchedBundleBinary = true
			case "install-aio.sh": // Inert installer text is never executed by apply.
			case "data/bin/mihomo":
				inputs.resources["bin/mihomo"] = body
			case "data/bin/core-channel":
				if _, _, ok := config.ParseCoreChannelSidecar(body); !ok {
					return migrateState("unsupported bundled core channel")
				}
				inputs.resources["bin/core-channel"] = body
			case "data/geoip/GeoLite2-Country.mmdb", "data/geoip/GeoLite2-ASN.mmdb":
				inputs.resources[name[len("data/"):]] = body
				inputs.trust.geo[sha256HexBytes(body)] = struct{}{}
			default:
				return migrateData("unsupported install bundle entry")
			}
			return ctx.Err()
		})
		if err != nil {
			return nil, err
		}
		if !matchedBundleBinary {
			return nil, migrateData("bundle binary does not match verified candidate")
		}
		if !inputs.trust.acceptsBundle(bundleHash) {
			inputs.trust.bundle[bundleHash] = struct{}{}
		}
		inputs.trust.binary[hash] = struct{}{}
	}
	// The enclosing bundle has already been independently verified. Its core
	// is an offline input, not a request to fetch the compiled legacy version.
	inputs.core = inputs.resources["bin/mihomo"]

	return inputs, nil
}
