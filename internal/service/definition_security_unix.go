//go:build unix_security && (linux || darwin)

package service

// NewSecurityLaunchdAdapter exposes the real adapter's existing dependency seam
// only to isolated native acceptance fixtures; it never selects host paths.
func NewSecurityLaunchdAdapter(runner CommandRunner, files DefinitionStore, tree ProcessTree, hook ActionHook, paths LaunchdPaths) *LaunchdAdapter {
	return newLaunchdAdapter(launchdConfig{Runner: runner, Files: files, Tree: tree, Hook: hook, Paths: paths})
}
