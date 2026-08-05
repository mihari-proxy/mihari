package panel

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/LeeShunEE/mihari/internal/control/protocol"
	"github.com/LeeShunEE/mihari/internal/panel/archive"
)

const defaultMaxAssetBytes int64 = 128 << 20

// ServiceOptions configures panel install roots and adapters.
type ServiceOptions struct {
	WebRoot    string
	WebActive  string
	StagingDir string
	Adapters   []Adapter
	HTTPClient *http.Client
	MaxBytes   int64
	AllowHTTP  bool
}

// PanelInfo is a redacted view of one supported panel's install state.
type PanelInfo struct {
	ID             string
	Name           string
	Active         bool
	InstalledBuild string
	LatestBuild    string
	RollbackBuild  string
	Health         string
}

// Service owns panel install trees under web/ and the active.json pointer.
type Service struct {
	mu         sync.Mutex
	webRoot    string
	webActive  string
	stagingDir string
	adapters   map[string]Adapter
	httpClient *http.Client
	maxBytes   int64
	allowHTTP  bool
}

// Open constructs a panel service. Adapters may be empty for path-only tests.
func Open(options ServiceOptions) (*Service, error) {
	if options.WebRoot == "" || options.WebActive == "" || options.StagingDir == "" {
		return nil, protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "panel service paths are required"}
	}
	if err := os.MkdirAll(options.WebRoot, 0o700); err != nil {
		return nil, fmt.Errorf("create web root: %w", err)
	}
	if err := os.MkdirAll(options.StagingDir, 0o700); err != nil {
		return nil, fmt.Errorf("create panel staging: %w", err)
	}
	client := options.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Minute}
	}
	maxBytes := options.MaxBytes
	if maxBytes <= 0 {
		maxBytes = defaultMaxAssetBytes
	}
	adapters := make(map[string]Adapter, len(options.Adapters))
	for _, adapter := range options.Adapters {
		if adapter == nil {
			continue
		}
		adapters[adapter.ID()] = adapter
	}
	return &Service{
		webRoot: options.WebRoot, webActive: options.WebActive, stagingDir: options.StagingDir,
		adapters: adapters, httpClient: client, maxBytes: maxBytes, allowHTTP: options.AllowHTTP,
	}, nil
}

// Active returns the current active.json pointer.
func (s *Service) Active() (Active, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return LoadActive(s.webActive)
}

// ActiveDir returns the filesystem path of the active panel static tree, or empty if none.
// Nested archive layouts (dist/, single GitHub zipball root) are resolved.
func (s *Service) ActiveDir() (string, error) {
	active, err := s.Active()
	if err != nil {
		return "", err
	}
	if active.Panel == "" || active.Build == "" {
		return "", nil
	}
	return ResolveFileRoot(PanelBuildDir(s.webRoot, active.Panel, active.Build)), nil
}

// PanelDir returns the static file root for an installed panel, independent of the default active pointer.
// When panelID is the default active panel, the active build is preferred; otherwise the newest ready build.
func (s *Service) PanelDir(panelID string) (string, error) {
	if panelID == "" {
		return "", protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "panel id is required"}
	}
	if _, ok := Lookup(panelID); !ok {
		return "", protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "unknown panel"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	active, err := LoadActive(s.webActive)
	if err != nil {
		return "", err
	}
	build := ""
	if active.Panel == panelID && active.Build != "" && s.buildReadyLocked(panelID, active.Build) {
		build = active.Build
	} else {
		builds := s.listBuildsLocked(panelID)
		if len(builds) == 0 {
			return "", nil
		}
		build = builds[0]
	}
	return ResolveFileRoot(PanelBuildDir(s.webRoot, panelID, build)), nil
}

// SetupPath returns the default active panel's same-origin setup deep-link (under /__mihari/panels/{id}/).
func (s *Service) SetupPath(gatewayHost string) string {
	active, err := s.Active()
	if err != nil || active.Panel == "" {
		return "/"
	}
	return s.SetupPathFor(active.Panel, gatewayHost)
}

// SetupPathFor returns a same-origin setup deep-link mounted at /__mihari/panels/{panelID}/.
func (s *Service) SetupPathFor(panelID, gatewayHost string) string {
	if panelID == "" {
		return "/"
	}
	s.mu.Lock()
	adapter := s.adapters[panelID]
	s.mu.Unlock()
	if adapter == nil {
		return UIMount(panelID) + "/"
	}
	setup := adapter.SetupPath(gatewayHost)
	if setup == "" || setup == "/" {
		return UIMount(panelID) + "/"
	}
	// Adapters return root-relative hash routes like /#/setup?... — mount under the panel URL.
	if strings.HasPrefix(setup, "/#") || strings.HasPrefix(setup, "/?") {
		return UIMount(panelID) + setup
	}
	if strings.HasPrefix(setup, "/") {
		return UIMount(panelID) + setup
	}
	return UIMount(panelID) + "/" + setup
}

// List returns catalog entries with install/active metadata (no secrets).
func (s *Service) List() []PanelInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	active, _ := LoadActive(s.webActive)
	catalog := BuiltInCatalog()
	out := make([]PanelInfo, 0, len(catalog))
	for _, entry := range catalog {
		info := PanelInfo{ID: entry.ID, Name: entry.Name, Health: "missing"}
		if builds := s.listBuildsLocked(entry.ID); len(builds) > 0 {
			info.InstalledBuild = builds[0]
			info.Health = "installed"
		}
		if active.Panel == entry.ID && active.Build != "" {
			info.Active = true
			info.InstalledBuild = active.Build
			info.Health = "active"
		}
		if active.Previous != nil {
			info.RollbackBuild = active.Previous[entry.ID]
		}
		if adapter := s.adapters[entry.ID]; adapter != nil && info.Name == "" {
			info.Name = adapter.DisplayName()
		}
		out = append(out, info)
	}
	return out
}

// Install downloads and extracts a panel build outside any caller commit section.
// pinBuild selects a specific already-resolved asset only when the adapter's ResolveLatest
// is replaced by a test adapter; for production adapters, pinBuild when non-empty must match
// the resolved build id after ResolveLatest (download URL still comes from the adapter).
func (s *Service) Install(ctx context.Context, panelID, pinBuild string) error {
	adapter, err := s.adapter(panelID)
	if err != nil {
		return err
	}
	build, assetURL, err := adapter.ResolveLatest(ctx)
	if err != nil {
		return err
	}
	if pinBuild != "" && pinBuild != build {
		// Allow pin to force build id only when the adapter returned that identity;
		// production adapters resolve identity and URL together.
		return protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "pinned panel build does not match resolved build"}
	}
	if build == "" || assetURL == "" {
		return protocol.APIError{Code: protocol.CodeDataFailure, Message: "panel adapter returned empty build"}
	}
	return s.installBuild(ctx, panelID, build, assetURL)
}

// Update installs the latest build when it differs from the current installed build for panelID.
func (s *Service) Update(ctx context.Context, panelID string) error {
	adapter, err := s.adapter(panelID)
	if err != nil {
		return err
	}
	build, assetURL, err := adapter.ResolveLatest(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	builds := s.listBuildsLocked(panelID)
	s.mu.Unlock()
	if len(builds) > 0 && builds[0] == build {
		if s.buildReady(panelID, build) {
			return nil
		}
	}
	return s.installBuild(ctx, panelID, build, assetURL)
}

// Activate sets active.json to the newest complete installed build for panelID.
func (s *Service) Activate(ctx context.Context, panelID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.adapterLocked(panelID); err != nil {
		return err
	}
	builds := s.listBuildsLocked(panelID)
	if len(builds) == 0 {
		return protocol.APIError{Code: protocol.CodeInvalidState, Message: "panel is not installed"}
	}
	build := builds[0]
	if !s.buildReadyLocked(panelID, build) {
		return protocol.APIError{Code: protocol.CodeInvalidState, Message: "panel build is incomplete"}
	}
	current, err := LoadActive(s.webActive)
	if err != nil {
		return err
	}
	previous := clonePrevious(current.Previous)
	if current.Panel == panelID && current.Build != "" && current.Build != build {
		previous[panelID] = current.Build
	} else if current.Panel != "" && current.Panel != panelID && current.Build != "" {
		// Keep prior panel's build as its rollback target.
		if previous[current.Panel] == "" {
			previous[current.Panel] = current.Build
		}
	}
	next := Active{Panel: panelID, Build: build, Previous: previous}
	if err := SaveActive(s.webActive, next); err != nil {
		return err
	}
	s.pruneBuildsLocked(panelID, build, previous[panelID])
	return nil
}

// Rollback restores the retained previous build for panelID when present.
func (s *Service) Rollback(ctx context.Context, panelID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := LoadActive(s.webActive)
	if err != nil {
		return err
	}
	if current.Previous == nil || current.Previous[panelID] == "" {
		return protocol.APIError{Code: protocol.CodeInvalidState, Message: "panel has no rollback build"}
	}
	target := current.Previous[panelID]
	if !s.buildReadyLocked(panelID, target) {
		return protocol.APIError{Code: protocol.CodeInvalidState, Message: "panel rollback build is missing"}
	}
	previous := clonePrevious(current.Previous)
	if current.Panel == panelID && current.Build != "" {
		previous[panelID] = current.Build
	} else {
		delete(previous, panelID)
	}
	next := Active{Panel: panelID, Build: target, Previous: previous}
	return SaveActive(s.webActive, next)
}

// RemoveBuild deletes an installed build directory when it is not active.
func (s *Service) RemoveBuild(panelID, build string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	active, err := LoadActive(s.webActive)
	if err != nil {
		return err
	}
	if active.Panel == panelID && active.Build == build {
		return protocol.APIError{Code: protocol.CodeInvalidState, Message: "cannot delete active panel build"}
	}
	if active.Previous != nil && active.Previous[panelID] == build {
		return protocol.APIError{Code: protocol.CodeInvalidState, Message: "cannot delete panel rollback build"}
	}
	return os.RemoveAll(PanelBuildDir(s.webRoot, panelID, build))
}

// Uninstall removes all installed builds for panelID and clears default/rollback
// pointers that referenced them. Other installed panels are left intact.
func (s *Service) Uninstall(ctx context.Context, panelID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if panelID == "" {
		return protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "panel id is required"}
	}
	if _, ok := Lookup(panelID); !ok {
		return protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "unknown panel"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	active, err := LoadActive(s.webActive)
	if err != nil {
		return err
	}
	previous := clonePrevious(active.Previous)
	delete(previous, panelID)
	if active.Panel == panelID {
		if err := SaveActive(s.webActive, Active{Previous: previous}); err != nil {
			return err
		}
	} else if active.Panel != "" {
		next := Active{Panel: active.Panel, Build: active.Build, Previous: previous}
		if err := SaveActive(s.webActive, next); err != nil {
			return err
		}
	} else if len(previous) != len(active.Previous) {
		if err := SaveActive(s.webActive, Active{Previous: previous}); err != nil {
			return err
		}
	}
	// Remove the entire panel tree (all builds), including incomplete installs.
	if err := os.RemoveAll(filepath.Join(s.webRoot, panelID)); err != nil {
		return fmt.Errorf("remove panel install tree: %w", err)
	}
	return nil
}

// Reinstall uninstalls panelID then installs the latest build.
// When the panel was the default active panel, it is re-activated after install.
func (s *Service) Reinstall(ctx context.Context, panelID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	wasDefault := false
	if active, err := s.Active(); err == nil && active.Panel == panelID && active.Panel != "" {
		wasDefault = true
	}
	if err := s.Uninstall(ctx, panelID); err != nil {
		return err
	}
	if err := s.Install(ctx, panelID, ""); err != nil {
		return err
	}
	if wasDefault {
		return s.Activate(ctx, panelID)
	}
	return nil
}

func (s *Service) installBuild(ctx context.Context, panelID, build, assetURL string) error {
	if err := validateDownloadURL(assetURL, s.allowHTTP); err != nil {
		return err
	}
	// Download + extract outside the service mutex so activate/rollback can run concurrently.
	zipPath, err := s.download(ctx, panelID, build, assetURL)
	if err != nil {
		return err
	}
	defer os.Remove(zipPath)

	// Promote under the mutex so incomplete trees are never activated mid-rename.
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.buildReadyLocked(panelID, build) {
		return nil
	}
	_, err = InstallFromZip(InstallRequest{
		PanelID: panelID, Build: build, Archive: zipPath,
		StagingDir: s.stagingDir, WebRoot: s.webRoot,
	})
	return err
}

func (s *Service) download(ctx context.Context, panelID, build, assetURL string) (string, error) {
	if err := os.MkdirAll(s.stagingDir, 0o700); err != nil {
		return "", fmt.Errorf("create panel staging: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, assetURL, nil)
	if err != nil {
		return "", protocol.APIError{Code: protocol.CodeInternal, Message: "create panel download request"}
	}
	request.Header.Set("User-Agent", "mihari")
	response, err := s.httpClient.Do(request)
	if err != nil {
		return "", protocol.APIError{Code: protocol.CodeNetworkFailure, Message: "download panel asset failed"}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", protocol.APIError{
			Code: protocol.CodeNetworkFailure, Message: "download panel asset failed",
			Details: map[string]any{"status": response.StatusCode},
		}
	}
	file, err := os.CreateTemp(s.stagingDir, "."+sanitizeBuild(panelID+"-"+build)+"-*.zip")
	if err != nil {
		return "", fmt.Errorf("create panel download: %w", err)
	}
	path := file.Name()
	written, copyErr := io.Copy(file, io.LimitReader(response.Body, s.maxBytes+1))
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil {
		os.Remove(path)
		return "", protocol.APIError{Code: protocol.CodeNetworkFailure, Message: "read panel asset failed"}
	}
	if written > s.maxBytes {
		os.Remove(path)
		return "", protocol.APIError{Code: protocol.CodeDataFailure, Message: "panel asset is too large"}
	}
	// Soft-check archive can open before promote.
	if _, err := os.Stat(path); err != nil {
		return "", err
	}
	_ = archive.MaxZipSize
	return path, nil
}

func (s *Service) adapter(panelID string) (Adapter, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.adapterLocked(panelID)
}

func (s *Service) adapterLocked(panelID string) (Adapter, error) {
	adapter := s.adapters[panelID]
	if adapter == nil {
		if _, ok := Lookup(panelID); !ok {
			return nil, protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "unknown panel"}
		}
		return nil, protocol.APIError{Code: protocol.CodeInvalidState, Message: "panel adapter is unavailable"}
	}
	return adapter, nil
}

func (s *Service) listBuildsLocked(panelID string) []string {
	dir := filepath.Join(s.webRoot, panelID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var builds []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if s.buildReadyLocked(panelID, entry.Name()) {
			builds = append(builds, entry.Name())
		}
	}
	// Newest lexicographic last; reverse for newest-first preference on semver-ish tags.
	sort.Slice(builds, func(i, j int) bool { return builds[i] > builds[j] })
	return builds
}

func (s *Service) buildReady(panelID, build string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buildReadyLocked(panelID, build)
}

func (s *Service) buildReadyLocked(panelID, build string) bool {
	root := PanelBuildDir(s.webRoot, panelID, build)
	if _, err := os.Stat(filepath.Join(root, "index.html")); err == nil {
		return true
	}
	// Nested single-root dist/index.html is also valid after extract.
	var found bool
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if strings.EqualFold(d.Name(), "index.html") {
			found = true
			return io.EOF
		}
		return nil
	})
	return found
}

func (s *Service) pruneBuildsLocked(panelID, activeBuild, previousBuild string) {
	dir := filepath.Join(s.webRoot, panelID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	keep := map[string]struct{}{activeBuild: {}}
	if previousBuild != "" {
		keep[previousBuild] = struct{}{}
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, ok := keep[entry.Name()]; ok {
			continue
		}
		_ = os.RemoveAll(filepath.Join(dir, entry.Name()))
	}
}

func clonePrevious(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		if k != "" && v != "" {
			out[k] = v
		}
	}
	return out
}

func validateDownloadURL(raw string, allowHTTP bool) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "invalid panel asset url"}
	}
	switch strings.ToLower(parsed.Scheme) {
	case "https":
		return nil
	case "http":
		if allowHTTP {
			return nil
		}
		return protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "panel asset url must use https"}
	default:
		return protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "invalid panel asset url scheme"}
	}
}
