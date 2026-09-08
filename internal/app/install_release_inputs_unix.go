//go:build linux || darwin

package app

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"

	"github.com/mihari-proxy/mihari/internal/core"
	"github.com/mihari-proxy/mihari/internal/panel/archive"
	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/update"
)

type nativeReleaseInputs struct {
	binary, core, receipt []byte
	resources             map[string][]byte
	trust                 migrationTrust
	source                migrationCapability
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
	if !inputs.trust.acceptsBinary(hash) {
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
	needsCore := false
	if sourcePath != "" {
		inputs.source, err = openReadOnlyMigrationRoot(ctx, sourcePath)
		if err != nil {
			return nil, err
		}
		_, err = inputs.source.Stat(ctx, "bin/mihomo")
		if err == nil {
			needsCore = true
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	if req.Bundle != "" {
		raw, err := readHostFile(req.Bundle, migrationBundleComp)
		if err != nil {
			return nil, err
		}
		bundleHash := sha256HexBytes(raw)
		if !inputs.trust.acceptsBundle(bundleHash) {
			want, err := official.Checksum(ctx, req.ReleaseTag, "mihari-all-in-one-"+runtime.GOOS+"-"+runtime.GOARCH+".tar.gz")
			if err != nil {
				return nil, err
			}
			if want != bundleHash {
				return nil, migrateData("install bundle checksum mismatch")
			}
			inputs.trust.bundle[bundleHash] = struct{}{}
		}
		if req.BundleSHA256 != "" && req.BundleSHA256 != bundleHash {
			return nil, migrateData("install bundle checksum mismatch")
		}
		err = archive.ExtractTarGzipBytes(raw, archive.Limits{MaxFile: migrationBinaryMax, MaxTotal: migrationBundleExpand, MaxEntries: migrationBundleFiles, MaxDepth: migrationMaxDepth}, nil, func(name string, body []byte) error {
			switch name {
			case "mihari":
				if sha256HexBytes(body) != hash {
					return migrateData("bundle binary does not match verified candidate")
				}
			case "install-aio.sh": // Inert installer text is never executed by apply.
			case "data/bin/mihomo":
				inputs.resources["bin/mihomo"] = body
				needsCore = true
			case "data/bin/core-channel":
				if string(body) != "stable" && string(body) != "stable\n" {
					return migrateState("unsupported bundled core channel")
				}
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
	}
	if needsCore {
		digest, err := core.CompiledAssetDigest(ctx, runtime.GOOS, runtime.GOARCH, "v1.19.30", "stable")
		if err != nil {
			return nil, err
		}
		raw, err := readUnixOfflineArtifact(ctx, offlineRoot, digest+".gz", 128<<20)
		if err == nil {
			inputs.core, inputs.receipt, err = core.RebuildMigrationCore(ctx, runtime.GOOS, runtime.GOARCH, "v1.19.30", raw)
		} else if errors.Is(err, os.ErrNotExist) {
			inputs.core, inputs.receipt, err = core.DownloadMigrationCore(ctx, client, runtime.GOOS, runtime.GOARCH, "v1.19.30")
		}
		if err != nil {
			return nil, err
		}
		coreHash := sha256HexBytes(inputs.core)
		inputs.trust.core[coreHash] = struct{}{}
		if bundled, ok := inputs.resources["bin/mihomo"]; ok && sha256HexBytes(bundled) != coreHash {
			return nil, migrateState("unsupported bundled core")
		}
	}
	return inputs, nil
}
func readUnixOfflineArtifact(ctx context.Context, rootPath, name string, limit int64) (raw []byte, err error) {
	if filepath.Base(name) != name {
		return nil, os.ErrInvalid
	}
	root, err := platform.OpenTrustedParent(ctx, rootPath, 0)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	file, _, err := root.OpenFile(ctx, name, 0644)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	return readInstallFile(ctx, file, limit)
}
