package releaseinputs

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDecodeAcceptsValidLock(t *testing.T) {
	lock, err := Decode(strings.NewReader(validLockJSON))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if lock.Schema != SchemaV1 {
		t.Fatalf("schema = %q, want %q", lock.Schema, SchemaV1)
	}
	if got := lock.Mihomo.Assets["windows/arm64"].AssetID; got != 516687082 {
		t.Fatalf("windows/arm64 asset ID = %d, want 516687082", got)
	}
	if got := lock.GeoIP.Commit; got != "69986b5d098c8d723a2c4d56317bc10cd5669c02" {
		t.Fatalf("GeoIP commit = %q", got)
	}
}

func TestLoadReadsAndValidatesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "release-inputs.lock.json")
	if err := os.WriteFile(path, []byte(validLockJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	lock, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if lock.Mihomo.Tag != "v1.19.30" {
		t.Fatalf("mihomo tag = %q, want v1.19.30", lock.Mihomo.Tag)
	}

	if _, err := Load(filepath.Join(t.TempDir(), "missing.json")); err == nil || !strings.Contains(err.Error(), "open") {
		t.Fatalf("Load missing file error = %v, want open context", err)
	}
}

func TestCheckedInLockIsValidCanonicalJSON(t *testing.T) {
	path := filepath.Join("..", "..", "release-inputs.lock.json")
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read checked-in lock: %v", err)
	}
	lock, err := Load(path)
	if err != nil {
		t.Fatalf("Load checked-in lock: %v", err)
	}
	got, err := Encode(lock)
	if err != nil {
		t.Fatalf("Encode checked-in lock: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("checked-in release input lock is not canonical JSON")
	}
}

func TestDecodeRejectsOversizedLock(t *testing.T) {
	_, err := Decode(strings.NewReader(strings.Repeat(" ", MaxLockSize+1)))
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("Decode oversized lock error = %v, want size-limit error", err)
	}
}

func TestDecodeRejectsInvalidUTF8(t *testing.T) {
	_, err := Decode(bytes.NewReader([]byte{0xff, 0xfe}))
	if err == nil || !strings.Contains(err.Error(), "UTF-8") {
		t.Fatalf("Decode invalid UTF-8 error = %v, want UTF-8 error", err)
	}
}

func TestDecodeRejectsUnknownAndTrailingJSON(t *testing.T) {
	tests := []struct {
		name string
		data string
		want string
	}{
		{name: "unknown field", data: strings.Replace(validLockJSON, `"schema":`, `"unexpected": true, "schema":`, 1), want: "unknown field"},
		{name: "trailing object", data: validLockJSON + `{}`, want: "trailing JSON"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Decode(strings.NewReader(test.data))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Decode error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestDecodeRejectsDuplicateJSONKeys(t *testing.T) {
	tests := []struct {
		name string
		data string
		want string
	}{
		{
			name: "top-level field",
			data: strings.Replace(validLockJSON, `"schema": "mihari-aio-input-lock/v1",`, `"schema": "mihari-aio-input-lock/v1", "schema": "mihari-aio-input-lock/v1",`, 1),
			want: "duplicate JSON object key",
		},
		{
			name: "nested field",
			data: strings.Replace(validLockJSON, `"repository": "MetaCubeX/mihomo",`, `"repository": "MetaCubeX/mihomo", "repository": "MetaCubeX/mihomo",`, 1),
			want: "duplicate JSON object key",
		},
		{
			name: "asset platform key",
			data: strings.Replace(validLockJSON, `"windows/arm64": {`, `"linux/amd64": {`, 1),
			want: "duplicate JSON object key",
		},
		{
			name: "escaped-equivalent key",
			data: strings.Replace(validLockJSON, `"schema": "mihari-aio-input-lock/v1",`, `"schema": "mihari-aio-input-lock/v1", "\u0073chema": "mihari-aio-input-lock/v1",`, 1),
			want: "duplicate JSON object key",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Decode(strings.NewReader(test.data))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Decode error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestDecodeRejectsCaseInsensitiveFieldAliases(t *testing.T) {
	tests := []struct {
		name string
		data string
		want string
	}{
		{
			name: "top-level alias",
			data: strings.Replace(validLockJSON, `"schema":`, `"Schema":`, 1),
			want: "lowercase",
		},
		{
			name: "top-level semantic duplicate",
			data: strings.Replace(validLockJSON, `"schema": "mihari-aio-input-lock/v1",`, `"schema": "mihari-aio-input-lock/v1", "Schema": "mihari-aio-input-lock/v1",`, 1),
			want: "duplicate JSON object key",
		},
		{
			name: "nested alias",
			data: strings.Replace(validLockJSON, `"repository": "MetaCubeX/mihomo",`, `"Repository": "MetaCubeX/mihomo",`, 1),
			want: "lowercase",
		},
		{
			name: "nested semantic duplicate",
			data: strings.Replace(validLockJSON, `"asset_id": 516687180,`, `"asset_id": 516687180, "Asset_ID": 516687180,`, 1),
			want: "duplicate JSON object key",
		},
		{
			name: "non-ASCII case-fold alias",
			data: strings.Replace(validLockJSON, `"schema":`, `"ſchema":`, 1),
			want: "lowercase ASCII",
		},
		{
			name: "non-ASCII semantic duplicate",
			data: strings.Replace(validLockJSON, `"schema": "mihari-aio-input-lock/v1",`, `"schema": "mihari-aio-input-lock/v1", "ſchema": "mihari-aio-input-lock/v1",`, 1),
			want: "duplicate JSON object key",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Decode(strings.NewReader(test.data))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Decode error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestDecodeAcceptsEscapedLowercaseASCIIKey(t *testing.T) {
	data := strings.Replace(validLockJSON, `"schema":`, `"\u0073chema":`, 1)
	lock, err := Decode(strings.NewReader(data))
	if err != nil {
		t.Fatalf("Decode escaped lowercase ASCII key: %v", err)
	}
	if lock.Schema != SchemaV1 {
		t.Fatalf("schema = %q, want %q", lock.Schema, SchemaV1)
	}
}

func TestNonASCIIKeyErrorDoesNotEchoRejectedAlias(t *testing.T) {
	data := strings.Replace(validLockJSON, `"schema":`, `"ſchema":`, 1)
	_, err := Decode(strings.NewReader(data))
	if err == nil {
		t.Fatal("Decode accepted non-ASCII field alias")
	}
	const want = "decode release input lock: JSON object keys must use lowercase ASCII schema spelling"
	if err.Error() != want {
		t.Fatalf("non-ASCII key error = %q, want fixed safe text %q", err, want)
	}
	if strings.Contains(err.Error(), "ſchema") {
		t.Fatalf("non-ASCII key error leaked rejected alias: %q", err)
	}
}

func TestDuplicateKeyErrorDoesNotEchoUntrustedKey(t *testing.T) {
	key := strings.Repeat("secret-token-", 200)
	data := []byte(`{"` + key + `": 1, "` + key + `": 2}`)
	err := rejectDuplicateKeys(data)
	if err == nil {
		t.Fatal("rejectDuplicateKeys accepted a duplicate long key")
	}
	message := err.Error()
	if message != "decode release input lock: duplicate JSON object key" {
		t.Fatalf("duplicate-key error = %q, want fixed safe text", message)
	}
	if strings.Contains(message, "secret-token") || strings.Contains(message, key) {
		t.Fatalf("duplicate-key error leaked untrusted key: %q", message)
	}
}

func TestDuplicateKeyCheckIgnoresObjectSyntaxInsideString(t *testing.T) {
	data := []byte(`{"message":"embedded {\"message\":true} is data"}`)
	if err := rejectDuplicateKeys(data); err != nil {
		t.Fatalf("rejectDuplicateKeys: %v", err)
	}
}

func TestDecodeValidatesTopLevelMetadata(t *testing.T) {
	tests := []struct {
		name string
		old  string
		new  string
		want string
	}{
		{name: "schema", old: SchemaV1, new: "mihari-aio-input-lock/v2", want: "schema"},
		{name: "mihomo repository", old: "MetaCubeX/mihomo", new: "attacker/mihomo", want: "mihomo repository"},
		{name: "mihomo channel", old: `"channel": "stable"`, new: `"channel": "nightly"`, want: "mihomo channel"},
		{name: "mihomo release ID", old: `"release_id": 371291937`, new: `"release_id": 0`, want: "release ID"},
		{name: "mihomo tag", old: `"tag": "v1.19.30"`, new: `"tag": "../v1.19.30"`, want: "mihomo tag"},
		{name: "GeoIP repository", old: "Loyalsoldier/geoip", new: "attacker/geoip", want: "GeoIP repository"},
		{name: "GeoIP commit length", old: "69986b5d098c8d723a2c4d56317bc10cd5669c02", new: "69986b5d", want: "GeoIP commit"},
		{name: "GeoIP commit uppercase", old: "69986b5d098c8d723a2c4d56317bc10cd5669c02", new: "69986B5D098C8D723A2C4D56317BC10CD5669C02", want: "GeoIP commit"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data := strings.Replace(validLockJSON, test.old, test.new, 1)
			_, err := Decode(strings.NewReader(data))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Decode error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestDecodeRequiresExactPlatformSet(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Lock)
		want string
	}{
		{
			name: "missing platform",
			edit: func(lock *Lock) {
				delete(lock.Mihomo.Assets, "windows/arm64")
			},
			want: "exactly six",
		},
		{
			name: "unexpected platform",
			edit: func(lock *Lock) {
				asset := lock.Mihomo.Assets["windows/arm64"]
				delete(lock.Mihomo.Assets, "windows/arm64")
				lock.Mihomo.Assets["freebsd/arm64"] = asset
			},
			want: "platform",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			lock, err := Decode(strings.NewReader(validLockJSON))
			if err != nil {
				t.Fatalf("Decode fixture: %v", err)
			}
			test.edit(&lock)
			err = lock.Validate()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestDecodeValidatesMihomoAssets(t *testing.T) {
	tests := []struct {
		name string
		old  string
		new  string
		want string
	}{
		{name: "asset ID", old: `"asset_id": 516687180`, new: `"asset_id": 0`, want: "asset ID"},
		{name: "size zero", old: `"size": 18899951`, new: `"size": 0`, want: "size"},
		{name: "size over bound", old: `"size": 18899951`, new: `"size": 268435457`, want: "size"},
		{name: "digest length", old: "db214c7a2517e63c150d123178d16d102e03a241ccdae4e5e07ffbe9cf56c6f9", new: "deadbeef", want: "SHA-256"},
		{name: "digest uppercase", old: "db214c7a2517e63c150d123178d16d102e03a241ccdae4e5e07ffbe9cf56c6f9", new: "DB214C7A2517E63C150D123178D16D102E03A241CCDAE4E5E07FFBE9CF56C6F9", want: "SHA-256"},
		{name: "wrong platform name", old: "mihomo-linux-amd64-compatible-v1.19.30.gz", new: "mihomo-linux-arm64-compatible-v1.19.30.gz", want: "asset name"},
		{name: "tag absent from name", old: "mihomo-linux-amd64-compatible-v1.19.30.gz", new: "mihomo-linux-amd64-compatible-v1.19.29.gz", want: "asset name"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data := strings.Replace(validLockJSON, test.old, test.new, 1)
			_, err := Decode(strings.NewReader(data))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Decode error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestValidateMihomoAssetSizeMatchesCoreDownloadBound(t *testing.T) {
	lock, err := Decode(strings.NewReader(validLockJSON))
	if err != nil {
		t.Fatalf("Decode fixture: %v", err)
	}
	asset := lock.Mihomo.Assets["linux/amd64"]
	asset.Size = 134217728 // 128 MiB: internal/core maxCoreArchiveSize.
	lock.Mihomo.Assets["linux/amd64"] = asset
	if err := lock.Validate(); err != nil {
		t.Fatalf("Validate 128 MiB asset: %v", err)
	}

	asset.Size = 134217729
	lock.Mihomo.Assets["linux/amd64"] = asset
	if err := lock.Validate(); err == nil || !strings.Contains(err.Error(), "size") {
		t.Fatalf("Validate 128 MiB + 1 asset error = %v, want size error", err)
	}
}

func TestValidateRequiresStableTagAsExactAssetNameSuffix(t *testing.T) {
	lock, err := Decode(strings.NewReader(validLockJSON))
	if err != nil {
		t.Fatalf("Decode fixture: %v", err)
	}
	lock.Mihomo.Tag = "v1.19.3"
	for platform, asset := range lock.Mihomo.Assets {
		asset.URL = strings.Replace(asset.URL, "/v1.19.30/", "/v1.19.3/", 1)
		lock.Mihomo.Assets[platform] = asset
	}
	if err := lock.Validate(); err == nil || !strings.Contains(err.Error(), "asset name") {
		t.Fatalf("Validate short tag error = %v, want exact asset-name suffix error", err)
	}
}

func TestValidateAlphaReleaseTagAndAssetNames(t *testing.T) {
	t.Run("valid Prerelease-Alpha", func(t *testing.T) {
		lock := alphaLock(t)
		if err := lock.Validate(); err != nil {
			t.Fatalf("Validate alpha lock: %v", err)
		}
	})

	t.Run("wrong release tag", func(t *testing.T) {
		lock := alphaLock(t)
		lock.Mihomo.Tag = "alpha-e183c58"
		if err := lock.Validate(); err == nil || !strings.Contains(err.Error(), "tag") {
			t.Fatalf("Validate alpha tag error = %v, want tag error", err)
		}
	})

	t.Run("non-standard asset name", func(t *testing.T) {
		lock := alphaLock(t)
		asset := lock.Mihomo.Assets["linux/amd64"]
		asset.Name = "mihomo-linux-amd64-compatible-alpha-e183c58.gz"
		asset.URL = "https://github.com/MetaCubeX/mihomo/releases/download/Prerelease-Alpha/" + asset.Name
		lock.Mihomo.Assets["linux/amd64"] = asset
		if err := lock.Validate(); err == nil || !strings.Contains(err.Error(), "asset name") {
			t.Fatalf("Validate alpha asset name error = %v, want asset-name error", err)
		}
	})
}

func alphaLock(t *testing.T) Lock {
	t.Helper()
	lock, err := Decode(strings.NewReader(validLockJSON))
	if err != nil {
		t.Fatalf("Decode fixture: %v", err)
	}
	lock.Mihomo.Channel = "alpha"
	lock.Mihomo.Tag = "Prerelease-Alpha"
	for platform, asset := range lock.Mihomo.Assets {
		extension := ".gz"
		if strings.HasPrefix(platform, "windows/") {
			extension = ".zip"
		}
		asset.Name = "mihomo-" + strings.ReplaceAll(platform, "/", "-") + "-alpha-e183c58" + extension
		asset.URL = "https://github.com/MetaCubeX/mihomo/releases/download/Prerelease-Alpha/" + asset.Name
		lock.Mihomo.Assets[platform] = asset
	}
	return lock
}

func TestDecodeConstrainsMihomoURLs(t *testing.T) {
	base := "https://github.com/MetaCubeX/mihomo/releases/download/v1.19.30/mihomo-linux-amd64-compatible-v1.19.30.gz"
	tests := []struct {
		name string
		url  string
	}{
		{name: "HTTP", url: strings.Replace(base, "https://", "http://", 1)},
		{name: "credentials", url: strings.Replace(base, "github.com", "token@github.com", 1)},
		{name: "wrong host", url: strings.Replace(base, "github.com", "example.com", 1)},
		{name: "wrong repository", url: strings.Replace(base, "MetaCubeX/mihomo", "attacker/mihomo", 1)},
		{name: "wrong tag", url: strings.Replace(base, "v1.19.30/", "v1.19.29/", 1)},
		{name: "wrong name", url: strings.Replace(base, "compatible-v1.19.30.gz", "v1.19.30.gz", 1)},
		{name: "query", url: base + "?download=1"},
		{name: "fragment", url: base + "#asset"},
		{name: "empty fragment delimiter", url: base + "#"},
		{name: "encoded path", url: strings.Replace(base, "/MetaCubeX/", "/%4detaCubeX/", 1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data := strings.Replace(validLockJSON, base, test.url, 1)
			_, err := Decode(strings.NewReader(data))
			if err == nil || !strings.Contains(err.Error(), "URL") {
				t.Fatalf("Decode error = %v, want URL validation error", err)
			}
		})
	}
}

func TestURLParseErrorsDoNotLeakCredentialsOrRawURL(t *testing.T) {
	rawURL := "https://secret-token@github.com/MetaCubeX/mihomo/%zz"
	err := validateExactHTTPSURL(rawURL, "github.com", "/MetaCubeX/mihomo/asset")
	if err == nil {
		t.Fatal("validateExactHTTPSURL accepted an invalid escaped URL")
	}
	message := err.Error()
	for _, secret := range []string{"secret-token", rawURL, "%zz"} {
		if strings.Contains(message, secret) {
			t.Fatalf("URL parse error %q leaked %q", message, secret)
		}
	}
}

func TestDecodeValidatesGeoIPFilesAndURLs(t *testing.T) {
	base := "https://raw.githubusercontent.com/Loyalsoldier/geoip/69986b5d098c8d723a2c4d56317bc10cd5669c02/GeoLite2-Country.mmdb"
	tests := []struct {
		name string
		old  string
		new  string
		want string
	}{
		{name: "file name", old: `"name": "GeoLite2-Country.mmdb"`, new: `"name": "country.mmdb"`, want: "GeoIP country name"},
		{name: "digest", old: "26a2c3c3791b36303a1c70bac18320c4e6bd40950286224a38f2756c0f7d0ca2", new: "deadbeef", want: "SHA-256"},
		{name: "HTTP", old: base, new: strings.Replace(base, "https://", "http://", 1), want: "URL"},
		{name: "credentials", old: base, new: strings.Replace(base, "raw.githubusercontent.com", "token@raw.githubusercontent.com", 1), want: "URL"},
		{name: "wrong host", old: base, new: strings.Replace(base, "raw.githubusercontent.com", "github.com", 1), want: "URL"},
		{name: "mutable ref", old: base, new: strings.Replace(base, "69986b5d098c8d723a2c4d56317bc10cd5669c02", "release", 1), want: "URL"},
		{name: "wrong file path", old: base, new: strings.Replace(base, "GeoLite2-Country.mmdb", "GeoLite2-ASN.mmdb", 1), want: "URL"},
		{name: "query", old: base, new: base + "?raw=1", want: "URL"},
		{name: "empty fragment delimiter", old: base, new: base + "#", want: "URL"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data := strings.Replace(validLockJSON, test.old, test.new, 1)
			_, err := Decode(strings.NewReader(data))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Decode error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestEncodeProducesCanonicalValidatedJSON(t *testing.T) {
	lock, err := Decode(strings.NewReader(validLockJSON))
	if err != nil {
		t.Fatalf("Decode fixture: %v", err)
	}
	first, err := Encode(lock)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	second, err := Encode(lock)
	if err != nil {
		t.Fatalf("Encode second: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("Encode returned non-deterministic bytes")
	}
	if len(first) == 0 || first[len(first)-1] != '\n' || (len(first) > 1 && first[len(first)-2] == '\n') {
		t.Fatalf("Encode must end in exactly one newline, got suffix %q", first[max(0, len(first)-4):])
	}
	if !bytes.Contains(first, []byte("\n  \"mihomo\":")) {
		t.Fatalf("Encode output is not canonical indented JSON: %s", first)
	}
	if bytes.Index(first, []byte(`"darwin/amd64"`)) > bytes.Index(first, []byte(`"windows/arm64"`)) {
		t.Fatal("platform map keys are not in canonical lexical order")
	}

	lock.Mihomo.ReleaseID = 0
	if _, err := Encode(lock); err == nil {
		t.Fatal("Encode accepted invalid lock")
	}
}

const validLockJSON = `{
  "schema": "mihari-aio-input-lock/v1",
  "mihomo": {
    "repository": "MetaCubeX/mihomo",
    "channel": "stable",
    "release_id": 371291937,
    "tag": "v1.19.30",
    "assets": {
      "linux/amd64": {
        "asset_id": 516687180,
        "name": "mihomo-linux-amd64-compatible-v1.19.30.gz",
        "url": "https://github.com/MetaCubeX/mihomo/releases/download/v1.19.30/mihomo-linux-amd64-compatible-v1.19.30.gz",
        "size": 18899951,
        "sha256": "db214c7a2517e63c150d123178d16d102e03a241ccdae4e5e07ffbe9cf56c6f9"
      },
      "linux/arm64": {
        "asset_id": 516687149,
        "name": "mihomo-linux-arm64-v1.19.30.gz",
        "url": "https://github.com/MetaCubeX/mihomo/releases/download/v1.19.30/mihomo-linux-arm64-v1.19.30.gz",
        "size": 16965828,
        "sha256": "58896873736d28628f66de3677c8654fa0f180662523148e136cff4f6e890069"
      },
      "darwin/amd64": {
        "asset_id": 516687197,
        "name": "mihomo-darwin-amd64-compatible-v1.19.30.gz",
        "url": "https://github.com/MetaCubeX/mihomo/releases/download/v1.19.30/mihomo-darwin-amd64-compatible-v1.19.30.gz",
        "size": 18300486,
        "sha256": "6e75de0732e8afabe413ff7c235e8f16226ce136672371c60787cbf9607402c5"
      },
      "darwin/arm64": {
        "asset_id": 516687188,
        "name": "mihomo-darwin-arm64-v1.19.30.gz",
        "url": "https://github.com/MetaCubeX/mihomo/releases/download/v1.19.30/mihomo-darwin-arm64-v1.19.30.gz",
        "size": 16805556,
        "sha256": "2c7f3a7904fa1cee291e124123e630e7b1ebd13765dd9bf26c0a28432004d9f4"
      },
      "windows/amd64": {
        "asset_id": 516687134,
        "name": "mihomo-windows-amd64-compatible-v1.19.30.zip",
        "url": "https://github.com/MetaCubeX/mihomo/releases/download/v1.19.30/mihomo-windows-amd64-compatible-v1.19.30.zip",
        "size": 18529108,
        "sha256": "289fde5e29d37a5b3326480590d8b3551c5bf7f8737290355c19bce74d57a563"
      },
      "windows/arm64": {
        "asset_id": 516687082,
        "name": "mihomo-windows-arm64-v1.19.30.zip",
        "url": "https://github.com/MetaCubeX/mihomo/releases/download/v1.19.30/mihomo-windows-arm64-v1.19.30.zip",
        "size": 16344535,
        "sha256": "b37c4b0259e85b020edc4215aa4c86052e21071cf520d4800364b21b4e2fc162"
      }
    }
  },
  "geoip": {
    "repository": "Loyalsoldier/geoip",
    "commit": "69986b5d098c8d723a2c4d56317bc10cd5669c02",
    "country": {
      "name": "GeoLite2-Country.mmdb",
      "url": "https://raw.githubusercontent.com/Loyalsoldier/geoip/69986b5d098c8d723a2c4d56317bc10cd5669c02/GeoLite2-Country.mmdb",
      "sha256": "26a2c3c3791b36303a1c70bac18320c4e6bd40950286224a38f2756c0f7d0ca2"
    },
    "asn": {
      "name": "GeoLite2-ASN.mmdb",
      "url": "https://raw.githubusercontent.com/Loyalsoldier/geoip/69986b5d098c8d723a2c4d56317bc10cd5669c02/GeoLite2-ASN.mmdb",
      "sha256": "82abcabdf4d0ecb34da45e4f0f9bc30bf933cfbfec446b89a2215fae5b1fdbdc"
    }
  }
}`
