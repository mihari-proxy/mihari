package app

import (
	"path/filepath"
	"sort"
	"strings"
)

var installationResetEntries = [...]struct{ name, category string }{
	{"mihari.yaml", "config"}, {"onboarding.json", "config"}, {"subscriptions", "subscriptions"},
	{"preferences", "preferences"}, {"runtime", "runtime"}, {"logs", "logs"}, {"logs-export", "logs"},
	{"bin", "binaries"}, {"geoip", "assets"}, {"web", "web"}, {"staging", "staging"},
}

// installationDataEntries constructs a preview from verified root and direct-child observations.
// It grants no filesystem authority: native execution must reacquire and validate each capability.
func installationDataEntries(mode string, manifest InstallationManifest, source *InstallationSourceScope, children []string) (preserve, remove []InstallationEntry, err error) {
	if mode != InstallationModeRepair && mode != InstallationModeFresh || !validAbsPath(manifest.DataRoot) || !validInstallationPath(manifest.DataRoot) || !validAbsPath(manifest.Credential) || !validInstallationPath(manifest.Credential) {
		return nil, nil, invalidInstallationPlan()
	}
	if mode == InstallationModeFresh && source != nil && installationPathsOverlap(manifest.DataRoot, source.DataRoot) {
		return nil, nil, invalidInstallationPlan()
	}
	preserve = make([]InstallationEntry, 0, len(children)+1)
	remove = make([]InstallationEntry, 0, len(installationResetEntries))
	if mode == InstallationModeFresh {
		for _, entry := range installationResetEntries {
			path := filepath.Join(manifest.DataRoot, entry.name)
			if installationPathsOverlap(path, manifest.Credential) {
				return nil, nil, invalidInstallationPlan()
			}
			remove = append(remove, InstallationEntry{Path: path, Category: entry.category})
		}
	}
	for _, name := range children {
		if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00") {
			return nil, nil, invalidInstallationPlan()
		}
		path := filepath.Join(manifest.DataRoot, name)
		if !validInstallationPath(path) {
			return nil, nil, invalidInstallationPlan()
		}
		category := "unknown"
		managed := false
		for _, entry := range installationResetEntries {
			if installationSamePath(path, filepath.Join(manifest.DataRoot, entry.name)) {
				category = entry.category
				managed = true
				break
			}
		}
		if mode == InstallationModeFresh && managed {
			continue
		}
		if installationSamePath(path, manifest.Credential) {
			continue
		}
		for _, prior := range preserve {
			if installationSamePath(prior.Path, path) {
				return nil, nil, invalidInstallationPlan()
			}
		}
		preserve = append(preserve, InstallationEntry{Path: path, Category: category})
	}
	preserve = append(preserve, InstallationEntry{Path: manifest.Credential, Category: "credential"})
	sort.Slice(preserve, func(i, j int) bool { return preserve[i].Path < preserve[j].Path })
	return preserve, remove, nil
}

func installationSamePath(a, b string) bool {
	rel, err := filepath.Rel(a, b)
	return err == nil && rel == "."
}

func installationPathsOverlap(a, b string) bool {
	inside := func(parent, child string) bool {
		rel, err := filepath.Rel(parent, child)
		return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
	}
	return inside(a, b) || inside(b, a)
}
