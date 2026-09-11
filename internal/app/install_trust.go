package app

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"strings"
)

//go:embed install_trust.json
var compiledInstallTrustJSON []byte

type compiledInstallTrustFile struct {
	Core     []string          `json:"core,omitempty"`
	Geo      []string          `json:"geo,omitempty"`
	Binaries []string          `json:"binaries,omitempty"`
	Bundles  []string          `json:"bundles,omitempty"`
	Panels   map[string]string `json:"panels,omitempty"`
}

func compiledInstallerTrust() migrationTrust {
	trust := migrationTrust{
		core:   map[string]struct{}{},
		geo:    map[string]struct{}{},
		panel:  map[string][]byte{},
		binary: map[string]struct{}{},
		bundle: map[string]struct{}{},
	}
	var file compiledInstallTrustFile
	if err := json.Unmarshal(compiledInstallTrustJSON, &file); err != nil {
		return trust
	}
	for _, hash := range file.Core {
		trust.core[hash] = struct{}{}
	}
	for _, hash := range file.Geo {
		trust.geo[hash] = struct{}{}
	}
	for _, hash := range file.Binaries {
		trust.binary[hash] = struct{}{}
	}
	for _, hash := range file.Bundles {
		trust.bundle[hash] = struct{}{}
	}
	// Hash strings are not archive bytes. Panels are populated only after
	// retrieving and verifying their archive through a trusted authority.

	return trust
}

// decodeOfflineTrust consumes bytes only after the native caller has established
// root ownership and no-follow authority for the immutable offline directory.
func decodeOfflineTrust(ctx context.Context, raw []byte, read func(context.Context, string, int64) ([]byte, error)) (migrationTrust, error) {
	trust := compiledInstallerTrust()
	var file compiledInstallTrustFile
	if _, err := decodeStrictJSON(bytes.NewReader(raw), MaxInstallJournalBytes, &file); err != nil {
		return trust, migrateData("invalid offline install authority")
	}
	for _, group := range []struct {
		hashes []string
		target map[string]struct{}
	}{
		{file.Core, trust.core}, {file.Geo, trust.geo}, {file.Binaries, trust.binary}, {file.Bundles, trust.bundle},
	} {
		for _, hash := range group.hashes {
			if !validSHA256(hash) {
				return trust, migrateData("invalid offline install authority")
			}
			group.target[hash] = struct{}{}
		}
	}
	for key, hash := range file.Panels {
		parts := strings.Split(key, "/")
		if len(parts) != 2 || (parts[0] != "zashboard" && parts[0] != "metacubexd") || parts[1] == "" || parts[1] == "." || parts[1] == ".." || strings.ContainsAny(key, "\\\x00") || !validSHA256(hash) || read == nil {
			return trust, migrateData("invalid offline panel authority")
		}
		archive, err := read(ctx, hash+".zip", migrationBusinessMax)
		if err != nil {
			return trust, err
		}
		if sha256HexBytes(archive) != hash {
			return trust, migrateData("offline panel archive checksum mismatch")
		}
		trust.panel[key] = archive
	}
	return trust, nil
}

func mergeTrust(base, extra migrationTrust) migrationTrust {
	out := compiledInstallerTrust()
	for _, src := range []migrationTrust{base, extra} {
		for hash := range src.core {
			if out.core == nil {
				out.core = map[string]struct{}{}
			}
			out.core[hash] = struct{}{}
		}
		for hash := range src.geo {
			if out.geo == nil {
				out.geo = map[string]struct{}{}
			}
			out.geo[hash] = struct{}{}
		}
		for hash := range src.binary {
			if out.binary == nil {
				out.binary = map[string]struct{}{}
			}
			out.binary[hash] = struct{}{}
		}
		for hash := range src.bundle {
			if out.bundle == nil {
				out.bundle = map[string]struct{}{}
			}
			out.bundle[hash] = struct{}{}
		}
		for key, raw := range src.panel {
			if out.panel == nil {
				out.panel = map[string][]byte{}
			}
			out.panel[key] = raw
		}
	}
	return out
}
