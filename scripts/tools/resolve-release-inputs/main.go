// Command resolve-release-inputs resolves and verifies immutable upstream inputs
// for Mihari all-in-one release bundles. Release workflows never invoke it.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/mihari-proxy/mihari/internal/core"
	"github.com/mihari-proxy/mihari/scripts/tools/internal/releaseinputs"
)

const (
	defaultAPIBase      = "https://api.github.com"
	defaultMihomoRepo   = "MetaCubeX/mihomo"
	defaultGeoIPRepo    = "Loyalsoldier/geoip"
	defaultGeoIPRef     = "release"
	defaultOutput       = "scripts/release-inputs.lock.json"
	maxRefResponse      = 64 << 10
	defaultMaxDownload  = int64(128 << 20)
	resolverHTTPTimeout = 15 * time.Minute
	githubAPIVersion    = "2026-03-10"
	resolverUserAgent   = "mihari-release-input-resolver"
)

type options struct {
	Channel string
	Out     string

	GitHubToken      string
	HTTPClient       *http.Client
	APIBase          string
	DownloadBase     string
	MihomoRepository string
	GeoIPRepository  string
	GeoIPRef         string
	MaxDownloadBytes int64
}

func main() {
	os.Exit(runCLI(context.Background(), os.Args[1:], os.Stdout, os.Stderr, os.Getenv))
}

func parseOptions(args []string) (options, error) {
	return parseOptionsTo(args, io.Discard)
}

func parseOptionsTo(args []string, output io.Writer) (options, error) {
	flags := flag.NewFlagSet("resolve-release-inputs", flag.ContinueOnError)
	flags.SetOutput(output)
	var result options
	flags.StringVar(&result.Channel, "channel", "stable", "mihomo channel: stable or alpha")
	flags.StringVar(&result.Out, "out", defaultOutput, "release input lock output path")
	if err := flags.Parse(args); err != nil {
		return options{}, err
	}
	if flags.NArg() != 0 {
		return options{}, errors.New("unexpected positional arguments")
	}
	if result.Channel != "stable" && result.Channel != "alpha" {
		return options{}, fmt.Errorf("unsupported mihomo channel %q", result.Channel)
	}
	if result.Out == "" {
		return options{}, errors.New("--out is required")
	}
	return result, nil
}

func runCLI(ctx context.Context, args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	return runCLIWithResolver(ctx, args, stdout, stderr, getenv, resolve)
}

func runCLIWithResolver(ctx context.Context, args []string, stdout, stderr io.Writer, getenv func(string) string, execute func(context.Context, options) error) int {
	options, err := parseOptionsTo(args, stdout)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		if _, writeErr := fmt.Fprintln(stderr, "resolve-release-inputs:", err); writeErr != nil {
			return 1
		}
		return 2
	}
	if getenv != nil {
		options.GitHubToken = getenv("GITHUB_TOKEN")
	}
	if err := execute(ctx, options); err != nil {
		if _, writeErr := fmt.Fprintln(stderr, "resolve-release-inputs:", err); writeErr != nil {
			return 1
		}
		return 1
	}
	return 0
}

func resolve(ctx context.Context, options options) error {
	options = withDefaults(options)
	if options.Channel != "stable" && options.Channel != "alpha" {
		return fmt.Errorf("unsupported mihomo channel %q", options.Channel)
	}
	if options.Out == "" {
		return errors.New("--out is required")
	}

	client := options.HTTPClient
	if client == nil {
		var err error
		client, err = newHTTPClient(options.GitHubToken, options.APIBase)
		if err != nil {
			return err
		}
	}
	installer := core.Installer{
		HTTPClient: client,
		APIBase:    options.APIBase,
		Repository: options.MihomoRepository,
	}
	release, err := installer.LatestRelease(ctx, options.Channel)
	if err != nil {
		return fmt.Errorf("resolve mihomo release: %w", err)
	}
	commit, err := resolveCommit(ctx, client, options)
	if err != nil {
		return err
	}

	lock, selected, err := assembleLock(release, commit, options)
	if err != nil {
		return err
	}
	// Validate all identifiers and immutable public URLs before any payload I/O.
	if err := lock.Validate(); err != nil {
		return fmt.Errorf("validate resolved release inputs: %w", err)
	}

	for platform, asset := range selected {
		digest, err := downloadDigest(ctx, client, mappedDownloadURL(asset.URL, options.DownloadBase), asset.Size, options.MaxDownloadBytes, asset.Digest, mihomoPayload, options.DownloadBase)
		if err != nil {
			return fmt.Errorf("verify mihomo %s payload: %w", platform, err)
		}
		entry := lock.Mihomo.Assets[platform]
		entry.SHA256 = digest
		lock.Mihomo.Assets[platform] = entry
	}

	countryDigest, err := downloadDigest(ctx, client, mappedDownloadURL(lock.GeoIP.Country.URL, options.DownloadBase), 0, options.MaxDownloadBytes, "", geoIPPayload, options.DownloadBase)
	if err != nil {
		return fmt.Errorf("verify GeoIP country payload: %w", err)
	}
	lock.GeoIP.Country.SHA256 = countryDigest
	asnDigest, err := downloadDigest(ctx, client, mappedDownloadURL(lock.GeoIP.ASN.URL, options.DownloadBase), 0, options.MaxDownloadBytes, "", geoIPPayload, options.DownloadBase)
	if err != nil {
		return fmt.Errorf("verify GeoIP ASN payload: %w", err)
	}
	lock.GeoIP.ASN.SHA256 = asnDigest

	data, err := releaseinputs.Encode(lock)
	if err != nil {
		return fmt.Errorf("encode resolved release inputs: %w", err)
	}
	if err := writeAtomic(ctx, options.Out, data); err != nil {
		return err
	}
	return nil
}

func withDefaults(options options) options {
	if options.Channel == "" {
		options.Channel = "stable"
	}
	if options.APIBase == "" {
		options.APIBase = defaultAPIBase
	}
	if options.MihomoRepository == "" {
		options.MihomoRepository = defaultMihomoRepo
	}
	if options.GeoIPRepository == "" {
		options.GeoIPRepository = defaultGeoIPRepo
	}
	if options.GeoIPRef == "" {
		options.GeoIPRef = defaultGeoIPRef
	}
	if options.MaxDownloadBytes <= 0 {
		options.MaxDownloadBytes = defaultMaxDownload
	}
	return options
}

func resolveCommit(ctx context.Context, client *http.Client, options options) (string, error) {
	rawURL := strings.TrimRight(options.APIBase, "/") + "/repos/" + options.GeoIPRepository + "/git/ref/heads/" + url.PathEscape(options.GeoIPRef)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", errors.New("create GeoIP commit request")
	}
	setGitHubHeaders(request, options.GitHubToken)
	response, err := client.Do(request)
	if err != nil {
		return "", safeNetworkError{operation: "resolve GeoIP commit", cause: err}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("resolve GeoIP commit: unexpected HTTP status %d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxRefResponse+1))
	if err != nil {
		return "", fmt.Errorf("read GeoIP commit response: %w", err)
	}
	if len(raw) > maxRefResponse {
		return "", errors.New("GeoIP ref response exceeds size limit")
	}
	var result struct {
		Object struct {
			Type string `json:"type"`
			SHA  string `json:"sha"`
		} `json:"object"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", errors.New("decode GeoIP ref response")
	}
	if result.Object.Type != "commit" {
		return "", errors.New("GeoIP release branch must point directly to a commit object")
	}
	if !isLowerHex(result.Object.SHA, 40) {
		return "", errors.New("GeoIP commit must be exactly 40 lowercase hexadecimal characters")
	}
	return result.Object.SHA, nil
}

func assembleLock(release core.Release, commit string, options options) (releaseinputs.Lock, map[string]core.Asset, error) {
	lock := releaseinputs.Lock{
		Schema: releaseinputs.SchemaV1,
		Mihomo: releaseinputs.MihomoInputs{
			Repository: options.MihomoRepository,
			Channel:    options.Channel,
			ReleaseID:  release.ID,
			Tag:        release.TagName,
			Assets:     make(map[string]releaseinputs.MihomoAsset, 6),
		},
		GeoIP: releaseinputs.GeoIPInputs{
			Repository: options.GeoIPRepository,
			Commit:     commit,
			Country: releaseinputs.GeoIPFile{
				Name:   "GeoLite2-Country.mmdb",
				URL:    "https://raw.githubusercontent.com/" + options.GeoIPRepository + "/" + commit + "/GeoLite2-Country.mmdb",
				SHA256: strings.Repeat("0", sha256.Size*2),
			},
			ASN: releaseinputs.GeoIPFile{
				Name:   "GeoLite2-ASN.mmdb",
				URL:    "https://raw.githubusercontent.com/" + options.GeoIPRepository + "/" + commit + "/GeoLite2-ASN.mmdb",
				SHA256: strings.Repeat("0", sha256.Size*2),
			},
		},
	}
	selected := make(map[string]core.Asset, 6)
	for _, platform := range releaseinputs.RequiredPlatforms() {
		goos, goarch, found := strings.Cut(platform, "/")
		if !found {
			return releaseinputs.Lock{}, nil, errors.New("invalid required platform")
		}
		asset, err := selectResolverAsset(release, goos, goarch, options.Channel)
		if err != nil {
			return releaseinputs.Lock{}, nil, fmt.Errorf("select mihomo %s asset: %w", platform, err)
		}
		selected[platform] = asset
		lock.Mihomo.Assets[platform] = releaseinputs.MihomoAsset{
			AssetID: asset.ID,
			Name:    asset.Name,
			URL:     asset.URL,
			Size:    asset.Size,
			SHA256:  strings.Repeat("0", sha256.Size*2),
		}
	}
	return lock, selected, nil
}

func selectResolverAsset(release core.Release, goos, goarch, channel string) (core.Asset, error) {
	candidates := make([]core.Asset, 0, len(release.Assets))
	for _, asset := range release.Assets {
		if _, err := core.SelectAsset(core.Release{TagName: release.TagName, Assets: []core.Asset{asset}}, goos, goarch, channel); err == nil {
			candidates = append(candidates, asset)
		}
	}
	if len(candidates) == 0 {
		return core.Asset{}, fmt.Errorf("mihomo release has no compatible %s/%s asset", goos, goarch)
	}
	preferred := preferredStableAssetName(goos, goarch, release.TagName)
	sort.Slice(candidates, func(leftIndex, rightIndex int) bool {
		left := candidates[leftIndex]
		right := candidates[rightIndex]
		leftPreferred := channel != "alpha" && strings.EqualFold(left.Name, preferred)
		rightPreferred := channel != "alpha" && strings.EqualFold(right.Name, preferred)
		if leftPreferred != rightPreferred {
			return leftPreferred
		}
		leftName := strings.ToLower(left.Name)
		rightName := strings.ToLower(right.Name)
		if leftName != rightName {
			return leftName < rightName
		}
		if left.ID != right.ID {
			return left.ID < right.ID
		}
		if left.URL != right.URL {
			return left.URL < right.URL
		}
		if left.Size != right.Size {
			return left.Size < right.Size
		}
		return left.Digest < right.Digest
	})
	return candidates[0], nil
}

func preferredStableAssetName(goos, goarch, tag string) string {
	extension := ".gz"
	if goos == "windows" {
		extension = ".zip"
	}
	variant := ""
	if goarch == "amd64" {
		variant = "-compatible"
	}
	return "mihomo-" + strings.ToLower(goos) + "-" + strings.ToLower(goarch) + variant + "-" + tag + extension
}

type payloadSource uint8

const (
	mihomoPayload payloadSource = iota + 1
	geoIPPayload
)

func downloadDigest(ctx context.Context, client *http.Client, rawURL string, expectedSize, maxBytes int64, upstreamDigest string, source payloadSource, testBase string) (string, error) {
	policy, err := newPayloadURLPolicy(source, testBase)
	if err != nil {
		return "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", errors.New("create payload request")
	}
	if err := policy.validate(request.URL); err != nil {
		return "", err
	}
	request.Header.Set("User-Agent", resolverUserAgent)
	downloadClient := *client
	baseRedirect := client.CheckRedirect
	downloadClient.CheckRedirect = func(next *http.Request, previous []*http.Request) error {
		if len(previous) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		if err := policy.validate(next.URL); err != nil {
			return err
		}
		if baseRedirect != nil {
			return baseRedirect(next, previous)
		}
		return nil
	}
	response, err := downloadClient.Do(request)
	if err != nil {
		return "", safeNetworkError{operation: "download payload", cause: err}
	}
	defer response.Body.Close()
	if response.Request == nil {
		return "", errors.New("payload response URL is unavailable")
	}
	if err := policy.validate(response.Request.URL); err != nil {
		return "", err
	}
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download payload: unexpected HTTP status %d", response.StatusCode)
	}
	if response.ContentLength > maxBytes {
		return "", errors.New("payload exceeds size limit")
	}
	hash := sha256.New()
	written, err := io.Copy(hash, io.LimitReader(response.Body, maxBytes+1))
	if err != nil {
		return "", fmt.Errorf("download payload: %w", err)
	}
	if written > maxBytes {
		return "", errors.New("payload exceeds size limit")
	}
	if expectedSize > 0 && written != expectedSize {
		return "", errors.New("payload size mismatch")
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	if upstreamDigest != "" {
		algorithm, expected, found := strings.Cut(upstreamDigest, ":")
		if !found || algorithm != "sha256" || !isLowerHex(expected, sha256.Size*2) {
			return "", errors.New("unsupported upstream payload digest")
		}
		if digest != expected {
			return "", errors.New("payload digest mismatch")
		}
	}
	return digest, nil
}

type safeNetworkError struct {
	operation string
	cause     error
}

func (failure safeNetworkError) Error() string {
	return failure.operation + " failed"
}

func (failure safeNetworkError) Unwrap() error {
	return failure.cause
}

type payloadURLPolicy struct {
	source     payloadSource
	testOrigin *url.URL
}

func newPayloadURLPolicy(source payloadSource, testBase string) (payloadURLPolicy, error) {
	if source != mihomoPayload && source != geoIPPayload {
		return payloadURLPolicy{}, errors.New("payload source is unsupported")
	}
	policy := payloadURLPolicy{source: source}
	if testBase == "" {
		return policy, nil
	}
	base, err := url.Parse(testBase)
	if err != nil || strings.Contains(testBase, "#") || !base.IsAbs() || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" || base.User != nil || base.Opaque != "" || base.RawQuery != "" || base.ForceQuery || base.Fragment != "" {
		return payloadURLPolicy{}, errors.New("injected payload origin is invalid")
	}
	policy.testOrigin = base
	return policy, nil
}

func (policy payloadURLPolicy) validate(candidate *url.URL) error {
	if candidate == nil || candidate.User != nil || candidate.Opaque != "" {
		return errors.New("payload URL is not approved")
	}
	if policy.testOrigin != nil {
		if candidate.Scheme != policy.testOrigin.Scheme || candidate.Host != policy.testOrigin.Host {
			return errors.New("payload redirect escaped the injected download origin")
		}
		return nil
	}
	if candidate.Scheme != "https" || candidate.Port() != "" {
		return errors.New("payload URL must use an approved HTTPS host")
	}
	host := strings.ToLower(candidate.Hostname())
	switch policy.source {
	case geoIPPayload:
		if host != "raw.githubusercontent.com" {
			return errors.New("GeoIP payload URL host is not approved")
		}
	case mihomoPayload:
		if host != "github.com" && host != "release-assets.githubusercontent.com" && host != "objects.githubusercontent.com" {
			return errors.New("mihomo payload URL host is not approved")
		}
	default:
		return errors.New("payload source is unsupported")
	}
	return nil
}

func mappedDownloadURL(rawURL, downloadBase string) string {
	if downloadBase == "" {
		return rawURL
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	return strings.TrimRight(downloadBase, "/") + parsed.EscapedPath()
}

func newHTTPClient(token, apiBase string) (*http.Client, error) {
	parsed, err := url.Parse(apiBase)
	if err != nil || strings.Contains(apiBase, "#") || !parsed.IsAbs() || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Opaque != "" || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return nil, errors.New("GitHub API base must be an absolute HTTPS URL without credentials, query, or fragment")
	}
	transport := http.DefaultTransport
	if token != "" {
		transport = bearerTransport{base: transport, token: token, allowedHost: parsed.Host}
	}
	return &http.Client{Timeout: resolverHTTPTimeout, Transport: transport, CheckRedirect: redirectPolicy}, nil
}

type bearerTransport struct {
	base        http.RoundTripper
	token       string
	allowedHost string
}

func (transport bearerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	cloned := request.Clone(request.Context())
	if cloned.URL.Host == transport.allowedHost {
		cloned.Header.Set("Authorization", "Bearer "+transport.token)
	} else {
		cloned.Header.Del("Authorization")
	}
	return transport.base.RoundTrip(cloned)
}

func redirectPolicy(request *http.Request, previous []*http.Request) error {
	if len(previous) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	if len(previous) > 0 && previous[len(previous)-1].URL.Scheme == "https" && request.URL.Scheme != "https" {
		return errors.New("payload redirect must preserve HTTPS")
	}
	return nil
}

func setGitHubHeaders(request *http.Request, token string) {
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", githubAPIVersion)
	request.Header.Set("User-Agent", resolverUserAgent)
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
}

func isLowerHex(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}
