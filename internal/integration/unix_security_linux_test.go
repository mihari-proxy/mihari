//go:build unix_security && linux

package integration

// Linux native cases share the public control fixture in unix_security_test.go;
// the platform package additionally requires a real private-namespace bind mount.
