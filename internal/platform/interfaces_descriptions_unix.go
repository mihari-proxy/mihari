//go:build !windows

package platform

import "context"

func interfaceDescriptions(context.Context) (map[string]string, error) {
	return nil, nil
}
