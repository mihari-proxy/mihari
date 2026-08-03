package geoip

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	maxminddb "github.com/oschwald/maxminddb-golang/v2"
)

const defaultMaxDatabaseBytes int64 = 128 << 20

// DownloadSpec identifies one database, its checksum, and its local destination.
type DownloadSpec struct {
	URL         string
	ChecksumURL string
	Destination string
}

// Downloader prepares bounded, checksum-verified MMDB candidates.
type Downloader struct {
	Client     *http.Client
	StagingDir string
	MaxBytes   int64
	AllowHTTP  bool
	Validate   func(string) error
}

// FileCandidate is one validated file awaiting activation.
type FileCandidate struct {
	staged      string
	destination string
	digest      [sha256.Size]byte
	committed   bool
	hadPrevious bool
}

// Prepare downloads, verifies, and stages one database without changing the active file.
func (d Downloader) Prepare(ctx context.Context, spec DownloadSpec) (_ *FileCandidate, resultErr error) {
	if err := validateDownloadURL(spec.URL, d.AllowHTTP); err != nil {
		return nil, err
	}
	if err := validateDownloadURL(spec.ChecksumURL, d.AllowHTTP); err != nil {
		return nil, err
	}
	if spec.Destination == "" || d.StagingDir == "" {
		return nil, errors.New("geoip download paths are required")
	}
	client := d.Client
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Minute}
	}
	want, err := downloadChecksum(ctx, client, spec.ChecksumURL, d.AllowHTTP)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(d.StagingDir, 0o700); err != nil {
		return nil, fmt.Errorf("create geoip staging directory: %w", err)
	}
	file, err := os.CreateTemp(d.StagingDir, ".candidate-*.mmdb")
	if err != nil {
		return nil, fmt.Errorf("create geoip candidate: %w", err)
	}
	staged := file.Name()
	defer func() {
		_ = file.Close()
		if resultErr != nil {
			_ = os.Remove(staged)
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		return nil, fmt.Errorf("protect geoip candidate: %w", err)
	}
	limit := d.MaxBytes
	if limit <= 0 {
		limit = defaultMaxDatabaseBytes
	}
	got, err := downloadFile(ctx, client, spec.URL, file, limit, d.AllowHTTP)
	if err != nil {
		return nil, err
	}
	if got != want {
		return nil, errors.New("geoip candidate checksum mismatch")
	}
	if err := file.Sync(); err != nil {
		return nil, fmt.Errorf("sync geoip candidate: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("close geoip candidate: %w", err)
	}
	validate := d.Validate
	if validate == nil {
		validate = validateMMDB
	}
	if err := validate(staged); err != nil {
		return nil, fmt.Errorf("validate geoip candidate: %w", err)
	}
	return &FileCandidate{staged: staged, destination: spec.Destination, digest: got}, nil
}

func validateDownloadURL(raw string, allowHTTP bool) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && !(allowHTTP && parsed.Scheme == "http")) {
		return errors.New("geoip download URL must use HTTPS")
	}
	return nil
}

func downloadChecksum(ctx context.Context, client *http.Client, rawURL string, allowHTTP bool) ([sha256.Size]byte, error) {
	var result [sha256.Size]byte
	response, err := doGET(ctx, client, rawURL, allowHTTP)
	if err != nil {
		return result, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 4097))
	if err != nil || len(raw) > 4096 {
		return result, errors.New("read geoip checksum")
	}
	fields := strings.Fields(string(raw))
	if len(fields) == 0 {
		return result, errors.New("invalid geoip checksum")
	}
	decoded, err := hex.DecodeString(fields[0])
	if err != nil || len(decoded) != sha256.Size {
		return result, errors.New("invalid geoip checksum")
	}
	copy(result[:], decoded)
	return result, nil
}

func downloadFile(ctx context.Context, client *http.Client, rawURL string, destination io.Writer, maxBytes int64, allowHTTP bool) ([sha256.Size]byte, error) {
	var result [sha256.Size]byte
	response, err := doGET(ctx, client, rawURL, allowHTTP)
	if err != nil {
		return result, err
	}
	defer response.Body.Close()
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(destination, hash), io.LimitReader(response.Body, maxBytes+1))
	if err != nil {
		return result, fmt.Errorf("download geoip database: %w", err)
	}
	if written > maxBytes {
		return result, errors.New("geoip database exceeds size limit")
	}
	copy(result[:], hash.Sum(nil))
	return result, nil
}

func doGET(ctx context.Context, client *http.Client, rawURL string, allowHTTP bool) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create geoip request: %w", err)
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("download geoip resource: %w", err)
	}
	if err := validateDownloadURL(response.Request.URL.String(), allowHTTP); err != nil {
		response.Body.Close()
		return nil, errors.New("geoip redirect must preserve HTTPS")
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		response.Body.Close()
		return nil, fmt.Errorf("download geoip resource: unexpected HTTP status %d", response.StatusCode)
	}
	return response, nil
}

func validateMMDB(path string) error {
	reader, err := maxminddb.Open(path)
	if err != nil {
		return err
	}
	defer reader.Close()
	return reader.Verify()
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
		_ = os.Rename(previous, c.destination)
		return fmt.Errorf("activate geoip database: %w", err)
	}
	c.committed = true
	c.staged = ""
	if err := syncDirectory(filepath.Dir(c.destination)); err != nil {
		_ = c.Rollback()
		return fmt.Errorf("sync geoip directory: %w", err)
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
		_ = os.Remove(c.staged)
		c.staged = ""
	}
}
