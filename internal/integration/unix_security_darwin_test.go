//go:build unix_security && darwin

package integration

// Darwin uses the same actual-UID public control fixture and native LOCAL_PEERCRED.
// ACL ABI, REALFSID and local-filesystem proof reside in the platform native cases.
