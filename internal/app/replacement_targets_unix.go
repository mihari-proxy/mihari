//go:build linux || darwin

package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/service"
	"github.com/mihari-proxy/mihari/internal/update"
)

// ObserveReplacement discovers only the files replaced by this installer's update.
// It inspects the service before accessing any installation root.
func (i *UnixInstaller) ObserveReplacement(ctx context.Context, binary string) (update.ReplacementSnapshot, error) {
	if filepath.Clean(binary) != i.binary {
		return update.ReplacementSnapshot{}, invalidInstallRequest()
	}
	def, err := i.adapter(nil).InspectDefinition(ctx)
	if err != nil {
		return update.ReplacementSnapshot{}, err
	}
	req := i.request(InstallOperationUpdate, "", "", i.channel)
	if def.Status != service.StatusNotInstalled {
		if err := checkUnixReplacementPending(ctx, i.layout); err != nil {
			return update.ReplacementSnapshot{}, err
		}
	}
	return observeUnixReplacementTargets(ctx, req, i.layout, i.binary, def, nil)
}

// bound is used only under the existing lease: identity and content are reread,
// while versions remain attached to the originally observed file identity/hash.
func observeUnixReplacementTargets(ctx context.Context, req InstallRequest, layout platform.ResolvedLayout, binary string, def service.Definition, bound *update.ReplacementSnapshot) (update.ReplacementSnapshot, error) {
	switch def.Status {
	case service.StatusNotInstalled, service.StatusStopped, service.StatusRunning:
	default:
		return update.ReplacementSnapshot{}, installBusy("service status is unknown")
	}
	snapshot := update.ReplacementSnapshot{}
	normalized := def
	normalized.Status = ""
	normalized.Running = false
	normalized.Process = service.ProcessIdentity{}
	normalized.Files = append([]service.DefinitionFile(nil), def.Files...)
	normalized.Links = append([]service.DefinitionLink(nil), def.Links...)
	slices.SortFunc(normalized.Files, func(a, b service.DefinitionFile) int { return strings.Compare(a.Path, b.Path) })
	slices.SortFunc(normalized.Links, func(a, b service.DefinitionLink) int { return strings.Compare(a.Path, b.Path) })
	raw, err := json.Marshal(struct {
		Installed  bool
		Definition service.Definition
	}{def.Status != service.StatusNotInstalled, normalized})
	if err != nil {
		return snapshot, err
	}
	snapshot.ServiceDefinitionSHA256 = sha256HexBytes(raw)
	paths := map[string][]string{}
	add := func(path, role string) { path = filepath.Clean(path); paths[path] = append(paths[path], role) }
	if req.Operation == InstallOperationUpdate && def.Status == service.StatusNotInstalled {
		add(binary, "binary")
	} else {
		add(filepath.Join(layout.InstallRoot, "mihari"), "managed")
		if req.PathBinary != "" {
			add(req.PathBinary, "path")
		}
	}
	for path, roles := range paths {
		if def.Status != service.StatusNotInstalled && filepath.Clean(def.Binary) == path {
			roles = append(roles, "service")
		}
		var target update.ReplacementTarget
		if bound == nil {
			target, err = update.ObserveReplacementTarget(ctx, roles[0], path, update.ExecVersionRunner{})
		} else {
			var file platform.ReplacementFile
			file, err = platform.ObserveReplacementFile(ctx, path)
			target = update.ReplacementTarget{Path: file.Path, FileID: file.FileID, SHA256: file.SHA256, Exists: file.Exists}
			for _, previous := range bound.Targets {
				if previous.Path == target.Path && previous.FileID == target.FileID && previous.SHA256 == target.SHA256 && previous.Exists == target.Exists {
					target.Version = previous.Version
					break
				}
			}
		}
		if err != nil {
			return snapshot, err
		}
		target.Roles = roles
		snapshot.Targets = append(snapshot.Targets, target)
	}
	// Reuse the shared canonicalization for stable target/role ordering.
	p, err := update.NewReplacementPreview(update.ReplacementCandidate{}, snapshot)
	return p.Snapshot, err
}

func rejectPendingReplacement(ctx context.Context, store *InstallJournalStore) error {
	object, err := store.files.inspect(ctx, installJournalFileName)
	if err != nil {
		return err
	}
	if !object.Present {
		return nil
	}
	journal, err := store.Load(ctx)
	if err != nil {
		return err
	}
	if journal.Phase != InstallPhaseComplete {
		return installBusy("installation recovery required before replacement")
	}
	return nil
}

func checkUnixReplacementPending(ctx context.Context, layout platform.ResolvedLayout) error {
	if err := checkUnixReplacementPendingRoot(ctx, platform.SystemLayoutDefaults().BaseDir, 0711); err != nil {
		return err
	}
	if layout.Mode == platform.PrivateMode {
		return checkUnixReplacementPendingRoot(ctx, layout.Data.Root, 0700)
	}
	return nil
}
func checkUnixReplacementPendingRoot(ctx context.Context, path string, mode uint32) (err error) {
	root, err := platform.OpenTrustedRoot(ctx, path, platform.RootPolicy{Owner: 0, Mode: mode})
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	store, err := NewInstallJournalStore(ctx, root)
	if err != nil {
		return err
	}
	return rejectPendingReplacement(ctx, store)
}
