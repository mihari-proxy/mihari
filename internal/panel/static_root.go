package panel

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// UIPathPrefix is the URL mount prefix for concurrent panel static trees.
// Each installed panel is available at /__mihari/panels/{panelID}/ independently of the default active panel.
// The prefix is under /__mihari/ so it never collides with mihomo controller routes such as /ui.
const UIPathPrefix = "/__mihari/panels"

// UIMount returns the absolute URL path prefix for panelID (no trailing slash).
func UIMount(panelID string) string {
	panelID = strings.Trim(panelID, "/")
	if panelID == "" {
		return UIPathPrefix
	}
	return UIPathPrefix + "/" + panelID
}

// ResolveFileRoot finds the directory that should be served for a panel build tree.
// Preference order: index.html at root, dist/index.html, then the shallowest nested index.html.
// Returns empty string when no index.html is present.
func ResolveFileRoot(buildDir string) string {
	if buildDir == "" {
		return ""
	}
	if hasIndexHTML(buildDir) {
		return buildDir
	}
	dist := filepath.Join(buildDir, "dist")
	if hasIndexHTML(dist) {
		return dist
	}
	var found string
	_ = filepath.WalkDir(buildDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if !strings.EqualFold(d.Name(), "index.html") {
			return nil
		}
		found = filepath.Dir(path)
		return fs.SkipAll
	})
	return found
}

// hoistSingleRootDir lifts a sole top-level directory (common GitHub zipball layout)
// so index.html ends up at the build root when possible.
func hoistSingleRootDir(destDir string) error {
	entries, err := os.ReadDir(destDir)
	if err != nil {
		return err
	}
	var onlyDir string
	for _, entry := range entries {
		if entry.IsDir() {
			if onlyDir != "" {
				return nil
			}
			onlyDir = entry.Name()
			continue
		}
		// Any top-level file means the archive already has a usable root.
		return nil
	}
	if onlyDir == "" {
		return nil
	}
	nested := filepath.Join(destDir, onlyDir)
	// Leave conventional web roots alone (zashboard ships as dist/...).
	// Hoist GitHub zipball wrappers (e.g. metacubexd-<sha>/) that place index.html inside.
	switch strings.ToLower(onlyDir) {
	case "dist", "build", "public", "www", "static", "assets":
		return nil
	}
	if !hasIndexHTML(nested) {
		return nil
	}
	children, err := os.ReadDir(nested)
	if err != nil {
		return err
	}
	for _, child := range children {
		src := filepath.Join(nested, child.Name())
		dst := filepath.Join(destDir, child.Name())
		if err := os.Rename(src, dst); err != nil {
			return err
		}
	}
	return os.RemoveAll(nested)
}

func hasIndexHTML(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, "index.html"))
	return err == nil && !info.IsDir()
}
