package geoip

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mihari-proxy/mihari/internal/diagnostics"
	maxminddb "github.com/oschwald/maxminddb-golang/v2"
)

const defaultMaxDatabaseBytes int64 = 128 << 20

var errInvalidExpectedSHA256 = errors.New("expected geoip SHA-256 must be 64 lowercase hexadecimal characters")

// DownloadSpec identifies one database, its checksum, and its local destination.
type DownloadSpec struct {
	URL            string
	ChecksumURL    string
	ExpectedSHA256 string
	Destination    string
}

// Downloader prepares bounded, checksum-verified MMDB candidates.
type Downloader struct {
	Client     *http.Client
	StagingDir string
	MaxBytes   int64
	AllowHTTP  bool
	Validate   func(string) error
	// Reporter borrows an existing file diagnostic outlet; nil skips warnings.
	Reporter diagnostics.Reporter
}

// FileCandidate is one validated file awaiting activation.
type FileCandidate struct {
	staged      string
	destination string
	digest      [sha256.Size]byte
	committed   bool
	hadPrevious bool
	reporter    diagnostics.Reporter
}

// Prepare downloads, verifies, and stages one database without changing the active file.
func (d Downloader) Prepare(ctx context.Context, spec DownloadSpec) (_ *FileCandidate, resultErr error) {
	if err := validateDownloadURL(spec.URL, d.AllowHTTP); err != nil {
		return nil, err
	}
	if (spec.ChecksumURL == "") == (spec.ExpectedSHA256 == "") {
		return nil, errors.New("exactly one geoip checksum source is required")
	}
	var want [sha256.Size]byte
	if spec.ChecksumURL != "" {
		if err := validateDownloadURL(spec.ChecksumURL, d.AllowHTTP); err != nil {
			return nil, err
		}
	}
	if spec.ExpectedSHA256 != "" {
		var err error
		want, err = parseExpectedSHA256(spec.ExpectedSHA256)
		if err != nil {
			return nil, err
		}
	}
	if spec.Destination == "" || d.StagingDir == "" {
		return nil, errors.New("geoip download paths are required")
	}
	client := d.Client
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Minute}
	}
	if spec.ChecksumURL != "" {
		var err error
		want, err = downloadChecksum(ctx, client, spec.ChecksumURL, d.AllowHTTP, d.Reporter)
		if err != nil {
			return nil, err
		}
	}
	if err := os.MkdirAll(d.StagingDir, 0o700); err != nil {
		return nil, fmt.Errorf("create geoip staging directory: %w", err)
	}
	file, err := os.CreateTemp(d.StagingDir, ".candidate-*.mmdb")
	if err != nil {
		return nil, fmt.Errorf("create geoip candidate: %w", err)
	}
	staged := file.Name()
	closed := false
	defer func() {
		if !closed {
			resultErr = errors.Join(resultErr, file.Close())
		}
		if resultErr != nil {
			if err := os.Remove(staged); err != nil && !errors.Is(err, os.ErrNotExist) {
				resultErr = errors.Join(resultErr, fmt.Errorf("remove geoip candidate: %w", err))
			}
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		return nil, fmt.Errorf("protect geoip candidate: %w", err)
	}
	limit := d.MaxBytes
	if limit <= 0 {
		limit = defaultMaxDatabaseBytes
	}
	got, err := downloadFile(ctx, client, spec.URL, file, limit, d.AllowHTTP, d.Reporter)
	if err != nil {
		return nil, err
	}
	if got != want {
		return nil, downloadFailure{message: "geoip candidate checksum mismatch"}
	}
	if err := file.Sync(); err != nil {
		return nil, fmt.Errorf("sync geoip candidate: %w", err)
	}
	if err := file.Close(); err != nil {
		closed = true
		return nil, fmt.Errorf("close geoip candidate: %w", err)
	}
	closed = true
	validate := d.Validate
	if validate == nil {
		validate = validateMMDB
	}
	if err := validate(staged); err != nil {
		return nil, downloadFailure{message: "geoip candidate failed database validation", cause: err}
	}
	return &FileCandidate{staged: staged, destination: spec.Destination, digest: got, reporter: d.Reporter}, nil
}

func parseExpectedSHA256(raw string) ([sha256.Size]byte, error) {
	var result [sha256.Size]byte
	if len(raw) != sha256.Size*2 {
		return result, errInvalidExpectedSHA256
	}
	if raw != strings.ToLower(raw) {
		return result, errInvalidExpectedSHA256
	}
	decoded, err := hex.DecodeString(raw)
	if err != nil {
		return result, errInvalidExpectedSHA256
	}
	copy(result[:], decoded)
	return result, nil
}

func validateDownloadURL(raw string, allowHTTP bool) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && !(allowHTTP && parsed.Scheme == "http")) {
		return errors.New("geoip download URL must use HTTPS")
	}
	return nil
}

func downloadChecksum(ctx context.Context, client *http.Client, rawURL string, allowHTTP bool, reporter diagnostics.Reporter) (result [sha256.Size]byte, resultErr error) {
	response, err := doGET(ctx, client, rawURL, allowHTTP)
	if err != nil {
		return result, err
	}
	defer closeGeoIPResponse(ctx, response, rawURL, &resultErr, reporter)
	raw, err := io.ReadAll(io.LimitReader(response.Body, 4097))
	if err != nil {
		return result, downloadFailure{message: "read geoip checksum", cause: &diagnostics.HTTPError{Operation: "geoip GET checksum", URL: rawURL, Phase: "read", Status: response.StatusCode, Cause: err}}
	}
	if len(raw) > 4096 {
		return result, errors.New("read geoip checksum")
	}
	fields := strings.Fields(string(raw))
	if len(fields) == 0 {
		return result, errors.New("invalid geoip checksum")
	}
	decoded, err := hex.DecodeString(fields[0])
	if err != nil {
		return result, downloadFailure{message: "invalid geoip checksum", cause: err}
	}
	if len(decoded) != sha256.Size {
		return result, errors.New("invalid geoip checksum")
	}
	copy(result[:], decoded)
	return result, nil
}

// downloadFile streams a size-bounded resource while computing its SHA-256 digest.
func downloadFile(ctx context.Context, client *http.Client, rawURL string, destination io.Writer, maxBytes int64, allowHTTP bool, reporter diagnostics.Reporter) (result [sha256.Size]byte, resultErr error) {
	response, err := doGET(ctx, client, rawURL, allowHTTP)
	if err != nil {
		return result, err
	}
	defer closeGeoIPResponse(ctx, response, rawURL, &resultErr, reporter)
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(destination, hash), io.LimitReader(response.Body, maxBytes+1))
	if err != nil {
		return result, fmt.Errorf("download geoip database: %w", err)
	}
	if written > maxBytes {
		return result, downloadFailure{message: "geoip database exceeds size limit"}
	}
	copy(result[:], hash.Sum(nil))
	return result, nil
}

// doGET checks response status and redirect transport policy; callers close a successful response.
func doGET(ctx context.Context, client *http.Client, rawURL string, allowHTTP bool) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create geoip request: %w", err)
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, downloadFailure{message: "download geoip resource", cause: &diagnostics.HTTPError{Operation: "geoip GET resource", URL: rawURL, Phase: "transport", Cause: err}}
	}
	if err := validateDownloadURL(response.Request.URL.String(), allowHTTP); err != nil {
		closeErr := response.Body.Close()
		return nil, downloadFailure{message: "geoip redirect must preserve HTTPS", cause: &diagnostics.HTTPError{Operation: "geoip GET resource", URL: response.Request.URL.String(), Phase: "redirect", Status: response.StatusCode, Cause: errors.Join(err, closeErr)}}
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		raw, readErr := io.ReadAll(io.LimitReader(response.Body, diagnostics.MaxHTTPBodyBytes+1))
		closeErr := response.Body.Close()
		return nil, downloadFailure{message: fmt.Sprintf("download geoip resource: unexpected HTTP status %d", response.StatusCode), cause: &diagnostics.HTTPError{Operation: "geoip GET resource", URL: rawURL, Phase: "response", Status: response.StatusCode, Body: diagnostics.HTTPBody(raw), Cause: errors.Join(readErr, closeErr)}}
	}
	return response, nil
}

func closeGeoIPResponse(ctx context.Context, response *http.Response, rawURL string, resultErr *error, reporter diagnostics.Reporter) {
	if err := response.Body.Close(); err != nil {
		detail := &diagnostics.HTTPError{Operation: "geoip GET resource", URL: rawURL, Phase: "close", Status: response.StatusCode, Cause: err}
		if *resultErr != nil {
			*resultErr = downloadFailure{message: (*resultErr).Error(), cause: errors.Join(*resultErr, detail)}
		} else if reporter != nil {
			reporter(ctx, diagnostics.Record{Component: "geoip", Event: "http.close.failed", Level: slog.LevelWarn, Err: detail})
		}
	}
}

func validateMMDB(path string) error {
	reader, err := maxminddb.Open(path)
	if err != nil {
		return err
	}
	defer reader.Close()
	return reader.Verify()
}

// ValidateMMDBFile verifies a MaxMind database with the downloader's validator.
func ValidateMMDBFile(path string) error {
	return validateMMDB(path)
}

// MatchSHA256 reports whether data matches an expected lowercase hex digest.
func MatchSHA256(data []byte, expected string) error {
	want, err := parseExpectedSHA256(expected)
	if err != nil {
		return err
	}
	got := sha256.Sum256(data)
	if got != want {
		return errors.New("geoip candidate checksum mismatch")
	}
	return nil
}

// Commit activates the candidate while retaining the previous file.
func (c *FileCandidate) Commit() error {
	if c == nil || c.staged == "" || c.destination == "" {
		return errors.New("invalid geoip candidate")
	}
	if err := os.MkdirAll(filepath.Dir(c.destination), 0o700); err != nil {
		return fmt.Errorf("create geoip directory: %w", err)
	}
	previous := c.destination + ".previous"
	if err := os.Remove(previous); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove retained geoip database: %w", err)
	}
	if _, err := os.Stat(c.destination); err == nil {
		c.hadPrevious = true
		if err := os.Rename(c.destination, previous); err != nil {
			return fmt.Errorf("retain previous geoip database: %w", err)
		}
	}
	if err := os.Rename(c.staged, c.destination); err != nil {
		restoreErr := os.Rename(previous, c.destination)
		return joinUpdateRecovery(fmt.Errorf("activate geoip database: %w", err), restoreErr)
	}
	c.committed = true
	c.staged = ""
	if err := syncDirectory(filepath.Dir(c.destination)); err != nil {
		restoreErr := c.Rollback()
		return joinUpdateRecovery(fmt.Errorf("sync geoip directory: %w", err), restoreErr)
	}
	return nil
}

// Valid reports whether the staged bytes still match the prepared digest.
func (c *FileCandidate) Valid() bool {
	if c == nil || c.staged == "" {
		return false
	}
	file, err := os.Open(c.staged)
	if err != nil {
		return false
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return false
	}
	return string(hash.Sum(nil)) == string(c.digest[:])
}

// Rollback restores the retained file after a partial pair commit.
func (c *FileCandidate) Rollback() error {
	if c == nil || !c.committed {
		return nil
	}
	if err := os.Remove(c.destination); err != nil && !os.IsNotExist(err) {
		return err
	}
	if c.hadPrevious {
		if err := os.Rename(c.destination+".previous", c.destination); err != nil {
			return err
		}
	}
	c.committed = false
	return syncDirectory(filepath.Dir(c.destination))
}

// Cleanup removes an uncommitted staged file.
func (c *FileCandidate) Cleanup() {
	if c != nil && c.staged != "" {
		if err := os.Remove(c.staged); err != nil && !errors.Is(err, os.ErrNotExist) && c.reporter != nil {
			c.reporter(context.Background(), diagnostics.Record{Component: "geoip", Event: "candidate.cleanup.failed", Level: slog.LevelWarn, Err: fmt.Errorf("remove geoip candidate %s: %w", c.staged, err)})
		}
		c.staged = ""
	}
}
