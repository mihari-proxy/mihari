package platform

import (
	"fmt"
	"os"
)

// ErrUnsafeComponent identifies a non-directory, symlink or multiply linked file.
// It also matches os.ErrPermission for control API permission-denied mapping.
var ErrUnsafeComponent = fmt.Errorf("unsafe filesystem component: %w", os.ErrPermission)
