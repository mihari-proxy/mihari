// Command build-all-in-one packs mihari + mihomo core + GeoIP×2 + the local
// install-aio installer into per-platform all-in-one bundles for offline
// distribution. It reuses the runtime core.LatestRelease/Download/SelectAsset
// (mihomo) and geoip.Downloader (GeoIP, same checksum mechanism as the daemon);
// extraction and smoke checks are bundler-local per design §4.1.
package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/mihari-proxy/mihari/internal/core"
	"github.com/mihari-proxy/mihari/internal/geoip"
)

var defaultPlatforms = []string{
	"linux/amd64", "linux/arm64",
	"darwin/amd64", "darwin/arm64",
	"windows/amd64", "windows/arm64",
}

const maxMihomoBinary int64 = 256 << 20

type options struct {
	MihariDir   string // dist/ — CI cross-build artifacts (mihari-<os>-<arch>[.exe])
	Out         string // bundles/
	ScriptsDir  string // directory holding install-aio.sh / install-aio.ps1 (scripts/install in CI)
	Platforms   []string
	GitHubToken string // optional; adds Authorization: Bearer when set
	Channel     string // mihomo channel: stable (default) or alpha

	// Test seams (zero-valued in production).
	HTTPClient    *http.Client
	APIBase       string // override GitHub API base
	Repository    string // override "MetaCubeX/mihomo"
	GeoIPBase     string // override GeoIP root (empty → geoip.Default*URL)
	GeoIPValidate func(string) error
	Runner        core.CommandRunner // linux/amd64 `-v` smoke; defaults to OSCommandRunner
}

func main() {
	mihariDir := flag.String("mihari-dir", "dist", "directory with mihari-<os>-<arch>[.exe] build artifacts")
	out := flag.String("out", "bundles", "output directory for all-in-one bundles")
	platforms := flag.String("platforms", strings.Join(defaultPlatforms, ","), "comma-separated goos/goarch targets")
	scriptsDir := flag.String("scripts-dir", "scripts/install", "directory holding install-aio.sh / install-aio.ps1")
	token := flag.String("github-token", os.Getenv("GITHUB_TOKEN"), "GitHub API token (default $GITHUB_TOKEN)")
	channel := flag.String("channel", "stable", "mihomo channel: stable or alpha")
	flag.Parse()

	if err := run(options{
		MihariDir: *mihariDir, Out: *out, ScriptsDir: *scriptsDir,
		Platforms: splitPlatforms(*platforms), GitHubToken: *token, Channel: *channel,
	}); err != nil {
		fmt.Fprintln(os.Stderr, "build-all-in-one:", err)
		os.Exit(1)
	}
}

func run(o options) error {
	if o.MihariDir == "" {
		return errors.New("--mihari-dir is required")
	}
	if o.Out == "" {
		return errors.New("--out is required")
	}
	if len(o.Platforms) == 0 {
		o.Platforms = defaultPlatforms
	}
	if o.ScriptsDir == "" {
		o.ScriptsDir = "scripts/install"
	}
	if o.Channel == "" {
		o.Channel = "stable"
	}
	if err := os.MkdirAll(o.Out, 0o755); err != nil {
		return fmt.Errorf("create bundles directory: %w", err)
	}
	client := o.HTTPClient
	if client == nil {
		client = newClient(o.GitHubToken)
	}
	installer := core.Installer{HTTPClient: client, APIBase: o.APIBase, Repository: o.Repository, Runner: o.Runner}
	ctx := context.Background()

	// Exactly one mihomo API request returns every platform asset.
	release, err := installer.LatestRelease(ctx, o.Channel)
	if err != nil {
		return fmt.Errorf("fetch mihomo release: %w", err)
	}

	// GeoIP×2 is shared across all platforms — download once.
	geoipDir, err := os.MkdirTemp("", "aio-geoip-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(geoipDir)
	countryPath, asnPath, err := downloadGeoIP(ctx, client, geoipDir, o)
	if err != nil {
		return fmt.Errorf("download geoip: %w", err)
	}

	for _, platform := range o.Platforms {
		goos, goarch, err := splitPlatform(platform)
		if err != nil {
			return fmt.Errorf("platform %q: %w", platform, err)
		}
		if err := buildPlatform(ctx, installer, release, goos, goarch, o, countryPath, asnPath); err != nil {
			return fmt.Errorf("build %s: %w", platform, err)
		}
	}
	return nil
}

func buildPlatform(ctx context.Context, installer core.Installer, release core.Release, goos, goarch string, o options, countryPath, asnPath string) error {
	stage, err := os.MkdirTemp("", "aio-stage-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)

	asset, err := core.SelectAsset(release, goos, goarch, o.Channel)
	if err != nil {
		return err
	}
	// core.Download opens the destination with O_WRONLY|O_TRUNC (no O_CREATE);
	// create it first, mirroring Prepare's CreateTemp contract (Task 7).
	archivePath := filepath.Join(stage, ".mihomo-archive")
	stagedArchive, err := os.Create(archivePath)
	if err != nil {
		return err
	}
	if err := stagedArchive.Close(); err != nil {
		return err
	}
	if err := installer.Download(ctx, asset, archivePath); err != nil {
		return err
	}
	mihomoName := "mihomo"
	if goos == "windows" {
		mihomoName = "mihomo.exe"
	}
	mihomoDest := filepath.Join(stage, "data", "bin", mihomoName)
	if err := extractMihomo(archivePath, asset.Name, mihomoDest); err != nil {
		return err
	}
	if err := writeCoreChannelSidecar(filepath.Join(stage, "data", "bin", "core-channel"), o.Channel, release, asset); err != nil {
		return err
	}
	if err := os.Remove(archivePath); err != nil {
		return err
	}
	if err := smokeMihomo(ctx, goos, goarch, mihomoDest, o.Runner); err != nil {
		return err
	}

	geoipDest := filepath.Join(stage, "data", "geoip")
	if err := os.MkdirAll(geoipDest, 0o700); err != nil {
		return err
	}
	if err := copyFile(countryPath, filepath.Join(geoipDest, "GeoLite2-Country.mmdb")); err != nil {
		return err
	}
	if err := copyFile(asnPath, filepath.Join(geoipDest, "GeoLite2-ASN.mmdb")); err != nil {
		return err
	}

	mihariName := "mihari"
	if goos == "windows" {
		mihariName = "mihari.exe"
	}
	if err := copyFile(distBinaryName(o.MihariDir, goos, goarch), filepath.Join(stage, mihariName)); err != nil {
		return err
	}

	script := "install-aio.sh"
	if goos == "windows" {
		script = "install-aio.ps1"
	}
	if err := copyFile(filepath.Join(o.ScriptsDir, script), filepath.Join(stage, script)); err != nil {
		return err
	}

	files, err := relativeFiles(stage)
	if err != nil {
		return err
	}
	if err := assertStage(goos, files); err != nil {
		return err
	}
	return packStage(stage, o.Out, goos, goarch)
}

// downloadGeoIP fetches Country+ASN MMDB once with the same checksum-verified
// mechanism as the runtime geoip.Service (geoip.Downloader + .sha256sum).
func downloadGeoIP(ctx context.Context, client *http.Client, destDir string, o options) (country, asn string, err error) {
	countryURL, countryChecksum := geoip.DefaultCountryURL, geoip.DefaultCountryChecksumURL
	asnURL, asnChecksum := geoip.DefaultASNURL, geoip.DefaultASNChecksumURL
	allowHTTP := false
	if o.GeoIPBase != "" {
		allowHTTP = true
		root := strings.TrimRight(o.GeoIPBase, "/")
		countryURL = root + "/GeoLite2-Country.mmdb"
		countryChecksum = countryURL + ".sha256sum"
		asnURL = root + "/GeoLite2-ASN.mmdb"
		asnChecksum = asnURL + ".sha256sum"
	}
	downloader := geoip.Downloader{Client: client, StagingDir: destDir, AllowHTTP: allowHTTP, Validate: o.GeoIPValidate}

	country = filepath.Join(destDir, "GeoLite2-Country.mmdb")
	countryCandidate, err := downloader.Prepare(ctx, geoip.DownloadSpec{URL: countryURL, ChecksumURL: countryChecksum, Destination: country})
	if err != nil {
		return "", "", err
	}
	if err := countryCandidate.Commit(); err != nil {
		return "", "", err
	}
	asn = filepath.Join(destDir, "GeoLite2-ASN.mmdb")
	asnCandidate, err := downloader.Prepare(ctx, geoip.DownloadSpec{URL: asnURL, ChecksumURL: asnChecksum, Destination: asn})
	if err != nil {
		return "", "", err
	}
	if err := asnCandidate.Commit(); err != nil {
		return "", "", err
	}
	return country, asn, nil
}

// stageWhitelist enumerates every file permitted inside an all-in-one stage.
// Anything outside this set (onboarding.json, mihari.yaml, control.token,
// subscriptions/, logs/, web/, ...) must be explicitly approved — design §4.1
// step 6/8.
var stageWhitelist = map[string]bool{
	"mihari": true, "mihari.exe": true,
	"install-aio.sh": true, "install-aio.ps1": true,
	"data/bin/mihomo": true, "data/bin/mihomo.exe": true,
	"data/bin/core-channel":            true,
	"data/geoip/GeoLite2-Country.mmdb": true,
	"data/geoip/GeoLite2-ASN.mmdb":     true,
}

func assertStage(goos string, files []string) error {
	for _, file := range files {
		if !stageWhitelist[file] {
			return fmt.Errorf("bundler: unexpected stage file %q (whitelist approval required)", file)
		}
	}
	suffix, script := "", "install-aio.sh"
	if goos == "windows" {
		suffix, script = ".exe", "install-aio.ps1"
	}
	required := []string{
		"mihari" + suffix,
		script,
		"data/bin/mihomo" + suffix,
		"data/bin/core-channel",
		"data/geoip/GeoLite2-Country.mmdb",
		"data/geoip/GeoLite2-ASN.mmdb",
	}
	seen := make(map[string]bool, len(files))
	for _, file := range files {
		seen[file] = true
	}
	for _, want := range required {
		if !seen[want] {
			return fmt.Errorf("bundler: missing required stage file %q", want)
		}
	}
	return nil
}

func writeCoreChannelSidecar(path, channel string, release core.Release, asset core.Asset) error {
	if channel == "" {
		channel = "stable"
	}
	fingerprint := release.TagName
	if channel == "alpha" {
		fingerprint = core.ParseAlphaSHA(asset.Name)
	}
	if fingerprint == "" {
		return fmt.Errorf("empty core-channel fingerprint for %s asset %q", channel, asset.Name)
	}
	content := channel + "\n" + channel + "-" + fingerprint + "\n"
	return os.WriteFile(path, []byte(content), 0o644)
}

func extractMihomo(archivePath, assetName, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return err
	}
	switch lower := strings.ToLower(assetName); {
	case strings.HasSuffix(lower, ".gz"):
		return extractGzipSingle(archivePath, dest)
	case strings.HasSuffix(lower, ".zip"):
		return extractZipSingle(archivePath, dest)
	default:
		return fmt.Errorf("unsupported mihomo archive %q", assetName)
	}
}

func extractGzipSingle(archivePath, dest string) error {
	archive, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer archive.Close()
	reader, err := gzip.NewReader(archive)
	if err != nil {
		return errors.New("invalid mihomo gzip archive")
	}
	defer reader.Close()
	return writeAll(dest, io.LimitReader(reader, maxMihomoBinary+1), maxMihomoBinary)
}

func extractZipSingle(archivePath, dest string) error {
	archive, err := zip.OpenReader(archivePath)
	if err != nil {
		return errors.New("invalid mihomo zip archive")
	}
	defer archive.Close()
	for _, file := range archive.File {
		base := strings.ToLower(filepath.Base(file.Name))
		if file.FileInfo().IsDir() || !strings.Contains(base, "mihomo") || !strings.HasSuffix(base, ".exe") {
			continue
		}
		source, err := file.Open()
		if err != nil {
			return err
		}
		err = writeAll(dest, io.LimitReader(source, maxMihomoBinary+1), maxMihomoBinary)
		source.Close()
		return err
	}
	return errors.New("mihomo executable is missing from archive")
}

// writeAll copies reader into dest, enforcing a maxBytes ceiling on the result.
func writeAll(dest string, reader io.Reader, maxBytes int64) error {
	file, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o700)
	if err != nil {
		return err
	}
	written, err := io.Copy(file, reader)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if written > maxBytes {
		return errors.New("mihomo executable exceeds size limit")
	}
	return nil
}

// smokeMihomo runs `-v` for the target matching the build host (the only target
// the host can actually execute) and verifies the executable magic number for
// the other platforms — design §4.1 step 2. Matching on runtime.GOOS/GOARCH
// keeps the bundler runnable on any host (CI is linux/amd64, but local dev on
// Windows/macOS now smokes its own platform instead of failing to exec a
// foreign binary).
func smokeMihomo(ctx context.Context, goos, goarch, path string, runner core.CommandRunner) error {
	if goos == runtime.GOOS && goarch == runtime.GOARCH {
		runner := runner
		if runner == nil {
			runner = core.OSCommandRunner{}
		}
		output, err := runner.Run(ctx, path, "-v")
		if err != nil {
			return fmt.Errorf("mihomo -v smoke: %w", err)
		}
		if len(strings.TrimSpace(string(output))) == 0 {
			return errors.New("mihomo -v produced no output")
		}
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return checkMagic(goos, data)
}

func checkMagic(goos string, data []byte) error {
	switch goos {
	case "linux":
		if len(data) >= 4 && bytes.Equal(data[:4], []byte{0x7f, 'E', 'L', 'F'}) {
			return nil
		}
	case "darwin":
		for _, magic := range [][]byte{
			{0xfe, 0xed, 0xfa, 0xce}, // Mach-O 32 big-endian
			{0xfe, 0xed, 0xfa, 0xcf}, // Mach-O 64 big-endian
			{0xce, 0xfa, 0xed, 0xfe}, // Mach-O 32 little-endian
			{0xcf, 0xfa, 0xed, 0xfe}, // Mach-O 64 little-endian (real amd64/arm64)
			{0xca, 0xfe, 0xba, 0xbe}, // Universal/fat binary
		} {
			if len(data) >= 4 && bytes.Equal(data[:4], magic) {
				return nil
			}
		}
	case "windows":
		if len(data) >= 2 && data[0] == 'M' && data[1] == 'Z' {
			return nil
		}
	}
	return fmt.Errorf("mihomo magic number mismatch for %s", goos)
}

func packStage(stage, out, goos, goarch string) error {
	name := "mihari-all-in-one-" + goos + "-" + goarch
	if goos == "windows" {
		return zipStage(stage, filepath.Join(out, name+".zip"))
	}
	return tarStage(stage, filepath.Join(out, name+".tar.gz"))
}

func tarStage(stage, dest string) (err error) {
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()
	gzipWriter := gzip.NewWriter(out)
	defer func() {
		if cerr := gzipWriter.Close(); err == nil {
			err = cerr
		}
	}()
	tarWriter := tar.NewWriter(gzipWriter)
	defer func() {
		if cerr := tarWriter.Close(); err == nil {
			err = cerr
		}
	}()
	return filepath.Walk(stage, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(stage, path)
		if err != nil {
			return err
		}
		header := &tar.Header{Name: filepath.ToSlash(rel), Mode: int64(info.Mode().Perm()), Size: info.Size(), Format: tar.FormatGNU}
		if err := tarWriter.WriteHeader(header); err != nil {
			return err
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		_, err = io.Copy(tarWriter, file)
		file.Close()
		return err
	})
}

func zipStage(stage, dest string) (err error) {
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()
	zipWriter := zip.NewWriter(out)
	defer func() {
		if cerr := zipWriter.Close(); err == nil {
			err = cerr
		}
	}()
	return filepath.Walk(stage, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(stage, path)
		if err != nil {
			return err
		}
		entry, err := zipWriter.Create(filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		_, err = io.Copy(entry, file)
		file.Close()
		return err
	})
}

func copyFile(source, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return err
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	return err
}

func relativeFiles(root string) ([]string, error) {
	var files []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	return files, err
}

func distBinaryName(dir, goos, goarch string) string {
	name := "mihari-" + goos + "-" + goarch
	if goos == "windows" {
		name += ".exe"
	}
	return filepath.Join(dir, name)
}

func splitPlatform(platform string) (string, string, error) {
	parts := strings.SplitN(platform, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid platform %q (want goos/goarch)", platform)
	}
	return parts[0], parts[1], nil
}

func splitPlatforms(csv string) []string {
	var platforms []string
	for raw := range strings.SplitSeq(csv, ",") {
		if platform := strings.TrimSpace(raw); platform != "" {
			platforms = append(platforms, platform)
		}
	}
	return platforms
}

func newClient(token string) *http.Client {
	if token == "" {
		return &http.Client{Timeout: 15 * time.Minute}
	}
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok || base == nil {
		return &http.Client{Timeout: 15 * time.Minute}
	}
	transport := base.Clone()
	return &http.Client{Timeout: 15 * time.Minute, Transport: &bearerTransport{base: transport, token: token}}
}

type bearerTransport struct {
	base  http.RoundTripper
	token string
}

func (t *bearerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	cloned := request.Clone(request.Context())
	cloned.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(cloned)
}
