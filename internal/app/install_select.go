package app

import (
	"path"
	"strings"

	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/service"
)

func selectInstallSource(req InstallRequest, def service.Definition, layout platform.ResolvedLayout) (string, error) {
	source := req.Source
	if def.Status != service.StatusNotInstalled {
		fixed := ""
		for _, env := range def.Env {
			if value, ok := strings.CutPrefix(env, "MIHARI_DATA="); ok {
				if fixed != "" && fixed != value {
					return "", migrateState("service source is ambiguous")
				}
				fixed = value
			}
		}
		if fixed == "" && def.Binary != path.Join(layout.InstallRoot, "mihari") {
			return "", migrateState("service definition does not identify its data source")
		}
		if source != "" && (fixed == "" || !equalPath(source, fixed)) {
			return "", migrateInvalid("source must match service definition")
		}
		if source == "" {
			source = fixed
			if equalPath(source, layout.Data.Root) {
				source = ""
			}
		}
	}
	if source != "" {
		if !strings.HasPrefix(source, "/") || strings.ContainsRune(source, 0) || nestedMigrationPaths(source, layout.Data.Root) {
			return "", migrateInvalid("source and target must not nest")
		}
		source = path.Clean(source)
	}
	return source, nil
}
