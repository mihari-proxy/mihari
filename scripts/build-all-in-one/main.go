// Command build-all-in-one packs mihari + mihomo core + GeoIP×2 + the local
// install-aio installer into per-platform all-in-one bundles for offline
// distribution. Every external input comes from a reviewed release-input lock;
// the builder never discovers a latest release or a mutable GeoIP ref.
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
	"github.com/mihari-proxy/mihari/scripts/internal/releaseinputs"
)

var defaultPlatforms = releaseinputs.RequiredPlatforms()

const maxMihomoBinary int64 = 256 << 20

const publishCleanupWarning = "old bundle backup cleanup failed; manual cleanup may be required"

var errUnsafeBundleOutput = errors.New("--out must be a dedicated managed bundle directory")

type options struct {
	LockPath   string
	MihariDir  string // dist/ — CI cross-build artifacts (mihari-<os>-<arch>[.exe])
	Out        string // bundles/
	ScriptsDir string // directory holding install-aio.sh / install-aio.ps1 (scripts/install in CI)
	Platforms  []string

	// Test seams (zero-valued in production).
	Context       context.Context
	HTTPClient    *http.Client
	GeoIPValidate func(string) error
	Runner        core.CommandRunner // linux/amd64 `-v` smoke; defaults to OSCommandRunner
	PublishFault  func(operation, path string) error
	WarningSink   func(message string)
	CopyChunkHook func()
}

func main() {
	lockPath := flag.String("lock", "", "reviewed release input lock (required)")
	mihariDir := flag.String("mihari-dir", "dist", "directory with mihari-<os>-<arch>[.exe] build artifacts")
	out := flag.String("out", "bundles", "output directory for all-in-one bundles")
	platforms := flag.String("platforms", strings.Join(defaultPlatforms, ","), "comma-separated goos/goarch targets")
	scriptsDir := flag.String("scripts-dir", "scripts/install", "directory holding install-aio.sh / install-aio.ps1")
	flag.Parse()

	if err := run(options{
		LockPath: *lockPath, MihariDir: *mihariDir, Out: *out, ScriptsDir: *scriptsDir,
		Platforms: splitPlatforms(*platforms),
		WarningSink: func(message string) {
			fmt.Fprintln(os.Stderr, "build-all-in-one: warning:", message)
		},
	}); err != nil {
		fmt.Fprintln(os.Stderr, "build-all-in-one:", err)
		os.Exit(1)
	}
}

func run(o options) error {
	if o.LockPath == "" {
		return errors.New("--lock is required")
	}
	lock, err := releaseinputs.Load(o.LockPath)
	if err != nil {
		return fmt.Errorf("load release input lock: %w", err)
	}
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
	if err := validateOutputIsolation(o.Out, o.LockPath, o.MihariDir, o.ScriptsDir); err != nil {
		return err
	}
	if err := validateRequestedPlatforms(o.Platforms, lock.Mihomo.Assets); err != nil {
		return err
	}
	client := o.HTTPClient
	if client == nil {
		client = newClient()
	}
	client = secureRedirectClient(client)
	installer := core.Installer{HTTPClient: client, Runner: o.Runner}
	ctx := o.Context
	if ctx == nil {
		ctx = context.Background()
	}
	bundleDir, err := os.MkdirTemp("", "aio-bundles-")
	if err != nil {
		return fmt.Errorf("create temporary bundles directory: %w", err)
	}
	defer os.RemoveAll(bundleDir)
	buildOptions := o
	buildOptions.Out = bundleDir

	// GeoIP×2 is shared across all platforms — download once.
	geoipDir, err := os.MkdirTemp("", "aio-geoip-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(geoipDir)
	countryPath, asnPath, err := downloadGeoIP(ctx, client, geoipDir, lock.GeoIP, o.GeoIPValidate)
	if err != nil {
		return fmt.Errorf("download geoip: %w", err)
	}

	for _, platform := range o.Platforms {
		goos, goarch, err := splitPlatform(platform)
		if err != nil {
			return fmt.Errorf("platform %q: %w", platform, err)
		}
		lockedAsset, ok := lock.Mihomo.Assets[platform]
		if !ok {
			return fmt.Errorf("platform %q is not present in release input lock", platform)
		}
		if err := buildPlatform(ctx, installer, lock.Mihomo, lockedAsset, goos, goarch, buildOptions, countryPath, asnPath); err != nil {
			return fmt.Errorf("build %s: %w", platform, err)
		}
	}
	if err := publishBundles(ctx, bundleDir, o.Out, o.Platforms, o.PublishFault, o.WarningSink, o.CopyChunkHook); err != nil {
		return fmt.Errorf("publish bundles: %w", err)
	}
	return nil
}

func validateOutputIsolation(output, lockPath, mihariDir, scriptsDir string) error {
	workingDirectory, err := os.Getwd()
	if err != nil {
		return errUnsafeBundleOutput
	}
	output, err = canonicalOverlapPath(output)
	if err != nil {
		return errUnsafeBundleOutput
	}
	workingDirectory, err = canonicalOverlapPath(workingDirectory)
	if err != nil || pathContains(output, workingDirectory) {
		return errUnsafeBundleOutput
	}
	lockPath, err = canonicalOverlapPath(lockPath)
	if err != nil || pathContains(output, lockPath) {
		return errUnsafeBundleOutput
	}
	for _, inputDirectory := range []string{mihariDir, scriptsDir} {
		inputDirectory, err = canonicalOverlapPath(inputDirectory)
		if err != nil || pathContains(output, inputDirectory) || pathContains(inputDirectory, output) {
			return errUnsafeBundleOutput
		}
	}
	return nil
}

func canonicalOverlapPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	current := filepath.Clean(absolute)
	var missing []string
	for {
		if _, err := os.Lstat(current); err == nil {
			break
		} else if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", errors.New("path has no existing ancestor")
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
	resolved, err := filepath.EvalSymlinks(current)
	if err != nil {
		return "", err
	}
	for index := len(missing) - 1; index >= 0; index-- {
		resolved = filepath.Join(resolved, missing[index])
	}
	return filepath.Clean(resolved), nil
}

func pathContains(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	if err != nil || filepath.IsAbs(relative) {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

func validateRequestedPlatforms(platforms []string, assets map[string]releaseinputs.MihomoAsset) error {
	seen := make(map[string]struct{}, len(platforms))
	for _, platform := range platforms {
		if _, _, err := splitPlatform(platform); err != nil {
			return fmt.Errorf("platform %q: %w", platform, err)
		}
		if _, ok := assets[platform]; !ok {
			return fmt.Errorf("platform %q is not present in release input lock", platform)
		}
		if _, duplicate := seen[platform]; duplicate {
			return fmt.Errorf("platform %q is duplicated", platform)
		}
		seen[platform] = struct{}{}
	}
	return nil
}

func buildPlatform(ctx context.Context, installer core.Installer, inputs releaseinputs.MihomoInputs, lockedAsset releaseinputs.MihomoAsset, goos, goarch string, o options, countryPath, asnPath string) error {
	stage, err := os.MkdirTemp("", "aio-stage-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)

	asset := core.Asset{
		ID: lockedAsset.AssetID, Name: lockedAsset.Name, URL: lockedAsset.URL,
		Size: lockedAsset.Size, Digest: "sha256:" + lockedAsset.SHA256,
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
	if err := extractMihomo(ctx, archivePath, asset.Name, mihomoDest, o.CopyChunkHook); err != nil {
		return err
	}
	if err := writeCoreChannelSidecar(filepath.Join(stage, "data", "bin", "core-channel"), inputs.Channel, inputs.Tag, asset); err != nil {
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
	if err := copyFile(ctx, countryPath, filepath.Join(geoipDest, "GeoLite2-Country.mmdb"), o.CopyChunkHook); err != nil {
		return err
	}
	if err := copyFile(ctx, asnPath, filepath.Join(geoipDest, "GeoLite2-ASN.mmdb"), o.CopyChunkHook); err != nil {
		return err
	}

	mihariName := "mihari"
	if goos == "windows" {
		mihariName = "mihari.exe"
	}
	if err := copyFile(ctx, distBinaryName(o.MihariDir, goos, goarch), filepath.Join(stage, mihariName), o.CopyChunkHook); err != nil {
		return err
	}

	script := "install-aio.sh"
	if goos == "windows" {
		script = "install-aio.ps1"
	}
	if err := copyFile(ctx, filepath.Join(o.ScriptsDir, script), filepath.Join(stage, script), o.CopyChunkHook); err != nil {
		return err
	}

	files, err := relativeFiles(ctx, stage)
	if err != nil {
		return err
	}
	if err := assertStage(goos, files); err != nil {
		return err
	}
	return packStage(ctx, stage, o.Out, goos, goarch, o.CopyChunkHook)
}

// downloadGeoIP fetches the two immutable, digest-locked databases without a
// mutable branch or checksum sidecar request.
func downloadGeoIP(ctx context.Context, client *http.Client, destDir string, inputs releaseinputs.GeoIPInputs, validate func(string) error) (country, asn string, err error) {
	downloader := geoip.Downloader{Client: client, StagingDir: destDir, Validate: validate}

	country = filepath.Join(destDir, "GeoLite2-Country.mmdb")
	countryCandidate, err := downloader.Prepare(ctx, geoip.DownloadSpec{URL: inputs.Country.URL, ExpectedSHA256: inputs.Country.SHA256, Destination: country})
	if err != nil {
		return "", "", err
	}
	if err := countryCandidate.Commit(); err != nil {
		return "", "", err
	}
	asn = filepath.Join(destDir, "GeoLite2-ASN.mmdb")
	asnCandidate, err := downloader.Prepare(ctx, geoip.DownloadSpec{URL: inputs.ASN.URL, ExpectedSHA256: inputs.ASN.SHA256, Destination: asn})
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

func writeCoreChannelSidecar(path, channel, tag string, asset core.Asset) error {
	fingerprint := tag
	if channel == "alpha" {
		fingerprint = core.ParseAlphaSHA(asset.Name)
	}
	if fingerprint == "" {
		return fmt.Errorf("empty core-channel fingerprint for %s asset %q", channel, asset.Name)
	}
	content := channel + "\n" + channel + "-" + fingerprint + "\n"
	return os.WriteFile(path, []byte(content), 0o644)
}

func extractMihomo(ctx context.Context, archivePath, assetName, dest string, chunkHook func()) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return err
	}
	switch lower := strings.ToLower(assetName); {
	case strings.HasSuffix(lower, ".gz"):
		return extractGzipSingle(ctx, archivePath, dest, chunkHook)
	case strings.HasSuffix(lower, ".zip"):
		return extractZipSingle(ctx, archivePath, dest, chunkHook)
	default:
		return fmt.Errorf("unsupported mihomo archive %q", assetName)
	}
}

func extractGzipSingle(ctx context.Context, archivePath, dest string, chunkHook func()) error {
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
	return writeAll(ctx, dest, io.LimitReader(reader, maxMihomoBinary+1), maxMihomoBinary, chunkHook)
}

func extractZipSingle(ctx context.Context, archivePath, dest string, chunkHook func()) error {
	archive, err := zip.OpenReader(archivePath)
	if err != nil {
		return errors.New("invalid mihomo zip archive")
	}
	defer archive.Close()
	for _, file := range archive.File {
		if err := ctx.Err(); err != nil {
			return err
		}
		base := strings.ToLower(filepath.Base(file.Name))
		if file.FileInfo().IsDir() || !strings.Contains(base, "mihomo") || !strings.HasSuffix(base, ".exe") {
			continue
		}
		source, err := file.Open()
		if err != nil {
			return err
		}
		err = writeAll(ctx, dest, io.LimitReader(source, maxMihomoBinary+1), maxMihomoBinary, chunkHook)
		source.Close()
		return err
	}
	return errors.New("mihomo executable is missing from archive")
}

// writeAll copies reader into dest, enforcing a maxBytes ceiling on the result.
func writeAll(ctx context.Context, dest string, reader io.Reader, maxBytes int64, chunkHook func()) error {
	file, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o700)
	if err != nil {
		return err
	}
	written, err := copyWithContext(ctx, file, reader, chunkHook)
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
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := ctx.Err(); err != nil {
		return err
	}
	data := make([]byte, 4)
	read, err := io.ReadFull(file, data)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return checkMagic(goos, data[:read])
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

func packStage(ctx context.Context, stage, out, goos, goarch string, chunkHook func()) error {
	name := bundleName(goos, goarch)
	if goos == "windows" {
		return zipStage(ctx, stage, filepath.Join(out, name), chunkHook)
	}
	return tarStage(ctx, stage, filepath.Join(out, name), chunkHook)
}

func bundleName(goos, goarch string) string {
	name := "mihari-all-in-one-" + goos + "-" + goarch
	if goos == "windows" {
		return name + ".zip"
	}
	return name + ".tar.gz"
}

func publishBundles(ctx context.Context, source, destination string, platforms []string, fault func(operation, path string) error, warningSink func(message string), chunkHook func()) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	sourceInfo, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if sourceInfo.Mode()&os.ModeSymlink != 0 || !sourceInfo.IsDir() {
		return errors.New("temporary bundle source must be a real directory")
	}
	destination, err = filepath.Abs(destination)
	if err != nil {
		return err
	}
	parent := filepath.Dir(destination)
	if parent == destination || filepath.Base(destination) == "." {
		return errors.New("bundle destination must not be a filesystem root")
	}
	createdParents, err := ensureSafePublishParent(ctx, parent)
	if err != nil {
		return err
	}
	publishCommitted := false
	defer func() {
		if !publishCommitted {
			removeCreatedPublishParents(createdParents)
		}
	}()

	stage, err := os.MkdirTemp(parent, ".mihari-aio-publish-")
	if err != nil {
		return err
	}
	stagePending := true
	defer func() {
		if stagePending {
			_ = os.RemoveAll(stage)
		}
	}()

	destinationExists := false
	destinationMode := os.FileMode(0o755)
	var originalDestinationInfo os.FileInfo
	if info, statErr := os.Lstat(destination); statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.New("bundle destination must be a real directory")
		}
		destinationExists = true
		destinationMode = info.Mode().Perm()
		originalDestinationInfo = info
		if err := validateManagedOutput(ctx, destination, platforms); err != nil {
			return err
		}
	} else if !os.IsNotExist(statErr) {
		return statErr
	}

	for _, platform := range platforms {
		if err := ctx.Err(); err != nil {
			return err
		}
		goos, goarch, err := splitPlatform(platform)
		if err != nil {
			return err
		}
		name := bundleName(goos, goarch)
		sourcePath := filepath.Join(source, name)
		info, err := os.Stat(sourcePath)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("temporary bundle %q is not a regular file", name)
		}
		destinationPath := filepath.Join(destination, name)
		stagePath := filepath.Join(stage, name)
		if existing, statErr := os.Lstat(stagePath); statErr == nil && !existing.Mode().IsRegular() {
			return fmt.Errorf("existing output path %q is not a regular file", name)
		} else if statErr != nil && !os.IsNotExist(statErr) {
			return statErr
		}
		if err := runPublishFault(fault, "copy", destinationPath); err != nil {
			return err
		}
		if err := copyRegularFile(ctx, sourcePath, stagePath, info.Mode().Perm(), chunkHook); err != nil {
			return err
		}
		if err := runPublishFault(fault, "chmod", destinationPath); err != nil {
			return err
		}
		if err := os.Chmod(stagePath, info.Mode().Perm()); err != nil {
			return err
		}
	}
	if err := os.Chmod(stage, destinationMode); err != nil {
		return err
	}

	if !destinationExists {
		if _, err := os.Lstat(destination); !os.IsNotExist(err) {
			if err == nil {
				return errors.New("bundle destination appeared during publish")
			}
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := runPublishFault(fault, "replace", destination); err != nil {
			return err
		}
		if err := os.Rename(stage, destination); err != nil {
			return err
		}
		stagePending = false
		publishCommitted = true
		return nil
	}

	backup, err := reservePublishBackup(parent)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	currentDestinationInfo, err := os.Lstat(destination)
	if err != nil {
		return err
	}
	if currentDestinationInfo.Mode()&os.ModeSymlink != 0 || !currentDestinationInfo.IsDir() || !os.SameFile(originalDestinationInfo, currentDestinationInfo) {
		return errors.New("bundle destination changed during publish")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Rename(destination, backup); err != nil {
		return err
	}
	rollback := func(commitErr error) error {
		if err := os.Rename(backup, destination); err != nil {
			return fmt.Errorf("%w; restore original output: %v", commitErr, err)
		}
		return commitErr
	}
	if err := runPublishFault(fault, "replace", destination); err != nil {
		return rollback(err)
	}
	if err := os.Rename(stage, destination); err != nil {
		return rollback(err)
	}
	stagePending = false
	publishCommitted = true
	// The destination swap is the commit point. Old-output cleanup is
	// best-effort: returning an error now would falsely report an uncommitted
	// publish even though the new directory is already visible.
	cleanupErr := runPublishFault(fault, "cleanup", backup)
	if cleanupErr == nil {
		cleanupErr = removePublishBackup(backup)
	}
	if cleanupErr != nil && warningSink != nil {
		warningSink(publishCleanupWarning)
	}
	return nil
}

func ensureSafePublishParent(ctx context.Context, parent string) ([]string, error) {
	var missing []string
	for current := parent; ; current = filepath.Dir(current) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		info, err := os.Lstat(current)
		switch {
		case err == nil:
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
				return nil, errors.New("bundle destination path must not cross a link or non-directory")
			}
		case os.IsNotExist(err):
			missing = append(missing, current)
		default:
			return nil, err
		}
		if filepath.Dir(current) == current {
			break
		}
	}
	created := make([]string, 0, len(missing))
	for index := len(missing) - 1; index >= 0; index-- {
		if err := ctx.Err(); err != nil {
			removeCreatedPublishParents(created)
			return nil, err
		}
		if err := os.Mkdir(missing[index], 0o755); err == nil {
			created = append(created, missing[index])
			continue
		} else if !os.IsExist(err) {
			removeCreatedPublishParents(created)
			return nil, err
		}
		info, err := os.Lstat(missing[index])
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			removeCreatedPublishParents(created)
			return nil, errors.New("bundle destination path changed during creation")
		}
	}
	return created, nil
}

func removeCreatedPublishParents(created []string) {
	for index := len(created) - 1; index >= 0; index-- {
		_ = os.Remove(created[index])
	}
}

func validateManagedOutput(ctx context.Context, destination string, platforms []string) error {
	expected := make(map[string]struct{}, len(platforms))
	for _, platform := range platforms {
		if err := ctx.Err(); err != nil {
			return err
		}
		goos, goarch, err := splitPlatform(platform)
		if err != nil {
			return errors.New("managed bundle output has an invalid platform set")
		}
		expected[bundleName(goos, goarch)] = struct{}{}
	}
	entries, err := os.ReadDir(destination)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, ok := expected[entry.Name()]; !ok {
			return errors.New("managed bundle output contains an unexpected entry")
		}
		info, err := os.Lstat(filepath.Join(destination, entry.Name()))
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("managed bundle output contains a non-regular entry")
		}
	}
	return nil
}

func copyRegularFile(ctx context.Context, source, destination string, mode os.FileMode, chunkHook func()) (resultErr error) {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("publish source must be a regular file")
	}
	if existing, statErr := os.Lstat(destination); statErr == nil && !existing.Mode().IsRegular() {
		return errors.New("publish destination must be a regular file")
	} else if statErr != nil && !os.IsNotExist(statErr) {
		return statErr
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := out.Close(); resultErr == nil {
			resultErr = closeErr
		}
	}()
	if _, err := copyWithContext(ctx, out, in, chunkHook); err != nil {
		return err
	}
	if err := out.Sync(); err != nil {
		return err
	}
	return nil
}

func reservePublishBackup(parent string) (string, error) {
	path, err := os.MkdirTemp(parent, ".mihari-aio-backup-")
	if err != nil {
		return "", err
	}
	if err := os.Remove(path); err != nil {
		_ = os.RemoveAll(path)
		return "", err
	}
	return path, nil
}

func removePublishBackup(path string) error {
	_ = filepath.WalkDir(path, func(current string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if entry.IsDir() {
			_ = os.Chmod(current, 0o700)
		} else {
			_ = os.Chmod(current, 0o600)
		}
		return nil
	})
	return os.RemoveAll(path)
}

func runPublishFault(fault func(operation, path string) error, operation, path string) error {
	if fault == nil {
		return nil
	}
	return fault(operation, path)
}

func tarStage(ctx context.Context, stage, dest string, chunkHook func()) (err error) {
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(dest)
		}
	}()
	defer out.Close()
	gzipWriter := gzip.NewWriter(out)
	gzipWriter.ModTime = time.Unix(0, 0).UTC()
	gzipWriter.OS = 255
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
		if err := ctx.Err(); err != nil {
			return err
		}
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
		archiveName := filepath.ToSlash(rel)
		mode, err := canonicalBundleMode(archiveName)
		if err != nil {
			return err
		}
		header := &tar.Header{
			Name: archiveName, Mode: int64(mode), Size: info.Size(),
			Typeflag: tar.TypeReg, ModTime: time.Unix(0, 0).UTC(), Format: tar.FormatGNU,
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			return err
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		_, err = copyWithContext(ctx, tarWriter, file, chunkHook)
		file.Close()
		return err
	})
}

func zipStage(ctx context.Context, stage, dest string, chunkHook func()) (err error) {
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(dest)
		}
	}()
	defer out.Close()
	zipWriter := zip.NewWriter(out)
	defer func() {
		if cerr := zipWriter.Close(); err == nil {
			err = cerr
		}
	}()
	return filepath.Walk(stage, func(path string, info os.FileInfo, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
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
		archiveName := filepath.ToSlash(rel)
		mode, err := canonicalBundleMode(archiveName)
		if err != nil {
			return err
		}
		header := &zip.FileHeader{Name: archiveName, Method: zip.Deflate}
		header.SetMode(mode)
		header.Modified = time.Date(1980, time.January, 1, 0, 0, 0, 0, time.UTC)
		entry, err := zipWriter.CreateHeader(header)
		if err != nil {
			return err
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		_, err = copyWithContext(ctx, entry, file, chunkHook)
		file.Close()
		return err
	})
}

func canonicalBundleMode(path string) (os.FileMode, error) {
	switch path {
	case "mihari", "mihari.exe", "data/bin/mihomo", "data/bin/mihomo.exe", "install-aio.sh":
		return 0o755, nil
	case "install-aio.ps1", "data/bin/core-channel", "data/geoip/GeoLite2-Country.mmdb", "data/geoip/GeoLite2-ASN.mmdb":
		return 0o644, nil
	default:
		return 0, errors.New("bundle stage contains an entry without a canonical archive mode")
	}
}

func copyFile(ctx context.Context, source, dest string, chunkHook func()) error {
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
	_, err = copyWithContext(ctx, out, in, chunkHook)
	if err == nil {
		err = out.Sync()
	}
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	return err
}

func copyWithContext(ctx context.Context, destination io.Writer, source io.Reader, chunkHook func()) (int64, error) {
	buffer := make([]byte, 64<<10)
	var written int64
	for {
		if err := ctx.Err(); err != nil {
			return written, err
		}
		read, readErr := source.Read(buffer)
		if read > 0 {
			count, writeErr := destination.Write(buffer[:read])
			written += int64(count)
			if writeErr != nil {
				return written, writeErr
			}
			if count != read {
				return written, io.ErrShortWrite
			}
			if chunkHook != nil {
				chunkHook()
			}
			if err := ctx.Err(); err != nil {
				return written, err
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return written, nil
			}
			return written, readErr
		}
	}
}

func relativeFiles(ctx context.Context, root string) ([]string, error) {
	var files []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
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

func newClient() *http.Client {
	return &http.Client{Timeout: 15 * time.Minute}
}

func secureRedirectClient(base *http.Client) *http.Client {
	client := *base
	previous := client.CheckRedirect
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if request.URL.Scheme != "https" || request.URL.User != nil {
			return errors.New("release input redirect must use HTTPS without credentials")
		}
		if previous != nil {
			return previous(request, via)
		}
		if len(via) >= 10 {
			return errors.New("release input redirect limit exceeded")
		}
		return nil
	}
	return &client
}
