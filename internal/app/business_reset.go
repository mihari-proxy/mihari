package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/platform"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// BusinessReset owns a fixed list of business files. Call only under daemon mutation ownership.
type BusinessReset struct {
	Paths        platform.Paths
	Reporter     diagnostics.Reporter
	saveSettings func(string, config.Settings) (config.CommitResult, error)
}

var resetCacheName = regexp.MustCompile(`^[0-9a-f]{32}\.yaml$`)

func (b BusinessReset) marker() string { return filepath.Join(b.Paths.Root, "business-reset.state") }

// Pending reports a durable reset or Setup barrier without loading business configuration.
func (b BusinessReset) Pending() (bool, error) {
	if err := b.checkPath(b.marker()); err != nil {
		return false, err
	}
	data, err := os.ReadFile(b.marker())
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if string(data) != "clearing\n" && string(data) != "setup\n" {
		return false, fmt.Errorf("invalid business reset state")
	}
	return true, nil
}

// Resume finishes an already confirmed, interrupted clear before business stores are opened.
func (b BusinessReset) Resume(ctx context.Context) error {
	pending, err := b.Pending()
	if err != nil || !pending {
		return err
	}
	content, err := os.ReadFile(b.marker())
	if err != nil {
		return err
	}
	if string(content) == "clearing\n" {
		_, _, err = b.clear(context.WithoutCancel(ctx), func(e error) {
			_ = diagnostics.ReportError(ctx, b.Reporter, diagnostics.Record{Component: "data-reset", Event: "persist.warning", Level: slog.LevelWarn, Err: e})
		})
	}
	return err
}

// Complete clears the Setup barrier after the user explicitly completes onboarding.
func (b BusinessReset) Complete() error { return os.Remove(b.marker()) }
func (b BusinessReset) checkPath(path string) error {
	root := filepath.Clean(b.Paths.Root)
	if !filepath.IsAbs(root) {
		return fmt.Errorf("reset root must be absolute")
	}
	rel, err := filepath.Rel(root, filepath.Clean(path))
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("reset target outside data root")
	}
	current := root
	parts := append([]string{""}, strings.Split(rel, string(filepath.Separator))...)
	for _, part := range parts {
		if part != "" {
			current = filepath.Join(current, part)
		}
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("reset target contains a link: %s", current)
		}
		if current != filepath.Clean(path) && !info.IsDir() {
			return fmt.Errorf("reset parent is not a directory: %s", current)
		}
		if current == filepath.Clean(path) && !info.Mode().IsRegular() {
			return fmt.Errorf("reset target is not a regular file: %s", current)
		}
	}
	return nil
}
func (b BusinessReset) targets() ([]string, error) {
	p := b.Paths
	targets := []string{p.Settings, p.Onboarding, p.RuntimeConfig, p.TUIPreferences, p.WebActive, p.WebCredential, filepath.Join(p.Root, "cache.db"), filepath.Join(p.Root, "cache.db-shm"), filepath.Join(p.Root, "cache.db-wal"), p.SubscriptionCatalog, filepath.Join(p.Root, "runtime", "core-home", "cache.db"), filepath.Join(p.Root, "runtime", "core-home", "cache.db-shm"), filepath.Join(p.Root, "runtime", "core-home", "cache.db-wal")}
	// Only core-owned subscription cache names are candidates; unknown entries stay untouched.
	if err := b.checkPath(filepath.Join(p.SubscriptionCache, "identity-check")); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(p.SubscriptionCache)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	for _, entry := range entries {
		if resetCacheName.MatchString(entry.Name()) {
			targets = append(targets, filepath.Join(p.SubscriptionCache, entry.Name()))
		}
	}
	sort.Strings(targets)
	for _, path := range targets {
		if err := b.checkPath(path); err != nil {
			return nil, err
		}
	}
	return targets, nil
}

// Preview binds the fixed file list to its current bytes, including absent files.
func (b BusinessReset) Preview(ctx context.Context) (protocol.ResetPreview, error) {
	targets, err := b.targets()
	if err != nil {
		return protocol.ResetPreview{}, err
	}
	hash := sha256.New()
	for _, path := range targets {
		if err := ctx.Err(); err != nil {
			return protocol.ResetPreview{}, err
		}
		_, _ = fmt.Fprintf(hash, "%d:%s:", len(path), path) // SHA-256 writes cannot fail.
		if strings.HasPrefix(filepath.Base(path), "cache.db") {
			hash.Write([]byte("volatile core cache\n"))
			continue
		}
		file, err := os.Open(path)
		if errors.Is(err, os.ErrNotExist) {
			hash.Write([]byte("absent\n"))
			continue
		}
		if err != nil {
			return protocol.ResetPreview{}, err
		}
		info, statErr := file.Stat()
		if statErr != nil {
			_ = file.Close()
			return protocol.ResetPreview{}, statErr
		}
		if info.Size() > 32<<20 {
			_ = file.Close()
			return protocol.ResetPreview{}, fmt.Errorf("reset target too large: %s", path)
		}
		_, _ = fmt.Fprintf(hash, "present:%d:", info.Size()) // SHA-256 writes cannot fail.
		_, copyErr := io.Copy(hash, io.LimitReader(file, 32<<20+1))
		closeErr := file.Close()
		if err := errors.Join(copyErr, closeErr); err != nil {
			return protocol.ResetPreview{}, err
		}
		hash.Write([]byte("\n"))
	}
	return protocol.ResetPreview{Schema: "mihari/v1", Digest: hex.EncodeToString(hash.Sum(nil)), Targets: targets, Preserved: []string{"Mihari program and command", "installed service", "mihomo, panel and GeoIP downloads", "logs and exports", "local control credential", "unknown files"}}, nil
}

// Reset rechecks the plan before the first durable change. Its marker makes interrupted clears restart-safe.
func (b BusinessReset) Reset(ctx context.Context, digest string, warning func(error)) (config.Settings, string, error) {
	preview, err := b.Preview(ctx)
	if err != nil {
		return config.Settings{}, "", err
	}
	if digest == "" || digest != preview.Digest {
		return config.Settings{}, "", protocol.APIError{Code: protocol.CodeRevisionConflict, Message: "business reset preview changed; review a fresh preview"}
	}
	if err := ctx.Err(); err != nil {
		return config.Settings{}, "", err
	}
	if err := b.checkPath(b.marker()); err != nil {
		return config.Settings{}, "", err
	}
	// Reject installed-resource errors before committing the recovery barrier.
	if _, err := b.resetSettings(nil); err != nil {
		return config.Settings{}, "", err
	}
	if err := b.write(b.marker(), []byte("clearing\n"), warning); err != nil {
		return config.Settings{}, "", err
	}
	// Once the marker commits, finish under owner lifetime even when the client disconnects.
	return b.clear(context.WithoutCancel(ctx), warning)
}
func (b BusinessReset) clear(ctx context.Context, warning func(error)) (config.Settings, string, error) {
	targets, err := b.targets()
	if err != nil {
		return config.Settings{}, "", err
	}
	settings, err := b.resetSettings(warning)
	if err != nil {
		return config.Settings{}, "", err
	}
	secret := make([]byte, 64)
	if _, err := rand.Read(secret); err != nil {
		return config.Settings{}, "", err
	}
	settings.ControllerSecret = hex.EncodeToString(secret[:32])
	web := hex.EncodeToString(secret[32:])
	for _, path := range targets {
		if err := b.checkPath(path); err != nil {
			return config.Settings{}, "", err
		}
		if path == b.Paths.Settings || path == b.Paths.WebCredential || path == b.Paths.Onboarding {
			continue
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return config.Settings{}, "", fmt.Errorf("reset interrupted removing %s; rerun data reset: %w", path, err)
		}
	}
	save := b.saveSettings
	if save == nil {
		save = config.SaveWithCommit
	}
	saved, err := save(b.Paths.Settings, settings)
	if err != nil {
		return config.Settings{}, "", err
	}
	if !saved.Committed {
		return config.Settings{}, "", errors.New("reset settings were not committed")
	}
	if saved.Warning != nil && warning != nil {
		warning(saved.Warning)
	}
	if err := b.write(b.Paths.WebCredential, []byte(web+"\n"), warning); err != nil {
		return config.Settings{}, "", err
	}
	if err := b.write(b.Paths.Onboarding, []byte(`{"schema":"mihari.onboarding/v1","complete":false}`), warning); err != nil {
		return config.Settings{}, "", err
	}
	if err := b.write(b.marker(), []byte("setup\n"), warning); err != nil {
		return config.Settings{}, "", err
	}
	return settings, web, nil
}

func (b BusinessReset) resetSettings(warning func(error)) (config.Settings, error) {
	settings := config.Defaults()
	// Channel is installed-resource identity and is preserved with the core binary.
	if old, e := config.Load(b.Paths.Settings); e == nil {
		settings.CoreChannel, settings.CoreChannelBundle = old.CoreChannel, old.CoreChannelBundle
	} else if repaired, e2 := config.LoadForProxyAuthReset(b.Paths.Settings); e2 == nil {
		settings.CoreChannel, settings.CoreChannelBundle = repaired.CoreChannel, repaired.CoreChannelBundle
	} else {
		// Full reset must remain available for malformed business settings. Preserve
		// installed-channel identity from its fixed resource sidecar when available.
		if !errors.Is(e, os.ErrNotExist) && warning != nil {
			warning(fmt.Errorf("resetting unreadable business settings; preserving installed resources: %w", e))
		}
		sidecar := filepath.Join(b.Paths.Bin, "core-channel")
		if err := b.checkPath(sidecar); err != nil {
			return config.Settings{}, err
		}
		if _, err := config.ApplyCoreChannelSidecar(&settings, sidecar); err != nil {
			return config.Settings{}, err
		}
	}
	return settings, nil
}

func (b BusinessReset) write(path string, content []byte, warning func(error)) error {
	result, err := config.AtomicWriteWithCommit(path, content, 0600)
	if err != nil {
		return err
	}
	if !result.Committed {
		return errors.New("reset file was not committed")
	}
	if result.Warning != nil && warning != nil {
		warning(result.Warning)
	}
	return nil
}
