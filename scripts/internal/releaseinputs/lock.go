// Package releaseinputs defines the reviewed, immutable inputs used to build
// Mihari all-in-one release bundles.
package releaseinputs

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// SchemaV1 identifies the first Mihari all-in-one release input lock schema.
	SchemaV1 = "mihari-aio-input-lock/v1"
	// MaxLockSize bounds a release input lock before JSON decoding.
	MaxLockSize = 1 << 20

	mihomoRepository = "MetaCubeX/mihomo"
	geoIPRepository  = "Loyalsoldier/geoip"
	// Keep this in sync with internal/core maxCoreArchiveSize. The lock must
	// never approve an archive that the consuming downloader will reject.
	maxMihomoAsset = 128 << 20
)

var requiredPlatforms = [...]string{
	"linux/amd64",
	"linux/arm64",
	"darwin/amd64",
	"darwin/arm64",
	"windows/amd64",
	"windows/arm64",
}

// Lock contains every immutable external input used by the all-in-one build.
type Lock struct {
	Schema string       `json:"schema"`
	Mihomo MihomoInputs `json:"mihomo"`
	GeoIP  GeoIPInputs  `json:"geoip"`
}

// MihomoInputs identifies one release and its exact supported assets.
type MihomoInputs struct {
	Repository string                 `json:"repository"`
	Channel    string                 `json:"channel"`
	ReleaseID  int64                  `json:"release_id"`
	Tag        string                 `json:"tag"`
	Assets     map[string]MihomoAsset `json:"assets"`
}

// MihomoAsset identifies and verifies one platform-specific mihomo archive.
type MihomoAsset struct {
	AssetID int64  `json:"asset_id"`
	Name    string `json:"name"`
	URL     string `json:"url"`
	Size    int64  `json:"size"`
	SHA256  string `json:"sha256"`
}

// GeoIPInputs pins the GeoIP repository and both required databases.
type GeoIPInputs struct {
	Repository string    `json:"repository"`
	Commit     string    `json:"commit"`
	Country    GeoIPFile `json:"country"`
	ASN        GeoIPFile `json:"asn"`
}

// GeoIPFile identifies and verifies one GeoIP database.
type GeoIPFile struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}

// RequiredPlatforms returns the exact platform keys required by the lock.
func RequiredPlatforms() []string {
	platforms := make([]string, len(requiredPlatforms))
	copy(platforms, requiredPlatforms[:])
	return platforms
}

// Load opens, strictly decodes, and validates a release input lock file.
func Load(path string) (Lock, error) {
	file, err := os.Open(path)
	if err != nil {
		return Lock{}, fmt.Errorf("open release input lock: %w", err)
	}
	defer func() {
		// A close error cannot affect a completed read-only decode.
		_ = file.Close()
	}()
	return Decode(file)
}

// Decode reads, strictly decodes, and validates a release input lock.
func Decode(reader io.Reader) (Lock, error) {
	limited := io.LimitReader(reader, MaxLockSize+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return Lock{}, fmt.Errorf("read release input lock: %w", err)
	}
	if len(data) > MaxLockSize {
		return Lock{}, fmt.Errorf("release input lock exceeds %d bytes", MaxLockSize)
	}
	if !utf8.Valid(data) {
		return Lock{}, errors.New("release input lock is not valid UTF-8")
	}
	if err := rejectDuplicateKeys(data); err != nil {
		return Lock{}, err
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var lock Lock
	if err := decoder.Decode(&lock); err != nil {
		return Lock{}, fmt.Errorf("decode release input lock: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return Lock{}, errors.New("decode release input lock: trailing JSON value")
		}
		return Lock{}, fmt.Errorf("decode release input lock: trailing JSON: %w", err)
	}
	if err := lock.Validate(); err != nil {
		return Lock{}, err
	}
	return lock, nil
}

// Encode validates a lock and returns deterministic indented JSON ending in
// exactly one newline.
func Encode(lock Lock) ([]byte, error) {
	if err := lock.Validate(); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(lock, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode release input lock: %w", err)
	}
	return append(data, '\n'), nil
}

// Validate checks the complete semantic and URL contract of a lock.
func (lock Lock) Validate() error {
	if lock.Schema != SchemaV1 {
		return fmt.Errorf("release input lock schema %q is unsupported", lock.Schema)
	}
	if lock.Mihomo.Repository != mihomoRepository {
		return fmt.Errorf("mihomo repository must be %q", mihomoRepository)
	}
	if lock.Mihomo.Channel != "stable" && lock.Mihomo.Channel != "alpha" {
		return fmt.Errorf("mihomo channel %q is unsupported", lock.Mihomo.Channel)
	}
	if lock.Mihomo.ReleaseID <= 0 {
		return errors.New("mihomo release ID must be positive")
	}
	if err := validateTag(lock.Mihomo.Channel, lock.Mihomo.Tag); err != nil {
		return err
	}
	if err := validateMihomoAssets(lock.Mihomo); err != nil {
		return err
	}
	if lock.GeoIP.Repository != geoIPRepository {
		return fmt.Errorf("GeoIP repository must be %q", geoIPRepository)
	}
	if !isLowerHex(lock.GeoIP.Commit, 40) {
		return errors.New("GeoIP commit must be exactly 40 lowercase hexadecimal characters")
	}
	if err := validateGeoIPFile("country", "GeoLite2-Country.mmdb", lock.GeoIP, lock.GeoIP.Country); err != nil {
		return err
	}
	if err := validateGeoIPFile("ASN", "GeoLite2-ASN.mmdb", lock.GeoIP, lock.GeoIP.ASN); err != nil {
		return err
	}
	return nil
}

func validateTag(channel, tag string) error {
	if tag == "" || strings.ContainsAny(tag, "/\\?#") || strings.TrimSpace(tag) != tag {
		return fmt.Errorf("mihomo tag %q is invalid", tag)
	}
	for _, char := range tag {
		if char < 0x21 || char > 0x7e {
			return fmt.Errorf("mihomo tag %q is invalid", tag)
		}
	}
	if channel == "stable" && !strings.HasPrefix(tag, "v") {
		return fmt.Errorf("mihomo tag %q is invalid for stable channel", tag)
	}
	if channel == "alpha" && tag != "Prerelease-Alpha" {
		return fmt.Errorf("mihomo tag %q is invalid for alpha channel", tag)
	}
	return nil
}

func validateMihomoAssets(inputs MihomoInputs) error {
	if len(inputs.Assets) != len(requiredPlatforms) {
		return fmt.Errorf("mihomo assets must contain exactly six platforms, got %d", len(inputs.Assets))
	}
	seenIDs := make(map[int64]string, len(inputs.Assets))
	seenNames := make(map[string]string, len(inputs.Assets))
	seenURLs := make(map[string]string, len(inputs.Assets))
	for _, platform := range requiredPlatforms {
		asset, ok := inputs.Assets[platform]
		if !ok {
			return fmt.Errorf("mihomo assets missing required platform %q", platform)
		}
		if asset.AssetID <= 0 {
			return fmt.Errorf("mihomo %s asset ID must be positive", platform)
		}
		if previous, duplicate := seenIDs[asset.AssetID]; duplicate {
			return fmt.Errorf("mihomo %s asset ID duplicates %s", platform, previous)
		}
		seenIDs[asset.AssetID] = platform
		if asset.Size <= 0 || asset.Size > maxMihomoAsset {
			return fmt.Errorf("mihomo %s asset size must be between 1 and %d bytes", platform, maxMihomoAsset)
		}
		if !isLowerHex(asset.SHA256, 64) {
			return fmt.Errorf("mihomo %s SHA-256 must be exactly 64 lowercase hexadecimal characters", platform)
		}
		if err := validateMihomoAssetName(platform, inputs.Channel, inputs.Tag, asset.Name); err != nil {
			return err
		}
		if previous, duplicate := seenNames[asset.Name]; duplicate {
			return fmt.Errorf("mihomo %s asset name duplicates %s", platform, previous)
		}
		seenNames[asset.Name] = platform
		if err := validateExactHTTPSURL(asset.URL, "github.com", "/"+inputs.Repository+"/releases/download/"+inputs.Tag+"/"+asset.Name); err != nil {
			return fmt.Errorf("mihomo %s asset URL: %w", platform, err)
		}
		if previous, duplicate := seenURLs[asset.URL]; duplicate {
			return fmt.Errorf("mihomo %s asset URL duplicates %s", platform, previous)
		}
		seenURLs[asset.URL] = platform
	}
	for platform := range inputs.Assets {
		if !isRequiredPlatform(platform) {
			return fmt.Errorf("mihomo assets contain unexpected platform %q", platform)
		}
	}
	return nil
}

func validateMihomoAssetName(platform, channel, tag, name string) error {
	prefix := "mihomo-" + strings.ReplaceAll(platform, "/", "-") + "-"
	if !strings.HasPrefix(name, prefix) || strings.ContainsAny(name, "/\\") {
		return fmt.Errorf("mihomo %s asset name %q is invalid", platform, name)
	}
	extension := ".gz"
	if strings.HasPrefix(platform, "windows/") {
		extension = ".zip"
	}
	if !strings.HasSuffix(name, extension) {
		return fmt.Errorf("mihomo %s asset name %q must end in %s", platform, name, extension)
	}
	if channel == "stable" && !strings.HasSuffix(name, "-"+tag+extension) {
		return fmt.Errorf("mihomo %s asset name %q must end in -%s%s", platform, name, tag, extension)
	}
	if channel == "alpha" {
		alphaPart := strings.TrimSuffix(strings.TrimPrefix(name, prefix), extension)
		sha, found := strings.CutPrefix(alphaPart, "alpha-")
		if !found || len(sha) < 7 || len(sha) > 40 || !isLowerHex(sha, len(sha)) {
			return fmt.Errorf("mihomo %s asset name %q must use the standard alpha SHA form", platform, name)
		}
	}
	return nil
}

func validateGeoIPFile(label, expectedName string, inputs GeoIPInputs, file GeoIPFile) error {
	if file.Name != expectedName {
		return fmt.Errorf("GeoIP %s name must be %q", label, expectedName)
	}
	if !isLowerHex(file.SHA256, 64) {
		return fmt.Errorf("GeoIP %s SHA-256 must be exactly 64 lowercase hexadecimal characters", label)
	}
	expectedPath := "/" + inputs.Repository + "/" + inputs.Commit + "/" + file.Name
	if err := validateExactHTTPSURL(file.URL, "raw.githubusercontent.com", expectedPath); err != nil {
		return fmt.Errorf("GeoIP %s URL: %w", label, err)
	}
	return nil
}

func validateExactHTTPSURL(rawURL, host, path string) error {
	if strings.Contains(rawURL, "#") {
		return errors.New("must not contain a fragment delimiter")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return errors.New("invalid URL syntax")
	}
	if parsed.Scheme != "https" || parsed.Host != host || parsed.User != nil || parsed.Opaque != "" {
		return errors.New("must use the approved HTTPS host without credentials")
	}
	if parsed.RawPath != "" || parsed.Path != path {
		return fmt.Errorf("path must be %q", path)
	}
	if parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return errors.New("must not contain a query or fragment")
	}
	return nil
}

func rejectDuplicateKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := walkJSONValue(decoder); err != nil {
		return fmt.Errorf("decode release input lock: %w", err)
	}
	return nil
}

func walkJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("object key is not a string")
			}
			canonicalKey := foldJSONKey(key)
			if _, duplicate := seen[canonicalKey]; duplicate {
				return errors.New("duplicate JSON object key")
			}
			seen[canonicalKey] = struct{}{}
			if !isLowerASCIIKey(key) {
				return errors.New("JSON object keys must use lowercase ASCII schema spelling")
			}
			if err := walkJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim('}') {
			return errors.New("object is not properly closed")
		}
	case '[':
		for decoder.More() {
			if err := walkJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim(']') {
			return errors.New("array is not properly closed")
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delim)
	}
	return nil
}

func foldJSONKey(key string) string {
	var folded strings.Builder
	folded.Grow(len(key))
	for _, character := range key {
		folded.WriteRune(foldKeyRune(character))
	}
	return folded.String()
}

func foldKeyRune(character rune) rune {
	if character < utf8.RuneSelf {
		if character >= 'A' && character <= 'Z' {
			return character + ('a' - 'A')
		}
		return character
	}
	for candidate := unicode.SimpleFold(character); candidate != character; candidate = unicode.SimpleFold(candidate) {
		if candidate < utf8.RuneSelf {
			if candidate >= 'A' && candidate <= 'Z' {
				candidate += 'a' - 'A'
			}
			return candidate
		}
	}
	return unicode.ToLower(character)
}

func isLowerASCIIKey(key string) bool {
	for index := 0; index < len(key); index++ {
		character := key[index]
		if character >= utf8.RuneSelf || (character >= 'A' && character <= 'Z') {
			return false
		}
	}
	return true
}

func isRequiredPlatform(candidate string) bool {
	for _, platform := range requiredPlatforms {
		if candidate == platform {
			return true
		}
	}
	return false
}

func isLowerHex(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}
