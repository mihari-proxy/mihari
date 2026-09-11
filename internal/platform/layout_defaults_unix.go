//go:build !unix_security && (linux || darwin)

package platform

func platformLayoutDefaults(home string) LayoutDefaults { return nativeLayoutDefaults(home) }
